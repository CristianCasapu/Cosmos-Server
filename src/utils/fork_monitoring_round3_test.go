package utils

// Fork addition (plan 51, review round 3): tests of the fallback's record
// (fork_monitoring_fallback.go) and of the watcher's convergence.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var pgConfig = DatabaseConfig{PostgresHost: "192.0.2.10:5432", PostgresUsername: "cosmos", PostgresPassword: "x"}

// handedWindows records the windows handed to the catch-up (which says
// done, all present, at once).
func handedWindows(t *testing.T) func() []CatchUpWindow {
	t.Helper()
	var mu sync.Mutex
	got := []CatchUpWindow{}
	prev := MonitoringCatchUp
	MonitoringCatchUp = func(w CatchUpWindow, done func(CatchUpResult)) {
		mu.Lock()
		got = append(got, w)
		mu.Unlock()
		done(CatchUpResult{Ran: true, Counted: true})
	}
	t.Cleanup(func() { MonitoringCatchUp = prev })
	return func() []CatchUpWindow {
		mu.Lock()
		defer mu.Unlock()
		return append([]CatchUpWindow(nil), got...)
	}
}

// pingSwitch makes the pings fail or answer.
func pingSwitch() *atomic.Bool {
	ok := &atomic.Bool{}
	ok.Store(true)
	pingMonitoringStore = func(context.Context) error {
		if ok.Load() {
			return nil
		}
		return errors.New("connection lost")
	}
	return ok
}

// fallBack makes PostgreSQL stop: two failed pings, the local file takes over.
func fallBack(t *testing.T, stub *storeStub, w *monitoringWatch, ping *atomic.Bool) {
	t.Helper()
	ping.Store(false)
	stub.setProblem(refusedProblem)
	for i := 0; i < 4 && MonitoringDialect() != DialectSQLite; i++ {
		stub.advance(5 * time.Second)
		w.look(false)
	}
	if MonitoringDialect() != DialectSQLite {
		t.Fatal("not on the local file")
	}
}

// comeBack makes PostgreSQL answer until the watcher switched back.
func comeBack(t *testing.T, stub *storeStub, w *monitoringWatch, ping *atomic.Bool) {
	t.Helper()
	ping.Store(true)
	stub.setProblem(nil)
	for i := 0; i < 400 && MonitoringDialect() != DialectPostgres; i++ {
		stub.advance(w.look(false))
	}
	if MonitoringDialect() != DialectPostgres {
		t.Fatal("not back on PostgreSQL")
	}
	waitCatchUps(t)
}

func localFile(t *testing.T) *sql.DB {
	t.Helper()
	local, err := sql.Open(DialectSQLite, localFileDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { local.Close() })
	return local
}

