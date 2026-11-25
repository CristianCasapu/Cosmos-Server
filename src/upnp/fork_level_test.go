package upnp

import (
	"errors"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// reviews 5 and 6: UPnP says its level once a pass published it; a forward
// is over only on evidence of this run.
func TestTheLevelOfTheRouterAndTheForwards(t *testing.T) {
	savedPaused, savedFailed, savedAccepted, savedEnded, savedPassed := paused, failed, accepted, ended, passed.Load()
	defer func() {
		paused, failed, accepted, ended = savedPaused, savedFailed, savedAccepted, savedEnded
		passed.Store(savedPassed)
	}()
	passed.Store(false)
	if _, known := utils.SourceThreadLevel("upnp.router.unreachable", routerThread); known {
		t.Fatal("known before a pass")
	}
	passed.Store(true)
	paused, failed = true, map[string]string{"cosmos:HTTPS": "refused"}
	accepted, ended = map[string]bool{"cosmos:HTTP": true}, map[string]bool{"app:gone:80/tcp": true}
	if open, known := utils.SourceThreadLevel("upnp.router.unreachable", routerThread); !open || !known {
		t.Errorf("router: %v %v", open, known)
	}
	for _, c := range []struct {
		key         string
		open, known bool
	}{{"cosmos:HTTPS", true, true}, {"cosmos:HTTP", false, true}, {"app:gone:80/tcp", false, true}, {"app:never:81/tcp", false, false}} {
		if open, known := utils.SourceThreadLevel("upnp.forward.failed", threadOf(c.key)); open != c.open || known != c.known {
			t.Errorf("%s: open %v known %v", c.key, open, known)
		}
	}
	runMu.Lock()
	if _, known := utils.SourceThreadLevel("upnp.router.unreachable", routerThread); known {
		t.Error("known while a pass runs")
	}
	runMu.Unlock()
}

// review 6, U1: a refused forward is told; Cosmos starts again (or "Check
// now") while the router does not answer: the refusal is not over. It ends
// when the router accepts the forward.
func TestARefusedForwardStaysOpenWhileTheRouterIsAway(t *testing.T) {
	w := newWorld(t)
	w.router.refuse["TCP/2053"] = 606
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.Enabled = true })
	reconcile(true)
	thread := threadOf("cosmos:HTTPS")
	if open, known := utils.SourceThreadLevel("upnp.forward.failed", thread); !open || !known {
		t.Fatalf("refused: open %v known %v", open, known)
	}
	w.routerErr = errors.New("no UPnP router answered")
	reconcile(true)
	if open, known := utils.SourceThreadLevel("upnp.forward.failed", thread); !open || !known {
		t.Errorf("a manual pass without the router: open %v known %v", open, known)
	}
	// after a restart (the memory is empty) without the router: unknown
	runMu.Lock()
	failed, accepted, ended = map[string]string{}, map[string]bool{}, map[string]bool{}
	runMu.Unlock()
	reconcile(true)
	if _, known := utils.SourceThreadLevel("upnp.forward.failed", thread); known {
		t.Error("a start without the router: known")
	}
	// the router answers and takes it
	w.routerErr = nil
	delete(w.router.refuse, "TCP/2053")
	reconcile(true)
	if open, known := utils.SourceThreadLevel("upnp.forward.failed", thread); open || !known {
		t.Errorf("accepted: open %v known %v", open, known)
	}
}

// review 6: a refused forward the settings no longer want has ended; a
// refusal asked again by "Check now" and refused again is not told again.
func TestAForwardNoLongerWantedEnds(t *testing.T) {
	w := newWorld(t)
	w.router.refuse["TCP/2053"] = 606
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.Enabled = true })
	reconcile(true)
	told := len(w.notices)
	reconcile(true) // "Check now": asked again, refused again
	if len(w.notices) != told {
		t.Errorf("told again: %d then %d", told, len(w.notices))
	}
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.Enabled = false })
	reconcile(true)
	thread := threadOf("cosmos:HTTPS")
	if open, known := utils.SourceThreadLevel("upnp.forward.failed", thread); open || !known {
		t.Errorf("no longer wanted: open %v known %v", open, known)
	}
	if !strings.HasPrefix(thread, "upnp:") {
		t.Fatal(thread)
	}
}

// review 7, N4: an invalid saved setting is not the owner's removal: the
// refused forward stays open; a valid change ends it.
func TestAnInvalidSettingEndsNoForward(t *testing.T) {
	w := newWorld(t)
	w.router.refuse["TCP/2053"] = 606
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.Enabled = true })
	reconcile(true)
	thread := threadOf("cosmos:HTTPS")
	if open, known := utils.SourceThreadLevel("upnp.forward.failed", thread); !open || !known {
		t.Fatalf("refused: open %v known %v", open, known)
	}
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.ExternalHTTPSPort = "70000" })
	reconcile(true)
	if open, known := utils.SourceThreadLevel("upnp.forward.failed", thread); !open && known {
		t.Errorf("an invalid setting ended it: open %v known %v", open, known)
	}
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.ExternalHTTPSPort = ""; c.HTTPConfig.UPnP.Enabled = false })
	reconcile(true)
	if open, known := utils.SourceThreadLevel("upnp.forward.failed", thread); open || !known {
		t.Errorf("a valid change: open %v known %v", open, known)
	}
}

// review 7, N4: an app's choice the code drops (its container gone for
// forgetAfter) is not the owner's removal: unknown, not over.
func TestADroppedChoiceIsNotAnEnd(t *testing.T) {
	newWorld(t)
	forkForwardRefused("app:x")
	failed["app:x"] = "refused"
	forkPlanSeen(plan{forget: []string{"app:x"}})
	forkPlanSeen(plan{})
	if open, known := forwardLevel(utils.ThreadName(threadOf("app:x"))); open || known {
		t.Errorf("dropped by the code: open %v known %v", open, known)
	}
}
