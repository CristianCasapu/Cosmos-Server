package forkrelease

// Fork addition: an update that does not start goes back by itself.
//
// The launcher installs a release only after LauncherAccepts and upstream's
// MD5 check, both unchanged. Around the unpacking it keeps a way back, and
// the state of that way back is one small file, cosmos-update.trial, changed
// only under an exclusive lock (cosmos-update.lock):
//
//	installing  the copy kept is complete and the unzip is under way
//	trial       the new version runs and has not proved itself yet
//	restoring   a restore is owed (a rollback, a failed or cut-short unzip)
//
// The launcher's hooks (src/launcher/launcher.go):
//
//   - KeepReplaced, just before unzipping: checks the free space, copies every
//     file the zip will overwrite into cosmos-update.previous/ (0700) with a
//     manifest, and sets "installing". Not enough room or any error: nothing
//     is touched, the update is refused in words (cosmos-update.rejected, which
//     the server reports).
//   - UndoInstall, when the unzip fails: the restore runs at once.
//   - BeginTrial, after the unzip: every file written is synced, the state
//     becomes "trial" (start 1), a watchdog is started.
//   - LauncherTrial, at every start: finishes an owed restore first (until it
//     completes the launcher ends with an error after a pause, so start.sh
//     never runs a half-restored folder); in "trial" counts the last start as
//     failed unless it ended by a clean stop, rolls back after TrialStarts
//     failed starts, or starts the next one, watched.
//   - RunWatchdog: the launcher started with WatchdogArg. It ends this
//     folder's cosmos process when the start has not answered within
//     WatchdogWindow, or not proved itself ProofGrace after its first answer.
//
// The new server (src/fork_update_trial.go) notes its first answer, notes a
// clean stop (SIGTERM, SIGINT, a planned restart), and proves itself with
// ProveTrial; only then is the launcher that came with it put in place
// (cleanUpUpdateFiles waits while TrialPending).
//
// A restore is idempotent: the copy kept is never changed by it. It removes
// the added files first (that frees room), then puts every replaced file
// back after checking it against its SHA-256, syncs, and only then records
// the failed version, writes the note for the server and clears the state.
// Only the program files go back: data and settings a newer version changed
// stay as they are. No wall clock decides anything in a trial: starts are
// counted, and the watchdog measures on the monotonic clock.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
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

	// WatchdogArg is the one argument the launcher is started with as the
	// watchdog of a trial.
	WatchdogArg = "--fork-update-watchdog"

	stateInstalling = "installing"
	stateTrial      = "trial"
	stateRestoring  = "restoring"

	purposeRollback    = "rollback"    // the new version did not prove itself
	purposeUndo        = "undo"        // the unzip failed
	purposeInterrupted = "interrupted" // the unzip was cut short (power loss, kill)

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

// The service's unit (src/index.go) restarts Cosmos RestartSec after it
// ends, and systemd gives up for good when it is started StartLimitBurst
// times within StartLimitInterval. A release that crashes at once must
// reach the start that goes back well before that: TrialStarts failed
// starts, each followed by crashPause in the launcher, keep at most two
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
)

// trialRefusals start the reasons the trial code refuses an update for.
// None is about a release that could not be verified.
var trialRefusals = []string{"not enough free space", "the files it replaces could not be kept", "it could not be unpacked",
	"its installation was cut short", ErrFailedHere.Error()}

// SecurityRefusal: a refusal noted by the launcher is about the release's
// signature or origin (a security incident), not about room, an unzip, or a
// version that went back here.
func SecurityRefusal(reason string) bool {
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
	State           string `json:"state"`
	NewVersion      string `json:"newVersion"`
	PreviousVersion string `json:"previousVersion"`
	// Run numbers the starts of the new version; the watchdog, the clean stop
	// and the proof refer to one run.
	Run         int `json:"run,omitempty"`
	Failures    int `json:"failures,omitempty"`    // runs that ended without proof and without a clean stop
	TimedOut    int `json:"timedOut,omitempty"`    // runs the watchdog ended
	CleanRun    int `json:"cleanRun,omitempty"`    // the run that ended by a clean stop
	KilledRun   int `json:"killedRun,omitempty"`   // the run the watchdog ended
	AnsweredRun int `json:"answeredRun,omitempty"` // the run that answered on its HTTP port
	// restoring
	Purpose string `json:"purpose,omitempty"`
	Touched bool   `json:"touched,omitempty"` // the restore may have changed files of the folder
	Cause   string `json:"cause,omitempty"`   // why the unzip failed
}

