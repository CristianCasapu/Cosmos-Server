package forkrelease

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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

func readTrialFile(t *testing.T, folder string) Trial {
	t.Helper()
	trial, err := readTrial(folder)
	if err != nil {
		t.Fatalf("the trial file: %v", err)
	}
	return trial
}

type spawnCounter struct{ n int }

func (s *spawnCounter) spawn(string, io.Writer) { s.n++ }

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
func TestAnUpdateThatNeverProvesItselfGoesBack(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	before := snapshot(t, folder)
	out := &bytes.Buffer{}

	if !keepReplaced(folder, newVersion, out) {
		t.Fatalf("not kept: %s", out.String())
	}
	if info, err := os.Lstat(filepath.Join(folder, previousFolder)); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("the kept folder: %v %v", info, err)
	}
	manifest, err := readKept(folder)
	if err != nil {
		t.Fatal(err)
	}
	replaced := []string{}
	for _, f := range manifest.Replaced {
		replaced = append(replaced, f.Path)
		if info, _ := os.Stat(filepath.Join(folder, previousFolder, f.Path)); info.Mode().Perm() != 0600 {
			t.Errorf("kept copy of %s has mode %v", f.Path, info.Mode().Perm())
		}
		if sum, _ := fileSHA256(filepath.Join(folder, f.Path), MaxZip); sum != f.SHA256 {
			t.Errorf("%s: the recorded SHA-256 is not the file's", f.Path)
		}
	}
	sort.Strings(replaced)
	if strings.Join(replaced, ",") != "cosmos,meta.json,static/app.js" {
		t.Errorf("replaced %v", replaced)
	}
	if strings.Join(manifest.Added, ",") != "cosmos-launcher.updated,static/new/chunk.js" || strings.Join(manifest.AddedDirs, ",") != "static/new" {
		t.Errorf("added %v, dirs %v", manifest.Added, manifest.AddedDirs)
	}
	if manifest.PreviousVersion != oldVersion || manifest.NewVersion != newVersion {
		t.Errorf("versions %s -> %s", manifest.PreviousVersion, manifest.NewVersion)
	}
	if trial := readTrialFile(t, folder); !trial.Installing || trial.Starts != 0 {
		t.Errorf("before the unzip the trial is %+v", trial)
	}

	extract(t, folder)
	os.Remove(filepath.Join(folder, StagedZip))
	spawned := &spawnCounter{}
	beginTrial(folder, out, spawned.spawn)
	if trial := readTrialFile(t, folder); trial.Installing || trial.Starts != 1 || trial.NewVersion != newVersion || trial.PreviousVersion != oldVersion {
		t.Errorf("after the unzip the trial is %+v", trial)
	}
	if spawned.n != 1 {
		t.Errorf("%d watchdogs for the first start", spawned.n)
	}
	if !TrialPending(folder) {
		t.Error("the server does not see the trial")
	}
	if got, _ := os.ReadFile(filepath.Join(folder, "cosmos")); string(got) != "new server" {
		t.Fatal("the update was not unzipped")
	}

	// starts 2 and 3: counted, watched
	for start := 2; start <= TrialStarts; start++ {
		launcherTrial(folder, out, spawned.spawn)
		if trial := readTrialFile(t, folder); trial.Starts != start {
			t.Fatalf("start %d counted as %d", start, trial.Starts)
		}
	}
	if spawned.n != TrialStarts {
		t.Errorf("%d watchdogs for %d starts", spawned.n, TrialStarts)
	}

	// the next start goes back
	launcherTrial(folder, out, spawned.spawn)
	after := snapshot(t, folder)
	if len(after) != len(before) {
		t.Errorf("after going back the folder holds %v, before %v", after, before)
	}
	for name, was := range before {
		if after[name] != was {
			t.Errorf("%s: %q after going back, %q before", name, after[name], was)
		}
	}
	if spawned.n != TrialStarts {
		t.Error("a watchdog was started for the start that went back")
	}
	if _, err := os.Lstat(trialPath(folder)); err == nil {
		t.Error("the trial file is still there")
	}
	if !failedIn(folder, newVersion) || !failedIn(folder, "v"+newVersion) || failedIn(folder, oldVersion) {
		t.Errorf("failed list: %v", readFailed(folder))
	}
	if !RollbackNoted(folder) {
		t.Fatal("no note for the server")
	}
	note, found := TakeRollbackNote(folder)
	if !found || note.Failed != newVersion || note.Back != oldVersion || note.Reason != RollbackStopped || !note.Restored || note.Starts != TrialStarts || note.Minutes != 5 {
		t.Errorf("note %+v", note)
	}
	if _, again := TakeRollbackNote(folder); again {
		t.Error("the note is reported twice")
	}
	if _, err := os.Lstat(filepath.Join(folder, previousFolder, previousManifest)); err != nil {
		t.Error("the kept files are not kept for a manual return")
	}
	for _, line := range []string{"start 2 of 3", "start 3 of 3", "going back to " + oldVersion, newVersion + " is not installed again"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("the launcher did not print %q:\n%s", line, out.String())
		}
	}

	// the next start: nothing pending
	launcherTrial(folder, out, spawned.spawn)
	if spawned.n != TrialStarts || RollbackNoted(folder) {
		t.Error("a start after going back did something")
	}
}

