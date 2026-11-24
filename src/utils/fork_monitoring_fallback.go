package utils

// Fork addition (plan 51, review rounds 3 and 4): the record of a fallback,
// kept in the local file itself, and the end of an outage, whatever ends it.
//
// When the local file becomes the store because PostgreSQL does not answer,
// a record is written into the local file's monitoring_kv (a bookkeeping key
// of the copy, never copied): the server and database (and the server's
// identity, see below), the outage's start, whether it was told, when Cosmos
// last ran it, the local file's high-water marks (the highest id of events,
// notifications and security entries) and its own hour and day totals then.
// The rows the writers insert into the local file from then on have higher
// ids, whatever time they carry (an event kept in memory while the pings found
// PostgreSQL gone keeps its own time). Rows changed in place (a grouped
// notification) and the metric rows (natural keys) are taken by time, from
// before the fallback's start by the pings that found PostgreSQL gone.
//
// A record belongs to one server (review round 4, S1). Its fallback ends when
// PostgreSQL becomes the store again (that server or another one), when a
// fallback of another server takes the local file over, or when it is found
// stale: then, once nothing writes into the local file for it any more (after
// the switch's drain), its end marks and end time are taken, and the change of
// the local file's own hour and day totals during it is added to that server's
// part of them (`own:<server>:<bucket>`): the local file's rows are shared by
// every period on it, and a catch-up into a server adds that server's part
// only. Nothing of a record is widened later.
//
// The record survives a restart from the moment the fallback begins. A
// fallback of the same server and database seen running within the last day
// continues that outage (its start, "told"); an older one, or one of another
// server, is closed with words that fit, and a new outage begins. Every switch
// to PostgreSQL ends the outages, whatever caused it (the watcher, a save, the
// start): "told" reset, the notice closed, each record of that server caught
// up on its own, "answers again" told for a told outage. The records of
// another server stay in the local file as rows aside (the card says so) until
// that server is the store again; a move to "local file only" closes the open
// outage.
//
// A PostgreSQL store has an identity row in its own monitoring_kv (review
// round 4, M7): the ledgers of the copy and these parts are keyed by it, so
// the same database reached at another address keeps its ledger. The
// identities seen are remembered in the local file by address, for the
// fallbacks of a server that does not answer.

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// fallbackKey holds the records (keys of the copy's bookkeeping prefix:
	// the copy never copies them)
	fallbackKey = "fork_monitoring_copy:fallbacks"
	// identityKey: a PostgreSQL store's identity, in its own monitoring_kv
	identityKey = "fork_monitoring_copy:identity"
	// identitiesKey: in the local file, the identities seen, by address
	identitiesKey = "fork_monitoring_copy:identities"
	// OwnPrefix starts a server's part of the local file's hour and day
	// totals (own:<server>:<bucket>); PriorPrefix the part of a store's row
	// that went to another store at a change (prior:<bucket>, written by
	// src/metrics); both read by src/monitorcopy
	OwnPrefix   = "fork_monitoring_copy:own:"
	PriorPrefix = "fork_monitoring_copy:prior:"
)

// fallbackContinueWithin: an open fallback of the same server last seen
// running this recently is the same outage after a restart. A restart for an
// update takes minutes; a server switched off over a night still continues
// its outage; an older record is not taken as this outage.
const fallbackContinueWithin = 24 * time.Hour

// fallbackTables are the tables whose ids mark the rows a fallback wrote.
var fallbackTables = []string{"events", "notifications", "fork_security_events"}

// FallbackTables are those tables (for src/monitorcopy).
func FallbackTables() []string { return append([]string(nil), fallbackTables...) }

