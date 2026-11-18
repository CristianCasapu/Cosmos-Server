package reachwatch

// Plan 48, review 5: the reachability watch says the level of its outage
// to the Telegram notifier from its own state (persisted), not from the
// notifications, which the owner may delete: open while the outage is open
// with that cause; over otherwise, once the state was read or a round
// concluded; unknown before.

import "github.com/azukaar/cosmos-server/src/utils"

func init() {
	utils.RegisterThreadLevel("reach.down", func(thread string) (bool, bool) {
		w.mu.Lock()
		defer w.mu.Unlock()
		if !w.stateRead && !w.settled {
			return false, false
		}
		if w.outage != nil && (thread == w.outage.Thread || thread == threadFor(w.outage.Cause)) {
			return true, true
		}
		return false, true
	})
	utils.RegisterOpenThreads("reach.down", func() ([]string, bool) {
		w.mu.Lock()
		defer w.mu.Unlock()
		if !w.stateRead && !w.settled {
			return nil, false
		}
		if w.outage == nil {
			return nil, true
		}
		thread := w.outage.Thread
		if thread == "" {
			thread = threadFor(w.outage.Cause)
		}
		return []string{thread}, true
	})
}
