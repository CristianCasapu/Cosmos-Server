package forkrelease

// Fork addition: an update that does not start goes back by itself.
//
// The launcher installs a release only after LauncherAccepts and upstream's
// MD5 check, both unchanged. Around the unpacking it keeps a way back, and
// the state of that way back is one small file, cosmos-update.trial, written
// only by the launcher (and its watchdog), under an exclusive lock
// (cosmos-update.lock):
//
//	installing  the copy kept is complete and the unzip is under way
//	trial       the new version runs and has not proved itself yet
//	restoring   a restore is owed (a rollback, a failed or cut-short unzip)
//
// The new server never writes that file: it leaves empty mark files
// (cosmos-update.mark.<trial id>.<run>.<kind>: its first answer, a clean
// stop, a Docker it could not reach) and removes the state when it has
// proved itself. A server of a later release is proven by the launcher of
// the earlier one, so the state file and the marks are a contract between
// versions (see readTrial).
//
// The launcher's hooks (src/launcher/launcher.go):
//
//   - KeepReplaced, just before unzipping: checks the free space, copies every
//     file the zip will overwrite into cosmos-update.previous/ (0700) with a
//     manifest, and sets "installing" (that write is required). Short of room
//     or any error: nothing is touched, the update is refused in words
//     (cosmos-update.rejected, which the server reports).
//   - UndoInstall, when the unzip fails: the restore runs at once.
//   - BeginTrial, after the unzip: every file written is synced, the state
//     becomes "trial" (start 1, a trial id), a watchdog is started.
//   - LauncherTrial, at every start: finishes an owed restore first. Until it
//     completes the launcher prints what is wrong and the ways out (also in
//     cosmos-update.README), and ends with an error after a pause, so
//     start.sh never runs a half-restored folder. In "trial" it counts the
//     last start as failed unless it ended by a clean stop (or by a Docker
//     that the launcher cannot reach either), rolls back after TrialStarts
//     failed starts, or starts the next one, watched. When the count cannot
//     be recorded it goes back rather than run the new version unwatched.
//   - RunWatchdog: the launcher started with WatchdogArg. It ends this
//     folder's cosmos process when the start (this trial id and run) has not
//     answered within WatchdogWindow, or not proved itself ProofGrace after
//     its first answer.
//
// A restore is idempotent: the copy kept is never changed by it. It removes
// the added files first (that frees room), then puts every replaced file
// back after checking it against its SHA-256, syncs, and only then records
// the failed version, writes the note for the server and clears the state.
// Only the program files go back: data and settings a newer version changed
// stay as they are. No wall clock decides anything in a trial: starts are
// counted, and the watchdog measures on the monotonic clock.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	previousFolder   = "cosmos-update.previous"
	previousManifest = "manifest.json"
	trialName        = "cosmos-update.trial"
	lockName         = "cosmos-update.lock"
	failedName       = "cosmos-update.failed"
	rollbackName     = "cosmos-update.rolledback"
	provenName       = "cosmos-update.proven"
	readmeName       = "cosmos-update.README"
	markPrefix       = "cosmos-update.mark."

	// WatchdogArg is the one argument the launcher is started with as the
	// watchdog of a trial.
	WatchdogArg = "--fork-update-watchdog"

	// trialFormat is the format of the state file this code writes. See
	// readTrial for the rule that keeps releases able to read each other.
	trialFormat = 1

	stateInstalling = "installing"
	stateTrial      = "trial"
	stateRestoring  = "restoring"

	purposeRollback    = "rollback"    // the new version did not prove itself
	purposeUnrecorded  = "unrecorded"  // its trial could not be recorded: back, not failed
	purposeUndo        = "undo"        // the unzip failed
	purposeInterrupted = "interrupted" // the unzip was cut short (power loss, kill)

	markAnswered = "answered"
	markClean    = "clean"
	markDocker   = "docker"
	markRestore  = "restore"            // run 0: a restore is owed although the state could not say so
	notedName    = markPrefix + "noted" // the note of a rollback was written (one name: also without a trial id)

	failedKept       = 20
	maxKeptFiles     = 20000
	maxKeptManifest  = 8 << 20
	maxTrialFile     = 4096
	maxFailedList    = 4096
	maxRollbackNote  = 4096
	maxReadme        = 16 << 10
	maxVersionLength = 40

	// RollbackStopped, RollbackTimeout and RollbackUnrecorded are why a
	// version went back; RollbackDamaged says it could not go back.
	RollbackStopped    = "stopped"
	RollbackTimeout    = "timeout"
	RollbackUnrecorded = "unrecorded"
	RollbackDamaged    = "damaged"
)

