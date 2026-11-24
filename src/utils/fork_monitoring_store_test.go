package utils

// Fork addition (plan 51): tests of fork_monitoring_store.go. A PostgreSQL
// that cannot be the store leaves monitoring on the local file, said once an
// outage lasted; the watcher tries again with a back-off, switches back only
// after steady answers and a stay; a failed switch never closes the local
// file; a panic in a look does not stop the watcher; swaps lose no writer.
//
// The live tests need a real PostgreSQL: COSMOS_FORK_PG_TEST (host:port),
// COSMOS_FORK_PG_TEST_DB/_USER/_PASSWORD (default cosmos/cosmos/cosmos-test-pw)
// for an account that owns its database, and two more roles made by the test
// setup: "reader" (password reader-test-pw, may sign in, may not create
// tables) and "other" (password other-test-pw, may create tables in that
// database's public schema). scripts/fork-ui-check-monitoring-db.sh makes them.

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// The watcher (started by the first InitMetricsDatabase of any test) is held
// for the whole test process: the store's tests drive the looks themselves,
// and the other tests change the configuration folder under it.
func init() { storeWatchPaused.Store(true) }

type storeStub struct {
	mu       sync.Mutex
	problem  *PostgresProblem
	probes   int
	down     []MonitoringStoreState
	back     []MonitoringStoreState
	resolved int
	ended    []string
	now      time.Time
}

// waitCatchUps waits for the catch-ups handed over after switches (they run
// after the drain, in the background).
func waitCatchUps(t *testing.T) {
	t.Helper()
	for i := 0; i < 400 && catchUpsRunning.Load() > 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if n := catchUpsRunning.Load(); n > 0 {
		t.Fatalf("%d catch-ups still running", n)
	}
}

func (s *storeStub) endedWhy() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ended...)
}

func (s *storeStub) clock() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

func (s *storeStub) advance(d time.Duration) {
	s.mu.Lock()
	s.now = s.now.Add(d)
	s.mu.Unlock()
}

func (s *storeStub) setProblem(p *PostgresProblem) {
	s.mu.Lock()
	s.problem = p
	s.mu.Unlock()
}

func (s *storeStub) counts() (int, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.down), len(s.back), s.resolved
}

func resetStoreState() {
	setStoreState(func(s *MonitoringStoreState) { *s = MonitoringStoreState{Configured: "sqlite"} })
	storeStateMu.Lock()
	storeTold, storeOnLocal, storeOutages = false, time.Time{}, nil
	// not the start of the process: a test that wants one says so (each
	// test's clock starts at the same time, so a boot grace left by an
	// earlier test would cover the next one)
	storeStarted, storeBootUntil, storeLocalProblem = true, time.Time{}, ""
	storeStateMu.Unlock()
	adviceKey = ""
}

func withMonitoringStore(t *testing.T, db DatabaseConfig, problem *PostgresProblem) *storeStub {
	t.Helper()
	stub := &storeStub{problem: problem, now: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)}
	storeWatchPaused.Store(true) // the test's own looks only
	prevFolder, prevConfig := CONFIGFOLDER, GetBaseMainConfig()
	prevProbe, prevDown, prevBack, prevResolve, prevEnded := probeMonitoringPostgres, monitoringTellDown, monitoringTellBack, monitoringResolveDown, tellMonitoringEnded
	prevClock, prevAdvice, prevOpen, prevSchema, prevPing := monitoringClock, monitoringAdviceFor, openPostgresStore, prepareStoreSchema, pingMonitoringStore
	prevTiming := timing()
	CloseMetricsDatabase()
	CONFIGFOLDER = t.TempDir() + "/"
	config := prevConfig
	config.Database = db
	LoadBaseMainConfig(config)
	monitoringClock = stub.clock
	probeMonitoringPostgres = func(ctx context.Context, d DatabaseConfig) *PostgresProblem {
		stub.mu.Lock()
		defer stub.mu.Unlock()
		stub.probes++
		if stub.problem == nil {
			return nil
		}
		p := *stub.problem
		return &p
	}
	monitoringTellDown = func(s MonitoringStoreState) { stub.mu.Lock(); stub.down = append(stub.down, s); stub.mu.Unlock() }
	monitoringTellBack = func(s MonitoringStoreState, since time.Time, _ CatchUpResult) {
		stub.mu.Lock()
		stub.back = append(stub.back, s)
		stub.mu.Unlock()
	}
	monitoringResolveDown = func() { stub.mu.Lock(); stub.resolved++; stub.mu.Unlock() }
	tellMonitoringEnded = func(why string) { stub.mu.Lock(); stub.ended = append(stub.ended, why); stub.mu.Unlock() }
	monitoringAdviceFor = func(string, string) string { return "" }
	setTiming(func(*storeTimes) { storeTiming.drain = 50 * time.Millisecond })
	resetStoreState()
	t.Cleanup(func() {
		waitCatchUps(t) // a catch-up handed over reads the folder and the hooks
		CloseMetricsDatabase()
		CONFIGFOLDER = prevFolder
		LoadBaseMainConfig(prevConfig)
		probeMonitoringPostgres, monitoringTellDown, monitoringTellBack, monitoringResolveDown, tellMonitoringEnded = prevProbe, prevDown, prevBack, prevResolve, prevEnded
		monitoringClock, monitoringAdviceFor, openPostgresStore, prepareStoreSchema, pingMonitoringStore = prevClock, prevAdvice, prevOpen, prevSchema, prevPing
		setTiming(func(t *storeTimes) { *t = prevTiming })
		resetStoreState()
		storeWatchPaused.Store(true)
	})
	return stub
}

