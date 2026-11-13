package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/azukaar/cosmos-server/src/forkrelease"
	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: the server's side of signed releases. The rules themselves
// are tested in package forkrelease; here: that the update code goes through
// them, and what the administrators are told. Nothing here talks to GitHub.

func readSource(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestUpdatesGoThroughTheSignatureCheck(t *testing.T) {
	cron := readSource(t, "CRON.go")
	apply := cron[strings.Index(cron, "func applyServerUpdate("):]
	apply = apply[:strings.Index(apply, "\nfunc ")]
	staged := strings.Index(apply, "stageVerifiedUpdate(dlPath, updates)")
	exits := strings.Index(apply, "os.Exit(0)")
	if staged < 0 || exits < 0 || staged > exits {
		t.Fatal("applyServerUpdate does not stage the update through stageVerifiedUpdate before it exits")
	}
	if strings.Contains(strings.ReplaceAll(apply, "(upstream: utils.DownloadFileToLocation(dlPath, url))", ""), "DownloadFile") {
		t.Error("applyServerUpdate downloads by itself")
	}

	// both ways to an update end in applyServerUpdate, and nothing else
	// writes the file the launcher installs
	system := readSource(t, "system.go")
	if !strings.Contains(system, "applyServerUpdate(updates, useBeta)") || !strings.Contains(cron, "applyServerUpdate(updates, useBeta)") {
		t.Error("the nightly update or \"update now\" no longer calls applyServerUpdate")
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		source := readSource(t, name)
		if strings.Contains(source, "cosmos-update") && name != "CRON.go" && name != "update.go" {
			t.Errorf("%s names the update file: every writer of it must go through stageVerifiedUpdate", name)
		}
		if name != "fork_update_verify.go" && strings.Contains(source, "forkrelease.Stager") {
			t.Errorf("%s stages updates by itself", name)
		}
	}
	update := readSource(t, "update.go")
	if strings.Contains(update, "http.Get(") || strings.Contains(update, "http.Client") || strings.Contains(update, "os.Create") || strings.Contains(update, "WriteFile") {
		t.Error("update.go fetches or writes outside the updater's rules")
	}

	// the launcher: the check comes before the release list is asked and before anything is unpacked
	launcher := readSource(t, filepath.Join("launcher", "launcher.go"))
	main := launcher[strings.Index(launcher, "func main()"):]
	check := strings.Index(main, "if !forkrelease.LauncherAccepts(currentFolder, runtime.GOARCH) {")
	list := strings.Index(main, "GetLatestVersion(")
	unpack := strings.Index(main, "unzip(")
	if check < 0 || list < 0 || unpack < 0 || check > list || check > unpack {
		t.Fatal("the launcher does not check the signature before it unpacks")
	}
	refusal := main[check:list]
	if !strings.Contains(refusal, "return") {
		t.Error("the launcher goes on after a refused update")
	}
	if strings.Count(main, "unzip(") != 1 {
		t.Error("the launcher unpacks in more than one place")
	}

	index := readSource(t, "index.go")
	if !strings.Contains(index, "reportLauncherRefusal()") {
		t.Error("a refusal by the launcher is not reported at start")
	}
}

func captureRejections(t *testing.T) (notifications *[]string, events func() []utils.SecurityEvent) {
	t.Helper()
	var mu sync.Mutex
	sent := []string{}
	recorded := []utils.SecurityEvent{}
	previous := notifyRejectedUpdate
	notifyRejectedUpdate = func(summary string) {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, summary)
	}
	utils.SetSecurityRecorder(func(event utils.SecurityEvent) {
		mu.Lock()
		defer mu.Unlock()
		recorded = append(recorded, event)
	}, func() {})
	rejectedUpdatesMu.Lock()
	rejectedUpdates = map[string]bool{}
	rejectedUpdatesMu.Unlock()
	t.Cleanup(func() {
		notifyRejectedUpdate = previous
		utils.SetSecurityRecorder(nil, nil)
	})
	return &sent, func() []utils.SecurityEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]utils.SecurityEvent(nil), recorded...)
	}
}

// A build whose version cannot be told, or that holds no release key, places
// nothing and sends no request; the refusal is reported once.
func TestAnUpdateThatCannotBeVerifiedIsNotPlacedAndIsReported(t *testing.T) {
	notifications, events := captureRejections(t)
	folder := t.TempDir()
	dlPath := filepath.Join(folder, forkrelease.StagedZip)

	for i := 0; i < 3; i++ {
		err := stageVerifiedUpdate(dlPath, &VersionInfo{Version: "v0.24.0-fork.999999", AMDURL: "https://example.com/evil-amd64.zip", ARMURL: "https://example.com/evil-arm64.zip"})
		if err == nil {
			t.Fatal("an update was staged by a test build")
		}
	}
	entries, _ := os.ReadDir(folder)
	if len(entries) != 0 {
		t.Errorf("files were placed: %v", entries)
	}
	if len(*notifications) != 1 || !strings.Contains((*notifications)[0], "v0.24.0-fork.999999") {
		t.Errorf("notifications: %q, want one that names the release", *notifications)
	}
	recorded := events()
	if len(recorded) != 3 || recorded[0].Kind != utils.SecServerUpdateRejected || recorded[0].Target != "v0.24.0-fork.999999" ||
		recorded[0].Details["by"] != "server" || recorded[0].Details["reason"] == "" {
		t.Errorf("security record: %+v", recorded)
	}

	// not a file the launcher looks for, or no release at all
	if err := stageVerifiedUpdate(filepath.Join(folder, "other.zip"), &VersionInfo{Version: "v0.24.0-fork.999999"}); err == nil {
		t.Error("staged under another name")
	}
	if err := stageVerifiedUpdate(dlPath, nil); err == nil {
		t.Error("staged without a release")
	}
}

func TestARefusalByTheLauncherIsReportedAtStart(t *testing.T) {
	notifications, events := captureRejections(t)
	execPath, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	note := filepath.Join(filepath.Dir(execPath), "cosmos-update.rejected")
	if err := os.WriteFile(note, []byte("the signature file is not signed with a release key of this build\n"), 0600); err != nil {
		t.Skip("cannot write next to the test binary: ", err)
	}
	defer os.Remove(note)

	reportLauncherRefusal()
	reportLauncherRefusal() // the note is gone: nothing more

	if len(*notifications) != 1 || !strings.Contains((*notifications)[0], "not signed with a release key") {
		t.Errorf("notifications: %q", *notifications)
	}
	recorded := events()
	if len(recorded) != 1 || recorded[0].Kind != utils.SecServerUpdateRejected || recorded[0].Details["by"] != "launcher" {
		t.Errorf("security record: %+v", recorded)
	}
	if _, err := os.Stat(note); !errors.Is(err, os.ErrNotExist) {
		t.Error("the launcher's note is still there")
	}
}
