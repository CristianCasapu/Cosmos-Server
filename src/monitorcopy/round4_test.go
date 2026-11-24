package monitorcopy

// Review round 4 (plan 51): a falling gauge merges exactly with nothing
// "missing" (S2); a catch-up adds its server's part of the local file's
// totals only (S1); the bookkeeping is pruned (M3); a copy out of the store
// in use while it is written does not report a false mismatch (M6).

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/azukaar/cosmos-server/src/utils"
)

// S2: an averaged gauge whose own part in the local file is negative (it fell
// during a short outage): merged, totals exact, nothing missing, a re-run
// changes nothing; only a negative count is refused.
func TestAFallingGaugeMergesExactly(t *testing.T) {
	for _, dialect := range dialects() {
		t.Run(short(dialect), func(t *testing.T) {
			src, dst := openStore(t, utils.DialectSQLite), openStore(t, dialect)
			now := time.Now().UnixMilli()
			hour := now - now%int64(time.Hour/time.Millisecond)
			// PostgreSQL holds the hour so far: the raw bucket open at the cut flushed 400 there
			totals(t, dst, "gauge", "avg", map[string][3]int64{"hour": {hour, 2000, 5}})
			// the local file got that bucket whole (400, noted as prior) and its fall to 395 (-5) and nothing else
			totals(t, src, "gauge", "avg", map[string][3]int64{"hour": {hour, 395, 1}})
			exec(t, src, "INSERT INTO monitoring_kv(key, value) VALUES(?, '400|1')", utils.PriorPrefix+"server.local|gauge|hour|"+itoa(hour))
			report := copyOnce(t, src, dst, testOptions("gauge", true))
			if v, i := bucketValue(t, dst, "gauge", "hour"); v != 1995 || i != 5 {
				t.Errorf("hour %d/%d, want 1995/5 (the fall of 5 merged, no new count)", v, i)
			}
			if got := tableOf(report, "metric_values"); got.Missing != 0 || report.Mismatch {
				t.Errorf("checked: %+v", got)
			}
			again := copyOnce(t, src, dst, testOptions("gauge2", true))
			if v, _ := bucketValue(t, dst, "gauge", "hour"); v != 1995 || tableOf(again, "metric_values").Missing != 0 {
				t.Errorf("again: %d, %+v", v, tableOf(again, "metric_values"))
			}
		})
	}
}

// S1: the local file's day total holds the parts of two servers' outages; the
// catch-up of server A's fallback adds A's part only, B's that of B; a manual
// copy afterwards adds what neither outage claimed (the local file's own
// period), once.
func TestACatchUpAddsItsServersPartOnly(t *testing.T) {
	src, a, b := openStore(t, utils.DialectSQLite), openStore(t, utils.DialectSQLite), openStore(t, utils.DialectSQLite)
	a.ID, b.ID = "pgx:aaaa", "pgx:bbbb"
	now := time.Now().UnixMilli()
	day := now - now%int64(24*time.Hour/time.Millisecond)
	totals(t, src, "sum", "sum", map[string][3]int64{"day": {day, 17 + 4, 3}}) // A's 10, B's 7, the local file's own 4
	for store, part := range map[string]string{a.ID: "10|1", b.ID: "7|1"} {
		exec(t, src, "INSERT INTO monitoring_kv(key, value) VALUES(?, ?)", utils.OwnPrefix+store+":server.local|sum|day|"+itoa(day), part)
	}
	totals(t, a, "sum", "sum", map[string][3]int64{"day": {day, 100, 9}})
	totals(t, b, "sum", "sum", map[string][3]int64{"day": {day, 50, 4}})
	catchUp := func(dst Store, id string) Report {
		opt := testOptions("catchup-"+id, true)
		opt.From, opt.End, opt.Back, opt.Store = day, now+60000, now, id
		return copyOnce(t, src, dst, opt)
	}
	for _, c := range []struct {
		dst    Store
		v, i   int64
		reason string
	}{{a, 110, 10, "A"}, {b, 57, 5, "B"}} {
		report := catchUp(c.dst, c.dst.ID)
		if v, i := bucketValue(t, c.dst, "sum", "day"); v != c.v || i != c.i || tableOf(report, "metric_values").Missing != 0 {
			t.Errorf("%s after its catch-up: %d/%d, want %d/%d (%+v)", c.reason, v, i, c.v, c.i, tableOf(report, "metric_values"))
		}
		catchUp(c.dst, c.dst.ID) // again: nothing twice
		if v, _ := bucketValue(t, c.dst, "sum", "day"); v != c.v {
			t.Errorf("%s again: %d", c.reason, v)
		}
	}
	// the owner copies the local file's history into A by hand: the rest of the
	// local file's row (B's 7 and its own 4) comes over, A's 10 not again
	copyOnce(t, src, a, testOptions("manual", true))
	if v, _ := bucketValue(t, a, "sum", "day"); v != 121 {
		t.Errorf("A after the manual copy: %d, want 121", v)
	}
	catchUp(a, a.ID) // a later catch-up of A adds nothing again
	if v, _ := bucketValue(t, a, "sum", "day"); v != 121 {
		t.Errorf("A's catch-up after the manual copy: %d", v)
	}
}