type fallbackRecord struct {
	Host     string `json:"host"`
	Database string `json:"database"`
	// Store: the server's ledger id when the fallback began
	Store string `json:"store,omitempty"`
	// Since: the outage's start (unix ms)
	Since int64 `json:"since"`
	// From: the window's start for rows taken by time (unix ms)
	From int64 `json:"from"`
	// After: the highest id per table when the local file became the store
	After map[string]int64 `json:"after"`
	// Start: the local file's own hour and day totals then (bucket -> value, count)
	Start map[string][2]int64 `json:"start,omitempty"`
	Told  bool                `json:"told,omitempty"`
	// Seen: the last moment Cosmos ran this fallback (unix ms)
	Seen int64 `json:"seen"`
	// Ended: its fallback ended (unix ms); Before and To are taken once nothing
	// writes for it any more, and never widened
	Ended  int64            `json:"ended,omitempty"`
	Before map[string]int64 `json:"before,omitempty"`
	To     int64            `json:"to,omitempty"`
}

func (r fallbackRecord) of(db DatabaseConfig) bool {
	return r.Host == db.PostgresHost && r.Database == postgresDatabaseName(db)
}

func (r fallbackRecord) same(o fallbackRecord) bool {
	return r.Host == o.Host && r.Database == o.Database && r.Since == o.Since
}

// AsideOutage is an outage of another server whose rows stay in the local
// file (the settings changed while it lasted).
type AsideOutage struct {
	Host     string    `json:"host"`
	Database string    `json:"database"`
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
}

// CatchUpWindow is what a catch-up brings over: the rows with ids in
// (After, Before] per table, the rows touched from From to To (metric rows,
// rows changed in place), and of the hour and day totals the part of server
// Store. Back is when its fallback ended.
type CatchUpWindow struct {
	From, To, Back, Since time.Time
	After, Before         map[string]int64
	Store                 string
}

var (
	// fallbackMu: one reader-writer of the records at a time
	fallbackMu sync.Mutex
	// catchUpsRunning: catch-ups handed over and not ended (the history in
	// use is partial until then)
	catchUpsRunning atomic.Int32
	// storeBootFallback: the fallback open now is the first of the process
	// (the boot grace applies to it only)
	storeBootFallback bool
	// identities: address -> ledger id of the PostgreSQL stores seen
	identities sync.Map
)

// fallbackLead is how long before the fallback's start PostgreSQL may have
// stopped taking writes (the pings that found it gone) plus a margin.
func fallbackLead() time.Duration {
	t := timing()
	return time.Duration(t.pingFailures)*(t.pingEvery+t.pingWithin) + 5*time.Second
}

// LedgerID names a store for the ledgers of the copy and the metrics'
// flush tokens: the local file by its path, PostgreSQL by its identity row
// (or, before it is known, by server, database and user). Nothing secret.
func LedgerID(dialect, identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return dialect + ":" + fmt.Sprintf("%x", sum[:8])
}

func addressOf(db DatabaseConfig) string {
	return db.PostgresHost + "/" + db.PostgresDatabase + "/" + db.PostgresUsername
}

// postgresLedgerID is the ledger id of a PostgreSQL store: by its identity
// when it was seen, else by its address.
func postgresLedgerID(db DatabaseConfig) string {
	if id, ok := identities.Load(addressOf(db)); ok {
		return id.(string)
	}
	var known map[string]string
	withLocalFile(false, func(local *sql.DB) error {
		var value string
		if local.QueryRow("SELECT value FROM monitoring_kv WHERE key = ?", identitiesKey).Scan(&value) == nil {
			json.Unmarshal([]byte(value), &known)
		}
		return nil
	})
	if id, ok := known[addressOf(db)]; ok && id != "" {
		identities.Store(addressOf(db), id)
		return id
	}
	return LedgerID(DialectPostgres, addressOf(db))
}

