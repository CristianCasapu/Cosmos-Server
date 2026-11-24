package main

// Fork addition (plan 48): the watches of src/watch, given the readers of
// this server: its disks (S.M.A.R.T. without waking a sleeping disk), the
// space of its data disks, its Docker client, the incremental backup's
// folders, the local GeoLite file, and the security record's sign-ins.

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anatol/smart.go"
	"github.com/dell/csi-baremetal/pkg/base/linuxutils/lsblk"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/security"
	"github.com/azukaar/cosmos-server/src/snapshots"
	"github.com/azukaar/cosmos-server/src/storage"
	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/azukaar/cosmos-server/src/watch"
)

// startWatches is the hook in index.go, after the Telegram notifications.
func startWatches() {
	signIn := watch.Start(watch.Readers{
		Disks:          watchDisks,
		Usages:         watchUsages,
		DockerPing:     docker.ForkPing,
		Snapshots:      watchSnapshots,
		SnapshotOnDisk: watchSnapshotOnDisk,
		Country:        utils.GetIPLocation,
		Users:          watchUsers,
	})
	security.Subscribe("watch-country", func(event security.Event) {
		if event.Kind == utils.SecLoginSuccess {
			signIn(event.Actor, event.IP)
		}
	})
}

// watchDisks reads every whole disk the system lists: model, serial, where
// its partitions are mounted, and its health. A disk it cannot read is
// still listed (Unreadable): only a disk the system no longer lists is gone.
func watchDisks() ([]watch.Disk, error) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	devices, err := lsblk.NewLSBLK(logger).GetBlockDevices("")
	if err != nil {
		return nil, err
	}
	disks := []watch.Disk{}
	for _, device := range devices {
		if device.Type != "disk" || len(disks) >= 32 {
			continue
		}
		disk := watch.Disk{Device: device.Name, Model: strings.TrimSpace(device.Model), Serial: strings.TrimSpace(device.Serial), WWN: strings.TrimSpace(device.WWN),
			Reallocated: -1, Pending: -1, Uncorrectable: -1, MediaErrors: -1}
		disk.Mounts = mountsOf(device)
		dev, err := smart.Open(disk.Device)
		if err != nil {
			disk.Unreadable = true
			disks = append(disks, disk)
			continue
		}
		switch d := dev.(type) {
		case *smart.SataDevice:
			readSata(d, &disk)
		case *smart.NVMeDevice:
			readNVMe(d, &disk)
		default:
			disk.Unreadable = true
		}
		dev.Close()
		disks = append(disks, disk)
	}
	return disks, nil
}

func mountsOf(device lsblk.BlockDevice) []string {
	mounts := []string{}
	if device.MountPoint != "" && device.MountPoint != "[SWAP]" {
		mounts = append(mounts, device.MountPoint)
	}
	for _, child := range device.Children {
		mounts = append(mounts, mountsOf(child)...)
	}
	return mounts
}

// sataDisk is what is asked of a SATA disk (a fake in the tests).
type sataDisk interface {
	CheckPowerMode() (smart.AtaPowerMode, error)
	Identify() (*smart.AtaIdentifyDevice, error)
	ReadSMARTData() (*smart.AtaSmartPage, error)
	ReadSMARTThresholds() (*smart.AtaSmartThresholdsPage, error)
}

// readSata reads a SATA disk's health. Its power state is asked first, which
// never spins it up: asleep, it is not read; when the question itself fails
// (some USB bridges), it is not read either, since that could wake it.
func readSata(d sataDisk, disk *watch.Disk) {
	mode, err := d.CheckPowerMode()
	switch {
	case err != nil:
		disk.Unwatchable = true
		return
	case mode == smart.AtaPowerModeStandby || mode == smart.AtaPowerModeStandbyY:
		disk.Asleep = true
		return
	}
	if id, err := d.Identify(); err == nil && id != nil {
		fillIdentity(disk, id.ModelNumber(), id.SerialNumber())
	}
	page, err := d.ReadSMARTData()
	if err != nil || page == nil {
		disk.Unreadable = true
		return
	}
	thresholds, _ := d.ReadSMARTThresholds()
	for id, attr := range page.Attrs {
		raw := int64(attr.ValueRaw & 0xffffffff)
		switch id {
		case 5:
			disk.Reallocated = raw
		case 197:
			disk.Pending = raw
		case 198:
			disk.Uncorrectable = raw
		}
		if thresholds != nil && attr.Flags&1 != 0 {
			if limit := thresholds.Thresholds[id]; limit > 0 && attr.Current > 0 && attr.Current <= limit {
				disk.PreFail = append(disk.PreFail, attr.Name)
			}
		}
	}
}

func readNVMe(d *smart.NVMeDevice, disk *watch.Disk) {
	if id, _, err := d.Identify(); err == nil {
		fillIdentity(disk, id.ModelNumber(), id.SerialNumber())
	}
	log, err := d.ReadSMART()
	if err != nil {
		disk.Unreadable = true
		return
	}
	disk.CriticalWarning = log.CritWarning
	disk.MediaErrors = int64(log.MediaErrors.Val[0] & 0x7fffffffffffffff)
}

// fillIdentity completes what the system listed: its serial stays the
// disk's key on every path (review T2: two identities split one disk).
func fillIdentity(disk *watch.Disk, model string, serial string) {
	if disk.Model == "" {
		disk.Model = strings.TrimSpace(model)
	}
	if disk.Serial == "" && disk.WWN == "" {
		disk.Serial = strings.TrimSpace(serial)
	}
}

