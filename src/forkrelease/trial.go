package forkrelease

// Fork addition: an update that does not start goes back by itself.
//
// The launcher installs a release only after LauncherAccepts and upstream's
// MD5 check. Around the unpacking it now does three more things:
//
//   - KeepReplaced, just before unzipping: every file the zip is about to
//     overwrite is copied into cosmos-update.previous/ (0700) with a manifest
//     (versions, the replaced files with their SHA-256, the files and folders
//     the zip adds). Any error: nothing is installed, the old version starts.
//   - BeginTrial, just after: cosmos-update.trial says the new version is on
//     trial (start 1), and a watchdog is started: the launcher itself with
//     WatchdogArg, detached. It ends this folder's cosmos process when the
//     trial is still pending after WatchdogWindow, and stops watching as soon
//     as the trial is proven or another start began. UndoInstall puts the
//     kept files back at once when the unzip fails.
//   - LauncherTrial, at every later start: one start more; after TrialStarts
//     starts without proof the update is rolled back: the kept files back
//     (each checked against its SHA-256), the added files removed, the
//     version noted in cosmos-update.failed (never installed again) and a
//     note cosmos-update.rolledback left for the server to report.
//
// The new server proves itself (src/fork_update_trial.go) and removes the
// trial file with ProveTrial; only then is its launcher put in place
// (cleanUpUpdateFiles waits while TrialPending), so during a trial the
// launcher that runs is always the old one, which has this code.
//
// Going back puts back only files that were installed and running before:
// nothing is downloaded, nothing is unpacked, and the signature check of a
// new install is the same as before. No wall clock is trusted: starts are
// counted, and the watchdog measures its five minutes on the monotonic clock.

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	previousFolder   = "cosmos-update.previous"
	previousManifest = "manifest.json"
	trialName        = "cosmos-update.trial"
	failedName       = "cosmos-update.failed"
	rollbackName     = "cosmos-update.rolledback"

	// WatchdogArg is the one argument the launcher is started with as the
	// watchdog of a trial.
	WatchdogArg = "--fork-update-watchdog"

	failedKept       = 20
	maxKeptFiles     = 20000
	maxKeptManifest  = 8 << 20
	maxTrialFile     = 4096
	maxFailedList    = 4096
	maxRollbackNote  = 4096
	maxVersionLength = 40

	// RollbackStopped and RollbackTimeout are why a version went back;
	// RollbackDamaged says it could not go back.
	RollbackStopped = "stopped"
	RollbackTimeout = "timeout"
	RollbackDamaged = "damaged"
)

// Variables so tests can shorten them.
var (
	// TrialStarts is how many starts a new version gets to prove itself.
	TrialStarts = 3
	// WatchdogWindow is how long one start may take to prove itself.
	WatchdogWindow = 5 * time.Minute
	watchdogPoll   = 2 * time.Second
	watchdogGrace  = 20 * time.Second
	procRoot       = "/proc"
)

var (
	ErrFailedHere   = errors.New("this release went back by itself on this server because it did not start properly; it is not installed again")
	ErrTrialPending = errors.New("the last update is still on trial (cosmos-update.trial is there)")
	errStateEntry   = errors.New("the update would overwrite a file the updater keeps its state in")
)

// Trial is the content of cosmos-update.trial.
type Trial struct {
	NewVersion      string `json:"newVersion"`
	PreviousVersion string `json:"previousVersion"`
	Starts          int    `json:"starts"`
	// Installing: the kept files are complete and the unzip has begun. Still
	// set at a start: the unzip was interrupted.
	Installing bool `json:"installing,omitempty"`
	// TimedOut counts the starts the watchdog ended.
	TimedOut int `json:"timedOut,omitempty"`
}

// RollbackNote is the content of cosmos-update.rolledback.
type RollbackNote struct {
	Failed   string `json:"failed"`   // the version that did not start properly
	Back     string `json:"back"`     // the version the folder went back to
	Reason   string `json:"reason"`   // RollbackStopped, RollbackTimeout or RollbackDamaged
	Starts   int    `json:"starts"`   // starts it was given
	Minutes  int    `json:"minutes"`  // minutes one start was given
	Restored bool   `json:"restored"` // false: the kept files could not be put back
}

type keptFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size"`
}

type keptManifest struct {
	Format          int        `json:"format"`
	PreviousVersion string     `json:"previousVersion"`
	NewVersion      string     `json:"newVersion"`
	Replaced        []keptFile `json:"replaced"`
	Added           []string   `json:"added"`
	AddedDirs       []string   `json:"addedDirs"`
}

// accepted is the release LauncherAccepts let through in this run of the
// launcher: KeepReplaced takes its version from it, not from the files.
var accepted *Manifest

// ---------------------------------------------------------------- the zip

// InstallTargets lists what unzipping the archive into dest writes, relative
// to dest, with exactly the mapping of the launcher's unzip: the zip's root
// folder stripped, start.sh skipped, cosmos-launcher[-arm64] written as
// <name>.updated. files are written, dirs are created. Whatever unzip
// refuses is an error here too.
func InstallTargets(zipPath string, dest string) (files []string, dirs []string, err error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, nil, err
	}
	defer reader.Close()
	return zipTargets(reader.File, dest)
}

func zipTargets(entries []*zip.File, dest string) (files []string, dirs []string, err error) {
	dest, err = filepath.Abs(dest)
	if err != nil {
		return nil, nil, err
	}
	if len(entries) > maxKeptFiles {
		return nil, nil, errors.New("the update holds too many files")
	}
	var rootFolder string
	if len(entries) > 0 {
		rootFolder = strings.Split(entries[0].Name, "/")[0] + "/"
	}
	for _, f := range entries {
		if f.Mode()&os.ModeSymlink != 0 {
			return nil, nil, fmt.Errorf("refusing symlink entry in archive: %s", SafeText(f.Name))
		}
		fpath := filepath.Join(dest, strings.TrimPrefix(f.Name, rootFolder))
		if filepath.IsAbs(f.Name) || !within(dest, fpath) {
			return nil, nil, fmt.Errorf("refusing archive entry outside destination: %s", SafeText(f.Name))
		}
		baseName := filepath.Base(fpath)
		if baseName == "start.sh" {
			continue
		}
		if baseName == "cosmos-launcher" || baseName == "cosmos-launcher-arm64" {
			fpath = filepath.Join(filepath.Dir(fpath), baseName+".updated")
		}
		rel, err := filepath.Rel(dest, fpath)
		if err != nil {
			return nil, nil, err
		}
		if f.FileInfo().IsDir() {
			dirs = append(dirs, rel)
			continue
		}
		if rel == "." {
			return nil, nil, fmt.Errorf("refusing archive entry that is the destination: %s", SafeText(f.Name))
		}
		files = append(files, rel)
	}
	return files, dirs, nil
}

