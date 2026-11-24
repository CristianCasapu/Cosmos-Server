package utils

// Fork addition (plan 51, review round 3): the record of a fallback, kept in
// the local file itself, and the end of an outage, whatever ends it.
//
// When the local file becomes the store because PostgreSQL does not answer,
// a record is written into the local file's monitoring_kv (a bookkeeping key
// of the copy, never copied): the server and database, the outage's start,
// whether it was told, when Cosmos last ran it, and the local file's
// high-water marks (the highest id of events, notifications and security
// entries). The rows the writers insert into the local file from then on have
// higher ids, whatever time they carry (an event kept in memory while the
// pings found PostgreSQL gone keeps its own time): the rows of the fallback are
// the ids between the marks taken at its start and the marks taken after the
// switch back and its drain. Rows changed in place (a grouped notification)
// and the metric rows (natural keys) are taken by time, from before the
// fallback's start by the pings that found PostgreSQL gone.
//
// The record survives a restart from the moment the fallback begins (review
// round 3, M2). A fallback of the same server and database seen running
// within the last day continues that outage (its start, "told"); an older one,
// or one of another server, is closed with words that fit, and a new outage
// begins (S3). Every switch to PostgreSQL ends the outages of the records,
// whatever caused it (the watcher, a save, the start): "told" reset, the
// notice closed, the rows of the same server's fallback caught up, "answers
// again" told for a told outage (S3). The records of another server stay in
// the local file as rows aside (the card says so, M8) until that server is
// the store again; a move to "local file only" closes the open outage.

import (
	"database/sql"
	"encoding/json"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// fallbackKey holds the records (a key of the copy's bookkeeping prefix:
// the copy never copies it).
const fallbackKey = "fork_monitoring_copy:fallbacks"

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
	// Since: the outage's start (unix ms)
	Since int64 `json:"since"`
	// From: the window's start for rows taken by time (unix ms)
	From int64 `json:"from"`
	// After: the highest id per table when the local file became the store
	After map[string]int64 `json:"after"`
	Told  bool             `json:"told,omitempty"`
	// Seen: the last moment Cosmos ran this fallback (unix ms)
	Seen int64 `json:"seen"`
	// Ended: PostgreSQL (this or another server) became the store (unix ms)
	Ended int64 `json:"ended,omitempty"`
	// Before and To: the marks and the time after the switch back's drain
	Before map[string]int64 `json:"before,omitempty"`
	To     int64            `json:"to,omitempty"`
}