// S1: every row the writers inserted into the local file during the
// fallback is within the window's ids, kept rows dated before the fallback
// included, the local file's earlier rows not; and the window's time starts
// before the fallback by the pings that found PostgreSQL gone (the
// reviewer's surviving mutation, the window starting 10 s late, fails here).
func TestTheWindowHoldsEveryRowTheFallbackWrote(t *testing.T) {
	stub := withMonitoringStore(t, pgConfig, nil)
	fakePostgres(t)
	windows := handedWindows(t)
	ping := pingSwitch()
	// the local file's own history (an earlier period on it)
	if err := withLocalFile(true, func(local *sql.DB) error {
		if err := initMetricsSchema(local, DialectSQLite); err != nil {
			return err
		}
		_, err := local.Exec("INSERT INTO events(node, event_id, label, application, level, date, data, object, search) VALUES('n', 'history', '', '', '', 1, '', '', '')")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	InitMetricsDatabase()
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	fallBack(t, stub, w, ping)
	since := GetMonitoringStoreState().Since
	// written in the first second of the fallback: a kept event dated before it,
	// one of the fallback, a notification
	InsertEvent(Event{EventId: "kept", Label: "kept", Date: since.Add(-90 * time.Second), Level: "info"})
	InsertEvent(Event{EventId: "during", Label: "during", Date: since, Level: "info"})
	WriteNotification(Notification{Recipient: "tester", Title: "during", Message: "during", Level: "info"})
	stub.advance(10 * time.Second)
	InsertEvent(Event{EventId: "later", Label: "later", Date: stub.clock(), Level: "info"})
	comeBack(t, stub, w, ping)
	got := windows()
	if len(got) != 1 {
		t.Fatalf("%d windows handed", len(got))
	}
	win := got[0]
	local := localFile(t)
	rows, err := local.Query("SELECT id, event_id FROM events")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var id int64
		var name string
		rows.Scan(&id, &name)
		in := id > win.After["events"] && id <= win.Before["events"]
		if in != (name != "history") {
			t.Errorf("event %s (id %d): in the window %v (ids %d..%d)", name, id, in, win.After["events"], win.Before["events"])
		}
		seen++
	}
	if seen != 4 {
		t.Errorf("%d events in the local file", seen)
	}
	var notes int64
	local.QueryRow("SELECT COUNT(*) FROM notifications WHERE id > ? AND id <= ?", win.After["notifications"], win.Before["notifications"]).Scan(&notes)
	if notes < 1 {
		t.Errorf("the notification of the fallback is not in the window")
	}
	if lead := timing().pingFailures * int(timing().pingEvery/time.Second); win.From.After(since.Add(-time.Duration(lead) * time.Second)) {
		t.Errorf("the window starts at %v, after the fallback's start %v less the pings that found PostgreSQL gone", win.From, since)
	}
	if win.Back.IsZero() || win.To.Before(win.Back) {
		t.Errorf("the window ends before the switch back: %+v", win)
	}
}

// told runs an outage until it was told.
func told(t *testing.T, stub *storeStub, w *monitoringWatch) {
	t.Helper()
	before, _, _ := stub.counts()
	for i := 0; i < 20; i++ {
		stub.advance(30 * time.Second)
		w.look(false)
		if down, _, _ := stub.counts(); down > before {
			return
		}
	}
	t.Fatal("not told")
}

// S3: a told outage ended by a save of the same server (the password
// corrected): closed, caught up, "answers again"; the next outage is told
// anew, from its own start.
func TestASaveOfTheSameServerEndsAToldOutage(t *testing.T) {
	stub := withMonitoringStore(t, pgConfig, nil)
	fakePostgres(t)
	windows := handedWindows(t)
	ping := pingSwitch()
	InitMetricsDatabase()
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	fallBack(t, stub, w, ping)
	told(t, stub, w)
	// the owner corrects the password and saves
	config := GetBaseMainConfig()
	config.Database.PostgresPassword = "corrected"
	LoadBaseMainConfig(config)
	ping.Store(true)
	stub.setProblem(nil)
	InitMetricsDatabase()
	waitCatchUps(t)
	if down, back, resolved := stub.counts(); MonitoringDialect() != DialectPostgres || down != 1 || back != 1 || resolved != 1 || len(windows()) != 1 {
		t.Fatalf("%s: down %d back %d resolved %d windows %d", MonitoringDialect(), down, back, resolved, len(windows()))
	}
	storeStateMu.Lock()
	stillTold := storeTold
	storeStateMu.Unlock()
	if stillTold {
		t.Error("still told after the save")
	}
	// a day later, a real outage: told, from its own start
	stub.advance(24 * time.Hour)
	fallBack(t, stub, w, ping)
	start := GetMonitoringStoreState().Since
	told(t, stub, w)
	if down, _, _ := stub.counts(); down != 2 || !start.After(stub.clock().Add(-time.Hour)) {
		t.Errorf("the next outage: %d down notices, since %v", down, start)
	}
}

// S3: a move to "local file only" during a told outage closes it with words
// that fit; a later outage of another server is told anew.
func TestAMoveToTheLocalFileClosesTheOutage(t *testing.T) {
	stub := withMonitoringStore(t, pgConfig, nil)
	fakePostgres(t)
	handedWindows(t)
	ping := pingSwitch()
	InitMetricsDatabase()
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	fallBack(t, stub, w, ping)
	told(t, stub, w)
	config := GetBaseMainConfig()
	config.Database = DatabaseConfig{}
	LoadBaseMainConfig(config)
	InitMetricsDatabase()
	if why := stub.endedWhy(); len(why) != 1 || why[0] != "local" {
		t.Fatalf("closed with %v", why)
	}
	if _, _, resolved := stub.counts(); resolved != 1 {
		t.Errorf("the notice is not closed")
	}
	if recs := records(t); len(recs) != 0 {
		t.Errorf("records left: %+v", recs)
	}
	// three weeks later, another server that does not answer
	stub.advance(21 * 24 * time.Hour)
	config.Database = DatabaseConfig{PostgresHost: "192.0.2.20:5432", PostgresUsername: "cosmos", PostgresPassword: "y"}
	LoadBaseMainConfig(config)
	stub.setProblem(refusedProblem)
	InitMetricsDatabase()
	if s := GetMonitoringStoreState(); !s.Fallback || stub.clock().Sub(s.Since) > time.Minute {
		t.Errorf("the new outage: %+v", s)
	}
	told(t, stub, w)
	if down, _, _ := stub.counts(); down != 2 {
		t.Errorf("the new outage is not told: %d", down)
	}
}

func records(t *testing.T) []fallbackRecord {
	t.Helper()
	var out []fallbackRecord
	withLocalFile(false, func(local *sql.DB) error { out = loadFallbacks(local); return nil })
	return out
}

// S3: an open outage is continued at a start only for the same server seen
// running within a day; an older one is closed ("stale") and a new outage
// begins; one of another server is closed with its own words.
func TestOnlyARecentOutageOfTheSameServerIsContinued(t *testing.T) {
	for _, c := range []struct {
		name      string
		host      string
		age       time.Duration
		continue_ bool
		why       string
	}{
		{"same, an hour", pgConfig.PostgresHost, time.Hour, true, ""},
		{"same, two days", pgConfig.PostgresHost, 48 * time.Hour, false, "stale"},
		{"another server", "192.0.2.99:5432", time.Hour, false, "other"},
	} {
		t.Run(c.name, func(t *testing.T) {
			stub := withMonitoringStore(t, pgConfig, refusedProblem)
			fakePostgres(t)
			handedWindows(t)
			old := stub.clock().Add(-c.age - time.Hour)
			withLocalFile(true, func(local *sql.DB) error {
				initMetricsSchema(local, DialectSQLite)
				return saveFallbacks(local, []fallbackRecord{{Host: c.host, Database: postgresDatabaseName(pgConfig), Since: TimeToMillis(old),
					From: TimeToMillis(old), After: map[string]int64{}, Told: true, Seen: TimeToMillis(stub.clock().Add(-c.age))}})
			})
			storeStateMu.Lock()
			storeStarted = false // a start
			storeStateMu.Unlock()
			InitMetricsDatabase()
			storeStateMu.Lock()
			wasTold, since := storeTold, storeState.Since
			storeStateMu.Unlock()
			if c.continue_ != (wasTold && since.Equal(MillisToTime(TimeToMillis(old)))) {
				t.Errorf("continued: told %v since %v", wasTold, since)
			}
			why := stub.endedWhy()
			if c.why == "" && len(why) != 0 || c.why != "" && (len(why) != 1 || why[0] != c.why) {
				t.Errorf("closed with %v, want %q", why, c.why)
			}
		})
	}
}

// S4: from every combination of (the store asked for, the fallback flag, the
// store in use) the watcher's looks reach the right store: the local file
// when it is asked for, PostgreSQL when it answers, the local file with the
// fallback when it does not.
func TestTheWatcherConvergesFromEveryState(t *testing.T) {
	for _, configured := range []string{"sqlite", "answers", "refuses"} {
		for _, fallback := range []bool{false, true} {
			for _, inUse := range []string{"", DialectSQLite, DialectPostgres} {
				t.Run(fmt.Sprintf("%s-%v-%s", configured, fallback, inUse), func(t *testing.T) {
					db := pgConfig
					if configured == "sqlite" {
						db = DatabaseConfig{}
					}
					var problem *PostgresProblem
					if configured == "refuses" {
						problem = refusedProblem
					}
					stub := withMonitoringStore(t, db, problem)
					fakePostgres(t)
					handedWindows(t)
					ping := pingSwitch()
					ping.Store(configured != "refuses")
					node := resolveNodeName(GetMainConfig())
					switch inUse {
					case DialectSQLite:
						if err := openLocalStore(node); err != nil {
							t.Fatal(err)
						}
					case DialectPostgres:
						w, r, err := openPostgresStore(pgConfig)
						if err != nil {
							t.Fatal(err)
						}
						swapMonitoringStore(w, r, DialectPostgres, postgresStoreKey(pgConfig), node)
					}
					setStoreState(func(s *MonitoringStoreState) {
						s.Fallback, s.Configured, s.Since = fallback, "postgres", stub.clock()
						if !fallback && configured == "sqlite" {
							s.Configured = "sqlite"
						}
						s.Host = pgConfig.PostgresHost
					})
					w := &monitoringWatch{backoff: storeTiming.retryFirst}
					for i := 0; i < 300; i++ {
						stub.advance(w.look(false))
					}
					waitCatchUps(t)
					s := GetMonitoringStoreState()
					want, wantFallback := DialectSQLite, false
					switch configured {
					case "answers":
						want = DialectPostgres
					case "refuses":
						wantFallback = true
					}
					if s.InUse != want || s.Fallback != wantFallback {
						t.Errorf("ended on %q, fallback %v; want %q, %v", s.InUse, s.Fallback, want, wantFallback)
					}
				})
			}
		}
	}
}

// S4: a look that read "fallback" before a save made PostgreSQL the store
// does not open the local file afterwards.
func TestALateLookDoesNotUndoASave(t *testing.T) {
	withMonitoringStore(t, pgConfig, nil)
	fakePostgres(t)
	handedWindows(t)
	InitMetricsDatabase()
	if MonitoringDialect() != DialectPostgres {
		t.Fatal("PostgreSQL first")
	}
	ensureLocalStore() // what the stale look would do
	if MonitoringDialect() != DialectPostgres {
		t.Error("the local file replaced PostgreSQL after the save")
	}
}

// M5: the boot grace is for the first fallback of the process only: a
// second one within the five minutes is an outage (it waits for steady
// answers and counts).
func TestTheBootGraceIsForTheFirstFallbackOnly(t *testing.T) {
	stub := withMonitoringStore(t, pgConfig, refusedProblem)
	fakePostgres(t)
	handedWindows(t)
	ping := pingSwitch()
	storeStateMu.Lock()
	storeStarted = false
	storeStateMu.Unlock()
	InitMetricsDatabase()
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	w.look(true)
	stub.advance(10 * time.Second)
	stub.setProblem(nil)
	w.look(false)
	if MonitoringDialect() != DialectPostgres {
		t.Fatal("the boot fallback did not switch back at once")
	}
	waitCatchUps(t)
	fallBack(t, stub, w, ping)
	stub.setProblem(nil)
	ping.Store(true)
	stub.advance(5 * time.Second)
	w.look(false)
	if MonitoringDialect() != DialectPostgres {
		// waits for steady answers: right
	} else {
		t.Error("a second fallback within the grace switched back at the first good probe")
	}
	storeStateMu.Lock()
	outages := len(storeOutages)
	storeStateMu.Unlock()
	if outages != 1 {
		t.Errorf("the second fallback is not counted as an outage: %d", outages)
	}
	comeBack(t, stub, w, ping)
}

// M7: the history in use stays partial until the catch-up has ended (the
// Telegram check of a new address stays hedged until then).
func TestTheHistoryIsPartialUntilTheCatchUpEnded(t *testing.T) {
	stub := withMonitoringStore(t, pgConfig, nil)
	fakePostgres(t)
	ping := pingSwitch()
	release := make(chan struct{})
	prev := MonitoringCatchUp
	MonitoringCatchUp = func(w CatchUpWindow, done func(CatchUpResult)) {
		go func() { <-release; done(CatchUpResult{Ran: true, Counted: true}) }()
	}
	t.Cleanup(func() { MonitoringCatchUp = prev })
	InitMetricsDatabase()
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	fallBack(t, stub, w, ping)
	ping.Store(true)
	stub.setProblem(nil)
	for i := 0; i < 400 && MonitoringDialect() != DialectPostgres; i++ {
		stub.advance(w.look(false))
	}
	time.Sleep(timing().drain + 600*time.Millisecond)
	if !MonitoringStorePartial() {
		t.Error("not partial while the catch-up runs")
	}
	close(release)
	waitCatchUps(t)
	if MonitoringStorePartial() {
		t.Error("still partial after the catch-up ended")
	}
}

// M2: the fallback is recorded in the local file from its start: a restart
// before it was told still hands its window over when PostgreSQL answers.
func TestARestartDuringAnUntoldFallbackKeepsItsWindow(t *testing.T) {
	stub := withMonitoringStore(t, pgConfig, nil)
	fakePostgres(t)
	windows := handedWindows(t)
	ping := pingSwitch()
	InitMetricsDatabase()
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	fallBack(t, stub, w, ping)
	InsertEvent(Event{EventId: "aside", Label: "aside", Date: stub.clock(), Level: "info"})
	recs := records(t)
	if len(recs) != 1 || recs[0].Ended != 0 || recs[0].After == nil {
		t.Fatalf("not recorded at its start: %+v", recs)
	}
	// the restart, PostgreSQL answering again
	resetStoreState()
	storeStateMu.Lock()
	storeStarted = false
	storeStateMu.Unlock()
	CloseMetricsDatabase()
	stub.setProblem(nil)
	InitMetricsDatabase()
	waitCatchUps(t)
	got := windows()
	if len(got) != 1 || got[0].After["events"] != recs[0].After["events"] || got[0].Before["events"] <= got[0].After["events"] {
		t.Fatalf("the window after the restart: %+v (record %+v)", got, recs)
	}
	if _, back, _ := stub.counts(); back != 0 {
		t.Errorf("an untold fallback told \"answers again\"")
	}
	if left := records(t); len(left) != 0 {
		t.Errorf("record left after its catch-up: %+v", left)
	}
}