// within is the launcher's insideDir.
func within(dest, fpath string) bool {
	rel, err := filepath.Rel(dest, fpath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// stateEntry: a path the updater keeps its own state in.
func stateEntry(rel string) bool {
	first := strings.Split(rel, string(os.PathSeparator))[0]
	return strings.HasPrefix(first, "cosmos-update.")
}

// localPath: a clean relative path inside the folder.
func localPath(rel string) bool {
	return rel != "" && rel != "." && filepath.IsLocal(rel) && filepath.Clean(rel) == rel && !strings.ContainsRune(rel, '\\')
}

// ------------------------------------------------------------ the backup

// KeepReplaced is the launcher's hook just before it unzips the accepted
// update: the files the zip overwrites are kept. False: nothing may be
// installed; the update files are removed and the reason is left for the
// server to report.
func KeepReplaced(folder string) bool {
	version := ""
	if accepted != nil {
		version = accepted.Version
	}
	return keepReplaced(folder, version, os.Stdout)
}

func keepReplaced(folder string, newVersion string, out io.Writer) bool {
	err := backUp(folder, newVersion)
	if err == nil {
		fmt.Fprintln(out, "The files the update replaces are kept in "+previousFolder+": it goes back by itself if "+newVersion+" does not start properly.")
		return true
	}
	reason := "the files it replaces could not be kept, so it could not go back by itself: " + SafeText(pathless(err).Error())
	fmt.Fprintln(out, "[ERROR] Update NOT installed: "+reason)
	os.Remove(filepath.Join(folder, StagedZip))
	os.Remove(filepath.Join(folder, StagedSignature))
	if !errors.Is(err, ErrTrialPending) {
		writeNew(filepath.Join(folder, rejectedReport), []byte(reason+"\n"))
	}
	return false
}

func backUp(folder string, newVersion string) error {
	if _, ok := Build(newVersion); !ok {
		return errors.New("the version of the update is not known")
	}
	if _, err := os.Lstat(filepath.Join(folder, trialName)); err == nil {
		return ErrTrialPending
	}
	previousVersion, err := installedVersion(folder)
	if err != nil {
		return err
	}
	if _, ok := Build(previousVersion); !ok {
		return errors.New("the installed version is not a release of this fork")
	}
	files, dirs, err := InstallTargets(filepath.Join(folder, StagedZip), folder)
	if err != nil {
		return err
	}

	kept := filepath.Join(folder, previousFolder)
	if err := os.RemoveAll(kept); err != nil { // the last update's: replaced
		return err
	}
	if err := os.Mkdir(kept, 0700); err != nil {
		return err
	}
	manifest := keptManifest{Format: 1, PreviousVersion: previousVersion, NewVersion: newVersion, Replaced: []keptFile{}, Added: []string{}, AddedDirs: []string{}}
	fail := func(err error) error {
		os.RemoveAll(kept)
		return err
	}

	addedDir := map[string]bool{}
	noteDirs := func(rel string) error {
		// every folder on the way must be a real folder, or not exist yet
		parts := strings.Split(rel, string(os.PathSeparator))
		for i := 1; i <= len(parts); i++ {
			dir := filepath.Join(parts[:i]...)
			info, err := os.Lstat(filepath.Join(folder, dir))
			switch {
			case os.IsNotExist(err):
				addedDir[dir] = true
			case err != nil:
				return err
			case !info.IsDir():
				return fmt.Errorf("%s is in the way of the update: it is not a folder", SafeText(dir))
			}
		}
		return nil
	}
	for _, dir := range dirs {
		if dir == "." {
			continue
		}
		if stateEntry(dir) {
			return fail(errStateEntry)
		}
		if err := noteDirs(dir); err != nil {
			return fail(err)
		}
	}
	seen := map[string]bool{}
	for _, rel := range files {
		if seen[rel] {
			continue
		}
		seen[rel] = true
		if stateEntry(rel) {
			return fail(errStateEntry)
		}
		if dir := filepath.Dir(rel); dir != "." {
			if err := noteDirs(dir); err != nil {
				return fail(err)
			}
		}
		target := filepath.Join(folder, rel)
		info, err := os.Lstat(target)
		if os.IsNotExist(err) {
			manifest.Added = append(manifest.Added, rel)
			continue
		}
		if err != nil {
			return fail(err)
		}
		if !info.Mode().IsRegular() {
			return fail(fmt.Errorf("%s is in the way of the update: it is not a plain file", SafeText(rel)))
		}
		if err := os.MkdirAll(filepath.Join(kept, filepath.Dir(rel)), 0700); err != nil {
			return fail(err)
		}
		sum, size, err := copyFile(target, filepath.Join(kept, rel), 0600)
		if err != nil {
			return fail(err)
		}
		manifest.Replaced = append(manifest.Replaced, keptFile{Path: rel, SHA256: sum, Mode: uint32(info.Mode().Perm()), Size: size})
	}
	for dir := range addedDir {
		manifest.AddedDirs = append(manifest.AddedDirs, dir)
	}
	sort.Strings(manifest.AddedDirs)

	raw, err := json.Marshal(manifest)
	if err != nil {
		return fail(err)
	}
	if err := writeAtomic(filepath.Join(kept, previousManifest), raw); err != nil {
		return fail(err)
	}
	syncFolder(kept)
	// from here on a start finds the trial: an unzip cut short is undone
	if err := writeTrial(folder, Trial{NewVersion: newVersion, PreviousVersion: previousVersion, Installing: true}); err != nil {
		return fail(err)
	}
	return nil
}

// copyFile copies a regular file (never through a link) to a new file with
// the given mode, and returns the SHA-256 and size of what was copied.
func copyFile(from string, to string, mode os.FileMode) (string, int64, error) {
	source, err := os.OpenFile(from, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", 0, err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxZip {
		return "", 0, ErrTooLarge
	}
	os.Remove(to)
	target, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, mode)
	if err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(target, hash), io.LimitReader(source, MaxZip+1))
	if err == nil {
		err = target.Chmod(mode)
	}
	if err == nil {
		err = target.Sync()
	}
	if closeErr := target.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(to)
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func syncFolder(path string) {
	if dir, err := os.Open(path); err == nil {
		dir.Sync()
		dir.Close()
	}
}

// writeAtomic replaces a file with a new one (0600), written beside it and
// renamed, never through a link.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	os.Remove(tmp)
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

func readKept(folder string) (keptManifest, error) {
	var manifest keptManifest
	kept := filepath.Join(folder, previousFolder)
	if info, err := os.Lstat(kept); err != nil || !info.IsDir() {
		return manifest, errors.New("there are no kept files")
	}
	raw, err := readRegular(filepath.Join(kept, previousManifest), maxKeptManifest)
	if err != nil {
		return manifest, errors.New("the list of kept files cannot be read")
	}
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.Format != 1 {
		return manifest, errors.New("the list of kept files is not in the expected form")
	}
	if len(manifest.Replaced)+len(manifest.Added)+len(manifest.AddedDirs) > 3*maxKeptFiles {
		return manifest, errors.New("the list of kept files is too long")
	}
	for _, f := range manifest.Replaced {
		if !localPath(f.Path) || stateEntry(f.Path) || !sha256Pattern.MatchString(f.SHA256) || f.Mode > 0777 {
			return manifest, errors.New("the list of kept files is not in the expected form")
		}
	}
	for _, list := range [][]string{manifest.Added, manifest.AddedDirs} {
		for _, rel := range list {
			if !localPath(rel) || stateEntry(rel) {
				return manifest, errors.New("the list of kept files is not in the expected form")
			}
		}
	}
	return manifest, nil
}

// ---------------------------------------------------------------- trial

func trialPath(folder string) string { return filepath.Join(folder, trialName) }

func writeTrial(folder string, trial Trial) error {
	raw, err := json.Marshal(trial)
	if err != nil {
		return err
	}
	return writeAtomic(trialPath(folder), raw)
}

// readTrial: os.ErrNotExist when no trial is pending; another error when the
// file is there but cannot be used.
func readTrial(folder string) (Trial, error) {
	var trial Trial
	raw, err := readRegular(trialPath(folder), maxTrialFile)
	if err != nil {
		if os.IsNotExist(err) {
			return trial, os.ErrNotExist
		}
		return trial, errors.New("the trial file cannot be read")
	}
	if err := json.Unmarshal(raw, &trial); err != nil {
		return trial, errors.New("the trial file is not in the expected form")
	}
	if _, ok := Build(trial.NewVersion); !ok || trial.Starts < 0 || trial.Starts > 1000 || trial.TimedOut < 0 {
		return trial, errors.New("the trial file is not in the expected form")
	}
	return trial, nil
}

// BeginTrial is the launcher's hook after the update was unzipped: the new
// version starts on trial, watched.
func BeginTrial(folder string) {
	beginTrial(folder, os.Stdout, spawnWatchdog)
}

func beginTrial(folder string, out io.Writer, spawn func(string, io.Writer)) {
	trial, err := readTrial(folder)
	if err != nil {
		fmt.Fprintln(out, "[ERROR] Update: the trial of the new version cannot be started: "+SafeText(pathless(err).Error()))
		return
	}
	trial.Installing, trial.Starts = false, 1
	if err := writeTrial(folder, trial); err != nil {
		fmt.Fprintln(out, "[ERROR] Update: the trial of the new version cannot be started: "+SafeText(pathless(err).Error()))
		return
	}
	fmt.Fprintln(out, "Update: "+SafeText(trial.NewVersion)+" starts on trial (start 1 of "+strconv.Itoa(TrialStarts)+").")
	spawn(folder, out)
}

// UndoInstall is the launcher's hook when the unzip failed: the kept files
// go back at once, and the server reports why.
func UndoInstall(folder string, cause error) {
	undoInstall(folder, cause, os.Stdout)
}

func undoInstall(folder string, cause error, out io.Writer) {
	trial, err := readTrial(folder)
	if err != nil || !trial.Installing {
		return
	}
	reason := "it could not be unpacked (" + SafeText(pathless(cause).Error()) + ")"
	if err := restoreKept(folder, trial.NewVersion); err != nil {
		reason += " and the files it replaced could not all be put back: " + SafeText(pathless(err).Error())
		fmt.Fprintln(out, "[ERROR] Update: "+reason)
		// the trial stays "installing": the next start tries again
		writeNew(filepath.Join(folder, rejectedReport), []byte(reason+"\n"))
		return
	}
	os.Remove(trialPath(folder))
	fmt.Fprintln(out, "[ERROR] Update NOT installed: "+reason+"; the files it replaced were put back.")
	writeNew(filepath.Join(folder, rejectedReport), []byte(reason+"; the files it replaced were put back\n"))
}

// LauncherTrial is the launcher's hook at every start, before it looks for
// an update: a version on trial gets one start more, or goes back.
func LauncherTrial(folder string) {
	launcherTrial(folder, os.Stdout, spawnWatchdog)
}

func launcherTrial(folder string, out io.Writer, spawn func(string, io.Writer)) {
	trial, err := readTrial(folder)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		// a trial is pending but cannot be read: back to the proven version
		fmt.Fprintln(out, "[ERROR] Update: "+SafeText(err.Error())+"; going back to the version before the update.")
		manifest, keptErr := readKept(folder)
		if keptErr != nil {
			fmt.Fprintln(out, "[ERROR] Update: "+SafeText(keptErr.Error())+"; nothing can be put back.")
			os.Remove(trialPath(folder))
			return
		}
		trial = Trial{NewVersion: manifest.NewVersion, PreviousVersion: manifest.PreviousVersion, Starts: TrialStarts}
	}

	installed, _ := installedVersion(folder)
	if trial.Installing {
		// the unzip was cut short (power loss): the old files go back; a zip
		// still there is installed again right after
		fmt.Fprintln(out, "Update: the installation of "+SafeText(trial.NewVersion)+" was interrupted; putting back "+SafeText(trial.PreviousVersion)+".")
		if err := restoreKept(folder, trial.NewVersion); err != nil {
			fmt.Fprintln(out, "[ERROR] Update: the files of the version before could not be put back: "+SafeText(pathless(err).Error()))
		}
		os.Remove(trialPath(folder))
		return
	}
	if installed != trial.NewVersion {
		// installed by other means since: not this trial's business any more
		fmt.Fprintln(out, "Update: the trial of "+SafeText(trial.NewVersion)+" is over: "+SafeText(installed)+" is installed.")
		os.Remove(trialPath(folder))
		return
	}
	if trial.Starts >= TrialStarts {
		rollBack(folder, trial, out)
		return
	}
	trial.Starts++
	if err := writeTrial(folder, trial); err != nil {
		fmt.Fprintln(out, "[ERROR] Update: the trial file cannot be written: "+SafeText(pathless(err).Error()))
		return
	}
	fmt.Fprintln(out, "Update: "+SafeText(trial.NewVersion)+" has not proved itself yet: start "+strconv.Itoa(trial.Starts)+" of "+strconv.Itoa(TrialStarts)+".")
	spawn(folder, out)
}

