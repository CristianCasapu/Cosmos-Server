package forkrelease

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Fork addition: the trial of a new version and the way back, in a folder
// made up like an install.

const (
	oldVersion = "0.24.0-fork.9"
	newVersion = testVersion // 0.24.0-fork.10
)

type zipEntry struct {
	name    string
	content string
	mode    os.FileMode
}

func zipOf(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	buffer := &bytes.Buffer{}
	writer := zip.NewWriter(buffer)
	for _, e := range entries {
		header := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		mode := e.mode
		if mode == 0 {
			mode = 0644
		}
		header.SetMode(mode)
		w, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(e.name, "/") {
			w.Write([]byte(e.content))
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// release zip of the new version: replaces cosmos, static/app.js and
// meta.json, adds static/new/chunk.js, a launcher and leaves start.sh alone
func newReleaseZip(t *testing.T) []byte {
	return zipOf(t,
		zipEntry{name: "cosmos-cloud-" + newVersion + "/"},
		zipEntry{name: "cosmos-cloud-" + newVersion + "/cosmos", content: "new server", mode: 0755},
		zipEntry{name: "cosmos-cloud-" + newVersion + "/cosmos-launcher", content: "new launcher", mode: 0755},
		zipEntry{name: "cosmos-cloud-" + newVersion + "/start.sh", content: "#!/bin/sh\necho evil\n", mode: 0755},
		zipEntry{name: "cosmos-cloud-" + newVersion + "/meta.json", content: `{"version": "` + newVersion + `"}`},
		zipEntry{name: "cosmos-cloud-" + newVersion + "/static/"},
		zipEntry{name: "cosmos-cloud-" + newVersion + "/static/app.js", content: "new app"},
		zipEntry{name: "cosmos-cloud-" + newVersion + "/static/new/chunk.js", content: "new chunk"},
	)
}

// oldInstall is a folder with the old version installed and the new
// release staged next to it.
func oldInstall(t *testing.T, zipContent []byte) string {
	t.Helper()
	folder := t.TempDir()
	files := map[string]struct {
		content string
		mode    os.FileMode
	}{
		"cosmos":          {"old server", 0755},
		"cosmos-launcher": {"old launcher", 0750},
		"start.sh":        {"#!/bin/bash\n./cosmos-launcher && ./cosmos\n", 0755},
		"meta.json":       {`{"version": "` + oldVersion + `"}`, 0644},
		"static/app.js":   {"old app", 0640},
		"static/keep.css": {"untouched", 0644},
		"nebula":          {"old nebula", 0755},
	}
	for name, f := range files {
		path := filepath.Join(folder, name)
		os.MkdirAll(filepath.Dir(path), 0755)
		if err := os.WriteFile(path, []byte(f.content), f.mode); err != nil {
			t.Fatal(err)
		}
		os.Chmod(path, f.mode)
	}
	if zipContent != nil {
		if err := os.WriteFile(filepath.Join(folder, StagedZip), zipContent, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return folder
}

// snapshot is every file of the folder with its mode and content, the
// updater's own state left out.
func snapshot(t *testing.T, folder string) map[string]string {
	t.Helper()
	found := map[string]string{}
	filepath.Walk(folder, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(folder, path)
		if rel == "." || stateEntry(rel) {
			if info.IsDir() && rel != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			found[rel+"/"] = info.Mode().Perm().String()
			return nil
		}
		content, _ := os.ReadFile(path)
		found[rel] = info.Mode().Perm().String() + " " + string(content)
		return nil
	})
	return found
}

// extract unzips like the launcher, by the mapping InstallTargets gives
// (the launcher's test proves that mapping is unzip's).
func extract(t *testing.T, folder string) {
	t.Helper()
	reader, err := zip.OpenReader(filepath.Join(folder, StagedZip))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	files, dirs, err := zipTargets(reader.File, folder)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		os.MkdirAll(filepath.Join(folder, dir), os.ModePerm)
	}
	i := 0
	for _, f := range reader.File {
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/start.sh") {
			continue
		}
		path := filepath.Join(folder, files[i])
		i++
		os.MkdirAll(filepath.Dir(path), os.ModePerm)
		rc, _ := f.Open()
		out, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(out, rc)
		out.Close()
		rc.Close()
	}
}

func TestTheZipsTargets(t *testing.T) {
	folder := t.TempDir()
	path := filepath.Join(folder, "u.zip")
	os.WriteFile(path, newReleaseZip(t), 0600)
	files, dirs, err := InstallTargets(path, folder)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"cosmos", "cosmos-launcher.updated", "meta.json", "static/app.js", "static/new/chunk.js"}
	if strings.Join(files, ",") != strings.Join(want, ",") {
		t.Errorf("files %v, want %v (start.sh skipped, the launcher as .updated)", files, want)
	}
	if strings.Join(dirs, ",") != ".,static" {
		t.Errorf("dirs %v", dirs)
	}
	for _, bad := range []string{"cosmos-cloud/../../evil", "/etc/evil"} {
		os.WriteFile(path, zipOf(t, zipEntry{name: "cosmos-cloud/ok"}, zipEntry{name: bad, content: "x"}), 0600)
		if _, _, err := InstallTargets(path, folder); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
	os.WriteFile(path, zipOf(t, zipEntry{name: "cosmos-cloud/ok"}, zipEntry{name: "cosmos-cloud/link", content: "/etc/passwd", mode: os.ModeSymlink | 0777}), 0600)
	if _, _, err := InstallTargets(path, folder); err == nil {
		t.Error("a link entry was accepted")
	}
}

// The whole way: kept, unzipped, three starts without proof, back.

// fakeEnv stands in for the watchdog, the signal handler and the pauses.
type fakeEnv struct {
	spawned, armed int
	paused         []time.Duration
	out            bytes.Buffer
}

func (f *fakeEnv) env() launcherEnv {
	return launcherEnv{out: &f.out,
		spawn: func(string, io.Writer) { f.spawned++ },
		arm:   func(string, string, int) { f.armed++ },
		pause: func(d time.Duration) { f.paused = append(f.paused, d) }}
}

func readTrialFile(t *testing.T, folder string) Trial {
	t.Helper()
	trial, err := readTrial(folder)
	if err != nil {
		t.Fatalf("the state file: %v", err)
	}
	return trial
}

func noTrial(folder string) bool {
	_, err := os.Lstat(trialPath(folder))
	return os.IsNotExist(err)
}

// installNew installs the staged zip as the launcher does: kept, unzipped,
// on trial (start 1).
func installNew(t *testing.T, folder string, version string, f *fakeEnv) {
	t.Helper()
	if !keepReplaced(folder, version, &f.out) {
		t.Fatalf("not kept: %s", f.out.String())
	}
	extract(t, folder)
	beginTrial(folder, f.env())
	os.Remove(filepath.Join(folder, StagedZip))
}

// sameAs reports how folder differs from a snapshot.
func sameAs(t *testing.T, what string, folder string, before map[string]string) {
	t.Helper()
	after := snapshot(t, folder)
	for name, was := range before {
		if after[name] != was {
			t.Errorf("%s: %s is %q, was %q", what, name, after[name], was)
		}
	}
	for name := range after {
		if _, ok := before[name]; !ok {
			t.Errorf("%s: %s was not there before", what, name)
		}
	}
}

// The whole way: kept, unzipped, three failed starts, back.
func TestAnUpdateThatNeverProvesItselfGoesBack(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	before := snapshot(t, folder)
	f := &fakeEnv{}

	if !keepReplaced(folder, newVersion, &f.out) {
		t.Fatalf("not kept: %s", f.out.String())
	}
	if info, err := os.Lstat(filepath.Join(folder, previousFolder)); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("the kept folder: %v %v", info, err)
	}
	manifest, err := readKept(folder)
	if err != nil {
		t.Fatal(err)
	}
	replaced := []string{}
	for _, k := range manifest.Replaced {
		replaced = append(replaced, k.Path)
		if info, _ := os.Stat(filepath.Join(folder, previousFolder, k.Path)); info.Mode().Perm() != 0600 {
			t.Errorf("kept copy of %s has mode %v", k.Path, info.Mode().Perm())
		}
	}
	sort.Strings(replaced)
	if strings.Join(replaced, ",") != "cosmos,meta.json,static/app.js" ||
		strings.Join(manifest.Added, ",") != "cosmos-launcher.updated,static/new/chunk.js" || strings.Join(manifest.AddedDirs, ",") != "static/new" {
		t.Errorf("replaced %v, added %v, dirs %v", replaced, manifest.Added, manifest.AddedDirs)
	}
	if trial := readTrialFile(t, folder); trial.State != stateInstalling || trial.PreviousVersion != oldVersion || trial.NewVersion != newVersion {
		t.Errorf("before the unzip the state is %+v", trial)
	}

	extract(t, folder)
	beginTrial(folder, f.env())
	if trial := readTrialFile(t, folder); trial.State != stateTrial || trial.Run != 1 || trial.Failures != 0 {
		t.Errorf("after the unzip the state is %+v", trial)
	}
	if f.spawned != 1 || f.armed != 1 || !TrialPending(folder) {
		t.Errorf("start 1: %d watchdogs, %d handlers, pending %v", f.spawned, f.armed, TrialPending(folder))
	}
	os.Remove(filepath.Join(folder, StagedZip))

	// two crashes: counted, paused, the next start watched
	for failed := 1; failed < TrialStarts; failed++ {
		if !launcherTrial(folder, f.env()) {
			t.Fatal("the server may not start")
		}
		if trial := readTrialFile(t, folder); trial.Failures != failed || trial.Run != failed+1 {
			t.Fatalf("after crash %d the state is %+v", failed, trial)
		}
	}
	if f.spawned != TrialStarts || len(f.paused) != TrialStarts-1 || f.paused[0] != crashPause {
		t.Errorf("%d watchdogs, pauses %v", f.spawned, f.paused)
	}

	// the third crash: back
	if !launcherTrial(folder, f.env()) {
		t.Fatalf("the server may not start after going back: %s", f.out.String())
	}
	sameAs(t, "after going back", folder, before)
	if !noTrial(folder) || f.spawned != TrialStarts {
		t.Error("the state is still there, or a watchdog was started for the start that went back")
	}
	if !failedIn(folder, newVersion) || failedIn(folder, oldVersion) {
		t.Errorf("failed list: %v", readFailed(folder))
	}
	note, found := TakeRollbackNote(folder)
	if !found || note.Failed != newVersion || note.Back != oldVersion || note.Reason != RollbackStopped || !note.Restored ||
		note.Starts != TrialStarts || note.Minutes != 15 || note.File != filepath.Join(folder, failedName) {
		t.Errorf("note %+v", note)
	}
	if _, err := readKept(folder); err != nil {
		t.Error("the copy kept is gone after going back")
	}
	for _, line := range []string{"failed start 1 of 3", "failed start 2 of 3", "going back to " + oldVersion,
		"To allow it again, remove the line " + newVersion + " from " + filepath.Join(folder, failedName)} {
		if !strings.Contains(f.out.String(), line) {
			t.Errorf("the launcher did not print %q:\n%s", line, f.out.String())
		}
	}
	// the next start: nothing pending
	if !launcherTrial(folder, f.env()) || f.spawned != TrialStarts || RollbackNoted(folder) {
		t.Error("a start after going back did something")
	}
}

// Stops that were asked for are not failures, wherever they come; crashes
// in between still count.
func TestCleanStopsDoNotUndoAGoodRelease(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	f := &fakeEnv{}
	installNew(t, folder, newVersion, f)

	for i := 0; i < 10; i++ {
		trial := readTrialFile(t, folder)
		switch i % 3 {
		case 0: // the service stopped while the server started
			MarkCleanStop(folder, newVersion, trial.Run)
		case 1: // ... while it was in its minute of proof
			MarkAnswered(folder, newVersion, trial.Run)
			MarkCleanStop(folder, newVersion, trial.Run)
		case 2: // a planned restart from the panel, noted the same way
			MarkCleanStop(folder, newVersion, trial.Run)
		}
		if !launcherTrial(folder, f.env()) {
			t.Fatal("the server may not start")
		}
	}
	trial := readTrialFile(t, folder)
	if trial.Failures != 0 || trial.Run != 11 || len(f.paused) != 0 {
		t.Fatalf("after 10 clean stops: %+v, pauses %v", trial, f.paused)
	}
	// two crashes among them still count, the version stays
	launcherTrial(folder, f.env())
	MarkCleanStop(folder, newVersion, readTrialFile(t, folder).Run)
	launcherTrial(folder, f.env())
	launcherTrial(folder, f.env())
	if trial := readTrialFile(t, folder); trial.Failures != 2 || trial.State != stateTrial {
		t.Fatalf("after two crashes: %+v", trial)
	}
	// a stop noted for another run, or by another version, is not this one's
	MarkCleanStop(folder, newVersion, readTrialFile(t, folder).Run-1)
	MarkCleanStop(folder, oldVersion, readTrialFile(t, folder).Run)
	if err := ProveTrial(folder, newVersion, readTrialFile(t, folder).Run); err != nil {
		t.Fatal(err)
	}
	if !noTrial(folder) || failedIn(folder, newVersion) {
		t.Error("a good release was not kept")
	}
	if got, _ := os.ReadFile(filepath.Join(folder, "cosmos")); string(got) != "new server" {
		t.Error("the new version is not installed")
	}

	// three crashes still undo
	folder = oldInstall(t, newReleaseZip(t))
	before := snapshot(t, folder)
	installNew(t, folder, newVersion, f)
	for i := 0; i < 5; i++ {
		MarkCleanStop(folder, newVersion, readTrialFile(t, folder).Run)
		launcherTrial(folder, f.env())
	}
	for i := 0; i < TrialStarts; i++ {
		launcherTrial(folder, f.env())
	}
	sameAs(t, "after three crashes", folder, before)
	if !failedIn(folder, newVersion) {
		t.Error("not noted as failed")
	}
}

// The launcher's own SIGTERM while a run starts is a clean stop of that run.
func TestASignalToTheLauncherIsACleanStop(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	f := &fakeEnv{}
	installNew(t, folder, newVersion, f)
	exited := make(chan int, 1)
	previous := exitLauncher
	exitLauncher = func(code int) { exited <- code }
	defer func() {
		exitLauncher = previous
		signal.Reset(syscall.SIGTERM, syscall.SIGINT)
	}()

	armCleanStop(folder, newVersion, 1)
	syscall.Kill(os.Getpid(), syscall.SIGTERM)
	select {
	case code := <-exited:
		if code != 143 {
			t.Errorf("exit code %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the signal was not handled")
	}
	if trial := readTrialFile(t, folder); trial.CleanRun != 1 {
		t.Errorf("state %+v", trial)
	}
	launcherTrial(folder, f.env())
	if trial := readTrialFile(t, folder); trial.Failures != 0 || trial.Run != 2 {
		t.Errorf("the cut-short start was counted: %+v", trial)
	}
}

func TestATrialThatTimedOutSaysSo(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	f := &fakeEnv{}
	installNew(t, folder, newVersion, f)
	for i := 0; i < TrialStarts; i++ {
		run := readTrialFile(t, folder).Run
		if !timeOut(folder, run) {
			t.Fatal("no time-out noted")
		}
		MarkCleanStop(folder, newVersion, run) // the SIGTERM of the watchdog is no clean stop
		launcherTrial(folder, f.env())
	}
	if len(f.paused) != 0 {
		t.Errorf("paused after a time-out: %v", f.paused)
	}
	if note, _ := TakeRollbackNote(folder); note.Reason != RollbackTimeout || !note.Restored {
		t.Errorf("note %+v", note)
	}
}

func TestAProvenUpdateStays(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	f := &fakeEnv{}
	installNew(t, folder, newVersion, f)

	if err := ProveTrial(folder, oldVersion, 1); err == nil {
		t.Error("another version ended the trial")
	}
	if err := ProveTrial(folder, newVersion, 2); err == nil {
		t.Error("another start ended the trial")
	}
	if err := ProveTrial(folder, newVersion, 1); err != nil {
		t.Fatal(err)
	}
	if TrialPending(folder) {
		t.Error("still pending after the proof")
	}
	for i := 0; i < 5; i++ {
		launcherTrial(folder, f.env())
	}
	if got, _ := os.ReadFile(filepath.Join(folder, "cosmos")); string(got) != "new server" || f.spawned != 1 || failedIn(folder, newVersion) {
		t.Error("a proven version was touched")
	}

	// a start the watchdog ended cannot prove itself any more
	folder = oldInstall(t, newReleaseZip(t))
	installNew(t, folder, newVersion, f)
	timeOut(folder, 1)
	if err := ProveTrial(folder, newVersion, 1); err == nil || noTrial(folder) {
		t.Error("a start whose time was up proved itself")
	}
}

// The proof and the watchdog's time-out race: exactly one wins, and a proven
// trial is never written again.
func TestTheProofAndTheTimeOutNeverBothWin(t *testing.T) {
	folder := oldInstall(t, nil)
	for i := 0; i < 200; i++ {
		writeTrial(folder, Trial{State: stateTrial, NewVersion: newVersion, PreviousVersion: oldVersion, Run: 1})
		proofs, timeOuts := make(chan bool, 1), make(chan bool, 1)
		go func() { proofs <- ProveTrial(folder, newVersion, 1) == nil }()
		go func() { timeOuts <- timeOut(folder, 1) }()
		proven, ended := <-proofs, <-timeOuts
		if proven == ended {
			t.Fatalf("round %d: both or neither won", i)
		}
		if proven && !noTrial(folder) {
			t.Fatalf("round %d: a proven trial was written again", i)
		}
		if ended && readTrialFile(t, folder).KilledRun != 1 {
			t.Fatalf("round %d: the time-out was not noted", i)
		}
		os.Remove(trialPath(folder))
	}
}

func TestDamagedKeptFilesAreNotPutBack(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	f := &fakeEnv{}
	installNew(t, folder, newVersion, f)
	os.WriteFile(filepath.Join(folder, previousFolder, "static", "app.js"), []byte("tampered"), 0600)
	during := snapshot(t, folder)
	for i := 0; i < TrialStarts; i++ {
		launcherTrial(folder, f.env())
	}
	sameAs(t, "with damaged kept files", folder, during)
	note, _ := TakeRollbackNote(folder)
	if note.Restored || note.Reason != RollbackDamaged || failedIn(folder, newVersion) || !noTrial(folder) {
		t.Errorf("note %+v, failed %v", note, readFailed(folder))
	}
	if !strings.Contains(f.out.String(), "could NOT go back") {
		t.Errorf("printed %s", f.out.String())
	}
}

// Review test B: the restore fails halfway (a stand-in for a full disk).
// The server is not started on the mix, the restore is owed and finished
// at a later start.
func TestARestoreThatFailsHalfwayIsFinishedLater(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	before := snapshot(t, folder)
	f := &fakeEnv{}
	installNew(t, folder, newVersion, f)
	block := filepath.Join(folder, "static", "app.js.rollback", "x")
	os.MkdirAll(block, 0755)
	for i := 1; i < TrialStarts; i++ {
		launcherTrial(folder, f.env())
	}
	if launcherTrial(folder, f.env()) {
		t.Fatal("the server may start on a half restored folder")
	}
	trial := readTrialFile(t, folder)
	if trial.State != stateRestoring || trial.Purpose != purposeRollback || failedIn(folder, newVersion) || RollbackNoted(folder) {
		t.Fatalf("state %+v, failed %v, note %v", trial, readFailed(folder), RollbackNoted(folder))
	}
	if _, err := os.Lstat(filepath.Join(folder, "static", "new", "chunk.js")); err == nil {
		t.Error("the added files were not removed first")
	}
	// no new zip is taken meanwhile, and the refusal is told
	os.WriteFile(filepath.Join(folder, StagedZip), newReleaseZip(t), 0600)
	if keepReplaced(folder, newVersion, &f.out) || !strings.Contains(TakeLauncherRefusal(folder), "being installed or undone") {
		t.Error("an update was taken while a restore is owed")
	}
	// a later start, with room again
	os.RemoveAll(filepath.Join(folder, "static", "app.js.rollback"))
	again := 0
	for !launcherTrial(folder, f.env()) && again < 3 {
		again++
	}
	sameAs(t, "after the restore was finished", folder, before)
	if !noTrial(folder) || !failedIn(folder, newVersion) || !RollbackNoted(folder) {
		t.Error("the finished restore did not say so")
	}
}

// Review test A: the rollback is cut short after meta.json is back. The
// next start finishes it; it does not take the folder for "installed by
// other means".
func TestARollbackCutShortAfterMetaJSONIsBackIsFinished(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	before := snapshot(t, folder)
	f := &fakeEnv{}
	installNew(t, folder, newVersion, f)
	writeTrial(folder, Trial{State: stateRestoring, Purpose: purposeRollback, NewVersion: newVersion, PreviousVersion: oldVersion, Failures: 3, Run: 3, Touched: true})
	kept, _ := os.ReadFile(filepath.Join(folder, previousFolder, "meta.json"))
	os.WriteFile(filepath.Join(folder, "meta.json"), kept, 0644) // put back, then the power went
	if !launcherTrial(folder, f.env()) {
		t.Fatal(f.out.String())
	}
	sameAs(t, "after the cut-short rollback", folder, before)
	if !failedIn(folder, newVersion) || !RollbackNoted(folder) || !noTrial(folder) {
		t.Error("the version that failed is not noted")
	}
}

// A meta.json that cannot be read never ends a trial.
func TestAnUnreadableMetaJSONDoesNotEndATrial(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	before := snapshot(t, folder)
	f := &fakeEnv{}
	installNew(t, folder, newVersion, f)
	os.WriteFile(filepath.Join(folder, "meta.json"), []byte("{"), 0644)
	if !TrialPending(folder) {
		t.Error("the server ends the trial")
	}
	for i := 0; i < TrialStarts; i++ {
		launcherTrial(folder, f.env())
	}
	sameAs(t, "after going back", folder, before)
}

// Review test C: the unzip fails, the undo fails twice. The copy kept is
// never touched, the half folder never runs, and the undo finishes once
// there is room.
func TestAnUndoThatFailsTwiceKeepsTheCopy(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	before := snapshot(t, folder)
	f := &fakeEnv{}
	keepReplaced(folder, newVersion, &f.out)
	keptSum, _ := fileSHA256(filepath.Join(folder, previousFolder, "cosmos"), MaxZip)
	os.WriteFile(filepath.Join(folder, "cosmos"), []byte("new ser"), 0755) // half written, then ENOSPC
	os.MkdirAll(filepath.Join(folder, "cosmos.rollback", "x"), 0755)

	if undoInstall(folder, errors.New("no space left on device"), f.env()) {
		t.Fatal("the server may start after a failed undo")
	}
	if launcherTrial(folder, f.env()) {
		t.Fatal("the server may start after the second failed undo")
	}
	trial := readTrialFile(t, folder)
	if trial.State != stateRestoring || trial.Purpose != purposeUndo {
		t.Fatalf("state %+v", trial)
	}
	if sum, _ := fileSHA256(filepath.Join(folder, previousFolder, "cosmos"), MaxZip); sum != keptSum {
		t.Fatal("the copy kept was changed")
	}
	os.RemoveAll(filepath.Join(folder, "cosmos.rollback"))
	if !launcherTrial(folder, f.env()) {
		t.Fatal(f.out.String())
	}
	sameAs(t, "after the undo", folder, before)
	if reason := TakeLauncherRefusal(folder); !strings.Contains(reason, "no space left on device") || !strings.Contains(reason, "put back") {
		t.Errorf("the server is told %q", reason)
	}
	if _, err := os.Lstat(filepath.Join(folder, StagedZip)); err == nil {
		t.Error("the zip that could not be unpacked is still there")
	}
}

// A full disk: the state cannot even be written. The restore goes on all
// the same (it frees room itself) and finishes.
func TestAnUndoGoesOnWhenTheStateCannotBeWritten(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	before := snapshot(t, folder)
	f := &fakeEnv{}
	keepReplaced(folder, newVersion, &f.out)
	os.WriteFile(filepath.Join(folder, "cosmos"), []byte("new ser"), 0755)
	os.MkdirAll(trialPath(folder)+".tmp/x", 0755) // every write of the state fails
	if !undoInstall(folder, errors.New("no space left on device"), f.env()) {
		t.Fatal(f.out.String())
	}
	os.RemoveAll(trialPath(folder) + ".tmp")
	sameAs(t, "after the undo", folder, before)
	if !noTrial(folder) || !strings.Contains(TakeLauncherRefusal(folder), "no space left on device") {
		t.Errorf("not finished or not told: %s", f.out.String())
	}

	// the same at a later start, from "installing"
	folder = oldInstall(t, newReleaseZip(t))
	before = snapshot(t, folder)
	keepReplaced(folder, newVersion, &f.out)
	os.WriteFile(filepath.Join(folder, "cosmos"), []byte("new ser"), 0755)
	os.MkdirAll(trialPath(folder)+".tmp/x", 0755)
	if !launcherTrial(folder, f.env()) {
		t.Fatal(f.out.String())
	}
	os.RemoveAll(trialPath(folder) + ".tmp")
	sameAs(t, "after the cut-short unzip", folder, before)
	if !noTrial(folder) {
		t.Error("the state is left")
	}
}

func TestAFailedUnzipIsUndoneAtOnce(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	before := snapshot(t, folder)
	f := &fakeEnv{}
	keepReplaced(folder, newVersion, &f.out)
	os.WriteFile(filepath.Join(folder, "cosmos"), []byte(""), 0755)
	os.WriteFile(filepath.Join(folder, "cosmos-launcher.updated"), []byte("half"), 0755)
	if !undoInstall(folder, errors.New("no space left on device"), f.env()) {
		t.Fatal(f.out.String())
	}
	sameAs(t, "after undoing", folder, before)
	if !noTrial(folder) || !strings.Contains(TakeLauncherRefusal(folder), "no space left on device") {
		t.Error("the undo is not finished or not told")
	}
}

func TestAnInterruptedUnzipIsUndoneAtTheNextStart(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	before := snapshot(t, folder)
	f := &fakeEnv{}
	keepReplaced(folder, newVersion, &f.out)
	os.WriteFile(filepath.Join(folder, "cosmos"), []byte("new ser"), 0755)
	os.MkdirAll(filepath.Join(folder, "static", "new"), 0755)
	os.WriteFile(filepath.Join(folder, "static", "new", "chunk.js"), []byte("new"), 0644)

	if !launcherTrial(folder, f.env()) {
		t.Fatal(f.out.String())
	}
	sameAs(t, "after undoing", folder, before)
	if f.spawned != 0 || !noTrial(folder) || failedIn(folder, newVersion) || RollbackNoted(folder) {
		t.Error("an interrupted unzip was taken for a failed version")
	}
	if _, err := os.Stat(filepath.Join(folder, StagedZip)); err == nil {
		t.Error("the zip is tried again at once (a full disk would loop)")
	}
	if reason := TakeLauncherRefusal(folder); !strings.Contains(reason, "cut short") || !strings.Contains(reason, "put back") {
		t.Errorf("the server is told %q", reason)
	}
	os.WriteFile(filepath.Join(folder, StagedZip), newReleaseZip(t), 0600)
	if !keepReplaced(folder, newVersion, &f.out) {
		t.Error("the update cannot be installed later")
	}
}

func TestNotEnoughRoomTouchesNothing(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	// an earlier update's copy, which is replaced and so counts as free room
	os.MkdirAll(filepath.Join(folder, previousFolder), 0700)
	os.WriteFile(filepath.Join(folder, previousFolder, "manifest.json"), []byte("{}"), 0600)
	os.WriteFile(filepath.Join(folder, previousFolder, "cosmos"), make([]byte, 1000), 0600)
	before := snapshot(t, folder)
	previous := freeSpace
	defer func() { freeSpace = previous }()

	freeSpace = func(string) (uint64, error) { return 10 << 20, nil }
	out := &bytes.Buffer{}
	if keepReplaced(folder, newVersion, out) {
		t.Fatal("kept without room")
	}
	sameAs(t, "refused for want of room", folder, before)
	if got, _ := os.ReadFile(filepath.Join(folder, previousFolder, "cosmos")); len(got) != 1000 {
		t.Error("the earlier copy kept was touched")
	}
	want := "not enough free space in " + folder + ": 65 MiB needed, 11 MiB free"
	if reason := TakeLauncherRefusal(folder); reason != want {
		t.Errorf("told %q, want %q", reason, want)
	}
	if !strings.Contains(out.String(), want) || !noTrial(folder) {
		t.Errorf("printed %q", out.String())
	}

	// the margin: at least 64 MiB, 10 %, the largest file
	if spaceMargin(1<<30, 1<<20) != (1<<30)/10 || spaceMargin(1<<20, 100<<20) != 100<<20 || spaceMargin(0, 0) != 64<<20 {
		t.Error("margin")
	}
	freeSpace = func(string) (uint64, error) { return 1 << 30, nil }
	os.WriteFile(filepath.Join(folder, StagedZip), newReleaseZip(t), 0600)
	if !keepReplaced(folder, newVersion, out) {
		t.Errorf("refused with room: %s", out.String())
	}
}

func TestNothingIsInstalledWhenTheFilesCannotBeKept(t *testing.T) {
	cases := map[string]func(t *testing.T, folder string){
		"a link in the way": func(t *testing.T, folder string) {
			os.Remove(filepath.Join(folder, "cosmos"))
			os.Symlink("/etc/hostname", filepath.Join(folder, "cosmos"))
		},
		"a linked folder in the way": func(t *testing.T, folder string) {
			os.RemoveAll(filepath.Join(folder, "static"))
			os.Symlink(t.TempDir(), filepath.Join(folder, "static"))
		},
		"a folder where a file goes": func(t *testing.T, folder string) {
			os.Remove(filepath.Join(folder, "cosmos"))
			os.Mkdir(filepath.Join(folder, "cosmos"), 0755)
		},
		"an update that writes the updater's state": func(t *testing.T, folder string) {
			os.WriteFile(filepath.Join(folder, StagedZip), zipOf(t, zipEntry{name: "c/cosmos", content: "x"}, zipEntry{name: "c/" + failedName, content: ""}), 0600)
		},
		"an update that writes into the kept files": func(t *testing.T, folder string) {
			os.WriteFile(filepath.Join(folder, StagedZip), zipOf(t, zipEntry{name: "c/cosmos", content: "x"}, zipEntry{name: "c/" + previousFolder + "/manifest.json", content: "{}"}), 0600)
		},
		"the installed version is unknown": func(t *testing.T, folder string) {
			os.WriteFile(filepath.Join(folder, "meta.json"), []byte("{}"), 0644)
		},
		"a state that cannot be read": func(t *testing.T, folder string) {
			os.WriteFile(trialPath(folder), []byte("garbage"), 0600)
		},
	}
	for name, prepare := range cases {
		folder := oldInstall(t, newReleaseZip(t))
		prepare(t, folder)
		before := snapshot(t, folder)
		out := &bytes.Buffer{}
		if keepReplaced(folder, newVersion, out) {
			t.Errorf("%s: kept, so the update would be installed", name)
			continue
		}
		if _, err := os.Lstat(filepath.Join(folder, StagedZip)); err == nil {
			t.Errorf("%s: the zip is still there", name)
		}
		if _, err := os.Lstat(filepath.Join(folder, previousFolder)); err == nil {
			t.Errorf("%s: a half kept folder is left", name)
		}
		sameAs(t, name, folder, before)
		if reason := TakeLauncherRefusal(folder); !strings.Contains(reason, "could not be kept") {
			t.Errorf("%s: the server is told %q", name, reason)
		}
		if !strings.Contains(out.String(), "[ERROR] Update NOT installed") {
			t.Errorf("%s: printed %q", name, out.String())
		}
	}
	folder := oldInstall(t, newReleaseZip(t))
	if keepReplaced(folder, "", io.Discard) {
		t.Error("kept without a version")
	}
}

// secondReleaseZip is a later release that also changes nebula, which the
// first update left alone, and no longer has static/new.
const laterVersion = "0.24.0-fork.11"

func secondReleaseZip(t *testing.T) []byte {
	return zipOf(t,
		zipEntry{name: "cosmos-cloud-" + laterVersion + "/"},
		zipEntry{name: "cosmos-cloud-" + laterVersion + "/cosmos", content: "later server", mode: 0755},
		zipEntry{name: "cosmos-cloud-" + laterVersion + "/meta.json", content: `{"version": "` + laterVersion + `"}`},
		zipEntry{name: "cosmos-cloud-" + laterVersion + "/nebula", content: "later nebula", mode: 0755},
		zipEntry{name: "cosmos-cloud-" + laterVersion + "/extra/tool", content: "later tool"},
	)
}

// Finding 5: an install by hand during a trial replaces it; the restore
// point stays the version that was proven.
func TestAManualInstallDuringATrialKeepsTheProvenRestorePoint(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	before := snapshot(t, folder)
	f := &fakeEnv{}
	installNew(t, folder, newVersion, f)
	keptSum, _ := fileSHA256(filepath.Join(folder, previousFolder, "cosmos"), MaxZip)

	// the administrator stops the service and puts a later release there
	MarkCleanStop(folder, newVersion, 1)
	os.WriteFile(filepath.Join(folder, StagedZip), secondReleaseZip(t), 0600)
	if !launcherTrial(folder, f.env()) {
		t.Fatal(f.out.String())
	}
	if !keepReplaced(folder, laterVersion, &f.out) {
		t.Fatalf("refused: %s", f.out.String())
	}
	manifest, _ := readKept(folder)
	if manifest.PreviousVersion != oldVersion || manifest.NewVersion != laterVersion {
		t.Errorf("manifest %s -> %s", manifest.PreviousVersion, manifest.NewVersion)
	}
	if sum, _ := fileSHA256(filepath.Join(folder, previousFolder, "cosmos"), MaxZip); sum != keptSum {
		t.Error("the proven version's copy of cosmos was replaced by the version on trial")
	}
	extract(t, folder)
	beginTrial(folder, f.env())
	os.Remove(filepath.Join(folder, StagedZip))
	if trial := readTrialFile(t, folder); trial.NewVersion != laterVersion || trial.PreviousVersion != oldVersion || trial.Run != 1 || trial.Failures != 0 {
		t.Fatalf("state %+v", trial)
	}
	// it fails too: back to the proven version, not to the one on trial before
	for i := 0; i < TrialStarts; i++ {
		launcherTrial(folder, f.env())
	}
	sameAs(t, "after the later release went back", folder, before)
	if !failedIn(folder, laterVersion) {
		t.Error("not noted as failed")
	}
}

// Finding 6: the version that was proven, installed again, never starts a
// trial, so it can never be noted as failed.
func TestAReinstallOfTheProvenVersionStartsNoTrial(t *testing.T) {
	sameZip := zipOf(t, zipEntry{name: "c/cosmos", content: "old server", mode: 0755}, zipEntry{name: "c/meta.json", content: `{"version": "` + oldVersion + `"}`})
	folder := oldInstall(t, sameZip)
	f := &fakeEnv{}
	installNew(t, folder, oldVersion, f)
	if !noTrial(folder) || f.spawned != 0 {
		t.Error("a reinstall started a trial")
	}
	for i := 0; i < 5; i++ {
		launcherTrial(folder, f.env())
	}
	if failedIn(folder, oldVersion) {
		t.Error("the running version was noted as failed")
	}
}

func TestStaleAndUnreadableTrials(t *testing.T) {
	// a version installed by other means since the trial: the trial is over
	folder := oldInstall(t, nil)
	writeTrial(folder, Trial{State: stateTrial, NewVersion: newVersion, PreviousVersion: oldVersion, Run: 1})
	if TrialPending(folder) || !noTrial(folder) {
		t.Error("a trial of a version that is not installed is pending")
	}
	writeTrial(folder, Trial{State: stateTrial, NewVersion: newVersion, PreviousVersion: oldVersion, Run: 2})
	f := &fakeEnv{}
	if !launcherTrial(folder, f.env()) || !noTrial(folder) || f.spawned != 0 {
		t.Error("the launcher kept a stale trial")
	}

	// an unreadable state: back to the version kept
	folder = oldInstall(t, newReleaseZip(t))
	before := snapshot(t, folder)
	installNew(t, folder, newVersion, f)
	os.WriteFile(trialPath(folder), []byte("garbage"), 0600)
	if !TrialPending(folder) {
		t.Error("an unreadable state lets the launcher be replaced")
	}
	if !launcherTrial(folder, f.env()) {
		t.Fatal(f.out.String())
	}
	sameAs(t, "after an unreadable state", folder, before)
	if !failedIn(folder, newVersion) {
		t.Error("not noted as failed")
	}

	// unreadable state and no copy kept: the server is not started
	folder = oldInstall(t, nil)
	os.WriteFile(trialPath(folder), []byte("garbage"), 0600)
	if launcherTrial(folder, f.env()) {
		t.Error("started with an unreadable state and nothing kept")
	}
}

func TestAFailedVersionIsNeitherStagedNorUnpacked(t *testing.T) {
	public, private := testKey(t)
	amd := testZip(t, "amd64")
	files := map[string]string{ZipName(testVersion, "amd64"): sha256Hex(amd)}
	good := signedFile(testVersion, files, private)

	fetch, asked := serveReleases(t, map[string]release{"v" + testVersion: {SignatureName(testVersion): good, ZipName(testVersion, "amd64"): amd}})
	folder := installFolder(t, "")
	addFailed(folder, testVersion)
	err := Stager{Repo: testRepo, Folder: folder, Arch: "amd64", Current: "0.24.0-fork.9", keys: []ed25519.PublicKey{public}, fetch: fetch}.Stage("v" + testVersion)
	if !errors.Is(err, ErrFailedHere) || len(*asked) != 0 || !strings.Contains(err.Error(), "remove the line "+testVersion+" from "+filepath.Join(folder, failedName)) {
		t.Errorf("staged a failed version: %v, %d requests", err, len(*asked))
	}
	fetch, _ = serveReleases(t, map[string]release{"v" + testVersion: {SignatureName(testVersion): good, ZipName(testVersion, "amd64"): amd}})
	folder = installFolder(t, "")
	addFailed(folder, "0.24.0-fork.8")
	if err := (Stager{Repo: testRepo, Folder: folder, Arch: "amd64", Current: "0.24.0-fork.9", keys: []ed25519.PublicKey{public}, fetch: fetch}).Stage("v" + testVersion); err != nil {
		t.Errorf("refused: %v", err)
	}
	folder = installFolder(t, "")
	writeTrial(folder, Trial{State: stateTrial, NewVersion: "0.24.0-fork.9", PreviousVersion: "0.24.0-fork.8", Run: 1})
	if err := (Stager{Repo: testRepo, Folder: folder, Arch: "amd64", Current: "0.24.0-fork.9", keys: []ed25519.PublicKey{public}, fetch: fetch}).Stage("v" + testVersion); !errors.Is(err, ErrTrialPending) {
		t.Errorf("staged during a trial: %v", err)
	}

	folder = launcherFolder(t, "0.24.0-fork.9", amd, good)
	addFailed(folder, testVersion)
	out := &bytes.Buffer{}
	if launcherAccepts([]ed25519.PublicKey{public}, folder, "amd64", out) {
		t.Fatal("the launcher accepted a failed version")
	}
	if reason := TakeLauncherRefusal(folder); !strings.Contains(reason, "went back by itself") || !strings.Contains(reason, "remove the line "+testVersion) {
		t.Errorf("reason %q", reason)
	}
	accepted = nil
	folder = launcherFolder(t, "0.24.0-fork.9", amd, good)
	if !launcherAccepts([]ed25519.PublicKey{public}, folder, "amd64", io.Discard) || accepted == nil || accepted.Version != testVersion {
		t.Error("the accepted version is not remembered")
	}
	accepted = nil
}

func TestTheWatchdog(t *testing.T) {
	previousWindow, previousGrace, previousPoll := WatchdogWindow, ProofGrace, watchdogPoll
	defer func() { WatchdogWindow, ProofGrace, watchdogPoll = previousWindow, previousGrace, previousPoll }()
	watchdogPoll = 10 * time.Millisecond
	ended := 0
	end := func(string, io.Writer) { ended++ }
	folder := t.TempDir()
	trialAt := func(run int) {
		writeTrial(folder, Trial{State: stateTrial, NewVersion: newVersion, PreviousVersion: oldVersion, Run: run})
	}

	// no answer in time: ended, and the time-out noted
	WatchdogWindow, ProofGrace = 100*time.Millisecond, time.Minute
	trialAt(2)
	watchdog(folder, io.Discard, end)
	if trial := readTrialFile(t, folder); ended != 1 || trial.TimedOut != 1 || trial.KilledRun != 2 {
		t.Errorf("ended %d, trial %+v", ended, trial)
	}

	// answered but no proof within the grace: ended too
	ended = 0
	WatchdogWindow, ProofGrace = time.Minute, 100*time.Millisecond
	trialAt(1)
	go func() { time.Sleep(50 * time.Millisecond); MarkAnswered(folder, newVersion, 1) }()
	begun := time.Now()
	watchdog(folder, io.Discard, end)
	if ended != 1 || time.Since(begun) > 20*time.Second {
		t.Errorf("an answered start without proof: ended %d after %v", ended, time.Since(begun))
	}

	// From here the limits are far longer than the steps take: a watchdog
	// that waits for its end instead of stopping early fails the test.
	WatchdogWindow, ProofGrace = time.Minute, time.Minute
	early := 20 * time.Second
	for name, change := range map[string]func(){
		"proven":              func() { ProveTrial(folder, newVersion, 1) },
		"another start began": func() { trialAt(2) },
		"went back": func() {
			writeTrial(folder, Trial{State: stateRestoring, Purpose: purposeRollback, NewVersion: newVersion, PreviousVersion: oldVersion})
		},
		"answered, then proven": func() { MarkAnswered(folder, newVersion, 1); ProveTrial(folder, newVersion, 1) },
	} {
		ended = 0
		trialAt(1)
		go func() { time.Sleep(50 * time.Millisecond); change() }()
		begun := time.Now()
		watchdog(folder, io.Discard, end)
		if ended != 0 || time.Since(begun) > early {
			t.Errorf("%s: ended %d after %v", name, ended, time.Since(begun))
		}
	}
	os.Remove(trialPath(folder))
	watchdog(folder, io.Discard, end)
	if ended != 0 {
		t.Error("ended without a trial")
	}
	if RunWatchdog([]string{"cosmos-launcher"}) || RunWatchdog([]string{"cosmos-launcher", "--other"}) || RunWatchdog([]string{"cosmos-launcher", WatchdogArg, "x"}) {
		t.Error("an ordinary start was taken for the watchdog")
	}
}

// helperEnv makes the test binary act as a server or as the watchdog (see
// TestMain).
const helperEnv = "FORKRELEASE_TEST_HELPER"

func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "server": // a server that hangs; SIGTERM ends it
		time.Sleep(time.Hour)
		os.Exit(0)
	case "stubborn": // a server that hangs and ignores SIGTERM
		signal.Ignore(syscall.SIGTERM)
		time.Sleep(time.Hour)
		os.Exit(0)
	case "watchdog":
		WatchdogWindow, watchdogPoll, watchdogGrace = 300*time.Millisecond, 20*time.Millisecond, 500*time.Millisecond
		os.Setenv(helperEnv, "")
		if !RunWatchdog(os.Args) {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func copyProgram(t *testing.T, to string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := copyFile(self, to, 0755); err != nil {
		t.Fatal(err)
	}
}

// The real watchdog, started as a detached process of its own, ends a real
// hanging server of the folder: asked, or forced when it does not listen.
func TestARealWatchdogEndsAHangingServer(t *testing.T) {
	for _, kind := range []string{"server", "stubborn"} {
		folder := t.TempDir()
		copyProgram(t, filepath.Join(folder, "cosmos"))
		copyProgram(t, filepath.Join(folder, "cosmos-launcher"))
		writeTrial(folder, Trial{State: stateTrial, NewVersion: newVersion, PreviousVersion: oldVersion, Run: 1})

		server := exec.Command(filepath.Join(folder, "cosmos"))
		server.Env = append(os.Environ(), helperEnv+"="+kind)
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- server.Wait() }()

		os.Setenv(helperEnv, "watchdog")
		startWatchdog(filepath.Join(folder, "cosmos-launcher"), folder, io.Discard)
		os.Setenv(helperEnv, "")

		select {
		case <-done:
		case <-time.After(30 * time.Second):
			server.Process.Kill()
			t.Fatalf("%s: the watchdog did not end the server", kind)
		}
		if state := server.ProcessState.Sys().(syscall.WaitStatus); !state.Signaled() ||
			(kind == "server" && state.Signal() != syscall.SIGTERM) || (kind == "stubborn" && state.Signal() != syscall.SIGKILL) {
			t.Errorf("%s: ended by %v", kind, state)
		}
		if trial := readTrialFile(t, folder); trial.TimedOut != 1 || trial.KilledRun != 1 {
			t.Errorf("%s: state %+v", kind, trial)
		}
	}
}

// Finding 9: a release that crashes at once reaches the start that goes
// back long before systemd's start limit.
func TestTheStartLimitIsNeverReached(t *testing.T) {
	unit, err := os.ReadFile("../index.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"RestartSec=" + strconv.Itoa(int(unitRestartSec/time.Second)),
		"StartLimitInterval=" + strconv.Itoa(int(unitStartLimitInterval/time.Second)), "StartLimitBurst=" + strconv.Itoa(unitStartLimitBurst)} {
		if !strings.Contains(string(unit), line+"\n") {
			t.Errorf("the unit in src/index.go no longer has %s: recompute the margin", line)
		}
	}
	// start 1 installs and crashes at once; every later start counts the
	// crash and pauses, up to the one that goes back
	starts := []time.Duration{0}
	launcherTime := time.Duration(0)
	for failed := 1; failed <= TrialStarts; failed++ {
		starts = append(starts, starts[len(starts)-1]+launcherTime+unitRestartSec)
		launcherTime = crashPause
	}
	most := 0
	for i := range starts {
		in := 0
		for _, s := range starts[i:] {
			if s-starts[i] < unitStartLimitInterval {
				in++
			}
		}
		if in > most {
			most = in
		}
	}
	if most > unitStartLimitBurst-2 {
		t.Errorf("starts at %v: %d within %v, the limit is %d", starts, most, unitStartLimitInterval, unitStartLimitBurst)
	}
	if RestorePause < unitStartLimitInterval {
		t.Error("a restore that keeps failing would reach the start limit")
	}
}

func TestTheCopyKeptGoesAfterTheProof(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	f := &fakeEnv{}
	installNew(t, folder, newVersion, f)
	if PruneKept(folder, time.Now().Add(100*time.Hour)) {
		t.Error("removed during the trial")
	}
	ProveTrial(folder, newVersion, 1)
	if PruneKept(folder, time.Now().Add(time.Hour)) {
		t.Error("removed an hour after the proof")
	}
	if !PruneKept(folder, time.Now().Add(KeptFor+time.Minute)) {
		t.Fatal("not removed after KeptFor")
	}
	if _, err := os.Lstat(filepath.Join(folder, previousFolder)); err == nil {
		t.Error("still there")
	}
	// never without a proof
	folder = oldInstall(t, newReleaseZip(t))
	installNew(t, folder, newVersion, f)
	os.Remove(trialPath(folder))
	if PruneKept(folder, time.Now().Add(1000*time.Hour)) {
		t.Error("removed without a proof")
	}
}

func TestTheFailedListIsBounded(t *testing.T) {
	folder := t.TempDir()
	for i := 1; i <= 30; i++ {
		addFailed(folder, "0.24.0-fork."+strconv.Itoa(i))
	}
	addFailed(folder, "0.24.0-fork.30")
	list := readFailed(folder)
	if len(list) != failedKept || list[0] != "0.24.0-fork.11" || list[len(list)-1] != "0.24.0-fork.30" {
		t.Errorf("list %v", list)
	}
	os.WriteFile(filepath.Join(folder, failedName), []byte("garbage\n../x\n0.24.0-fork.5\n"+strings.Repeat("x", 5000)), 0600)
	if failedIn(folder, "0.24.0-fork.5") {
		t.Error("an oversized list was read")
	}
	os.WriteFile(filepath.Join(folder, failedName), []byte("garbage\n../x\n0.24.0-fork.5\n"), 0600)
	if !failedIn(folder, "v0.24.0-fork.5") || failedIn(folder, "garbage") {
		t.Error("the list is not read line by line")
	}
	previous := executableFolder
	executableFolder = func() (string, error) { return folder, nil }
	defer func() { executableFolder = previous }()
	if !FailedHere("v0.24.0-fork.5") || FailedHere("v0.24.0-fork.6") {
		t.Error("FailedHere")
	}
}

func TestTheWatchdogFindsOnlyThisFoldersServer(t *testing.T) {
	folder := t.TempDir()
	other := t.TempDir()
	proc := t.TempDir()
	previous := procRoot
	procRoot = proc
	defer func() { procRoot = previous }()

	process := func(pid string, comm string, exe string) {
		os.MkdirAll(filepath.Join(proc, pid), 0755)
		os.WriteFile(filepath.Join(proc, pid, "comm"), []byte(comm+"\n"), 0644)
		os.Symlink(exe, filepath.Join(proc, pid, "exe"))
	}
	process("100", "cosmos", filepath.Join(folder, "cosmos"))          // this folder's server
	process("101", "cosmos", filepath.Join(other, "cosmos"))           // another install's
	process("102", "bash", filepath.Join(folder, "cosmos"))            // renamed
	process("103", "cosmos", filepath.Join(folder, "cosmos-launcher")) // the launcher
	process("104", "cosmos", filepath.Join(folder, "cosmos")+" (deleted)")
	process("1", "cosmos", filepath.Join(folder, "cosmos"))
	process(strconv.Itoa(os.Getpid()), "cosmos", filepath.Join(folder, "cosmos"))
	os.MkdirAll(filepath.Join(proc, "self"), 0755)

	if got := serverProcesses(folder); len(got) != 1 || got[0] != 100 {
		t.Errorf("found %v, want [100]", got)
	}
}

func TestTheRollbackNoteIsReadSafely(t *testing.T) {
	folder := t.TempDir()
	raw, _ := json.Marshal(map[string]interface{}{"failed": "0.24.0-fork.10\n\x1b[31m" + strings.Repeat("x", 100), "back": "", "reason": "rm -rf", "starts": -4, "minutes": 1 << 40, "restored": true})
	os.WriteFile(filepath.Join(folder, rollbackName), raw, 0600)
	note, found := TakeRollbackNote(folder)
	if !found || strings.ContainsAny(note.Failed, "\n\x1b") || len(note.Failed) > maxVersionLength || note.Back != "unknown" || note.Reason != RollbackStopped || note.Starts != TrialStarts || note.Minutes != 15 {
		t.Errorf("note %+v", note)
	}
	os.WriteFile(filepath.Join(folder, rollbackName), []byte(strings.Repeat("x", 5000)), 0600)
	if _, found := TakeRollbackNote(folder); found || RollbackNoted(folder) {
		t.Error("an oversized note is passed on or left")
	}
	os.Symlink(filepath.Join(folder, "elsewhere"), filepath.Join(folder, rollbackName))
	os.WriteFile(filepath.Join(folder, "elsewhere"), raw, 0600)
	if _, found := TakeRollbackNote(folder); found {
		t.Error("a note that is a link was read")
	}
}
