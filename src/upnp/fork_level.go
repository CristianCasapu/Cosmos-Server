package upnp

// Fork addition (plan 48, reviews 5 and 6): UPnP says the level of its
// problems to the Telegram notifier from its own state, once a pass
// published it. The rule (review 6): "over" only on evidence of this run;
// absence is never evidence.
//
//   - The router: open while the pass is paused for it; over after a pass
//     in which it answered, or in which nothing needed it (the settings).
//   - A forward: open while it is among the refused ones; over once the
//     router accepted it in this run, or when the valid saved settings no
//     longer want it (the owner closed the port, switched UPnP or Cosmos'
//     own port off); unknown otherwise (review 7, N4: an invalid setting,
//     or an app's choice the code dropped, is not the owner's removal): a
//     forward not asked yet in this run (the router did not answer since
//     the start) is not over.
//
// Unknown before the first pass of this run, and while a pass runs (it is
// asked again later). The thread names are compared as the notifications
// store them (utils.ThreadName).

import (
	"sync/atomic"

	"github.com/azukaar/cosmos-server/src/utils"
)

var (
	passed atomic.Bool
	// accepted: the forwards the router accepted in this run since their
	// last refusal; ended: the forwards the settings no longer want (both
	// under runMu)
	accepted = map[string]bool{}
	ended    = map[string]bool{}
)

const maxForwardMarks = 1024

func forkForwardAccepted(key string) {
	if len(accepted) >= maxForwardMarks {
		accepted = map[string]bool{}
	}
	accepted[key] = true
	delete(ended, key)
}

func forkForwardRefused(key string) {
	delete(accepted, key)
	delete(ended, key)
}

func forkForwardEnded(key string) {
	if len(ended) >= maxForwardMarks {
		ended = map[string]bool{}
	}
	ended[key] = true
	delete(accepted, key)
	delete(failed, key)
}

// forkPlanSeen: a refused or accepted forward that the plan (made from the
// saved settings) no longer holds has ended. Review 7 (N4): only a valid
// saved change of the owner is: a plan whose settings are invalid
// (cfgErr) ends nothing, and an app's choice the code drops (its container
// or port gone for forgetAfter) is not the owner's removal: its forward is
// unknown from then on (the chat keeps it until a level or the panel ends
// it). The caller holds runMu.
func forkPlanSeen(p plan) {
	for _, key := range p.forget {
		delete(accepted, key)
		delete(ended, key)
		delete(failed, key)
	}
	if p.cfgErr != nil {
		return
	}
	inPlan := map[string]bool{}
	for _, w := range p.wants {
		inPlan[w.key] = true
	}
	for _, marks := range []map[string]bool{boolKeys(failed), accepted} {
		for key := range marks {
			if !inPlan[key] {
				forkForwardEnded(key)
			}
		}
	}
}

func boolKeys(m map[string]string) map[string]bool {
	out := map[string]bool{}
	for key := range m {
		out[key] = true
	}
	return out
}

// levelState reads the state between passes; ok is false while unknown.
func levelState(read func()) bool {
	if !passed.Load() || !runMu.TryLock() {
		return false
	}
	defer runMu.Unlock()
	read()
	return true
}

// forwardLevel: the level of a forward's thread; known is false while
// unknown. The caller holds runMu.
func forwardLevel(thread string) (open bool, known bool) {
	for key := range failed {
		if utils.ThreadName(threadOf(key)) == thread {
			return true, true
		}
	}
	for _, marks := range []map[string]bool{accepted, ended} {
		for key := range marks {
			if utils.ThreadName(threadOf(key)) == thread {
				return false, true
			}
		}
	}
	return false, false
}

func init() {
	utils.RegisterThreadLevel("upnp.router.unreachable", func(thread string) (open bool, ok bool) {
		ok = levelState(func() { open = thread == routerThread && paused })
		return open, ok
	})
	utils.RegisterOpenThreads("upnp.router.unreachable", func() (list []string, ok bool) {
		ok = levelState(func() {
			if paused {
				list = []string{routerThread}
			}
		})
		return list, ok
	})
	utils.RegisterThreadLevel("upnp.forward.failed", func(thread string) (open bool, ok bool) {
		known := false
		ok = levelState(func() { open, known = forwardLevel(thread) })
		return open, ok && known
	})
	utils.RegisterOpenThreads("upnp.forward.failed", func() (list []string, ok bool) {
		ok = levelState(func() {
			for key := range failed {
				list = append(list, utils.ThreadName(threadOf(key)))
			}
		})
		return list, ok
	})
}