// RollbackNote is the content of cosmos-update.rolledback.
type RollbackNote struct {
	Failed   string `json:"failed"`   // the version that did not start properly
	Back     string `json:"back"`     // the version the folder went back to
	Reason   string `json:"reason"`   // RollbackStopped, RollbackTimeout or RollbackDamaged
	Starts   int    `json:"starts"`   // failed starts it was given
	Minutes  int    `json:"minutes"`  // minutes one start was given to answer
	Restored bool   `json:"restored"` // false: the kept files could not be put back
	File     string `json:"-"`        // the list of failed versions, to allow one again
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
		if err := os.Remove(trialPath(folder)); err != nil && !os.IsNotExist(err) {
			return err
		}
		syncFolder(folder)
	case keep:
		return writeTrial(folder, trial)
	}
	return nil
}

func writeTrial(folder string, trial Trial) error {
	raw, err := json.Marshal(trial)
	if err != nil {
		return err
	}
	return writeAtomic(trialPath(folder), raw)
}

// readTrial: os.ErrNotExist when no update is pending; another error when
// the file is there but cannot be used.
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
	_, newOK := Build(trial.NewVersion)
	_, previousOK := Build(trial.PreviousVersion)
	if !newOK || !previousOK {
		return trial, bad
	}
	for _, n := range []int{trial.Run, trial.Failures, trial.TimedOut, trial.CleanRun, trial.KilledRun, trial.AnsweredRun} {
		if n < 0 || n > 100000 {
			return trial, bad
		}
	}
	switch trial.State {
	case stateInstalling, stateTrial:
	case stateRestoring:
		switch trial.Purpose {
		case purposeRollback, purposeUndo, purposeInterrupted:
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

func syncFolder(path string) {
	if dir, err := os.Open(path); err == nil {
		dir.Sync()
		dir.Close()
	}
}

// ------------------------------------------------------ launcher hooks

// launcherEnv is what the launcher's steps do besides files. Tests replace it.
type launcherEnv struct {
	out   io.Writer
	spawn func(folder string, out io.Writer)           // the watchdog of a run
	arm   func(folder string, version string, run int) // a SIGTERM to the launcher is a clean stop of run
	pause func(time.Duration)                          // crashPause
}

func realEnv() launcherEnv {
	return launcherEnv{out: os.Stdout, spawn: spawnWatchdog, arm: armCleanStop, pause: time.Sleep}
}

// stayDown ends the launcher with an error after RestorePause: start.sh
// then does not start the server, and systemd tries again.
var stayDown = func(out io.Writer) {
	fmt.Fprintln(out, "[ERROR] Update: Cosmos is not started until the files of the version before are back; trying again at the next start.")
	time.Sleep(RestorePause)
	exitLauncher(1)
}

// LauncherTrial is the launcher's hook at every start, before it looks for
// an update. It returns only when the server may be started.
func LauncherTrial(folder string) {
	env := realEnv()
	if !launcherTrial(folder, env) {
		stayDown(env.out)
	}
}

// launcherTrial: false when the server must not be started (an owed restore
// did not complete).
func launcherTrial(folder string, env launcherEnv) bool {
	trial, err := readTrial(folder)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil {
		// a state that cannot be read: back to the version the copy kept is of
		manifest, keptErr := readKept(folder)
		if keptErr != nil {
			fmt.Fprintln(env.out, "[ERROR] Update: "+SafeText(err.Error())+" and "+SafeText(keptErr.Error())+": nothing can be put back by itself. Removing "+trialPath(folder)+" starts the installed files as they are.")
			return false
		}
		fmt.Fprintln(env.out, "[ERROR] Update: "+SafeText(err.Error())+"; going back to "+SafeText(manifest.PreviousVersion)+".")
		trial = Trial{State: stateRestoring, Purpose: purposeRollback, NewVersion: manifest.NewVersion, PreviousVersion: manifest.PreviousVersion,
			Failures: TrialStarts, Touched: true}
		if !setState(folder, trial, env) {
			return false
		}
		return restoreNow(folder, trial, env)
	}

	switch trial.State {
	case stateInstalling:
		fmt.Fprintln(env.out, "Update: the installation of "+SafeText(trial.NewVersion)+" was cut short; putting back "+SafeText(trial.PreviousVersion)+".")
		trial.State, trial.Purpose, trial.Touched = stateRestoring, purposeInterrupted, true
		noteState(folder, trial, env) // "installing" on disk owes the same restore
		return restoreNow(folder, trial, env)
	case stateRestoring:
		fmt.Fprintln(env.out, "Update: putting back the files of "+SafeText(trial.PreviousVersion)+", owed since an earlier start.")
		return restoreNow(folder, trial, env)
	}

	// stateTrial
	if installed, err := installedVersion(folder); err == nil && installed != trial.NewVersion {
		// installed by other means since: not this trial's business any more.
		// A meta.json that cannot be read never ends a trial.
		changeTrial(folder, func(t *Trial, readErr error) (bool, bool) {
			return false, readErr == nil && t.State == stateTrial && t.NewVersion == trial.NewVersion
		})
		fmt.Fprintln(env.out, "Update: the trial of "+SafeText(trial.NewVersion)+" is over: "+SafeText(installed)+" is installed.")
		return true
	}

	var counted, timedOut, rollBack, changed bool
	err = changeTrial(folder, func(t *Trial, readErr error) (bool, bool) {
		if readErr != nil || t.State != stateTrial {
			return false, false
		}
		if t.Run > 0 && t.CleanRun != t.Run {
			t.Failures++
			counted, timedOut = true, t.KilledRun == t.Run
		}
		if t.Failures >= TrialStarts {
			t.State, t.Purpose, t.Touched = stateRestoring, purposeRollback, false
			rollBack = true
		} else {
			t.Run++
			t.CleanRun, t.AnsweredRun = 0, 0
		}
		trial, changed = *t, true
		return true, false
	})
	if err != nil || !changed {
		fmt.Fprintln(env.out, "[ERROR] Update: the state of the trial cannot be written: "+SafeText(errorText(err)))
		return true // the version on trial runs; the next start tries again
	}
	if rollBack {
		why := "stopped " + strconv.Itoa(trial.Failures) + " times before it had started properly"
		if trial.TimedOut > 0 {
			why = "did not answer in time"
		}
		fmt.Fprintln(env.out, "[ERROR] Update: "+SafeText(trial.NewVersion)+" "+why+"; going back to "+SafeText(trial.PreviousVersion)+".")
		return restoreNow(folder, trial, env)
	}
	if counted {
		fmt.Fprintln(env.out, "Update: the last start of "+SafeText(trial.NewVersion)+" ended before it had proved itself: failed start "+strconv.Itoa(trial.Failures)+" of "+strconv.Itoa(TrialStarts)+".")
		if !timedOut {
			env.pause(crashPause) // keeps clear of systemd's start limit
		}
	}
	fmt.Fprintln(env.out, "Update: "+SafeText(trial.NewVersion)+" has not proved itself yet: start "+strconv.Itoa(trial.Run)+".")
	env.arm(folder, trial.NewVersion, trial.Run)
	env.spawn(folder, env.out)
	return true
}

func errorText(err error) string {
	if err == nil {
		return "the state changed meanwhile"
	}
	return pathless(err).Error()
}

// setState writes the state as given.
func setState(folder string, trial Trial, env launcherEnv) bool {
	if err := changeTrial(folder, func(t *Trial, _ error) (bool, bool) { *t = trial; return true, false }); err != nil {
		fmt.Fprintln(env.out, "[ERROR] Update: the state of the update cannot be written: "+SafeText(pathless(err).Error()))
		return false
	}
	return true
}

// noteState writes the state when it can. A full disk must not keep a
// restore from starting: it frees room itself, and "installing" on disk
// owes the same restore at the next start.
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
	reason := SafeText(pathless(err).Error())
	var space *noSpaceError
	if !errors.As(err, &space) {
		reason = "the files it replaces could not be kept, so it could not go back by itself: " + reason
	}
	fmt.Fprintln(out, "[ERROR] Update NOT installed: "+reason)
	os.Remove(filepath.Join(folder, StagedZip))
	os.Remove(filepath.Join(folder, StagedSignature))
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
	err := changeTrial(folder, func(t *Trial, readErr error) (bool, bool) {
		if readErr != nil || t.State != stateInstalling {
			return false, false
		}
		if t.NewVersion == t.PreviousVersion {
			same = true
			return false, true // the proven version again: nothing to try, never "failed"
		}
		*t = Trial{State: stateTrial, NewVersion: t.NewVersion, PreviousVersion: t.PreviousVersion, Run: 1}
		trial = *t
		return true, false
	})
	switch {
	case err != nil:
		fmt.Fprintln(env.out, "[ERROR] Update: the trial of the new version cannot be started: "+SafeText(pathless(err).Error()))
	case same:
		fmt.Fprintln(env.out, "Update: the version that was proven was installed again; there is nothing to try.")
	case trial.Run == 1:
		fmt.Fprintln(env.out, "Update: "+SafeText(trial.NewVersion)+" starts on trial (start 1; it goes back after "+strconv.Itoa(TrialStarts)+" failed starts).")
		env.arm(folder, trial.NewVersion, 1)
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
func armCleanStop(folder string, version string, run int) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-signals
		MarkCleanStop(folder, version, run)
		exitLauncher(143)
	}()
}

// ------------------------------------------------------- for the server

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
	if err != nil || installed == trial.NewVersion {
		return true
	}
	dropped := false
	changeTrial(folder, func(t *Trial, readErr error) (bool, bool) {
		dropped = readErr == nil && t.State == stateTrial && t.NewVersion == trial.NewVersion
		return false, dropped
	})
	return !dropped
}

