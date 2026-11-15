package forkrelease

// Fork addition: putting the copy kept back (see trial.go). The restore can
// be repeated as often as it takes: the copy kept is only read, the added
// files go first (that frees room), every replaced file is checked against
// its SHA-256, and the state is cleared only once everything is back.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// restoreNow carries out the restore the state (restoring) owes. True when
// the server may be started.
func restoreNow(folder string, trial Trial, env launcherEnv) bool {
	if !trial.Done {
		manifest, err := readKept(folder)
		if err == nil && manifest.PreviousVersion != trial.PreviousVersion {
			err = errors.New("the files kept are of " + SafeText(manifest.PreviousVersion) + ", not of " + SafeText(trial.PreviousVersion))
		}
		if err == nil {
			err = verifyKept(folder, manifest)
		}
		if err != nil {
			if !trial.Touched && trial.Purpose == purposeRollback && !restoreOwed(folder, trial) { // nothing changed: neither the state nor the mark says so
				// nothing was changed yet: the new version is complete and stays
				fmt.Fprintln(env.out, "[ERROR] Update: could NOT go back: "+SafeText(err.Error())+". "+SafeText(trial.NewVersion)+" stays installed.")
				writeNote(folder, RollbackNote{Failed: trial.NewVersion, Back: trial.PreviousVersion, Reason: RollbackDamaged, Starts: trial.Failures,
					Minutes: int(WatchdogWindow / time.Minute)})
				changeTrial(folder, func(t *Trial, readErr error) (bool, bool) { return false, readErr == nil && t.State == stateRestoring })
				clearMarks(folder, "")
				return true
			}
			explain(folder, trial, err, env)
			return false
		}
		if !trial.Touched {
			trial.Touched = true
			noteState(folder, trial, env)
		}
		// before the first file changes, the restore must be owed on disk:
		// a start after a cut is never let run a half-restored folder
		if err := owedOnDisk(folder); err != nil {
			explain(folder, trial, fmt.Errorf("%v; nothing was changed", err), env)
			return false
		}
		if err := putBack(folder, manifest); err != nil {
			explain(folder, trial, err, env)
			return false
		}
		trial.Done = true
		noteState(folder, trial, env) // a later start only redoes the bookkeeping
		fmt.Fprintln(env.out, "Update: every file of "+SafeText(trial.PreviousVersion)+" is back.")
	}

	// everything is back: from here on the restored server is started
	// whatever else fails, and what is left is told
	switch trial.Purpose {
	case purposeRollback, purposeUnrecorded:
		if trial.Purpose == purposeRollback && trial.NewVersion != trial.PreviousVersion {
			if err := addFailed(folder, trial.NewVersion); err != nil {
				// while the state stays, no update is staged or unpacked: the
				// failed version cannot come back meanwhile
				noteOnce(folder, &trial, env)
				attention(folder, "Cosmos went back to version "+trial.PreviousVersion+" and runs it, but "+trial.NewVersion+" could not be noted as failed in "+
					filepath.Join(folder, failedName)+" ("+SafeText(pathless(err).Error())+"). Until that file can be written, no update is installed; the launcher tries again at every start. Fix the cause (a folder or a read-only file system in the way, a full disk).", env)
				return true
			}
		}
		noteOnce(folder, &trial, env)
		if trial.Purpose == purposeRollback {
			fmt.Fprintln(env.out, "Update: back on "+SafeText(trial.PreviousVersion)+"; "+SafeText(trial.NewVersion)+" is not installed again. To allow it again, remove the line "+SafeText(trial.NewVersion)+" from "+filepath.Join(folder, failedName)+".")
		} else {
			fmt.Fprintln(env.out, "Update: back on "+SafeText(trial.PreviousVersion)+"; "+SafeText(trial.NewVersion)+" is not noted as failed and is installed again at the next update.")
		}
	case purposeUndo, purposeInterrupted:
		reason := "it could not be unpacked (" + SafeText(trial.Cause) + "); the files it replaced were put back"
		if trial.Purpose == purposeInterrupted {
			// a power loss, a kill or a full disk: not tried again at once (a
			// full disk would loop); the server stages it again at its next update
			reason = "its installation was cut short (a power loss, a kill or a full disk); the files it replaced were put back, and it is tried again at the next update"
		}
		if trial.OnTrial != "" && trial.OnTrial != trial.NewVersion && trial.OnTrial != trial.PreviousVersion {
			reason += "; that is version " + SafeText(trial.PreviousVersion) + ", the version proven before. " + SafeText(trial.OnTrial) +
				", which was on trial when this install began, is neither proven nor failed and is offered again"
		}
		dropZip(folder, trial, env)
		writeNew(filepath.Join(folder, rejectedReport), []byte(SafeText(trial.NewVersion)+": "+reason+"\n"))
		fmt.Fprintln(env.out, "[ERROR] Update NOT installed: "+reason+".")
	}
	if err := changeTrial(folder, func(t *Trial, readErr error) (bool, bool) {
		// the state of this update, whatever it could say (it may still say
		// "trial" when it could not be written, or be unreadable)
		return false, readErr != nil || (t.NewVersion == trial.NewVersion && t.PreviousVersion == trial.PreviousVersion)
	}); err != nil {
		attention(folder, "Cosmos went back to version "+trial.PreviousVersion+" and runs it, but "+trialPath(folder)+" could not be removed ("+
			SafeText(pathless(err).Error())+"). Until it can, no update is installed; the launcher tries again at every start. Fix the cause (a read-only file system, a full disk), or remove that file by hand.", env)
		return true
	}
	clearMarks(folder, "")
	resolved(folder)
	return true
}

