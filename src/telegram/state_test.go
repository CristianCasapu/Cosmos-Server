package telegram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWhatAStartSays(t *testing.T) {
	now := time.Date(2026, 11, 13, 3, 0, 0, 0, time.Local)
	long := now.Add(-26 * time.Hour)
	st := testSettings()

	cases := []struct {
		name     string
		previous *runState
		version  string
		says     string // empty: nothing is said
		key      string
		priority priority
	}{
		{"the first start there is a record of", nil, "0.24.0-fork.8", "", "", 0},
		{"after an update", &runState{Version: "0.24.0-fork.8", Exit: exitUpdate, UpdateTo: "0.24.0-fork.9", Starts: []time.Time{long}}, "0.24.0-fork.9",
			"🔵 Info · Server updated to 0.24.0-fork.9\n\nCosmos was updated from 0.24.0-fork.8 to 0.24.0-fork.9 and is running again.", "update.installed", priorityLow},
		{"after an update that was not installed", &runState{Version: "0.24.0-fork.8", Exit: exitUpdate, UpdateTo: "0.24.0-fork.9", Starts: []time.Time{long}}, "0.24.0-fork.8",
			"🟠 Error · Update to 0.24.0-fork.9 failed: still on 0.24.0-fork.8\n\nThe server update was not installed: Cosmos started again with version 0.24.0-fork.8.", "update.failed.start", priorityNormal},
		{"another version without an update of its own", &runState{Version: "0.24.0-fork.7", Running: true, Starts: []time.Time{long}}, "0.24.0-fork.8",
			"Cosmos was updated from 0.24.0-fork.7 to 0.24.0-fork.8", "update.installed", priorityLow},
		{"after a restart from the panel", &runState{Version: "v", Exit: exitRestart, Starts: []time.Time{long}}, "v",
			"🔵 Info · Cosmos restarted after a change of settings\n", "cosmos.restarted", priorityLow},
		{"after a stop by the system", &runState{Version: "v", Exit: exitSignal, Starts: []time.Time{long}}, "v",
			"🟡 Warning · Cosmos back after a stop or reboot\n", "cosmos.signal", priorityNormal},
		{"after a crash", &runState{Version: "v", Running: true, Starts: []time.Time{long}}, "v",
			"🟠 Error · Cosmos back after a crash or power loss\n\nCosmos started again after an unexpected stop: a crash, a kill or a power loss. Running version v.", "cosmos.crashed", priorityNormal},
		{"the version cannot be read", &runState{Version: "v", Running: true, Starts: []time.Time{long}}, "",
			"🟠 Error · Cosmos back after a crash or power loss\n", "cosmos.crashed", priorityNormal},
	}
	for _, tc := range cases {
		nt, next := startNotice(tc.previous, tc.version, now)
		if !next.Running || next.Exit != "" || next.Version != tc.version || len(next.Starts) == 0 || !next.Starts[len(next.Starts)-1].Equal(now) {
			t.Errorf("%s: the state for the next start is %+v", tc.name, next)
		}
		if tc.says == "" {
			if nt != nil {
				t.Errorf("%s: something is said: %s", tc.name, nt.kind)
			}
			continue
		}
		if nt == nil {
			t.Errorf("%s: nothing is said", tc.name)
			continue
		}
		text, _ := composeNotice(st, *nt, true)
		if !strings.Contains(text, tc.says) || nt.kind != tc.key || nt.priority() != tc.priority || nt.category != CategorySystem {
			t.Errorf("%s: kind %s priority %d, message:\n%s\nwant it to say %q with kind %s priority %d", tc.name, nt.kind, nt.priority(), text, tc.says, tc.key, tc.priority)
		}
	}
}

