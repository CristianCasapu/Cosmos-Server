package forkrelease

// Fork addition: what the server of a release on trial does with the trial
// (see trial.go). The server never writes the state file: it leaves marks
// and removes the state when it has proved itself.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TrialPending: an update is not finished in this folder. The launcher that
// came with it is not put in place meanwhile. A trial of a version that is
// not the installed one is over and removed.
func TrialPending(folder string) bool {
	trial, err := readTrial(folder)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil || trial.State != stateTrial {
		return true // the launcher decides at the next start
	}
	installed, err := installedVersion(folder)
	if err != nil || installed == trial.NewVersion || restoreOwed(folder, trial) {
		return true
	}
	// the same end as the launcher's (endedByHand): the launcher of the
	// version that was on trial goes too, so that it is not put in place
	return !endTrialByHand(folder, trial)
}

// PendingTrial returns the trial of this folder, if one is running.
func PendingTrial(folder string) (Trial, bool) {
	trial, err := readTrial(folder)
	return trial, err == nil && trial.State == stateTrial
}

// TrialOf names the start on trial in this folder, when the installed
// version is the one on trial.
func TrialOf(folder string) (TrialRun, bool) {
	trial, ok := PendingTrial(folder)
	if !ok || !idPattern.MatchString(trial.ID) {
		return TrialRun{}, false
	}
	if installed, err := installedVersion(folder); err != nil || installed != trial.NewVersion {
		return TrialRun{}, false
	}
	return TrialRun{Version: trial.NewVersion, ID: trial.ID, Run: trial.Run}, true
}

// own: the state is the trial of run, and the watchdog has not ended it.
func own(t *Trial, readErr error, run TrialRun) bool {
	return readErr == nil && t.State == stateTrial && t.NewVersion == run.Version && t.ID == run.ID && t.Run == run.Run && t.KilledRun != run.Run
}

func markOwn(folder string, run TrialRun, kind string) error {
	trial, err := readTrial(folder)
	if !own(&trial, err, run) {
		return nil // not this start's trial any more
	}
	return writeMark(folder, run.ID, run.Run, kind)
}

// MarkAnswered notes that run answered for the first time: the watchdog
// then gives it ProofGrace to prove itself.
func MarkAnswered(folder string, run TrialRun) error { return markOwn(folder, run, markAnswered) }

// MarkCleanStop notes that run is ending because it was asked to (a
// signal, a planned restart): it is not counted as a failed start.
func MarkCleanStop(folder string, run TrialRun) error { return markOwn(folder, run, markClean) }

// MarkDockerUnreachable notes that run is ending because Docker could not
// be reached: not counted when the launcher cannot reach it either.
func MarkDockerUnreachable(folder string, run TrialRun) error {
	return markOwn(folder, run, markDocker)
}

// ProveTrial ends the trial of run: it started properly. It fails when the
// watchdog already ended that run.
func ProveTrial(folder string, run TrialRun) error {
	proven := false
	err := changeTrial(folder, func(t *Trial, readErr error) (bool, bool) {
		proven = own(t, readErr, run)
		return false, proven
	})
	if err != nil {
		return err
	}
	if !proven {
		return errors.New("the update on trial is another version or start, or its time is up")
	}
	clearMarks(folder, "")
	writeAtomic(filepath.Join(folder, provenName), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"))
	return nil
}

// PruneKept removes the copy kept once the version it was kept for proved
// itself at least KeptFor ago and no update is pending. True when removed.
func PruneKept(folder string, now time.Time) bool {
	unlock, err := lockState(folder)
	if err != nil {
		return false
	}
	defer unlock()
	if _, err := os.Lstat(trialPath(folder)); !os.IsNotExist(err) {
		return false // pending or unreadable: the copy may be owed
	}
	raw, err := readRegular(filepath.Join(folder, provenName), 64)
	if err != nil {
		return false
	}
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(string(raw)))
	if err != nil || now.Sub(at) < KeptFor {
		return false
	}
	if os.RemoveAll(filepath.Join(folder, previousFolder)) != nil {
		return false
	}
	os.Remove(filepath.Join(folder, provenName))
	syncFolder(folder)
	return true
}

// AttentionFile is where the launcher explains what needs a person.
func AttentionFile(folder string) string { return readmePath(folder) }

// Attention returns the README the launcher left when an update needs a
// person ("" when there is none), bounded and with its control characters
// removed but its lines kept.
func Attention(folder string) string {
	raw, err := readRegular(filepath.Join(folder, readmeName), maxReadme)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		lines[i] = SafeText(line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// RollbackNoted: the launcher went back at this start (the note is still
// there for TakeRollbackNote).
func RollbackNoted(folder string) bool {
	_, err := os.Lstat(filepath.Join(folder, rollbackName))
	return err == nil
}

// TakeRollbackNote returns what the launcher noted when it went back, and
// removes the note.
func TakeRollbackNote(folder string) (RollbackNote, bool) {
	path := filepath.Join(folder, rollbackName)
	raw, err := readRegular(path, maxRollbackNote)
	if err != nil {
		if !os.IsNotExist(err) {
			os.Remove(path)
		}
		return RollbackNote{}, false
	}
	os.Remove(path)
	var note RollbackNote
	if json.Unmarshal(raw, &note) != nil {
		return RollbackNote{}, false
	}
	note.Failed, note.Back = shortVersion(note.Failed), shortVersion(note.Back)
	switch note.Reason {
	case RollbackStopped, RollbackTimeout, RollbackUnrecorded, RollbackDamaged:
	default:
		note.Reason = RollbackStopped
	}
	if note.Starts < 1 || note.Starts > 1000 {
		note.Starts = TrialStarts
	}
	if note.Minutes < 1 || note.Minutes > 1000 {
		note.Minutes = int(WatchdogWindow / time.Minute)
	}
	note.Cause = SafeText(note.Cause)
	note.File = filepath.Join(folder, failedName) // never taken from the note
	return note, true
}

func shortVersion(version string) string {
	version = SafeText(version)
	if len(version) > maxVersionLength {
		version = version[:maxVersionLength]
	}
	if version == "" {
		version = "unknown"
	}
	return version
}

// ----------------------------------------------------- failed versions

func readFailed(folder string) []string {
	raw, err := readRegular(filepath.Join(folder, failedName), maxFailedList)
	if err != nil {
		return nil
	}
	versions := []string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if _, ok := Build(line); ok && !strings.HasPrefix(line, "v") {
			versions = append(versions, line)
		}
	}
	return versions
}

func failedIn(folder string, version string) bool {
	version = strings.TrimPrefix(version, "v")
	for _, failed := range readFailed(folder) {
		if failed == version {
			return true
		}
	}
	return false
}

func addFailed(folder string, version string) error {
	if failedIn(folder, version) {
		return nil
	}
	versions := append(readFailed(folder), version)
	if len(versions) > failedKept {
		versions = versions[len(versions)-failedKept:]
	}
	return writeAtomic(filepath.Join(folder, failedName), []byte(strings.Join(versions, "\n")+"\n"))
}

// executableFolder is where the running binary is. A variable for the tests.
var executableFolder = func() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(self), nil
}

// FailedHere: the release with this tag or version went back by itself on
// this server. The release list skips it (GetLatestVersion), so it is
// neither announced nor installed.
func FailedHere(tag string) bool {
	folder, err := executableFolder()
	if err != nil {
		return false
	}
	return failedIn(folder, tag)
}