// noteOnce writes the note for the server once: that it was written is
// kept in the state and, for a state that cannot be written, in a mark.
func noteOnce(folder string, trial *Trial, env launcherEnv) {
	if trial.Noted || isEmptyMark(filepath.Join(folder, notedName)) {
		return
	}
	if writeNote(folder, rollbackNote(*trial)) != nil {
		return
	}
	trial.Noted = true
	writeEmpty(folder, filepath.Join(folder, notedName))
	noteState(folder, *trial, env)
}

func rollbackNote(trial Trial) RollbackNote {
	note := RollbackNote{Failed: trial.NewVersion, Back: trial.PreviousVersion, Reason: RollbackStopped, Starts: trial.Failures,
		Minutes: int(WatchdogWindow / time.Minute), Restored: true}
	switch {
	case trial.Purpose == purposeUnrecorded:
		note.Reason, note.Cause = RollbackUnrecorded, trial.Cause
	case trial.TimedOut > 0:
		note.Reason = RollbackTimeout
	}
	return note
}

// dropZip removes the update file that failed, never another one copied
// there meanwhile: that one is looked at right after the restore.
func dropZip(folder string, trial Trial, env launcherEnv) {
	zip := filepath.Join(folder, StagedZip)
	if _, err := os.Lstat(zip); err != nil {
		os.Remove(filepath.Join(folder, StagedSignature))
		return
	}
	if sum, err := fileSHA256(zip, MaxZip); err == nil && trial.ZipSHA256 != "" && sum != trial.ZipSHA256 {
		fmt.Fprintln(env.out, "Update: another update file was put beside the launcher meanwhile; it is looked at now.")
		return
	}
	os.Remove(zip)
	os.Remove(filepath.Join(folder, StagedSignature))
	syncFolder(folder)
}

func writeNote(folder string, note RollbackNote) error {
	raw, err := json.Marshal(note)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(folder, rollbackName), raw)
}

// verifyKept: every kept file has its SHA-256.
func verifyKept(folder string, manifest keptManifest) error {
	kept := filepath.Join(folder, previousFolder)
	for _, f := range manifest.Replaced {
		sum, err := fileSHA256(filepath.Join(kept, f.Path), MaxZip)
		if err != nil || sum != f.SHA256 {
			return &keptDamagedError{path: f.Path}
		}
	}
	return nil
}

