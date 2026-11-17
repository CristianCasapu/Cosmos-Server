package utils

// Fork addition (plan 51): monitoring never runs without a store.
//
// Upstream's InitMetricsDatabase closes the open store first and then opens
// the configured one; when that is a PostgreSQL that does not answer, the
// function returned with no store open at all: metrics, events,
// notifications and the security record were dropped from then on, silently,
// until the next restart with working settings.
//
// The two hooks of InitMetricsDatabase (database.go):
//
//   - forkMonitoringInitLock: one opening at a time (the op-log's reaction,
//     the start, and the watcher below may ask at once).
//   - forkMonitoringConfig: when PostgreSQL is configured it is asked first,
//     bounded, outside the store's lock (connect, sign in, may create tables:
//     what the schema needs). When it does not answer, the configuration
//     handed to upstream's body names no PostgreSQL, so the local file
//     (database.db) is opened instead, and the state says so.
//
// One watcher goroutine for the life of the process then keeps trying
// PostgreSQL with a back-off (5 s doubling up to 5 min, for ever) and switches
// over when it answers; while PostgreSQL is in use it pings it, and two
// failed pings in a row reopen the store (which then falls back). The
// administrators are told when the fallback starts (written into the local
// file, the store in use then) and when PostgreSQL answers again (the first
// one resolved in the local file before the switch, the second written into
// PostgreSQL after it, with the period whose records stayed in the file).
//
// Records made meanwhile stay in the local file: switching back shows them
// again, and the history copy (src/monitorcopy) can bring them over.

import (
	"context"
	"database/sql"
	"strconv"
	"sync"
	"time"
)

const (
	monitoringProbeAtStart   = 5 * time.Second
	monitoringProbeTimeout   = 8 * time.Second
	monitoringRetryFirst     = 5 * time.Second
	monitoringRetryMax       = 5 * time.Minute
	monitoringPingEvery      = 15 * time.Second
	monitoringPingTimeout    = 5 * time.Second
	monitoringPingFailures   = 2
	monitoringStoreThread    = "monitoring.store"
	monitoringIdleLook       = time.Minute
)

