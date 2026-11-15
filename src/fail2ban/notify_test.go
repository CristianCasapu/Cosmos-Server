package fail2ban

import (
	"testing"
	"time"
)

func TestFail2banDownIsToldOnceAndItsReturn(t *testing.T) {
	downs, backs := 0, 0
	prevDown, prevBack := notifyDown, notifyBack
	notifyDown = func(time.Time) { downs++ }
	notifyBack = func(time.Time, time.Time) { backs++ }
	defer func() { notifyDown, notifyBack = prevDown, prevBack; downLooks, downTold = 0, false }()

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
