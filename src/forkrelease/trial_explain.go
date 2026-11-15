package forkrelease

// Fork addition: when a restore that is owed cannot finish, the launcher
// says, at every attempt, what is wrong and how to get out of it, in the
// journal and in cosmos-update.README in the install folder.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// keptDamagedError: a file of the copy kept is missing or not what it was.
type keptDamagedError struct{ path string }

func (e *keptDamagedError) Error() string {
	return "the kept copy of " + SafeText(e.path) + " is missing or does not have its SHA-256"
}

// obstacleError: something in the install folder is in the way.
type obstacleError struct{ path, what string }

func (e *obstacleError) Error() string { return SafeText(e.path) + " " + e.what }

// busyError: an update is refused because an earlier one is being undone.
type busyError struct{ version, earlier string }

func (e *busyError) Error() string {
	return "an earlier update is still being undone (" + SafeText(e.earlier) + "), so " + SafeText(e.version) + " is not installed now; it is tried again at the next update"
}

func readmePath(folder string) string { return filepath.Join(folder, readmeName) }

// resolved: nothing needs a person any more.
func resolved(folder string) {
	if _, err := os.Lstat(readmePath(folder)); err == nil {
		os.Remove(readmePath(folder))
	}
}

func purposeWords(trial Trial) string {
	switch trial.Purpose {
	case purposeRollback:
		return "Version " + trial.NewVersion + " did not start properly, and Cosmos is going back to version " + trial.PreviousVersion + "."
	case purposeUnrecorded:
		return "The trial of version " + trial.NewVersion + " could not be recorded, and Cosmos is going back to version " + trial.PreviousVersion + "."
	case purposeUndo:
		return "Version " + trial.NewVersion + " could not be unpacked (" + trial.Cause + "), and Cosmos is putting back version " + trial.PreviousVersion + "."
	case purposeInterrupted:
		return "The installation of version " + trial.NewVersion + " was cut short, and Cosmos is putting back version " + trial.PreviousVersion + "."
	}
	return "The state of an update could not be read."
}

// explain prints what is wrong and the ways out, and writes them to the
// README (overwritten at every attempt).
func explain(folder string, trial Trial, problem error, env launcherEnv) {
	previous := filepath.Join(folder, previousFolder)
	state := trialPath(folder)
	lines := []string{
		"Cosmos update: a restore of the program files is owed and cannot finish (" + time.Now().UTC().Format("2006-01-02 15:04 UTC") + ").",
		"",
		"What happened: " + purposeWords(trial),
		"What is wrong: " + SafeText(problem.Error()) + ".",
		"Cosmos is NOT started until this is resolved, so that it never runs a mix of two versions. The launcher tries again at every start (about every " + strconv.Itoa(int((RestorePause+unitRestartSec)/time.Second)) + " seconds); once the cause is gone it finishes by itself.",
		"",
		"Ways out, the first that applies:",
	}
	var damaged *keptDamagedError
	var obstacle *obstacleError
	step := 1
	add := func(text string) {
		lines = append(lines, strconv.Itoa(step)+". "+text)
		step++
	}
	switch {
	case errors.As(problem, &obstacle):
		add("Remove or rename " + filepath.Join(folder, obstacle.path) + " (it " + obstacle.what + "). The next start then finishes the restore by itself.")
	case errors.As(problem, &damaged):
		add("The kept copy of " + damaged.path + " is damaged. Take that file from the release " + trial.PreviousVersion + " instead (its zip from the fork's GitHub releases, checked against its .sig file), copy it to " + filepath.Join(previous, damaged.path) + ", then wait for the next start, or follow 2.")
	case trial.State == "":
		add("Look at " + state + " and " + filepath.Join(previous, previousManifest) + ": both could not be used.")
	default:
		add("Fix the cause above (for example a full disk: df -h " + folder + "; a read-only file system: mount | grep \" " + folder + " \"). The next start then finishes the restore by itself.")
	}
	if trial.PreviousVersion != "" {
		add("Put the files of " + trial.PreviousVersion + " back by hand. " + filepath.Join(previous, previousManifest) + " lists them: for each entry under \"replaced\", copy " + previous + "/<path> to " + folder + "/<path> (for example: cp -p " + filepath.Join(previous, "cosmos") + " " + filepath.Join(folder, "cosmos") + ") and set its mode to the entry's \"mode\"; delete each path listed under \"added\". Then delete " + state + ". Cosmos then starts " + trial.PreviousVersion + "; " + trial.NewVersion + " is not noted as failed.")
	}
	add("Or delete " + state + " to start the files as they are now. They may be a mix of " + nonEmpty(trial.NewVersion, "the new version") + " and " + nonEmpty(trial.PreviousVersion, "the version before") + " that does not start or does not work; nothing is noted as failed and nothing is put back any more.")
	text := strings.Join(lines, "\n") + "\n"
	for _, line := range lines {
		if line != "" {
			fmt.Fprintln(env.out, "[ERROR] Update: "+line)
		}
	}
	if err := writeAtomic(readmePath(folder), []byte(text)); err != nil {
		fmt.Fprintln(env.out, "[ERROR] Update: "+readmePath(folder)+" could not be written: "+SafeText(pathless(err).Error()))
	}
}

// attention writes a README for a server that runs but whose update needs a
// person (the bookkeeping of a finished restore could not be written).
func attention(folder string, what string, env launcherEnv) {
	text := "Cosmos update: needs attention (" + time.Now().UTC().Format("2006-01-02 15:04 UTC") + ").\n\n" + what + "\n"
	fmt.Fprintln(env.out, "[ERROR] Update: "+what)
	writeAtomic(readmePath(folder), []byte(text))
}

func nonEmpty(value string, otherwise string) string {
	if value == "" {
		return otherwise
	}
	return value
}
