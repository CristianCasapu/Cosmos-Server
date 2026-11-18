package upnp

// Fork addition (plan 48, review 5): UPnP says the level of its problems to
// the Telegram notifier from its own state, once a pass published it: the
// router open while the pass is paused for it; a forward open while it is
// among the refused ones. Unknown before the first pass of this run, and
// while a pass runs (it is asked again later).

import (
	"strings"
	"sync/atomic"

	"github.com/azukaar/cosmos-server/src/utils"
)

var passed atomic.Bool

// levelState reads the state between passes; ok is false while unknown.
func levelState(read func()) bool {
	if !passed.Load() || !runMu.TryLock() {
		return false
	}
	defer runMu.Unlock()
	read()
	return true
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
		ok = levelState(func() {
			_, open = failed[strings.TrimPrefix(thread, "upnp:")]
		})
		return open, ok
	})
	utils.RegisterOpenThreads("upnp.forward.failed", func() (list []string, ok bool) {
		ok = levelState(func() {
			for key := range failed {
				list = append(list, threadOf(key))
			}
		})
		return list, ok
	})
}