// storeIdentity reads, or makes, the identity row of a PostgreSQL store and
// remembers it for that address.
var storeIdentity = func(w *sql.DB, db DatabaseConfig) string {
	var id string
	err := w.QueryRow(rebindFor(DialectPostgres, "SELECT value FROM monitoring_kv WHERE key = ?"), identityKey).Scan(&id)
	if err == sql.ErrNoRows {
		b := make([]byte, 16)
		rand.Read(b)
		w.Exec(rebindFor(DialectPostgres, "INSERT INTO monitoring_kv(key, value) VALUES(?, ?) ON CONFLICT(key) DO NOTHING"), identityKey, hex.EncodeToString(b))
		err = w.QueryRow(rebindFor(DialectPostgres, "SELECT value FROM monitoring_kv WHERE key = ?"), identityKey).Scan(&id)
	}
	if err != nil || id == "" {
		return postgresLedgerID(db)
	}
	ledger := DialectPostgres + ":" + id
	if prev, ok := identities.Load(addressOf(db)); !ok || prev.(string) != ledger {
		identities.Store(addressOf(db), ledger)
		// remembered in the local file (made if need be: it is the store of
		// the fallbacks), for a fallback after a restart
		withLocalFile(true, func(local *sql.DB) error {
			if err := initMetricsSchema(local, DialectSQLite); err != nil {
				return err
			}
			known := map[string]string{}
			var value string
			if local.QueryRow("SELECT value FROM monitoring_kv WHERE key = ?", identitiesKey).Scan(&value) == nil {
				json.Unmarshal([]byte(value), &known)
			}
			known[addressOf(db)] = ledger
			encoded, _ := json.Marshal(known)
			_, err := local.Exec("INSERT INTO monitoring_kv(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", identitiesKey, string(encoded))
			return err
		})
	}
	return ledger
}

func localLedgerID() string { return LedgerID(DialectSQLite, CONFIGFOLDER+"database.db") }

// handleLedger: the ledger id of each handle of the store.
var handleLedger sync.Map // *sql.DB -> string

// MonitoringLedgerIDOf is the ledger id of a handle of the store ("" for a
// handle it does not know).
func MonitoringLedgerIDOf(db *sql.DB) string {
	if id, ok := handleLedger.Load(db); ok {
		return id.(string)
	}
	return ""
}

// withLocalFile runs fn with a handle of its own on the local file (the
// store's may be draining); nothing when the file does not exist and create
// is false.
func withLocalFile(create bool, fn func(local *sql.DB) error) error {
	if !create {
		if info, err := os.Stat(CONFIGFOLDER + "database.db"); err != nil || info.Size() == 0 {
			return nil
		}
	}
	local, err := sql.Open(DialectSQLite, localFileDSN())
	if err != nil {
		return err
	}
	defer local.Close()
	return fn(local)
}

// kvq is a *sql.DB or a *sql.Tx of the local file.
type kvq interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

// inLocalTx runs fn in one transaction of the local file.
func inLocalTx(create bool, fn func(tx kvq) error) error {
	return withLocalFile(create, func(local *sql.DB) error {
		tx, err := local.Begin()
		if err != nil {
			return err
		}
		if err := fn(tx); err != nil {
			tx.Rollback()
			return err
		}
		return tx.Commit()
	})
}

func loadFallbacks(q kvq) []fallbackRecord {
	var value string
	if q.QueryRow("SELECT value FROM monitoring_kv WHERE key = ?", fallbackKey).Scan(&value) != nil {
		return nil
	}
	out := []fallbackRecord{}
	if json.Unmarshal([]byte(value), &out) != nil {
		return nil
	}
	return out
}

func saveFallbacks(q kvq, records []fallbackRecord) error {
	if len(records) == 0 {
		_, err := q.Exec("DELETE FROM monitoring_kv WHERE key = ?", fallbackKey)
		return err
	}
	encoded, _ := json.Marshal(records)
	_, err := q.Exec("INSERT INTO monitoring_kv(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", fallbackKey, string(encoded))
	return err
}

// localMarks are the highest ids of the local file's tables (0 for a table
// not made yet).
func localMarks(q kvq) map[string]int64 {
	marks := map[string]int64{}
	for _, table := range fallbackTables {
		var n sql.NullInt64
		q.QueryRow("SELECT MAX(id) FROM " + table).Scan(&n)
		marks[table] = n.Int64
	}
	return marks
}