func TestATrialThatTimedOutSaysSo(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	keepReplaced(folder, newVersion, io.Discard)
	extract(t, folder)
	writeTrial(folder, Trial{NewVersion: newVersion, PreviousVersion: oldVersion, Starts: TrialStarts, TimedOut: 1})
	launcherTrial(folder, io.Discard, (&spawnCounter{}).spawn)
	if note, _ := TakeRollbackNote(folder); note.Reason != RollbackTimeout || !note.Restored {
		t.Errorf("note %+v", note)
	}
}

func TestAProvenUpdateStays(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	keepReplaced(folder, newVersion, io.Discard)
	extract(t, folder)
	beginTrial(folder, io.Discard, (&spawnCounter{}).spawn)

	if err := ProveTrial(folder, oldVersion); err == nil {
		t.Error("another version ended the trial")
	}
	if err := ProveTrial(folder, newVersion); err != nil {
		t.Fatal(err)
	}
	if TrialPending(folder) {
		t.Error("still pending after the proof")
	}
	spawned := &spawnCounter{}
	for i := 0; i < 5; i++ {
		launcherTrial(folder, io.Discard, spawned.spawn)
	}
	if got, _ := os.ReadFile(filepath.Join(folder, "cosmos")); string(got) != "new server" || spawned.n != 0 || failedIn(folder, newVersion) {
		t.Error("a proven version was touched")
	}
}

func TestDamagedKeptFilesAreNotPutBack(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	keepReplaced(folder, newVersion, io.Discard)
	extract(t, folder)
	beginTrial(folder, io.Discard, (&spawnCounter{}).spawn)
	os.WriteFile(filepath.Join(folder, previousFolder, "static", "app.js"), []byte("tampered"), 0600)
	writeTrial(folder, Trial{NewVersion: newVersion, PreviousVersion: oldVersion, Starts: TrialStarts})
	during := snapshot(t, folder)

	out := &bytes.Buffer{}
	launcherTrial(folder, out, (&spawnCounter{}).spawn)
	after := snapshot(t, folder)
	for name, was := range during {
		if after[name] != was {
			t.Errorf("%s changed although the kept files did not verify", name)
		}
	}
	note, _ := TakeRollbackNote(folder)
	if note.Restored || note.Reason != RollbackDamaged || failedIn(folder, newVersion) || TrialPending(folder) {
		t.Errorf("note %+v, failed %v, pending %v", note, readFailed(folder), TrialPending(folder))
	}
	if !strings.Contains(out.String(), "could NOT go back") {
		t.Errorf("printed %s", out.String())
	}
}

