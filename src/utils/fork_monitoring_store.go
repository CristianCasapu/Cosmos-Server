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
	"crypto/rand"
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
	bootGrace             time.Duration // after the start, back on the first good probe
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
	flapWindow: time.Hour, tellAfter: 2 * time.Minute, idle: time.Minute, drain: 2 * time.Second, bootGrace: 5 * time.Minute,
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
	// Aside: outages of other servers whose rows stay in the local file (the
	// settings changed while they lasted; review round 3, M8)
	Aside []AsideOutage `json:"aside,omitempty"`
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
	// storeBootUntil: a fallback at the start of the process (PostgreSQL
	// starting after Cosmos at a reboot) switches back on the first good
	// probe until then, and is not an outage (review round 2, S4a)
	storeBootUntil time.Time
	// storeLocalProblem: why the local file cannot be the store either
	storeLocalProblem string

	// monitorKey: what the open store is (dialect and address; internal only);
	// monitorID: its random name for clients, drawn at each swap (review
	// round 2, B1: nothing derived from a secret leaves the server); under
	// monitorMu
	monitorKey string
	monitorID  string

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
	prepareStoreSchema = preparePostgresSchema
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
		storeOnLocal = time.Time{}
		storeStateMu.Unlock()
		if err := openLocalStore(node); err != nil {
			return err
		}
		endFallbackForLocal() // an open outage ends: the settings ask for the local file (review round 3, S3)
		return nil
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
		swapped := false
		if err == nil {
			// a failure or a panic between the opening and the swap (the
			// schema included) does not leak the new pools (review round 2, M2)
			defer func() {
				if !swapped {
					w.Close()
					r.Close()
				}
			}()
			err = prepareStoreSchema(w, db)
		}
		if err == nil {
			swapMonitoringStore(w, r, DialectPostgres, key, node)
			swapped = true
			markPostgresInUse(db)
			Log("Monitoring database ready (" + DialectPostgres + ", node " + node + ")")
			// every switch to PostgreSQL ends the outages, whatever caused it
			// (the watcher, a save, the start): review round 3, S3
			endFallbacks(db, GetMonitoringStoreState())
			if opened := OnMonitoringStorePostgres; opened != nil {
				go opened() // a catch-up interrupted by a restart goes on (src/monitorcopy)
			}
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
			// a reboot, not an outage: the first fallback of the process only
			// (review round 3, M5)
			storeBootFallback = first
			if first {
				storeBootUntil = now.Add(timing().bootGrace)
			} else {
				storeOutages = append(recentOutages(now), now)
			}
		}
		s.Configured, s.Fallback = "postgres", true
		s.Host, s.Database = db.PostgresHost, postgresDatabaseName(db)
		s.Reason, s.Message = problem.Reason, problem.Message
		s.Attempts++
	})
	kickMonitoringWatcher()
	if err := openLocalStore(node); err != nil {
		localUnusable(err)
		return err
	}
	beginFallback(db) // recorded in the local file from its start (review round 3, S1, M2, S3)
	return nil
}

// localUnusable says, loudly and once per cause, that the local file cannot
// be the store either; the watcher tries it again.
func localUnusable(err error) {
	text := err.Error()
	storeStateMu.Lock()
	again := storeLocalProblem == text
	storeLocalProblem = text
	storeStateMu.Unlock()
	if !again {
		Error("Monitoring database: neither PostgreSQL nor the local file (database.db) can be the store; monitoring records nothing until one of them works. Cosmos tries both again", err)
	}
}

// localFileDSN is the local file's address.
func localFileDSN() string {
	// _txlock=immediate: a transaction takes the write lock when it begins and
	// waits for it (busy_timeout); a deferred one that reads first fails at
	// once with 517 when another connection wrote meanwhile (two handles of
	// the file during a drain, a copy, the fallback)
	return "file:" + CONFIGFOLDER + "database.db" +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_txlock=immediate"
}