func minMarks(a, b map[string]int64) map[string]int64 {
	if a == nil {
		return b
	}
	out := map[string]int64{}
	for _, t := range fallbackTables {
		out[t] = a[t]
		if v, ok := b[t]; ok && v < out[t] {
			out[t] = v
		}
	}
	return out
}

// ParsePair reads "value|count".
func ParsePair(text string) ([2]int64, bool) {
	parts := strings.SplitN(text, "|", 2)
	if len(parts) != 2 {
		return [2]int64{}, false
	}
	v, err1 := strconv.ParseInt(parts[0], 10, 64)
	i, err2 := strconv.ParseInt(parts[1], 10, 64)
	return [2]int64{v, i}, err1 == nil && err2 == nil
}

// FormatPair writes "value|count".
func FormatPair(p [2]int64) string {
	return strconv.FormatInt(p[0], 10) + "|" + strconv.FormatInt(p[1], 10)
}

// ownTotals are the local file's own hour and day totals of the buckets
// dated from the day of from on: its rows, less the parts that went to
// another store at a change (prior keys).
func ownTotals(q kvq, from int64) map[string][2]int64 {
	day := int64(24 * time.Hour / time.Millisecond)
	since := from - from%day
	out := map[string][2]int64{}
	rows, err := q.Query("SELECT node, key, granularity, date, value, avg_index FROM metric_values WHERE granularity IN ('hour', 'day') AND date >= ?", since)
	if err != nil {
		return out
	}
	for rows.Next() {
		var node, key, gran string
		var date, value, index int64
		if rows.Scan(&node, &key, &gran, &date, &value, &index) == nil {
			out[node+"|"+key+"|"+gran+"|"+strconv.FormatInt(date, 10)] = [2]int64{value, index}
		}
	}
	rows.Close()
	prior, err := q.Query("SELECT key, value FROM monitoring_kv WHERE key >= ? AND key < ?", PriorPrefix, PriorPrefix+"~")
	if err != nil {
		return out
	}
	for prior.Next() {
		var key, value string
		if prior.Scan(&key, &value) != nil {
			continue
		}
		id := strings.TrimPrefix(key, PriorPrefix)
		if total, ok := out[id]; ok {
			if p, ok := ParsePair(value); ok {
				out[id] = [2]int64{total[0] - p[0], total[1] - p[1]}
			}
		}
	}
	prior.Close()
	return out
}

// fallbackStart is the local file at a fallback's start, taken before it
// takes writes: its marks, and its own totals from the earliest day an open
// record reaches back to.
type fallbackStart struct {
	ok     bool
	marks  map[string]int64
	totals map[string][2]int64
	from   int64
}

// prepareFallback takes it when the local file is not the store yet (once
// the switch to it is made, writers insert at once: marks taken after it
// would leave those rows in no window, review round 4). A fallback that
// ended a moment ago (a switch back whose drain is not over) is waited for:
// its stragglers first. forkInitMu held.
func prepareFallback() fallbackStart {
	if monitoringInUse() == DialectSQLite {
		return fallbackStart{}
	}
	waitLocalDrain(localFileDrained())
	storeStateMu.Lock()
	from := TimeToMillis(storeState.Since.Add(-fallbackLead()))
	storeStateMu.Unlock()
	st := fallbackStart{}
	fallbackMu.Lock()
	defer fallbackMu.Unlock()
	withLocalFile(false, func(local *sql.DB) error {
		for _, r := range loadFallbacks(local) {
			if r.Before == nil && r.From < from {
				from = r.From
			}
		}
		st = fallbackStart{ok: true, marks: localMarks(local), totals: ownTotals(local, from), from: from}
		return nil
	})
	if !st.ok { // no local file yet: nothing in it
		st = fallbackStart{ok: true, marks: map[string]int64{}, totals: map[string][2]int64{}, from: from}
	}
	return st
}

