package snapshots

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMountBelowIn(t *testing.T) {
	points := []string{"/", "/proc", "/storage", "/storage/cosmos/app/media", "/srv/data.before-restore-2026-11-10_030000/disk", "/mnt/with space/sub"}
	cases := []struct{ dir, real, want string }{
		{"/storage/cosmos/app", "/storage/cosmos/app", "/storage/cosmos/app/media"},
		{"/storage/cosmos", "/storage/cosmos", "/storage/cosmos/app/media"},
		{"/srv/data.before-restore-2026-11-10_030000", "/srv/data.before-restore-2026-11-10_030000", "/srv/data.before-restore-2026-11-10_030000/disk"},
		// the folder is reached through a link: the mount table knows the real path
		{"/data/app", "/storage/cosmos/app", "/storage/cosmos/app/media"},
		{"/mnt/with space", "/mnt/with space", "/mnt/with space/sub"},
		// a mount point itself is not "below" itself (mountPoint answers for that)
		{"/storage/cosmos/app/media", "/storage/cosmos/app/media", ""},
		// a name that only starts the same
		{"/storage/cosmos/ap", "/storage/cosmos/ap", ""},
		{"/storage/cosmos/app/med", "/storage/cosmos/app/med", ""},
		{"/home/user/files", "/home/user/files", ""},
	}
	for _, c := range cases {
		if got := mountBelowIn(points, c.dir, c.real); got != c.want {
			t.Errorf("below %s (really %s): got %q, want %q", c.dir, c.real, got, c.want)
		}
	}
	// the real mount table: nothing is mounted inside a fresh folder, and /proc is inside /
	if got := findMountBelow(t.TempDir()); got != "" {
		t.Errorf("a fresh folder has %s mounted inside it", got)
	}
}

// A folder with a disk mounted somewhere inside it can be renamed, and the
// mount goes with it: a restore must not move it aside.
func TestRestoreRefusesAFolderWithAMountInside(t *testing.T) {
	cfg, source := fixture(t)
	write(t, filepath.Join(source, "file.txt"), "content", 0644)
	write(t, filepath.Join(source, "disk", "on-the-disk.txt"), "precious", 0644)
	dir, _ := snap(t, cfg, source, day(1))
	id := filepath.Base(dir)
	useConfig(t, cfg)

	realMountBelow := mountBelow
	mountBelow = func(dir string) string {
		if dir == source {
			return filepath.Join(source, "disk")
		}
		return ""
	}
	t.Cleanup(func() { mountBelow = realMountBelow })

	err := Restore(source, id)
	if err == nil || !strings.Contains(err.Error(), "mounted inside") {
		t.Fatalf("a folder with a mount inside was restored: %v", err)
	}
	if !strings.Contains(err.Error(), filepath.Join(source, "disk")) {
		t.Errorf("the refusal does not say what is mounted: %v", err)
	}
	// nothing moved
	if read(t, filepath.Join(source, "disk", "on-the-disk.txt")) != "precious" {
		t.Error("the folder was touched")
	}
	if kept, _ := filepath.Glob(source + ".before-restore-*"); len(kept) != 0 {
		t.Errorf("the folder was moved aside: %v", kept)
	}
	if staging, _ := filepath.Glob(source + ".restoring-*"); len(staging) != 0 {
		t.Errorf("a restore was started: %v", staging)
	}
}

// Deleting a kept folder walks into everything below it: with a disk mounted
// inside, that would empty the disk.
func TestRemoveKeptRefusesAFolderWithAMountInside(t *testing.T) {
	cfg, source := fixture(t)
	write(t, filepath.Join(source, "file.txt"), "content", 0644)
	older, newer := keptFixture(t, source)
	useConfig(t, cfg)
	base := filepath.Dir(source)
	write(t, filepath.Join(base, older, "sub", "disk", "on-the-disk.txt"), "precious", 0644)

	realMountBelow := mountBelow
	mountBelow = func(dir string) string {
		if dir == filepath.Join(base, older) {
			return filepath.Join(base, older, "sub", "disk")
		}
		return ""
	}
	t.Cleanup(func() { mountBelow = realMountBelow })

	err := RemoveKept(source, older)
	if err == nil || !strings.Contains(err.Error(), "mounted inside") {
		t.Fatalf("a kept folder with a mount inside was deleted: %v", err)
	}
	if read(t, filepath.Join(base, older, "sub", "disk", "on-the-disk.txt")) != "precious" || read(t, filepath.Join(base, older, "file.txt")) != "older" {
		t.Error("files of the kept folder are gone")
	}

	// a kept folder without a mount is deleted as before
	if err := RemoveKept(source, newer); err != nil {
		t.Fatalf("a kept folder without a mount: %v", err)
	}
	if exists(filepath.Join(base, newer)) {
		t.Error("the kept folder without a mount is still there")
	}
}
