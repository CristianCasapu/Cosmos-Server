package utils

// Fork addition (plan 51, review round 2, M4): events and notifications that
// could not be written (the store stopped answering, before the fallback took
// over, or a switch in between) are kept, bounded, and written again a few
// seconds later, as metrics and the security record already do. An event
// whose insert was reported as failed although the server committed it is
// looked up first, so it is not stored twice.

import (
	"strconv"
	"sync"
	"time"
)

const (
	maxPendingEvents        = 5000
	maxPendingNotifications = 500
)

var (
	pendingMu            sync.Mutex
	pendingEvents        []Event
	pendingNotifications []Notification
	pendingRunning       bool
	pendingDropped       int64
	// pendingEvery: how often the kept rows are tried again; the tests shorten it
	pendingEvery = 5 * time.Second
)

// forkKeepEvent is the hook of InsertEvent's error: the event is kept.
func forkKeepEvent(e Event, err error) error {
	if err == nil {
		return nil
	}
	pendingMu.Lock()
	if len(pendingEvents) >= maxPendingEvents {
		pendingEvents = pendingEvents[1:]
		pendingDropped++
	}
	pendingEvents = append(pendingEvents, e)
	startPendingLocked()
	pendingMu.Unlock()
	return err
}

// keepNotification keeps a notification whose rows could not be written.
func keepNotification(n Notification) {
	pendingMu.Lock()
	if len(pendingNotifications) >= maxPendingNotifications {
		pendingNotifications = pendingNotifications[1:]
		pendingDropped++
	}
	pendingNotifications = append(pendingNotifications, n)
	startPendingLocked()
	pendingMu.Unlock()
}

func startPendingLocked() {
	if pendingRunning {
		return
	}
	pendingRunning = true
	go writePending()
}

// writePending tries the kept rows again until none is left; one goroutine.
func writePending() {
	for {
		time.Sleep(pendingEvery)
		pendingMu.Lock()
		events, notifications := pendingEvents, pendingNotifications
		pendingEvents, pendingNotifications = nil, nil
		pendingMu.Unlock()
		for _, e := range events {
			if eventStored(e) {
				continue
			}
			InsertEvent(e) // kept again by its hook on failure
		}
		for _, n := range notifications {
			if !forkWriteNotification(n) {
				keepNotification(n)
			}
		}
		pendingMu.Lock()
		if len(pendingEvents) == 0 && len(pendingNotifications) == 0 {
			pendingRunning = false
			if pendingDropped > 0 {
				Warn("Monitoring database: " + itoa64(pendingDropped) + " events or notifications could not be kept while the store did not answer")
				pendingDropped = 0
			}
			pendingMu.Unlock()
			return
		}
		pendingMu.Unlock()
	}
}

// eventStored: an event equal to e is in the store already.
func eventStored(e Event) bool {
	db, err := MonitoringReadDB()
	if err != nil {
		return false
	}
	var one int
	return db.QueryRow(MonitoringRebindFor(db, "SELECT 1 FROM events WHERE node = ? AND event_id = ? AND date = ? AND label = ? LIMIT 1"),
		MonitoringNode(), e.EventId, TimeToMillis(e.Date), e.Label).Scan(&one) == nil
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