// fakePostgres makes "PostgreSQL" a second SQLite file with no schema step:
// the state machine is tested without a server (the live tests have one).
func fakePostgres(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	openPostgresStore = func(DatabaseConfig) (*sql.DB, *sql.DB, error) {
		dsn := "file:" + dir + "/pg.db?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
		w, err := sql.Open(DialectSQLite, dsn)
		if err != nil {
			return nil, nil, err
		}
		w.SetMaxOpenConns(1)
		r, _ := sql.Open(DialectSQLite, dsn)
		if err := initMetricsSchema(w, DialectSQLite); err != nil {
			return nil, nil, err
		}
		return w, r, nil
	}
	prepareStoreSchema = func(*sql.DB, DatabaseConfig) error { return nil }
}

var refusedProblem = &PostgresProblem{"closed", "Nothing answers at localhost:5432: check the address and the port, and that the database runs."}

// The owner's state: PostgreSQL configured where nothing answers. The store
// is the local file, it records, and the state says why; the notice comes
// once the outage lasted.
func TestBrokenPostgresLeavesMonitoringOnTheLocalFile(t *testing.T) {
	stub := withMonitoringStore(t, DatabaseConfig{PostgresHost: "localhost:5432", PostgresDatabase: "cosmos", PostgresUsername: "cosmos", PostgresPassword: "x"}, refusedProblem)
	if err := InitMetricsDatabase(); err != nil {
		t.Fatalf("the opening failed: %v", err)
	}
	if MonitoringDialect() != DialectSQLite {
		t.Fatalf("dialect %s", MonitoringDialect())
	}
	db, err := MonitoringWriteDB()
	if err != nil {
		t.Fatalf("no store: %v", err)
	}
	if _, err := db.Exec("INSERT INTO monitoring_kv(key, value) VALUES('t', 'v')"); err != nil {
		t.Fatalf("the local file does not record: %v", err)
	}
	s := GetMonitoringStoreState()
	if !s.Fallback || s.Configured != "postgres" || s.InUse != DialectSQLite || s.Host != "localhost:5432" || s.Reason != "closed" || s.Since.IsZero() {
		t.Fatalf("state %+v", s)
	}
	if !strings.Contains(MonitoringStoreSentence(s), "does not answer since") {
		t.Errorf("sentence %q", MonitoringStoreSentence(s))
	}
	if GetMainConfig().Database.PostgresHost != "localhost:5432" {
		t.Error("the settings were changed: the fallback must not touch them")
	}
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	w.look(true)
	if down, _, _ := stub.counts(); down != 0 {
		t.Fatalf("told before the outage lasted")
	}
	stub.advance(storeTiming.tellAfter)
	w.look(false)
	w.look(false)
	InitMetricsDatabase() // an unrelated reopening is the same outage
	w.look(false)
	w.look(true)
	if len(stub.down) != 1 || stub.down[0].Host != "localhost:5432" {
		t.Errorf("told %+v", stub.down)
	}
	if same, _ := MonitoringWriteDB(); same != db {
		t.Error("the local file was opened again although it was the store")
	}
}

// Working local-file settings: nothing is probed, nothing is told.
func TestLocalFileStoreProbesNothing(t *testing.T) {
	stub := withMonitoringStore(t, DatabaseConfig{}, refusedProblem)
	if err := InitMetricsDatabase(); err != nil {
		t.Fatal(err)
	}
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	if wait := w.look(true); wait != storeTiming.idle {
		t.Errorf("wait %v", wait)
	}
	if stub.probes != 0 || len(stub.down) != 0 || GetMonitoringStoreState().Fallback {
		t.Errorf("probes %d, told %v, %+v", stub.probes, stub.down, GetMonitoringStoreState())
	}
}

// The back-off doubles up to its cap and the state says when the next try is.
func TestFallbackRetriesWithABackOff(t *testing.T) {
	withMonitoringStore(t, DatabaseConfig{PostgresHost: "192.0.2.10:5432", PostgresUsername: "cosmos"}, refusedProblem)
	InitMetricsDatabase()
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	if wait := w.look(true); wait != storeTiming.retryFirst {
		t.Errorf("first wait %v", wait)
	}
	want := []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 160 * time.Second, 5 * time.Minute, 5 * time.Minute}
	for i, expected := range want {
		if wait := w.look(false); wait != expected {
			t.Errorf("try %d: wait %v, want %v", i, wait, expected)
		}
	}
	if s := GetMonitoringStoreState(); s.Attempts < len(want) {
		t.Errorf("state %+v", s)
	}
	if wait := w.look(true); wait != storeTiming.retryFirst {
		t.Errorf("after a change: %v", wait)
	}
}

