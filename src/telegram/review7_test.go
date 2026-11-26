package telegram

// Review 7 of plan 48: what it found, each in a test of its own.

import (
	"testing"
	"time"
)

// N6: a reopening the reconcile decides is written to the threads file at
// once, not when the sender next wakes (a restart in between would forget
// it).
func TestR7AReopeningByTheLevelIsWrittenAtOnce(t *testing.T) {
	th := reachThread("reach:connect")
	th.registered = true // the source says it is open again
	m := scripted(t, th)
	m.open(th)
	m.settle(5 * time.Minute)
	m.closeWith(th)
	m.settle(5 * time.Minute)
	m.truth(th, true) // open again, seen only by the level
	m.n.reconcile()
	saved, has := m.n.threads.entries()[threadID(th.name)]
	if !has || !saved.Open {
		t.Errorf("not written: %+v (has %v)", saved, has)
	}
}
