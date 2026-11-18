package utils

// Fork addition (plan 51, review round 2, S2): the real writers of events and
// notifications through 200 switches of the store between PostgreSQL and the
// local file, both ways, while PostgreSQL answers: no error, no row lost.
// Needs COSMOS_FORK_PG_TEST (see fork_monitoring_store_test.go).

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
)

// countBothStores counts rows in the local file and in PostgreSQL.
func countBothStores(t *testing.T, pg DatabaseConfig, query string, args ...interface{}) int {
	t.Helper()
	local, err := sql.Open(DialectSQLite, localFileDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	var a, b int
	if err := local.QueryRow(query, args...).Scan(&a); err != nil {
		t.Fatal(err)
	}
	cfg, err := postgresStoreConfig(pg)
	if err != nil {
		t.Fatal(err)
	}
	remote := stdlib.OpenDB(*cfg)
	defer remote.Close()
	if err := remote.QueryRow(rebindFor(DialectPostgres, query), args...).Scan(&b); err != nil {
		t.Fatal(err)
	}
	return a + b
}

func TestSwapsLoseNoEventOrNotificationLive(t *testing.T) {
	pg := livePostgres(t)
	withMonitoringStore(t, pg, nil)
	monitoringClock = time.Now
	probeMonitoringPostgres = CheckPostgresStore
	setTiming(func(*storeTimes) { storeTiming.drain = 2 * time.Second })
	prevRecipients := notificationRecipients
	notificationRecipients = func(string) []string { return []string{"admin"} }
	prevPending := pendingEvery
	pendingEvery = 100 * time.Millisecond
	t.Cleanup(func() { notificationRecipients, pendingEvery = prevRecipients, prevPending })
	if err := InitMetricsDatabase(); err != nil || MonitoringDialect() != DialectPostgres {
		t.Fatalf("PostgreSQL first: %v %s", err, MonitoringDialect())
	}
	run := fmt.Sprintf("swap-%d", time.Now().UnixNano())
	var events, notes atomic.Int64
	errorsBy := sync.Map{}
	fail := func(kind string, err error) {
		if err == nil {
			return
		}
		key := kind + ": " + err.Error()
		if len(key) > 120 {
			key = key[:120]
		}
		n, _ := errorsBy.LoadOrStore(key, new(atomic.Int64))
		n.(*atomic.Int64).Add(1)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			for j := 0; ; j++ {
				select {
				case <-stop:
					return
				default:
				}
				err := InsertEvent(Event{EventId: run, Label: fmt.Sprintf("%d-%d", i, j), Date: time.Now(), Level: "info"})
				fail("event", err)
				events.Add(1)
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			for j := 0; ; j++ {
				select {
				case <-stop:
					return
				default:
				}
				// a thread of its own: not grouped with the others
				WriteNotification(Notification{Recipient: "admin", Level: "info", Fork: &NotificationInfo{Kind: "other",
					Thread: fmt.Sprintf("%s-%d-%d", run, i, j), Title: NText(run), What: NText("x")}})
				notes.Add(1)
				time.Sleep(time.Millisecond)
			}
		}(i)
	}
	local := DatabaseConfig{}
	for swap := 0; swap < 200; swap++ {
		config := GetBaseMainConfig()
		if swap%2 == 0 {
			config.Database = local
		} else {
			config.Database = pg
		}
		LoadBaseMainConfig(config)
		if err := InitMetricsDatabase(); err != nil {
			fail("swap", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
	// the kept rows are written again
	for i := 0; i < 200; i++ {
		pendingMu.Lock()
		running := pendingRunning
		pendingMu.Unlock()
		if !running {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(3 * time.Second) // the drains end
	report := []string{}
	errorsBy.Range(func(k, v interface{}) bool {
		report = append(report, fmt.Sprintf("%s ×%d", k, v.(*atomic.Int64).Load()))
		return true
	})
	foundEvents := countBothStores(t, pg, "SELECT COUNT(*) FROM events WHERE event_id = ?", run)
	foundNotes := countBothStores(t, pg, "SELECT COUNT(*) FROM notifications WHERE title = ?", run)
	t.Logf("events %d written, %d found; notifications %d written, %d found; errors %v", events.Load(), foundEvents, notes.Load(), foundNotes, report)
	if len(report) > 0 || int64(foundEvents) != events.Load() || int64(foundNotes) != notes.Load() {
		t.Errorf("errors %s; events %d of %d, notifications %d of %d", strings.Join(report, "; "), foundEvents, events.Load(), foundNotes, notes.Load())
	}
}
