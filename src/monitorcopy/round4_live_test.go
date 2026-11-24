package monitorcopy

// Review round 4 (plan 51), live, against a real PostgreSQL through proxies
// that cut it like a stopped container, the real writers and the real
// catch-up (the switches are driven by openings, as a save or the watcher
// makes them):
//   - TestAServerBackAfterAnotherLive: the reviewer's sequence. Server A does
//     not answer; the owner saves B; B has an outage of its own and comes
//     back; the owner saves A again. Nothing of B's goes into A, nothing of
//     A's into B; each catch-up's numbers are its own server's.
//   - TestManyCutsLive (ROUND4_CUTS=n): n short outages under the writers: a
//     summed metric, an averaged gauge that falls within each raw bucket and a
//     maximum end exact in PostgreSQL, every event and notification once.
// Needs COSMOS_FORK_PG_TEST, _USER, _PASSWORD (an account that may own a
// database) and COSMOS_FORK_PG_TEST_DSN (a role that may create databases).

import (
	"database/sql"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/metrics"
	"github.com/azukaar/cosmos-server/src/utils"
)

type liveServer struct {
	proxy *cutProxy
	db    utils.DatabaseConfig
	name  string
}

// liveServers makes databases of their own (owned by the test account), each
// behind a proxy of its own.
func liveServers(t *testing.T, names ...string) []liveServer {
	t.Helper()
	host, admin := os.Getenv("COSMOS_FORK_PG_TEST"), os.Getenv("COSMOS_FORK_PG_TEST_DSN")
	if host == "" || admin == "" {
		t.Skip("COSMOS_FORK_PG_TEST and COSMOS_FORK_PG_TEST_DSN are not set")
	}
	user, password := os.Getenv("COSMOS_FORK_PG_TEST_USER"), os.Getenv("COSMOS_FORK_PG_TEST_PASSWORD")
	if user == "" {
		user, password = "cosmos", "cosmos-test-pw"
	}
	conn, err := sql.Open(utils.DialectPostgres, admin)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	out := []liveServer{}
	for _, n := range names {
		name := fmt.Sprintf("r4%s_%d", n, time.Now().UnixNano()%1e9)
		if _, err := conn.Exec("CREATE DATABASE " + name + " OWNER " + user); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)") })
		p := newCutProxy(t, host)
		out = append(out, liveServer{proxy: p, name: name, db: utils.DatabaseConfig{PostgresHost: p.addr, PostgresDatabase: name, PostgresUsername: user, PostgresPassword: password}})
	}
	return out
}

// useLiveStore points Cosmos at db, in a configuration folder of its own.
func useLiveStore(t *testing.T, db utils.DatabaseConfig) {
	t.Helper()
	prevFolder, prevConfig, prevInUse := utils.CONFIGFOLDER, utils.GetBaseMainConfig(), storeInUse
	utils.CONFIGFOLDER = t.TempDir() + "/"
	config := prevConfig
	config.Database = db
	utils.LoadBaseMainConfig(config)
	storeInUse = inUse
	t.Cleanup(func() {
		waitCatchUpsDone(t, 2*time.Minute)
		utils.CloseMetricsDatabase()
		utils.CONFIGFOLDER = prevFolder
		utils.LoadBaseMainConfig(prevConfig)
		storeInUse = prevInUse
	})
	if err := utils.InitMetricsDatabase(); err != nil || utils.GetMonitoringStoreState().InUse != utils.DialectPostgres {
		t.Fatalf("PostgreSQL first: %v %+v", err, utils.GetMonitoringStoreState())
	}
}

func saveSettings(db utils.DatabaseConfig) {
	config := utils.GetBaseMainConfig()
	config.Database = db
	utils.LoadBaseMainConfig(config)
	utils.InitMetricsDatabase()
}

// waitCatchUpsDone waits until no catch-up is running nor waiting.
func waitCatchUpsDone(t *testing.T, limit time.Duration) {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		catchUpMu.Lock()
		busy := catchUpWorker || len(catchUpQueue) > 0
		catchUpMu.Unlock()
		if !busy && !utils.MonitoringStorePartial() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Errorf("catch-ups still running after %v", limit)
}

func liveCount(t *testing.T, db *sql.DB, query string, args ...interface{}) int64 {
	t.Helper()
	var n sql.NullInt64
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n.Int64
}

