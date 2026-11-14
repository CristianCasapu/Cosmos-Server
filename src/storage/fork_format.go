package storage

// Fork addition: formatting a disk tells the truth, and refuses a disk that is
// in use.
//
// Upstream started mkfs and returned its output pipe without ever waiting for
// it, then wrote "[OPERATION SUCCEEDED]" whatever mkfs' exit status was:
// mke2fs over an existing file system asks "Proceed anyway?", reads nothing
// and exits 1; mkfs.xfs and mkfs.btrfs refuse an existing file system without
// -f; "fat32" is not a mkfs type at all. All of these were reported as a
// success. Its "is the disk mounted" check compared the mount point with the
// device path, so it never found anything: a mounted disk, the system disk
// included, could be formatted. For a whole disk it formatted the disk, then
// wrote a partition table over it and formatted "<disk>1", which is not the
// partition's name for nvme0n1, mmcblk0 or loop0.
//
// Now each file system has its own fixed command line, the command is waited
// for and its exit status and last lines become the reason shown in the
// panel; a disk counts as in use when it or one of its partitions is mounted,
// is a swap or belongs to a RAID, LVM or encrypted volume; a whole disk gets a
// GPT label, one partition (named as the kernel names it) and the file system
// on that partition, each step checked. The request names the device a second
// time ("confirm"), as typed by the administrator.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/moby/sys/mountinfo"
)

// runStorageCommand runs one command with fixed arguments and returns its
// combined output. A variable so that the tests run no command.
var runStorageCommand = func(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// formatCommands: the command line for each file system the route accepts.
// The device is appended last. -F / -f: the administrator confirmed that what
// is on the device goes; without it the tools stop at a question nobody can
// answer. NTFS: -Q, a quick format (without it mkfs.ntfs zeroes the whole
// device first, for hours).
var formatCommands = map[string][]string{
	"ext4":  {"mkfs.ext4", "-F"},
	"ext3":  {"mkfs.ext3", "-F"},
	"ext2":  {"mkfs.ext2", "-F"},
	"xfs":   {"mkfs.xfs", "-f"},
	"btrfs": {"mkfs.btrfs", "-f"},
	"fat32": {"mkfs.vfat", "-F", "32"},
	"exfat": {"mkfs.exfat"},
	"ntfs":  {"mkfs.ntfs", "-Q", "-F"},
}

// formatCommand returns the command line that puts a file system on a device.
func formatCommand(filesystem string, device string) ([]string, error) {
	if filesystem == "zfs" {
		return nil, errors.New("ZFS is not made with mkfs: create a pool with zpool instead")
	}
	base, ok := formatCommands[filesystem]
	if !ok {
		return nil, fmt.Errorf("the file system %q is not supported: choose ext4, ext3, ext2, xfs, btrfs, fat32, exfat or ntfs", cleanText(filesystem, 20))
	}
	return append(append([]string{}, base...), device), nil
}

// cleanText keeps a text from a command or a request fit for one line of the
// panel and the log: control characters become spaces, the length is bounded.
func cleanText(text string, max int) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > max {
		text = text[:max] + "…"
	}
	return text
}

// lastLines returns the last n lines of a command's output that say
// something, joined with " / ".
func lastLines(output string, n int) string {
	var kept []string
	for _, line := range strings.Split(output, "\n") {
		if line = cleanText(line, 200); line != "" {
			kept = append(kept, line)
		}
	}
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	return cleanText(strings.Join(kept, " / "), 400)
}

// commandFailure turns a failed command into the reason shown to the
// administrator: which step, the exit status, and what the command said.
func commandFailure(step string, name string, output string, err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s: %s is not installed on this server", step, name)
	}
	status := err.Error()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		status = fmt.Sprintf("exit status %d", exitErr.ExitCode())
	}
	if said := lastLines(output, 3); said != "" {
		return fmt.Errorf("%s failed (%s): %s", step, status, said)
	}
	return fmt.Errorf("%s failed (%s)", step, status)
}

// forkFormatDisk puts a file system on a device that is not in use and waits
// for the command. The output is returned in both cases; the error carries
// the reason.
func forkFormatDisk(device string, filesystem string) (io.Reader, error) {
	args, err := formatCommand(filesystem, device)
	if err != nil {
		return nil, err
	}
	if reason, err := diskInUse(device); err != nil {
		return nil, err
	} else if reason != "" {
		return nil, errors.New(reason)
	}
	out, err := runStorageCommand(args[0], args[1:]...)
	if err != nil {
		return strings.NewReader(out), commandFailure("Formatting "+device+" as "+filesystem, args[0], out, err)
	}
	return strings.NewReader(out), nil
}

