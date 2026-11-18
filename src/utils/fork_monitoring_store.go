package utils

// Fork addition (plan 51): monitoring never runs without a store.
//
// Upstream's InitMetricsDatabase closes the open store first and then opens
// the configured one; when that is a PostgreSQL that does not answer, the
// function returned with no store open at all: metrics, events,
// notifications and the security record were dropped from then on, silently,
// until the next restart with working settings.
//
// Its hook (database.go) hands the opening to openMonitoringStore:
//
//   - One opening at a time (the op-log's reaction, the start and the watcher
//     may ask at once).
//   - When PostgreSQL is configured it is asked first, bounded, outside the
//     store's lock: connect, sign in, may create tables, owns the tables that
//     exist (what upstream's schema needs). The new store is then opened and
//     its schema made while the old one keeps recording, and swapped in under
//     the lock; the old handles are closed after a drain, so a writer that
//     took the old handle a moment before still completes.
//   - When PostgreSQL does not answer (or its schema cannot be made), the
//     local file (database.db) is the store meanwhile, and the state says so.
//     The local file stays open across failed tries: it is never closed for
//     a switch that does not happen.
//
// One watcher goroutine for the life of the process (a panic in a look is
// recovered and logged; the loop goes on) keeps trying PostgreSQL with a
// back-off (5 s doubling up to 5 min, for ever) and switches back only after
// several good probes over a minimum time and a minimum stay on the local
// file, longer after repeated outages within the hour. While PostgreSQL is in
// use it pings it; two failed pings in a row move the store to the local file.
// The administrators are told once an outage has lasted (2 minutes): one
// notice per outage, closed in the local file and followed by "answers
// again" in PostgreSQL after the switch, with the period whose records stayed
// in the file. A short outage is said on the pages only.
//
// The store's PostgreSQL connections are bounded (connect timeout, TCP
// keepalives and user timeout, statement and idle-in-transaction timeouts):
// a database host that disappears without a reset cannot hold a writer or
// the store's lock for the kernel's quarter of an hour.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const (
	monitoringStoreThread = "monitoring.store"
	// the store's PostgreSQL sessions
	storeConnectTimeout     = 8 * time.Second
	storeTCPUserTimeout     = 20 * time.Second
	storeKeepAlive          = 10 * time.Second
	storeStatementTimeoutMs = 30000
	storeIdleInTxTimeoutMs  = 60000
)

// storeTiming are the watcher's times; the tests shorten them.
type storeTimes struct {
	probeAtStart, probe   time.Duration
	retryFirst, retryMax  time.Duration
	pingEvery, pingWithin time.Duration
	pingFailures          int
	goodProbes            int           // good probes in a row before switching back
	goodSpan              time.Duration // ... spread over at least this long
	minStay               time.Duration // on the local file at least, doubled per recent outage
	maxStay               time.Duration
	flapWindow            time.Duration // outages closer than this are "repeated"
	tellAfter             time.Duration // an outage is told once it lasted this long
	idle                  time.Duration
	drain                 time.Duration // old handles stay open this long after a swap
}

// storeTimingMu guards storeTiming (the tests change it while the watcher runs).
var storeTimingMu sync.RWMutex

func timing() storeTimes {
	storeTimingMu.RLock()
	defer storeTimingMu.RUnlock()
	return storeTiming
}

var storeTiming = storeTimes{
	probeAtStart: 5 * time.Second, probe: 8 * time.Second,
	retryFirst: 5 * time.Second, retryMax: 5 * time.Minute,
	pingEvery: 15 * time.Second, pingWithin: 5 * time.Second, pingFailures: 2,
	goodProbes: 3, goodSpan: time.Minute, minStay: time.Minute, maxStay: 16 * time.Minute,
	flapWindow: time.Hour, tellAfter: 2 * time.Minute, idle: time.Minute, drain: 2 * time.Second,
}

