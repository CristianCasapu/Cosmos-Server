package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/forkrelease"
)

// The "update installed" message of a release whose meta.json carries a
// dependency summary says so; other start messages and other releases do
// not.
func TestAnInstalledDependencyUpdateSaysSo(t *testing.T) {
	t.Cleanup(func() { SetDependencyUpdate(nil) })
	now := time.Date(2026, 11, 13, 3, 0, 0, 0, time.Local)
	st := testSettings()
	updated := &runState{Version: "0.24.0-fork.30", Exit: exitUpdate, UpdateTo: "0.24.0-fork.31", Starts: []time.Time{now.Add(-26 * time.Hour)}}
	restarted := &runState{Version: "0.24.0-fork.31", Exit: exitRestart, Starts: []time.Time{now.Add(-26 * time.Hour)}}
	say := func(previous *runState) string {
		nt, _ := startNotice(previous, "0.24.0-fork.31", now)
		if nt == nil {
			t.Fatal("nothing is said")
		}
		text, _ := composeNotice(st, *nt, true)
		return text
	}

	meta := `{"version":"0.24.0-fork.31","forkDependencies":{"minor":14,"major":1,"majors":["react 19.0.0"]}}`
	d, err := forkrelease.ParseForkDependencies([]byte(meta))
	if err != nil {
		t.Fatal(err)
	}
	SetDependencyUpdate(d)
	text := say(updated)
	if !strings.Contains(text, "from 0.24.0-fork.30 to 0.24.0-fork.31 and is running again.\nIt was an automatic dependency update: 14 minor, 1 major (react 19.0.0).") {
		t.Errorf("message:\n%s", text)
	}
	if text := say(restarted); strings.Contains(text, "dependency") {
		t.Errorf("a restart says: %s", text)
	}

	SetDependencyUpdate(&forkrelease.ForkDependencies{Major: 5, Majors: []string{"a 1", "b 2", "c 3", "d 4", "e 5"}})
	if text := say(updated); !strings.Contains(text, "It was an automatic dependency update: 5 major (a 1, b 2, c 3 and 2 more).") {
		t.Errorf("message:\n%s", text)
	}
	SetDependencyUpdate(&forkrelease.ForkDependencies{Minor: 3})
	if text := say(updated); !strings.Contains(text, "It was an automatic dependency update: 3 minor.") {
		t.Errorf("message:\n%s", text)
	}

	// a release without the summary
	plain, _ := forkrelease.ParseForkDependencies([]byte(`{"version":"0.24.0-fork.31"}`))
	SetDependencyUpdate(plain)
	if text := say(updated); strings.Contains(text, "dependency") {
		t.Errorf("a plain release says: %s", text)
	}
}