// rollBack puts the version before the update back.
func rollBack(folder string, trial Trial, out io.Writer) {
	note := RollbackNote{Failed: trial.NewVersion, Back: trial.PreviousVersion, Reason: RollbackStopped,
		Starts: trial.Starts, Minutes: int(WatchdogWindow / time.Minute), Restored: true}
	if trial.TimedOut > 0 {
		note.Reason = RollbackTimeout
	}
	fmt.Fprintln(out, "[ERROR] Update: "+SafeText(trial.NewVersion)+" did not start properly in "+strconv.Itoa(trial.Starts)+" starts; going back to "+SafeText(trial.PreviousVersion)+".")
	if err := restoreKept(folder, trial.NewVersion); err != nil {
		fmt.Fprintln(out, "[ERROR] Update: could NOT go back: "+SafeText(pathless(err).Error())+". "+SafeText(trial.NewVersion)+" stays installed.")
		note.Reason, note.Restored = RollbackDamaged, false
	} else {
		addFailed(folder, trial.NewVersion)
		fmt.Fprintln(out, "Update: back on "+SafeText(trial.PreviousVersion)+"; "+SafeText(trial.NewVersion)+" is not installed again.")
	}
	if raw, err := json.Marshal(note); err == nil {
		writeAtomic(filepath.Join(folder, rollbackName), raw)
	}
	os.Remove(trialPath(folder))
}