func TestAnInterruptedUnzipIsUndoneAtTheNextStart(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	before := snapshot(t, folder)
	keepReplaced(folder, newVersion, io.Discard)
	// half the files written, then the power went
	os.WriteFile(filepath.Join(folder, "cosmos"), []byte("new ser"), 0755)
	os.MkdirAll(filepath.Join(folder, "static", "new"), 0755)
	os.WriteFile(filepath.Join(folder, "static", "new", "chunk.js"), []byte("new"), 0644)

	spawned := &spawnCounter{}
	launcherTrial(folder, io.Discard, spawned.spawn)
	after := snapshot(t, folder)
	for name, was := range before {
		if after[name] != was {
			t.Errorf("%s: %q, before %q", name, after[name], was)
		}
	}
	if len(after) != len(before) || spawned.n != 0 || TrialPending(folder) || failedIn(folder, newVersion) || RollbackNoted(folder) {
		t.Errorf("after undoing: %v", after)
	}
	// the zip is still there: the launcher installs it again right after
	if _, err := os.Stat(filepath.Join(folder, StagedZip)); err != nil {
		t.Error("the zip was removed")
	}
	if !keepReplaced(folder, newVersion, io.Discard) {
		t.Error("the update cannot be installed again")
	}
}

func TestAFailedUnzipIsUndoneAtOnce(t *testing.T) {
	folder := oldInstall(t, newReleaseZip(t))
	before := snapshot(t, folder)
	keepReplaced(folder, newVersion, io.Discard)
	os.WriteFile(filepath.Join(folder, "cosmos"), []byte(""), 0755)
	os.WriteFile(filepath.Join(folder, "cosmos-launcher.updated"), []byte("half"), 0755)

	out := &bytes.Buffer{}
	undoInstall(folder, errors.New("no space left on device"), out)
	after := snapshot(t, folder)
	for name, was := range before {
		if after[name] != was {
			t.Errorf("%s: %q, before %q", name, after[name], was)
		}
	}
	if len(after) != len(before) || TrialPending(folder) {
		t.Errorf("after undoing: %v", after)
	}
	if reason := TakeLauncherRefusal(folder); !strings.Contains(reason, "no space left on device") || !strings.Contains(reason, "put back") {
		t.Errorf("the server is told %q", reason)
	}
}

