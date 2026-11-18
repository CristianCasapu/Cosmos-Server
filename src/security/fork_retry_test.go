package security

// Fork addition (plan 51, review round 2, M4): a batch retried after a
// failure whose commit had in fact gone through is not stored twice.

import (
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

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