// MonitoringStoreState is what the panel shows about the store. No secret.
type MonitoringStoreState struct {
	// Configured: "sqlite" or "postgres", what the settings ask for
	Configured string `json:"configured"`
	// InUse: the dialect open now ("sqlite", "pgx"), "" when none is open
	InUse    string `json:"inUse"`
	Host     string `json:"host,omitempty"`
	Database string `json:"database,omitempty"`
	// Fallback: PostgreSQL is configured but does not answer; the local file
	// is used meanwhile
	Fallback bool      `json:"fallback"`
	Since    time.Time `json:"since,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	Message  string    `json:"message,omitempty"`
	Attempts int       `json:"attempts,omitempty"`
	NextTry  time.Time `json:"nextTry,omitempty"`
	// Healed: PostgreSQL answered again after a fallback; the records of
	// LocalFrom..LocalTo are in the local file
	LocalFrom time.Time `json:"localFrom,omitempty"`
	LocalTo   time.Time `json:"localTo,omitempty"`
}

var (
	forkInitMu sync.Mutex

	storeStateMu sync.Mutex
	storeState   = MonitoringStoreState{Configured: "sqlite"}
	// storeStarted: the first opening (the start of the server) is done
	storeStarted bool
	// storeNoticePending: the fallback started and is not told yet
	storeNoticePending bool
	// storeForceLocal: PostgreSQL answered but its schema could not be made
	// (the store was left closed): the next opening takes the local file
	storeForceLocal bool

	storeWatchOnce sync.Once
	storeKick      = make(chan struct{}, 1)

	// probeMonitoringPostgres asks the database whether it can be the store;
	// replaced by the tests.
	probeMonitoringPostgres = CheckPostgresStore
	// monitoringClock is the time; replaced by the tests.
	monitoringClock = time.Now
	// monitoringTellDown / monitoringTellBack tell the administrators;
	// replaced by the tests.
	monitoringTellDown = tellMonitoringDown
	monitoringTellBack = tellMonitoringBack
)

// forkMonitoringInitLock takes the opening lock and returns its release:
// `defer forkMonitoringInitLock()()`.
func forkMonitoringInitLock() func() {
	forkInitMu.Lock()
	return forkInitMu.Unlock
}

// forkMonitoringConfig is the hook of InitMetricsDatabase: the configuration
// its body opens. Called with forkInitMu held, monitorMu not held.
func forkMonitoringConfig(config Config) Config {
	startMonitoringWatcher()
	db := config.Database

	storeStateMu.Lock()
	first := !storeStarted
	storeStarted = true
	storeStateMu.Unlock()

	if db.PostgresHost == "" {
		setStoreState(func(s *MonitoringStoreState) {
			*s = MonitoringStoreState{Configured: "sqlite"}
		})
		return config
	}

	storeStateMu.Lock()
	forceLocal := storeForceLocal
	storeForceLocal = false
	storeStateMu.Unlock()

	var problem *PostgresProblem
	if forceLocal {
		problem = &PostgresProblem{"schema", "The database answers, but Cosmos could not make its tables there (the account may not create or change tables in that database)."}
	} else {
		timeout := monitoringProbeTimeout
		if first {
			timeout = monitoringProbeAtStart
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		problem = probeMonitoringPostgres(ctx, db)
		cancel()
	}

	now := monitoringClock()
	if problem == nil {
		setStoreState(func(s *MonitoringStoreState) {
			healed := s.Fallback && s.Host == db.PostgresHost && s.Database == postgresDatabaseName(db)
			from := s.Since
			*s = MonitoringStoreState{Configured: "postgres", Host: db.PostgresHost, Database: postgresDatabaseName(db)}
			if healed {
				s.LocalFrom, s.LocalTo = from, now
			}
		})
		return config
	}

	Warn("Monitoring database: PostgreSQL at " + db.PostgresHost + " does not answer (" + problem.Reason + "): the local file is used meanwhile")
	setStoreState(func(s *MonitoringStoreState) {
		same := s.Fallback && s.Host == db.PostgresHost && s.Database == postgresDatabaseName(db)
		if !same {
			*s = MonitoringStoreState{Since: now}
			storeNoticePending = true
		}
		s.Configured, s.Fallback = "postgres", true
		s.Host, s.Database = db.PostgresHost, postgresDatabaseName(db)
		s.Reason, s.Message = problem.Reason, problem.Message
		s.Attempts++
	})
	kickMonitoringWatcher()
	config.Database.PostgresHost = ""
	return config
}

func setStoreState(change func(*MonitoringStoreState)) {
	storeStateMu.Lock()
	defer storeStateMu.Unlock()
	change(&storeState)
}

// GetMonitoringStoreState is the state now, with the dialect open.
func GetMonitoringStoreState() MonitoringStoreState {
	inUse := ""
	if _, err := MonitoringWriteDB(); err == nil {
		inUse = MonitoringDialect()
	}
	storeStateMu.Lock()
	defer storeStateMu.Unlock()
	s := storeState
	s.InUse = inUse
	return s
}

// MonitoringStoreChanged is told by the settings save that the store's
// settings changed: the watcher's back-off starts again.
func MonitoringStoreChanged() { kickMonitoringWatcher() }

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
	defer func() {
		if r := recover(); r != nil {
			Error("Monitoring database: the watcher stopped", nil)
		}
	}()
	w := &monitoringWatch{backoff: monitoringRetryFirst}
	timer := time.NewTimer(monitoringRetryFirst)
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
		wait := w.look(kicked)
		timer.Reset(wait)
	}
}

type monitoringWatch struct {
	backoff  time.Duration
	failures int
}

// look does one look and says when the next one is due.
func (w *monitoringWatch) look(kicked bool) time.Duration {
	if kicked {
		w.backoff, w.failures = monitoringRetryFirst, 0
	}
	tellPendingMonitoringNotice()

	db := GetMainConfig().Database
	if db.PostgresHost == "" {
		return monitoringIdleLook
	}
	state := GetMonitoringStoreState()

	switch {
	case state.Fallback:
		if kicked { // just fell back: the first try after the first delay
			setStoreState(func(s *MonitoringStoreState) { s.NextTry = monitoringClock().Add(w.backoff) })
			return w.backoff
		}
		ctx, cancel := context.WithTimeout(context.Background(), monitoringProbeTimeout)
		problem := probeMonitoringPostgres(ctx, db)
		cancel()
		if problem != nil {
			w.backoff *= 2
			if w.backoff > monitoringRetryMax {
				w.backoff = monitoringRetryMax
			}
			setStoreState(func(s *MonitoringStoreState) {
				s.Reason, s.Message = problem.Reason, problem.Message
				s.Attempts++
				s.NextTry = monitoringClock().Add(w.backoff)
			})
			return w.backoff
		}
		// it answers: the notice of the fallback is closed where it was
		// written (the local file), then the store moves
		since := state.Since
		monitoringResolveDown()
		if err := InitMetricsDatabase(); err != nil {
			Error("Monitoring database: switching to PostgreSQL failed", err)
		}
		after := GetMonitoringStoreState()
		if after.Fallback || after.InUse != DialectPostgres {
			// it answered the probe but could not be opened: the local
			// file again at once, and the back-off goes on
			if after.InUse == "" {
				reopenLocal()
			}
			setStoreState(func(s *MonitoringStoreState) {
				s.NextTry = monitoringClock().Add(w.backoff)
				s.Since = since // the same outage
			})
			storeStateMu.Lock()
			storeNoticePending = true // the notice closed above is told again
			storeStateMu.Unlock()
			return w.backoff
		}
		Log("Monitoring database: PostgreSQL at " + db.PostgresHost + " answers again; the store is PostgreSQL")
		monitoringTellBack(after, since)
		w.backoff, w.failures = monitoringRetryFirst, 0
		return monitoringPingEvery

	case state.InUse == "":
		// configured, and nothing open: the schema could not be made after
		// the probe (or the database stopped in between): the local file
		reopenLocal()
		return monitoringRetryFirst

	case state.InUse == DialectPostgres:
		store, err := MonitoringWriteDB()
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), monitoringPingTimeout)
			err = store.PingContext(ctx)
			cancel()
		}
		if err == nil {
			w.failures = 0
			return monitoringPingEvery
		}
		w.failures++
		if w.failures < monitoringPingFailures {
			return monitoringRetryFirst
		}
		w.failures = 0
		Warn("Monitoring database: PostgreSQL at " + db.PostgresHost + " stopped answering; opening the store again")
		if err := InitMetricsDatabase(); err != nil {
			Error("Monitoring database: opening again failed", err)
		}
		return monitoringRetryFirst
	}
	return monitoringIdleLook
}

// reopenLocal opens the local file after PostgreSQL was left closed.
func reopenLocal() {
	storeStateMu.Lock()
	storeForceLocal = true
	storeStateMu.Unlock()
	if err := InitMetricsDatabase(); err != nil {
		Error("Monitoring database: opening the local file failed", err)
	}
}

// tellPendingMonitoringNotice tells the fallback once the local file is open.
func tellPendingMonitoringNotice() {
	storeStateMu.Lock()
	pending := storeNoticePending && storeState.Fallback
	storeNoticePending = false
	s := storeState
	storeStateMu.Unlock()
	if pending {
		monitoringTellDown(s)
	}
}

var monitoringResolveDown = func() { ResolveThread(monitoringStoreThread) }

// MonitoringStoreSentence is the plain line of the panel and the notice, in
// English (the panel has its own translation of it).
func MonitoringStoreSentence(s MonitoringStoreState) string {
	if !s.Fallback {
		return ""
	}
	return "The monitoring database at " + s.Host + " does not answer since " + s.Since.Local().Format("2006-01-02 15:04") +
		": " + s.Message + " Records are kept in the local file (database.db) meanwhile; Cosmos tries again by itself."
}

func tellMonitoringDown(s MonitoringStoreState) {
	WriteNotification(Notification{Recipient: "admin", Level: "error", Fork: &NotificationInfo{
		Kind: "monitoring.store.down", Thread: monitoringStoreThread,
		Subject: &NotificationSubject{Type: "setting", Name: "Monitoring database", Link: "/cosmos-ui/config-general?tab=external"},
		What:    NText("notif.monitoring.store.down.what", "host", s.Host, "at", s.Since.Local().Format("15:04"), "reason", s.Message),
		Means:   NText("notif.monitoring.store.down.means"),
		Doing:   NText("notif.monitoring.store.down.doing"),
		Steps: []NotificationStep{{NotificationText: *NText("notif.step.monitoringdb.open"), Link: "/cosmos-ui/config-general?tab=external"}},
		Facts: []NotificationFact{{Key: "host", Value: s.Host}, {Key: "reason", Value: s.Reason}},
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
	"notif.monitoring.store.down.doing":     "Cosmos tries the database again by itself, more and more seldom (at most every 5 minutes), and switches over as soon as it answers.",
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

// CheckPostgresStore is CheckPostgres and more: the account may create the
// store's tables in that database (a table made and rolled back in one
// transaction). nil: it can be the monitoring store.
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
	tx, err := conn.Begin(ctx)
	if err != nil {
		return postgresProblemOf(err, db)
	}
	defer tx.Rollback(context.Background())
	probe := "fork_cosmos_probe_" + strconv.FormatInt(time.Now().UnixNano()%1e9, 10)
	if _, err := tx.Exec(ctx, "CREATE TABLE "+probe+" (id BIGINT)"); err != nil {
		if code := postgresErrorCode(err); code == "42501" {
			return &PostgresProblem{"create", "User " + db.PostgresUsername + " may sign in but may not create tables in database " +
				postgresDatabaseName(db) + ": run GRANT CREATE ON SCHEMA public TO " + db.PostgresUsername +
				" in that database, or make " + db.PostgresUsername + " its owner (ALTER DATABASE " + postgresDatabaseName(db) + " OWNER TO " + db.PostgresUsername + ")."}
		}
		return postgresProblemOf(err, db)
	}
	return nil
}

// monitoringStoreHandle is for the tests: the handle open now.
func monitoringStoreHandle() *sql.DB {
	monitorMu.RLock()
	defer monitorMu.RUnlock()
	return monitorWriteDB
}