func openDirect(t *testing.T, s liveServer) *sql.DB {
	t.Helper()
	u := os.Getenv("COSMOS_FORK_PG_TEST")
	db, err := sql.Open(utils.DialectPostgres, "postgres://"+s.db.PostgresUsername+":"+s.db.PostgresPassword+"@"+u+"/"+s.name+"?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestAServerBackAfterAnotherLive(t *testing.T) {
	servers := liveServers(t, "a", "b")
	a, b := servers[0], servers[1]
	useLiveStore(t, a.db)
	run := fmt.Sprintf("aba-%d", time.Now().UnixNano())
	metric := "round4." + run
	def := metrics.DataDef{Max: 1 << 30, Period: 30 * time.Second, Label: "round 4", AggloType: "sum", SetOperation: "sum"}
	var phase atomic.Value
	phase.Store("A-up")
	var paused atomic.Bool
	pushed := map[string]*atomic.Int64{"A": {}, "B": {}}
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
				if !paused.Load() {
					write(i)
				}
				time.Sleep(every)
			}
		}()
	}
	writer(10*time.Millisecond, func(i int) {
		utils.InsertEvent(utils.Event{EventId: run, Label: phase.Load().(string), Object: strconv.Itoa(i), Date: time.Now(), Level: "info"})
	})
	writer(100*time.Millisecond, func(i int) {
		utils.WriteNotification(utils.Notification{Recipient: "tester", Title: run + "-" + strconv.Itoa(i), Message: phase.Load().(string), Level: "info"})
	})
	writer(20*time.Millisecond, func(int) {
		owner := phase.Load().(string)[:1]
		metrics.PushSetMetric(metric, 1, def)
		pushed[owner].Add(1)
	})
	writer(time.Second, func(int) { metrics.SaveMetrics() })
	// a change of the settings: the writers held, the metrics flushed into the
	// store in use, then the opening
	quiet := func(next string, change func()) {
		paused.Store(true)
		time.Sleep(300 * time.Millisecond) // the last pushes (their own goroutines)
		metrics.SaveMetrics()
		change()
		phase.Store(next)
		paused.Store(false)
	}
	catchUpAfter := func(since time.Time) *Result {
		waitCatchUpsDone(t, 3*time.Minute)
		c := Status().CatchUp
		if c == nil || !c.FinishedAt.After(since) {
			t.Fatalf("no catch-up after %v: %+v", since, c)
		}
		r := *c
		return &r
	}

	time.Sleep(3 * time.Second)
	a.proxy.down() // A does not answer
	utils.InitMetricsDatabase()
	phase.Store("A-out")
	time.Sleep(5 * time.Second)
	quiet("B-up", func() { saveSettings(b.db) }) // the owner saves B
	time.Sleep(3 * time.Second)
	b.proxy.down() // B's own outage
	utils.InitMetricsDatabase()
	phase.Store("B-out")
	time.Sleep(5 * time.Second)
	b.proxy.up(t)
	backB := time.Now()
	quiet("B-up", func() { utils.InitMetricsDatabase() }) // B answers again
	caughtB := catchUpAfter(backB)
	time.Sleep(2 * time.Second)
	a.proxy.up(t)
	backA := time.Now()
	quiet("A-back", func() { saveSettings(a.db) }) // the owner saves A again
	caughtA := catchUpAfter(backA)
	time.Sleep(2 * time.Second)
	close(stop)
	wg.Wait()
	time.Sleep(300 * time.Millisecond)
	metrics.SaveMetrics()
	waitCatchUpsDone(t, time.Minute)
	time.Sleep(3 * time.Second)

	pa, pb := openDirect(t, a), openDirect(t, b)
	local, err := sql.Open(utils.DialectSQLite, "file:"+utils.CONFIGFOLDER+"database.db?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	for _, owner := range []string{"A", "B"} {
		for _, store := range []struct {
			name string
			db   *sql.DB
		}{{"A", pa}, {"B", pb}} {
			events := liveCount(t, store.db, "SELECT COUNT(*) FROM events WHERE event_id = $1 AND substr(label, 1, 1) = $2", run, owner)
			notes := liveCount(t, store.db, "SELECT COUNT(*) FROM notifications WHERE title LIKE $1 AND substr(message, 1, 1) = $2", run+"-%", owner)
			t.Logf("written while %s was asked for: %d events and %d notifications in %s", owner, events, notes, store.name)
			if owner != store.name && (events != 0 || notes != 0) {
				t.Errorf("%d events and %d notifications of %s's time went into %s", events, notes, owner, store.name)
			}
		}
		// every row of the owner's time is in the owner's database
		left := liveCount(t, local, "SELECT COUNT(*) FROM events WHERE event_id = ? AND substr(label, 1, 1) = ?", run, owner)
		t.Logf("%s's events in the local file: %d", owner, left)
	}
	if rows, err := local.Query("SELECT MIN(id), MAX(id), substr(label, 1, 1), MIN(date), MAX(date) FROM events WHERE event_id = ? GROUP BY substr(label, 1, 1)", run); err == nil {
		for rows.Next() {
			var lo, hi, d0, d1 int64
			var o string
			rows.Scan(&lo, &hi, &o, &d0, &d1)
			t.Logf("local %s: ids %d..%d, dates %s..%s", o, lo, hi, time.UnixMilli(d0).Format("15:04:05.000"), time.UnixMilli(d1).Format("15:04:05.000"))
		}
		rows.Close()
	}
	totalA := liveCount(t, pa, "SELECT COUNT(*) FROM events WHERE event_id = $1 AND substr(label, 1, 1) = 'A'", run)
	totalB := liveCount(t, pb, "SELECT COUNT(*) FROM events WHERE event_id = $1 AND substr(label, 1, 1) = 'B'", run)
	localA := liveCount(t, local, "SELECT COUNT(*) FROM events WHERE event_id = ? AND substr(label, 1, 1) = 'A'", run)
	localB := liveCount(t, local, "SELECT COUNT(*) FROM events WHERE event_id = ? AND substr(label, 1, 1) = 'B'", run)
	if localA == 0 || localB == 0 {
		t.Errorf("the test proves nothing: %d and %d events in the local file", localA, localB)
	}
	// each catch-up went through its own server's rows: the local file's events of its time
	if got := tableOf(Report{Tables: caughtB.Tables}, "events"); got.SourceRows != localB {
		t.Errorf("B's catch-up went through %d events, the local file holds %d of B's time", got.SourceRows, localB)
	}
	if got := tableOf(Report{Tables: caughtA.Tables}, "events"); got.SourceRows != localA {
		t.Errorf("A's catch-up went through %d events, the local file holds %d of A's time", got.SourceRows, localA)
	}
	for name, c := range map[string]*Result{"A": caughtA, "B": caughtB} {
		t.Logf("%s's window: %v .. %v", name, time.UnixMilli(c.From).Format("15:04:05.000"), time.UnixMilli(c.To).Format("15:04:05.000"))
		t.Logf("%s's catch-up: %s, records %d, present %d, missing %d, unchecked %d, inexact %d", name, c.Outcome, c.Aside, c.Present, c.Missing, c.Unchecked, c.Inexact)
		if c.Outcome != "done" || !c.Counted || c.Missing != 0 || c.Unchecked != 0 || c.Inexact != 0 || c.Aside != c.Present {
			t.Errorf("%s's catch-up: %+v", name, *c)
		}
	}
	key := "cosmos." + metric
	dayA := liveCount(t, pa, "SELECT SUM(value) FROM metric_values WHERE key = $1 AND granularity = 'day'", key)
	dayB := liveCount(t, pb, "SELECT SUM(value) FROM metric_values WHERE key = $1 AND granularity = 'day'", key)
	// the raw bucket open when the settings named B was written whole into
	// B's new day row (it reads right on its own); its part flushed before
	// the change is noted there, and no merge adds it again
	priorB := int64(0)
	if rows, err := pb.Query("SELECT value FROM monitoring_kv WHERE key LIKE $1", utils.PriorPrefix+"%|"+key+"|day|%"); err == nil {
		for rows.Next() {
			var v string
			rows.Scan(&v)
			p, _ := utils.ParsePair(v)
			priorB += p[0]
		}
		rows.Close()
	}
	t.Logf("events: A %d, B %d; day totals: A %d of %d pushed in its time, B %d (of which %d noted as flushed before the change) of %d",
		totalA, totalB, dayA, pushed["A"].Load(), dayB, priorB, pushed["B"].Load())
	if dayA != pushed["A"].Load() || dayB-priorB != pushed["B"].Load() {
		t.Errorf("day totals: A %d (pushed %d), B %d less %d (pushed %d)", dayA, pushed["A"].Load(), dayB, priorB, pushed["B"].Load())
	}
}

