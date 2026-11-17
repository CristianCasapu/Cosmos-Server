package monitorcopy

// Test helpers: stores of both dialects and generated contents.
//
// PostgreSQL tests run when COSMOS_FORK_PG_TEST_DSN names a server where the
// user may create databases (each test gets a database of its own), e.g.
//   COSMOS_FORK_PG_TEST_DSN=postgres://cosmos:secret@pg:5432/cosmos?sslmode=disable
// and are skipped without it.

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func pgBase(t testing.TB) string {
	t.Helper()
	dsn := os.Getenv("COSMOS_FORK_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("COSMOS_FORK_PG_TEST_DSN is not set: no PostgreSQL to test against")
	}
	return dsn
}

var databaseCounter atomic.Int64

// newPGDatabase creates an empty database of its own; it is dropped at the end.
func newPGDatabase(t testing.TB) (string, string) {
	t.Helper()
	base := pgBase(t)
	admin, err := sql.Open(utils.DialectPostgres, base)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("monitorcopy_%d_%d_%d", os.Getpid(), time.Now().UnixNano()%1000000, databaseCounter.Add(1))
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		admin.Close()
		t.Fatal("CREATE DATABASE:", err)
	}
	t.Cleanup(func() {
		admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
		admin.Close()
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String(), name
}

// openStore opens an empty, prepared store of the dialect.
func openStore(t testing.TB, dialect string) Store {
	t.Helper()
	var s Store
	if dialect == utils.DialectPostgres {
		dsn, _ := newPGDatabase(t)
		db, err := sql.Open(utils.DialectPostgres, dsn)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(4)
		s = Store{DB: db, Dialect: dialect}
	} else {
		var err error
		s, err = openLocalAt(filepath.Join(t.TempDir(), "database.db"), false)
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { s.DB.Close() })
	if err := utils.PrepareMonitoringSchema(s.DB, s.Dialect); err != nil {
		t.Fatal("PrepareMonitoringSchema:", err)
	}
	return s
}

// dialects are the dialects to test with: SQLite always, PostgreSQL when available.
func dialects() []string {
	if os.Getenv("COSMOS_FORK_PG_TEST_DSN") == "" {
		return []string{utils.DialectSQLite}
	}
	return []string{utils.DialectSQLite, utils.DialectPostgres}
}

func short(dialect string) string {
	if dialect == utils.DialectPostgres {
		return "pg"
	}
	return "sqlite"
}

func exec(t testing.TB, s Store, query string, args ...interface{}) {
	t.Helper()
	if _, err := s.DB.Exec(s.bind(query), args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func count(t testing.TB, s Store, query string, args ...interface{}) int64 {
	t.Helper()
	var n int64
	if err := s.DB.QueryRow(s.bind(query), args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// generated contents -------------------------------------------------------

type contents struct {
	node          string
	metrics       int   // metrics, each with values buckets
	values        int   // buckets per metric (hourly, going back from end)
	events        int   // one a second, going back from end
	notifications int   // one a minute, going back from end
	security      int   // one a second, going back from end
	end           int64 // unix ms: the newest time of the contents
	ref           string // prefix of the notifications' references ("ref" when empty)
}

// fill writes generated rows, in transactions of 5000 rows.
func fill(t testing.TB, s Store, c contents) {
	t.Helper()
	ctx := context.Background()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	pending := 0
	step := func() {
		pending++
		if pending < 5000 {
			return
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		tx, err = s.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		pending = 0
	}
	run := func(query string, args ...interface{}) {
		if _, err := tx.Exec(s.bind(query), args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		step()
	}
	hour := int64(time.Hour / time.Millisecond)
	if c.ref == "" {
		c.ref = "ref"
	}
	for m := 0; m < c.metrics; m++ {
		key := fmt.Sprintf("cosmos.system.metric%d", m)
		run("INSERT INTO metrics(node, key, label, unit, max, agglo_type, scale, object, time_scale, last_update) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			c.node, key, "Metric "+key, "%", int64(100), "avg", int64(1), "", 1.5, c.end-int64(m))
		for v := 0; v < c.values; v++ {
			date := (c.end/hour)*hour - int64(v)*hour
			run("INSERT INTO metric_values(node, key, granularity, date, value, avg_index, expire) VALUES(?, ?, ?, ?, ?, ?, ?)",
				c.node, key, "hourly", date, int64(v*7+m)*1000003, int64(v%5+1), int64(0))
		}
	}
	for e := 0; e < c.events; e++ {
		run("INSERT INTO events(node, event_id, label, application, level, date, data, object, search) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)",
			c.node, fmt.Sprintf("cosmos.event.%d", e%17), fmt.Sprintf("Event %d", e), "cosmos", "info", c.end-int64(e)*1000,
			fmt.Sprintf(`{"n":%d,"text":"something happened"}`, e), "", fmt.Sprintf("event %d", e))
	}
	for n := 0; n < c.notifications; n++ {
		date := c.end - int64(n)*60000
		run("INSERT INTO notifications(node, recipient, title, message, vars, icon, link, date, level, read, actions,"+
			" fork_kind, fork_source, fork_thread, fork_ref, fork_audience, fork_count, fork_first, fork_resolved, fork_search, fork_info)"+
			" VALUES(?, ?, ?, ?, '', '', '', ?, 'warn', ?, '[]', 'kind', 'monitoring', ?, ?, 'admin', 1, ?, 0, '', '{}')",
			c.node, "admin", fmt.Sprintf("Notice %d", n), "Something to look at", date, int64(n%2), fmt.Sprintf("thread%d", n%5),
			fmt.Sprintf("%s%08d", c.ref, n), date)
	}
	for e := 0; e < c.security; e++ {
		run("INSERT INTO fork_security_events(node, ts, kind, class, severity, actor, ip, target, host, path, method, outcome, n, details)"+
			" VALUES(?, ?, 'auth.login.failed', 'incident', 2, '', '203.0.113.7', 'admin', 'server.local', '/cosmos/api/login', 'POST', 'failed', 1, '{}')",
			c.node, c.end-int64(e)*1000)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func testOptions(id string, targetInUse bool) Options {
	return Options{SourceID: id, TargetInUse: targetInUse, Batch: 50}
}

func copyOnce(t testing.TB, src, dst Store, opt Options) Report {
	t.Helper()
	report, err := Copy(context.Background(), src, dst, opt, &Tracker{})
	if err != nil {
		t.Fatalf("Copy: %v (%v)", err, unwrap(err))
	}
	return report
}

func unwrap(err error) error {
	if c, ok := err.(*CopyError); ok {
		return c.Err
	}
	return nil
}

func tableOf(report Report, table string) TableProgress {
	for _, p := range report.Tables {
		if p.Table == table {
			return p
		}
	}
	return TableProgress{}
}