// restoreKept puts the kept files back, after checking every one of them,
// and removes what the update added. Nothing is changed when a kept file
// does not have its SHA-256.
func restoreKept(folder string, newVersion string) error {
	manifest, err := readKept(folder)
	if err != nil {
		return err
	}
	if manifest.NewVersion != newVersion {
		return errors.New("the kept files are of another update")
	}
	kept := filepath.Join(folder, previousFolder)
	for _, f := range manifest.Replaced {
		sum, err := fileSHA256(filepath.Join(kept, f.Path), MaxZip)
		if err != nil || sum != f.SHA256 {
			return fmt.Errorf("the kept copy of %s does not have its SHA-256", SafeText(f.Path))
		}
	}
	for _, f := range manifest.Replaced {
		target := filepath.Join(folder, f.Path)
		if err := ensureFolders(folder, filepath.Dir(f.Path)); err != nil {
			return err
		}
		tmp := target + ".rollback"
		sum, _, err := copyFile(filepath.Join(kept, f.Path), tmp, os.FileMode(f.Mode))
		if err != nil {
			return err
		}
		if sum != f.SHA256 {
			os.Remove(tmp)
			return fmt.Errorf("the kept copy of %s changed while it was put back", SafeText(f.Path))
		}
		if err := os.Rename(tmp, target); err != nil { // never written into: a running binary stays intact
			os.Remove(tmp)
			return err
		}
	}
	for _, rel := range manifest.Added {
		target := filepath.Join(folder, rel)
		if info, err := os.Lstat(target); err == nil && !info.IsDir() && realFolders(folder, filepath.Dir(rel)) == nil {
			os.Remove(target) // a link is removed itself, never what it points to
		}
	}
	dirs := append([]string{}, manifest.AddedDirs...)
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) }) // deepest first
	for _, rel := range dirs {
		if realFolders(folder, rel) == nil {
			os.Remove(filepath.Join(folder, rel)) // only when empty
		}
	}
	for _, name := range []string{pendingLauncher, "cosmos-launcher-arm64.updated"} {
		os.Remove(filepath.Join(folder, name))
	}
	syncFolder(folder)
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

