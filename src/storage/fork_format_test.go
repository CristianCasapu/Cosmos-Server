package storage

import (
	"bytes"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeMachine replaces every command and every read of the machine's state.
type fakeMachine struct {
	calls      [][]string
	results    map[string]fakeResult // by command name
	mounts     []mountEntry
	swaps      []string
	holders    map[string][]string
	partitions map[string][]string
	devices    map[string]bool // block devices that exist
	links      map[string]string
	diskType   string
	// devices that appear once a command with this name ran
	appearAfter map[string]string
}

type fakeResult struct {
	out string
	err error
}

func exitError(t *testing.T, code int) error {
	t.Helper()
	// a real *exec.ExitError with the wanted status, from the shell
	err := exec.Command("sh", "-c", "exit "+string(rune('0'+code))).Run()
	if err == nil {
		t.Fatal("no exit error")
	}
	return err
}

func installFake(t *testing.T, m *fakeMachine) {
	t.Helper()
	saved := []interface{}{runStorageCommand, readMounts, readSwaps, readHolders, readPartitions, readMappedName, resolveDevice, isBlockDevice, diskTypeOf, partitionWait, sleepForStorage}
	t.Cleanup(func() {
		runStorageCommand = saved[0].(func(string, ...string) (string, error))
		readMounts = saved[1].(func() ([]mountEntry, error))
		readSwaps = saved[2].(func() ([]string, error))
		readHolders = saved[3].(func(string) []string)
		readPartitions = saved[4].(func(string) []string)
		readMappedName = saved[5].(func(string) string)
		resolveDevice = saved[6].(func(string) string)
		isBlockDevice = saved[7].(func(string) bool)
		diskTypeOf = saved[8].(func(string) (string, error))
		partitionWait = saved[9].(time.Duration)
		sleepForStorage = saved[10].(func(time.Duration))
	})
	runStorageCommand = func(name string, args ...string) (string, error) {
		m.calls = append(m.calls, append([]string{name}, args...))
		if dev, ok := m.appearAfter[name]; ok {
			m.devices[dev] = true
		}
		r := m.results[name]
		return r.out, r.err
	}
	readMounts = func() ([]mountEntry, error) { return m.mounts, nil }
	readSwaps = func() ([]string, error) { return m.swaps, nil }
	readHolders = func(name string) []string { return m.holders[name] }
	readPartitions = func(name string) []string { return m.partitions[name] }
	readMappedName = func(name string) string {
		if name == "dm-0" {
			return "vg-data"
		}
		return ""
	}
	resolveDevice = func(path string) string {
		if to, ok := m.links[path]; ok {
			return to
		}
		return path
	}
	isBlockDevice = func(path string) bool { return m.devices[path] }
	diskTypeOf = func(string) (string, error) { return m.diskType, nil }
	partitionWait = 50 * time.Millisecond
	sleepForStorage = func(time.Duration) {}
}

func newFake() *fakeMachine {
	return &fakeMachine{
		results:     map[string]fakeResult{},
		holders:     map[string][]string{},
		partitions:  map[string][]string{},
		devices:     map[string]bool{"/dev/sdb": true, "/dev/sdb1": true, "/dev/nvme0n1": true},
		links:       map[string]string{},
		appearAfter: map[string]string{},
		diskType:    "part",
	}
}

func stream(disk, fs, confirm string) (string, bool) {
	var out bytes.Buffer
	ok := forkFormatStream(&out, func() {}, disk, fs, confirm)
	return out.String(), ok
}

// The defect: mkfs failed (mke2fs over an existing file system without a
// terminal exits 1) and the route still wrote [OPERATION SUCCEEDED].
func TestFormatFailureIsReportedWithItsReason(t *testing.T) {
	m := newFake()
	installFake(t, m)
	m.results["mkfs.ext4"] = fakeResult{
		out: "mke2fs 1.47.2\n/dev/sdb1 contains a ext4 file system\nProceed anyway? (y,N)\n",
		err: exitError(t, 1),
	}
	text, ok := stream("/dev/sdb1", "ext4", "sdb1")
	if ok || strings.Contains(text, "[OPERATION SUCCEEDED]") {
		t.Fatalf("a failed mkfs was reported as a success:\n%s", text)
	}
	if !strings.Contains(text, "[OPERATION FAILED] Formatting /dev/sdb1 as ext4 failed (exit status 1): ") ||
		!strings.Contains(text, "Proceed anyway? (y,N)") {
		t.Fatalf("the failure does not carry the exit status and what mkfs said:\n%s", text)
	}
}

func TestFormatSuccessEndsWithSuccess(t *testing.T) {
	m := newFake()
	installFake(t, m)
	m.results["mkfs.ext4"] = fakeResult{out: "Creating filesystem with 1024 4k blocks\nWriting superblocks: done\n"}
	text, ok := stream("/dev/sdb1", "ext4", "/dev/sdb1")
	if !ok || !strings.Contains(text, "[OPERATION SUCCEEDED] /dev/sdb1 now holds an empty ext4 file system") || strings.Contains(text, "FAILED") {
		t.Fatalf("success not reported:\n%s", text)
	}
	if !strings.Contains(text, "Writing superblocks: done") {
		t.Fatalf("mkfs' output is not shown:\n%s", text)
	}
	if want := [][]string{{"mkfs.ext4", "-F", "/dev/sdb1"}}; !reflect.DeepEqual(m.calls, want) {
		t.Fatalf("ran %v, want %v", m.calls, want)
	}
}

func TestMissingToolIsNamed(t *testing.T) {
	m := newFake()
	installFake(t, m)
	m.results["mkfs.exfat"] = fakeResult{err: &exec.Error{Name: "mkfs.exfat", Err: exec.ErrNotFound}}
	text, ok := stream("/dev/sdb1", "exfat", "sdb1")
	if ok || !strings.Contains(text, "mkfs.exfat is not installed on this server") {
		t.Fatalf("a missing tool is not said:\n%s", text)
	}
}

func TestFormatCommandLines(t *testing.T) {
	want := map[string][]string{
		"ext4":  {"mkfs.ext4", "-F", "/dev/x"},
		"ext3":  {"mkfs.ext3", "-F", "/dev/x"},
		"ext2":  {"mkfs.ext2", "-F", "/dev/x"},
		"xfs":   {"mkfs.xfs", "-f", "/dev/x"},
		"btrfs": {"mkfs.btrfs", "-f", "/dev/x"},
		"fat32": {"mkfs.vfat", "-F", "32", "/dev/x"},
		"exfat": {"mkfs.exfat", "/dev/x"},
		"ntfs":  {"mkfs.ntfs", "-Q", "-F", "/dev/x"},
	}
	for fs, args := range want {
		got, err := formatCommand(fs, "/dev/x")
		if err != nil || !reflect.DeepEqual(got, args) {
			t.Errorf("%s: %v %v, want %v", fs, got, err, args)
		}
	}
	for _, fs := range []string{"zfs", "", "ext4 -E", "vfat", "../x"} {
		if _, err := formatCommand(fs, "/dev/x"); err == nil {
			t.Errorf("%q accepted", fs)
		}
	}
}

func TestPartitionName(t *testing.T) {
	for disk, want := range map[string]string{
		"/dev/sdb": "/dev/sdb1", "/dev/vdc": "/dev/vdc1", "/dev/nvme0n1": "/dev/nvme0n1p1",
		"/dev/mmcblk0": "/dev/mmcblk0p1", "/dev/loop3": "/dev/loop3p1",
	} {
		if got := partitionName(disk); got != want {
			t.Errorf("%s: %s, want %s", disk, got, want)
		}
	}
}

func TestWholeDiskIsPartitionedThenFormatted(t *testing.T) {
	m := newFake()
	installFake(t, m)
	m.diskType = "disk"
	m.appearAfter["partprobe"] = "/dev/nvme0n1p1"
	text, ok := stream("/dev/nvme0n1", "xfs", "nvme0n1")
	if !ok {
		t.Fatalf("failed:\n%s", text)
	}
	want := [][]string{
		{"parted", "-s", "/dev/nvme0n1", "mklabel", "gpt"},
		{"parted", "-s", "/dev/nvme0n1", "mkpart", "primary", "0%", "100%"},
		{"partprobe", "/dev/nvme0n1"},
		{"mkfs.xfs", "-f", "/dev/nvme0n1p1"},
	}
	if !reflect.DeepEqual(m.calls, want) {
		t.Fatalf("ran %v, want %v (no mkfs on the whole disk, the partition named p1)", m.calls, want)
	}
}

func TestWholeDiskStopsAtTheFailedStep(t *testing.T) {
	m := newFake()
	installFake(t, m)
	m.diskType = "disk"
	m.results["parted"] = fakeResult{out: "Error: Partition(s) on /dev/sdb are being used.\n", err: exitError(t, 1)}
	text, ok := stream("/dev/sdb", "ext4", "sdb")
	if ok || !strings.Contains(text, "[OPERATION FAILED] Creating the partition table on /dev/sdb failed (exit status 1): Error: Partition(s) on /dev/sdb are being used.") {
		t.Fatalf("parted's failure not reported:\n%s", text)
	}
	if len(m.calls) != 1 {
		t.Fatalf("went on after the failure: %v", m.calls)
	}
}

func TestPartitionThatNeverAppears(t *testing.T) {
	m := newFake()
	installFake(t, m)
	m.diskType = "disk"
	delete(m.devices, "/dev/sdb1") // partprobe ran, the kernel shows no partition
	text, ok := stream("/dev/sdb", "ext4", "sdb")
	if ok || !strings.Contains(text, "the partition /dev/sdb1 did not appear") {
		t.Fatalf("a missing partition not reported:\n%s", text)
	}
	for _, c := range m.calls {
		if strings.HasPrefix(c[0], "mkfs") {
			t.Fatalf("formatted although the partition is missing: %v", m.calls)
		}
	}
}

func TestDiskInUseIsRefused(t *testing.T) {
	cases := []struct {
		name  string
		setup func(m *fakeMachine)
		disk  string
		want  string
	}{
		{"partition mounted", func(m *fakeMachine) {
			m.partitions["sdb"] = []string{"sdb1"}
			m.mounts = []mountEntry{{Source: "/dev/sdb1", Mountpoint: "/mnt/data"}}
		}, "/dev/sdb", "/dev/sdb1 is mounted at /mnt/data"},
		{"mounted by UUID", func(m *fakeMachine) {
			m.links["/dev/disk/by-uuid/abcd"] = "/dev/sdb1"
			m.mounts = []mountEntry{{Source: "/dev/disk/by-uuid/abcd", Mountpoint: "/"}}
		}, "/dev/sdb1", "/dev/sdb1 is mounted at /"},
		{"swap", func(m *fakeMachine) {
			m.swaps = []string{"/dev/sdb1"}
		}, "/dev/sdb1", "/dev/sdb1 is in use as swap"},
		{"raid member", func(m *fakeMachine) {
			m.holders["sdb"] = []string{"md0"}
		}, "/dev/sdb", "/dev/sdb is part of the RAID array md0"},
		{"lvm", func(m *fakeMachine) {
			m.partitions["sdb"] = []string{"sdb1"}
			m.holders["sdb1"] = []string{"dm-0"}
		}, "/dev/sdb", "/dev/sdb1 is used by the LVM or encrypted volume vg-data"},
	}
	for _, c := range cases {
		m := newFake()
		installFake(t, m)
		m.diskType = "disk"
		c.setup(m)
		text, ok := stream(c.disk, "ext4", c.disk)
		if ok || !strings.Contains(text, "[OPERATION FAILED] "+c.want) {
			t.Errorf("%s: not refused:\n%s", c.name, text)
		}
		if len(m.calls) != 0 {
			t.Errorf("%s: ran %v", c.name, m.calls)
		}
	}
}

func TestOtherMountsDoNotCount(t *testing.T) {
	m := newFake()
	installFake(t, m)
	m.mounts = []mountEntry{{Source: "/dev/sda1", Mountpoint: "/"}, {Source: "tmpfs", Mountpoint: "/dev/sdb1"}, {Source: "/dev/sdb10", Mountpoint: "/mnt/x"}}
	if reason, err := diskInUse("/dev/sdb1"); err != nil || reason != "" {
		t.Fatalf("in use: %q %v", reason, err)
	}
	if mounted, _ := forkDiskMounted("/dev/sdb1"); mounted {
		t.Fatal("a mount point named like the device counted as the device")
	}
}

// IsDiskMounted compared the mount point with the device: never true.
func TestIsDiskMountedFindsTheDevice(t *testing.T) {
	m := newFake()
	installFake(t, m)
	m.partitions["sdb"] = []string{"sdb1"}
	m.mounts = []mountEntry{{Source: "/dev/sdb1", Mountpoint: "/mnt/data"}}
	for _, dev := range []string{"/dev/sdb1", "/dev/sdb"} {
		if mounted, err := IsDiskMounted(dev); err != nil || !mounted {
			t.Errorf("%s: mounted=%v err=%v", dev, mounted, err)
		}
	}
}

func TestFormatRequestIsChecked(t *testing.T) {
	m := newFake()
	installFake(t, m)
	bad := []struct{ disk, fs, confirm, want string }{
		{"/dev/sdb1", "ext4", "", "type the name of the disk (sdb1)"},
		{"/dev/sdb1", "ext4", "sdb", "type the name of the disk (sdb1)"},
		{"/dev/sdb1", "ext4", "/dev/sdb", "type the name of the disk"},
		{"/dev/sdb1", "zfs", "sdb1", "ZFS is not made with mkfs"},
		{"/dev/sdb1", "fat", "sdb1", "is not supported"},
		{"sdb1", "ext4", "sdb1", "must be a device such as /dev/sdb"},
		{"/dev/../etc/passwd", "ext4", "x", "must be a device"},
		{"/dev/sdb1 /dev/sda", "ext4", "x", "must be a device"},
		{"/etc/passwd", "ext4", "/etc/passwd", "must be a device"},
		{"/dev/sdz", "ext4", "sdz", "is not a disk or partition of this server"},
	}
	for _, c := range bad {
		text, ok := stream(c.disk, c.fs, c.confirm)
		if ok || !strings.Contains(text, c.want) {
			t.Errorf("%+v: %s", c, text)
		}
	}
	if len(m.calls) != 0 {
		t.Fatalf("ran commands for refused requests: %v", m.calls)
	}
}

func TestCleanTextAndLastLines(t *testing.T) {
	if got := cleanText("a\x1b[31mb\r\nc\x00", 100); strings.ContainsAny(got, "\x1b\r\n\x00") {
		t.Fatalf("control characters kept: %q", got)
	}
	if got := lastLines("1\n2\n\n3\n4\n", 3); got != "2 / 3 / 4" {
		t.Fatalf("lastLines: %q", got)
	}
	if got := cleanText(strings.Repeat("x", 1000), 50); len(got) > 60 {
		t.Fatalf("not bounded: %d", len(got))
	}
}

func TestCommandFailureWithoutOutput(t *testing.T) {
	err := commandFailure("Step", "tool", "", errors.New("signal: killed"))
	if err.Error() != "Step failed (signal: killed)" {
		t.Fatal(err)
	}
}
