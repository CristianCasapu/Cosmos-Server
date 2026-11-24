package monitorcopy

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// unruled lists the tables of a store that have no rule, and the columns of
// ruled tables that the rule does not copy (a surrogate table's id aside).
func unruled(t *testing.T, s Store) []string {
	t.Helper()
	ctx := context.Background()
	tables, err := s.tables(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for table := range tables {
		r, ok := ruleOf(table)
		if !ok {
			out = append(out, "table "+table)
			continue
		}
		columns, err := s.columns(ctx, table)
		if err != nil {
			t.Fatal(err)
		}
		known := map[string]bool{}
		for _, c := range r.columns {
			known[c.name] = true
			if !columns[c.name] {
				out = append(out, "column "+table+"."+c.name+" (in the rule, not in the schema)")
			}
		}
		for name := range columns {
			if !known[name] && !(r.surrogate() && name == "id") {
				out = append(out, "column "+table+"."+name)
			}
		}
	}
	for _, r := range rules {
		if !tables[r.table] {
			out = append(out, "rule "+r.table+" (no such table)")
		}
	}
	sort.Strings(out)
	return out
}

// A table or a column added to the monitoring store without a copy rule
// fails here: the copy would leave it behind.
func TestEveryTableAndColumnHasARule(t *testing.T) {
	if got := utils.RegisteredMonitoringSchemas(); len(got) == 0 || got[0] != "fork_security_events" {
		t.Fatalf("registered schemas %v: the security record's table is not registered", got)
	}
	for _, dialect := range dialects() {
		t.Run(short(dialect), func(t *testing.T) {
			s := openStore(t, dialect)
			if missing := unruled(t, s); len(missing) > 0 {
				t.Fatalf("without a copy rule: %v", missing)
			}
			// the check sees a new table and a new column
			exec(t, s, "CREATE TABLE fork_newthing (id BIGINT PRIMARY KEY, label TEXT)")
			exec(t, s, "ALTER TABLE events ADD COLUMN fork_newcolumn TEXT NOT NULL DEFAULT ''")
			got := strings.Join(unruled(t, s), "; ")
			if !strings.Contains(got, "table fork_newthing") || !strings.Contains(got, "column events.fork_newcolumn") {
				t.Fatalf("the check missed an addition: %q", got)
			}
		})
	}
}

// The SQL condition of monitoring_kv leaves out exactly the bookkeeping keys.
func TestBookkeepingKeysAreNotCopied(t *testing.T) {
	kv, _ := ruleOf("monitoring_kv")
	keys := []string{"schema_version", "fork_notifications_schema", "fork_security_schema", markerPrefix + "x", markerPrefix + "x:events",
		"custom", "schema", "my_schemata", "fork_monitoring_copyx", "a_schema_b", "fork%schema"}
	for _, dialect := range dialects() {
		t.Run(short(dialect), func(t *testing.T) {
			s := openStore(t, dialect)
			for _, key := range keys {
				exec(t, s, "INSERT INTO monitoring_kv(key, value) VALUES(?, '1') ON CONFLICT(key) DO NOTHING", key)
			}
			for _, key := range keys {
				copied := count(t, s, "SELECT COUNT(*) FROM monitoring_kv WHERE key = ? AND "+kv.where, key) == 1
				if copied == bookkeepingKey(key) {
					t.Errorf("%q: copied %v, bookkeeping %v", key, copied, bookkeepingKey(key))
				}
			}
		})
	}
}

type pair struct{ src, dst string }

func pairs() []pair {
	out := []pair{{utils.DialectSQLite, utils.DialectSQLite}}
	if os.Getenv("COSMOS_FORK_PG_TEST_DSN") != "" {
		out = append(out, pair{utils.DialectSQLite, utils.DialectPostgres}, pair{utils.DialectPostgres, utils.DialectSQLite}, pair{utils.DialectPostgres, utils.DialectPostgres})
	}
	return out
}

// scenario fills a source with the history and a target with what was
// recorded since the switch, with a row of each kind in both.
type scenario struct {
	src, dst Store
	end      int64 // the source's newest time
	hour     int64
}

func newScenario(t *testing.T, p pair) scenario {
	t.Helper()
	src, dst := openStore(t, p.src), openStore(t, p.dst)
	hour := int64(time.Hour / time.Millisecond)
	end := (time.Now().UnixMilli()/hour)*hour - 5*hour
	fill(t, src, contents{node: "server.local", metrics: 3, values: 10, events: 120, notifications: 12, security: 30, end: end})
	fill(t, dst, contents{node: "server.local", metrics: 2, values: 4, events: 15, notifications: 3, security: 5, end: end + 4*hour, ref: "dst"})

	// monitoring_kv: a key in both, one of the source only
	exec(t, src, "INSERT INTO monitoring_kv(key, value) VALUES('custom.both', 'source'), ('custom.source', 'yes')")
	exec(t, dst, "INSERT INTO monitoring_kv(key, value) VALUES('custom.both', 'target')")
	// metrics: metric1 is newer in the source, metric0 in the target
	exec(t, src, "UPDATE metrics SET last_update = ?, label = 'newer in the source' WHERE key = 'cosmos.system.metric1'", end+4*hour+hour/2)
	exec(t, dst, "UPDATE metrics SET last_update = ?, label = 'newer in the target' WHERE key = 'cosmos.system.metric0'", end+20*hour)
	// metric_values: the bucket that was open at the switch is in both
	exec(t, dst, "INSERT INTO metric_values(node, key, granularity, date, value, avg_index, expire) VALUES('server.local', 'cosmos.system.metric2', 'hourly', ?, 999, 9, 0)", end)
	// an expired bucket and an event older than the retention: not copied
	exec(t, src, "INSERT INTO metric_values(node, key, granularity, date, value, avg_index, expire) VALUES('server.local', 'cosmos.system.metric0', 'raw', ?, 5, 1, ?)", end, time.Now().UnixMilli()-1000)
	exec(t, src, "INSERT INTO events(node, event_id, label, application, level, date, data, object, search) VALUES('server.local', 'old', 'Old', 'cosmos', 'info', ?, '', '', '')", time.Now().Add(-31*24*time.Hour).UnixMilli())
	// two identical events stay two
	for i := 0; i < 2; i++ {
		exec(t, src, "INSERT INTO events(node, event_id, label, application, level, date, data, object, search) VALUES('server.local', 'twin', 'Twin', 'cosmos', 'info', ?, '{}', '', '')", end-500)
	}
	// a text PostgreSQL would refuse as it is
	if p.src == utils.DialectSQLite {
		exec(t, src, "INSERT INTO events(node, event_id, label, application, level, date, data, object, search) VALUES('server.local', 'odd', ?, 'cosmos', 'info', ?, ?, '', '')",
			"bad \xff byte", end-700, "nul\x00inside")
	}
	// notifications: the target has the same one (ref00000001), older; and an upstream row of its own
	exec(t, dst, "UPDATE notifications SET fork_ref = 'ref00000001', fork_first = ?, date = ?, read = 0, fork_count = 1 WHERE fork_ref = 'dst00000000'", end-60000, end-60000)
	exec(t, src, "UPDATE notifications SET date = ?, read = 1, fork_count = 4 WHERE fork_ref = 'ref00000001'", end+3*hour)
	exec(t, src, "INSERT INTO notifications(node, recipient, title, message, date, level, read) VALUES('server.local', 'admin', 'Upstream', 'Plain', ?, 'info', 0)", end-1234)
	return scenario{src: src, dst: dst, end: end, hour: hour}
}

func TestCopyBothWaysAndTwice(t *testing.T) {
	for _, p := range pairs() {
		for _, targetInUse := range []bool{true, false} {
			name := short(p.src) + "-to-" + short(p.dst)
			if targetInUse {
				name += "-target-in-use"
			} else {
				name += "-source-in-use"
			}
			t.Run(name, func(t *testing.T) {
				sc := newScenario(t, p)
				src, dst := sc.src, sc.dst
				rows := func(table string) int64 {
					q := "SELECT COUNT(*) FROM " + table
					if table == "monitoring_kv" {
						q += " WHERE key NOT LIKE 'fork_monitoring_copy:%'"
					}
					return count(t, dst, q)
				}
				before := map[string]int64{}
				for _, r := range rules {
					before[r.table] = rows(r.table)
				}
				report := copyOnce(t, src, dst, testOptions("test", targetInUse))
				if report.Mismatch {
					t.Fatalf("the verification found a mismatch: %+v", report.Tables)
				}
				for _, p := range report.Tables {
					if p.State != "verified" || p.Done != p.Total || p.Missing != 0 {
						t.Errorf("%s: %+v", p.Table, p)
					}
				}

				// what each table holds afterwards
				want := map[string]int64{
					"monitoring_kv":        before["monitoring_kv"] + 1,          // custom.source; custom.both was there
					"metrics":              3,                                    // metric0..2
					"metric_values":        3*10 + 2*4,                           // + the target's bucket that is the source's too; the expired one stays out
					"events":               before["events"] + 120 + 2,           // + the twins, not the old one
					"notifications":        before["notifications"] + 12 + 1 - 1, // + upstream's row; ref00000001 is in both
					"fork_security_events": before["fork_security_events"] + 30,
				}
				if p.src == utils.DialectSQLite {
					want["events"]++
				}
				for table, n := range want {
					if got := rows(table); got != n {
						t.Errorf("%s: %d rows, want %d", table, got, n)
					}
				}
				var value string
				dst.DB.QueryRow(dst.bind("SELECT value FROM monitoring_kv WHERE key = ?"), "custom.both").Scan(&value)
				if value != "target" {
					t.Errorf("custom.both = %q: the target's value must stay", value)
				}
				if n := count(t, dst, "SELECT COUNT(*) FROM monitoring_kv WHERE key LIKE 'fork_monitoring_copy:%' AND value LIKE '%\"finished\":true%'"); n != 1 {
					t.Errorf("finished run markers: %d", n)
				}
				var label string
				dst.DB.QueryRow("SELECT label FROM metrics WHERE key = 'cosmos.system.metric1'").Scan(&label)
				if label != "newer in the source" {
					t.Errorf("metric1: %q, the newer source row must win", label)
				}
				dst.DB.QueryRow("SELECT label FROM metrics WHERE key = 'cosmos.system.metric0'").Scan(&label)
				if label != "newer in the target" {
					t.Errorf("metric0: %q, the newer target row must stay", label)
				}
				bucket := count(t, dst, "SELECT value FROM metric_values WHERE key = 'cosmos.system.metric2' AND granularity = 'hourly' AND date = ?", sc.end)
				if targetInUse && bucket != 999 {
					t.Errorf("the bucket in both: %d, the store in use (target) must win", bucket)
				}
				if !targetInUse && bucket == 999 {
					t.Errorf("the bucket in both kept the target's value: the store in use (source) must win")
				}
				if n := count(t, dst, "SELECT COUNT(*) FROM notifications WHERE fork_ref = 'ref00000001' AND read = 1 AND fork_count = 4"); n != 1 {
					t.Errorf("the notification in both: %d rows with the source's newer state", n)
				}
				if n := count(t, dst, "SELECT COUNT(*) FROM events WHERE event_id = 'twin'"); n != 2 {
					t.Errorf("twins: %d", n)
				}
				if p.src == utils.DialectSQLite && p.dst == utils.DialectPostgres {
					var data, label string
					dst.DB.QueryRow("SELECT data, label FROM events WHERE event_id = 'odd'").Scan(&data, &label)
					if data != "nulinside" || label != "bad � byte" {
						t.Errorf("odd text: %q %q", data, label)
					}
				}

				// the ids go on after the highest: a new row takes a fresh one
				for _, table := range []string{"events", "fork_security_events"} {
					max := count(t, dst, "SELECT MAX(id) FROM "+table)
					var id int64
					query := "INSERT INTO events(node, date) VALUES('server.local', 1) RETURNING id"
					if table == "fork_security_events" {
						query = "INSERT INTO fork_security_events(node, ts) VALUES('server.local', 1) RETURNING id"
					}
					if err := dst.DB.QueryRow(query).Scan(&id); err != nil || id <= max {
						t.Errorf("%s: a new row got id %d (max %d): %v", table, id, max, err)
					}
					exec(t, dst, "DELETE FROM "+table+" WHERE id = ?", id)
				}

				// twice: nothing is written again, and it still agrees
				again := copyOnce(t, src, dst, testOptions("test", targetInUse))
				if again.Written != 0 || again.Mismatch {
					t.Fatalf("the second copy wrote %d rows (mismatch %v): %+v", again.Written, again.Mismatch, again.Tables)
				}
				for table, n := range want {
					if got := rows(table); got != n {
						t.Errorf("after the second copy %s: %d rows, want %d", table, got, n)
					}
				}
			})
		}
	}
}

// The verification sees a row that went missing, a changed value and a row
// missing from the range both stores share.
func TestVerificationCatchesADroppedRow(t *testing.T) {
	for _, p := range pairs() {
		t.Run(short(p.src)+"-to-"+short(p.dst), func(t *testing.T) {
			sc := newScenario(t, p)
			copyOnce(t, sc.src, sc.dst, testOptions("test", true))

			check := func(table string) TableProgress {
				t.Helper()
				report, err := verifyOnly(context.Background(), sc.src, sc.dst, testOptions("test", true), &Tracker{})
				if err != nil {
					t.Fatal(err)
				}
				for _, p := range report.Tables {
					if p.State == "mismatch" && p.Table != table {
						t.Errorf("%s: mismatch where nothing was changed: %+v", p.Table, p)
					}
				}
				return tableOf(report, table)
			}
			if got := check(""); got.State != "" {
				t.Fatalf("before the change: %+v", got)
			}

			// a copied event (below T0) goes missing
			exec(t, sc.dst, "DELETE FROM events WHERE id = (SELECT MIN(id) FROM events WHERE label = 'Event 50')")
			if got := check("events"); got.State != "mismatch" || got.Missing != 1 {
				t.Errorf("a dropped event: %+v", got)
			}
			exec(t, sc.dst, "INSERT INTO events(node, event_id, label, application, level, date, data, object, search) SELECT node, event_id, label, application, level, date, data, object, search FROM events WHERE label = 'Event 50' LIMIT 1")
			// put back from the source's values
			copyOnce(t, sc.src, sc.dst, testOptions("test", true))

			// a copied event changes (its every column is its identity)
			exec(t, sc.dst, "UPDATE events SET data = 'changed' WHERE label = 'Event 60'")
			if got := check("events"); got.State != "mismatch" || got.Missing != 1 {
				t.Errorf("a changed event: %+v", got)
			}
			exec(t, sc.dst, "DELETE FROM events WHERE label = 'Event 60' AND data = 'changed'")
			copyOnce(t, sc.src, sc.dst, testOptions("test", true))

			// a bucket the target had too goes missing
			exec(t, sc.dst, "DELETE FROM metric_values WHERE key = 'cosmos.system.metric0' AND granularity = 'hourly' AND date = ?", sc.end)
			if got := check("metric_values"); got.State != "mismatch" || got.Missing != 1 {
				t.Errorf("a dropped shared bucket: %+v", got)
			}
			copyOnce(t, sc.src, sc.dst, testOptions("test", true))

			// a security entry goes missing
			exec(t, sc.dst, "DELETE FROM fork_security_events WHERE ts = ?", sc.end-7000)
			if got := check("fork_security_events"); got.State != "mismatch" {
				t.Errorf("a dropped security entry: %+v", got)
			}
			copyOnce(t, sc.src, sc.dst, testOptions("test", true))

			// a metric's key goes missing
			exec(t, sc.dst, "DELETE FROM metrics WHERE key = 'cosmos.system.metric2'")
			if got := check("metrics"); got.State != "mismatch" || got.Missing != 1 {
				t.Errorf("a dropped metric: %+v", got)
			}
		})
	}
}

// Interrupted (cancelled, or the store in use changed) and resumed: the same
// result as in one go, nothing twice.
func TestInterruptAndResume(t *testing.T) {
	for _, p := range pairs() {
		for _, how := range []string{"cancel", "store-changed"} {
			t.Run(short(p.src)+"-to-"+short(p.dst)+"-"+how, func(t *testing.T) {
				sc := newScenario(t, p)
				// the reference: a copy in one go into a copy of the target
				ref := newScenario(t, p)
				copyOnce(t, ref.src, ref.dst, testOptions("ref", true))

				stops := 0
				for round := 0; round < 100; round++ {
					ctx, cancel := context.WithCancel(context.Background())
					calls := 0
					opt := testOptions("test", true)
					opt.Batch = 7
					opt.Guard = func() error {
						calls++
						if calls == 4 {
							if how == "cancel" {
								cancel()
							} else {
								return ErrStoreChanged
							}
						}
						return nil
					}
					report, err := Copy(ctx, sc.src, sc.dst, opt, &Tracker{})
					cancel()
					if err == nil {
						if report.Mismatch {
							t.Fatalf("mismatch after %d interruptions: %+v", stops, report.Tables)
						}
						break
					}
					if (how == "cancel" && !errors.Is(err, errStopped)) || (how == "store-changed" && !errors.Is(err, ErrStoreChanged)) {
						t.Fatalf("round %d: %v", round, err)
					}
					stops++
				}
				if stops < 5 {
					t.Fatalf("only %d interruptions: the test did not interrupt", stops)
				}
				for _, r := range rules {
					q := "SELECT COUNT(*) FROM " + r.table
					if r.table == "monitoring_kv" {
						q += " WHERE key NOT LIKE 'fork_monitoring_copy:%'"
					}
					if got, want := count(t, sc.dst, q), count(t, ref.dst, q); got != want {
						t.Errorf("%s: %d rows after %d interruptions, %d in one go", r.table, got, stops, want)
					}
				}
			})
		}
	}
}

// The local file is opened read-only: the copy cannot change its records, and
// does not; the main file's content stays byte for byte (SQLite may leave the
// companion files -wal and -shm next to it).
func TestLocalFileStaysUntouched(t *testing.T) {
	folder := t.TempDir()
	path := filepath.Join(folder, "database.db")
	local, err := openLocalAt(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := utils.PrepareMonitoringSchema(local.DB, local.Dialect); err != nil {
		t.Fatal(err)
	}
	fill(t, local, contents{node: "server.local", metrics: 2, values: 30, events: 200, notifications: 5, security: 10, end: time.Now().UnixMilli() - 3600000})
	local.DB.Close()
	sum := func() [32]byte {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return sha256.Sum256(data)
	}
	before := sum()

	for _, dialect := range dialects() {
		src, err := openLocalAt(path, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := src.DB.Exec("INSERT INTO monitoring_kv(key, value) VALUES('x', 'y')"); err == nil {
			t.Fatal("the read-only handle took a write")
		}
		dst := openStore(t, dialect)
		report := copyOnce(t, src, dst, testOptions("test", true))
		if report.Written == 0 || report.Mismatch {
			t.Fatalf("%s: %+v", dialect, report)
		}
		src.DB.Close()
		if sum() != before {
			t.Fatalf("%s: the local file changed", dialect)
		}
	}
	entries, _ := os.ReadDir(folder)
	for _, entry := range entries {
		if entry.Name() != "database.db" {
			if info, err := entry.Info(); err == nil && info.Size() > 0 && strings.HasSuffix(entry.Name(), "-wal") {
				t.Errorf("a journal with content was left: %s", entry.Name())
			}
		}
	}
}

// A value PostgreSQL's INTEGER cannot hold stops the copy with a sentence,
// before anything of the batch is written.
func TestTooLargeForPostgres(t *testing.T) {
	if len(dialects()) < 2 {
		t.Skip("no PostgreSQL")
	}
	src, dst := openStore(t, utils.DialectSQLite), openStore(t, utils.DialectPostgres)
	exec(t, src, "INSERT INTO fork_security_events(node, ts, n) VALUES('server.local', ?, ?)", time.Now().UnixMilli()-1000, int64(1)<<33)
	_, err := Copy(context.Background(), src, dst, testOptions("test", true), &Tracker{})
	var copyErr *CopyError
	if !errors.As(err, &copyErr) || copyErr.Table != "fork_security_events" || !strings.Contains(copyErr.Sentence, "column n") {
		t.Fatalf("%v", err)
	}
	if n := count(t, dst, "SELECT COUNT(*) FROM fork_security_events"); n != 0 {
		t.Fatalf("%d rows written", n)
	}
}

// A failure is said in a fixed sentence: never the database's text.
func TestFailureSentences(t *testing.T) {
	c := &copier{}
	r, _ := ruleOf("events")
	err := c.fail(r, "copying", errors.New(`pq: password authentication failed for user "cosmos" secret-value`))
	if strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "table events") {
		t.Fatalf("%q", err.Error())
	}
	if err := c.fail(r, "copying", context.DeadlineExceeded); !strings.Contains(err.Error(), "did not answer in time") {
		t.Fatalf("%q", err.Error())
	}
	if c.fail(r, "copying", errStopped) != errStopped || c.fail(r, "copying", ErrStoreChanged) != ErrStoreChanged {
		t.Fatal("stop and store change must pass through")
	}
}

// Equal rows are a multiset: the target keeps as many as the source has; and
// rows of one time value are never split between two batches.
func TestEqualRowsAndOneTimeValue(t *testing.T) {
	for _, p := range pairs() {
		t.Run(short(p.src)+"-to-"+short(p.dst), func(t *testing.T) {
			src, dst := openStore(t, p.src), openStore(t, p.dst)
			at := time.Now().UnixMilli() - 3600000
			insert := func(s Store, label string) {
				exec(t, s, "INSERT INTO events(node, event_id, label, application, level, date, data, object, search) VALUES('server.local', 'same', ?, 'cosmos', 'info', ?, '', '', '')", label, at)
			}
			for i := 0; i < 3; i++ {
				insert(src, "twin")
			}
			insert(dst, "twin") // one of the three is there already
			for i := 0; i < 25; i++ {
				insert(src, fmt.Sprintf("crowd %d", i))
			}
			opt := testOptions("test", true)
			opt.Batch = 7
			report, err := Copy(context.Background(), src, dst, opt, &Tracker{})
			if err != nil || report.Mismatch {
				t.Fatalf("%v %+v", err, report.Tables)
			}
			if n := count(t, dst, "SELECT COUNT(*) FROM events WHERE label = 'twin'"); n != 3 {
				t.Errorf("twins: %d, want 3", n)
			}
			if n := count(t, dst, "SELECT COUNT(*) FROM events WHERE label LIKE 'crowd %'"); n != 25 {
				t.Errorf("rows of one time value: %d, want 25", n)
			}
			if again, err := Copy(context.Background(), src, dst, opt, &Tracker{}); err != nil || again.Written != 0 {
				t.Errorf("again: %v %d", err, again.Written)
			}
		})
	}
}

// The two stores take turns as the store in use any number of times (back
// to the local file, the fallback while PostgreSQL does not answer, back to
// PostgreSQL), with a copy after each turn. Rows the target wrote in its own
// turns are not an error, whatever their time; every source row must be there.
func TestStoresTakingTurns(t *testing.T) {
	hour := int64(time.Hour / time.Millisecond)
	for _, dialect := range dialects() {
		t.Run("sqlite-to-"+short(dialect), func(t *testing.T) {
			local, remote := openStore(t, utils.DialectSQLite), openStore(t, dialect)
			start := (time.Now().UnixMilli()/hour)*hour - 40*hour
			turn := func(s Store, at int64, name string) {
				fill(t, s, contents{node: "server.local", events: 20, notifications: 3, security: 10, end: at, ref: name})
				// the same metrics and buckets each turn, with other values: buckets both stores wrote
				for m := 0; m < 3; m++ {
					key := fmt.Sprintf("cosmos.system.metric%d", m)
					exec(t, s, "INSERT INTO metrics(node, key, label, last_update) VALUES('server.local', ?, ?, ?) ON CONFLICT(node, key) DO UPDATE SET label = excluded.label, last_update = excluded.last_update", key, name, at)
					for v := int64(0); v < 6; v++ {
						exec(t, s, "INSERT INTO metric_values(node, key, granularity, date, value, avg_index, expire) VALUES('server.local', ?, 'hourly', ?, ?, 1, 0)"+
							" ON CONFLICT(node, key, granularity, date) DO UPDATE SET value = metric_values.value + excluded.value, avg_index = metric_values.avg_index + 1",
							key, at-v*hour, at%100000+v)
					}
					exec(t, s, "INSERT INTO metric_values(node, key, granularity, date, value, avg_index, expire) VALUES('server.local', ?, 'daily', ?, ?, 1, 0)"+
						" ON CONFLICT(node, key, granularity, date) DO UPDATE SET value = metric_values.value + excluded.value, avg_index = metric_values.avg_index + 1",
						key, start, at%100000)
				}
			}
			for round := int64(0); round < 4; round++ {
				at := start + round*8*hour
				turn(local, at, fmt.Sprintf("local%d", round)) // the local file in use
				if round == 0 {
					// the first copy finds PostgreSQL still empty
					copyOnce(t, local, remote, testOptions("turns", true))
				}
				turn(remote, at+2*hour, fmt.Sprintf("remote%d", round))  // PostgreSQL in use
				turn(local, at+4*hour, fmt.Sprintf("fallback%d", round)) // the fallback
				turn(remote, at+6*hour, fmt.Sprintf("healed%d", round))  // PostgreSQL again
				report := copyOnce(t, local, remote, testOptions("turns", true))
				if report.Mismatch {
					t.Fatalf("round %d: a mismatch: %+v", round, report.Tables)
				}
			}
			again := copyOnce(t, local, remote, testOptions("turns", true))
			if again.Written != 0 || again.Mismatch {
				t.Fatalf("again: %d written, mismatch %v", again.Written, again.Mismatch)
			}
			// and a real loss is still seen
			exec(t, remote, "DELETE FROM events WHERE date = ?", start-3000) // an event of the local file's first turn
			report, err := verifyOnly(context.Background(), local, remote, testOptions("turns", true), &Tracker{})
			if err != nil {
				t.Fatal(err)
			}
			if p := tableOf(report, "events"); p.State != "mismatch" || p.Missing < 1 {
				t.Fatalf("a dropped event after the turns: %+v", p)
			}
		})
	}
}

// The check compares values (review round 3, S2): a day total whose
// source part is not all in the target (the target's row lost it, or the row
// is gone) is missing; an open total of a source that is the store in use
// may grow after the copy began, and only its presence is checked.
func TestVerificationSeesAChangedClosedBucket(t *testing.T) {
	for _, p := range pairs() {
		t.Run(short(p.src)+"-to-"+short(p.dst), func(t *testing.T) {
			src, dst := openStore(t, p.src), openStore(t, p.dst)
			day := int64(24 * time.Hour / time.Millisecond)
			now := time.Now().UnixMilli()
			old, today := now-now%day-20*day, now-now%day
			exec(t, src, "INSERT INTO metrics(node, key, agglo_type, time_scale, last_update) VALUES('server.local', 'k', 'sum', 1, ?)", now-1000)
			for _, date := range []int64{old, today} {
				exec(t, src, "INSERT INTO metric_values(node, key, granularity, date, value, avg_index, expire) VALUES('server.local', 'k', 'day', ?, 10, 1, ?)", date, now+40*day)
			}
			copyOnce(t, src, dst, testOptions("test", false))
			exec(t, src, "UPDATE metric_values SET value = 12, avg_index = 2 WHERE date = ?", today) // the open day grows in the source
			report, err := verifyOnly(context.Background(), src, dst, testOptions("test", false), &Tracker{})
			if err != nil || report.Mismatch {
				t.Fatalf("an open bucket that grew: %v %+v", err, tableOf(report, "metric_values"))
			}
			exec(t, dst, "UPDATE monitoring_kv SET value = '4|1' WHERE key LIKE '"+ledgerPrefix+"%' AND key LIKE ?", "%|"+itoa(old))
			report, err = verifyOnly(context.Background(), src, dst, testOptions("test", false), &Tracker{})
			if got := tableOf(report, "metric_values"); err != nil || got.State != "mismatch" || got.Missing != 1 {
				t.Fatalf("a closed total only partly in the target: %v %+v", err, got)
			}
			exec(t, dst, "DELETE FROM metric_values WHERE date = ?", today)
			report, _ = verifyOnly(context.Background(), src, dst, testOptions("test", false), &Tracker{})
			if got := tableOf(report, "metric_values"); got.Missing != 2 {
				t.Fatalf("a total gone from the target: %+v", got)
			}
		})
	}
}

// A value that is not a finite number (PostgreSQL's DOUBLE PRECISION holds
// NaN and infinities) cannot be held by the local file's NOT NULL column: it
// is stored as the neutral value (1 for time_scale, the scale the panel
// divides by) and said; towards PostgreSQL nothing is replaced.
func TestNotANumber(t *testing.T) {
	if len(dialects()) < 2 {
		t.Skip("no PostgreSQL")
	}
	t.Run("pg-to-sqlite", func(t *testing.T) {
		src, dst := openStore(t, utils.DialectPostgres), openStore(t, utils.DialectSQLite)
		for i, value := range []string{"NaN", "Infinity", "-Infinity", "1.5"} {
			exec(t, src, "INSERT INTO metrics(node, key, time_scale, last_update) VALUES('server.local', ?, CAST(? AS DOUBLE PRECISION), 1)", fmt.Sprintf("m%d", i), value)
		}
		report := copyOnce(t, src, dst, testOptions("test", false))
		if report.Mismatch || tableOf(report, "metrics").Replaced != 3 {
			t.Fatalf("%+v", tableOf(report, "metrics"))
		}
		if n := count(t, dst, "SELECT COUNT(*) FROM metrics WHERE time_scale = 1"); n != 3 {
			t.Errorf("stored as 1, the neutral scale: %d", n)
		}
		if n := count(t, dst, "SELECT COUNT(*) FROM metrics WHERE time_scale = 1.5"); n != 1 {
			t.Errorf("a number is kept: %d", n)
		}
		if again := copyOnce(t, src, dst, testOptions("test", false)); again.Mismatch || again.Written != 0 {
			t.Errorf("again: %+v", again)
		}
	})
	t.Run("sqlite-to-pg", func(t *testing.T) {
		src, dst := openStore(t, utils.DialectSQLite), openStore(t, utils.DialectPostgres)
		exec(t, src, "INSERT INTO metrics(node, key, time_scale, last_update) VALUES('server.local', 'inf', 9e999, 1)")
		report := copyOnce(t, src, dst, testOptions("test", true))
		if report.Mismatch || tableOf(report, "metrics").Replaced != 0 {
			t.Fatalf("%+v", tableOf(report, "metrics"))
		}
		if n := count(t, dst, "SELECT COUNT(*) FROM metrics WHERE time_scale = 'Infinity'"); n != 1 {
			t.Errorf("an infinity reaches PostgreSQL as it is: %d", n)
		}
	})
}

// The copy waits between its batches, for as long as the pace asks, after
// every batch of the copy and of the check (no sleep here: the waits are
// counted by a fake).
func TestPauseBetweenBatches(t *testing.T) {
	src, dst := openStore(t, utils.DialectSQLite), openStore(t, utils.DialectSQLite)
	fill(t, src, contents{node: "server.local", events: 100, end: time.Now().UnixMilli() - 3600000})
	var asked []time.Duration
	var slept []time.Duration
	opt := testOptions("pace", true)
	opt.Batch = 10
	opt.Pause = func(took time.Duration) time.Duration {
		asked = append(asked, took)
		return 7 * time.Millisecond
	}
	opt.Sleep = func(ctx context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}
	report := copyOnce(t, src, dst, opt)
	if report.Mismatch || tableOf(report, "events").Written != 100 {
		t.Fatalf("%+v", report.Tables)
	}
	// 100 events in batches of 10: 10 batches copied, 10 checked
	if len(slept) < 20 || len(slept) != len(asked) {
		t.Fatalf("%d waits for %d batches asked", len(slept), len(asked))
	}
	for _, d := range slept {
		if d != 7*time.Millisecond {
			t.Fatalf("waited %s, the pace asked 7 ms", d)
		}
	}
	// the production pace: as long as the batch took, from 5 ms to 0.5 s
	for took, want := range map[time.Duration]time.Duration{0: 5 * time.Millisecond, 40 * time.Millisecond: 40 * time.Millisecond, 3 * time.Second: 500 * time.Millisecond} {
		if got := pauseFor(took); got != want {
			t.Errorf("pauseFor(%s) = %s, want %s", took, got, want)
		}
	}
}