// ---------------------------------------------------------- the watchdog

func spawnWatchdog(folder string, out io.Writer) {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(out, "[ERROR] Update: the watchdog of the trial cannot be started: "+SafeText(pathless(err).Error()))
		return
	}
	cmd := exec.Command(self, WatchdogArg)
	cmd.Dir = folder
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(out, "[ERROR] Update: the watchdog of the trial cannot be started: "+SafeText(pathless(err).Error()))
		return
	}
	cmd.Process.Release()
}

// RunWatchdog is the launcher's first hook: true when it was started as the
// watchdog of a trial (and has done that work).
func RunWatchdog(args []string) bool {
	if len(args) != 2 || args[1] != WatchdogArg {
		return false
	}
	self, err := os.Executable()
	if err != nil {
		return true
	}
	watchdog(filepath.Dir(self), os.Stdout, endServer)
	return true
}

// watchdog waits for the trial of this start to be proven; when it is not
// within WatchdogWindow, it notes the time-out and ends the server.
func watchdog(folder string, out io.Writer, end func(string, io.Writer)) {
	trial, err := readTrial(folder)
	if err != nil || trial.Installing {
		return
	}
	starts := trial.Starts
	begun := time.Now() // monotonic: a clock set meanwhile changes nothing
	for time.Since(begun) < WatchdogWindow {
		time.Sleep(watchdogPoll)
		now, err := readTrial(folder)
		if err != nil || now.Starts != starts || now.Installing {
			return // proven, or another start watches itself
		}
	}
	trial, err = readTrial(folder)
	if err != nil || trial.Starts != starts {
		return
	}
	trial.TimedOut++
	writeTrial(folder, trial)
	fmt.Fprintln(out, "[ERROR] Update: "+SafeText(trial.NewVersion)+" did not prove itself within "+strconv.Itoa(int(WatchdogWindow/time.Minute))+" minutes; ending it (start "+strconv.Itoa(starts)+" of "+strconv.Itoa(TrialStarts)+").")
	end(folder, out)
}

