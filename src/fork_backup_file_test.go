package main

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: tests of fork_backup_file.go.

func TestBackupFileIsPrivateAndNeverWrittenThroughALink(t *testing.T) {
	configFolder := t.TempDir()
	output := t.TempDir()
	elsewhere := t.TempDir()

	if err := os.WriteFile(filepath.Join(configFolder, "cosmos.config.json"), []byte(`{"secret":"key"}`), 0600); err != nil {
		t.Fatal(err)
	}
	// somebody who can write in the backup folder points the temporary name at another file
	victim := filepath.Join(elsewhere, "victim.txt")
	if err := os.WriteFile(victim, []byte("do not touch"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(output, "cosmos-backup.temp.zip")); err != nil {
		t.Fatal(err)
	}
	// and the backup of an earlier version is still there, readable by everyone
	if err := os.WriteFile(filepath.Join(output, "cosmos-backup.zip"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	prevFolder, prevConfig := utils.CONFIGFOLDER, utils.GetBaseMainConfig()
	utils.CONFIGFOLDER = configFolder + "/"
	config := utils.Config{}
	config.BackupOutputDir = output
	utils.LoadBaseMainConfig(config)
	// a permissive umask, as under systemd without UMask=
	prevUmask := syscall.Umask(0022)
	t.Cleanup(func() {
		syscall.Umask(prevUmask)
		utils.CONFIGFOLDER = prevFolder
		utils.LoadBaseMainConfig(prevConfig)
	})

	RunBackup()

	if content, err := os.ReadFile(victim); err != nil || string(content) != "do not touch" {
		t.Fatalf("the file the temporary name pointed to was written (%q, %v)", content, err)
	}
	final := filepath.Join(output, "cosmos-backup.zip")
	info, err := os.Lstat(final)
	if err != nil {
		t.Fatalf("no backup was written: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("the backup is not a regular file: %v", info.Mode())
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("the backup is created with mode %o, want 600", perm)
	}
	if _, err := os.Lstat(filepath.Join(output, "cosmos-backup.temp.zip")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the temporary file is still there (%v)", err)
	}

	archive, err := zip.OpenReader(final)
	if err != nil {
		t.Fatalf("the backup cannot be opened: %v", err)
	}
	defer archive.Close()
	found := false
	for _, file := range archive.File {
		if file.Name == "cosmos.config.json" {
			found = true
		}
	}
	if !found {
		t.Error("the configuration is not in the backup")
	}
}

func TestBackupListingSkipsAnEntryThatVanished(t *testing.T) {
	if !skipBackupEntry(nil, os.ErrNotExist) || !skipBackupEntry(nil, nil) {
		t.Error("an entry without file information must be skipped")
	}
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if skipBackupEntry(info, nil) {
		t.Error("a readable entry must not be skipped")
	}
	if !skipBackupEntry(info, os.ErrPermission) {
		t.Error("an entry reported with an error must be skipped")
	}
}
