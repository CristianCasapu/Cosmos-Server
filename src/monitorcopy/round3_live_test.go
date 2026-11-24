package monitorcopy

// Review round 3 (plan 51), live: what the reviewer measured. PostgreSQL is
// cut like a stopped container (connections closed, new ones refused) under
// the real writers (events, notifications, the security record, a summed
// metric), the real watcher at its real cadence falls back and comes back,
// and the real catch-up runs: after it, no row of the outage is left only
// in the local file (S1: events and security entries kept while the pings
// found PostgreSQL gone included), and the metric's day and hour totals in
// PostgreSQL equal what was pushed (S2), before, during and after.
//
// Needs COSMOS_FORK_PG_TEST (host:port) and _DB/_USER/_PASSWORD of an account
// that owns its database (see src/utils/fork_monitoring_store_test.go).
// ROUND3_OUTAGES=n repeats the outage (default 1); each takes about three
// minutes at the watcher's real cadence.

import (
	"database/sql"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/metrics"
	"github.com/azukaar/cosmos-server/src/security"
	"github.com/azukaar/cosmos-server/src/utils"
)

// cutProxy forwards to PostgreSQL; down closes every connection and refuses
// new ones (the listener is closed), up listens again on the same address.
type cutProxy struct {
	target string
	addr   string
	mu     sync.Mutex
	ln     net.Listener
	conns  map[net.Conn]bool
}

func newCutProxy(t *testing.T, target string) *cutProxy {
	p := &cutProxy{target: target, conns: map[net.Conn]bool{}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p.addr = ln.Addr().String()
	p.serve(ln)
	t.Cleanup(p.down)
	return p
}

func (p *cutProxy) serve(ln net.Listener) {
	p.mu.Lock()
	p.ln = ln
	p.mu.Unlock()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go p.pipe(c)
		}
	}()
}

