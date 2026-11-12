package snapshots

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// useConfig makes cfg the saved settings for the duration of a test.
func useConfig(t *testing.T, cfg utils.SnapshotsConfig) {
	t.Helper()
	utils.LoadBaseMainConfig(utils.Config{Snapshots: cfg})
	t.Cleanup(func() { utils.LoadBaseMainConfig(utils.Config{}) })
}

// describe lists everything below root with its kind, mode and content, to
// tell whether anything changed.
func describe(t *testing.T, root string) []string {
	t.Helper()
	lines := []string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		line := fmt.Sprintf("%s %v", strings.TrimPrefix(path, root), info.Mode())
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			line += " -> " + target
		case info.Mode().IsRegular():
			data, _ := os.ReadFile(path)
			line += fmt.Sprintf(" %x", sha256.Sum256(data))
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return lines
}

func TestRestore(t *testing.T) {
	cfg, source := fixture(t)
	useConfig(t, cfg)
	old := time.Date(2021, 6, 1, 8, 0, 0, 0, time.Local)

	write(t, filepath.Join(source, "config.yml"), "version 1", 0640)
	os.Chtimes(filepath.Join(source, "config.yml"), old, old)
	write(t, filepath.Join(source, "db", "data.bin"), "data 1", 0600)
	os.Chmod(filepath.Join(source, "db"), 0700)
	os.Symlink("db", filepath.Join(source, "current"))
	os.Chmod(source, 0750)

	first, _ := snap(t, cfg, source, day(12))
	id := filepath.Base(first)
	snapshotBefore := describe(t, first)

	// the folder goes wrong after the snapshot
	write(t, filepath.Join(source, "config.yml"), "version 2, broken", 0644)
	os.Remove(filepath.Join(source, "db", "data.bin"))
	write(t, filepath.Join(source, "added.txt"), "added later", 0644)

	if err := Restore(source, id); err != nil {
		t.Fatal(err)
	}

	// the old content is back, with its modes and times
	if got := read(t, filepath.Join(source, "config.yml")); got != "version 1" {
		t.Errorf("config.yml after the restore: %q", got)
	}
	if info, _ := os.Stat(filepath.Join(source, "config.yml")); info.Mode().Perm() != 0640 || !info.ModTime().Equal(old) {
		t.Errorf("config.yml after the restore: mode %o, time %v", info.Mode().Perm(), info.ModTime())
	}
	if got := read(t, filepath.Join(source, "db", "data.bin")); got != "data 1" {
		t.Errorf("db/data.bin after the restore: %q", got)
	}
	if info, _ := os.Stat(filepath.Join(source, "db")); info.Mode().Perm() != 0700 {
		t.Errorf("db after the restore: mode %o", info.Mode().Perm())
	}
	if info, _ := os.Stat(source); info.Mode().Perm() != 0750 {
		t.Errorf("the folder after the restore: mode %o", info.Mode().Perm())
	}
	if target, err := os.Readlink(filepath.Join(source, "current")); err != nil || target != "db" {
		t.Errorf("the symbolic link after the restore: %q, %v", target, err)
	}
	if exists(filepath.Join(source, "added.txt")) {
		t.Error("a file added after the snapshot is in the restored folder")
	}
	if exists(filepath.Join(source, metaName)) {
		t.Error("the snapshot's record was restored into the folder")
	}

	// the version that was there is kept next to it
	kept, _ := filepath.Glob(source + ".before-restore-*")
	if len(kept) != 1 {
		t.Fatalf("versions kept: %v", kept)
	}
	if got := read(t, filepath.Join(kept[0], "config.yml")); got != "version 2, broken" {
		t.Errorf("config.yml in the kept version: %q", got)
	}
	if !exists(filepath.Join(kept[0], "added.txt")) || exists(filepath.Join(kept[0], "db", "data.bin")) {
		t.Error("the kept version is not the folder as it was before the restore")
	}
	result := GetStatus().LastRestore
	if result == nil || result.Error != "" || result.KeptAs != kept[0] || result.Source != source || result.Snapshot != id || result.Files != 3 {
		t.Errorf("result of the restore: %+v", result)
	}
	if leftovers, _ := filepath.Glob(source + ".restoring-*"); len(leftovers) != 0 {
		t.Errorf("left behind: %v", leftovers)
	}

	// real copies: working in the restored folder cannot reach the snapshot
	for _, name := range []string{"config.yml", "db/data.bin"} {
		if sameFile(t, filepath.Join(source, name), filepath.Join(first, name)) {
			t.Errorf("%s in the restored folder is a link to the snapshot", name)
		}
	}
	file, err := os.OpenFile(filepath.Join(source, "config.yml"), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	file.WriteString(" edited after the restore")
	file.Close()
	os.Chmod(filepath.Join(source, "db", "data.bin"), 0666)
	if got := read(t, filepath.Join(first, "config.yml")); got != "version 1" {
		t.Errorf("editing the restored file changed the snapshot: %q", got)
	}
	if after := describe(t, first); !reflect.DeepEqual(after, snapshotBefore) {
		t.Errorf("the snapshot changed:\n%v\n%v", snapshotBefore, after)
	}
	os.Chmod(filepath.Join(source, "db", "data.bin"), 0600)

	// the next snapshot does not copy again what the restore brought back unchanged
	second, meta := snap(t, cfg, source, day(13))
	if !sameFile(t, filepath.Join(first, "db", "data.bin"), filepath.Join(second, "db", "data.bin")) {
		t.Error("a restored, unchanged file was copied again by the next snapshot")
	}
	if meta.BytesCopied != int64(len("version 1 edited after the restore")) {
		t.Errorf("the next snapshot copied %d bytes", meta.BytesCopied)
	}

	// restoring again keeps one more version
	if err := Restore(source, id); err != nil {
		t.Fatal(err)
	}
	if kept, _ := filepath.Glob(source + ".before-restore-*"); len(kept) != 2 {
		t.Errorf("versions kept after a second restore: %v", kept)
	}
	if got := read(t, filepath.Join(source, "config.yml")); got != "version 1" {
		t.Errorf("config.yml after the second restore: %q", got)
	}
}

func TestRestoreAFolderThatIsGone(t *testing.T) {
	cfg, source := fixture(t)
	useConfig(t, cfg)
	write(t, filepath.Join(source, "sub", "file.txt"), "content", 0644)
	first, _ := snap(t, cfg, source, day(12))

	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	if err := Restore(source, filepath.Base(first)); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(source, "sub", "file.txt")); got != "content" {
		t.Errorf("after the restore: %q", got)
	}
	if kept, _ := filepath.Glob(source + ".before-restore-*"); len(kept) != 0 {
		t.Errorf("there was nothing to keep, yet: %v", kept)
	}
	if result := GetStatus().LastRestore; result == nil || result.KeptAs != "" || result.Error != "" {
		t.Errorf("result: %+v", result)
	}
}