// A server that keeps restarting must not send a message per start: the
// budgets of the notifier are gone with every start, so this is counted in
// the state file.
func TestARestartLoopGivesOneMessage(t *testing.T) {
	now := time.Date(2026, 11, 13, 3, 0, 0, 0, time.Local)
	var state *runState
	said := []string{}

	// a crash every 20 seconds for two hours
	for i := 0; i < 360; i++ {
		nt, next := startNotice(state, "v", now)
		if nt != nil {
			said = append(said, nt.kind)
		}
		if len(next.Starts) > startsKept {
			t.Fatalf("the state keeps %d starts", len(next.Starts))
		}
		state = &next
		now = now.Add(20 * time.Second)
	}
	// two ordinary "started again", then "keeps restarting" once an hour
	want := []string{"cosmos.crashed", "cosmos.crashed", "cosmos.restarting", "cosmos.restarting"}
	if strings.Join(said, " ") != strings.Join(want, " ") {
		t.Errorf("360 starts in two hours said %q, want %q", said, want)
	}

	// restarts that were asked for are no loop, however quickly they follow each other
	var asked *runState
	at := now
	for i := 0; i < 8; i++ {
		nt, next := startNotice(asked, "v", at)
		if nt != nil && nt.kind != "cosmos.restarted" {
			t.Fatalf("restart %d asked for from the panel said %q", i, nt.kind)
		}
		next.Running, next.Exit = false, exitRestart
		asked = &next
		at = at.Add(30 * time.Second)
	}

	// once it is calm again, a start is an ordinary start
	now = now.Add(2 * time.Hour)
	if nt, _ := startNotice(state, "v", now); nt == nil || nt.kind != "cosmos.crashed" {
		t.Errorf("after two calm hours: %+v", nt)
	}

	// times from the future (a clock that was wrong) do not count
	odd := &runState{Version: "v", Running: true, Starts: []time.Time{now.Add(time.Hour), now.Add(2 * time.Hour), now.Add(3 * time.Hour), now.Add(4 * time.Hour)}}
	if nt, _ := startNotice(odd, "v", now); nt == nil || nt.kind != "cosmos.crashed" {
		t.Errorf("with start times in the future: %+v", nt)
	}
}

func TestTheStateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), stateFileName)
	now := time.Date(2026, 11, 13, 3, 0, 0, 0, time.UTC)

	if readState(path) != nil {
		t.Fatal("a state was read from no file")
	}
	if nt := recordStart(path, "0.24.0-fork.8", now); nt != nil {
		t.Errorf("the first start says: %s", nt.kind)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("the state file: %v, mode %v", err, info.Mode())
	}
	state := readState(path)
	if state == nil || !state.Running || state.Version != "0.24.0-fork.8" {
		t.Fatalf("state %+v", state)
	}

	// each way of ending is remembered for the next start
	markExitAt(path, exitUpdate, "0.24.0-fork.9")
	if state := readState(path); state.Running || state.Exit != exitUpdate || state.UpdateTo != "0.24.0-fork.9" {
		t.Errorf("after the update was planned: %+v", state)
	}
	if nt := recordStart(path, "0.24.0-fork.9", now.Add(time.Hour)); nt == nil || nt.kind != "update.installed" {
		t.Errorf("the start after the update: %+v", nt)
	}
	markExitAt(path, exitRestart, "")
	if nt := recordStart(path, "0.24.0-fork.9", now.Add(2*time.Hour)); nt == nil || nt.kind != "cosmos.restarted" {
		t.Errorf("the start after a restart: %+v", nt)
	}
	markExitAt(path, exitSignal, "")
	if nt := recordStart(path, "0.24.0-fork.9", now.Add(3*time.Hour)); nt == nil || nt.kind != "cosmos.signal" {
		t.Errorf("the start after a signal: %+v", nt)
	}
	// and no way of ending at all is a crash
	if nt := recordStart(path, "0.24.0-fork.9", now.Add(4*time.Hour)); nt == nil || nt.kind != "cosmos.crashed" {
		t.Errorf("the start after a crash: %+v", nt)
	}

	// a version is announced once, also across restarts
	if !firstAnnouncement(path, "0.24.0-fork.10") {
		t.Error("a new version is not announced")
	}
	if firstAnnouncement(path, "0.24.0-fork.10") {
		t.Error("a version is announced twice")
	}
	recordStart(path, "0.24.0-fork.9", now.Add(30*time.Hour))
	if firstAnnouncement(path, "0.24.0-fork.10") {
		t.Error("a version is announced again after a restart")
	}
	if !firstAnnouncement(path, "0.24.0-fork.11") || firstAnnouncement(path, "") {
		t.Error("the next version is not announced, or an empty one is")
	}

	// nothing in it is a secret, and nothing is left beside it
	raw, _ := os.ReadFile(path)
	mustNotContain(t, "the state file", string(raw), "token")
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("%d files in the folder", len(entries))
	}

	// a file that is not a state, or far too large, is "nothing known"
	os.WriteFile(path, []byte("not json"), 0600)
	if readState(path) != nil {
		t.Error("a state was read from a broken file")
	}
	os.WriteFile(path, []byte(strings.Repeat(" ", maxStateBytes+1)), 0600)
	if readState(path) != nil {
		t.Error("an oversized file was read")
	}
	markExitAt(path, exitRestart, "") // must not fail or write on top of nothing known
}