// S1 of the review: PostgreSQL answers the probe but its schema cannot be
// made. The local file stays open (the same handle: never closed for a
// switch that does not happen), the back-off grows on this path too, the
// outage is told once and its thread never resolved in between.
func TestFailedSwitchKeepsTheLocalFileAndBacksOff(t *testing.T) {
	stub := withMonitoringStore(t, DatabaseConfig{PostgresHost: "192.0.2.10:5432", PostgresUsername: "cosmos", PostgresPassword: "x"}, refusedProblem)
	fakePostgres(t)
	attempts := 0
	prepareStoreSchema = func(*sql.DB, DatabaseConfig) error {
		attempts++
		return &pgconn.PgError{Code: "42501", Message: "must be owner of table notifications"}
	}
	InitMetricsDatabase()
	local, _ := MonitoringWriteDB()
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	w.look(true)
	stub.setProblem(nil) // it answers the probe from now on
	failedWaits := []time.Duration{}
	var elapsed time.Duration
	for elapsed < 3*time.Hour {
		before := attempts
		wait := w.look(false)
		if attempts > before {
			failedWaits = append(failedWaits, wait)
		}
		stub.advance(wait)
		elapsed += wait
	}
	if now, _ := MonitoringWriteDB(); now != local {
		t.Fatal("the local file was closed or replaced by a failed switch")
	}
	if _, err := local.Exec("INSERT INTO monitoring_kv(key, value) VALUES('still', 'here')"); err != nil {
		t.Fatalf("the local file stopped recording: %v", err)
	}
	s := GetMonitoringStoreState()
	if !s.Fallback || s.Reason != "schema" || !strings.Contains(s.Message, "permission denied") {
		t.Errorf("state %+v", s)
	}
	// 3 hours: the tries thin out to one every 5 minutes at most, plus the
	// steady answers each one waits for
	if attempts > 40 || len(failedWaits) < 3 || failedWaits[len(failedWaits)-1] != storeTiming.retryMax {
		t.Errorf("%d switch attempts in 3 hours, waits after them %v", attempts, failedWaits)
	}
	for i := 1; i < len(failedWaits); i++ {
		if failedWaits[i] < failedWaits[i-1] {
			t.Errorf("the back-off shrank: %v", failedWaits)
		}
	}
	if down, back, resolved := stub.counts(); down != 1 || resolved != 0 || back != 0 {
		t.Errorf("down %d, resolved %d, back %d", down, resolved, back)
	}
}

// S2 of the review: short outages switch the store but tell nothing, and the
// way back waits for steady answers and a stay; a long outage is told once
// and closed once.
func TestHysteresis(t *testing.T) {
	stub := withMonitoringStore(t, DatabaseConfig{PostgresHost: "192.0.2.10:5432", PostgresUsername: "cosmos", PostgresPassword: "x"}, nil)
	fakePostgres(t)
	pingOK := atomic.Bool{}
	pingOK.Store(true)
	pingMonitoringStore = func(context.Context) error {
		if pingOK.Load() {
			return nil
		}
		return errors.New("connection lost")
	}
	if err := InitMetricsDatabase(); err != nil || MonitoringDialect() != DialectPostgres {
		t.Fatalf("PostgreSQL first: %v %s", err, MonitoringDialect())
	}
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	switches := 0
	dialect := MonitoringDialect()
	look := func() time.Duration {
		wait := w.look(false)
		if d := MonitoringDialect(); d != dialect {
			switches++
			dialect = d
		}
		return wait
	}
	for outage := 0; outage < 5; outage++ {
		// 20 s down: two failed pings, the local file takes over
		pingOK.Store(false)
		stub.setProblem(refusedProblem)
		look()
		stub.advance(5 * time.Second)
		look()
		if MonitoringDialect() != DialectSQLite {
			t.Fatalf("outage %d: not on the local file", outage)
		}
		stub.advance(15 * time.Second)
		// back: the probe answers at once, the switch waits
		pingOK.Store(true)
		stub.setProblem(nil)
		var waited time.Duration
		for MonitoringDialect() != DialectPostgres && waited < time.Hour {
			wait := look()
			stub.advance(wait)
			waited += wait
		}
		if waited < storeTiming.goodSpan {
			t.Errorf("outage %d: switched back after %v", outage, waited)
		}
		look()
	}
	if down, back, _ := stub.counts(); down != 0 || back != 0 {
		t.Errorf("short outages told: %d down, %d back", down, back)
	}
	if switches != 10 {
		t.Errorf("%d switches for 5 outages", switches)
	}

	// a long outage: one notice, one "answers again", resolved once
	pingOK.Store(false)
	stub.setProblem(refusedProblem)
	look()
	look()
	for i := 0; i < 20; i++ {
		stub.advance(30 * time.Second)
		look()
	}
	pingOK.Store(true)
	stub.setProblem(nil)
	for i := 0; i < 200 && MonitoringDialect() != DialectPostgres; i++ {
		stub.advance(look())
	}
	waitCatchUps(t)
	if down, back, resolved := stub.counts(); down != 1 || back != 1 || resolved != 1 {
		t.Errorf("long outage: %d down, %d back, %d resolved", down, back, resolved)
	}
}

// The stay on the local file grows with the outages of the last hour.
func TestStayGrowsWithRepeatedOutages(t *testing.T) {
	stub := withMonitoringStore(t, DatabaseConfig{PostgresHost: "192.0.2.10:5432", PostgresUsername: "cosmos", PostgresPassword: "x"}, nil)
	fakePostgres(t)
	ping := atomic.Bool{}
	pingMonitoringStore = func(context.Context) error {
		if ping.Load() {
			return nil
		}
		return errors.New("lost")
	}
	InitMetricsDatabase()
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	stays := []time.Duration{}
	for outage := 0; outage < 4; outage++ {
		ping.Store(false)
		stub.setProblem(refusedProblem)
		w.look(false)
		w.look(false)
		ping.Store(true)
		stub.setProblem(nil)
		var waited time.Duration
		for MonitoringDialect() != DialectPostgres && waited < 2*time.Hour {
			wait := w.look(false)
			stub.advance(wait)
			waited += wait
		}
		stays = append(stays, waited)
	}
	if !(stays[1] > stays[0] && stays[2] > stays[1] && stays[3] > stays[2]) {
		t.Errorf("stays %v", stays)
	}
}