// afterPutBack runs after each replaced file is back. A variable so tests
// can cut a restore short after a number of files.
var afterPutBack = func(path string) error { return nil }

// putBack removes what the update added and puts every replaced file back.
// A file already back is left as it is.
func putBack(folder string, manifest keptManifest) error {
	kept := filepath.Join(folder, previousFolder)
	touched := map[string]bool{".": true}

	// first what was added: it frees room
	for _, rel := range append(append([]string{}, manifest.Added...), pendingLauncher, "cosmos-launcher-arm64.updated") {
		if realFolders(folder, filepath.Dir(rel)) != nil {
			continue // its folder is gone or not a folder: nothing of the update is in it
		}
		target := filepath.Join(folder, rel)
		info, err := os.Lstat(target)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.IsDir() {
			continue
		}
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) { // a link is removed itself
			return err
		}
		touched[filepath.Dir(rel)] = true
	}
	dirs := append([]string{}, manifest.AddedDirs...)
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) }) // deepest first
	for _, rel := range dirs {
		if realFolders(folder, rel) == nil {
			if os.Remove(filepath.Join(folder, rel)) == nil { // only when empty
				touched[filepath.Dir(rel)] = true
			}
		}
	}

	// then the replaced files
	for _, f := range manifest.Replaced {
		target := filepath.Join(folder, f.Path)
		if err := ensureFolders(folder, filepath.Dir(f.Path)); err != nil {
			return err
		}
		touched[filepath.Dir(f.Path)] = true
		if sum, err := fileSHA256(target, MaxZip); err == nil && sum == f.SHA256 {
			if err := os.Chmod(target, os.FileMode(f.Mode)); err != nil {
				return err
			}
			continue // back already (an earlier attempt)
		}
		info, err := os.Lstat(target)
		switch {
		case err == nil && info.IsDir():
			return &obstacleError{path: f.Path, what: "is a folder where a file of the version before goes"}
		case err == nil:
			// nothing runs from this folder until the restore is done: the
			// file of the update goes first, so its room can be used
			if err := os.Remove(target); err != nil {
				return err
			}
		case !os.IsNotExist(err):
			return err
		}
		tmp := target + ".rollback"
		if info, err := os.Lstat(tmp); err == nil && info.IsDir() {
			return &obstacleError{path: f.Path + ".rollback", what: "is a folder where the restore writes a file"}
		}
		sum, _, err := copyFile(filepath.Join(kept, f.Path), tmp, os.FileMode(f.Mode))
		if err != nil {
			os.Remove(tmp)
			return fmt.Errorf("%s: %v", SafeText(f.Path), pathless(err))
		}
		if sum != f.SHA256 {
			os.Remove(tmp)
			return &keptDamagedError{path: f.Path}
		}
		if err := os.Rename(tmp, target); err != nil {
			os.Remove(tmp)
			return err
		}
		if err := afterPutBack(f.Path); err != nil {
			return err
		}
	}
	for dir := range touched {
		syncFolder(filepath.Join(folder, dir))
	}
	return nil
}

// realFolders: every folder of rel inside folder is a real folder, not a link.
func realFolders(folder string, rel string) error {
	if rel == "." || rel == "" {
		return nil
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	for i := 1; i <= len(parts); i++ {
		info, err := os.Lstat(filepath.Join(folder, filepath.Join(parts[:i]...)))
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return &obstacleError{path: filepath.Join(parts[:i]...), what: "is not a folder"}
		}
	}
	return nil
}

// ensureFolders makes the folders of rel inside folder that are missing,
// and refuses a link or a file on the way.
func ensureFolders(folder string, rel string) error {
	if rel == "." || rel == "" {
		return nil
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	for i := 1; i <= len(parts); i++ {
		path := filepath.Join(folder, filepath.Join(parts[:i]...))
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			if err := os.Mkdir(path, 0755); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return &obstacleError{path: filepath.Join(parts[:i]...), what: "is a file or a link where a folder of the version before goes"}
		}
	}
	return nil
}