func TestManyCutsLive(t *testing.T) {
	cuts, _ := strconv.Atoi(os.Getenv("ROUND4_CUTS"))
	if cuts <= 0 {
		t.Skip("ROUND4_CUTS is not set")
	}
	servers := liveServers(t, "cuts")
	s := servers[0]
	useLiveStore(t, s.db)
	run := fmt.Sprintf("cuts-%d", time.Now().UnixNano())
	period := 30 * time.Second
	sum := metrics.DataDef{Max: 1 << 30, Period: period, Label: "sum", AggloType: "sum", SetOperation: "sum"}
	gauge := metrics.DataDef{Max: 1 << 30, Period: period, Label: "gauge", AggloType: "avg", SetOperation: "min"}
	peak := metrics.DataDef{Max: 1 << 30, Period: period, Label: "max", AggloType: "max", SetOperation: "max"}
	var mu sync.Mutex
	var sumPushed int64
	gaugeMin := map[int64]int64{} // raw bucket -> the gauge's lowest value in it (its value)
	maxPushed := int64(0)
	var events, notes atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	loop := func(every time.Duration, write func(i int)) {
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
	loop(10*time.Millisecond, func(i int) {
		utils.InsertEvent(utils.Event{EventId: run, Label: strconv.Itoa(i), Date: time.Now(), Level: "info"})
		events.Add(1)
	})
	loop(100*time.Millisecond, func(i int) {
		utils.WriteNotification(utils.Notification{Recipient: "tester", Title: run + "-" + strconv.Itoa(i), Message: "x", Level: "info"})
		notes.Add(1)
	})
	loop(25*time.Millisecond, func(i int) {
		now := time.Now()
		into := now.UnixNano() % int64(period)
		if into < int64(100*time.Millisecond) || into > int64(period-100*time.Millisecond) {
			return // too near a raw bucket's edge to know which one the push lands in
		}
		bucket := now.UnixNano() - into
		value := int64(1000 - into/int64(time.Second)*20 - int64(i%7)) // falls within each raw bucket
		peakValue := int64(i % 997)
		metrics.PushSetMetric("round4."+run+".sum", 1, sum)
		metrics.PushSetMetric("round4."+run+".gauge", int(value), gauge)
		metrics.PushSetMetric("round4."+run+".max", int(peakValue), peak)
		mu.Lock()
		sumPushed++
		if m, ok := gaugeMin[bucket]; !ok || value < m {
			gaugeMin[bucket] = value
		}
		if peakValue > maxPushed {
			maxPushed = peakValue
		}
		mu.Unlock()
	})
	loop(time.Second, func(int) { metrics.SaveMetrics() })

	var handedMu sync.Mutex
	handed := []string{}
	prevCatchUp := utils.MonitoringCatchUp
	utils.MonitoringCatchUp = func(w utils.CatchUpWindow, done func(utils.CatchUpResult)) {
		handedMu.Lock()
		handed = append(handed, fmt.Sprintf("events %d..%d since %s back %s", w.After["events"], w.Before["events"], w.Since.Format("05.000"), w.Back.Format("05.000")))
		handedMu.Unlock()
		prevCatchUp(w, done)
	}
	t.Cleanup(func() { utils.MonitoringCatchUp = prevCatchUp })
	time.Sleep(3 * time.Second)
	started := time.Now()
	for n := 0; n < cuts; n++ {
		s.proxy.down()
		utils.InitMetricsDatabase() // the store stopped answering: the local file
		time.Sleep(time.Duration(200+n%5*100) * time.Millisecond)
		s.proxy.up(t)
		utils.InitMetricsDatabase() // it answers again
		time.Sleep(time.Duration(100+n%3*100) * time.Millisecond)
	}
	t.Logf("%d cuts in %v", cuts, time.Since(started).Round(time.Second))
	time.Sleep(time.Second)
	close(stop)
	wg.Wait()
	time.Sleep(300 * time.Millisecond)
	metrics.SaveMetrics()
	waitCatchUpsDone(t, 10*time.Minute)
	time.Sleep(3 * time.Second)

	pg := openDirect(t, s)
	if local, err := sql.Open(utils.DialectSQLite, "file:"+utils.CONFIGFOLDER+"database.db?mode=ro"); err == nil {
		var n int64
		local.QueryRow("SELECT COUNT(*) FROM events WHERE event_id = ?", run).Scan(&n)
		var recs string
		local.QueryRow("SELECT value FROM monitoring_kv WHERE key = 'fork_monitoring_copy:fallbacks'").Scan(&recs)
		if len(recs) > 600 {
			recs = recs[:600]
		}
		t.Logf("the local file: %d events of the run; records left: %s", n, recs)
		handedMu.Lock()
		for _, h := range handed {
			t.Logf("handed: %s", h)
		}
		handedMu.Unlock()
		if rows, err := local.Query("SELECT id, label FROM events WHERE event_id = ? ORDER BY id", run); err == nil {
			missing := []string{}
			for rows.Next() {
				var id int64
				var label string
				rows.Scan(&id, &label)
				var one int
				if pg.QueryRow("SELECT 1 FROM events WHERE event_id = $1 AND label = $2", run, label).Scan(&one) != nil {
					missing = append(missing, strconv.FormatInt(id, 10))
				}
			}
			rows.Close()
			if len(missing) > 40 {
				missing = append(missing[:20], append([]string{"..."}, missing[len(missing)-20:]...)...)
			}
			t.Logf("local ids not in PostgreSQL: %s", strings.Join(missing, " "))
		}
		if rows, err := local.Query("SELECT id, title FROM notifications WHERE title LIKE ? ORDER BY id", run+"-%"); err == nil {
			missing, inLocal := []string{}, 0
			for rows.Next() {
				var id int64
				var title string
				rows.Scan(&id, &title)
				inLocal++
				var one int
				if pg.QueryRow("SELECT 1 FROM notifications WHERE title = $1", title).Scan(&one) != nil {
					missing = append(missing, strconv.FormatInt(id, 10))
				}
			}
			rows.Close()
			if len(missing) > 40 {
				missing = append(missing[:20], append([]string{"..."}, missing[len(missing)-20:]...)...)
			}
			t.Logf("notifications in the local file %d; not in PostgreSQL: %s", inLocal, strings.Join(missing, " "))
		}
		local.Close()
	}
	c := Status().CatchUp
	if c != nil {
		t.Logf("the last catch-up: %s, records %d, present %d, missing %d, unchecked %d, inexact %d", c.Outcome, c.Aside, c.Present, c.Missing, c.Unchecked, c.Inexact)
		if c.Missing != 0 || c.Unchecked != 0 || c.Inexact != 0 {
			t.Errorf("the last catch-up says %+v", *c)
		}
	}
	pending := liveCount(t, pg, "SELECT COUNT(*) FROM monitoring_kv WHERE key = $1 AND value <> '[]'", catchUpKey)
	pgEvents := liveCount(t, pg, "SELECT COUNT(*) FROM events WHERE event_id = $1", run)
	pgNotes := liveCount(t, pg, "SELECT COUNT(*) FROM notifications WHERE title LIKE $1", run+"-%")
	dup := liveCount(t, pg, "SELECT COUNT(*) FROM (SELECT title FROM notifications WHERE title LIKE $1 GROUP BY title HAVING COUNT(*) > 1) d", run+"-%")
	day := func(key string) (int64, int64) {
		var v, i sql.NullInt64
		pg.QueryRow("SELECT SUM(value), SUM(avg_index) FROM metric_values WHERE key = $1 AND granularity = 'day'", "cosmos.round4."+run+"."+key).Scan(&v, &i)
		return v.Int64, i.Int64
	}
	mu.Lock()
	gaugeTruth, buckets := int64(0), int64(len(gaugeMin))
	for _, m := range gaugeMin {
		gaugeTruth += m
	}
	mu.Unlock()
	sumDay, _ := day("sum")
	gaugeDay, gaugeCount := day("gauge")
	maxDay, _ := day("max")
	t.Logf("events %d written, %d in PostgreSQL; notifications %d written, %d in PostgreSQL, %d twice; windows pending %d", events.Load(), pgEvents, notes.Load(), pgNotes, dup, pending)
	t.Logf("sum: %d pushed, day %d; gauge: %d over %d raw buckets, day %d with count %d (average %.1f against %.1f); max: %d pushed, day %d",
		sumPushed, sumDay, gaugeTruth, buckets, gaugeDay, gaugeCount, float64(gaugeDay)/math.Max(1, float64(gaugeCount)), float64(gaugeTruth)/math.Max(1, float64(buckets)), maxPushed, maxDay)
	if pgEvents != events.Load() || pgNotes != notes.Load() || dup != 0 || pending != 0 {
		t.Errorf("rows: events %d of %d, notifications %d of %d, %d twice, %d windows pending", pgEvents, events.Load(), pgNotes, notes.Load(), dup, pending)
	}
	if sumDay != sumPushed || gaugeDay != gaugeTruth || gaugeCount != buckets || maxDay != maxPushed {
		t.Errorf("totals: sum %d of %d, gauge %d/%d of %d/%d, max %d of %d", sumDay, sumPushed, gaugeDay, gaugeCount, gaugeTruth, buckets, maxDay, maxPushed)
	}
	_ = strings.TrimSpace
}