// unresolvedOutage: the start of an outage notice left open in the local
// file (told before a restart), zero when none.
func unresolvedOutage(local *sql.DB) time.Time {
	var first sql.NullInt64
	if local == nil || local.QueryRow("SELECT MIN(CASE WHEN fork_first > 0 THEN fork_first ELSE date END) FROM notifications WHERE fork_thread = ? AND fork_resolved = 0 AND fork_info <> ''",
		monitoringStoreThread).Scan(&first) != nil || !first.Valid || first.Int64 <= 0 {
		return time.Time{}
	}
	return MillisToTime(first.Int64)
}

// ensureLocalStore makes the local file the store when no store answers.
// The state is read again under the lock: a save that made PostgreSQL the
// store meanwhile is not undone (review round 3, S4).
func ensureLocalStore() bool {
	forkInitMu.Lock()
	defer forkInitMu.Unlock()
	config := GetMainConfig()
	if !GetMonitoringStoreState().Fallback || config.Database.PostgresHost == "" {
		return true
	}
	if monitoringInUse() == DialectSQLite {
		return true
	}
	if err := openLocalStore(resolveNodeName(config)); err != nil {
		localUnusable(err)
		return false
	}
	storeStateMu.Lock()
	storeLocalProblem = ""
	storeStateMu.Unlock()
	beginFallback(config.Database)
	return true
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
	dsn := localFileDSN()
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
	// busy: an old handle in its drain, or a copy, writing the file (517, 5)
	for try := 1; ; try++ {
		err = initMetricsSchema(w, DialectSQLite)
		if err == nil || try >= 6 || !sqliteBusy(err) {
			break
		}
		time.Sleep(time.Duration(try) * 150 * time.Millisecond)
	}
	if err != nil {
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

// sqliteBusy: SQLite says another connection holds the file for now.
func sqliteBusy(err error) bool {
	text := err.Error()
	return strings.Contains(text, "SQLITE_BUSY") || strings.Contains(text, "database is locked") || strings.Contains(text, "(517)")
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
	registerHandles(dialect, w, r)
	monitorMu.Lock()
	oldW, oldR := monitorWriteDB, monitorReadDB
	monitorWriteDB, monitorReadDB = w, r
	monitorDialect, monitorKey, monitorNode = dialect, key, node
	monitorID = randomStoreID()
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
		forgetHandles(oldW, oldR)
	})
}

// MonitoringStoreID names the store in use for clients: a random value
// drawn at each opening of a store, related to nothing (review round 2,
// B1). Ids of notifications and the like are only meaningful within it.
func MonitoringStoreID() string {
	monitorMu.RLock()
	defer monitorMu.RUnlock()
	if monitorWriteDB == nil {
		return ""
	}
	return monitorID
}

func randomStoreID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
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

// schemaStatementTimeoutMs bounds a statement of the schema (a future
// migration of a large table must not die at the store's 30 s).
const schemaStatementTimeoutMs = 10 * 60 * 1000

