package forkrelease

// Fork addition: the watchdog of a start on trial (see trial.go).

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func spawnWatchdog(folder string, out io.Writer) {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(out, "[ERROR] Update: the watchdog of the trial cannot be started: "+SafeText(pathless(err).Error()))
		return
	}
	startWatchdog(self, folder, out)
}

// startWatchdog starts program with the one fixed argument, detached.
func startWatchdog(program string, folder string, out io.Writer) {
	cmd := exec.Command(program, WatchdogArg)
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

// watchdog waits for the run on trial to answer within WatchdogWindow and
// to prove itself within ProofGrace of its first answer; when it does not,
// it notes the time-out (under the lock, so a proof and a time-out never
// both happen) and ends the server.
func watchdog(folder string, out io.Writer, end func(string, io.Writer)) {
	trial, err := readTrial(folder)
	if err != nil || trial.State != stateTrial {
		return
	}
	run := trial.Run
	begun := time.Now() // monotonic: a clock set meanwhile changes nothing
	var answered time.Time
	for {
		time.Sleep(watchdogPoll)
		now, err := readTrial(folder)
		if err != nil || now.State != stateTrial || now.Run != run {
			return // proven, gone back, or another start watches itself
		}
		if now.AnsweredRun == run && answered.IsZero() {
			answered = time.Now()
		}
		if answered.IsZero() && time.Since(begun) >= WatchdogWindow {
			break
		}
		if !answered.IsZero() && time.Since(answered) >= ProofGrace {
			break
		}
	}
	if !timeOut(folder, run) {
		return
	}
	limit := strconv.Itoa(int(WatchdogWindow/time.Minute)) + " minutes"
	if !answered.IsZero() {
		limit = strconv.Itoa(int(ProofGrace/time.Minute)) + " minutes after its first answer"
	}
	fmt.Fprintln(out, "[ERROR] Update: "+SafeText(trial.NewVersion)+" did not prove itself within "+limit+"; ending it (start "+strconv.Itoa(run)+").")
	end(folder, out)
}

// timeOut notes, under the lock, that run's time is up. False when the run
// was proven (or ended otherwise) meanwhile: the proof and the time-out
// never both happen, and a proven trial is never written again.
func timeOut(folder string, run int) bool {
	ended := false
	changeTrial(folder, func(t *Trial, readErr error) (bool, bool) {
		if readErr != nil || t.State != stateTrial || t.Run != run || t.KilledRun == run {
			return false, false
		}
		t.TimedOut++
		t.KilledRun = run
		ended = true
		return true, false
	})
	return ended
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
		time.Sleep(200 * time.Millisecond)
	}
	for _, pid := range serverProcesses(folder) {
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			fmt.Fprintln(out, "[ERROR] Update: the server could not be ended: "+err.Error())
		}
	}
	if len(pids) == 0 {
		fmt.Fprintln(out, "Update: no running server of this folder was found to end.")
	}
}
