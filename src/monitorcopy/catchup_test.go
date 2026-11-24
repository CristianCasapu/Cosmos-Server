package monitorcopy

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// three periods of the local file: before the outage (already in the
// target), the outage, after it (the local file was not the store any more:
// a stray row that must not move either)
type periods struct {
	before, from, to, after int64
}

func newPeriods() periods {
	now := time.Now().UnixMilli()
	return periods{before: now - 5*3600000, from: now - 3*3600000, to: now - 2*3600000, after: now - 1800000}
}

// writeAt writes one row of each table at the time at, named by label.
func writeAt(t *testing.T, s Store, at int64, label string) {
	t.Helper()
	exec(t, s, "INSERT INTO events(node, event_id, label, application, level, date, data, object, search) VALUES('server.local', 'e', ?, 'cosmos', 'info', ?, '', '', '')", label, at)
	exec(t, s, "INSERT INTO fork_security_events(node, ts, kind, target) VALUES('server.local', ?, 'auth.login.failed', ?)", at, label)
	exec(t, s, "INSERT INTO notifications(node, recipient, title, date, fork_ref, fork_first) VALUES('server.local', 'admin', ?, ?, ?, ?)", label, at, "ref-"+label, at)
	exec(t, s, "INSERT INTO metrics(node, key, label, last_update) VALUES('server.local', ?, ?, ?)", "metric."+label, label, at)
	exec(t, s, "INSERT INTO metric_values(node, key, granularity, date, value, avg_index, expire) VALUES('server.local', ?, 'hour', ?, 5, 1, 0)", "metric."+label, (at/3600000)*3600000)
	exec(t, s, "INSERT INTO monitoring_kv(key, value) VALUES(?, '1')", "custom."+label)
}

func hasLabel(t *testing.T, s Store, label string) map[string]bool {
	return map[string]bool{
		"events":               count(t, s, "SELECT COUNT(*) FROM events WHERE label = ?", label) > 0,
		"fork_security_events": count(t, s, "SELECT COUNT(*) FROM fork_security_events WHERE target = ?", label) > 0,
		"notifications":        count(t, s, "SELECT COUNT(*) FROM notifications WHERE title = ?", label) > 0,
		"metrics":              count(t, s, "SELECT COUNT(*) FROM metrics WHERE label = ?", label) > 0,
		"metric_values":        count(t, s, "SELECT COUNT(*) FROM metric_values WHERE key = ?", "metric."+label) > 0,
		"monitoring_kv":        count(t, s, "SELECT COUNT(*) FROM monitoring_kv WHERE key = ?", "custom."+label) > 0,
	}
}

// Only the rows written in the window move; twice copies nothing twice.
func TestCopyOfAWindowOnly(t *testing.T) {
	for _, p := range pairs() {
		t.Run(short(p.src)+"-to-"+short(p.dst), func(t *testing.T) {
			src, dst := openStore(t, p.src), openStore(t, p.dst)
			at := newPeriods()
			old := at.from - 3*24*3600000 // older than the day the window reaches back for buckets
			writeAt(t, src, old, "old")
			writeAt(t, src, at.before, "before")
			writeAt(t, src, at.from+60000, "inside")
			writeAt(t, src, at.to+3600000, "after")
			opt := testOptions("window", true)
			opt.From, opt.End = at.from, at.to
			report := copyOnce(t, src, dst, opt)
			if report.Mismatch {
				t.Fatalf("%+v", report.Tables)
			}
			for table, there := range hasLabel(t, dst, "inside") {
				if there != (table != "monitoring_kv") {
					t.Errorf("inside, %s: copied %v", table, there)
				}
			}
			for _, label := range []string{"old", "after"} {
				for table, there := range hasLabel(t, dst, label) {
					if there {
						t.Errorf("%s, %s: copied", label, table)
					}
				}
			}
			// before the window by two hours: its hour bucket is of an hour the
			// window does not touch, so nothing of it (an hour or day bucket is
			// taken from the hour or day the window begins in)
			for table, there := range hasLabel(t, dst, "before") {
				if there {
					t.Errorf("before, %s: copied", table)
				}
			}
			again := copyOnce(t, src, dst, opt)
			if again.Written != 0 || again.Mismatch {
				t.Errorf("again: %+v", again)
			}
		})
	}
}

