package watch

import (
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// review 5: the watches say their level from their own state: unknown
// before the state was read or a look made, then open or over.
func TestTheWatchesSayTheirLevel(t *testing.T) {
	lookedMu.Lock()
	saved := looked
	looked = map[string]bool{}
	lookedMu.Unlock()
	defer func() { lookedMu.Lock(); looked = saved; lookedMu.Unlock() }()
	st := newStore("")
	registerLevels(st)
	if _, known := utils.SourceThreadLevel("smart.failing", "smart:serial:S1"); known {
		t.Fatal("known before any look")
	}
	st.with(func(d *stateData) bool {
		d.SMART["serial:S1"] = smartMemo{Open: true}
		d.SMART["serial:S2"] = smartMemo{Unreadable: true}
		d.Fill["/mnt/data"] = &fillRing{Open: true}
		d.Docker.Open = true
		d.SnapLate["/srv"] = true
		return false
	})
	for _, what := range []string{"smart", "fill", "docker", "snapshots"} {
		markLooked(what)
	}
	for _, c := range []struct {
		kind, thread string
		open         bool
	}{
		{"smart.failing", "smart:serial:S1", true}, {"smart.failing", "smart:serial:S2", false},
		{"smart.unreadable", "smart:serial:S2:unreadable", true}, {"smart.failing", "smart:serial:gone", false},
		{"disk.filling", "fill:/mnt/data", true}, {"disk.filling", "fill:/mnt/other", false},
		{"docker.down", "docker.down", true}, {"snapshots.late", "snapshots.late:/srv", true},
		{"snapshots.late", "snapshots.late:/home", false},
	} {
		if open, known := utils.SourceThreadLevel(c.kind, c.thread); !known || open != c.open {
			t.Errorf("%s %s: open %v known %v, want %v", c.kind, c.thread, open, known, c.open)
		}
	}
	opens := utils.SourceOpenThreads()
	if len(opens["smart.failing"]) != 1 || len(opens["disk.filling"]) != 1 || len(opens["docker.down"]) != 1 || len(opens["snapshots.late"]) != 1 || len(opens["smart.unreadable"]) != 1 {
		t.Errorf("open lists %v", opens)
	}
}