// afterLocalOpened runs between the switch to the local file and the
// record of the fallback; the tests write there, as the writers do.
var afterLocalOpened = func() {}

// closeRecord takes a record's end marks and end time and adds the change of
// the local file's own totals during its fallback to its server's part (at
// the given start of the next fallback when there is one).
func closeRecord(q kvq, r *fallbackRecord, now time.Time) error {
	return closeRecordAt(q, r, now, fallbackStart{})
}

func closeRecordAt(q kvq, r *fallbackRecord, now time.Time, at fallbackStart) error {
	marks, totals := at.marks, at.totals
	if !at.ok || at.from > r.From {
		marks, totals = localMarks(q), ownTotals(q, r.From)
	}
	r.Before = marks
	r.To = TimeToMillis(now.Add(time.Second))
	for id, end := range totals {
		start := r.Start[id]
		d := [2]int64{end[0] - start[0], end[1] - start[1]}
		if d == [2]int64{} {
			continue
		}
		key := OwnPrefix + r.Store + ":" + id
		var value string
		q.QueryRow("SELECT value FROM monitoring_kv WHERE key = ?", key).Scan(&value)
		had, _ := ParsePair(value)
		if _, err := q.Exec("INSERT INTO monitoring_kv(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
			key, FormatPair([2]int64{had[0] + d[0], had[1] + d[1]})); err != nil {
			return err
		}
	}
	r.Start = nil
	return nil
}

// asideOf are the records of servers other than db.
func asideOf(records []fallbackRecord, db DatabaseConfig) []AsideOutage {
	out := []AsideOutage{}
	for _, r := range records {
		if r.of(db) {
			continue
		}
		to := r.Ended
		if to == 0 {
			to = r.Seen
		}
		out = append(out, AsideOutage{Host: r.Host, Database: r.Database, From: MillisToTime(r.From), To: MillisToTime(to)})
	}
	return out
}

// beginFallback records the fallback open now (the local file is the store,
// storeState says which server). A fallback already recorded is only seen
// again; one of the same server seen running within a day is continued
// (start, told); the fallback of another server that was still open ends now
// (the local file stands in for this one from now on), as does a stale one of
// this server. forkInitMu held.
func beginFallback(db DatabaseConfig, start fallbackStart) {
	storeStateMu.Lock()
	since := storeState.Since
	storeStateMu.Unlock()
	now := monitoringClock()
	closeWhy := ""
	var continued *fallbackRecord
	fallbackMu.Lock()
	store := postgresLedgerID(db)
	inLocalTx(true, func(tx kvq) error {
		records := loadFallbacks(tx)
		kept := []fallbackRecord{}
		for _, r := range records {
			switch {
			case r.Ended != 0:
				// ended: its end is taken now at the latest, before this
				// fallback's rows (a switch back's drain may not be over), and
				// it waits for its server as it is
				if r.Before == nil {
					if err := closeRecordAt(tx, &r, now, start); err != nil {
						return err
					}
				}
				kept = append(kept, r)
			case !r.of(db):
				// another server's outage, left open by a change of the settings:
				// it ends here, before this one's rows
				r.Ended = TimeToMillis(now)
				if err := closeRecordAt(tx, &r, now, start); err != nil {
					return err
				}
				if r.Told {
					closeWhy, r.Told = "other", false
				}
				kept = append(kept, r)
			case r.Since == TimeToMillis(since):
				r.Seen = TimeToMillis(now) // this fallback, recorded already
				c := r
				continued = &c
			case now.Sub(MillisToTime(r.Seen)) < fallbackContinueWithin:
				r.Seen = TimeToMillis(now) // the same outage, Cosmos restarted while it lasted
				c := r
				continued = &c
			default:
				// an outage Cosmos stopped running long ago: it ends, as it was
				r.Ended = r.Seen
				if err := closeRecord(tx, &r, now); err != nil {
					return err
				}
				if r.Told {
					closeWhy, r.Told = "stale", false
				}
				kept = append(kept, r)
			}
		}
		if continued == nil {
			from := since.Add(-fallbackLead())
			rec := fallbackRecord{Host: db.PostgresHost, Database: postgresDatabaseName(db), Store: store, Since: TimeToMillis(since),
				From: TimeToMillis(from), After: localMarks(tx), Start: ownTotals(tx, TimeToMillis(from)), Seen: TimeToMillis(now)}
			if start.ok {
				// as the local file was before it took this fallback's writes
				rec.After, rec.Start = start.marks, start.totals
			}
			if closeWhy == "" && !unresolvedOutage(tx).IsZero() {
				closeWhy = "stale" // an open notice without a record (an older version)
			}
			continued = &rec
		}
		kept = append(kept, *continued)
		if err := saveFallbacks(tx, kept); err != nil {
			return err
		}
		setStoreState(func(s *MonitoringStoreState) { s.Aside = asideOf(kept, db) })
		return nil
	})
	fallbackMu.Unlock()
	if closeWhy != "" {
		monitoringResolveDown()
		tellMonitoringEnded(closeWhy)
	}
	if continued != nil && continued.Since != TimeToMillis(since) {
		storeStateMu.Lock()
		storeTold = continued.Told
		storeState.Since = MillisToTime(continued.Since)
		storeStateMu.Unlock()
	} else if continued != nil {
		storeStateMu.Lock()
		storeTold = storeTold || continued.Told
		storeStateMu.Unlock()
	}
}

