package fail2ban

import (
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestFail2banDownIsToldOnceAndItsReturn(t *testing.T) {
	downs, backs := 0, 0
	prevDown, prevBack := notifyDown, notifyBack
	notifyDown = func(time.Time) { downs++ }
	notifyBack = func(time.Time, time.Time) { backs++ }
	defer func() { notifyDown, notifyBack = prevDown, prevBack; downLooks, downTold, looked = 0, false, false }()

	now := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	noteAnswer(false, true, now) // one look without an answer: a reload, maybe
	if downs != 0 {
		t.Fatal("told after one look")
	}
	for i := 1; i < 10; i++ {
		noteAnswer(false, true, now.Add(time.Duration(i)*time.Minute))
	}
	if downs != 1 {
		t.Fatalf("told %d times", downs)
	}
	noteAnswer(true, true, now.Add(11*time.Minute))
	noteAnswer(true, true, now.Add(12*time.Minute))
	if backs != 1 {
		t.Fatalf("back told %d times", backs)
	}
	// not set up for Cosmos: nothing to tell
	for i := 0; i < 5; i++ {
		noteAnswer(false, false, now)
	}
	if downs != 1 {
		t.Fatal("told about a Fail2ban that is not set up")
	}
}

// review 4: what is known of Fail2ban is in memory; after a restart the
// first answer ends an outage told before (notifyBack says nothing when
// none is open), and the level is known only once it was looked at.
func TestAfterARestartTheFirstAnswerEndsAnOutageToldBefore(t *testing.T) {
	backs := 0
	prevBack := notifyBack
	notifyBack = func(time.Time, time.Time) { backs++ }
	defer func() { notifyBack = prevBack; downLooks, downTold, looked = 0, false, false }()
	downLooks, downTold, looked = 0, false, false
	if _, ok := utils.SourceThreadLevel("fail2ban.down", downThread); ok {
		t.Fatal("a level before any look")
	}
	noteAnswer(true, true, time.Now())
	if backs != 1 {
		t.Fatalf("the first answer after a start: %d follow-ups", backs)
	}
	if open, ok := utils.SourceThreadLevel("fail2ban.down", downThread); !ok || open {
		t.Errorf("level after an answer: %v %v", open, ok)
	}
	noteAnswer(true, true, time.Now())
	if backs != 1 {
		t.Errorf("a second answer: %d follow-ups", backs)
	}
}