// catchUpEnv makes dst the store in use (as storeInUse says) and a local
// file in a folder of its own.
type catchUpEnv struct {
	local Store
	dst   Store
	at    periods
	inUse func() (Store, Store, error)
}

func useCatchUp(t *testing.T, dialect string) *catchUpEnv {
	t.Helper()
	folder := t.TempDir() + string(os.PathSeparator)
	previousFolder, previousInUse := utils.CONFIGFOLDER, storeInUse
	utils.CONFIGFOLDER = folder
	local, err := openLocalAt(filepath.Join(folder, "database.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := utils.PrepareMonitoringSchema(local.DB, local.Dialect); err != nil {
		t.Fatal(err)
	}
	env := &catchUpEnv{local: local, dst: openStore(t, dialect), at: newPeriods()}
	env.inUse = func() (Store, Store, error) { return env.dst, env.dst, nil }
	storeInUse = func() (Store, Store, error) { return env.inUse() }
	previousBatch, previousPause := batchSize, pauseFor
	batchSize, pauseFor = 3, func(time.Duration) time.Duration { return 0 }
	t.Cleanup(func() {
		catchUpWG.Wait()
		wait()
		clearSummaries()
		local.DB.Close()
		storeInUse, utils.CONFIGFOLDER = previousInUse, previousFolder
		batchSize, pauseFor = previousBatch, previousPause
		state.mu.Lock()
		state.last, state.catchUp = nil, nil
		state.mu.Unlock()
		catchUpMu.Lock()
		catchUpQueue = nil
		catchUpMu.Unlock()
	})
	return env
}

func (e *catchUpEnv) fill(t *testing.T) {
	for i, at := range []int64{e.at.before, e.at.from + 1000, e.at.from + 600000, e.at.to - 1000, e.at.after} {
		label := []string{"before", "in1", "in2", "in3", "after"}[i]
		writeAt(t, e.local, at, label)
	}
}

// catchUp runs one and waits for its end; it returns what done heard, and
// how often.
func (e *catchUpEnv) catchUp(t *testing.T) (utils.CatchUpResult, int) {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	var got utils.CatchUpResult
	CatchUpAfterFallback(utils.CatchUpWindow{From: time.UnixMilli(e.at.from), To: time.UnixMilli(e.at.to)}, func(r utils.CatchUpResult) {
		mu.Lock()
		calls++
		got = r
		mu.Unlock()
	})
	catchUpWG.Wait()
	mu.Lock()
	defer mu.Unlock()
	return got, calls
}

func pendingOf(t *testing.T, s Store) []Window {
	windows, err := pendingWindows(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	return windows
}

func TestCatchUpAfterFallback(t *testing.T) {
	for _, dialect := range dialects() {
		t.Run(short(dialect), func(t *testing.T) {
			env := useCatchUp(t, dialect)
			recorded := captureSecurity(t)
			env.fill(t)
			before := map[string]bool{}
			for table, there := range hasLabel(t, env.dst, "after") {
				before[table] = there
			}

			got, calls := env.catchUp(t)
			// three rows of each of five tables in the window
			if calls != 1 || !got.Ran || got.Missing != 0 || got.Problem != "" || got.Copied < 15 {
				t.Fatalf("done: %d calls, %+v", calls, got)
			}
			for _, label := range []string{"in1", "in2", "in3"} {
				for table, there := range hasLabel(t, env.dst, label) {
					if there != (table != "monitoring_kv") {
						t.Errorf("%s, %s: copied %v", label, table, there)
					}
				}
			}
			for table, there := range hasLabel(t, env.dst, "after") {
				if there {
					t.Errorf("after the window, %s: copied", table)
				}
			}
			if left := pendingOf(t, env.dst); len(left) != 0 {
				t.Errorf("still pending: %v", left)
			}
			view := Status()
			if view.CatchUp == nil || view.CatchUp.Outcome != "done" || view.CatchUp.Written != got.Copied || view.CatchUp.From != env.at.from || view.CatchUp.To != env.at.to {
				t.Errorf("the page's line: %+v", view.CatchUp)
			}
			found := false
			for _, e := range recorded() {
				if e.Kind == utils.SecMonitoringCopy && e.Actor == "" && e.Details["action"] == "catchup" && e.Outcome == "ok" && e.Details["missing"] == "0" {
					found = true
				}
			}
			if !found {
				t.Errorf("not in the security record as automatic: %+v", recorded())
			}

			// twice: nothing twice
			again, calls := env.catchUp(t)
			if calls != 1 || !again.Ran || again.Copied != 0 || again.Missing != 0 {
				t.Errorf("again: %d calls, %+v", calls, again)
			}
		})
	}
}

// Interrupted (PostgreSQL went away again): done hears what is missing and
// why; the window stays pending and is taken up when PostgreSQL is back.
func TestCatchUpResumesThroughTheMarker(t *testing.T) {
	for _, dialect := range dialects() {
		t.Run(short(dialect), func(t *testing.T) {
			env := useCatchUp(t, dialect)
			for i := 0; i < 40; i++ {
				writeAt(t, env.local, env.at.from+int64(i)*1000, "row"+string(rune('a'+i%26))+string(rune('a'+i/26)))
			}
			calls := 0
			var mu sync.Mutex
			asked := 0
			env.inUse = func() (Store, Store, error) {
				mu.Lock()
				defer mu.Unlock()
				asked++
				if asked > 12 {
					return Store{}, Store{}, ErrNotPostgres
				}
				return env.dst, env.dst, nil
			}
			got, calls := env.catchUp(t)
			// true numbers: every record counted, the ones not gone through are "not checked"
			if calls != 1 || !got.Ran || !got.Counted || got.Complete() || got.Unchecked == 0 || got.Aside != got.Present+got.Missing+got.Unchecked+got.Inexact || !strings.Contains(got.Problem, "changed") {
				t.Fatalf("interrupted: %d calls, %+v", calls, got)
			}
			if left := pendingOf(t, env.dst); len(left) != 1 || left[0].From != env.at.from {
				t.Fatalf("pending after the interruption: %v", left)
			}
			partly := count(t, env.dst, "SELECT COUNT(*) FROM events")

			// PostgreSQL is the store again (also what a restart does)
			env.inUse = func() (Store, Store, error) { return env.dst, env.dst, nil }
			ResumeCatchUp()
			catchUpWG.Wait()
			if n := count(t, env.dst, "SELECT COUNT(*) FROM events"); n != 40 || n < partly {
				t.Errorf("events after the resume: %d (%d before)", n, partly)
			}
			if left := pendingOf(t, env.dst); len(left) != 0 {
				t.Errorf("still pending: %v", left)
			}
			if c := Status().CatchUp; c == nil || c.Outcome != "done" {
				t.Errorf("the page's line: %+v", c)
			}
		})
	}
}

// A copy started by hand has the slot: the catch-up is skipped with a line,
// done hears Ran=false at once, the window waits for the next time.
func TestCatchUpSkippedWhileACopyRuns(t *testing.T) {
	env := useCatchUp(t, utils.DialectSQLite)
	env.fill(t)
	state.mu.Lock()
	state.running, state.direction, state.track = true, ToPostgres, &Tracker{}
	state.mu.Unlock()
	got, calls := env.catchUp(t)
	if calls != 1 || got.Ran {
		t.Fatalf("while a copy runs: %d calls, %+v", calls, got)
	}
	if c := Status().CatchUp; c == nil || c.Outcome != "skipped" || c.Message == "" {
		t.Errorf("the page's line: %+v", c)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(pendingOf(t, env.dst)) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if left := pendingOf(t, env.dst); len(left) != 1 {
		t.Fatalf("not kept for later: %v", left)
	}
	if n := count(t, env.dst, "SELECT COUNT(*) FROM events"); n != 0 {
		t.Errorf("copied while the slot was taken: %d", n)
	}
	state.mu.Lock()
	state.running, state.direction = false, ""
	state.mu.Unlock()
	ResumeCatchUp()
	catchUpWG.Wait()
	if n := count(t, env.dst, "SELECT COUNT(*) FROM events"); n != 3 {
		t.Errorf("events after the slot was free: %d", n)
	}
}

// A failure: done hears Ran with Missing and a fixed sentence.
func TestCatchUpFailureSaysWhy(t *testing.T) {
	env := useCatchUp(t, utils.DialectSQLite)
	env.fill(t)
	// a local file of an older layout: the notifications without the fork's columns
	exec(t, env.local, "ALTER TABLE notifications DROP COLUMN fork_first")
	recorded := captureSecurity(t)
	got, calls := env.catchUp(t)
	// stopped before it could count: no number is given as known
	if calls != 1 || !got.Ran || got.Counted || got.Complete() || got.Aside != 0 || !strings.Contains(got.Problem, "older layout") {
		t.Fatalf("%d calls, %+v", calls, got)
	}
	failed := false
	for _, e := range recorded() {
		failed = failed || (e.Kind == utils.SecMonitoringCopy && e.Details["action"] == "catchup" && e.Outcome == "failed")
	}
	if !failed {
		t.Errorf("the failure is not in the security record: %+v", recorded())
	}
	if c := Status().CatchUp; c == nil || c.Outcome != "failed" || c.Message == "" {
		t.Errorf("the page's line: %+v", c)
	}
}

// done is called once even when the window is empty or reversed.
func TestCatchUpOfNothing(t *testing.T) {
	useCatchUp(t, utils.DialectSQLite)
	calls := 0
	CatchUpAfterFallback(utils.CatchUpWindow{To: time.Now()}, func(utils.CatchUpResult) { calls++ })
	CatchUpAfterFallback(utils.CatchUpWindow{From: time.Now(), To: time.Now().Add(-time.Minute)}, func(utils.CatchUpResult) { calls++ })
	catchUpWG.Wait()
	if calls != 2 {
		t.Fatalf("%d calls", calls)
	}
}

func TestWindowsMerge(t *testing.T) {
	got := merge([]Window{{From: 50, To: 60}, {From: 10, To: 20}, {From: 15, To: 30}, {From: 30, To: 40}, {From: 70, To: 70}})
	want := []Window{{From: 10, To: 40}, {From: 50, To: 60}}
	if len(got) != len(want) || !got[0].same(want[0]) || !got[1].same(want[1]) {
		t.Fatalf("%v", got)
	}
}

// The copy's own statements carry their own bound, not the store sessions'
// 30 s statement_timeout (here 100 ms).
func TestStatementTimeoutOfTheCopy(t *testing.T) {
	if len(dialects()) < 2 {
		t.Skip("no PostgreSQL")
	}
	dsn, name := newPGDatabase(t)
	// the contents first, through a session without the bound (review round
	// 3, M9: the test's own inserts must not meet the bound it tests)
	setup, err := sql.Open(utils.DialectPostgres, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer setup.Close()
	if err := utils.PrepareMonitoringSchema(setup, utils.DialectPostgres); err != nil {
		t.Fatal(err)
	}
	fill(t, Store{DB: setup, Dialect: utils.DialectPostgres}, contents{node: "server.local", metrics: 5, values: 400, events: 3000, notifications: 50, security: 300, end: time.Now().UnixMilli() - 3600000})
	if _, err := setup.Exec("ALTER DATABASE " + name + " SET statement_timeout = '100ms'"); err != nil {
		t.Fatal(err)
	}
	setup.Close()
	// new sessions carry the bound
	db, err := sql.Open(utils.DialectPostgres, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := Store{DB: db, Dialect: utils.DialectPostgres}
	if _, err := db.Exec("SELECT pg_sleep(0.3)"); err == nil {
		t.Fatal("the session's own bound is not 100 ms: the test proves nothing")
	}
	var one int
	if err := s.scalars(context.Background(), "SELECT 1 FROM pg_sleep(0.3)", nil, &one); err != nil {
		t.Errorf("a statement of the copy within its bound: %v", err)
	}
	tx, err := s.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("SELECT pg_sleep(0.3)"); err != nil {
		t.Errorf("a statement of a batch's transaction: %v", err)
	}
	tx.Rollback()
	// and a whole copy out of a store with that bound
	dst := openStore(t, utils.DialectSQLite)
	if report := copyOnce(t, s, dst, testOptions("bound", false)); report.Mismatch {
		t.Fatalf("%+v", report.Tables)
	}
}