// noteFallback writes what changed of the fallback open now into its record:
// told, and the moment it was last seen running (at most once a minute).
func noteFallback(told bool) {
	db := GetMainConfig().Database
	now := monitoringClock()
	fallbackMu.Lock()
	defer fallbackMu.Unlock()
	withLocalFile(false, func(local *sql.DB) error {
		records := loadFallbacks(local)
		for i := range records {
			if !records[i].of(db) || records[i].Ended != 0 {
				continue
			}
			if !told && now.Sub(MillisToTime(records[i].Seen)) < time.Minute {
				return nil
			}
			records[i].Seen = TimeToMillis(now)
			records[i].Told = records[i].Told || told
			return saveFallbacks(local, records)
		}
		return nil
	})
}

// endFallbacks: PostgreSQL db is the store now, whatever caused it. Every
// open fallback ends (its marks are taken after the switch's drain); each
// record of this server is caught up on its own, "answers again" told for
// the outage told now; another server's open outage is closed and its rows
// stay aside. forkInitMu held.
func endFallbacks(db DatabaseConfig, after MonitoringStoreState) {
	storeStateMu.Lock()
	wasTold := storeTold
	storeTold = false
	storeBootFallback = false
	storeStateMu.Unlock()
	now := monitoringClock()
	closeWhy := ""
	resolve := wasTold
	var endedNow *fallbackRecord // this server's fallback that ends now
	stale := false
	pending := false
	fallbackMu.Lock()
	withLocalFile(false, func(local *sql.DB) error {
		records := loadFallbacks(local)
		for i := range records {
			r := &records[i]
			if r.Ended == 0 {
				r.Ended = TimeToMillis(now)
				if !r.of(db) && r.Told {
					closeWhy, resolve, r.Told = "other", true, false
				}
				if r.of(db) {
					stale = now.Sub(MillisToTime(r.Seen)) >= fallbackContinueWithin
					c := *r
					endedNow = &c
				}
			}
			if r.of(db) {
				resolve = resolve || r.Told
				pending = true
			}
			if r.Before == nil {
				pending = true // its marks are taken after the drain
			}
		}
		if !resolve && !unresolvedOutage(local).IsZero() {
			resolve = true // an open notice without a record (an older version)
		}
		saveFallbacks(local, records)
		setStoreState(func(s *MonitoringStoreState) { s.Aside = asideOf(records, db) })
		return nil
	})
	fallbackMu.Unlock()
	if resolve {
		monitoringResolveDown()
	}
	if closeWhy != "" {
		tellMonitoringEnded(closeWhy)
	}
	if !pending {
		return
	}
	told := endedNow != nil && (endedNow.Told || wasTold) && !stale
	if stale && endedNow != nil && endedNow.Told {
		tellMonitoringEnded("staleback")
	}
	catchUpsRunning.Add(1)
	go catchUpFallback(db, endedNow, told, after, localFileDrained())
}

