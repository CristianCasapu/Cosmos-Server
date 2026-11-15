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
	manifest, err := readKept(folder)
	if err == nil && manifest.PreviousVersion != trial.PreviousVersion {
		err = errors.New("the files kept are of " + SafeText(manifest.PreviousVersion) + ", not of " + SafeText(trial.PreviousVersion))
	}
	if err == nil {
		err = verifyKept(folder, manifest)
	}
	if err != nil {
		if !trial.Touched && trial.Purpose == purposeRollback {
			// nothing was changed yet: the new version is complete and stays
			fmt.Fprintln(env.out, "[ERROR] Update: could NOT go back: "+SafeText(err.Error())+". "+SafeText(trial.NewVersion)+" stays installed.")
			note := RollbackNote{Failed: trial.NewVersion, Back: trial.PreviousVersion, Reason: RollbackDamaged, Starts: trial.Failures,
				Minutes: int(WatchdogWindow / time.Minute)}
			writeNote(folder, note)
			changeTrial(folder, func(t *Trial, readErr error) (bool, bool) { return false, readErr == nil && t.State == stateRestoring })
			return true
		}
		fmt.Fprintln(env.out, "[ERROR] Update: the files of "+SafeText(trial.PreviousVersion)+" cannot be put back: "+SafeText(err.Error())+".")
		return false
	}
	if !trial.Touched {
		trial.Touched = true
		noteState(folder, trial, env)
	}
	if err := putBack(folder, manifest); err != nil {
		fmt.Fprintln(env.out, "[ERROR] Update: the files of "+SafeText(trial.PreviousVersion)+" could not all be put back yet: "+SafeText(pathless(err).Error())+".")
		return false
	}

	// everything is back: now say so, then clear the state
	switch trial.Purpose {
	case purposeRollback:
		if trial.NewVersion != trial.PreviousVersion {
			if err := addFailed(folder, trial.NewVersion); err != nil {
				fmt.Fprintln(env.out, "[ERROR] Update: "+SafeText(trial.NewVersion)+" could not be noted as failed: "+SafeText(pathless(err).Error())+".")
				return false
			}
		}
		note := RollbackNote{Failed: trial.NewVersion, Back: trial.PreviousVersion, Reason: RollbackStopped, Starts: trial.Failures,
			Minutes: int(WatchdogWindow / time.Minute), Restored: true}
		if trial.TimedOut > 0 {
			note.Reason = RollbackTimeout
		}
		writeNote(folder, note)
		fmt.Fprintln(env.out, "Update: back on "+SafeText(trial.PreviousVersion)+"; "+SafeText(trial.NewVersion)+" is not installed again. To allow it again, remove the line "+SafeText(trial.NewVersion)+" from "+filepath.Join(folder, failedName)+".")
	case purposeUndo:
		reason := "it could not be unpacked (" + SafeText(trial.Cause) + "); the files it replaced were put back"
		os.Remove(filepath.Join(folder, StagedZip))
		os.Remove(filepath.Join(folder, StagedSignature))
		writeNew(filepath.Join(folder, rejectedReport), []byte(reason+"\n"))
		fmt.Fprintln(env.out, "[ERROR] Update NOT installed: "+reason+".")
	case purposeInterrupted:
		// a power loss, a kill or a full disk: not tried again at once (a
		// full disk would loop); the server stages it again at its next update
		reason := "its installation was cut short (a power loss, a kill or a full disk); the files it replaced were put back, and it is tried again at the next update"
		os.Remove(filepath.Join(folder, StagedZip))
		os.Remove(filepath.Join(folder, StagedSignature))
		writeNew(filepath.Join(folder, rejectedReport), []byte(reason+"\n"))
		fmt.Fprintln(env.out, "[ERROR] Update NOT installed: "+reason+".")
	}
	if err := changeTrial(folder, func(t *Trial, readErr error) (bool, bool) {
		return false, readErr == nil && (t.State == stateRestoring || t.State == stateInstalling)
	}); err != nil {
		fmt.Fprintln(env.out, "[ERROR] Update: the state of the update cannot be cleared: "+SafeText(pathless(err).Error())+".")
		return false
	}
	return true
}

func writeNote(folder string, note RollbackNote) {
	if raw, err := json.Marshal(note); err == nil {
		writeAtomic(filepath.Join(folder, rollbackName), raw)
	}
}

// verifyKept: every kept file has its SHA-256.
func verifyKept(folder string, manifest keptManifest) error {
	kept := filepath.Join(folder, previousFolder)
	for _, f := range manifest.Replaced {
		sum, err := fileSHA256(filepath.Join(kept, f.Path), MaxZip)
		if err != nil || sum != f.SHA256 {
			return fmt.Errorf("the kept copy of %s does not have its SHA-256", SafeText(f.Path))
		}
	}
	return nil
}

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
			return fmt.Errorf("%s is a folder where a file of the version before goes", SafeText(f.Path))
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
		sum, _, err := copyFile(filepath.Join(kept, f.Path), tmp, os.FileMode(f.Mode))
		if err != nil {
			os.Remove(tmp)
			return fmt.Errorf("%s: %v", SafeText(f.Path), pathless(err))
		}
		if sum != f.SHA256 {
			os.Remove(tmp)
			return fmt.Errorf("the kept copy of %s changed while it was put back", SafeText(f.Path))
		}
		if err := os.Rename(tmp, target); err != nil {
			os.Remove(tmp)
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
			return fmt.Errorf("%s is not a folder", SafeText(filepath.Join(parts[:i]...)))
		}
	}
	return nil
}

// ensureFolders makes the folders of rel inside folder that are missing,
// and refuses a link on the way.
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
			return fmt.Errorf("%s is not a folder", SafeText(filepath.Join(parts[:i]...)))
		}
	}
	return nil
}
