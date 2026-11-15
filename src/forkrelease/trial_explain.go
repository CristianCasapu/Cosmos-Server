package forkrelease

// Fork addition: when a restore that is owed cannot finish, the launcher
// says, at every attempt, what is wrong and how to get out of it, in the
// journal and in cosmos-update.README in the install folder. When the list
// of kept files can be read, the commands of the way back by hand are
// written out, file by file, in cosmos-update.restore.sh: an administrator
// reads it and runs it with sh. Every way out printed is literally right for
// the state it is printed in.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const restoreScriptName = "cosmos-update.restore.sh"

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

// resolved: nothing needs a person (any more).
func resolved(folder string) {
	for _, name := range []string{readmeName, restoreScriptName} {
		if _, err := os.Lstat(filepath.Join(folder, name)); err == nil {
			os.Remove(filepath.Join(folder, name))
		}
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

// quote makes a word for sh.
func quote(word string) string {
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
}

// present: a file of the folder is there.
func present(folder string, name string) bool {
	_, err := os.Lstat(filepath.Join(folder, name))
	return err == nil
}

// updateFiles are the update file and its signature, where they are there.
func updateFiles(folder string) []string {
	files := []string{}
	for _, name := range []string{StagedZip, StagedSignature} {
		if present(folder, name) {
			files = append(files, name)
		}
	}
	return files
}

// restoreScript is the way back by hand, file by file: every replaced file
// copied back from the copy kept, checked against its SHA-256 and given its
// mode, every added file and folder removed, the update files removed, the
// failed version noted (after a rollback), and last the state removed.
func restoreScript(folder string, trial Trial, manifest keptManifest) string {
	lines := []string{
		"#!/bin/sh",
		"# Puts back the files of Cosmos " + trial.PreviousVersion + " in " + folder + ", from " + previousFolder + ".",
		"# Written by the launcher on " + time.Now().UTC().Format("2006-01-02 15:04 UTC") + ". Read it, then run: sh " + filepath.Join(folder, restoreScriptName),
		"# It stops at the first error; it can be run again.",
		"set -e",
		"cd " + quote(folder),
	}
	for _, f := range manifest.Replaced {
		if dir := filepath.Dir(f.Path); dir != "." {
			lines = append(lines, "mkdir -p "+quote(dir))
		}
		kept := filepath.Join(previousFolder, f.Path)
		lines = append(lines,
			"cp "+quote(kept)+" "+quote(f.Path+".restore"),
			"echo "+quote(f.SHA256+"  "+f.Path+".restore")+" | sha256sum -c --quiet",
			"chmod "+fmt.Sprintf("%04o", f.Mode&0777)+" "+quote(f.Path+".restore"),
			"mv -f "+quote(f.Path+".restore")+" "+quote(f.Path))
	}
	for _, rel := range append(append([]string{}, manifest.Added...), pendingLauncher, "cosmos-launcher-arm64.updated") {
		lines = append(lines, "rm -f "+quote(rel))
	}
	dirs := append([]string{}, manifest.AddedDirs...)
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, dir := range dirs {
		lines = append(lines, "rmdir "+quote(dir)+" 2>/dev/null || true")
	}
	lines = append(lines, "rm -f "+quote(StagedZip)+" "+quote(StagedSignature))
	if trial.Purpose == purposeRollback && trial.NewVersion != trial.PreviousVersion {
		lines = append(lines, "grep -qx "+quote(trial.NewVersion)+" "+quote(failedName)+" 2>/dev/null || echo "+quote(trial.NewVersion)+" >> "+quote(failedName))
	}
	lines = append(lines,
		"rm -f "+quote(trialName)+" "+markPrefix+"*",
		"rm -f "+quote(readmeName)+" "+quote(restoreScriptName),
		"echo 'The files of "+trial.PreviousVersion+" are back; the next start of the service runs it.'")
	return strings.Join(lines, "\n") + "\n"
}

// explain prints what is wrong and the ways out, and writes them to the
// README (overwritten at every attempt).
func explain(folder string, trial Trial, problem error, env launcherEnv) {
	state := trialPath(folder)
	previous := filepath.Join(folder, previousFolder)
	manifest, keptErr := readKept(folder)
	if keptErr == nil && trial.PreviousVersion != "" && manifest.PreviousVersion != trial.PreviousVersion {
		keptErr = errors.New("the files kept are of another version")
	}
	if trial.PreviousVersion == "" && keptErr == nil {
		trial.PreviousVersion, trial.NewVersion = manifest.PreviousVersion, manifest.NewVersion
	}
	lines := []string{
		"Cosmos update: a restore of the program files is owed and cannot finish (" + time.Now().UTC().Format("2006-01-02 15:04 UTC") + ").",
		"",
		"What happened: " + purposeWords(trial),
		"What is wrong: " + SafeText(problem.Error()) + ".",
		"Cosmos is NOT started until this is resolved, so that it never runs a mix of two versions. The launcher tries again at every start (about every " + strconv.Itoa(int((RestorePause+unitRestartSec)/time.Second)) + " seconds); once the cause is gone it finishes by itself.",
		"",
		"Ways out, the first that applies:",
	}
	step := 1
	add := func(text string) {
		lines = append(lines, strconv.Itoa(step)+". "+text)
		step++
	}
	var damaged *keptDamagedError
	var obstacle *obstacleError
	switch {
	case keptErr != nil:
		// no copy to put back: only starting the files as they are applies
	case errors.As(problem, &obstacle):
		add("Remove or rename " + filepath.Join(folder, obstacle.path) + " (it " + obstacle.what + "). The next start then finishes the restore by itself.")
	case errors.As(problem, &damaged):
		add("The kept copy of " + damaged.path + " is damaged. Take that file from the release " + trial.PreviousVersion + " instead (its zip from the fork's GitHub releases, checked against its .sig file) and copy it to " + filepath.Join(previous, damaged.path) + ". The next start then finishes the restore by itself (or follow 2).")
	default:
		add("Fix the cause above. A full disk: df -h " + folder + ". A read-only file system: findmnt -T " + folder + " (or, without findmnt: df " + folder + ", and look for \"ro\" in mount's line for that file system). The next start then finishes the restore by itself.")
	}
	if keptErr == nil {
		script := restoreScript(folder, trial, manifest)
		what := "copies back the " + strconv.Itoa(len(manifest.Replaced)) + " files of " + trial.PreviousVersion + " (each checked against its SHA-256), removes the " + strconv.Itoa(len(manifest.Added)) + " files the update added and the update files"
		if trial.Purpose == purposeRollback {
			what += ", adds " + trial.NewVersion + " to " + filepath.Join(folder, failedName) + " (so it is not installed again)"
		}
		what += ", then removes the state: the next start runs " + trial.PreviousVersion + "."
		if err := writeAtomic(filepath.Join(folder, restoreScriptName), []byte(script)); err == nil {
			add("Put the files of " + trial.PreviousVersion + " back by hand: read " + filepath.Join(folder, restoreScriptName) + ", then run: sh " + filepath.Join(folder, restoreScriptName) + " . It " + what)
		} else {
			add("Put the files of " + trial.PreviousVersion + " back by hand with these commands (" + restoreScriptName + " could not be written: " + SafeText(pathless(err).Error()) + "): it " + what)
			for _, line := range strings.Split(strings.TrimSpace(script), "\n") {
				if !strings.HasPrefix(line, "#") {
					lines = append(lines, "   "+line)
				}
			}
		}
	}
	asIs := "start the files as they are now: rm -f " + quote(state)
	for _, name := range updateFiles(folder) {
		asIs += " " + quote(filepath.Join(folder, name))
	}
	asIs += " . They may be a mix of " + nonEmpty(trial.NewVersion, "the new version") + " and " + nonEmpty(trial.PreviousVersion, "the version before") + " that does not start or does not work; nothing is put back any more."
	if trial.Purpose == purposeRollback || trial.Purpose == purposeUnrecorded {
		asIs += " To keep " + trial.NewVersion + " from being installed again: echo " + quote(trial.NewVersion) + " >> " + quote(filepath.Join(folder, failedName))
	}
	way := "Or " + asIs
	if keptErr != nil {
		way = "There are no usable kept files (" + SafeText(keptErr.Error()) + "), so nothing can be put back; only this applies: " + asIs
	}
	add(way)
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