// A writer that took the handle just before a swap still completes: the old
// handles are closed only after a drain.
func TestSwapLosesNoWriter(t *testing.T) {
	withMonitoringStore(t, DatabaseConfig{PostgresHost: "192.0.2.10:5432", PostgresUsername: "cosmos", PostgresPassword: "x"}, nil)
	fakePostgres(t)
	setTiming(func(*storeTimes) { storeTiming.drain = 300 * time.Millisecond })
	InitMetricsDatabase()
	var closed, done atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				db, err := MonitoringWriteDB()
				if err != nil {
					closed.Add(1)
					continue
				}
				if _, err := db.Exec("INSERT INTO monitoring_kv(key, value) VALUES('w', 'v') ON CONFLICT(key) DO UPDATE SET value = excluded.value"); err != nil {
					if strings.Contains(err.Error(), "database is closed") {
						closed.Add(1)
					}
				} else {
					done.Add(1)
				}
			}
		}()
	}
	var refuse atomic.Bool
	probeMonitoringPostgres = func(context.Context, DatabaseConfig) *PostgresProblem {
		if refuse.Load() {
			return refusedProblem
		}
		return nil
	}
	for i := 0; i < 10; i++ {
		refuse.Store(true)
		InitMetricsDatabase() // to the local file
		time.Sleep(20 * time.Millisecond)
		refuse.Store(false)
		InitMetricsDatabase() // to "PostgreSQL"
		time.Sleep(20 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
	if closed.Load() != 0 || done.Load() == 0 {
		t.Errorf("%d writes failed on a closed store, %d done", closed.Load(), done.Load())
	}
}

// S5 of the review: a panic in a look is recovered, logged once, and the
// watcher goes on.
func TestPanicInALookDoesNotStopTheWatcher(t *testing.T) {
	stub := withMonitoringStore(t, DatabaseConfig{PostgresHost: "192.0.2.10:5432", PostgresUsername: "cosmos"}, refusedProblem)
	InitMetricsDatabase()
	calls := 0
	monitoringAdviceFor = func(string, string) string {
		calls++
		if calls <= 2 {
			panic("docker said something odd")
		}
		return ""
	}
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	if wait := w.safeLook(true); wait != storeTiming.retryFirst {
		t.Errorf("wait after a panic %v", wait)
	}
	w.safeLook(false)
	w.safeLook(false)
	if calls != 3 || stub.probes < 1 || w.panicked == "" {
		t.Errorf("the watcher did not go on: %d advice calls, %d probes", calls, stub.probes)
	}
}

// The ping path (a surviving mutation of the review): PostgreSQL in use that
// stops answering two pings in a row moves the store to the local file.
func TestFailedPingsMoveTheStore(t *testing.T) {
	stub := withMonitoringStore(t, DatabaseConfig{PostgresHost: "192.0.2.10:5432", PostgresUsername: "cosmos", PostgresPassword: "x"}, nil)
	fakePostgres(t)
	InitMetricsDatabase()
	pings := 0
	pingMonitoringStore = func(context.Context) error { pings++; return errors.New("lost") }
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	w.look(false)
	if MonitoringDialect() != DialectPostgres {
		t.Fatal("one failed ping moved the store")
	}
	stub.setProblem(refusedProblem)
	w.look(false)
	if MonitoringDialect() != DialectSQLite || !GetMonitoringStoreState().Fallback || pings != 2 {
		t.Errorf("after two failed pings: %s %+v (%d pings)", MonitoringDialect(), GetMonitoringStoreState(), pings)
	}
}

// One watcher for the process however often the store is opened.
func TestNoGoroutinePerOpening(t *testing.T) {
	withMonitoringStore(t, DatabaseConfig{PostgresHost: "192.0.2.10:5432", PostgresUsername: "cosmos"}, refusedProblem)
	InitMetricsDatabase()
	time.Sleep(200 * time.Millisecond)
	before := runtime.NumGoroutine()
	for i := 0; i < 30; i++ {
		InitMetricsDatabase()
	}
	time.Sleep(300 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+3 {
		t.Errorf("goroutines %d → %d", before, after)
	}
}

// The owner's later case: the store at a container's address on Docker's
// default bridge, the container made again elsewhere. The advice names the
// address the container has now, asked again until one answers.
func TestFallbackNamesTheLikelyRightServer(t *testing.T) {
	stub := withMonitoringStore(t, DatabaseConfig{PostgresHost: "172.17.0.2:5432", PostgresUsername: "cosmos"},
		&PostgresProblem{"network", "The database server could not be reached."})
	asked := 0
	monitoringAdviceFor = func(host string, reason string) string {
		asked++
		if host != "172.17.0.2:5432" || reason != "network" || asked == 1 {
			return "" // the first time the container still starts
		}
		return "PostgreSQL runs on this server in the container postgresql: use 192.168.1.10:5432 (the PostgreSQL container postgresql)."
	}
	InitMetricsDatabase()
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	w.look(true)
	if s := GetMonitoringStoreState(); s.Advice != "" {
		t.Fatalf("advice before anything answers: %+v", s)
	}
	stub.advance(storeTiming.tellAfter)
	w.look(false)
	s := GetMonitoringStoreState()
	if !s.Fallback || !strings.Contains(s.Advice, "use 192.168.1.10:5432") || !strings.Contains(MonitoringStoreSentence(s), "use 192.168.1.10:5432") {
		t.Fatalf("state %+v", s)
	}
	if len(stub.down) != 1 || !strings.Contains(stub.down[0].Advice, "use 192.168.1.10") {
		t.Errorf("the notice lacks the advice: %+v", stub.down)
	}
	w.look(false)
	w.look(false)
	if asked != 2 {
		t.Errorf("Docker asked %d times for one outage", asked)
	}
}

// A clean install holds the watcher while it deletes the files (M8).
func TestPauseHoldsTheWatcher(t *testing.T) {
	withMonitoringStore(t, DatabaseConfig{PostgresHost: "192.0.2.10:5432", PostgresUsername: "cosmos"}, refusedProblem)
	storeWatchPaused.Store(false)
	release := PauseMonitoringStore()
	if !storeWatchPaused.Load() {
		t.Fatal("not paused")
	}
	release()
	if storeWatchPaused.Load() {
		t.Fatal("still paused")
	}
	storeWatchPaused.Store(true)
}

// Identifiers in a sentence the owner may paste are quoted (M5).
func TestQuoteIdent(t *testing.T) {
	if quoteIdent("my-user") != `"my-user"` || quoteIdent(`a"b`) != `"a""b"` {
		t.Error(quoteIdent("my-user"), quoteIdent(`a"b`))
	}
}

// The store's identity names neither the password nor changes per opening.
func TestMonitoringStoreID(t *testing.T) {
	withMonitoringStore(t, DatabaseConfig{}, nil)
	InitMetricsDatabase()
	first := MonitoringStoreID()
	InitMetricsDatabase()
	if first == "" || MonitoringStoreID() != first {
		t.Errorf("%q %q", first, MonitoringStoreID())
	}
	if strings.Contains(postgresStoreKey(DatabaseConfig{PostgresHost: "h", PostgresPassword: "secret-pw"}), "secret-pw") {
		t.Error("the key holds the password")
	}
}

// A schema made at the same moment by the store in use is tried again;
// other failures are not.
func TestPrepareMonitoringSchemaRetriesARace(t *testing.T) {
	prevPause := schemaRacePause
	schemaRacePause = time.Millisecond
	t.Cleanup(func() { schemaRacePause = prevPause })
	tries := 0
	err := retrySchemaRace(func() error {
		tries++
		if tries < 3 {
			return &pgconn.PgError{Code: "23505"}
		}
		return nil
	})
	if err != nil || tries != 3 {
		t.Errorf("race: %v after %d tries", err, tries)
	}
	tries = 0
	if err := retrySchemaRace(func() error { tries++; return &pgconn.PgError{Code: "42501"} }); err == nil || tries != 1 {
		t.Errorf("a refusal is not retried: %v after %d tries", err, tries)
	}
	tries = 0
	if err := retrySchemaRace(func() error { tries++; return &pgconn.PgError{Code: "42P07"} }); err == nil || tries != schemaRaceTries {
		t.Errorf("bounded: %v after %d tries", err, tries)
	}
}

// ---------------------------------------------------------------- live

func livePostgres(t *testing.T) DatabaseConfig {
	t.Helper()
	host := os.Getenv("COSMOS_FORK_PG_TEST")
	if host == "" {
		t.Skip("COSMOS_FORK_PG_TEST not set")
	}
	or := func(name, fallback string) string {
		if v := os.Getenv(name); v != "" {
			return v
		}
		return fallback
	}
	return DatabaseConfig{PostgresHost: host, PostgresDatabase: or("COSMOS_FORK_PG_TEST_DB", "cosmos"),
		PostgresUsername: or("COSMOS_FORK_PG_TEST_USER", "cosmos"), PostgresPassword: or("COSMOS_FORK_PG_TEST_PASSWORD", "cosmos-test-pw")}
}

// The probe: tables may be made; "reader" may not create; the tables of
// another role are said with their owner (S1: the save refuses them too).
func TestCheckPostgresStoreLive(t *testing.T) {
	ok := livePostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if p := CheckPostgresStore(ctx, ok); p != nil {
		t.Fatalf("working settings: %+v", p)
	}
	reader := DatabaseConfig{PostgresHost: ok.PostgresHost, PostgresDatabase: ok.PostgresDatabase, PostgresUsername: "reader", PostgresPassword: "reader-test-pw"}
	if p := CheckPostgresStore(ctx, reader); p == nil || p.Reason != "create" || !strings.Contains(p.Message, `GRANT CREATE ON SCHEMA public TO "reader"`) {
		t.Errorf("reader: %+v", p)
	}
	if p := CheckPostgresStore(ctx, DatabaseConfig{PostgresHost: "127.0.0.1:1", PostgresUsername: "cosmos"}); p == nil || p.Reason != "closed" {
		t.Errorf("closed: %+v", p)
	}
	// "other" may create tables, but the store's tables are the owner's
	// (made by the store in use): the probe says so, as the schema would fail
	withMonitoringStore(t, ok, nil)
	probeMonitoringPostgres = CheckPostgresStore
	if err := InitMetricsDatabase(); err != nil || MonitoringDialect() != DialectPostgres {
		t.Fatalf("store on PostgreSQL: %v %s", err, MonitoringDialect())
	}
	other := DatabaseConfig{PostgresHost: ok.PostgresHost, PostgresDatabase: ok.PostgresDatabase, PostgresUsername: "other", PostgresPassword: "other-test-pw"}
	if p := CheckPostgresStore(ctx, other); p == nil || p.Reason != "owner" || !strings.Contains(p.Message, "belong to "+ok.PostgresUsername) ||
		!strings.Contains(p.Message, `REASSIGN OWNED BY "`+ok.PostgresUsername+`" TO "other"`) {
		t.Errorf("other: %+v", p)
	}
}

// pgProxy stands between Cosmos and PostgreSQL: up, refusing, or silent
// (accepts and never answers: a host that froze).
type pgProxy struct {
	ln     net.Listener
	target string
	mode   atomic.Value // "up", "refuse", "silent"
	mu     sync.Mutex
	conns  []net.Conn
}

func newPgProxy(t *testing.T, target string) *pgProxy {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &pgProxy{ln: ln, target: target}
	p.mode.Store("up")
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			p.conns = append(p.conns, c)
			p.mu.Unlock()
			switch p.mode.Load().(string) {
			case "refuse":
				c.Close()
				continue
			case "silent":
				continue // held open, never answered
			}
			go func(c net.Conn) {
				up, err := net.Dial("tcp", p.target)
				if err != nil {
					c.Close()
					return
				}
				p.mu.Lock()
				p.conns = append(p.conns, up)
				p.mu.Unlock()
				go func() { io.Copy(up, c); up.Close() }()
				io.Copy(c, up)
				c.Close()
			}(c)
		}
	}()
	t.Cleanup(func() { ln.Close(); p.cut() })
	return p
}

