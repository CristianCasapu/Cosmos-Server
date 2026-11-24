package monitorcopy

// Review round 3 (plan 51): the catch-up takes the rows a fallback inserted
// into the local file by their ids, whatever time they carry (S1), and adds
// up the hour and day totals both stores hold exactly once (S2).

import (
	"context"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// S1: events and security entries kept in memory while the pings found
// PostgreSQL gone are written into the local file after the fallback began,
// dated before it: the window's ids take them; the rows the local file had
// before the fallback are not taken; the check counts exactly those rows.
func TestCatchUpTakesTheRowsTheFallbackWroteByID(t *testing.T) {
	for _, dialect := range dialects() {
		t.Run(short(dialect), func(t *testing.T) {
			src, dst := openStore(t, utils.DialectSQLite), openStore(t, dialect)
			since := time.Now().Add(-10 * time.Minute).UnixMilli()
			// the local file's history before the fallback (an earlier period on it)
			for i := 0; i < 3; i++ {
				writeAt(t, src, since-int64(i+1)*60000, "history"+itoa(int64(i)))
			}
			marks := map[string]int64{}
			for _, table := range utils.FallbackTables() {
				marks[table] = count(t, src, "SELECT COALESCE(MAX(id), 0) FROM "+table)
			}
			// written during the fallback: kept rows dated a minute and a half
			// before it (beyond any margin of time), rows of the fallback itself
			exec(t, src, "INSERT INTO events(node, event_id, label, application, level, date, data, object, search) VALUES('server.local', 'kept', 'kept', 'cosmos', 'info', ?, '', '', '')", since-90000)
			exec(t, src, "INSERT INTO fork_security_events(node, ts, kind, target) VALUES('server.local', ?, 'auth.login.failed', 'kept')", since-90000)
			exec(t, src, "INSERT INTO events(node, event_id, label, application, level, date, data, object, search) VALUES('server.local', 'during', 'during', 'cosmos', 'info', ?, '', '', '')", since+1000)
			exec(t, src, "INSERT INTO notifications(node, recipient, title, date, fork_ref, fork_first) VALUES('server.local', 'admin', 'during', ?, 'ref-during', ?)", since+2000, since+2000)
			before := map[string]int64{}
			for _, table := range utils.FallbackTables() {
				before[table] = count(t, src, "SELECT COALESCE(MAX(id), 0) FROM "+table)
			}
			w := Window{From: since, To: since + 60000, Back: since + 60000, After: marks, Before: before}
			opt := testOptions("ids", true)
			opt.From, opt.End, opt.IDs, opt.Back = w.From-catchUpMargin.Milliseconds(), w.To+catchUpMargin.Milliseconds(), w.ids(), w.Back
			report := copyOnce(t, src, dst, opt)
			if report.Mismatch {
				t.Fatalf("%+v", report.Tables)
			}
			if n := count(t, dst, "SELECT COUNT(*) FROM events WHERE event_id IN ('kept', 'during')"); n != 2 {
				t.Errorf("events of the fallback in the target: %d, want 2", n)
			}
			if n := count(t, dst, "SELECT COUNT(*) FROM fork_security_events WHERE target = 'kept'"); n != 1 {
				t.Errorf("the kept security entry in the target: %d", n)
			}
			if n := count(t, dst, "SELECT COUNT(*) FROM notifications WHERE fork_ref = 'ref-during'"); n != 1 {
				t.Errorf("the notification of the fallback in the target: %d", n)
			}
			if n := count(t, dst, "SELECT COUNT(*) FROM events WHERE label LIKE 'history%'"); n != 0 {
				t.Errorf("the local file's earlier history was taken: %d", n)
			}
			// the check went through exactly the rows written aside
			if p := tableOf(report, "events"); p.SourceRows != 2 || p.TargetRows != 2 || p.Missing != 0 {
				t.Errorf("events checked: %+v", p)
			}
			// a window by time alone (what round 2 handed) leaves the kept rows behind
			late := testOptions("late", true)
			late.From, late.End = w.From, w.To
			other := openStore(t, dialect)
			copyOnce(t, src, other, late)
			if n := count(t, other, "SELECT COUNT(*) FROM events WHERE event_id = 'kept'"); n != 0 {
				t.Errorf("the test proves nothing: a time window took the kept event")
			}
		})
	}
}

// totals writes a metric's catalogue row and its buckets.
func totals(t *testing.T, s Store, key, agg string, buckets map[string][3]int64) {
	t.Helper()
	exec(t, s, "INSERT INTO metrics(node, key, agglo_type, time_scale, last_update) VALUES('server.local', ?, ?, 1, ?) ON CONFLICT(node, key) DO NOTHING", key, agg, time.Now().UnixMilli()-60000)
	for gran, b := range buckets {
		exec(t, s, "INSERT INTO metric_values(node, key, granularity, date, value, avg_index, expire) VALUES('server.local', ?, ?, ?, ?, ?, ?)", key, gran, b[0], b[1], b[2], time.Now().Add(40*24*time.Hour).UnixMilli())
	}
}

func bucketValue(t *testing.T, s Store, key, gran string) (int64, int64) {
	t.Helper()
	var v, i int64
	if err := s.DB.QueryRow(s.bind("SELECT value, avg_index FROM metric_values WHERE node = 'server.local' AND key = ? AND granularity = ?"), key, gran).Scan(&v, &i); err != nil {
		t.Fatalf("%s %s: %v", key, gran, err)
	}
	return v, i
}

// S2: a day and an hour total both stores hold are added up once (sum,
// avg: value and count; max, min: the larger, the smaller); again, nothing
// twice; the way back (a copy to the local file) adds only the other store's
// own part, and a copy that way again sends nothing back; a kind of total
// that cannot be merged is said, not merged.
func TestTotalsBothStoresHoldAreAddedUpOnce(t *testing.T) {
	for _, p := range pairs() {
		t.Run(short(p.src)+"-to-"+short(p.dst), func(t *testing.T) {
			src, dst := openStore(t, p.src), openStore(t, p.dst)
			now := time.Now().UnixMilli()
			day := now - now%int64(24*time.Hour/time.Millisecond)
			hour := now - now%int64(time.Hour/time.Millisecond)
			for _, s := range []struct {
				store Store
				own   int64
			}{{src, 1}, {dst, 2}} {
				totals(t, s.store, "sum", "sum", map[string][3]int64{"day": {day, 50 * s.own, 5 * s.own}, "hour": {hour, 20 * s.own, 2 * s.own}})
				totals(t, s.store, "avg", "avg", map[string][3]int64{"day": {day, 300 * s.own, 3 * s.own}})
				totals(t, s.store, "max", "max", map[string][3]int64{"day": {day, 9 - 2*s.own, 1}})
				totals(t, s.store, "min", "min", map[string][3]int64{"day": {day, 9 - 2*s.own, 1}})
				totals(t, s.store, "last", "", map[string][3]int64{"day": {day, 4 * s.own, 1}})
			}
			report := copyOnce(t, src, dst, testOptions("totals", true))
			check := func(store Store, key, gran string, v, i int64) {
				t.Helper()
				if gv, gi := bucketValue(t, store, key, gran); gv != v || gi != i {
					t.Errorf("%s %s: %d/%d, want %d/%d", key, gran, gv, gi, v, i)
				}
			}
			check(dst, "sum", "day", 150, 15)
			check(dst, "sum", "hour", 60, 6)
			check(dst, "avg", "day", 900, 9)
			check(dst, "max", "day", 7, 2)
			check(dst, "min", "day", 5, 2)
			check(dst, "last", "day", 8, 1) // the target's: not merged, said
			if got := tableOf(report, "metric_values"); got.Inexact != 1 || got.Missing != 0 {
				t.Errorf("checked: %+v", got)
			}
			// again: nothing twice
			if again := copyOnce(t, src, dst, testOptions("totals", true)); tableOf(again, "metric_values").Written != 0 {
				t.Errorf("again wrote %+v", tableOf(again, "metric_values"))
			}
			check(dst, "sum", "day", 150, 15)
			// the way back: only the target's own part (100), never the source's again
			copyOnce(t, dst, src, testOptions("back", false))
			check(src, "sum", "day", 150, 15)
			check(src, "avg", "day", 900, 9)
			// and that way again: nothing sent back
			copyOnce(t, src, dst, testOptions("totals2", true))
			check(dst, "sum", "day", 150, 15)
			check(dst, "avg", "day", 900, 9)
			// one more flush into the local file's open day: only that part goes
			exec(t, src, "UPDATE metric_values SET value = value + 7, avg_index = avg_index + 1 WHERE key = 'sum' AND granularity = 'day'")
			copyOnce(t, src, dst, testOptions("totals3", true))
			check(dst, "sum", "day", 157, 16)
		})
	}
}

// S2: raw rows (the last hour's fine points, replaced on each flush) in both
// stores: the catch-up takes the local file's when the raw bucket ended
// before PostgreSQL became the store again, PostgreSQL's otherwise.
func TestRawRowsGoToTheStoreWithTheLaterFlush(t *testing.T) {
	for _, dialect := range dialects() {
		t.Run(short(dialect), func(t *testing.T) {
			src, dst := openStore(t, utils.DialectSQLite), openStore(t, dialect)
			back := time.Now().Add(-5 * time.Minute).UnixMilli()
			back -= back % 30000
			for _, s := range []Store{src, dst} {
				exec(t, s, "INSERT INTO metrics(node, key, agglo_type, time_scale, last_update) VALUES('server.local', 'k', 'sum', 1, ?)", back)
			}
			early, late := back-120000, back
			exec(t, src, "INSERT INTO metric_values(node, key, granularity, date, value, avg_index, expire) VALUES('server.local', 'k', 'raw', ?, 9, 3, ?), ('server.local', 'k', 'raw', ?, 2, 1, ?)", early, early+3600000, late, late+3600000)
			exec(t, dst, "INSERT INTO metric_values(node, key, granularity, date, value, avg_index, expire) VALUES('server.local', 'k', 'raw', ?, 4, 1, ?), ('server.local', 'k', 'raw', ?, 6, 3, ?)", early, early+3600000, late, late+3600000)
			opt := testOptions("raw", true)
			opt.From, opt.End, opt.Back = early-60000, back+60000, back
			if report := copyOnce(t, src, dst, opt); report.Mismatch {
				t.Fatalf("%+v", tableOf(report, "metric_values"))
			}
			var v int64
			dst.DB.QueryRow(dst.bind("SELECT value FROM metric_values WHERE granularity = 'raw' AND date = ?"), early).Scan(&v)
			if v != 9 {
				t.Errorf("a raw bucket that ended during the fallback: %d, want the local file's 9", v)
			}
			dst.DB.QueryRow(dst.bind("SELECT value FROM metric_values WHERE granularity = 'raw' AND date = ?"), late).Scan(&v)
			if v != 6 {
				t.Errorf("a raw bucket open at the switch back: %d, want PostgreSQL's 6", v)
			}
		})
	}
}

// Windows of consecutive fallbacks (no row of the local file between them)
// are one run; windows with rows of another period between them are not.
func TestTouchingWindowsAreOneRun(t *testing.T) {
	ids := func(a, b int64) map[string]int64 {
		return map[string]int64{"events": a, "notifications": b, "fork_security_events": 0}
	}
	w1 := Window{From: 100, To: 200, After: ids(10, 5), Before: ids(20, 8)}
	w2 := Window{From: 300, To: 400, After: ids(20, 8), Before: ids(30, 9)}
	w3 := Window{From: 500, To: 600, After: ids(35, 9), Before: ids(40, 10)}
	got := merge([]Window{w3, w2, w1})
	if len(got) != 2 || got[0].From != 100 || got[0].To != 400 || got[0].After["events"] != 10 || got[0].Before["events"] != 30 || got[1].From != 500 {
		t.Fatalf("%+v", got)
	}
	_ = context.Background
}
