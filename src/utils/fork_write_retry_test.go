package utils

// Fork addition (plan 51, review round 2, M4): tests of fork_write_retry.go.

import (
	"testing"
	"time"
)

// An event and a notification the store did not take are written again once
// it does, and an event already stored is not stored twice.
func TestKeptRowsAreWrittenAgain(t *testing.T) {
	withMonitoringStore(t, DatabaseConfig{}, nil)
	prev := pendingEvery
	pendingEvery = 20 * time.Millisecond
	t.Cleanup(func() { pendingEvery = prev })
	InitMetricsDatabase()
	db, _ := MonitoringWriteDB()
	db.Exec("ALTER TABLE events RENAME TO events_away") // the store "does not take" events
	at := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	if err := InsertEvent(Event{EventId: "kept", Label: "x", Date: at}); err == nil {
		t.Fatal("the insert worked")
	}
	db.Exec("ALTER TABLE events_away RENAME TO events")
	InsertEvent(Event{EventId: "twice", Label: "y", Date: at})
	forkKeepEvent(Event{EventId: "twice", Label: "y", Date: at}, errLost) // "failed" although it was stored
	for i := 0; i < 100; i++ {
		pendingMu.Lock()
		running := pendingRunning
		pendingMu.Unlock()
		if !running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	count := func(id string) (n int) {
		db.QueryRow("SELECT COUNT(*) FROM events WHERE event_id = ?", id).Scan(&n)
		return
	}
	if count("kept") != 1 || count("twice") != 1 {
		t.Errorf("kept %d, twice %d", count("kept"), count("twice"))
	}
}

var errLost = &PostgresProblem{"network", "lost"}