// serverProcesses are the running cosmos processes of this folder: their
// name is cosmos and their program is this folder's cosmos.
func serverProcesses(folder string) []int {
	resolved, err := filepath.EvalSymlinks(folder)
	if err != nil {
		return nil
	}
	program := filepath.Join(resolved, "cosmos")
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	found := []int{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 || pid == os.Getpid() {
			continue
		}
		comm, err := readRegular(filepath.Join(procRoot, entry.Name(), "comm"), 64) // /proc reports size 0: the read is bounded all the same
		if err != nil || strings.TrimSpace(string(comm)) != "cosmos" {
			continue
		}
		exe, err := os.Readlink(filepath.Join(procRoot, entry.Name(), "exe"))
		if err != nil || exe != program {
			continue
		}
		found = append(found, pid)
	}
	return found
}

// endServer ends this folder's server: asked first, then forced.
func endServer(folder string, out io.Writer) {
	pids := serverProcesses(folder)
	for _, pid := range pids {
		syscall.Kill(pid, syscall.SIGTERM)
	}
	begun := time.Now()
	for len(serverProcesses(folder)) > 0 && time.Since(begun) < watchdogGrace {
		time.Sleep(500 * time.Millisecond)
	}
	for _, pid := range serverProcesses(folder) {
		syscall.Kill(pid, syscall.SIGKILL)
	}
	if len(pids) == 0 {
		fmt.Fprintln(out, "Update: no running server of this folder was found to end.")
	}
}

// ------------------------------------------------------- for the server

// TrialPending: a new version is on trial in this folder. The launcher that
// came with it is not put in place meanwhile. A trial of a version that is
// not the installed one is over and removed.
func TrialPending(folder string) bool {
	trial, err := readTrial(folder)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		return true // the launcher decides at the next start
	}
	if installed, _ := installedVersion(folder); installed != trial.NewVersion && !trial.Installing {
		os.Remove(trialPath(folder))
		return false
	}
	return true
}

// PendingTrial returns the trial of this folder, if one is pending and
// readable.
func PendingTrial(folder string) (Trial, bool) {
	trial, err := readTrial(folder)
	return trial, err == nil && !trial.Installing
}

// ProveTrial ends the trial of version: it started properly.
func ProveTrial(folder string, version string) error {
	trial, err := readTrial(folder)
	if err != nil {
		return err
	}
	if trial.Installing || trial.NewVersion != version {
		return errors.New("the update on trial is another version")
	}
	if err := os.Remove(trialPath(folder)); err != nil {
		return err
	}
	syncFolder(folder)
	return nil
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
	case RollbackStopped, RollbackTimeout, RollbackDamaged:
	default:
		note.Reason = RollbackStopped
	}
	if note.Starts < 1 || note.Starts > 1000 {
		note.Starts = TrialStarts
	}
	if note.Minutes < 1 || note.Minutes > 1000 {
		note.Minutes = int(WatchdogWindow / time.Minute)
	}
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

func addFailed(folder string, version string) {
	if failedIn(folder, version) {
		return
	}
	versions := append(readFailed(folder), version)
	if len(versions) > failedKept {
		versions = versions[len(versions)-failedKept:]
	}
	writeAtomic(filepath.Join(folder, failedName), []byte(strings.Join(versions, "\n")+"\n"))
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