func (p *cutProxy) pipe(c net.Conn) {
	s, err := net.DialTimeout("tcp", p.target, 5*time.Second)
	if err != nil {
		c.Close()
		return
	}
	p.mu.Lock()
	p.conns[c], p.conns[s] = true, true
	p.mu.Unlock()
	done := make(chan struct{}, 2)
	copyOne := func(a, b net.Conn) {
		buf := make([]byte, 32*1024)
		for {
			n, err := a.Read(buf)
			if n > 0 {
				if _, werr := b.Write(buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}
	go copyOne(c, s)
	go copyOne(s, c)
	<-done
	c.Close()
	s.Close()
	p.mu.Lock()
	delete(p.conns, c)
	delete(p.conns, s)
	p.mu.Unlock()
}

func (p *cutProxy) down() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ln != nil {
		p.ln.Close()
		p.ln = nil
	}
	for c := range p.conns {
		c.Close()
	}
}

func (p *cutProxy) up(t *testing.T) {
	for i := 0; i < 50; i++ {
		ln, err := net.Listen("tcp", p.addr)
		if err == nil {
			p.serve(ln)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the proxy could not listen again")
}

func waitFor(t *testing.T, what string, limit time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within %v (%+v)", what, limit, utils.GetMonitoringStoreState())
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func TestNothingLeftBehindLive(t *testing.T) {
	host := os.Getenv("COSMOS_FORK_PG_TEST")
	if host == "" {
		t.Skip("COSMOS_FORK_PG_TEST is not set")
	}
	env := func(name, def string) string {
		if v := os.Getenv(name); v != "" {
			return v
		}
		return def
	}
	outages, _ := strconv.Atoi(env("ROUND3_OUTAGES", "1"))
	proxy := newCutProxy(t, host)
	db := utils.DatabaseConfig{PostgresHost: proxy.addr, PostgresDatabase: env("COSMOS_FORK_PG_TEST_DB", "cosmos"),
		PostgresUsername: env("COSMOS_FORK_PG_TEST_USER", "cosmos"), PostgresPassword: env("COSMOS_FORK_PG_TEST_PASSWORD", "cosmos-test-pw")}
	direct := db
	direct.PostgresHost = host

	prevFolder, prevConfig, prevInUse := utils.CONFIGFOLDER, utils.GetBaseMainConfig(), storeInUse
	utils.CONFIGFOLDER = t.TempDir() + "/"
	config := prevConfig
	config.Database = db
	utils.LoadBaseMainConfig(config)
	storeInUse = inUse
	t.Cleanup(func() {
		utils.CloseMetricsDatabase()
		utils.CONFIGFOLDER = prevFolder
		utils.LoadBaseMainConfig(prevConfig)
		storeInUse = prevInUse
	})
	if err := utils.InitMetricsDatabase(); err != nil || utils.GetMonitoringStoreState().InUse != utils.DialectPostgres {
		t.Fatalf("PostgreSQL first: %v %+v", err, utils.GetMonitoringStoreState())
	}
	security.Start()

	run := fmt.Sprintf("r3-%d", time.Now().UnixNano())
	metric := "round3." + run
	def := metrics.DataDef{Max: 1 << 30, Period: 30 * time.Second, Label: "round 3", AggloType: "sum", SetOperation: "sum"}
	var events, notes, entries, pushed atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	writer := func(every time.Duration, write func(i int)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				write(i)
				time.Sleep(every)
			}
		}()
	}
	// rates far above Cosmos' own, below the bounds of what is kept in
	// memory while PostgreSQL does not answer (5 000 events, 500 notifications)
	writer(10*time.Millisecond, func(i int) {
		utils.InsertEvent(utils.Event{EventId: run, Label: strconv.Itoa(i), Date: time.Now(), Level: "info"})
		events.Add(1)
	})
	writer(100*time.Millisecond, func(i int) {
		utils.WriteNotification(utils.Notification{Recipient: "tester", Title: run + "-" + strconv.Itoa(i), Message: strconv.Itoa(i), Level: "info"})
		notes.Add(1)
	})
	writer(10*time.Millisecond, func(i int) {
		security.Record(security.Event{Kind: utils.SecLoginFailed, IP: "192.0.2." + strconv.Itoa(1+i%250), Target: run + "-" + strconv.Itoa(i)})
		entries.Add(1)
	})
	writer(20*time.Millisecond, func(int) {
		metrics.PushSetMetric(metric, 1, def)
		pushed.Add(1)
	})
	writer(time.Second, func(int) { metrics.SaveMetrics() })

	time.Sleep(5 * time.Second) // PostgreSQL holds today's buckets before the outage
	for n := 0; n < outages; n++ {
		proxy.down()
		cut := time.Now()
		waitFor(t, "the fallback", 90*time.Second, func() bool { s := utils.GetMonitoringStoreState(); return s.Fallback && s.InUse == utils.DialectSQLite })
		fellBack := time.Now()
		time.Sleep(20 * time.Second) // records go on in the local file
		proxy.up(t)
		waitFor(t, "the switch back", 6*time.Minute, func() bool { s := utils.GetMonitoringStoreState(); return !s.Fallback && s.InUse == utils.DialectPostgres })
		back := time.Now()
		waitFor(t, "the catch-up", 4*time.Minute, func() bool {
			c := Status().CatchUp
			return c != nil && c.FinishedAt.After(back)
		})
		c := Status().CatchUp
		t.Logf("outage %d: cut, fallback after %v, back after %v; catch-up %s: aside %d, present %d, missing %d, unchecked %d, inexact %d, written %d",
			n+1, fellBack.Sub(cut).Round(time.Second), back.Sub(cut).Round(time.Second), c.Outcome, c.Aside, c.Present, c.Missing, c.Unchecked, c.Inexact, c.Written)
		if c.Outcome != "done" || !c.Counted || c.Missing != 0 || c.Unchecked != 0 || c.Inexact != 0 || c.Aside == 0 {
			t.Errorf("outage %d: the catch-up says %+v", n+1, *c)
		}
	}
	time.Sleep(3 * time.Second)
	close(stop)
	wg.Wait()
	time.Sleep(500 * time.Millisecond) // the last pushes (their own goroutines)
	metrics.SaveMetrics()
	security.Flush()
	time.Sleep(3 * time.Second) // kept rows written again, drains ended

	local, err := sql.Open(utils.DialectSQLite, "file:"+utils.CONFIGFOLDER+"database.db?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	pgStore, _, err := inUse()
	if err != nil {
		t.Fatal(err)
	}
	pg := pgStore.DB
	count := func(q *sql.DB, query string, args ...interface{}) int64 {
		var n int64
		if err := q.QueryRow(query, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return n
	}
	// left behind: rows of the run in the local file that PostgreSQL does not hold
	leftBehind := func(query string, key func(rows *sql.Rows) (string, error), pgQuery string) (int64, int64) {
		rows, err := local.Query(query, run+"%")
		if err != nil {
			t.Fatal(err)
		}
		inLocal := map[string]int{}
		for rows.Next() {
			k, err := key(rows)
			if err != nil {
				t.Fatal(err)
			}
			inLocal[k]++
		}
		rows.Close()
		prows, err := pg.Query(utils.RebindFor(utils.DialectPostgres, pgQuery), run+"%")
		if err != nil {
			t.Fatal(err)
		}
		for prows.Next() {
			k, err := key(prows)
			if err != nil {
				t.Fatal(err)
			}
			if inLocal[k] > 0 {
				inLocal[k]--
			}
		}
		prows.Close()
		left, total := int64(0), int64(0)
		for _, n := range inLocal {
			left += int64(n)
		}
		for range inLocal {
			total++
		}
		return left, total
	}
	scan := func(rows *sql.Rows) (string, error) {
		var a, b string
		var c int64
		err := rows.Scan(&a, &b, &c)
		return a + "|" + b + "|" + strconv.FormatInt(c, 10), err
	}
	eventsLeft, eventsLocal := leftBehind("SELECT event_id, label, date FROM events WHERE event_id LIKE ?", scan, "SELECT event_id, label, date FROM events WHERE event_id LIKE ?")
	securityLeft, securityLocal := leftBehind("SELECT target, ip, ts FROM fork_security_events WHERE target LIKE ?", scan, "SELECT target, ip, ts FROM fork_security_events WHERE target LIKE ?")
	notesLeft, notesLocal := leftBehind("SELECT title, message, 0 FROM notifications WHERE title LIKE ?", scan, "SELECT title, message, 0 FROM notifications WHERE title LIKE ?")
	pgEvents := count(pg, "SELECT COUNT(*) FROM events WHERE event_id = $1", run)
	pgNotes := count(pg, "SELECT COUNT(*) FROM notifications WHERE title LIKE $1", run+"-%")
	key := "cosmos." + metric
	now := time.Now().UnixMilli()
	dayOf := now - now%int64(24*time.Hour/time.Millisecond)
	pgDay := count(pg, "SELECT COALESCE(SUM(value), 0) FROM metric_values WHERE key = $1 AND granularity = 'day' AND date = $2", key, dayOf)
	localDay := count(local, "SELECT COALESCE(SUM(value), 0) FROM metric_values WHERE key = ? AND granularity = 'day' AND date = ?", key, dayOf)
	pgHours := count(pg, "SELECT COALESCE(SUM(value), 0) FROM metric_values WHERE key = $1 AND granularity = 'hour'", key)
	t.Logf("events: %d written, %d in PostgreSQL, %d in the local file, %d left only there", events.Load(), pgEvents, eventsLocal, eventsLeft)
	t.Logf("notifications: %d written, %d in PostgreSQL, %d in the local file, %d left only there", notes.Load(), pgNotes, notesLocal, notesLeft)
	t.Logf("security entries: %d recorded, %d distinct in the local file, %d left only there", entries.Load(), securityLocal, securityLeft)
	t.Logf("metric %s: %d pushed; day total in PostgreSQL %d (the local file's own part %d); hour totals in PostgreSQL %d", key, pushed.Load(), pgDay, localDay, pgHours)
	if eventsLeft != 0 || securityLeft != 0 || notesLeft != 0 {
		t.Errorf("left only in the local file: events %d, security entries %d, notifications %d", eventsLeft, securityLeft, notesLeft)
	}
	if pgEvents != events.Load() {
		t.Errorf("events in PostgreSQL %d, written %d", pgEvents, events.Load())
	}
	if pgNotes != notes.Load() {
		t.Errorf("notifications in PostgreSQL %d, written %d", pgNotes, notes.Load())
	}
	if pgDay != pushed.Load() || pgHours != pushed.Load() {
		t.Errorf("the metric's totals in PostgreSQL: day %d, hours %d; pushed %d", pgDay, pgHours, pushed.Load())
	}
	_ = direct
}