// The service's unit (src/index.go) restarts Cosmos RestartSec after it
// ends, and systemd gives up for good when it is started StartLimitBurst
// times within StartLimitInterval. A release that crashes at once must
// reach the start that goes back well before that: TrialStarts failed
// starts, each followed by crashPause in the launcher, keep at most three
// starts in any StartLimitInterval (TestTheStartLimitIsNeverReached).
const (
	unitRestartSec         = 2 * time.Second
	unitStartLimitInterval = 10 * time.Second
	unitStartLimitBurst    = 5
)

// Variables so tests can shorten them.
var (
	// TrialStarts is how many failed starts a new version is given.
	TrialStarts = 3
	// WatchdogWindow is how long one start may take to answer for the first
	// time: long enough for a database migration after a reboot; a hang
	// costs at most TrialStarts times this, at night.
	WatchdogWindow = 15 * time.Minute
	// ProofGrace is how long after its first answer a start may take to prove
	// itself (the server checks again after 60 s).
	ProofGrace = 3 * time.Minute
	// KeptFor is how long the copy kept stays after the new version proved
	// itself; the next update replaces it in any case.
	KeptFor = 72 * time.Hour
	// RestorePause: when an owed restore cannot complete, the launcher waits
	// this long and ends with an error, so that systemd's start limit is
	// never reached while it tries again at every start.
	RestorePause = 30 * time.Second
	// DockerExcused is how many starts that ended because Docker could not
	// be reached (by the server and by the launcher) go uncounted.
	DockerExcused = 10

	crashPause    = 3 * time.Second
	watchdogPoll  = 2 * time.Second
	watchdogGrace = 20 * time.Second
	procRoot      = "/proc"
	exitLauncher  = os.Exit
)

