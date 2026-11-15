package forkrelease

// Fork addition: the way back by hand (cosmos-update.restore.sh) never takes
// a file name for an option of cp, chmod, mv or rm.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTheRestoreScriptTakesNoFileNameForAnOption(t *testing.T) {
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum is not installed")
	}
	folder := t.TempDir()
	old := []byte("old -n file")
	sum := sha256.Sum256(old)
	if err := os.MkdirAll(filepath.Join(folder, previousFolder), 0700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(folder, previousFolder, "-n"), old, 0600)
	os.WriteFile(filepath.Join(folder, "-n"), []byte("new"), 0644)
	os.WriteFile(filepath.Join(folder, "-rf"), []byte("added"), 0644)
	manifest := keptManifest{Format: 1,
		Replaced: []keptFile{{Path: "-n", SHA256: hex.EncodeToString(sum[:]), Mode: 0644, Size: int64(len(old))}},
		Added:    []string{"-rf"}, AddedDirs: []string{}}
	script := restoreScript(folder, Trial{NewVersion: "0.24.0-fork.2", PreviousVersion: "0.24.0-fork.1", Purpose: purposeRollback}, manifest)
	path := filepath.Join(t.TempDir(), "restore.sh")
	os.WriteFile(path, []byte(script), 0600)
	if out, err := exec.Command("sh", path).CombinedOutput(); err != nil {
		t.Fatalf("the script failed: %v\n%s\n%s", err, out, script)
	}
	if got, _ := os.ReadFile(filepath.Join(folder, "-n")); string(got) != string(old) {
		t.Fatalf("-n is %q, want the old file", got)
	}
	if info, err := os.Stat(filepath.Join(folder, "-n")); err != nil || info.Mode().Perm() != 0644 {
		t.Fatalf("-n mode %v %v, want 0644", info, err)
	}
	if _, err := os.Lstat(filepath.Join(folder, "-rf")); !os.IsNotExist(err) {
		t.Fatal("the added file -rf is still there")
	}
}
