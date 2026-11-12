package utils

import (
	"sync"
	"sync/atomic"
)

// ProcessTracker counts long-running jobs (CRON jobs, CI builds) a restart
// has to wait for. WaitForZero blocks every caller until the count is zero.
type ProcessTracker struct {
	mu    sync.Mutex
	cond  *sync.Cond
	count int
}

func NewProcessTracker() *ProcessTracker {
	pt := &ProcessTracker{}
	pt.cond = sync.NewCond(&pt.mu)
	return pt
}

func (pt *ProcessTracker) StartProcess() {
	pt.mu.Lock()
	pt.count++
	pt.mu.Unlock()
}

func (pt *ProcessTracker) EndProcess() {
	pt.mu.Lock()
	if pt.count > 0 {
		pt.count--
	}
	if pt.count == 0 {
		pt.cond.Broadcast()
	}
	pt.mu.Unlock()
}

// Count is the number of jobs currently registered.
func (pt *ProcessTracker) Count() int {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	return pt.count
}

// WaitForZero blocks until no job is registered. Safe for several waiters.
func (pt *ProcessTracker) WaitForZero() {
	pt.mu.Lock()
	for pt.count > 0 {
		pt.cond.Wait()
	}
	pt.mu.Unlock()
}

// InternalProcessTracker is the tracker every restart path waits on.
var InternalProcessTracker = NewProcessTracker()

// restartPlanned is raised while a restart waits for running jobs. Work that
// is dispatched to this node (CI builds) is refused meanwhile so the wait
// can actually end; CRON jobs keep firing and simply delay the restart.
var restartPlanned atomic.Bool

// PlanRestart marks that this node is draining ahead of a restart.
func PlanRestart() {
	if !restartPlanned.Swap(true) {
		Log("Restart planned: no new CI build will start on this node")
	}
}

// ClearPlannedRestart lifts the marker when a restart did not end the process
// (soft restart, failed host restart).
func ClearPlannedRestart() {
	if restartPlanned.Swap(false) {
		Log("Planned restart done: this node accepts CI builds again")
	}
}

// RestartPlanned reports whether a restart is waiting on this node.
func RestartPlanned() bool {
	return restartPlanned.Load()
}