// --- what holds a device -------------------------------------------------

type mountEntry struct {
	Source     string
	Mountpoint string
}

// The machine's state, behind variables for the tests.
var (
	readMounts = func() ([]mountEntry, error) {
		mounts, err := mountinfo.GetMounts(nil)
		if err != nil {
			return nil, err
		}
		entries := make([]mountEntry, 0, len(mounts))
		for _, m := range mounts {
			entries = append(entries, mountEntry{Source: m.Source, Mountpoint: m.Mountpoint})
		}
		return entries, nil
	}
	readSwaps = func() ([]string, error) {
		data, err := os.ReadFile("/proc/swaps")
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		var devices []string
		for i, line := range strings.Split(string(data), "\n") {
			if fields := strings.Fields(line); i > 0 && len(fields) > 0 {
				devices = append(devices, fields[0])
			}
		}
		return devices, nil
	}
	// the block devices holding this one (RAID, LVM, dm-crypt): names in /sys
	readHolders = func(name string) []string {
		return dirNames("/sys/class/block/" + name + "/holders")
	}
	// the partitions of a disk, by kernel name
	readPartitions = func(name string) []string {
		var parts []string
		for _, entry := range dirNames("/sys/class/block/" + name) {
			if _, err := os.Stat("/sys/class/block/" + name + "/" + entry + "/partition"); err == nil {
				parts = append(parts, entry)
			}
		}
		return parts
	}
	// the name a mapped device shows (/sys/class/block/dm-0/dm/name), "" if none
	readMappedName = func(name string) string {
		data, err := os.ReadFile("/sys/class/block/" + name + "/dm/name")
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(data))
	}
	// a path's canonical device path: /dev/disk/by-uuid/… and /dev/mapper/…
	// are links to /dev/sdb1, /dev/dm-0
	resolveDevice = func(path string) string {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return resolved
		}
		return filepath.Clean(path)
	}
	isBlockDevice = func(path string) bool {
		info, err := os.Stat(path)
		return err == nil && info.Mode()&os.ModeDevice != 0 && info.Mode()&os.ModeCharDevice == 0
	}
)

func dirNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// diskInUse says in words what holds a device or one of its partitions, ""
// when nothing does.
func diskInUse(device string) (string, error) {
	canonical := resolveDevice(device)
	name := filepath.Base(canonical)
	names := append([]string{name}, readPartitions(name)...)

	mounts, err := readMounts()
	if err != nil {
		return "", fmt.Errorf("cannot tell whether %s is in use: %v", device, err)
	}
	swaps, err := readSwaps()
	if err != nil {
		return "", fmt.Errorf("cannot tell whether %s is in use: %v", device, err)
	}

	for _, n := range names {
		path := "/dev/" + n
		for _, m := range mounts {
			if strings.HasPrefix(m.Source, "/") && resolveDevice(m.Source) == path {
				return path + " is mounted at " + cleanText(m.Mountpoint, 200) + ": unmount it first", nil
			}
		}
		for _, s := range swaps {
			if resolveDevice(s) == path {
				return path + " is in use as swap: turn the swap off first", nil
			}
		}
		for _, holder := range readHolders(n) {
			switch {
			case strings.HasPrefix(holder, "md"):
				return path + " is part of the RAID array " + holder + ": remove it from the array first", nil
			case strings.HasPrefix(holder, "dm-"):
				label := holder
				if mapped := readMappedName(holder); mapped != "" {
					label = mapped
				}
				return path + " is used by the LVM or encrypted volume " + cleanText(label, 100) + ": close that volume first", nil
			default:
				return path + " is used by " + cleanText(holder, 100) + ": release it first", nil
			}
		}
	}
	return "", nil
}

// forkDiskMounted is IsDiskMounted done right: the device or one of its
// partitions is the source of a mount.
func forkDiskMounted(device string) (bool, error) {
	canonical := resolveDevice(device)
	name := filepath.Base(canonical)
	mounts, err := readMounts()
	if err != nil {
		return false, err
	}
	for _, n := range append([]string{name}, readPartitions(name)...) {
		for _, m := range mounts {
			if strings.HasPrefix(m.Source, "/") && resolveDevice(m.Source) == "/dev/"+n {
				return true, nil
			}
		}
	}
	return false, nil
}

// --- the request ---------------------------------------------------------

