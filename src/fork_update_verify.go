package main

// Fork addition: the server installs only releases signed with the fork's
// release key (package forkrelease has the rules and the keys).
//
// applyServerUpdate is the one place that puts an update next to the binary
// for the launcher; the nightly update and "update now" both go through it.
// It no longer downloads whatever address the release list names: the
// release's signature file and zip are fetched from this fork's repository on
// github.com, checked, and only then placed. When anything is wrong nothing
// is placed, the server keeps running its version and the administrators are
// told. The launcher checks again by itself before it unpacks; what it
// refused at this start is reported here too.

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/azukaar/cosmos-server/src/forkrelease"
	"github.com/azukaar/cosmos-server/src/utils"
)

// stageVerifiedUpdate is the hook of applyServerUpdate: nil when the release
// lies verified at dlPath, ready for the launcher.
func stageVerifiedUpdate(dlPath string, updates *VersionInfo) error {
	if updates == nil || filepath.Base(dlPath) != forkrelease.StagedZip {
		return errors.New("no release to install")
	}
	// A launcher the last update brought is put in place first: the next
	// start must not unpack with an older one.
	cleanUpUpdateFiles()

	err := forkrelease.Stager{
		Repo:    updateRepo,
		Folder:  filepath.Dir(dlPath),
		Arch:    runtime.GOARCH,
		Current: GetCosmosVersion(),
	}.Stage(updates.Version)
	if err != nil {
		reportRejectedUpdate(updates.Version, err.Error(), "server")
		return err
	}
	utils.Log("Update: release " + updates.Version + " is signed with a release key of this fork and has the signed checksum")
	return nil
}

var (
	rejectedUpdatesMu sync.Mutex
	rejectedUpdates   = map[string]bool{}
)

// notifyRejectedUpdate tells the administrators. A variable so tests can
// replace it.
var notifyRejectedUpdate = func(summary string) {
	utils.WriteNotification(utils.Notification{
		Recipient: "admin",
		Title:     "header.notification.title.updateRejected",
		Message:   "header.notification.message.updateRejected",
		Vars:      summary,
		Level:     "error",
	})
}

// reportRejectedUpdate says, in the log, in the security record and (once per
// release and reason while the server runs) in a notification, that an
// update was not installed.
func reportRejectedUpdate(version string, reason string, by string) {
	version, reason = forkrelease.SafeText(version), forkrelease.SafeText(reason)
	utils.Error("Update: release "+version+" was NOT installed ("+by+"): "+reason, nil)
	utils.RecordSecurityEvent(utils.SecurityEvent{
		Kind:    utils.SecServerUpdateRejected,
		Target:  version,
		Details: map[string]string{"reason": reason, "by": by},
	})

	rejectedUpdatesMu.Lock()
	seen := rejectedUpdates[version+"\x00"+reason]
	if len(rejectedUpdates) < 100 {
		rejectedUpdates[version+"\x00"+reason] = true
	}
	rejectedUpdatesMu.Unlock()
	if seen {
		return
	}
	summary := reason
	if version != "" {
		summary = version + ": " + reason
	}
	notifyRejectedUpdate(summary)
}

// reportLauncherRefusal is the hook at start: when the launcher refused the
// update it found, say so.
func reportLauncherRefusal() {
	execPath, err := os.Executable()
	if err != nil {
		return
	}
	folder := filepath.Dir(execPath)
	// cleanUpUpdateFiles (upstream) removed the zip just before; its signature file goes with it
	forkrelease.ClearStaged(folder)
	if reason := forkrelease.TakeLauncherRefusal(folder); reason != "" {
		reportRejectedUpdate("", reason, "launcher")
	}
}