func TestNothingIsInstalledWhenTheFilesCannotBeKept(t *testing.T) {
	cases := map[string]func(t *testing.T, folder string){
		"a link in the way": func(t *testing.T, folder string) {
			os.Remove(filepath.Join(folder, "cosmos"))
			os.Symlink("/etc/hostname", filepath.Join(folder, "cosmos"))
		},
		"a linked folder in the way": func(t *testing.T, folder string) {
			elsewhere := t.TempDir()
			os.RemoveAll(filepath.Join(folder, "static"))
			os.Symlink(elsewhere, filepath.Join(folder, "static"))
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
		if TrialPending(folder) {
			t.Errorf("%s: a trial is pending", name)
		}
		after := snapshot(t, folder)
		for path, was := range before {
			if after[path] != was {
				t.Errorf("%s: %s changed", name, path)
			}
		}
		if reason := TakeLauncherRefusal(folder); !strings.Contains(reason, "could not be kept") {
			t.Errorf("%s: the server is told %q", name, reason)
		}
		if !strings.Contains(out.String(), "[ERROR] Update NOT installed") {
			t.Errorf("%s: printed %q", name, out.String())
		}
	}

	// unknown version (nothing accepted in this run)
	folder := oldInstall(t, newReleaseZip(t))
	if keepReplaced(folder, "", io.Discard) {
		t.Error("kept without a version")
	}
	// a trial is pending: refused, nothing told (it is the same update again)
	folder = oldInstall(t, newReleaseZip(t))
	writeTrial(folder, Trial{NewVersion: newVersion, PreviousVersion: oldVersion, Starts: 1})
	if keepReplaced(folder, newVersion, io.Discard) {
		t.Error("kept while a trial is pending")
	}
	if reason := TakeLauncherRefusal(folder); reason != "" {
		t.Errorf("told %q", reason)
	}
}

func TestStaleAndUnreadableTrials(t *testing.T) {
	// a version installed by other means since the trial: the trial is over
	folder := oldInstall(t, nil)
	writeTrial(folder, Trial{NewVersion: newVersion, PreviousVersion: oldVersion, Starts: 1})
	if TrialPending(folder) {
		t.Error("a trial of a version that is not installed is pending")
	}
	if _, err := os.Lstat(trialPath(folder)); err == nil {
		t.Error("the stale trial was not removed")
	}
	writeTrial(folder, Trial{NewVersion: newVersion, PreviousVersion: oldVersion, Starts: 2})
	spawned := &spawnCounter{}
	launcherTrial(folder, io.Discard, spawned.spawn)
	if _, err := os.Lstat(trialPath(folder)); err == nil || spawned.n != 0 {
		t.Error("the launcher kept a stale trial")
	}

	// an unreadable trial: back to the version kept
	folder = oldInstall(t, newReleaseZip(t))
	before := snapshot(t, folder)
	keepReplaced(folder, newVersion, io.Discard)
	extract(t, folder)
	os.WriteFile(trialPath(folder), []byte("garbage"), 0600)
	if !TrialPending(folder) {
		t.Error("an unreadable trial lets the launcher be replaced")
	}
	launcherTrial(folder, io.Discard, spawned.spawn)
	after := snapshot(t, folder)
	for name, was := range before {
		if after[name] != was {
			t.Errorf("%s: %q, before %q", name, after[name], was)
		}
	}
	if !failedIn(folder, newVersion) {
		t.Error("not noted as failed")
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

func TestAFailedVersionIsNeitherStagedNorUnpacked(t *testing.T) {
	public, private := testKey(t)
	amd := testZip(t, "amd64")
	files := map[string]string{ZipName(testVersion, "amd64"): sha256Hex(amd)}
	good := signedFile(testVersion, files, private)

	// the server
	fetch, asked := serveReleases(t, map[string]release{"v" + testVersion: {SignatureName(testVersion): good, ZipName(testVersion, "amd64"): amd}})
	folder := installFolder(t, "")
	addFailed(folder, testVersion)
	err := Stager{Repo: testRepo, Folder: folder, Arch: "amd64", Current: "0.24.0-fork.9", keys: []ed25519.PublicKey{public}, fetch: fetch}.Stage("v" + testVersion)
	if !errors.Is(err, ErrFailedHere) || len(*asked) != 0 {
		t.Errorf("staged a failed version: %v, %d requests", err, len(*asked))
	}
	// another version still is
	fetch, _ = serveReleases(t, map[string]release{"v" + testVersion: {SignatureName(testVersion): good, ZipName(testVersion, "amd64"): amd}})
	folder = installFolder(t, "")
	addFailed(folder, "0.24.0-fork.8")
	if err := (Stager{Repo: testRepo, Folder: folder, Arch: "amd64", Current: "0.24.0-fork.9", keys: []ed25519.PublicKey{public}, fetch: fetch}).Stage("v" + testVersion); err != nil {
		t.Errorf("refused: %v", err)
	}
	// nothing is staged while a trial is pending
	folder = installFolder(t, "")
	writeTrial(folder, Trial{NewVersion: "0.24.0-fork.9", Starts: 1})
	if err := (Stager{Repo: testRepo, Folder: folder, Arch: "amd64", Current: "0.24.0-fork.9", keys: []ed25519.PublicKey{public}, fetch: fetch}).Stage("v" + testVersion); !errors.Is(err, ErrTrialPending) {
		t.Errorf("staged during a trial: %v", err)
	}

	// the launcher
	folder = launcherFolder(t, "0.24.0-fork.9", amd, good)
	addFailed(folder, testVersion)
	out := &bytes.Buffer{}
	if launcherAccepts([]ed25519.PublicKey{public}, folder, "amd64", out) {
		t.Fatal("the launcher accepted a failed version")
	}
	if reason := TakeLauncherRefusal(folder); !strings.Contains(reason, "went back by itself") {
		t.Errorf("reason %q", reason)
	}
	if zipContent, _, _ := staged(folder); zipContent != nil {
		t.Error("the refused zip is still there")
	}
	// accepted: the version is what KeepReplaced uses
	accepted = nil
	folder = launcherFolder(t, "0.24.0-fork.9", amd, good)
	if !launcherAccepts([]ed25519.PublicKey{public}, folder, "amd64", io.Discard) || accepted == nil || accepted.Version != testVersion {
		t.Error("the accepted version is not remembered")
	}
	accepted = nil
}

func TestTheWatchdog(t *testing.T) {
	previousWindow, previousPoll := WatchdogWindow, watchdogPoll
	defer func() { WatchdogWindow, watchdogPoll = previousWindow, previousPoll }()
	watchdogPoll = 10 * time.Millisecond

	ended := 0
	end := func(string, io.Writer) { ended++ }

	// not proven in time: ended, and the time-out noted
	WatchdogWindow = 100 * time.Millisecond
	folder := t.TempDir()
	writeTrial(folder, Trial{NewVersion: newVersion, PreviousVersion: oldVersion, Starts: 2})
	watchdog(folder, io.Discard, end)
	if trial := readTrialFile(t, folder); ended != 1 || trial.TimedOut != 1 || trial.Starts != 2 {
		t.Errorf("ended %d, trial %+v", ended, trial)
	}

	// From here the window is far longer than the steps take: a watchdog
	// that waits for its end instead of stopping early fails the test, on
	// a slow machine too.
	WatchdogWindow = time.Minute
	early := 20 * time.Second

	// proven meanwhile: nothing ended, stopped early
	ended = 0
	writeTrial(folder, Trial{NewVersion: newVersion, PreviousVersion: oldVersion, Starts: 1})
	go func() { time.Sleep(50 * time.Millisecond); os.Remove(trialPath(folder)) }()
	begun := time.Now()
	watchdog(folder, io.Discard, end)
	if ended != 0 || time.Since(begun) > early {
		t.Errorf("ended %d after %v", ended, time.Since(begun))
	}

	// another start began meanwhile: it watches itself
	ended = 0
	writeTrial(folder, Trial{NewVersion: newVersion, PreviousVersion: oldVersion, Starts: 1})
	go func() {
		time.Sleep(50 * time.Millisecond)
		writeTrial(folder, Trial{NewVersion: newVersion, PreviousVersion: oldVersion, Starts: 2})
	}()
	begun = time.Now()
	watchdog(folder, io.Discard, end)
	if trial := readTrialFile(t, folder); ended != 0 || trial.TimedOut != 0 || time.Since(begun) > early {
		t.Errorf("ended %d, trial %+v, watched %v", ended, trial, time.Since(begun))
	}

	// nothing on trial: nothing to do, at once
	ended = 0
	os.Remove(trialPath(folder))
	begun = time.Now()
	watchdog(folder, io.Discard, end)
	if ended != 0 || time.Since(begun) > early {
		t.Error("ended without a trial")
	}

	if RunWatchdog([]string{"cosmos-launcher"}) || RunWatchdog([]string{"cosmos-launcher", "--other"}) || RunWatchdog([]string{"cosmos-launcher", WatchdogArg, "x"}) {
		t.Error("an ordinary start was taken for the watchdog")
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
	if !found || strings.ContainsAny(note.Failed, "\n\x1b") || len(note.Failed) > maxVersionLength || note.Back != "unknown" || note.Reason != RollbackStopped || note.Starts != TrialStarts || note.Minutes != 5 {
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