// cut drops every connection (as a stopped database would).
func (p *pgProxy) cut() {
	p.mu.Lock()
	for _, c := range p.conns {
		c.Close()
	}
	p.conns = nil
	p.mu.Unlock()
}

func (p *pgProxy) addr() string { return p.ln.Addr().String() }

// S2/S4 live: five short outages through a proxy while writers write: no
// notice for short outages, no "database is closed". Then a silent host:
// the opening returns within its bound and falls back.
func TestFlappingAndSilentHostLive(t *testing.T) {
	ok := livePostgres(t)
	proxy := newPgProxy(t, ok.PostgresHost)
	db := ok
	db.PostgresHost = proxy.addr()
	stub := withMonitoringStore(t, db, nil)
	monitoringClock = time.Now
	probeMonitoringPostgres = CheckPostgresStore
	setTiming(func(*storeTimes) {
		storeTiming.pingEvery, storeTiming.retryFirst, storeTiming.pingWithin = 200*time.Millisecond, 200*time.Millisecond, time.Second
	})
	setTiming(func(*storeTimes) {
		storeTiming.goodSpan, storeTiming.minStay, storeTiming.tellAfter, storeTiming.drain = time.Second, time.Second, time.Minute, 500*time.Millisecond
	})
	if err := InitMetricsDatabase(); err != nil || MonitoringDialect() != DialectPostgres {
		t.Fatalf("PostgreSQL first: %v %s", err, MonitoringDialect())
	}
	var closedErrors, writes atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				case <-time.After(20 * time.Millisecond):
				}
				store, err := MonitoringWriteDB()
				if err != nil {
					closedErrors.Add(1)
					continue
				}
				_, err = store.Exec(rebindFor(MonitoringDialect(), "INSERT INTO monitoring_kv(key, value) VALUES(?, 'v') ON CONFLICT(key) DO UPDATE SET value = excluded.value"), "flap")
				if err != nil && strings.Contains(err.Error(), "database is closed") {
					closedErrors.Add(1)
				} else if err == nil {
					writes.Add(1)
				}
			}
		}()
	}
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	run := func(d time.Duration) {
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			time.Sleep(w.safeLook(false))
		}
	}
	for outage := 0; outage < 5; outage++ {
		proxy.mode.Store("refuse")
		proxy.cut()
		run(2 * time.Second)
		proxy.mode.Store("up")
		run(5 * time.Second)
	}
	close(stop)
	wg.Wait()
	if closedErrors.Load() != 0 || writes.Load() == 0 {
		t.Errorf("writers: %d on a closed store, %d writes", closedErrors.Load(), writes.Load())
	}
	if down, back, _ := stub.counts(); down > 0 || back > 0 {
		t.Errorf("short outages told: %d down, %d back", down, back)
	}

	// a silent host: the opening returns within its bound and falls back
	proxy.mode.Store("silent")
	proxy.cut()
	start := time.Now()
	InitMetricsDatabase()
	if took := time.Since(start); took > storeTiming.probe+5*time.Second {
		t.Errorf("the opening took %v on a silent host", took)
	}
	if s := GetMonitoringStoreState(); !s.Fallback || s.InUse != DialectSQLite {
		t.Errorf("silent host: %+v", s)
	}
}