var (
	ErrFailedHere   = errors.New("this release went back by itself on this server because it did not start properly; it is not installed again")
	ErrTrialPending = errors.New("the last update is still on trial")
	errStateEntry   = errors.New("the update would overwrite a file the updater keeps its state in")
	errLeftoverZip  = errors.New("the update file is the one already installed and on trial")
	idPattern       = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// trialRefusals start the reasons the trial code refuses an update for.
// None is about a release that could not be verified.
var trialRefusals = []string{"not enough free space", "the files it replaces could not be kept", "it could not be unpacked",
	"its installation was cut short", "an earlier update is still being undone", ErrFailedHere.Error()}

// SecurityRefusal: a refusal noted by the launcher is about the release's
// signature or origin (a security incident), not about room, an unzip, or a
// version that went back here.
func SecurityRefusal(reason string) bool {
	if i := strings.Index(reason, ": "); i > 0 {
		if _, ok := Build(reason[:i]); ok {
			reason = reason[i+2:] // "<version>: <reason>"
		}
	}
	for _, prefix := range trialRefusals {
		if strings.HasPrefix(reason, prefix) {
			return false
		}
	}
	return true
}

// failedError names the way to allow a failed version again.
func failedError(folder string, version string) error {
	version = strings.TrimPrefix(SafeText(version), "v")
	return fmt.Errorf("%w. To allow it again, remove the line %s from %s", ErrFailedHere, version, filepath.Join(folder, failedName))
}

// Trial is the content of cosmos-update.trial.
type Trial struct {
	Format          int    `json:"format"`
	State           string `json:"state"`
	NewVersion      string `json:"newVersion"`
	PreviousVersion string `json:"previousVersion"`
	// ID names this trial; with Run it names one start (marks, watchdog).
	ID            string `json:"id,omitempty"`
	Run           int    `json:"run,omitempty"`
	Failures      int    `json:"failures,omitempty"`      // runs that ended without proof and without a clean stop
	TimedOut      int    `json:"timedOut,omitempty"`      // runs the watchdog ended
	KilledRun     int    `json:"killedRun,omitempty"`     // the run the watchdog ended
	DockerExcused int    `json:"dockerExcused,omitempty"` // runs not counted: Docker unreachable for both
	// ZipSHA256 is the update file being (or that was) installed: a copy of
	// it left behind is dropped, not installed again.
	ZipSHA256 string `json:"zipSHA256,omitempty"`
	// OnTrial is the version that was on trial when this install began (an
	// install by hand during a trial).
	OnTrial string `json:"onTrial,omitempty"`
	// restoring
	Purpose string `json:"purpose,omitempty"`
	Touched bool   `json:"touched,omitempty"` // the restore may have changed files of the folder
	Done    bool   `json:"done,omitempty"`    // every file is back; only the bookkeeping is left
	Noted   bool   `json:"noted,omitempty"`   // the note for the server is written
	Cause   string `json:"cause,omitempty"`   // why the unzip failed, or why the trial could not be recorded
}

// TrialRun names one start on trial, for the server's marks and proof.
type TrialRun struct {
	Version string
	ID      string
	Run     int
}

// RollbackNote is the content of cosmos-update.rolledback.
type RollbackNote struct {
	Failed   string `json:"failed"`   // the version that did not start properly
	Back     string `json:"back"`     // the version the folder went back to
	Reason   string `json:"reason"`   // RollbackStopped, RollbackTimeout, RollbackUnrecorded or RollbackDamaged
	Starts   int    `json:"starts"`   // failed starts it was given
	Minutes  int    `json:"minutes"`  // minutes one start was given to answer
	Restored bool   `json:"restored"` // false: the kept files could not be put back
	Cause    string `json:"cause,omitempty"`
	File     string `json:"-"` // the list of failed versions, to allow one again
}

// ---------------------------------------------------------------- state

func trialPath(folder string) string { return filepath.Join(folder, trialName) }

// lockState takes the exclusive lock every change of the state goes under.
func lockState(folder string) (func(), error) {
	file, err := os.OpenFile(filepath.Join(folder, lockName), os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
	}, nil
}

// removeState removes the state file. A variable so tests can make it fail.
var removeState = os.Remove

// changeTrial reads the state under the lock and lets change edit it: keep
// writes it back, drop removes it, neither leaves it. A missing or unusable
// state is given to change as readErr.
func changeTrial(folder string, change func(t *Trial, readErr error) (keep bool, drop bool)) error {
	unlock, err := lockState(folder)
	if err != nil {
		return err
	}
	defer unlock()
	trial, readErr := readTrial(folder)
	keep, drop := change(&trial, readErr)
	switch {
	case drop:
		if err := removeState(trialPath(folder)); err != nil && !os.IsNotExist(err) {
			return err
		}
		syncFolder(folder)
	case keep:
		return writeTrial(folder, trial)
	}
	return nil
}

func writeTrial(folder string, trial Trial) error {
	if trial.Format == 0 {
		trial.Format = trialFormat
	}
	raw, err := json.Marshal(trial)
	if err != nil {
		return err
	}
	return writeAtomic(trialPath(folder), raw)
}

// readTrial: os.ErrNotExist when no update is pending; another error when
// the file is there but cannot be used.
//
// The rule that keeps releases able to read each other: the state is
// written by the launcher of the proven release and read by it, by its
// watchdog and by the server of the release on trial (the next one), and
// the marks are written by that next server and read by the earlier
// launcher. So the format must never change incompatibly: add fields only
// (unknown fields are ignored here), keep the meaning of the states, the
// purposes and the mark names, and raise trialFormat only when both the
// earlier and the later code accept the new one. A format higher than this
// code knows is refused as unreadable. Format 0 is the first form of the
// file (never released) and read as format 1.
func readTrial(folder string) (Trial, error) {
	var trial Trial
	raw, err := readRegular(trialPath(folder), maxTrialFile)
	if err != nil {
		if os.IsNotExist(err) {
			return trial, os.ErrNotExist
		}
		return trial, errors.New("the state of the update (" + trialName + ") cannot be read")
	}
	bad := errors.New("the state of the update (" + trialName + ") is not in the expected form")
	if err := json.Unmarshal(raw, &trial); err != nil {
		return trial, bad
	}
	if trial.Format > trialFormat {
		return trial, errors.New("the state of the update (" + trialName + ") was written by a later version of the updater")
	}
	_, newOK := Build(trial.NewVersion)
	_, previousOK := Build(trial.PreviousVersion)
	if !newOK || !previousOK || (trial.ID != "" && !idPattern.MatchString(trial.ID)) {
		return trial, bad
	}
	for _, n := range []int{trial.Format, trial.Run, trial.Failures, trial.TimedOut, trial.KilledRun, trial.DockerExcused} {
		if n < 0 || n > 100000 {
			return trial, bad
		}
	}
	if trial.ZipSHA256 != "" && !sha256Pattern.MatchString(trial.ZipSHA256) {
		return trial, bad
	}
	if _, ok := Build(trial.OnTrial); trial.OnTrial != "" && !ok {
		return trial, bad
	}
	switch trial.State {
	case stateInstalling, stateTrial:
	case stateRestoring:
		switch trial.Purpose {
		case purposeRollback, purposeUnrecorded, purposeUndo, purposeInterrupted:
		default:
			return trial, bad
		}
	default:
		return trial, bad
	}
	trial.Cause = SafeText(trial.Cause)
	return trial, nil
}

// writeAtomic replaces a file with a new one (0600), written beside it,
// synced, renamed, and the folder synced; never through a link.
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
		return err
	}
	syncFolder(filepath.Dir(path))
	return nil
}

