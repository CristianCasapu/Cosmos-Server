package reachwatch

import (
	"os"
	"path/filepath"
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

// review 6, M1: a name failing once and being confirmed is no evidence: the
// level is unknown until the round settles with every name confirmed.
func TestANameBeingConfirmedIsNoEvidence(t *testing.T) {
	saved := w
	defer func() { w = saved }()
	w = newWatcher()
	w.names["example.org"] = &NameState{Host: "example.org", State: StateSuspect}
	w.settle(w.lastRound, Plan{})
	if _, known := utils.SourceThreadLevel("reach.down", "reach:connect"); known {
		t.Error("a round with a suspect name concludes")
	}
	w.names["example.org"].State = StateReachable
	w.settle(w.lastRound, Plan{})
	if open, known := utils.SourceThreadLevel("reach.down", "reach:connect"); open || !known {
		t.Errorf("all confirmed: open %v known %v", open, known)
	}
}

// review 6, m22: a state file that cannot be read is no evidence.
func TestAnUnreadableStateFileIsNotRead(t *testing.T) {
	saved, savedPath := w, storePath
	defer func() { w, storePath = saved, savedPath }()
	path := filepath.Join(t.TempDir(), "reach-watch.json")
	storePath = func() string { return path }
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	w = newWatcher()
	w.load()
	if _, known := utils.SourceThreadLevel("reach.down", "reach:connect"); known {
		t.Error("an unreadable file counts as read")
	}
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	w = newWatcher()
	w.load()
	if open, known := utils.SourceThreadLevel("reach.down", "reach:connect"); open || !known {
		t.Errorf("a readable file without an outage: open %v known %v", open, known)
	}
}
