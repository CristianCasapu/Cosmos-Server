package utils

import (
	"testing"
	"time"
)

func TestProcessTrackerWakesEveryWaiter(t *testing.T) {
	pt := NewProcessTracker()
	pt.StartProcess()
	pt.StartProcess()
	woken := make(chan struct{}, 3)
	for i := 0; i < 3; i++ {
		go func() { pt.WaitForZero(); woken <- struct{}{} }()
	}
	time.Sleep(50 * time.Millisecond)
	select {
	case <-woken:
		t.Fatalf("waiters must block while jobs run")
	default:
	}
	pt.EndProcess()
	pt.EndProcess()
	for i := 0; i < 3; i++ {
		select {
		case <-woken:
		case <-time.After(2 * time.Second):
			t.Fatalf("waiter %d never woke up", i)
		}
	}
	if pt.Count() != 0 {
		t.Fatalf("count: %d", pt.Count())
	}
	pt.EndProcess() // never goes negative
	if pt.Count() != 0 {
		t.Fatalf("count after extra end: %d", pt.Count())
	}
	pt.WaitForZero() // returns immediately at zero
}

func TestRestartPlannedFlag(t *testing.T) {
	ClearPlannedRestart()
	if RestartPlanned() {
		t.Fatalf("flag must start clear")
	}
	PlanRestart()
	PlanRestart()
	if !RestartPlanned() {
		t.Fatalf("flag must be set")
	}
	ClearPlannedRestart()
	if RestartPlanned() {
		t.Fatalf("flag must clear")
	}
}