func syncFolder(path string) { syncFolderErr(path) }

// syncFolderErr syncs a folder. A variable so tests can make it fail.
var syncFolderErr = func(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func newTrialID() string {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%016x", uint64(time.Now().UnixNano()))
	}
	return hex.EncodeToString(raw)
}

// ---------------------------------------------------------------- marks

// The marks are empty files: creating one needs no data block, so a nearly
// full disk still takes it, and the server rewrites nothing of the state.
func markPath(folder string, id string, run int, kind string) string {
	return filepath.Join(folder, markPrefix+id+"."+strconv.Itoa(run)+"."+kind)
}

func writeMark(folder string, id string, run int, kind string) error {
	if !idPattern.MatchString(id) {
		return errors.New("no trial id")
	}
	return writeEmpty(folder, markPath(folder, id, run, kind))
}

// writeEmpty creates an empty file (never through a link) and syncs the
// folder; a mark that may not survive a power loss is no mark.
func writeEmpty(folder string, path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		if os.IsExist(err) && isEmptyMark(path) {
			return nil
		}
		return err
	}
	file.Close()
	if err := syncFolderErr(folder); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}

func isEmptyMark(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func hasMark(folder string, id string, run int, kind string) bool {
	return idPattern.MatchString(id) && isEmptyMark(markPath(folder, id, run, kind))
}

// clearMarks removes the marks of a trial ("": of every trial).
func clearMarks(folder string, id string) {
	matches, _ := filepath.Glob(filepath.Join(folder, markPrefix+id+"*"))
	for _, path := range matches {
		os.Remove(path)
	}
}

// ------------------------------------------------------ launcher hooks

// launcherEnv is what the launcher's steps do besides files. Tests replace it.
type launcherEnv struct {
	out    io.Writer
	spawn  func(folder string, out io.Writer) // the watchdog of a run
	arm    func(folder string, run TrialRun)  // a SIGTERM to the launcher is a clean stop of run
	pause  func(time.Duration)                // crashPause
	docker func() bool                        // Docker answers the launcher
}

func realEnv() launcherEnv {
	return launcherEnv{out: os.Stdout, spawn: spawnWatchdog, arm: armCleanStop, pause: time.Sleep, docker: dockerAnswers}
}

// stayDown ends the launcher with an error after RestorePause: start.sh
// then does not start the server, and systemd tries again. What is wrong
// and the ways out were printed just before (and are in the README).
var stayDown = func(out io.Writer) {
	fmt.Fprintln(out, "[ERROR] Update: Cosmos is not started; the launcher tries again at the next start (in about "+strconv.Itoa(int((RestorePause+unitRestartSec)/time.Second))+" seconds).")
	time.Sleep(RestorePause)
	exitLauncher(1)
}

// releaseRepo is the repository the releases come from (the launcher's
// updateRepo), for the way out of a stuck restore.
var releaseRepo string

// LauncherTrial is the launcher's hook at every start, before it looks for
// an update. It returns only when the server may be started. repo is the
// repository the releases come from.
func LauncherTrial(folder string, repo string) {
	if repoPattern.MatchString(repo) {
		releaseRepo = repo
	}
	env := realEnv()
	if !launcherTrial(folder, env) {
		stayDown(env.out)
	}
}

// launcherTrial: false when the server must not be started (an owed restore
// did not complete).
func launcherTrial(folder string, env launcherEnv) bool {
	resolved(folder) // what still needs a person is written again below
	trial, err := readTrial(folder)
	if errors.Is(err, os.ErrNotExist) {
		clearMarks(folder, "")
		return true
	}
	if err != nil {
		// a state that cannot be read: back to the version the copy kept is of
		manifest, keptErr := readKept(folder)
		if keptErr != nil {
			explain(folder, Trial{}, fmt.Errorf("%s, and %s", err.Error(), keptErr.Error()), env)
			return false
		}
		fmt.Fprintln(env.out, "[ERROR] Update: "+SafeText(err.Error())+"; going back to "+SafeText(manifest.PreviousVersion)+".")
		trial = Trial{State: stateRestoring, Purpose: purposeRollback, NewVersion: manifest.NewVersion, PreviousVersion: manifest.PreviousVersion,
			Failures: TrialStarts, Touched: true}
		noteState(folder, trial, env) // a full disk must not keep the restore from being tried
		return restoreNow(folder, trial, env)
	}

	switch trial.State {
	case stateInstalling:
		fmt.Fprintln(env.out, "Update: the installation of "+SafeText(trial.NewVersion)+" was cut short; putting back "+SafeText(trial.PreviousVersion)+".")
		trial.State, trial.Purpose, trial.Touched = stateRestoring, purposeInterrupted, true
		noteState(folder, trial, env) // "installing" on disk owes the same restore
		return restoreNow(folder, trial, env)
	case stateRestoring:
		if !trial.Done {
			fmt.Fprintln(env.out, "Update: putting back the files of "+SafeText(trial.PreviousVersion)+", owed since an earlier start.")
		}
		return restoreNow(folder, trial, env)
	}

	// stateTrial: a restore that could not be noted in the state comes first
	if restoreOwed(folder, trial) {
		fmt.Fprintln(env.out, "Update: putting back the files of "+SafeText(trial.PreviousVersion)+", owed since an earlier start.")
		trial.State, trial.Purpose, trial.Touched = stateRestoring, purposeUnrecorded, true
		if trial.Cause == "" {
			trial.Cause = "the state of the update could not be written"
		}
		noteState(folder, trial, env)
		return restoreNow(folder, trial, env)
	}
	if installed, err := installedVersion(folder); err == nil && installed != trial.NewVersion {
		// another version was put there by hand (a newer one unzipped, an
		// older backup restored): the trial is over and nothing is put back.
		// A meta.json that cannot be read never ends a trial.
		endedByHand(folder, trial, installed, env)
		return true
	}

	// how the last start ended (the server's marks, the watchdog's record)
	last := trial.Run
	killed := last > 0 && trial.KilledRun == last
	clean := last > 0 && !killed && hasMark(folder, trial.ID, last, markClean)
	excused := false
	if last > 0 && !killed && !clean && hasMark(folder, trial.ID, last, markDocker) {
		if trial.DockerExcused < DockerExcused && !env.docker() {
			excused = true
			fmt.Fprintln(env.out, "Update: the last start of "+SafeText(trial.NewVersion)+" ended because Docker could not be reached, and the launcher cannot reach it either: not counted.")
		} else {
			fmt.Fprintln(env.out, "Update: the last start of "+SafeText(trial.NewVersion)+" ended because it could not reach Docker, but Docker answers the launcher (or it happened too often): counted.")
		}
	}

	var counted, rollBack, changed bool
	err = changeTrial(folder, func(t *Trial, readErr error) (bool, bool) {
		if readErr != nil || t.State != stateTrial || t.Run != last || t.ID != trial.ID {
			return false, false
		}
		if t.ID == "" {
			t.ID = newTrialID() // a state of the first form (never released) had none
		}
		switch {
		case last == 0 || clean:
		case excused:
			t.DockerExcused++
		default:
			t.Failures++
			counted = true
		}
		if t.Failures >= TrialStarts {
			t.State, t.Purpose, t.Touched = stateRestoring, purposeRollback, false
			rollBack = true
		} else {
			t.Run++
		}
		trial, changed = *t, true
		return true, false
	})
	clearMarks(folder, trial.ID)
	if err != nil || !changed {
		// never run the new version unwatched and uncounted: go back
		fmt.Fprintln(env.out, "[ERROR] Update: the trial of "+SafeText(trial.NewVersion)+" cannot be recorded ("+SafeText(errorText(err))+"); going back to "+SafeText(trial.PreviousVersion)+", without counting it against "+SafeText(trial.NewVersion)+".")
		trial.State, trial.Purpose, trial.Touched, trial.Cause = stateRestoring, purposeUnrecorded, true, SafeText(errorText(err))
		noteState(folder, trial, env) // restoreNow makes sure the restore is owed on disk before it changes a file
		return restoreNow(folder, trial, env)
	}
	if rollBack {
		why := "stopped " + strconv.Itoa(trial.Failures) + " times before it had proved itself"
		if trial.TimedOut > 0 {
			why = "did not answer in time"
		}
		fmt.Fprintln(env.out, "[ERROR] Update: "+SafeText(trial.NewVersion)+" "+why+"; going back to "+SafeText(trial.PreviousVersion)+".")
		return restoreNow(folder, trial, env)
	}
	if counted {
		fmt.Fprintln(env.out, "Update: the last start of "+SafeText(trial.NewVersion)+" ended before it had proved itself: failed start "+strconv.Itoa(trial.Failures)+" of "+strconv.Itoa(TrialStarts)+".")
		if !killed {
			env.pause(crashPause) // keeps clear of systemd's start limit
		}
	}
	fmt.Fprintln(env.out, "Update: "+SafeText(trial.NewVersion)+" has not proved itself yet: start "+strconv.Itoa(trial.Run)+".")
	env.arm(folder, TrialRun{Version: trial.NewVersion, ID: trial.ID, Run: trial.Run})
	env.spawn(folder, env.out)
	return true
}

func errorText(err error) string {
	if err == nil {
		return "the state changed meanwhile"
	}
	return pathless(err).Error()
}

// endedByHand ends a trial whose version is no longer the installed one:
// the state, the copy kept and the marks go, no file of the folder is
// touched.
func endedByHand(folder string, trial Trial, installed string, env launcherEnv) {
	if !endTrialByHand(folder, trial) {
		return
	}
	fmt.Fprintln(env.out, "Update: "+SafeText(installed)+" was installed by other means while "+SafeText(trial.NewVersion)+" was on trial: the trial is over, nothing was put back, and the copy of "+SafeText(trial.PreviousVersion)+" is removed. Nothing watches "+SafeText(installed)+": if it does not start, another version has to be installed by hand.")
}

// endTrialByHand drops the state of a trial whose version is no longer the
// installed one, with everything that belonged to the trial: the copy kept,
// the proof, the launcher of the version that was on trial (it must never be
// put in place now) and the marks. The launcher and the server (TrialPending)
// both end such a trial through here, so that neither leaves a part behind.
func endTrialByHand(folder string, trial Trial) bool {
	ended := false
	changeTrial(folder, func(t *Trial, readErr error) (bool, bool) {
		ended = readErr == nil && t.State == stateTrial && t.NewVersion == trial.NewVersion
		return false, ended
	})
	if !ended {
		return false
	}
	os.RemoveAll(filepath.Join(folder, previousFolder))
	for _, name := range []string{provenName, pendingLauncher, "cosmos-launcher-arm64.updated"} { // the launcher of the trial version too
		os.Remove(filepath.Join(folder, name))
	}
	clearMarks(folder, "")
	syncFolder(folder)
	return true
}

// restoreOwed: the state on disk says "trial" but this code began a restore
// that could not be noted in the state: its restore mark is there. The mark
// is the one sign of that (owedOnDisk writes it before any file changes).
func restoreOwed(folder string, trial Trial) bool {
	return trial.ID != "" && hasMark(folder, trial.ID, 0, markRestore)
}

// owedOnDisk makes sure that what is on disk owes the restore before the
// first file is changed: a state "installing" (always undone at the next
// start), a state "restoring" that says files may have changed, an
// unreadable state (always undone from the copy kept), or else the empty
// restore mark. An error: nothing may be changed.
func owedOnDisk(folder string) error {
	trial, err := readTrial(folder)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return errors.New("the state of the update is gone, so a restore could not be noted as owed")
	case err != nil:
		return nil // unreadable: the next start undoes from the copy kept all the same
	case trial.State == stateInstalling, trial.State == stateRestoring && trial.Touched:
		return nil
	case trial.ID == "":
		return errors.New("the restore could not be noted as owed (the state has no trial id)")
	}
	if err := writeMark(folder, trial.ID, 0, markRestore); err != nil {
		return fmt.Errorf("the restore could not be noted as owed: %v", pathless(err))
	}
	return nil
}