// M3: ledger keys of buckets the retention removed are pruned at the end of a
// copy; a catch-up's markers go once its window is done.
func TestTheBookkeepingIsPruned(t *testing.T) {
	env := useCatchUp(t, utils.DialectSQLite)
	env.fill(t)
	old := time.Now().Add(-40 * 24 * time.Hour).UnixMilli()
	exec(t, env.dst, "INSERT INTO monitoring_kv(key, value) VALUES(?, '1|1'), (?, '1|1'), (?, '1|1')",
		ledgerPrefix+"sqlite:x:server.local|k|day|"+itoa(old), ownLedgerPrefix+"sqlite:x:server.local|k|hour|"+itoa(old),
		ledgerPrefix+"sqlite:x:server.local|k|day|"+itoa(time.Now().UnixMilli()))
	got, _ := env.catchUp(t)
	if !got.Ran {
		t.Fatalf("%+v", got)
	}
	if n := count(t, env.dst, "SELECT COUNT(*) FROM monitoring_kv WHERE key LIKE ?", markerPrefix+"absorbed%"+itoa(old)); n != 0 {
		t.Errorf("%d ledger keys of removed buckets left", n)
	}
	if n := count(t, env.dst, "SELECT COUNT(*) FROM monitoring_kv WHERE key LIKE ?", ledgerPrefix+"sqlite:x:%"); n != 1 {
		t.Errorf("the ledger key of a bucket still kept: %d", n)
	}
	if n := count(t, env.dst, "SELECT COUNT(*) FROM monitoring_kv WHERE key LIKE ?", markerPrefix+CatchUp+":%"); n != 0 {
		t.Errorf("%d markers of a finished catch-up left", n)
	}
}

// M6: a copy to the local file while PostgreSQL (the source, the store in
// use) is written: a notification dated just before the copy, committed after
// the copy's batches passed its time and before the check, is not a false
// "mismatch" (the copy ends 30 s before it starts).
func TestACopyOutOfTheStoreInUseWhileItIsWritten(t *testing.T) {
	env := useCatchUp(t, utils.DialectSQLite)
	// "PostgreSQL" is a file here, with a second handle for the writer
	dstPath := filepath.Join(t.TempDir(), "pg.db")
	dst, err := openLocalAt(dstPath, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dst.DB.Close() })
	if err := utils.PrepareMonitoringSchema(dst.DB, dst.Dialect); err != nil {
		t.Fatal(err)
	}
	env.dst = dst
	for i := 0; i < 20; i++ {
		exec(t, env.dst, "INSERT INTO notifications(node, recipient, title, date, fork_ref, fork_first) VALUES('server.local', 'admin', ?, ?, ?, ?)",
			"old"+itoa(int64(i)), time.Now().Add(-time.Hour).UnixMilli()+int64(i), "r"+itoa(int64(i)), time.Now().Add(-time.Hour).UnixMilli()+int64(i))
	}
	// a writer's transaction, dated now, open across the copy
	held, err := sql.Open(utils.DialectSQLite, "file:"+dstPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	tx, err := held.Begin()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Second).UnixMilli()
	if _, err := tx.Exec("INSERT INTO notifications(node, recipient, title, date, fork_ref, fork_first) VALUES('server.local', 'admin', 'late', ?, 'late', ?)", at, at); err != nil {
		t.Fatal(err)
	}
	prev := beforeCheck
	t.Cleanup(func() { beforeCheck = prev })
	committed := false
	beforeCheck = func() {
		if !committed {
			committed = true
			tx.Commit()
		}
	}
	if err := Start(ToLocal); err != nil {
		t.Fatal(err)
	}
	wait()
	state.mu.Lock()
	last := state.last
	state.mu.Unlock()
	if !committed || last == nil || last.Outcome != "done" {
		t.Fatalf("committed %v, %+v", committed, last)
	}
}

