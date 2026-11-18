package reachwatch

import (
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// review 5: the outage's level comes from the watch's own state.
func TestTheLevelOfAnOutage(t *testing.T) {
	saved := w
	defer func() { w = saved }()
	w = newWatcher()
	if _, known := utils.SourceThreadLevel("reach.down", "reach:connect"); known {
		t.Fatal("known before anything was read or tested")
	}
	w.settled = true
	w.outage = &Outage{Cause: "connect", Thread: threadFor("connect")}
	if open, known := utils.SourceThreadLevel("reach.down", "reach:connect"); !open || !known {
		t.Errorf("open outage: %v %v", open, known)
	}
	if open, known := utils.SourceThreadLevel("reach.down", "reach:dns"); open || !known {
		t.Errorf("another cause: %v %v", open, known)
	}
	if list := utils.SourceOpenThreads()["reach.down"]; len(list) != 1 || list[0] != "reach:connect" {
		t.Errorf("open list %v", list)
	}
	w.outage = nil
	if open, known := utils.SourceThreadLevel("reach.down", "reach:connect"); open || !known {
		t.Errorf("over: %v %v", open, known)
	}
}
