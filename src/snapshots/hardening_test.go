package snapshots

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A restore that fails takes back only the folder it created. When that
// name leads to another folder by then, nothing is removed and the admin is
// told which name to check.
func TestFailedRestoreLeavesAFolderThatTookTheCopysName(t *testing.T) {
	cfg, source := fixture(t)
	useConfig(t, cfg)
	write(t, filepath.Join(source, "sub", "a.txt"), "a", 0640)
	write(t, filepath.Join(source, "sub", "b.txt"), "b", 0640)
	first, _ := snap(t, cfg, source, day(12))
	id := filepath.Base(first)

	other := filepath.Join(filepath.Dir(source), "other")
	write(t, filepath.Join(other, "keep.txt"), "not to be removed", 0644)
	staging := source + ".restoring-" + id
	done := false
	betweenLookAndOpen = func(rel string) {
		if rel != "sub/b.txt" || done {
			return
		}
		done = true
		if err := os.Rename(staging, staging+".moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(other, staging); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { betweenLookAndOpen = nil }()

	err := Restore(source, id)
	if !done {
		t.Fatal("the moment to swap the folder never came")
	}
	if err == nil {
		t.Fatal("the restore reported success although its copy was replaced")
	}
	if got := read(t, filepath.Join(staging, "keep.txt")); got != "not to be removed" {
		t.Errorf("the folder that took the copy's name: %q", got)
	}
	if !strings.Contains(err.Error(), filepath.Base(staging)) || !strings.Contains(err.Error(), "left as it is") {
		t.Errorf("the message does not say which folder was left: %v", err)
	}
}