// networkFileSystems are not watched for filling: a dead share would hang.
// autofs is not either: a statfs on its trigger mounts the share.
var networkFileSystems = []string{"nfs", "cifs", "smb", "fuse", "sshfs", "9p", "ceph", "glusterfs", "davfs", "autofs"}

// localFUSE are FUSE file systems of local disks: watched.
var localFUSE = map[string]bool{"fuseblk": true, "fuse.mergerfs": true}

// skipForFill reports whether a file system type is left out of the fill
// watch.
func skipForFill(fstype string) bool {
	if localFUSE[fstype] {
		return false
	}
	for _, kind := range networkFileSystems {
		if strings.HasPrefix(fstype, kind) {
			return true
		}
	}
	return false
}

var mountEscape = regexp.MustCompile(`\\[0-7]{3}`)

// unescapeMount decodes the octal escapes of the kernel's mount table
// (\040 space, \011 tab, \012 line feed, \134 backslash).
func unescapeMount(path string) string {
	return mountEscape.ReplaceAllStringFunc(path, func(m string) string {
		n, err := strconv.ParseUint(m[1:], 8, 8)
		if err != nil {
			return m
		}
		return string(rune(n))
	})
}

// mountTypes reads the file system type of each mount point from the
// kernel's table, which never touches the mounts themselves.
func mountTypes() map[string]string {
	types := map[string]string{}
	raw, err := os.ReadFile("/proc/self/mounts")
	if err != nil || len(raw) > 4<<20 {
		return types
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			types[unescapeMount(fields[1])] = fields[2]
		}
	}
	return types
}

// fillTestMounts: mounts the fill watch also reads. Empty in every build
// but the end-to-end one (the tag watch_fastclock, fork_watch_e2e.go), whose
// test disk is a size-limited tmpfs that needs no rights.
var fillTestMounts []string

// statfsDeadline bounds one mount's statfs: a mergerfs pool with a dead
// branch hangs there, and must not hold the other mounts.
var statfsDeadline = 10 * time.Second

var (
	statfsMu      sync.Mutex
	statfsPending = map[string]bool{} // a statfs that did not come back: not asked again
)

// statfsBounded asks one mount's space, giving up after the deadline.
func statfsBounded(mount string, statfs func(string, *unix.Statfs_t) error) (unix.Statfs_t, bool) {
	statfsMu.Lock()
	if statfsPending[mount] {
		statfsMu.Unlock()
		return unix.Statfs_t{}, false
	}
	statfsPending[mount] = true
	statfsMu.Unlock()
	type answer struct {
		fs unix.Statfs_t
		ok bool
	}
	done := make(chan answer, 1)
	go func() {
		var fs unix.Statfs_t
		err := statfs(mount, &fs)
		statfsMu.Lock()
		delete(statfsPending, mount)
		statfsMu.Unlock()
		done <- answer{fs, err == nil}
	}()
	select {
	case a := <-done:
		return a.fs, a.ok
	case <-time.After(statfsDeadline):
		return unix.Statfs_t{}, false
	}
}

// watchUsages reads the space of "/" and of the data disks, network
// shares left out, each mount bounded in time. A mount whose space could
// not be read is given as Failed (review 6: one failed look is no
// evidence); complete is false when the data disks could not be listed.
func watchUsages() (usages []watch.Usage, complete bool) {
	types := mountTypes()
	mounts := []string{"/"}
	data, err := storage.ForkDataMountPathsChecked()
	for mount := range data {
		mounts = append(mounts, mount)
	}
	mounts = append(mounts, fillTestMounts...) // only the end-to-end build sets it
	usages = []watch.Usage{}
	for _, mount := range mounts {
		if skipForFill(types[mount]) {
			continue
		}
		fs, ok := statfsBounded(mount, unix.Statfs)
		if !ok {
			usages = append(usages, watch.Usage{Mount: mount, Failed: true})
			continue
		}
		usages = append(usages, watch.Usage{Mount: mount, Total: fs.Blocks * uint64(fs.Bsize), Free: fs.Bavail * uint64(fs.Bsize),
			ReadOnly: fs.Flags&unix.ST_RDONLY != 0})
	}
	return usages, err == nil
}

// watchSnapshots gives the incremental backup's settings and what the
// snapshot module knows in memory of each folder's last run: no disk read.
func watchSnapshots() (utils.SnapshotsConfig, map[string]time.Time) {
	cfg := utils.GetMainConfig().Snapshots
	last := map[string]time.Time{}
	for source, result := range snapshots.GetStatus().LastResults {
		if result.Snapshot != "" {
			last[source] = result.Finished
		}
	}
	return cfg, last
}

// watchSnapshotOnDisk reads a folder's last snapshot on the backup disk; the
// watch asks only when the folder looks late.
func watchSnapshotOnDisk(source string) time.Time {
	cfg := utils.GetMainConfig().Snapshots
	last := time.Time{}
	if !cfg.Enabled || cfg.Destination == "" {
		return last
	}
	for _, s := range snapshots.List(cfg.Destination, source) {
		if s.Finished.After(last) {
			last = s.Finished
		}
	}
	return last
}

// watchUsers lists the accounts, so that a deleted one's countries are
// forgotten; nil when they cannot be listed.
func watchUsers() []string {
	users := []string{}
	for _, user := range utils.ListAllUsers("all") {
		users = append(users, user.Nickname)
	}
	if len(users) == 0 {
		return nil
	}
	return users
}