// partitionName is the kernel's name of the first partition of a disk: a
// name ending in a digit takes "p" (nvme0n1p1, mmcblk0p1, loop0p1).
func partitionName(disk string) string {
	if last := disk[len(disk)-1]; last >= '0' && last <= '9' {
		return disk + "p1"
	}
	return disk + "1"
}

// checkFormatRequest refuses a request before anything runs: the device must
// be a block device under /dev, the file system one this server can make, and
// the device must be named a second time, as the administrator typed it.
func checkFormatRequest(disk string, filesystem string, confirm string) error {
	if !oneField(disk) || !strings.HasPrefix(disk, "/dev/") || filepath.Clean(disk) != disk || strings.Contains(disk, "..") {
		return errors.New("the disk to format must be a device such as /dev/sdb")
	}
	if _, err := formatCommand(filesystem, disk); err != nil {
		return err
	}
	if !isBlockDevice(disk) {
		return errors.New(disk + " is not a disk or partition of this server")
	}
	typed := strings.TrimSpace(confirm)
	if typed == "" || (typed != disk && "/dev/"+typed != disk) {
		return errors.New("type the name of the disk (" + strings.TrimPrefix(disk, "/dev/") + ") to confirm that everything on it may be erased")
	}
	return nil
}

// Variables for the tests.
var (
	diskTypeOf      = isDiskOrPartition
	partitionWait   = 10 * time.Second
	partitionPoll   = 200 * time.Millisecond
	sleepForStorage = time.Sleep
)

// waitForDevice waits until the kernel shows a partition just made.
func waitForDevice(path string) bool {
	deadline := time.Now().Add(partitionWait)
	for {
		if isBlockDevice(path) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		sleepForStorage(partitionPoll)
	}
}

// forkFormatStream does what the format route asks and writes its progress,
// one line at a time, ending with "[OPERATION SUCCEEDED]" or
// "[OPERATION FAILED] <reason>". It reports whether it succeeded.
func forkFormatStream(w io.Writer, flush func(), disk string, filesystem string, confirm string) bool {
	say := func(line string) {
		fmt.Fprintf(w, "%s\n", line)
		flush()
	}
	copyOut := func(out io.Reader) {
		if out == nil {
			return
		}
		scanner := bufio.NewScanner(out)
		for scanner.Scan() {
			line := cleanText(scanner.Text(), 300)
			utils.Log(line)
			say(line)
		}
	}
	fail := func(err error) bool {
		reason := cleanText(err.Error(), 600)
		utils.Error("FormatDiskRoute: "+reason, nil)
		fmt.Fprintf(w, "%s\n", utils.DoErr("[OPERATION FAILED] %s", reason))
		flush()
		return false
	}

	if err := checkFormatRequest(disk, filesystem, confirm); err != nil {
		return fail(err)
	}
	if reason, err := diskInUse(disk); err != nil {
		return fail(err)
	} else if reason != "" {
		return fail(errors.New(reason))
	}

	target := disk
	diskType, err := diskTypeOf(disk)
	if err != nil {
		return fail(fmt.Errorf("cannot tell whether %s is a disk or a partition: %v", disk, err))
	}
	if diskType == "disk" {
		say("Creating a GPT partition table on " + disk + "...")
		out, err := CreateGPTTable(disk)
		copyOut(out)
		if err != nil {
			return fail(err)
		}
		say("Creating one partition over the whole disk...")
		out, err = CreateSinglePartition(disk)
		copyOut(out)
		if err != nil {
			return fail(err)
		}
		target = partitionName(disk)
		if !waitForDevice(target) {
			return fail(fmt.Errorf("the partition %s did not appear after the partition table was written", target))
		}
		say("Partition " + target + " created.")
	}

	say("Formatting " + target + " as " + filesystem + "...")
	out, err := FormatDisk(target, filesystem)
	copyOut(out)
	if err != nil {
		return fail(err)
	}

	utils.Log("FormatDiskRoute - formatted " + target + " as " + filesystem)
	fmt.Fprintf(w, "%s\n", utils.DoSuccess("[OPERATION SUCCEEDED] %s", target+" now holds an empty "+filesystem+" file system"))
	flush()
	return true
}

// forkPartitionProbe asks the kernel to read a disk's new partition table.
// Its failure only matters when the partition does not show up: the caller
// waits for the partition and reports this output then.
func forkPartitionProbe(disk string) string {
	out, err := runStorageCommand("partprobe", disk)
	if err != nil {
		return lastLines(out, 3)
	}
	return ""
}
