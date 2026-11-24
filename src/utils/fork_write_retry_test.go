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

// An insert that ran on a handle a switch had closed (taken just before the
// switch, held past the drain) is written at once into the store in use,
// and reported as written.
func TestEventOnAClosedHandleWrittenAtOnce(t *testing.T) {
	withMonitoringStore(t, DatabaseConfig{}, nil)
	InitMetricsDatabase()
	old, _ := MonitoringWriteDB()
	CloseMetricsDatabase() // the switch: the handle the writer holds is closed
	InitMetricsDatabase()
	at := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	_, err := old.Exec("INSERT INTO events(node, event_id, label, application, level, date, data, object, search) VALUES('n', 'late', 'x', '', 'info', 1, '', '', '')")
	if !closedHandle(err) {
		t.Fatalf("not the closed handle's error: %v", err)
	}
	if err := forkKeepEvent(Event{EventId: "late", Label: "x", Date: at}, err); err != nil {
		t.Fatalf("reported as failed: %v", err)
	}
	db, _ := MonitoringWriteDB()
	var n int
	db.QueryRow("SELECT COUNT(*) FROM events WHERE event_id = 'late'").Scan(&n)
	pendingMu.Lock()
	kept := len(pendingEvents)
	pendingMu.Unlock()
	if n != 1 || kept != 0 {
		t.Errorf("stored %d, kept for later %d", n, kept)
	}
}

// Review round 3: a kept notification whose commit went through although
// its answer was lost is not written again (it keeps its reference and is
// looked up first).
func TestAKeptNotificationIsNotWrittenTwice(t *testing.T) {
	withMonitoringStore(t, DatabaseConfig{}, nil)
	prev := pendingEvery
	pendingEvery = 20 * time.Millisecond
	t.Cleanup(func() { pendingEvery = prev })
	InitMetricsDatabase()
	n := Notification{Recipient: "tester", Title: "once", Message: "once", Level: "info"}
	ref := newRef()
	if !writeNotificationAs(n, ref) { // stored...
		t.Fatal("not written")
	}
	keepNotification(n, ref) // ...but its answer was "lost"
	keepNotification(Notification{Recipient: "tester", Title: "kept", Message: "kept", Level: "info"}, newRef())
	for i := 0; i < 100; i++ {
		pendingMu.Lock()
		running := pendingRunning
		pendingMu.Unlock()
		if !running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	db, _ := MonitoringWriteDB()
	var once, kept int
	db.QueryRow("SELECT COUNT(*) FROM notifications WHERE title = 'once'").Scan(&once)
	db.QueryRow("SELECT COUNT(*) FROM notifications WHERE title = 'kept'").Scan(&kept)
	if once != 1 || kept != 1 {
		t.Errorf("once %d, kept %d", once, kept)
	}
}