func (r fallbackRecord) of(db DatabaseConfig) bool {
	return r.Host == db.PostgresHost && r.Database == postgresDatabaseName(db)
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
// (After, Before] per table, and the rows touched from From to To (the metric
// rows, rows changed in place). Back is when PostgreSQL became the store again.
type CatchUpWindow struct {
	From, To, Back time.Time
	After, Before  map[string]int64
}

var (
	// fallbackMu: one reader-writer of the records at a time
	fallbackMu sync.Mutex
	// catchUpsRunning: catch-ups handed over and not ended (the history in
	// use is partial until then, M7)
	catchUpsRunning atomic.Int32
	// storeBootFallback: the fallback open now is the first of the process
	// (the boot grace applies to it only, M5)
	storeBootFallback bool
)

// fallbackLead is how long before the fallback's start PostgreSQL may have
// stopped taking writes (the pings that found it gone) plus a margin.
func fallbackLead() time.Duration {
	t := timing()
	return time.Duration(t.pingFailures)*(t.pingEvery+t.pingWithin) + 5*time.Second
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

func loadFallbacks(local *sql.DB) []fallbackRecord {
	var value string
	if local.QueryRow("SELECT value FROM monitoring_kv WHERE key = ?", fallbackKey).Scan(&value) != nil {
		return nil
	}
	out := []fallbackRecord{}
	if json.Unmarshal([]byte(value), &out) != nil {
		return nil
	}
	return out
}

func saveFallbacks(local *sql.DB, records []fallbackRecord) error {
	if len(records) == 0 {
		_, err := local.Exec("DELETE FROM monitoring_kv WHERE key = ?", fallbackKey)
		return err
	}
	encoded, _ := json.Marshal(records)
	_, err := local.Exec("INSERT INTO monitoring_kv(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", fallbackKey, string(encoded))
	return err
}

// localMarks are the highest ids of the local file's tables (0 for a table
// not made yet).
func localMarks(local *sql.DB) map[string]int64 {
	marks := map[string]int64{}
	for _, table := range fallbackTables {
		var n sql.NullInt64
		local.QueryRow("SELECT MAX(id) FROM " + table).Scan(&n)
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

func maxMarks(a, b map[string]int64) map[string]int64 {
	if a == nil {
		return b
	}
	out := map[string]int64{}
	for _, t := range fallbackTables {
		out[t] = a[t]
		if v := b[t]; v > out[t] {
			out[t] = v
		}
	}
	return out
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
// storeState says which server): a fallback already recorded is only seen
// again; one of the same server seen running within a day is continued
// (start, told); others are closed with words that fit. forkInitMu held.
func beginFallback(db DatabaseConfig) {
	storeStateMu.Lock()
	since := storeState.Since
	storeStateMu.Unlock()
	now := monitoringClock()
	closeWhy := ""
	var continued *fallbackRecord
	fallbackMu.Lock()
	withLocalFile(true, func(local *sql.DB) error {
		records := loadFallbacks(local)
		kept := []fallbackRecord{}
		var window *fallbackRecord // rows not caught up yet of earlier fallbacks of this server
		for _, r := range records {
			switch {
			case !r.of(db):
				if r.Ended == 0 { // another server's outage, left open by a change of the settings
					r.Ended = TimeToMillis(now)
					if r.Told {
						closeWhy = "other"
						r.Told = false
					}
				}
				kept = append(kept, r)
			case r.Ended == 0 && r.Since == TimeToMillis(since):
				// this fallback, recorded already
				r.Seen = TimeToMillis(now)
				continued = &r
			case r.Ended == 0 && now.Sub(MillisToTime(r.Seen)) < fallbackContinueWithin:
				// the same outage, Cosmos restarted while it lasted
				r.Seen = TimeToMillis(now)
				c := r
				continued = &c
			default:
				// an outage Cosmos stopped running long ago, or one that ended and
				// whose rows wait for their catch-up: its window goes on in the new one
				if r.Ended == 0 && r.Told {
					closeWhy = "stale"
				}
				if window == nil {
					w := r
					window = &w
				} else {
					window.From = min(window.From, r.From)
					window.After = minMarks(window.After, r.After)
				}
			}
		}
		if continued == nil {
			rec := fallbackRecord{Host: db.PostgresHost, Database: postgresDatabaseName(db), Since: TimeToMillis(since),
				From: TimeToMillis(since.Add(-fallbackLead())), After: localMarks(local), Seen: TimeToMillis(now)}
			if window != nil {
				rec.From = min(rec.From, window.From)
				rec.After = minMarks(rec.After, window.After)
			}
			if closeWhy == "" && !unresolvedOutage(local).IsZero() {
				closeWhy = "stale" // an open notice without a record (an older version)
			}
			continued = &rec
		} else if window != nil {
			continued.From = min(continued.From, window.From)
			continued.After = minMarks(continued.After, window.After)
		}
		kept = append(kept, *continued)
		saveFallbacks(local, kept)
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
// open outage ends; the same server's rows are caught up (after the switch's
// drain) and "answers again" told for a told outage; another server's open
// outage is closed and its rows stay aside. forkInitMu held.
func endFallbacks(db DatabaseConfig, after MonitoringStoreState) {
	storeStateMu.Lock()
	wasTold := storeTold
	storeTold = false
	storeBootFallback = false
	storeStateMu.Unlock()
	now := monitoringClock()
	closeWhy := ""
	resolve := wasTold
	var hand *fallbackRecord
	stale := false
	fallbackMu.Lock()
	withLocalFile(false, func(local *sql.DB) error {
		records := loadFallbacks(local)
		kept := []fallbackRecord{}
		for _, r := range records {
			if !r.of(db) {
				if r.Ended == 0 {
					r.Ended = TimeToMillis(now)
					if r.Told {
						closeWhy, resolve, r.Told = "other", true, false
					}
				}
				kept = append(kept, r)
				continue
			}
			if r.Ended == 0 {
				r.Ended = TimeToMillis(now)
				stale = stale || now.Sub(MillisToTime(r.Seen)) >= fallbackContinueWithin
			}
			if hand == nil {
				h := r
				hand = &h
			} else { // several of this server waiting: one window
				hand.Since, hand.From = min(hand.Since, r.Since), min(hand.From, r.From)
				hand.After = minMarks(hand.After, r.After)
				hand.Before = maxMarks(hand.Before, r.Before)
				hand.To, hand.Ended = max(hand.To, r.To), max(hand.Ended, r.Ended)
				hand.Told = hand.Told || r.Told
			}
		}
		if hand != nil {
			resolve = resolve || hand.Told
			kept = append(kept, *hand)
		}
		if !resolve && !unresolvedOutage(local).IsZero() {
			resolve = true // an open notice without a record (an older version)
		}
		saveFallbacks(local, kept)
		setStoreState(func(s *MonitoringStoreState) { s.Aside = asideOf(kept, db) })
		return nil
	})
	fallbackMu.Unlock()
	if resolve {
		monitoringResolveDown()
	}
	if closeWhy != "" {
		tellMonitoringEnded(closeWhy)
	}
	if hand == nil {
		return // no fallback of this server: nothing to catch up, nothing answers "again"
	}
	told := (hand.Told || wasTold) && !stale
	if stale && hand.Told {
		tellMonitoringEnded("staleback")
	}
	rec := *hand
	catchUpsRunning.Add(1)
	go catchUpFallback(db, rec, told, after)
}

// catchUpFallback hands the fallback's rows to the catch-up once the switch's
// drain has ended (the local file is not written any more), then tells.
var catchUpFallback = func(db DatabaseConfig, rec fallbackRecord, told bool, after MonitoringStoreState) {
	time.Sleep(timing().drain + 300*time.Millisecond)
	now := monitoringClock()
	fallbackMu.Lock()
	withLocalFile(false, func(local *sql.DB) error {
		marks := localMarks(local)
		rec.Before = maxMarks(rec.Before, marks)
		rec.To = max(rec.To, TimeToMillis(now.Add(time.Second)))
		records := loadFallbacks(local)
		for i := range records {
			if records[i].of(db) {
				records[i].Before, records[i].To, records[i].Told = rec.Before, rec.To, false // told below, once
			}
		}
		return saveFallbacks(local, records)
	})
	fallbackMu.Unlock()
	if rec.Before == nil {
		rec.Before = map[string]int64{}
	}
	window := CatchUpWindow{From: MillisToTime(rec.From), To: MillisToTime(rec.To), Back: MillisToTime(rec.Ended), After: rec.After, Before: rec.Before}
	finish := func(caught CatchUpResult) {
		catchUpsRunning.Add(-1)
		if caught.Ran {
			forgetFallback(db, rec)
		}
		if told {
			monitoringTellBack(after, MillisToTime(rec.Since), caught)
		}
	}
	if catchUp := MonitoringCatchUp; catchUp != nil {
		catchUp(window, finish)
	} else {
		finish(CatchUpResult{})
	}
}

// forgetFallback removes the record of a fallback whose rows the catch-up
// took (done, or kept in PostgreSQL's list of windows to take up again).
func forgetFallback(db DatabaseConfig, rec fallbackRecord) {
	fallbackMu.Lock()
	defer fallbackMu.Unlock()
	withLocalFile(false, func(local *sql.DB) error {
		records := loadFallbacks(local)
		kept := []fallbackRecord{}
		for _, r := range records {
			if r.of(db) && r.Ended != 0 && r.Since >= rec.Since && r.Ended <= rec.Ended {
				continue
			}
			kept = append(kept, r)
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
