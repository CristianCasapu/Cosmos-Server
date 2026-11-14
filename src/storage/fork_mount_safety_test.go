package storage

import (
	"os"
	"strings"
	"testing"
)

func TestCheckMountRequest(t *testing.T) {
	good := []struct {
		path, mountpoint, chown string
		net                     bool
	}{
		{"/dev/sdb1", "/mnt/disk1", "", false},
		{"/dev/disk/by-uuid/1234-ABCD", "/mnt/disk1/", "casapu:casapu", false},
		{"/dev/mapper/vg-data", "/var/mnt/data", "1000:1000", false},
		{"nas.example.org:/export/media", "/mnt/media", "", true},
		{"//nas/share", "/mnt/share", "user", true},
	}
	for _, c := range good {
		if err := checkMountRequest(c.path, c.mountpoint, c.chown, c.net); err != nil {
			t.Errorf("refused %+v: %v", c, err)
		}
	}

	bad := []struct {
		name, path, mountpoint, chown string
		net                           bool
	}{
		{"no device", "", "/mnt/a", "", false},
		{"device with a space", "/dev/sdb1 /etc", "/mnt/a", "", false},
		{"device with a line break", "/dev/sdb1\n/dev/sda1 / ext4 defaults 0 0", "/mnt/a", "", false},
		{"device that is an option", "--bind", "/mnt/a", "", false},
		{"device with a comment sign", "/dev/sdb1#", "/mnt/a", "", false},
		{"local device that is no path", "sdb1", "/mnt/a", "", false},
		{"share that is an option", "-o", "/mnt/a", "", true},
		{"mount point with a space", "/dev/sdb1", "/mnt/a b", "", false},
		{"mount point with a line break", "/dev/sdb1", "/mnt/a\n/dev/sda1 / ext4 defaults 0 0", "", false},
		{"mount point with a tab", "/dev/sdb1", "/mnt/a\tb", "", false},
		{"relative mount point", "/dev/sdb1", "mnt/a", "", false},
		{"mount point going up", "/dev/sdb1", "/mnt/../etc", "", false},
		{"mount point outside /mnt", "/dev/sdb1", "/etc", "", false},
		{"mount point that only starts like /mnt", "/dev/sdb1", "/mntx/a", "", false},
		{"/mnt itself", "/dev/sdb1", "/mnt", "", false},
		{"mount point that is an option", "/dev/sdb1", "-a", "", false},
		{"owner with an option", "/dev/sdb1", "/mnt/a", "-R root", false},
		{"owner with a path", "/dev/sdb1", "/mnt/a", "root:root /etc", false},
		{"owner starting with a dash", "/dev/sdb1", "/mnt/a", "-R", false},
	}
	for _, c := range bad {
		if err := checkMountRequest(c.path, c.mountpoint, c.chown, c.net); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
}

func TestCheckMergeRequestAndMountPoint(t *testing.T) {
	if err := checkMergeRequest([]string{"/mnt/disk1", "/mnt/disk2"}, "/mnt/pool", ",moveonenospc=true,minfreespace=20G", "1000:1000"); err != nil {
		t.Errorf("a plain merge was refused: %v", err)
	}
	for name, c := range map[string]struct {
		paths      []string
		mountpoint string
		opts       string
	}{
		"no folder":                 {nil, "/mnt/pool", ""},
		"a folder with a colon":     {[]string{"/mnt/a:/etc"}, "/mnt/pool", ""},
		"a folder with a space":     {[]string{"/mnt/a b"}, "/mnt/pool", ""},
		"a relative folder":         {[]string{"mnt/a"}, "/mnt/pool", ""},
		"options with a space":      {[]string{"/mnt/a"}, "/mnt/pool", ",x=1 0 0"},
		"options with a line break": {[]string{"/mnt/a"}, "/mnt/pool", ",x=1\n/dev/sda1 / ext4 defaults 0 0"},
		"a merged folder outside":   {[]string{"/mnt/a"}, "/srv/pool", ""},
	} {
		if err := checkMergeRequest(c.paths, c.mountpoint, c.opts, ""); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	if err := checkMountPoint("/mnt/disk1"); err != nil {
		t.Errorf("a plain folder was refused for unmounting: %v", err)
	}
	for _, mountpoint := range []string{"", "-a", "/mnt/a b", "/mnt/a\n", "mnt/a", "/mnt/../etc"} {
		if err := checkMountPoint(mountpoint); err == nil {
			t.Errorf("%q: accepted for unmounting", mountpoint)
		}
	}
}

func TestFstabEntriesCannotStopTheBoot(t *testing.T) {
	real := deviceUUID
	defer func() { deviceUUID = real }()

	deviceUUID = func(device string) string { return "0a1b2c3d-1111-2222-3333-444455556666" }
	entry := localFstabEntry("/dev/sdb1", "/mnt/disk1")
	if entry != "\nUUID=0a1b2c3d-1111-2222-3333-444455556666 /mnt/disk1 auto defaults,nofail,x-systemd.device-timeout=10s 0 0\n" {
		t.Errorf("entry by UUID: %q", entry)
	}

	// no UUID (no file system label, blkid missing): the path, still with nofail
	deviceUUID = func(device string) string { return "" }
	entry = localFstabEntry("/dev/sdb1", "/mnt/disk1")
	if !strings.HasPrefix(entry, "\n/dev/sdb1 /mnt/disk1 auto ") || !strings.Contains(entry, "nofail") {
		t.Errorf("entry by path: %q", entry)
	}

	merged := mergeFstabEntry([]string{"/mnt/disk1", "/mnt/disk2"}, "/mnt/pool", ",minfreespace=20G")
	if merged != "\n/mnt/disk1:/mnt/disk2 /mnt/pool mergerfs use_ino,cache.files=partial,dropcacheonclose=true,allow_other,category.create=mfs,nofail,minfreespace=20G 0 0\n" {
		t.Errorf("merged entry: %q", merged)
	}

	// every entry is exactly one line of six fields
	for _, line := range []string{entry, merged} {
		line = strings.Trim(line, "\n")
		if strings.Contains(line, "\n") || len(strings.Fields(line)) != 6 {
			t.Errorf("not one entry of six fields: %q", line)
		}
	}
}

// The checks are worth nothing if a merge with upstream drops the calls.
func TestMountFunctionsCallTheChecks(t *testing.T) {
	for file, calls := range map[string][]string{
		"mounts.go":   {"checkMountRequest(path, mountpoint, chown, netDisk)", "localFstabEntry(path, mountpoint)", "checkMountPoint(mountpoint)"},
		"mergerFS.go": {"checkMergeRequest(paths, mountpoint, opts, chown)", "mergeFstabEntry(paths, mountpoint, opts)"},
	} {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range calls {
			if !strings.Contains(string(source), call) {
				t.Errorf("%s no longer calls %s", file, call)
			}
		}
	}
}