// The healing against a real PostgreSQL: down at the start (the local file),
// answering later (the switch after steady answers).
func TestFallbackHealsLive(t *testing.T) {
	db := livePostgres(t)
	stub := withMonitoringStore(t, db, refusedProblem)
	InitMetricsDatabase()
	if s := GetMonitoringStoreState(); !s.Fallback || s.InUse != DialectSQLite {
		t.Fatalf("down: %+v", s)
	}
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	w.look(true)
	probeMonitoringPostgres = CheckPostgresStore
	for i := 0; i < 20 && MonitoringDialect() != DialectPostgres; i++ {
		stub.advance(w.look(false))
	}
	s := GetMonitoringStoreState()
	if s.Fallback || s.InUse != DialectPostgres || s.LocalFrom.IsZero() {
		t.Fatalf("healed: %+v", s)
	}
	store, _ := MonitoringWriteDB()
	if err := store.QueryRow("SELECT count(*) FROM metric_values").Scan(new(int)); err != nil {
		t.Fatalf("tables in PostgreSQL: %v", err)
	}
}

func setTiming(change func(*storeTimes)) {
	storeTimingMu.Lock()
	change(&storeTiming)
	storeTimingMu.Unlock()
}

// Settings changed to another server while a told outage lasted: the old
// outage's notice is closed in the local file, and nothing is told into the
// new store.
func TestOutageOfAnOldServerClosedOnAChange(t *testing.T) {
	stub := withMonitoringStore(t, DatabaseConfig{PostgresHost: "172.17.0.2:5432", PostgresUsername: "cosmos", PostgresPassword: "x"}, refusedProblem)
	fakePostgres(t)
	InitMetricsDatabase()
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	w.look(true)
	stub.advance(storeTiming.tellAfter)
	w.look(false)
	if down, _, _ := stub.counts(); down != 1 {
		t.Fatalf("not told")
	}
	config := GetBaseMainConfig()
	config.Database.PostgresHost = "192.168.1.10:5432"
	LoadBaseMainConfig(config)
	stub.setProblem(nil)
	InitMetricsDatabase() // the save of the new server
	waitCatchUps(t)
	if down, back, resolved := stub.counts(); MonitoringDialect() != DialectPostgres || resolved != 1 || back != 0 || down != 1 {
		t.Errorf("%s: down %d, back %d, resolved %d", MonitoringDialect(), down, back, resolved)
	}
	if why := stub.endedWhy(); len(why) != 1 || why[0] != "other" {
		t.Errorf("closed with %v, want the words of another server", why)
	}
	if s := GetMonitoringStoreState(); len(s.Aside) != 1 || s.Aside[0].Host != "172.17.0.2:5432" {
		t.Errorf("the rows of the old server's outage are not said to stay aside: %+v", s.Aside)
	}
	stub.advance(time.Hour)
	w.look(false)
	if down, _, _ := stub.counts(); down != 1 {
		t.Errorf("told into the new store")
	}
}