func TestRestoreRefuses(t *testing.T) {
	cfg, source := fixture(t)
	base := filepath.Dir(source)
	write(t, filepath.Join(source, "file.txt"), "content", 0644)
	write(t, filepath.Join(source, "sub", "deep.txt"), "deep", 0644)
	first, _ := snap(t, cfg, source, day(12))
	id := filepath.Base(first)

	// a folder with a snapshot on the disk, but not one of the configured ones
	other := filepath.Join(base, "other")
	write(t, filepath.Join(other, "file.txt"), "other", 0644)
	otherCfg := cfg
	otherCfg.Sources = []string{other}
	snap(t, otherCfg, other, day(12))
	// and a folder whose name would be used if an id could leave its place
	write(t, filepath.Join(cfg.Destination, "x", "file.txt"), "not a snapshot", 0644)

	useConfig(t, cfg)
	before := describe(t, base)

	cases := []struct{ name, source, id string }{
		{"a folder that is not configured", other, id},
		{"no folder", "", id},
		{"the folder with a trailing slash", source + "/", id},
		{"a folder inside the configured one", filepath.Join(source, "sub"), id},
		{"the folder above the configured one", base, id},
		{"a relative folder", "data", id},
		{"a folder with ..", source + "/../other", id},
		{"a folder with .. that comes back", source + "/../data", id},
		{"the destination", cfg.Destination, id},
		{"the root", "/", id},
		{"an id that leaves the folder", source, "../x"},
		{"an id that leaves the destination", source, "../../other"},
		{"an absolute path as id", source, "/etc"},
		{"the snapshot's full path as id", source, first},
		{"no id", source, ""},
		{"a dot", source, "."},
		{"two dots", source, ".."},
		{"an id with a path behind it", source, id + "/sub"},
		{"an id with a path before it", source, "./" + id},
		{"an unfinished snapshot", source, id + ".partial"},
		{"an id with a newline", source, id + "\n"},
		{"an id with a NUL", source, id + "\x00"},
		{"an id that is no date", source, "2026-13-45_999999"},
		{"a snapshot that does not exist", source, "2020-01-01_000000"},
	}
	for _, c := range cases {
		err := Restore(c.source, c.id)
		if err == nil {
			t.Errorf("%s: restored", c.name)
		} else if err == ErrBusy {
			t.Errorf("%s: refused as busy instead of as invalid", c.name)
		}
		if err := StartRestore(c.source, c.id); err == nil {
			t.Errorf("%s: restore started", c.name)
		}
	}

	// Cosmos' own folder, or one that holds it, or one inside it
	realFolder := utils.CONFIGFOLDER
	for _, own := range []string{source + "/", source, filepath.Join(source, "sub") + "/"} {
		utils.CONFIGFOLDER = own
		err := Restore(source, id)
		if err == nil || !strings.Contains(err.Error(), "Stop Cosmos") {
			t.Errorf("Cosmos' folder is %s: %v", own, err)
		}
	}
	utils.CONFIGFOLDER = base + "/"
	if err := Restore(source, id); err == nil {
		t.Error("a folder inside Cosmos' own folder was restored")
	}
	utils.CONFIGFOLDER = realFolder

	// a folder a disk is mounted on
	realMountPoint := mountPoint
	mountPoint = func(dir string) bool { return dir == source }
	if err := Restore(source, id); err == nil || !strings.Contains(err.Error(), "mounted") {
		t.Errorf("a mount point: %v", err)
	}
	mountPoint = realMountPoint

	// while a run or another restore is in progress
	runMu.Lock()
	if err := Restore(source, id); err != ErrBusy {
		t.Errorf("while busy: %v", err)
	}
	if err := StartRestore(source, id); err != ErrBusy {
		t.Errorf("while busy, in the background: %v", err)
	}
	runMu.Unlock()

	// nothing above changed anything
	if after := describe(t, base); !reflect.DeepEqual(after, before) {
		t.Errorf("a refused restore changed something:\nbefore %v\nafter  %v", before, after)
	}
	if runMu.TryLock() {
		runMu.Unlock()
	} else {
		t.Fatal("a refused restore kept the lock")
	}
	if GetStatus().Running {
		t.Error("a refused restore left the status at running")
	}

	// what an interrupted restore left is not overwritten or removed
	leftover := source + ".restoring-" + id
	write(t, filepath.Join(leftover, "half.txt"), "half", 0644)
	if err := Restore(source, id); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("with a leftover of an interrupted restore: %v", err)
	}
	if got := read(t, filepath.Join(leftover, "half.txt")); got != "half" {
		t.Error("the leftover was touched")
	}
	os.RemoveAll(leftover)

	// the folder has become something else
	os.Rename(source, source+".aside")
	write(t, source, "now a file", 0644)
	if err := Restore(source, id); err == nil {
		t.Error("a file was replaced by a restore")
	}
	os.Remove(source)
	os.Symlink(source+".aside", source)
	if err := Restore(source, id); err == nil {
		t.Error("a symbolic link was replaced by a restore")
	}
	os.Remove(source)
	os.Rename(source+".aside", source)

	// no destination set
	useConfig(t, utils.SnapshotsConfig{Sources: []string{source}})
	if err := Restore(source, id); err == nil {
		t.Error("restored without a destination")
	}

	if kept, _ := filepath.Glob(source + ".before-restore-*"); len(kept) != 0 {
		t.Errorf("a refused restore moved the folder aside: %v", kept)
	}
	if got := read(t, filepath.Join(source, "file.txt")); got != "content" {
		t.Errorf("the folder after the refused restores: %q", got)
	}
}