// noteState writes the state when it can. A full disk must not keep a
// restore from starting: it frees room itself, and the state on disk owes
// the same restore at the next start.
func noteState(folder string, trial Trial, env launcherEnv) {
	if err := changeTrial(folder, func(t *Trial, _ error) (bool, bool) { *t = trial; return true, false }); err != nil {
		fmt.Fprintln(env.out, "Update: the state of the update could not be noted ("+SafeText(pathless(err).Error())+"); the restore goes on.")
	}
}

// KeepReplaced is the launcher's hook just before it unzips the accepted
// update. False: nothing may be installed; the update files are removed and
// the reason is left for the server to report.
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
		fmt.Fprintln(out, "The files the update replaces are kept in "+previousFolder+": it goes back by itself if "+SafeText(newVersion)+" does not start properly.")
		return true
	}
	os.Remove(filepath.Join(folder, StagedZip))
	os.Remove(filepath.Join(folder, StagedSignature))
	syncFolder(folder)
	if errors.Is(err, errLeftoverZip) {
		fmt.Fprintln(out, "Update: the update file beside the launcher is the one of "+SafeText(newVersion)+", already installed and on trial; removed.")
		return false
	}
	reason := SafeText(pathless(err).Error())
	var space *noSpaceError
	var busy *busyError
	if !errors.As(err, &space) && !errors.As(err, &busy) {
		reason = "the files it replaces could not be kept, so it could not go back by itself: " + reason
	}
	fmt.Fprintln(out, "[ERROR] Update NOT installed: "+reason)
	writeNew(filepath.Join(folder, rejectedReport), []byte(reason+"\n"))
	return false
}

