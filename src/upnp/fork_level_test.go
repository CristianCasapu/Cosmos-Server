package upnp

import (
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// review 5: UPnP says its level once a pass published it.
func TestTheLevelOfTheRouterAndTheForwards(t *testing.T) {
	savedPaused, savedFailed, savedPassed := paused, failed, passed.Load()
	defer func() { paused, failed = savedPaused, savedFailed; passed.Store(savedPassed) }()
	passed.Store(false)
	if _, known := utils.SourceThreadLevel("upnp.router.unreachable", routerThread); known {
		t.Fatal("known before a pass")
	}
	passed.Store(true)
	paused, failed = true, map[string]string{"cosmos:HTTPS": "refused"}
	if open, known := utils.SourceThreadLevel("upnp.router.unreachable", routerThread); !open || !known {
		t.Errorf("router: %v %v", open, known)
	}
	if open, known := utils.SourceThreadLevel("upnp.forward.failed", threadOf("cosmos:HTTPS")); !open || !known {
		t.Errorf("forward: %v %v", open, known)
	}
	if open, known := utils.SourceThreadLevel("upnp.forward.failed", threadOf("cosmos:HTTP")); open || !known {
		t.Errorf("another forward: %v %v", open, known)
	}
	runMu.Lock()
	if _, known := utils.SourceThreadLevel("upnp.router.unreachable", routerThread); known {
		t.Error("known while a pass runs")
	}
	runMu.Unlock()
}