// PendingTrial returns the trial of this folder, if one is running.
func PendingTrial(folder string) (Trial, bool) {
	trial, err := readTrial(folder)
	return trial, err == nil && trial.State == stateTrial
}

// own: the state is the trial of version, run, and the watchdog has not
// ended that run.
func own(t *Trial, readErr error, version string, run int) bool {
	return readErr == nil && t.State == stateTrial && t.NewVersion == version && t.Run == run && t.KilledRun != run
}

// MarkAnswered notes that run answered for the first time: the watchdog
// then gives it ProofGrace to prove itself.
func MarkAnswered(folder string, version string, run int) {
	changeTrial(folder, func(t *Trial, readErr error) (bool, bool) {
		if !own(t, readErr, version, run) || t.AnsweredRun == run {
			return false, false
		}
		t.AnsweredRun = run
		return true, false
	})
}

// MarkCleanStop notes that run is ending because it was asked to (a
// signal, a planned restart): it is not counted as a failed start.
func MarkCleanStop(folder string, version string, run int) {
	changeTrial(folder, func(t *Trial, readErr error) (bool, bool) {
		if !own(t, readErr, version, run) {
			return false, false
		}
		t.CleanRun = run
		return true, false
	})
}

// ProveTrial ends the trial of version: run started properly. It fails when
// the watchdog already ended that run.
func ProveTrial(folder string, version string, run int) error {
	proven := false
	err := changeTrial(folder, func(t *Trial, readErr error) (bool, bool) {
		proven = own(t, readErr, version, run)
		return false, proven
	})
	if err != nil {
		return err
	}
	if !proven {
		return errors.New("the update on trial is another version or start, or its time is up")
	}
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

// accepted is the release LauncherAccepts let through in this run of the
// launcher: KeepReplaced takes its version from it, not from the files.
var accepted *Manifest