// preparePostgresSchema makes upstream's schema through a connection of its
// own with a generous statement timeout (review round 2, M1); a race with
// another maker of the same table is tried again.
func preparePostgresSchema(_ *sql.DB, db DatabaseConfig) error {
	cfg, err := postgresStoreConfig(db)
	if err != nil {
		return err
	}
	cfg.RuntimeParams["statement_timeout"] = strconv.Itoa(schemaStatementTimeoutMs)
	long := stdlib.OpenDB(*cfg)
	defer long.Close()
	long.SetMaxOpenConns(1)
	return retrySchemaRace(func() error { return initMetricsSchema(long, DialectPostgres) })
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
	// until the catch-up of the rows written aside has ended (review round 3, M7)
	return storeState.Fallback || catchUpsRunning.Load() > 0
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
	state := GetMonitoringStoreState()
	now := monitoringClock()
	if db.PostgresHost == "" {
		// the local file is asked for: anything else is opened again (review
		// round 3, S4: the watcher converges from every state)
		if state.InUse != DialectSQLite || state.Fallback || state.Configured != "sqlite" {
			if err := InitMetricsDatabase(); err != nil {
				return w.grow()
			}
			return timing().retryFirst
		}
		return timing().idle
	}

	switch {
	case state.Fallback:
		if state.InUse != DialectSQLite && !ensureLocalStore() {
			// neither answers: said (once per cause), tried again
			return w.grow()
		}
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
			noteFallback(false)
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
		storeStateMu.Lock()
		boot := storeBootFallback && now.Before(storeBootUntil)
		storeStateMu.Unlock()
		ready := boot || w.good >= timing().goodProbes && now.Sub(w.goodSince) >= timing().goodSpan && now.Sub(onLocal) >= stay
		if !ready {
			step := timing().goodSpan / time.Duration(max(1, timing().goodProbes-1))
			if left := stay - now.Sub(onLocal); w.good >= timing().goodProbes && left > step {
				step = left
			}
			setStoreState(func(s *MonitoringStoreState) { s.NextTry = now.Add(step) })
			return step
		}
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
		// the switch ended the outage: its notice, the catch-up and "answers
		// again" are the opening's (endFallbacks), whatever caused it
		Log("Monitoring database: PostgreSQL at " + db.PostgresHost + " answers again; the store is PostgreSQL")
		w.backoff, w.failures, w.good = timing().retryFirst, 0, 0
		return timing().pingEvery

	case state.InUse == "":
		// configured, and nothing open (closed for a clean install, or the
		// local file failed too): open again
		if err := InitMetricsDatabase(); err != nil {
			return w.grow()
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
	// PostgreSQL is asked for, no fallback, and the local file is in use (a
	// look that raced a save, review round 3, S4): opened again
	if err := InitMetricsDatabase(); err != nil {
		return w.grow()
	}
	return timing().retryFirst
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
	// no switch while it is written: the notice goes into the local file
	forkInitMu.Lock()
	defer forkInitMu.Unlock()
	if monitoringInUse() != DialectSQLite {
		return
	}
	storeStateMu.Lock()
	s := storeState
	due := s.Fallback && !storeTold && !s.Since.IsZero() && monitoringClock().Sub(s.Since) >= timing().tellAfter
	if due {
		storeTold = true
	}
	storeStateMu.Unlock()
	if due {
		monitoringTellDown(s)
		noteFallback(true)
	}
}

// monitoringResolveDown closes the outage's notice in the local file,
// through a handle of its own (the store's old one may be draining), again
// while the file is busy (review round 2, M8).
var monitoringResolveDown = func() {
	local, err := sql.Open(DialectSQLite, localFileDSN())
	if err != nil {
		return
	}
	defer local.Close()
	for try := 1; try <= 5; try++ {
		resolveThreadIn(local, DialectSQLite, monitoringStoreThread)
		if unresolvedOutage(local).IsZero() {
			return
		}
		time.Sleep(time.Duration(try) * 200 * time.Millisecond)
	}
}

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

// CatchUpResult is what the catch-up after a fallback did, in numbers that
// are true (review round 3): Aside = Present + Missing + Unchecked.
type CatchUpResult struct {
	Ran bool // it ran (a manual copy running, or no copy machinery: false)
	// Counted: the records were counted (false: it stopped before; the
	// numbers below are then not known)
	Counted bool
	// Aside: the records the local file got during the fallback (counted
	// there: the rows with the fallback's ids, the metric rows of its time)
	Aside   int64
	Copied  int64 // rows this catch-up wrote into PostgreSQL
	Present int64 // records checked: in PostgreSQL, with their values
	Missing int64 // records checked: not in PostgreSQL (or not with their values)
	// Unchecked: records not checked (the catch-up stopped before its check)
	Unchecked int64
	// Inexact: hourly or daily totals that could not be added up exactly
	Inexact int64
	Problem string // why not, a fixed sentence ("" when nothing is missing)
}

// Complete: every record of the fallback is known to be in PostgreSQL with
// its value.
func (r CatchUpResult) Complete() bool {
	return r.Ran && r.Counted && r.Missing == 0 && r.Unchecked == 0 && r.Inexact == 0 && r.Problem == ""
}

// MonitoringCatchUp is set by src/monitorcopy: after a switch back to
// PostgreSQL it brings the rows of the window the local file got into the
// store in use (paced, idempotent, verified, recorded as automatic, in the
// background), then calls done once. nil: nothing is caught up.
var MonitoringCatchUp func(w CatchUpWindow, done func(CatchUpResult))

// OnMonitoringStorePostgres is set by src/monitorcopy: called (in its own
// goroutine) each time PostgreSQL becomes the store in use.
var OnMonitoringStorePostgres func()

func tellMonitoringBack(s MonitoringStoreState, since time.Time, caught CatchUpResult) {
	now := monitoringClock()
	means := NText("notif.monitoring.store.restored.means", "duration", NotificationDuration(now.Sub(since)),
		"from", since.Local().Format("2006-01-02 15:04"), "to", now.Local().Format("2006-01-02 15:04"))
	steps := []NotificationStep{{NotificationText: *NText("notif.step.monitoringdb.copy"), Link: "/cosmos-ui/config-general?tab=external"}}
	switch {
	case caught.Complete():
		means = NText("notif.monitoring.store.restored.caught", "duration", NotificationDuration(now.Sub(since)),
			"aside", strconv.FormatInt(caught.Aside, 10), "count", strconv.FormatInt(caught.Copied, 10))
		steps = nil
	case caught.Ran && !caught.Counted:
		means = NText("notif.monitoring.store.restored.unknown", "duration", NotificationDuration(now.Sub(since)), "problem", caught.Problem)
	case caught.Ran:
		problem := caught.Problem
		if caught.Inexact > 0 {
			problem = strings.TrimSpace(problem + " " + strconv.FormatInt(caught.Inexact, 10) +
				" hourly or daily totals could not be added up exactly (their kind of total cannot be merged).")
		}
		means = NText("notif.monitoring.store.restored.partly", "duration", NotificationDuration(now.Sub(since)),
			"aside", strconv.FormatInt(caught.Aside, 10), "present", strconv.FormatInt(caught.Present, 10),
			"missing", strconv.FormatInt(caught.Missing, 10), "unchecked", strconv.FormatInt(caught.Unchecked, 10), "problem", problem)
	}
	WriteNotification(Notification{Recipient: "admin", Level: "success", Fork: &NotificationInfo{
		Kind: "monitoring.store.restored", Thread: monitoringStoreThread, Resolves: monitoringStoreThread,
		Subject: &NotificationSubject{Type: "setting", Name: "Monitoring database", Link: "/cosmos-ui/config-general?tab=external"},
		What:    NText("notif.monitoring.store.restored.what", "host", s.Host, "at", now.Local().Format("15:04")),
		Means:   means,
		Steps:   steps,
	}})
}

var monitoringNotificationKinds = map[string]notificationKind{
	"monitoring.store.down":     {"monitoring", "notif.monitoring.store.down.title"},
	"monitoring.store.restored": {"monitoring", "notif.monitoring.store.restored.title"},
	"monitoring.store.ended":    {"monitoring", "notif.monitoring.store.ended.title"},
}

var monitoringNotificationTexts = map[string]string{
	"notif.monitoring.store.down.title":       "The monitoring database does not answer",
	"notif.monitoring.store.down.what":        "The monitoring database at {{host}} does not answer since {{at}}: {{reason}}",
	"notif.monitoring.store.down.means":       "Monitoring keeps recording into the local file (database.db) meanwhile: the charts, events and notifications of this period are there, not in PostgreSQL.",
	"notif.monitoring.store.down.doing":       "Cosmos tries the database again by itself, more and more seldom (at most every 5 minutes), and switches over once it has answered steadily for a minute.",
	"notif.monitoring.store.restored.title":   "The monitoring database answers again",
	"notif.monitoring.store.restored.what":    "The monitoring database at {{host}} answers again since {{at}}: monitoring records into it again.",
	"notif.monitoring.store.restored.means":   "It did not answer for {{duration}}. The records from {{from}} to {{to}} are in the local file (database.db).",
	"notif.monitoring.store.restored.caught":  "It did not answer for {{duration}}. The {{aside}} records written meanwhile into the local file are in the database now, checked one by one ({{count}} brought over by the catch-up): the history in the database has no gap.",
	"notif.monitoring.store.restored.partly":  "It did not answer for {{duration}}. {{aside}} records were written meanwhile into the local file: {{present}} are in the database now, {{missing}} are not, {{unchecked}} could not be checked. {{problem}}",
	"notif.monitoring.store.restored.unknown": "It did not answer for {{duration}}. The records written meanwhile into the local file could not be counted nor brought over now: {{problem}} Cosmos tries again the next time the database becomes the store.",
	"notif.monitoring.store.ended.title":      "The outage of the monitoring database is closed",
	"notif.monitoring.store.ended.other":      "The settings name another monitoring database now. The records written into the local file (database.db) while the earlier one did not answer stay there; Configuration > External can copy them.",
	"notif.monitoring.store.ended.local":      "Monitoring uses the local file (database.db) now, as the settings ask: the outage of the monitoring database is closed.",
	"notif.monitoring.store.ended.stale":      "Cosmos was not running for a long time during this outage of the monitoring database: it is closed here, and a new one is counted from now.",
	"notif.monitoring.store.ended.staleback":  "The monitoring database answers again. Cosmos was not running for a long time during its outage, so how long it lasted is not known; the records written meanwhile into the local file are brought over.",
	"notif.step.monitoringdb.open":            "Open Configuration > External (the monitoring database) and check the server, the port and that the database runs",
	"notif.step.monitoringdb.copy":            "Open Configuration > External to copy the records of the local file into the database",
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
	rows, err := tx.Query(ctx, ownershipQuery, monitoringTables)
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
		var super bool
		tx.QueryRow(ctx, "SELECT rolsuper FROM pg_roles WHERE rolname = $1", owner).Scan(&super)
		return &PostgresProblem{"owner", ownerAdvice(name, foreign, owner, super, db.PostgresUsername)}
	}
	return nil
}

// ownerAdvice says how to give the store's tables to the account. A
// superuser's tables (the bootstrap account's, commonly) are given one by one:
// REASSIGN OWNED BY is refused for that role, and a superuser's account is no
// account for a store (review round 2, M3).
func ownerAdvice(database string, tables []string, owner string, super bool, user string) string {
	who := quoteIdent(user)
	if super {
		alters := []string{}
		for _, t := range tables {
			alters = append(alters, "ALTER TABLE "+quoteIdent(t)+" OWNER TO "+who+";")
		}
		return "The tables of Cosmos in database " + database + " (" + strings.Join(tables, ", ") + ") belong to " + owner +
			", a superuser, not to user " + user + ": as " + owner + ", in that database, run " + strings.Join(alters, " ") +
			" (the sequences of events, notifications and fork_security_events follow their tables)."
	}
	return "The tables of Cosmos in database " + database + " (" + strings.Join(tables, ", ") + ") belong to " + owner +
		", not to user " + user + ": use " + owner + "'s account, or make " + user + " their owner (in that database, as " + owner +
		" or a superuser: REASSIGN OWNED BY " + quoteIdent(owner) + " TO " + who + ", or ALTER TABLE … OWNER TO " + who + " for each)."
}

// ownershipQuery finds the store's tables the account does not own.
const ownershipQuery = "SELECT tablename, tableowner FROM pg_tables WHERE schemaname = current_schema() AND tablename = ANY($1) AND NOT pg_has_role(current_user, tableowner, 'USAGE') ORDER BY tablename"

// monitoringStoreHandle is for the tests: the handle open now.
func monitoringStoreHandle() *sql.DB {
	monitorMu.RLock()
	defer monitorMu.RUnlock()
	return monitorWriteDB
}

// Every handle of the store knows its dialect (review round 2, S2): a writer
// that took a handle just before a swap keeps it for the drain, and must
// rebind for that handle, not for the store open now.
var handleDialects sync.Map // *sql.DB -> string

func registerHandles(dialect string, handles ...*sql.DB) {
	for _, h := range handles {
		if h != nil {
			handleDialects.Store(h, dialect)
		}
	}
}

func forgetHandles(handles ...*sql.DB) {
	for _, h := range handles {
		if h != nil {
			handleDialects.Delete(h)
		}
	}
}

// MonitoringDialectOf is the dialect of a handle of the store (the store's
// dialect now for a handle it does not know).
func MonitoringDialectOf(db *sql.DB) string {
	if d, ok := handleDialects.Load(db); ok {
		return d.(string)
	}
	return MonitoringDialect()
}

// MonitoringRebindFor is MonitoringRebind for the dialect of that handle.
func MonitoringRebindFor(db *sql.DB, query string) string {
	return rebindFor(MonitoringDialectOf(db), query)
}
