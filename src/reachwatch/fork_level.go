package reachwatch

// Plan 48, reviews 5 and 6: the reachability watch says the level of its
// outage to the Telegram notifier from its own state (persisted), not from
// the notifications, which the owner may delete: open while the outage is
// open with that cause; over when there is no outage (or one of another
// cause) and nothing is being confirmed, once the state file was read or a
// round settled with every name confirmed; unknown otherwise (review 6: a
// name failing once and being confirmed is no evidence either way). Names
// are compared as the notifications store them.

import "github.com/azukaar/cosmos-server/src/utils"

// outageThread is the thread of the open outage; the caller holds mu.
func (w *watcher) outageThread() string {
	thread := w.outage.Thread
	if thread == "" {
		thread = threadFor(w.outage.Cause)
	}
	return thread
}

func init() {
	utils.RegisterThreadLevel("reach.down", func(thread string) (bool, bool) {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.outage != nil && (thread == utils.ThreadName(w.outageThread()) || thread == utils.ThreadName(threadFor(w.outage.Cause))) {
			return true, true
		}
		if (!w.stateRead && !w.settled) || w.suspect {
			return false, false
		}
		return false, true
	})
	utils.RegisterOpenThreads("reach.down", func() ([]string, bool) {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.outage != nil {
			return []string{utils.ThreadName(w.outageThread())}, true
		}
		if !w.stateRead && !w.settled {
			return nil, false
		}
		return nil, true
	})
}
