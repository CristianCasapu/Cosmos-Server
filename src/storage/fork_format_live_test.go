package storage

// Formatting for real, against loop devices only. Opt-in, and meant for a
// privileged throwaway container, never a machine whose disks matter:
//
//	COSMOS_FORK_LOOP=1 go test ./src/storage/ -run FormatLive -v
//
// The container needs e2fsprogs, xfsprogs, dosfstools, exfatprogs, parted and
// util-linux, and a devtmpfs on /dev (partition nodes of a loop device only
// appear there). Every command of this test names a loop device the test made
// itself on its own file; anything else is refused before it runs.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var ownLoop = regexp.MustCompile(`^/dev/loop[0-9]+$`)

type liveLoop struct {
	t      *testing.T
	file   string
	device string
}

func newLiveLoop(t *testing.T, dir string, name string) *liveLoop {
	t.Helper()
	file := filepath.Join(dir, name+".img")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(file, 300<<20); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("losetup", "-f", "--show", "-P", file).CombinedOutput()
	if err != nil {
		t.Fatalf("losetup: %v %s", err, out)
	}
	device := strings.TrimSpace(string(out))
	l := &liveLoop{t: t, file: file, device: device}
	t.Cleanup(func() {
		exec.Command("umount", "/mnt/cosmos-forms3-"+name).Run()
		if ownLoop.MatchString(device) {
			exec.Command("losetup", "-d", device).Run()
		}
		os.Remove(file)
	})
	l.mustBeOwn(device)
	return l
}

// mustBeOwn stops the test unless the path is this loop device or one of its
// partitions, backed by the test's own file.
func (l *liveLoop) mustBeOwn(path string) {
	l.t.Helper()
	if !ownLoop.MatchString(l.device) || !(path == l.device || path == l.device+"p1") {
		l.t.Fatalf("refusing to touch %s: not the test's own loop device", path)
	}
	out, err := exec.Command("losetup", "-n", "-O", "BACK-FILE", l.device).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != l.file {
		l.t.Fatalf("refusing to touch %s: backed by %q, not %s", l.device, strings.TrimSpace(string(out)), l.file)
	}
}

