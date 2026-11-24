package utils

// Fork addition (plan 51, review round 2, M4): events and notifications that
// could not be written (the store stopped answering, before the fallback took
// over, or a switch in between) are kept, bounded, and written again a few
// seconds later, as metrics and the security record already do. An event
// whose insert was reported as failed although the server committed it is
// looked up first, so it is not stored twice.

import (
	"encoding/json"
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
	pendingNotifications []keptNotification
	pendingRunning       bool
	pendingDropped       int64
	// pendingEvery: how often the kept rows are tried again; the tests shorten it
	pendingEvery = 5 * time.Second
)

// forkKeepEvent is the hook of InsertEvent's error: an insert that ran on a
// handle closed by a switch (taken just before it, held past the drain) is
// written at once into the store in use; otherwise the event is kept.
func forkKeepEvent(e Event, err error) error {
	if err == nil {
		return nil
	}
	// a handle a switch closed, or the local file held longer than its busy
	// timeout (two handles of it during a drain): once more at once, on the
	// store in use (neither error leaves the row written)
	if closedHandle(err) || sqliteBusy(err) {
		if err = insertEventAgain(e); err == nil {
			return nil
		}
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

// keptNotification is a notification whose rows could not be written, with
// the reference its rows carry.
type keptNotification struct {
	n   Notification
	ref string
	at  time.Time // its first try
}

// keepNotification keeps a notification whose rows could not be written.
func keepNotification(n Notification, ref string, at time.Time) {
	pendingMu.Lock()
	if len(pendingNotifications) >= maxPendingNotifications {
		pendingNotifications = pendingNotifications[1:]
		pendingDropped++
	}
	pendingNotifications = append(pendingNotifications, keptNotification{n, ref, at})
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
		for _, k := range notifications {
			if notificationStored(k.ref) {
				continue // its commit went through although its answer was lost
			}
			if !writeNotificationAs(k.n, k.ref, k.at) {
				keepNotification(k.n, k.ref, k.at)
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

// notificationStored: rows with this reference are in the store already.
func notificationStored(ref string) bool {
	db, err := MonitoringReadDB()
	if err != nil || ref == "" {
		return false
	}
	var one int
	return db.QueryRow(MonitoringRebindFor(db, "SELECT 1 FROM notifications WHERE fork_ref = ? LIMIT 1"), ref).Scan(&one) == nil
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

// closedHandle: the statement ran on a store's handle that a switch closed
// (database/sql's error is not exported).
func closedHandle(err error) bool {
	return IsClosedHandle(err)
}

// IsClosedHandle reports whether err is database/sql's "database is closed":
// a writer of the monitoring store that took its handle just before a switch
// and ran after the drain closed it; written again on the store in use.
func IsClosedHandle(err error) bool {
	return err != nil && err.Error() == "sql: database is closed"
}

// insertEventAgain is InsertEvent's statement on the store in use now.
func insertEventAgain(e Event) error {
	db, err := MonitoringWriteDB()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(e.Data)
	if err != nil {
		return err
	}
	_, err = db.Exec(MonitoringRebindFor(db,
		"INSERT INTO events(node, event_id, label, application, level, date, data, object, search)"+
			" VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)"),
		MonitoringNode(), e.EventId, e.Label, e.Application, e.Level,
		TimeToMillis(e.Date), string(payload), e.Object,
		e.EventId+" "+string(payload))
	return err
}
