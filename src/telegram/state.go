package telegram

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// What is remembered from one run of Cosmos to the next, so that a start
// can say what it follows (an update, a restart, a crash) and so that a
// server that keeps restarting gives one message instead of one per start.
// There is no secret in it. It is written when Cosmos starts, when it is
// about to end and when a new version is announced: a few times a day.

const (
	stateFileName = "telegram.state.json"
	exitRestart   = "restart"
	exitUpdate    = "update"
	exitSignal    = "signal"

	loopWindow    = 10 * time.Minute
	loopStarts    = 4 // this many starts within the window is "keeps restarting"
	loopQuiet     = time.Hour
	startsKept    = 10
	maxStateBytes = 64 << 10
)

// runState is the content of the state file.
type runState struct {
	// Version is the version of the run that wrote the file.
	Version string `json:"version"`
	// Running is set at a start and cleared by a way of ending that Cosmos
	// knows about. Still set at the next start: it ended unexpectedly.
	Running bool `json:"running"`
	// Exit says how the run ended: "restart", "update", "signal".
	Exit     string `json:"exit,omitempty"`
	UpdateTo string `json:"updateTo,omitempty"`
	// Starts are the times of the last starts.
	Starts []time.Time `json:"starts,omitempty"`
	// LoopTold is when "keeps restarting" was last said.
	LoopTold time.Time `json:"loopTold,omitempty"`
	// Announced is the newest version that was announced as available.
	Announced string `json:"announced,omitempty"`
}

func statePath() string {
	return filepath.Join(utils.CONFIGFOLDER, stateFileName)
}

// readState reads the state file. A missing or unreadable file is "no
// earlier run known".
func readState(path string) *runState {
	info, err := os.Stat(path)
	if err != nil || info.Size() > maxStateBytes {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var state runState
	if json.Unmarshal(raw, &state) != nil {
		return nil
	}
	if len(state.Starts) > startsKept {
		state.Starts = state.Starts[len(state.Starts)-startsKept:]
	}
	return &state
}

// writeState replaces the state file: written beside it, then renamed.
func writeState(path string, state runState) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// startNotice decides what a start of Cosmos says, from what the previous
// run left behind, and what this run leaves for the next. It says nothing
// on the first start there is a record of.
func startNotice(previous *runState, version string, now time.Time) (*notice, runState) {
	next := runState{Version: version, Running: true}
	if previous == nil {
		next.Starts = []time.Time{now}
		return nil, next
	}
	next.Announced = previous.Announced
	next.LoopTold = previous.LoopTold

	recent := 1
	for _, at := range previous.Starts {
		if now.Sub(at) < 3*loopWindow && !at.After(now) {
			next.Starts = append(next.Starts, at)
		}
		if now.Sub(at) < loopWindow && !at.After(now) {
			recent++
		}
	}
	next.Starts = append(next.Starts, now)
	if len(next.Starts) > startsKept {
		next.Starts = next.Starts[len(next.Starts)-startsKept:]
	}

	vars := map[string]string{"version": version}
	start := func(kind string) *notice {
		nt := newNotice(kind, vars)
		nt.at = now
		return &nt
	}

	// a restart that was asked for (from the panel, by a change of
	// settings, by an update) is not part of a loop, however many there are
	planned := previous.Exit == exitRestart || previous.Exit == exitUpdate
	if recent >= loopStarts && !planned {
		// one message for the whole episode, not one per start
		if now.Sub(previous.LoopTold) < loopQuiet && !previous.LoopTold.After(now) {
			return nil, next
		}
		next.LoopTold = now
		return start("cosmos.restarting"), next
	}

	var nt *notice
	switch {
	case previous.Exit == exitUpdate && version == previous.Version:
		vars["to"] = previous.UpdateTo
		nt = start("update.failed.start")
	case version != previous.Version && previous.Version != "" && version != "":
		vars["from"] = previous.Version
		nt = start("update.installed")
		nt.extra = withDependencyUpdate(nil) // Fork addition: says when it was an automatic dependency update, see fork_deps.go
	case previous.Exit == exitRestart:
		nt = start("cosmos.restarted")
	case previous.Exit == exitSignal:
		nt = start("cosmos.signal")
	case previous.Running:
		nt = start("cosmos.crashed")
	default:
		nt = start("cosmos.started")
	}
	return nt, next
}