// BeginTrial is the launcher's hook after the update was unzipped: the new
// version starts on trial, watched.
func BeginTrial(folder string) {
	beginTrial(folder, realEnv())
}

func beginTrial(folder string, env launcherEnv) {
	syncInstalled(folder) // what the trial relies on survives a power loss
	var trial Trial
	same := false
	abandoned := ""
	err := changeTrial(folder, func(t *Trial, readErr error) (bool, bool) {
		if readErr != nil || t.State != stateInstalling {
			return false, false
		}
		abandoned = t.OnTrial
		if t.NewVersion == t.PreviousVersion {
			same = true
			return false, true // the proven version again: nothing to try, never "failed"
		}
		*t = Trial{State: stateTrial, NewVersion: t.NewVersion, PreviousVersion: t.PreviousVersion, ID: newTrialID(), Run: 1, ZipSHA256: t.ZipSHA256}
		trial = *t
		return true, false
	})
	clearMarks(folder, "")
	if abandoned != "" && abandoned != trial.NewVersion {
		fmt.Fprintln(env.out, "Update: "+SafeText(abandoned)+", which was on trial, was replaced by this install; it is neither proven nor failed.")
	}
	switch {
	case err != nil:
		fmt.Fprintln(env.out, "[ERROR] Update: the trial of the new version cannot be started: "+SafeText(pathless(err).Error()))
	case same:
		fmt.Fprintln(env.out, "Update: the version that was proven was installed again; there is nothing to try.")
	case trial.Run == 1:
		fmt.Fprintln(env.out, "Update: "+SafeText(trial.NewVersion)+" starts on trial (start 1; it goes back after "+strconv.Itoa(TrialStarts)+" failed starts).")
		env.arm(folder, TrialRun{Version: trial.NewVersion, ID: trial.ID, Run: 1})
		env.spawn(folder, env.out)
	}
}