func TestRestoreRefusesAFolderThatHoldsTheSnapshots(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "data")
	destination := filepath.Join(source, "snapshots")
	os.MkdirAll(destination, 0755)
	write(t, filepath.Join(source, "file.txt"), "content", 0644)
	cfg := utils.SnapshotsConfig{Enabled: true, Destination: destination, Sources: []string{source}}
	useConfig(t, cfg)
	first, _ := snap(t, cfg, source, day(12))

	if err := Restore(source, filepath.Base(first)); err == nil || !strings.Contains(err.Error(), "by hand") {
		t.Errorf("restore of the folder that holds the snapshots: %v", err)
	}
	if !exists(first) || len(List(destination, source)) != 1 {
		t.Error("the snapshots were moved away")
	}
}

func TestMountPoints(t *testing.T) {
	mountinfo := strings.Join([]string{
		`22 28 0:21 / /sys rw,nosuid,nodev,noexec,relatime shared:7 - sysfs sysfs rw`,
		`36 28 8:3 / /storage rw,relatime shared:1 - ext4 /dev/sda3 rw`,
		`40 36 8:3 /a /mnt/with\040space rw,relatime - ext4 /dev/sda3 rw`,
		`41 36 8:3 /b /mnt/back\134slash rw - ext4 /dev/sda3 rw`,
		`too short`,
		``,
	}, "\n")
	want := []string{"/sys", "/storage", "/mnt/with space", `/mnt/back\slash`}
	if got := mountPoints(mountinfo); !reflect.DeepEqual(got, want) {
		t.Errorf("mount points: %q, want %q", got, want)
	}
	for text, want := range map[string]string{`plain`: `plain`, `a\040b`: `a b`, `end\04`: `end\04`, `\999`: `\999`, `\`: `\`} {
		if got := unescapeMount(text); got != want {
			t.Errorf("unescapeMount(%q) = %q, want %q", text, got, want)
		}
	}

	// an ordinary folder is not one; where the kernel lists its mounts, /proc is
	dir := filepath.Join(t.TempDir(), "plain")
	os.Mkdir(dir, 0755)
	if isMountPoint(dir) {
		t.Errorf("%s is taken for a mount point", dir)
	}
	if isMountPoint(filepath.Join(dir, "missing")) {
		t.Error("a folder that does not exist is taken for a mount point")
	}
	if _, err := os.Stat("/proc/self/mountinfo"); err == nil && !isMountPoint("/proc") {
		t.Error("/proc is not recognised as a mount point")
	}
}
