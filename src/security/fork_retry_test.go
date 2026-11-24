package security

// Fork addition (plan 51, review round 2, M4): a batch retried after a
// failure whose commit had in fact gone through is not stored twice.

import (
	"errors"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)


// The store's watcher is held for the test process: the tests change the
// configuration folder under it.
func init() { utils.PauseMonitoringStore() }

func TestRetriedBatchIsNotStoredTwice(t *testing.T) {
	openDatabase(t)
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	batch := func() []Event {
		return []Event{
			{Time: now, Kind: utils.SecLoginFailed, Class: string(ClassIncident), Severity: "low", IP: "192.0.2.7", Count: 1, Details: map[string]string{"user": "a"}},
			{Time: now.Add(time.Millisecond), Kind: utils.SecLoginFailed, Class: string(ClassIncident), Severity: "low", IP: "192.0.2.8", Count: 1},
		}
	}
	first := batch()
	if err := insert(first); err != nil {
		t.Fatal(err)
	}
	// the answer of that commit was "lost": the recorder retries it, with a new event after it
	again := append(batch(), Event{Time: now.Add(2 * time.Millisecond), Kind: utils.SecLoginFailed, Class: string(ClassIncident), Severity: "low", IP: "192.0.2.9", Count: 1})
	if err := insertRetrying(again, 2); err != nil {
		t.Fatal(err)
	}
	if again[0].ID != first[0].ID || again[1].ID != first[1].ID || again[2].ID == 0 {
		t.Errorf("ids %d %d %d, first %d %d", again[0].ID, again[1].ID, again[2].ID, first[0].ID, first[1].ID)
	}
	if n := len(all(t)); n != 3 {
		t.Errorf("%d rows, want 3", n)
	}
}

// A batch whose handle a switch of the store closed under it is stored at
// once on the store in use, each event looked up first.
func TestBatchOnAClosedHandleStoredAtOnce(t *testing.T) {
	prev := insertBatch
	t.Cleanup(func() { insertBatch = prev })
	calls := []int{}
	insertBatch = func(events []Event, retried int) error {
		calls = append(calls, retried)
		if len(calls) == 1 {
			return errors.New("sql: database is closed")
		}
		return nil
	}
	if err := insert([]Event{{Kind: "a"}, {Kind: "b"}}); err != nil || len(calls) != 2 || calls[1] != 2 {
		t.Errorf("err %v, calls %v", err, calls)
	}
	calls = nil
	insertBatch = func(events []Event, retried int) error { calls = append(calls, retried); return errors.New("other") }
	if err := insert([]Event{{Kind: "a"}}); err == nil || len(calls) != 1 {
		t.Errorf("another error is not tried again here: %v %v", err, calls)
	}
}

// Review round 3: the closed-handle retry is on the recorder's path (round
// 2's fix was on insert(), which the recorder does not call): through the
// recorder, a batch whose handle a switch closed is stored at once, not kept.
func TestTheRecorderStoresABatchOnAClosedHandleAtOnce(t *testing.T) {
	folder := openDatabase(t)
	clock := newTestClock()
	recorder := startRecorder(t, folder, clock.now)
	prev := insertBatch
	t.Cleanup(func() { insertBatch = prev })
	calls := []int{}
	insertBatch = func(events []Event, retried int) error {
		calls = append(calls, retried)
		if len(calls) == 1 {
			return errors.New("sql: database is closed")
		}
		return insertRetrying(events, retried)
	}
	clock.advance(time.Second)
	recorder.Record(Event{Kind: utils.SecLoginFailed, IP: "203.0.113.7", Target: "closed"})
	recorder.Flush()
	if n := len(all(t)); n != 1 || len(calls) != 2 || calls[1] != 1 {
		t.Fatalf("%d stored, calls %v", n, calls)
	}
	if len(recorder.unsaved) != 0 {
		t.Errorf("kept for later although stored: %d", len(recorder.unsaved))
	}
}