// syncInstalled syncs every file and folder the update wrote, and the folder.
func syncInstalled(folder string) {
	files, dirs, err := InstallTargets(filepath.Join(folder, StagedZip), folder)
	if err == nil {
		for _, rel := range append(files, dirs...) {
			if file, err := os.OpenFile(filepath.Join(folder, rel), os.O_RDONLY|syscall.O_NOFOLLOW, 0); err == nil {
				file.Sync()
				file.Close()
			}
		}
	}
	syncFolder(folder)
}

// UndoInstall is the launcher's hook when the unzip failed: the kept files
// go back at once. It returns only when the server may be started.
func UndoInstall(folder string, cause error) {
	env := realEnv()
	if !undoInstall(folder, cause, env) {
		stayDown(env.out)
	}
}

func undoInstall(folder string, cause error, env launcherEnv) bool {
	trial, err := readTrial(folder)
	if err != nil || trial.State != stateInstalling {
		return true
	}
	text := "unknown error"
	if cause != nil {
		text = SafeText(pathless(cause).Error())
	}
	trial.State, trial.Purpose, trial.Cause, trial.Touched = stateRestoring, purposeUndo, text, true
	noteState(folder, trial, env) // "installing" on disk owes the same restore
	fmt.Fprintln(env.out, "[ERROR] Update: "+SafeText(trial.NewVersion)+" could not be unpacked ("+text+"); putting back "+SafeText(trial.PreviousVersion)+".")
	return restoreNow(folder, trial, env)
}

// armCleanStop: a SIGTERM or SIGINT to the launcher while a run is starting
// is a clean stop of that run, not a failure.
func armCleanStop(folder string, run TrialRun) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-signals
		writeMark(folder, run.ID, run.Run, markClean)
		exitLauncher(143)
	}()
}

// accepted is the release LauncherAccepts let through in this run of the
// launcher: KeepReplaced takes its version from it, not from the files.
var accepted *Manifest