// MonitoringStoreState is what the panel shows about the store. No secret.
type MonitoringStoreState struct {
	// Configured: "sqlite" or "postgres", what the settings ask for
	Configured string `json:"configured"`
	// InUse: the dialect open now ("sqlite", "pgx"), "" when none is open
	InUse    string `json:"inUse"`
	Host     string `json:"host,omitempty"`
	Database string `json:"database,omitempty"`
	// Fallback: PostgreSQL is configured but cannot be the store; the local
	// file is used meanwhile
	Fallback bool      `json:"fallback"`
	Since    time.Time `json:"since,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	Message  string    `json:"message,omitempty"`
	Attempts int       `json:"attempts,omitempty"`
	NextTry  time.Time `json:"nextTry,omitempty"`
	// LocalFrom..LocalTo: after a fallback healed, the period whose records
	// are in the local file
	LocalFrom time.Time `json:"localFrom,omitempty"`
	LocalTo   time.Time `json:"localTo,omitempty"`
	// Advice: the likely right server while it does not answer (from the
	// PostgreSQL containers of this server)
	Advice string `json:"advice,omitempty"`
}

var (
	forkInitMu sync.Mutex

	storeStateMu sync.Mutex
	storeState   = MonitoringStoreState{Configured: "sqlite"}
	// storeStarted: the first opening (the start of the server) is done
	storeStarted bool
	// storeTold: the outage of this fallback was told
	storeTold bool
	// storeOnLocal: when the local file became the store for this outage
	storeOnLocal time.Time
	// storeOutages: the start of the outages within flapWindow
	storeOutages []time.Time

	// monitorKey: what the open store is (dialect and address, never the
	// password); under monitorMu
	monitorKey string

	storeWatchOnce sync.Once
	storeKick      = make(chan struct{}, 1)
	// storeWatchPaused: the tests drive the looks themselves; a clean
	// install holds the watcher while it deletes the files
	storeWatchPaused atomic.Bool

	// probeMonitoringPostgres asks the database whether it can be the store;
	// replaced by the tests.
	probeMonitoringPostgres = CheckPostgresStore
	// monitoringClock is the time; replaced by the tests.
	monitoringClock = time.Now
	// monitoringTellDown / monitoringTellBack tell the administrators;
	// replaced by the tests.
	monitoringTellDown = tellMonitoringDown
	monitoringTellBack = tellMonitoringBack
	// openPostgresStore opens the store's handles on PostgreSQL and
	// prepareStoreSchema makes upstream's schema; replaced by the tests.
	openPostgresStore  = openPostgresHandles
	prepareStoreSchema = initMetricsSchema
)

// forkOpensMonitoringStore: InitMetricsDatabase hands its work to
// openMonitoringStore (a variable so that the hook reads as a condition).
var forkOpensMonitoringStore = true

// openMonitoringStore is InitMetricsDatabase of the fork.
func openMonitoringStore() error {
	forkInitMu.Lock()
	defer forkInitMu.Unlock()
	startMonitoringWatcher()

	config := GetMainConfig()
	node := resolveNodeName(config)
	db := config.Database

	storeStateMu.Lock()
	first := !storeStarted
	storeStarted = true
	storeStateMu.Unlock()

	if db.PostgresHost == "" {
		setStoreState(func(s *MonitoringStoreState) { *s = MonitoringStoreState{Configured: "sqlite"} })
		storeStateMu.Lock()
		storeTold, storeOnLocal = false, time.Time{}
		storeStateMu.Unlock()
		return openLocalStore(node)
	}

	timeout := timing().probe
	if first {
		timeout = timing().probeAtStart
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	problem := probeMonitoringPostgres(ctx, db)
	cancel()
	if problem == nil {
		key := postgresStoreKey(db)
		if storeOpenAs(key) {
			setMonitorNode(node)
			markPostgresInUse(db)
			return nil
		}
		w, r, err := openPostgresStore(db)
		if err == nil {
			if err = prepareStoreSchema(w, DialectPostgres); err != nil {
				w.Close()
				r.Close()
			}
		}
		if err == nil {
			swapMonitoringStore(w, r, DialectPostgres, key, node)
			markPostgresInUse(db)
			Log("Monitoring database ready (" + DialectPostgres + ", node " + node + ")")
			return nil
		}
		problem = schemaProblem(err, db)
	}

	Warn("Monitoring database: PostgreSQL at " + db.PostgresHost + " cannot be the store (" + problem.Reason + "): the local file is used meanwhile")
	now := monitoringClock()
	setStoreState(func(s *MonitoringStoreState) {
		same := s.Fallback && s.Host == db.PostgresHost && s.Database == postgresDatabaseName(db)
		if !same {
			*s = MonitoringStoreState{Since: now}
			storeTold, storeOnLocal = false, now
			storeOutages = append(recentOutages(now), now)
		}
		s.Configured, s.Fallback = "postgres", true
		s.Host, s.Database = db.PostgresHost, postgresDatabaseName(db)
		s.Reason, s.Message = problem.Reason, problem.Message
		s.Attempts++
	})
	kickMonitoringWatcher()
	return openLocalStore(node)
}

// recentOutages are the outages within flapWindow; storeStateMu held.
func recentOutages(now time.Time) []time.Time {
	kept := []time.Time{}
	for _, t := range storeOutages {
		if now.Sub(t) < timing().flapWindow {
			kept = append(kept, t)
		}
	}
	return kept
}

func markPostgresInUse(db DatabaseConfig) {
	now := monitoringClock()
	setStoreState(func(s *MonitoringStoreState) {
		healed := s.Fallback && s.Host == db.PostgresHost && s.Database == postgresDatabaseName(db)
		from := s.Since
		*s = MonitoringStoreState{Configured: "postgres", Host: db.PostgresHost, Database: postgresDatabaseName(db)}
		if healed {
			s.LocalFrom, s.LocalTo = from, now
		}
	})
}

// openLocalStore makes the local file the store, unless it already is.
func openLocalStore(node string) error {
	dsn := "file:" + CONFIGFOLDER + "database.db" +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	key := "sqlite|" + CONFIGFOLDER + "database.db"
	if storeOpenAs(key) {
		setMonitorNode(node)
		return nil
	}
	w, err := sql.Open(DialectSQLite, dsn)
	if err != nil {
		return err
	}
	w.SetMaxOpenConns(1) // SQLite takes a single writer
	if err := initMetricsSchema(w, DialectSQLite); err != nil {
		w.Close()
		return err
	}
	r, err := sql.Open(DialectSQLite, dsn)
	if err != nil {
		w.Close()
		return err
	}
	r.SetMaxOpenConns(4)
	swapMonitoringStore(w, r, DialectSQLite, key, node)
	Log("Monitoring database ready (" + DialectSQLite + ", node " + node + ")")
	return nil
}

// storeOpenAs: the store open now is this one.
func storeOpenAs(key string) bool {
	monitorMu.RLock()
	defer monitorMu.RUnlock()
	return monitorWriteDB != nil && monitorKey == key
}

func setMonitorNode(node string) {
	monitorMu.Lock()
	monitorNode = node
	monitorMu.Unlock()
}

// swapMonitoringStore puts new handles in place; the old ones are closed
// after a drain (sql.DB.Close then waits for the statements running).
func swapMonitoringStore(w, r *sql.DB, dialect string, key string, node string) {
	monitorMu.Lock()
	oldW, oldR := monitorWriteDB, monitorReadDB
	monitorWriteDB, monitorReadDB = w, r
	monitorDialect, monitorKey, monitorNode = dialect, key, node
	DBStatus = true
	monitorMu.Unlock()
	if oldW == nil && oldR == nil {
		return
	}
	time.AfterFunc(timing().drain, func() {
		if oldW != nil {
			oldW.Close()
		}
		if oldR != nil {
			oldR.Close()
		}
	})
}

// MonitoringStoreID names the store in use (a digest of its kind and
// address, never the password): ids of notifications and the like are only
// meaningful within one store.
func MonitoringStoreID() string {
	monitorMu.RLock()
	key := monitorKey
	monitorMu.RUnlock()
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:6])
}

// postgresStoreKey: the PostgreSQL store's identity, without the password.
func postgresStoreKey(db DatabaseConfig) string {
	return "pgx|" + db.PostgresHost + "|" + postgresDatabaseName(db) + "|" + db.PostgresUsername + "|" + fmt.Sprintf("%x", sha256.Sum256([]byte(db.PostgresPassword)))[:8]
}

// postgresStoreConfig is the store's connection: the form's values only (as
// the check), bounded (S4 of the review).
func postgresStoreConfig(db DatabaseConfig) (*pgx.ConnConfig, error) {
	cfg, err := pgx.ParseConfig(postgresCheckDSN(db, int(storeConnectTimeout/time.Second)))
	if err != nil {
		return nil, err
	}
	explicitPostgresConfig(cfg, db)
	boundPostgresConfig(cfg)
	return cfg, nil
}

// boundPostgresConfig bounds a connection: dial, keepalives, TCP user
// timeout, and the server's own statement and idle-in-transaction timeouts.
func boundPostgresConfig(cfg *pgx.ConnConfig) {
	cfg.ConnectTimeout = storeConnectTimeout
	dialer := &net.Dialer{Timeout: storeConnectTimeout, KeepAlive: storeKeepAlive, Control: tcpUserTimeout(storeTCPUserTimeout)}
	cfg.DialFunc = dialer.DialContext
	cfg.RuntimeParams["statement_timeout"] = strconv.Itoa(storeStatementTimeoutMs)
	cfg.RuntimeParams["idle_in_transaction_session_timeout"] = strconv.Itoa(storeIdleInTxTimeoutMs)
}

func openPostgresHandles(db DatabaseConfig) (*sql.DB, *sql.DB, error) {
	cfg, err := postgresStoreConfig(db)
	if err != nil {
		return nil, nil, err
	}
	w := stdlib.OpenDB(*cfg)
	w.SetMaxOpenConns(8)
	r := stdlib.OpenDB(*cfg.Copy())
	r.SetMaxOpenConns(4)
	return w, r, nil
}

// schemaProblem says why upstream's schema could not be made.
func schemaProblem(err error, db DatabaseConfig) *PostgresProblem {
	if code := postgresErrorCode(err); code == "42501" {
		return &PostgresProblem{"schema", "The database answers, but user " + db.PostgresUsername + " may not make or change Cosmos' tables in database " +
			postgresDatabaseName(db) + " (permission denied)."}
	}
	if p := postgresProblemOf(err, db); p != nil && p.Reason != "network" {
		return p
	}
	return &PostgresProblem{"schema", "The database answers, but Cosmos could not make its tables there."}
}

func setStoreState(change func(*MonitoringStoreState)) {
	storeStateMu.Lock()
	defer storeStateMu.Unlock()
	change(&storeState)
}

// GetMonitoringStoreState is the state now, with the dialect open.
func GetMonitoringStoreState() MonitoringStoreState {
	inUse := monitoringInUse()
	storeStateMu.Lock()
	defer storeStateMu.Unlock()
	s := storeState
	s.InUse = inUse
	return s
}

// monitoringInUse is the dialect open, "" when none; replaced by the tests.
var monitoringInUse = func() string {
	if _, err := MonitoringWriteDB(); err != nil {
		return ""
	}
	return MonitoringDialect()
}

// PauseMonitoringStore holds the watcher (a clean install deletes the
// files); the returned function lets it go again.
func PauseMonitoringStore() func() {
	was := storeWatchPaused.Swap(true)
	forkInitMu.Lock() // a look under way finishes first
	forkInitMu.Unlock()
	return func() { storeWatchPaused.Store(was) }
}

// MonitoringStorePartial: the store in use lacks part of the recent
// history (the local file while PostgreSQL does not answer): a check that
// judges by what was seen before cannot tell "never" from "elsewhere".
func MonitoringStorePartial() bool {
	storeStateMu.Lock()
	defer storeStateMu.Unlock()
	return storeState.Fallback
}

func kickMonitoringWatcher() {
	select {
	case storeKick <- struct{}{}:
	default:
	}
}

func startMonitoringWatcher() {
	storeWatchOnce.Do(func() { go watchMonitoringStore() })
}

// watchMonitoringStore is the one watcher: it never ends, one look at a time.
func watchMonitoringStore() {
	w := &monitoringWatch{backoff: timing().retryFirst}
	timer := time.NewTimer(timing().retryFirst)
	defer timer.Stop()
	for {
		kicked := false
		select {
		case <-timer.C:
		case <-storeKick:
			kicked = true
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		wait := timing().retryFirst
		if !storeWatchPaused.Load() {
			wait = w.safeLook(kicked)
		}
		timer.Reset(wait)
	}
}

type monitoringWatch struct {
	backoff  time.Duration
	failures int
	// good probes in a row while on the local file, and when the first was
	good      int
	goodSince time.Time
	// the last panic logged
	panicked string
}

// safeLook is look, a panic recovered and logged once per distinct one.
func (w *monitoringWatch) safeLook(kicked bool) (wait time.Duration) {
	defer func() {
		if r := recover(); r != nil {
			text := fmt.Sprint(r)
			if text != w.panicked {
				w.panicked = text
				Error("Monitoring database: a look of the watcher failed", errors.New(text))
			}
			wait = timing().retryFirst
		}
	}()
	return w.look(kicked)
}

// pingMonitoringStore pings the store in use; replaced by the tests.
var pingMonitoringStore = func(ctx context.Context) error {
	store, err := MonitoringWriteDB()
	if err != nil {
		return err
	}
	return store.PingContext(ctx)
}

func (w *monitoringWatch) grow() time.Duration {
	w.backoff *= 2
	if w.backoff > timing().retryMax {
		w.backoff = timing().retryMax
	}
	return w.backoff
}

// look does one look and says when the next one is due.
func (w *monitoringWatch) look(kicked bool) time.Duration {
	if kicked {
		w.backoff, w.failures, w.good = timing().retryFirst, 0, 0
	}
	adviseFallback()

	db := GetMainConfig().Database
	if db.PostgresHost == "" {
		return timing().idle
	}
	state := GetMonitoringStoreState()
	now := monitoringClock()

	switch {
	case state.Fallback:
		if kicked { // just fell back: the first try after the first delay
			setStoreState(func(s *MonitoringStoreState) { s.NextTry = now.Add(w.backoff) })
			return w.backoff
		}
		ctx, cancel := context.WithTimeout(context.Background(), timing().probe)
		problem := probeMonitoringPostgres(ctx, db)
		cancel()
		if problem != nil {
			w.good = 0
			wait := w.grow()
			setStoreState(func(s *MonitoringStoreState) {
				s.Reason, s.Message = problem.Reason, problem.Message
				s.Attempts++
				s.NextTry = now.Add(wait)
			})
			tellLastingOutage() // still failing: told once it has lasted
			return wait
		}
		// it answers: several times, over a while, and after a stay on the
		// local file that grows with the outages of the last hour
		if w.good == 0 {
			w.goodSince = now
		}
		w.good++
		storeStateMu.Lock()
		stay := timing().minStay << uint(max(0, min(len(recentOutages(now))-1, 6)))
		onLocal := storeOnLocal
		storeStateMu.Unlock()
		if stay > timing().maxStay {
			stay = timing().maxStay
		}
		ready := w.good >= timing().goodProbes && now.Sub(w.goodSince) >= timing().goodSpan && now.Sub(onLocal) >= stay
		if !ready {
			step := timing().goodSpan / time.Duration(max(1, timing().goodProbes-1))
			if left := stay - now.Sub(onLocal); w.good >= timing().goodProbes && left > step {
				step = left
			}
			setStoreState(func(s *MonitoringStoreState) { s.NextTry = now.Add(step) })
			return step
		}
		local, _ := MonitoringWriteDB() // the local file, open until the drain after a switch
		if err := InitMetricsDatabase(); err != nil {
			Error("Monitoring database: switching to PostgreSQL failed", err)
		}
		after := GetMonitoringStoreState()
		if after.Fallback || after.InUse != DialectPostgres {
			// it answered the probe but could not be opened: the local file
			// stayed the store; the back-off goes on
			w.good = 0
			wait := w.grow()
			setStoreState(func(s *MonitoringStoreState) { s.NextTry = now.Add(wait) })
			tellLastingOutage()
			return wait
		}
		storeStateMu.Lock()
		told := storeTold
		storeTold = false
		storeStateMu.Unlock()
		Log("Monitoring database: PostgreSQL at " + db.PostgresHost + " answers again; the store is PostgreSQL")
		if told {
			// the notice of the outage is closed where it was written
			monitoringResolveDown(local)
			monitoringTellBack(after, state.Since)
		}
		w.backoff, w.failures, w.good = timing().retryFirst, 0, 0
		return timing().pingEvery

	case state.InUse == "":
		// configured, and nothing open (closed for a clean install or a
		// test): open again
		if err := InitMetricsDatabase(); err != nil {
			Error("Monitoring database: opening the store failed", err)
		}
		return timing().retryFirst

	case state.InUse == DialectPostgres:
		ctx, cancel := context.WithTimeout(context.Background(), timing().pingWithin)
		err := pingMonitoringStore(ctx)
		cancel()
		if err == nil {
			w.failures = 0
			return timing().pingEvery
		}
		w.failures++
		if w.failures < timing().pingFailures {
			return timing().retryFirst
		}
		w.failures = 0
		Warn("Monitoring database: PostgreSQL at " + db.PostgresHost + " stopped answering; the local file takes over")
		if err := InitMetricsDatabase(); err != nil {
			Error("Monitoring database: opening the store failed", err)
		}
		return timing().retryFirst
	}
	return timing().idle
}

// monitoringAdviceFor is PostgresHostAdvice, bounded; replaced by the tests.
var monitoringAdviceFor = func(host string, reason string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return PostgresHostAdvice(ctx, host, reason)
}

// adviceKey: the outage and reason the advice was made for (the watcher only).
var adviceKey string

// adviseFallback names the likely right server once per outage and reason
// (asked of Docker, outside every lock of the store), again while it cannot
// name an address that answers (the container may still be starting).
func adviseFallback() {
	storeStateMu.Lock()
	s := storeState
	storeStateMu.Unlock()
	if !s.Fallback {
		adviceKey = ""
		return
	}
	key := s.Host + "|" + s.Reason + "|" + s.Since.String()
	if adviceKey == key {
		return
	}
	advice := monitoringAdviceFor(s.Host, s.Reason) // a panic here leaves the key unset: asked again
	adviceKey = key
	setStoreState(func(st *MonitoringStoreState) {
		if st.Fallback && st.Host == s.Host {
			st.Advice = advice
		}
	})
	if !strings.Contains(advice, "use ") {
		adviceKey = ""
	}
}

// tellLastingOutage tells an outage once it has lasted, while PostgreSQL
// still cannot be the store (the stay on the local file after it answers
// again is not an outage).
func tellLastingOutage() {
	storeStateMu.Lock()
	s := storeState
	due := s.Fallback && !storeTold && !s.Since.IsZero() && monitoringClock().Sub(s.Since) >= timing().tellAfter
	if due {
		storeTold = true
	}
	storeStateMu.Unlock()
	if due {
		monitoringTellDown(s)
	}
}

var monitoringResolveDown = func(local *sql.DB) { resolveThreadIn(local, DialectSQLite, monitoringStoreThread) }

// MonitoringStoreSentence is the plain line of the panel and the notice, in
// English (the panel has its own translation of it).
func MonitoringStoreSentence(s MonitoringStoreState) string {
	if !s.Fallback {
		return ""
	}
	return "The monitoring database at " + s.Host + " does not answer since " + s.Since.Local().Format("2006-01-02 15:04") +
		": " + s.Message + adviceSuffix(s) + " Records are kept in the local file (database.db) meanwhile; Cosmos tries again by itself."
}

func adviceSuffix(s MonitoringStoreState) string {
	if s.Advice == "" {
		return ""
	}
	return " " + s.Advice
}

func tellMonitoringDown(s MonitoringStoreState) {
	WriteNotification(Notification{Recipient: "admin", Level: "error", Fork: &NotificationInfo{
		Kind: "monitoring.store.down", Thread: monitoringStoreThread,
		Subject: &NotificationSubject{Type: "setting", Name: "Monitoring database", Link: "/cosmos-ui/config-general?tab=external"},
		What:    NText("notif.monitoring.store.down.what", "host", s.Host, "at", s.Since.Local().Format("15:04"), "reason", s.Message+adviceSuffix(s)),
		Means:   NText("notif.monitoring.store.down.means"),
		Doing:   NText("notif.monitoring.store.down.doing"),
		Steps:   []NotificationStep{{NotificationText: *NText("notif.step.monitoringdb.open"), Link: "/cosmos-ui/config-general?tab=external"}},
		Facts:   []NotificationFact{{Key: "host", Value: s.Host}, {Key: "reason", Value: s.Reason}},
	}})
}

func tellMonitoringBack(s MonitoringStoreState, since time.Time) {
	now := monitoringClock()
	WriteNotification(Notification{Recipient: "admin", Level: "success", Fork: &NotificationInfo{
		Kind: "monitoring.store.restored", Thread: monitoringStoreThread, Resolves: monitoringStoreThread,
		Subject: &NotificationSubject{Type: "setting", Name: "Monitoring database", Link: "/cosmos-ui/config-general?tab=external"},
		What:    NText("notif.monitoring.store.restored.what", "host", s.Host, "at", now.Local().Format("15:04")),
		Means: NText("notif.monitoring.store.restored.means", "duration", NotificationDuration(now.Sub(since)),
			"from", since.Local().Format("2006-01-02 15:04"), "to", now.Local().Format("2006-01-02 15:04")),
		Steps: []NotificationStep{{NotificationText: *NText("notif.step.monitoringdb.copy"), Link: "/cosmos-ui/config-general?tab=external"}},
	}})
}

var monitoringNotificationKinds = map[string]notificationKind{
	"monitoring.store.down":     {"monitoring", "notif.monitoring.store.down.title"},
	"monitoring.store.restored": {"monitoring", "notif.monitoring.store.restored.title"},
}

var monitoringNotificationTexts = map[string]string{
	"notif.monitoring.store.down.title":     "The monitoring database does not answer",
	"notif.monitoring.store.down.what":      "The monitoring database at {{host}} does not answer since {{at}}: {{reason}}",
	"notif.monitoring.store.down.means":     "Monitoring keeps recording into the local file (database.db) meanwhile: the charts, events and notifications of this period are there, not in PostgreSQL.",
	"notif.monitoring.store.down.doing":     "Cosmos tries the database again by itself, more and more seldom (at most every 5 minutes), and switches over once it has answered steadily for a minute.",
	"notif.monitoring.store.restored.title": "The monitoring database answers again",
	"notif.monitoring.store.restored.what":  "The monitoring database at {{host}} answers again since {{at}}: monitoring records into it again.",
	"notif.monitoring.store.restored.means": "It did not answer for {{duration}}. The records from {{from}} to {{to}} are in the local file (database.db).",
	"notif.step.monitoringdb.open":          "Open Configuration > External (the monitoring database) and check the server, the port and that the database runs",
	"notif.step.monitoringdb.copy":          "Open Configuration > External to copy the records of the local file into the database",
}

func init() {
	for kind, k := range monitoringNotificationKinds {
		notificationKinds[kind] = k
	}
	for key, text := range monitoringNotificationTexts {
		notificationTexts[key] = text
	}
}

// quoteIdent quotes a PostgreSQL identifier for a sentence the owner may
// paste ("my-user").
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// monitoringTables are the tables of the store a probe looks at.
var monitoringTables = []string{"monitoring_kv", "metrics", "metric_values", "events", "notifications", "fork_security_events"}

// CheckPostgresStore is CheckPostgres and more, what Cosmos' schema needs:
// the account may create tables (a table made and rolled back in one
// transaction), and the store's tables that exist already are its own (the
// schema adds columns and indexes to them). nil: it can be the store.
func CheckPostgresStore(ctx context.Context, db DatabaseConfig) *PostgresProblem {
	if problem := PostgresFieldsProblem(db); problem != "" {
		return &PostgresProblem{"settings", problem}
	}
	if db.PostgresHost == "" {
		return &PostgresProblem{"settings", "Enter the database server first"}
	}
	if db.PostgresUsername == "" {
		return &PostgresProblem{"settings", "Enter the user name to test the connection"}
	}
	cfg, err := postgresCheckConfig(db)
	if err != nil {
		return &PostgresProblem{"settings", "These settings do not make a database address"}
	}
	conn, err := pgxConnect(ctx, cfg)
	if err != nil {
		return postgresProblemOf(err, db)
	}
	defer conn.Close(context.Background())
	if err := conn.Ping(ctx); err != nil {
		return postgresProblemOf(err, db)
	}
	user, name := quoteIdent(db.PostgresUsername), postgresDatabaseName(db)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return postgresProblemOf(err, db)
	}
	defer tx.Rollback(context.Background())
	probe := "fork_cosmos_probe_" + strconv.FormatInt(time.Now().UnixNano()%1e9, 10)
	if _, err := tx.Exec(ctx, "CREATE TABLE "+probe+" (id BIGINT)"); err != nil {
		if code := postgresErrorCode(err); code == "42501" {
			return &PostgresProblem{"create", "User " + db.PostgresUsername + " may sign in but may not create tables in database " +
				name + ": run GRANT CREATE ON SCHEMA public TO " + user + " in that database, or make " + db.PostgresUsername +
				" its owner (ALTER DATABASE " + quoteIdent(name) + " OWNER TO " + user + ")."}
		}
		return postgresProblemOf(err, db)
	}
	rows, err := tx.Query(ctx, "SELECT tablename, tableowner FROM pg_tables WHERE schemaname = current_schema() AND tablename = ANY($1) AND NOT pg_has_role(current_user, tableowner, 'USAGE') ORDER BY tablename",
		monitoringTables)
	if err != nil {
		return postgresProblemOf(err, db)
	}
	foreign, owner := []string{}, ""
	for rows.Next() {
		var table, by string
		if rows.Scan(&table, &by) == nil {
			foreign = append(foreign, table)
			owner = by
		}
	}
	rows.Close()
	if len(foreign) > 0 {
		return &PostgresProblem{"owner", "The tables of Cosmos in database " + name + " (" + strings.Join(foreign, ", ") + ") belong to " +
			owner + ", not to user " + db.PostgresUsername + ": use " + owner + "'s account, or make " + db.PostgresUsername +
			" their owner (in that database, as " + owner + " or a superuser: REASSIGN OWNED BY " + quoteIdent(owner) + " TO " + user +
			", or ALTER TABLE … OWNER TO " + user + " for each)."}
	}
	return nil
}

// monitoringStoreHandle is for the tests: the handle open now.
func monitoringStoreHandle() *sql.DB {
	monitorMu.RLock()
	defer monitorMu.RUnlock()
	return monitorWriteDB
}