func (l *liveLoop) run(name string, args ...string) (string, error) {
	l.t.Helper()
	l.mustBeOwn(args[len(args)-1])
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

func (l *liveLoop) format(path string, fs string) (string, bool) {
	l.t.Helper()
	l.mustBeOwn(path)
	var out bytes.Buffer
	ok := forkFormatStream(&out, func() {}, path, fs, filepath.Base(path))
	return out.String(), ok
}

func (l *liveLoop) fsType(path string) string {
	l.t.Helper()
	out, _ := l.run("blkid", "-s", "TYPE", "-o", "value", path)
	return strings.TrimSpace(out)
}

func TestFormatLive(t *testing.T) {
	if os.Getenv("COSMOS_FORK_LOOP") != "1" {
		t.Skip("formats loop devices for real: COSMOS_FORK_LOOP=1, in a throwaway container only")
	}
	dir := os.Getenv("COSMOS_FORK_LOOP_DIR")
	if dir == "" {
		dir = t.TempDir()
	}

	t.Run("upstream's mkfs over an existing file system fails", func(t *testing.T) {
		l := newLiveLoop(t, dir, "upstream")
		if out, err := l.run("mkfs.ext4", "-q", l.device); err != nil {
			t.Fatalf("first mkfs: %v %s", err, out)
		}
		// what upstream ran for XFS: it fails over an existing file system, and
		// upstream reported that as a success. (mke2fs 1.47 does not ask
		// without a terminal; mkfs.xfs and mkfs.btrfs refuse.)
		out, err := l.run("mkfs", "-t", "xfs", l.device)
		if err == nil {
			t.Fatalf("upstream's mkfs.xfs over ext4 succeeded: %s", out)
		}
		t.Logf("upstream's command: %v: %s", err, lastLines(out, 2))
	})

	t.Run("ext4 over ext4", func(t *testing.T) {
		l := newLiveLoop(t, dir, "ext4")
		if out, err := l.run("mkfs.ext4", "-q", l.device); err != nil {
			t.Fatalf("first mkfs: %v %s", err, out)
		}
		before, _ := l.run("blkid", "-s", "UUID", "-o", "value", l.device)
		text, ok := l.format(l.device, "ext4")
		after, _ := l.run("blkid", "-s", "UUID", "-o", "value", l.device)
		if !ok || !strings.Contains(text, "[OPERATION SUCCEEDED]") || before == after {
			t.Fatalf("ok=%v uuid %q -> %q\n%s", ok, before, after, text)
		}
	})

	t.Run("xfs over ext4, then fat32 and exfat", func(t *testing.T) {
		l := newLiveLoop(t, dir, "xfs")
		if out, err := l.run("mkfs.ext4", "-q", l.device); err != nil {
			t.Fatalf("first mkfs: %v %s", err, out)
		}
		for fs, want := range map[string]string{"xfs": "xfs", "fat32": "vfat", "exfat": "exfat"} {
			text, ok := l.format(l.device, fs)
			if !ok || l.fsType(l.device) != want {
				t.Fatalf("%s: ok=%v type=%q\n%s", fs, ok, l.fsType(l.device), text)
			}
		}
	})

	t.Run("a failure is reported", func(t *testing.T) {
		l := newLiveLoop(t, dir, "fail")
		if out, err := l.run("mkfs.ext4", "-q", l.device); err != nil {
			t.Fatalf("first mkfs: %v %s", err, out)
		}
		// upstream's XFS command line (no -f) over ext4: a real failure of the real tool
		saved := formatCommands["xfs"]
		formatCommands["xfs"] = []string{"mkfs.xfs"}
		defer func() { formatCommands["xfs"] = saved }()
		text, ok := l.format(l.device, "xfs")
		if ok || !strings.Contains(text, "[OPERATION FAILED] Formatting "+l.device+" as xfs failed (exit status") {
			t.Fatalf("a failing mkfs not reported:\n%s", text)
		}
		t.Logf("%s", lastLines(text, 1))
	})

	t.Run("a whole disk gets p1", func(t *testing.T) {
		l := newLiveLoop(t, dir, "disk")
		saved := diskTypeOf
		// lsblk calls a loop device "loop": here it stands for a disk
		diskTypeOf = func(path string) (string, error) { l.mustBeOwn(path); return "disk", nil }
		defer func() { diskTypeOf = saved }()
		text, ok := l.format(l.device, "ext4")
		if !ok || l.fsType(l.device+"p1") != "ext4" {
			t.Fatalf("ok=%v p1 type=%q\n%s", ok, l.fsType(l.device+"p1"), text)
		}
		table, _ := l.run("blkid", "-s", "PTTYPE", "-o", "value", l.device)
		if strings.TrimSpace(table) != "gpt" {
			t.Fatalf("partition table %q", table)
		}
	})

	t.Run("a mounted device is refused", func(t *testing.T) {
		l := newLiveLoop(t, dir, "mounted")
		if out, err := l.run("mkfs.ext4", "-q", l.device); err != nil {
			t.Fatalf("mkfs: %v %s", err, out)
		}
		mnt := "/mnt/cosmos-forms3-mounted"
		os.MkdirAll(mnt, 0700)
		if out, err := exec.Command("mount", l.device, mnt).CombinedOutput(); err != nil {
			t.Fatalf("mount: %v %s", err, out)
		}
		text, ok := l.format(l.device, "ext4")
		if ok || !strings.Contains(text, l.device+" is mounted at "+mnt) {
			t.Fatalf("a mounted device was not refused:\n%s", text)
		}
		if mounted, _ := IsDiskMounted(l.device); !mounted {
			t.Fatal("IsDiskMounted does not see it")
		}
	})
}