// catchUpFallback, once the switch's drain has ended (nothing writes into
// the local file for the fallbacks that ended any more): takes their end
// marks, then hands each record of server db to the catch-up on its own, and
// tells "answers again" for the outage that ended now.
var catchUpFallback = func(db DatabaseConfig, endedNow *fallbackRecord, told bool, after MonitoringStoreState, drained <-chan struct{}) {
	defer catchUpsRunning.Add(-1)
	// the local file's old handles closed (Close waits for the statements
	// running on them): no straggler of the fallback writes after the marks
	waitLocalDrain(drained)
	now := monitoringClock()
	mine := []fallbackRecord{}
	fallbackMu.Lock()
	closeErr := inLocalTx(false, func(tx kvq) error {
		records := loadFallbacks(tx)
		for i := range records {
			if records[i].Ended != 0 && records[i].Before == nil {
				if err := closeRecord(tx, &records[i], now); err != nil {
					return err
				}
			}
			// this server's fallbacks that ended (never the one open now: a
			// new outage may have begun meanwhile)
			if records[i].of(db) && records[i].Ended != 0 && records[i].Before != nil {
				records[i].Told = false // told below, once
				mine = append(mine, records[i])
			}
		}
		return saveFallbacks(tx, records)
	})
	fallbackMu.Unlock()
	if closeErr != nil {
		Error("Monitoring database: the end of a fallback could not be recorded in the local file", closeErr)
	}
	pruneLocalBookkeeping(now)
	for _, rec := range mine {
		rec := rec
		tellNow := told && endedNow != nil && rec.same(*endedNow)
		window := CatchUpWindow{From: MillisToTime(rec.From), To: MillisToTime(rec.To), Back: MillisToTime(rec.Ended),
			Since: MillisToTime(rec.Since), After: rec.After, Before: rec.Before, Store: rec.Store}
		catchUpsRunning.Add(1)
		finish := func(caught CatchUpResult) {
			defer catchUpsRunning.Add(-1) // after "answers again": the history is partial until then
			if caught.Ran {
				forgetFallback(rec)
			}
			if tellNow {
				monitoringTellBack(after, MillisToTime(rec.Since), caught)
			}
		}
		if catchUp := MonitoringCatchUp; catchUp != nil {
			catchUp(window, finish)
		} else {
			finish(CatchUpResult{})
		}
	}
	if told && endedNow != nil {
		found := false
		for _, rec := range mine {
			found = found || rec.same(*endedNow)
		}
		if !found {
			monitoringTellBack(after, MillisToTime(endedNow.Since), CatchUpResult{})
		}
	}
}

// forgetFallback removes the record of a fallback whose rows the catch-up
// took (done, or kept in PostgreSQL's list of windows to take up again).
func forgetFallback(rec fallbackRecord) {
	fallbackMu.Lock()
	defer fallbackMu.Unlock()
	withLocalFile(false, func(local *sql.DB) error {
		records := loadFallbacks(local)
		kept := []fallbackRecord{}
		for _, r := range records {
			if !r.same(rec) {
				kept = append(kept, r)
			}
		}
		return saveFallbacks(local, kept)
	})
}