// S1: the catch-up of a server's fallback takes its own rows only: events
// and security entries by id alone (none of the time around it, where the
// other server's rows are), notifications by id or changed in place during
// it, the raw rows of its time.
func TestACatchUpTakesNoRowOfAnotherServersTime(t *testing.T) {
	src, dst := openStore(t, utils.DialectSQLite), openStore(t, utils.DialectSQLite)
	since := time.Now().Add(-10 * time.Minute).UnixMilli()
	event := func(label string, at int64) {
		exec(t, src, "INSERT INTO events(node, event_id, label, application, level, date, data, object, search) VALUES('server.local', ?, ?, '', 'info', ?, '', '', '')", label, label, at)
	}
	event("other-before", since-20000) // another server's, 20 s before this fallback
	marks := map[string]int64{"events": count(t, src, "SELECT MAX(id) FROM events"), "notifications": 0, "fork_security_events": 0}
	event("mine", since+1000)
	event("mine-kept", since-30000) // kept while the pings found it gone
	before := map[string]int64{"events": count(t, src, "SELECT MAX(id) FROM events"), "notifications": 0, "fork_security_events": 0}
	event("other-after", since+65000) // another server's, right after this fallback
	opt := testOptions("own-rows", true)
	opt.From, opt.End, opt.Back, opt.Since, opt.Store = since-45000-catchUpMargin.Milliseconds(), since+60000+catchUpMargin.Milliseconds(), since+60000, since, "pgx:mine"
	opt.IDs = Window{After: marks, Before: before}.ids()
	report := copyOnce(t, src, dst, opt)
	for label, want := range map[string]int64{"mine": 1, "mine-kept": 1, "other-before": 0, "other-after": 0} {
		if n := count(t, dst, "SELECT COUNT(*) FROM events WHERE label = ?", label); n != want {
			t.Errorf("%s: %d in the target, want %d", label, n, want)
		}
	}
	if p := tableOf(report, "events"); p.SourceRows != 2 {
		t.Errorf("the check went through %d events, want the fallback's 2", p.SourceRows)
	}
}

// A batch that met the store's writers in a deadlock is tried again (round
// 4: the catch-up's merge and the metrics' flush update the same rows; the
// replay saw "deadlock detected" end a catch-up as failed).
func TestABatchInADeadlockIsTriedAgain(t *testing.T) {
	src, dst := openStore(t, utils.DialectSQLite), openStore(t, utils.DialectSQLite)
	now := time.Now().UnixMilli()
	totals(t, src, "sum", "sum", map[string][3]int64{"day": {now - now%86400000, 5, 1}})
	prev := batchFault
	t.Cleanup(func() { batchFault = prev })
	failed := 0
	batchFault = func(table string) error {
		if table == "metric_values" && failed < 2 {
			failed++
			return &pgconn.PgError{Code: "40P01", Message: "deadlock detected"}
		}
		return nil
	}
	report := copyOnce(t, src, dst, testOptions("deadlock", true))
	if failed != 2 || report.Mismatch {
		t.Fatalf("failed %d times, %+v", failed, tableOf(report, "metric_values"))
	}
	if v, _ := bucketValue(t, dst, "sum", "day"); v != 5 {
		t.Errorf("day total %d", v)
	}
}