// Review round 2, B1: the store's id given to clients is random, drawn at
// each opening of a store (another process: another id), and unrelated to
// the password (the same settings give different ids; it is not derived
// from anything).
func TestStoreIDIsRandom(t *testing.T) {
	withMonitoringStore(t, DatabaseConfig{PostgresHost: "192.0.2.10:5432", PostgresUsername: "cosmos", PostgresPassword: "pw-one"}, nil)
	fakePostgres(t)
	InitMetricsDatabase()
	first := MonitoringStoreID()
	InitMetricsDatabase() // the same store: the same id
	if MonitoringStoreID() != first || len(first) != 12 {
		t.Fatalf("%q %q", first, MonitoringStoreID())
	}
	CloseMetricsDatabase() // as a new process would
	InitMetricsDatabase()
	if second := MonitoringStoreID(); second == first {
		t.Errorf("the same id after opening the same store again: %q", second)
	}
	if strings.Contains(MonitoringStoreID(), "pw") {
		t.Error("the id carries the password")
	}
}

// Mutation (c) of the second review: an outage is not told before it lasted,
// also on a look that is not the first.
func TestOutageNotToldBeforeItLasted(t *testing.T) {
	stub := withMonitoringStore(t, DatabaseConfig{PostgresHost: "192.0.2.10:5432", PostgresUsername: "cosmos"}, refusedProblem)
	InitMetricsDatabase()
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	w.look(true)
	for i := 0; i < 5; i++ {
		stub.advance(20 * time.Second)
		w.look(false)
	}
	if down, _, _ := stub.counts(); down != 0 {
		t.Fatalf("told after 100 s")
	}
	stub.advance(storeTiming.tellAfter)
	w.look(false)
	if down, _, _ := stub.counts(); down != 1 {
		t.Errorf("not told after it lasted")
	}
}

// Review round 2, S1: the local file cannot be opened when the fallback
// starts (busy, a full disk): the watcher tries it again, and a store is in
// use as soon as it can be opened.
func TestLocalFileRetriedUntilThereIsAStore(t *testing.T) {
	stub := withMonitoringStore(t, DatabaseConfig{PostgresHost: "192.0.2.10:5432", PostgresUsername: "cosmos", PostgresPassword: "x"}, nil)
	fakePostgres(t)
	InitMetricsDatabase()
	// the file cannot be made: a folder in its place
	if err := os.Mkdir(CONFIGFOLDER+"database.db", 0o700); err != nil {
		t.Fatal(err)
	}
	pingMonitoringStore = func(context.Context) error { return errors.New("lost") }
	stub.setProblem(refusedProblem)
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	w.look(false)
	w.look(false)
	if s := GetMonitoringStoreState(); !s.Fallback || s.InUse == DialectSQLite {
		t.Fatalf("expected no local file yet: %+v", s)
	}
	w.look(false)
	os.Remove(CONFIGFOLDER + "database.db")
	w.look(false)
	if s := GetMonitoringStoreState(); s.InUse != DialectSQLite || !s.Fallback {
		t.Fatalf("the local file was not tried again: %+v", s)
	}
	if db, err := MonitoringWriteDB(); err != nil || func() error { _, e := db.Exec("INSERT INTO monitoring_kv(key, value) VALUES('a', 'b')"); return e }() != nil {
		t.Error("the store does not take writes")
	}
}

