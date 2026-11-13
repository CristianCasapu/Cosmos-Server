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

	nt := &notice{category: CategorySystem, key: "cosmos.started", label: "Cosmos started", priority: priorityNormal, at: now}
	running := func(c *composer) { c.fixed("Running version ").value(version, maxValue).fixed(".") }

	// a restart that was asked for (from the panel, by a change of
	// settings, by an update) is not part of a loop, however many there are
	planned := previous.Exit == exitRestart || previous.Exit == exitUpdate
	if recent >= loopStarts && !planned {
		// one message for the whole episode, not one per start
		if now.Sub(previous.LoopTold) < loopQuiet && !previous.LoopTold.After(now) {
			return nil, next
		}
		next.LoopTold = now
		nt.key, nt.label = "cosmos.restarting", "Cosmos keeps restarting"
		nt.title = "Cosmos keeps restarting: it started several times within ten minutes. Further starts are not announced for an hour."
		nt.details = running
		return nt, next
	}

	from, to := previous.Version, previous.UpdateTo
	switch {
	case previous.Exit == exitUpdate && version == previous.Version:
		nt.key, nt.label = "update.failed", "Server update failed"
		nt.title = "The server update was not installed: Cosmos started again with the version it had."
		nt.details = func(c *composer) {
			c.fixed("Running version ").value(version, maxValue)
			if to != "" {
				c.fixed(", the update was to ").value(to, maxValue)
			}
			c.fixed(".")
		}
	case version != previous.Version && previous.Version != "" && version != "":
		nt.key, nt.label = "update.installed", "Server update installed"
		nt.title = "Cosmos was updated and is running again."
		nt.details = func(c *composer) {
			c.fixed("From version ").value(from, maxValue).fixed(" to ").value(version, maxValue).fixed(".")
		}
	case previous.Exit == exitRestart:
		nt.priority = priorityLow
		nt.title = "Cosmos restarted, as asked for in the panel or after a change of settings."
		nt.details = running
	case previous.Exit == exitSignal:
		nt.title = "Cosmos started again after it was stopped by the system (the service was stopped or restarted, or the machine rebooted)."
		nt.details = running
	case previous.Running:
		nt.key, nt.label = "cosmos.crashed", "Cosmos started after an unexpected stop"
		nt.title = "Cosmos started again after an unexpected stop: a crash, a kill or a power loss."
		nt.details = running
	default:
		nt.priority = priorityLow
		nt.title = "Cosmos started."
		nt.details = running
	}
	return nt, next
}
