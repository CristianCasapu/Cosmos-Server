package utils

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func writeString(content string) func(io.Writer) error {
	return func(w io.Writer) error {
		_, err := io.WriteString(w, content)
		return err
	}
}

func folderEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func checkFile(t *testing.T, path string, content string, mode os.FileMode) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("%s holds %q, want %q", path, got, content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != mode {
		t.Errorf("%s has mode %o, want %o", path, info.Mode().Perm(), mode)
	}
}

func TestWriteFileAtomicReplacesContentAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cosmos.config.json")

	// a new file gets the mode asked for, whatever the umask
	old := syscall.Umask(0077)
	err := writeFileAtomic(path, 0640, writeString("first"))
	syscall.Umask(old)
	if err != nil {
		t.Fatal(err)
	}
	checkFile(t, path, "first", 0640)

	// an existing file is replaced and keeps its own mode, not the default one
	before, _ := os.Stat(path)
	if err := writeFileAtomic(path, 0600, writeString("second, and longer")); err != nil {
		t.Fatal(err)
	}
	checkFile(t, path, "second, and longer", 0640)
	after, _ := os.Stat(path)
	if os.SameFile(before, after) {
		t.Error("the file was rewritten in place, not replaced by a new one")
	}

	// shorter content leaves nothing of the previous one
	if err := writeFileAtomic(path, 0600, writeString("3")); err != nil {
		t.Fatal(err)
	}
	checkFile(t, path, "3", 0640)

	if names := folderEntries(t, dir); len(names) != 1 || names[0] != "cosmos.config.json" {
		t.Errorf("folder holds %v, want only the file", names)
	}
}

func TestWriteFileAtomicFailedWriteKeepsOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cosmos.config.json")
	if err := os.WriteFile(path, []byte("good config"), 0600); err != nil {
		t.Fatal(err)
	}

	broken := errors.New("disk full")
	err := writeFileAtomic(path, 0600, func(w io.Writer) error {
		io.WriteString(w, "half a conf") // the crash happens after part of the content
		return broken
	})
	if !errors.Is(err, broken) {
		t.Fatalf("error = %v, want the write error", err)
	}
	checkFile(t, path, "good config", 0600)
	if names := folderEntries(t, dir); len(names) != 1 {
		t.Errorf("folder holds %v, want only the file", names)
	}

	// what a crash left behind does not get in the way of the next save, and is removed
	if err := os.WriteFile(path+".tmp", []byte("half a conf"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, 0600, writeString("next config")); err != nil {
		t.Fatal(err)
	}
	checkFile(t, path, "next config", 0600)
	if names := folderEntries(t, dir); len(names) != 1 {
		t.Errorf("folder holds %v, want only the file", names)
	}
}

func TestWriteFileAtomicFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	link := filepath.Join(dir, "cosmos.config.json")
	if err := os.WriteFile(real, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	if err := writeFileAtomic(link, 0600, writeString("new")); err != nil {
		t.Fatal(err)
	}
	checkFile(t, real, "new", 0600)
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Error("the symbolic link was replaced by a file")
	}
}

func TestWriteFileAtomicWhenRenameIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cosmos.config.json")
	if err := os.WriteFile(path, []byte("good config"), 0600); err != nil {
		t.Fatal(err)
	}
	defer func() { renameFile = os.Rename }()

	// a file that is a mount point of its own cannot be replaced: written in place, as before
	renameFile = func(from, to string) error {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.EBUSY}
	}
	if err := writeFileAtomic(path, 0600, writeString("in place")); err != nil {
		t.Fatal(err)
	}
	checkFile(t, path, "in place", 0600)

	// any other failure must not touch the file
	renameFile = func(from, to string) error {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.ENOSPC}
	}
	if err := writeFileAtomic(path, 0600, writeString("lost")); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("error = %v, want ENOSPC", err)
	}
	checkFile(t, path, "in place", 0600)

	if names := folderEntries(t, dir); len(names) != 1 {
		t.Errorf("folder holds %v, want only the file", names)
	}
}

func TestSaveConfigTofileReplacesTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cosmos.config.json")
	t.Setenv("CONFIG_FILE", path)
	if err := os.WriteFile(path, []byte(`{"MaintenanceTime": "01:00", "left": "over from a much longer file"}`), 0600); err != nil {
		t.Fatal(err)
	}

	config := Config{}
	config.MaintenanceTime = "04:30"
	SaveConfigTofile(config)

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	saved := Config{}
	if err := json.Unmarshal(content, &saved); err != nil {
		t.Fatalf("the saved file is not valid JSON: %v", err)
	}
	if saved.MaintenanceTime != "04:30" {
		t.Errorf("MaintenanceTime = %q, want 04:30", saved.MaintenanceTime)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
	if names := folderEntries(t, dir); len(names) != 1 {
		t.Errorf("folder holds %v, want only the config file", names)
	}

	// the lock is released: a second save must not block
	SaveConfigTofile(config)
}