// Review round 2, S3: a restart during a told outage continues it: told,
// since its start; "answers again" follows.
func TestRestartDuringAToldOutage(t *testing.T) {
	stub := withMonitoringStore(t, DatabaseConfig{PostgresHost: "192.0.2.10:5432", PostgresUsername: "cosmos", PostgresPassword: "x"}, refusedProblem)
	fakePostgres(t)
	started := stub.clock()
	InitMetricsDatabase()
	w0 := &monitoringWatch{backoff: storeTiming.retryFirst}
	w0.look(true)
	stub.advance(storeTiming.tellAfter + time.Minute)
	w0.look(false)
	if down, _, _ := stub.counts(); down != 1 {
		t.Fatal("not told before the restart")
	}
	stub.advance(10 * time.Minute)
	// "the restart": the in-memory state of the outage is gone
	resetStoreState()
	storeStateMu.Lock()
	storeStarted = false
	storeStateMu.Unlock()
	CloseMetricsDatabase()
	InitMetricsDatabase()
	storeStateMu.Lock()
	told, since := storeTold, storeState.Since
	storeStateMu.Unlock()
	if !told || !since.Equal(MillisToTime(TimeToMillis(started))) {
		t.Fatalf("told %v since %v (want %v)", told, since, started)
	}
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	stub.setProblem(nil)
	for i := 0; i < 50 && MonitoringDialect() != DialectPostgres; i++ {
		stub.advance(w.look(false))
	}
	waitCatchUps(t)
	if down, back, resolved := stub.counts(); MonitoringDialect() != DialectPostgres || back != 1 || resolved != 1 || down != 1 {
		t.Errorf("%s: down %d back %d resolved %d", MonitoringDialect(), down, back, resolved)
	}
}

// Review round 2, S4a: a fallback at the start of the process (PostgreSQL
// starting after Cosmos) switches back on the first good probe, is not an
// outage and is not told.
func TestBootFallbackSwitchesBackAtOnce(t *testing.T) {
	stub := withMonitoringStore(t, DatabaseConfig{PostgresHost: "192.0.2.10:5432", PostgresUsername: "cosmos", PostgresPassword: "x"}, refusedProblem)
	fakePostgres(t)
	storeStateMu.Lock()
	storeStarted = false // the start of the process
	storeStateMu.Unlock()
	InitMetricsDatabase()
	if MonitoringDialect() != DialectSQLite {
		t.Fatal("not on the local file at the start")
	}
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	w.look(true)
	stub.advance(30 * time.Second)
	stub.setProblem(nil)
	w.look(false)
	if MonitoringDialect() != DialectPostgres {
		t.Fatal("not back on the first good probe after the start")
	}
	storeStateMu.Lock()
	outages := len(storeOutages)
	storeStateMu.Unlock()
	if down, back, _ := stub.counts(); outages != 0 || down != 0 || back != 0 {
		t.Errorf("a boot counted or told: outages %d down %d back %d", outages, down, back)
	}
}

// Review round 2, M3: a superuser's tables are given one by one.
func TestOwnerAdvice(t *testing.T) {
	a := ownerAdvice("cosmos", []string{"events", "metrics"}, "postgres", true, "my-user")
	if strings.Contains(a, "REASSIGN") || !strings.Contains(a, `ALTER TABLE "events" OWNER TO "my-user"; ALTER TABLE "metrics" OWNER TO "my-user";`) {
		t.Error(a)
	}
	if b := ownerAdvice("cosmos", []string{"events"}, "keeper", false, "u"); !strings.Contains(b, `REASSIGN OWNED BY "keeper" TO "u"`) {
		t.Error(b)
	}
}

// Review round 2, M2: a panic between the opening of PostgreSQL's pools and
// the swap (here in the schema step) closes the new pools.
func TestPanicBeforeTheSwapClosesTheNewPools(t *testing.T) {
	withMonitoringStore(t, DatabaseConfig{PostgresHost: "192.0.2.10:5432", PostgresUsername: "cosmos", PostgresPassword: "x"}, nil)
	fakePostgres(t)
	open := openPostgresStore
	var opened []*sql.DB
	openPostgresStore = func(d DatabaseConfig) (*sql.DB, *sql.DB, error) {
		w, r, err := open(d)
		opened = append(opened, w, r)
		return w, r, err
	}
	prepareStoreSchema = func(*sql.DB, DatabaseConfig) error { panic("schema") }
	func() {
		defer func() { _ = recover() }()
		InitMetricsDatabase()
	}()
	if len(opened) != 2 {
		t.Fatalf("pools opened: %d", len(opened))
	}
	for i, db := range opened {
		if err := db.Ping(); err == nil || !strings.Contains(err.Error(), "closed") {
			t.Errorf("pool %d left open after the panic: %v", i, err)
		}
	}
}

// Review round 2, M8: the outage's notice is closed in the local file
// through a handle of its own (the store's may be closed or draining), and
// again while another connection holds the file longer than its busy
// timeout.
func TestOutageResolvedThroughItsOwnHandleWhileBusy(t *testing.T) {
	resolve := monitoringResolveDown
	withMonitoringStore(t, DatabaseConfig{}, nil)
	InitMetricsDatabase()
	local, _ := MonitoringWriteDB()
	if _, err := local.Exec("INSERT INTO notifications(node, recipient, title, date, fork_kind, fork_thread, fork_first, fork_info) VALUES('n', 'admin', 't', 1, 'monitoring.store.down', ?, 1, '{}')", monitoringStoreThread); err != nil {
		t.Fatal(err)
	}
	CloseMetricsDatabase() // the store's handle is gone
	holder, err := sql.Open(DialectSQLite, localFileDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	tx, err := holder.Begin() // _txlock=immediate: the write lock, held
	if err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(5500 * time.Millisecond); tx.Rollback() }()
	resolve()
	check, _ := sql.Open(DialectSQLite, localFileDSN())
	defer check.Close()
	if at := unresolvedOutage(check); !at.IsZero() {
		t.Errorf("the outage's notice is still open: %v", at)
	}
}