// endFallbackForLocal: the settings ask for the local file only: an open
// outage ends (its rows are in the store in use now). forkInitMu held.
func endFallbackForLocal() {
	storeStateMu.Lock()
	wasTold := storeTold
	storeTold = false
	storeBootFallback = false
	storeStateMu.Unlock()
	told := wasTold
	fallbackMu.Lock()
	withLocalFile(false, func(local *sql.DB) error {
		records := loadFallbacks(local)
		kept := []fallbackRecord{}
		for _, r := range records {
			if r.Ended == 0 {
				told = told || r.Told
				continue
			}
			kept = append(kept, r)
		}
		if !told && !unresolvedOutage(local).IsZero() {
			told = true
		}
		return saveFallbacks(local, kept)
	})
	fallbackMu.Unlock()
	if told {
		monitoringResolveDown()
		tellMonitoringEnded("local")
	}
}

// tellMonitoringEnded closes an outage that did not end by its server
// answering again.
var tellMonitoringEnded = func(why string) {
	WriteNotification(Notification{Recipient: "admin", Level: "info", Fork: &NotificationInfo{
		Kind: "monitoring.store.ended", Thread: monitoringStoreThread, Resolves: monitoringStoreThread,
		Subject: &NotificationSubject{Type: "setting", Name: "Monitoring database", Link: "/cosmos-ui/config-general?tab=external"},
		What:    NText("notif.monitoring.store.ended." + why),
	}})
}

// BucketKeyExpired: a bookkeeping key ending in a bucket id
// ("node|key|granularity|date") whose bucket the retention has removed (an
// hour row lives 48 hours after its hour, a day row 30 days after its day,
// a raw row an hour), with a day of margin.
func BucketKeyExpired(key string, now time.Time) bool {
	parts := strings.Split(key, "|")
	if len(parts) < 4 {
		return false
	}
	date, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	if err != nil {
		return false
	}
	keep := 2 * time.Hour
	switch parts[len(parts)-2] {
	case "hour":
		keep = time.Hour + 48*time.Hour + 24*time.Hour
	case "day":
		keep = 24*time.Hour + 30*24*time.Hour + 24*time.Hour
	}
	return MillisToTime(date).Add(keep).Before(now)
}

// PruneBucketKeys removes from a store's monitoring_kv the keys under prefix
// whose bucket the retention has removed (review round 4, M3): it looks at
// most at scan keys and removes at most remove of them per call.
func PruneBucketKeys(db *sql.DB, dialect, prefix string, now time.Time, scan, remove int) (int, error) {
	expired := []interface{}{}
	after := prefix
	for seen := 0; seen < scan && len(expired) < remove; {
		rows, err := db.Query(rebindFor(dialect, "SELECT key FROM monitoring_kv WHERE key > ? AND key < ? ORDER BY key LIMIT 1000"), after, prefix+"~")
		if err != nil {
			return 0, err
		}
		n := 0
		for rows.Next() {
			var key string
			if rows.Scan(&key) != nil {
				continue
			}
			n++
			after = key
			if BucketKeyExpired(key, now) && len(expired) < remove {
				expired = append(expired, key)
			}
		}
		rows.Close()
		seen += n
		if n < 1000 {
			break
		}
	}
	removed := 0
	for start := 0; start < len(expired); start += 500 {
		end := min(start+500, len(expired))
		holes := strings.TrimSuffix(strings.Repeat("?, ", end-start), ", ")
		res, err := db.Exec(rebindFor(dialect, "DELETE FROM monitoring_kv WHERE key IN ("+holes+")"), expired[start:end]...)
		if err != nil {
			return removed, err
		}
		if n, e := res.RowsAffected(); e == nil {
			removed += int(n)
		}
	}
	return removed, nil
}

// pruneLocalBookkeeping removes the expired parts and notes of the local file.
func pruneLocalBookkeeping(now time.Time) {
	withLocalFile(false, func(local *sql.DB) error {
		for _, prefix := range []string{OwnPrefix, PriorPrefix} {
			PruneBucketKeys(local, DialectSQLite, prefix, now, 50000, 5000)
		}
		return nil
	})
}
