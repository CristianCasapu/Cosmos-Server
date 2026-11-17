package main

// Fork addition (plan 48): the watches of src/watch, given the readers of
// this server: its disks (S.M.A.R.T. without waking a sleeping disk), the
// space of its data disks, its Docker client, the incremental backup's
// folders, the local GeoLite file, and the security record's sign-ins.

import (
	"strings"
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
		Disks:      watchDisks,
		Usages:     watchUsages,
		DockerPing: docker.ForkPing,
		Snapshots:  watchSnapshots,
		Country:    utils.GetIPLocation,
	})
	security.Subscribe("watch-country", func(event security.Event) {
		if event.Kind == utils.SecLoginSuccess {
			signIn(event.Actor, event.IP)
		}
	})
}

// watchDisks reads every whole disk: model, serial, where its partitions
// are mounted, and its health. A SATA disk is asked its power state first,
// which never spins it up, and is not read while it sleeps.
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
		disk := watch.Disk{Device: device.Name, Model: strings.TrimSpace(device.Model), Serial: strings.TrimSpace(device.Serial),
			Reallocated: -1, Pending: -1, Uncorrectable: -1, MediaErrors: -1}
		disk.Mounts = mountsOf(device)
		if readHealth(&disk) {
			disks = append(disks, disk)
		}
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

// readHealth fills in what the disk says of itself; false when it cannot
// be read at all (no S.M.A.R.T., a virtual disk).
func readHealth(disk *watch.Disk) bool {
	dev, err := smart.Open(disk.Device)
	if err != nil {
		return false
	}
	defer dev.Close()
	switch d := dev.(type) {
	case *smart.SataDevice:
		if mode, err := d.CheckPowerMode(); err == nil && (mode == smart.AtaPowerModeStandby || mode == smart.AtaPowerModeStandbyY) {
			disk.Asleep = true
			return true
		}
		if id, err := d.Identify(); err == nil {
			disk.Model, disk.Serial = strings.TrimSpace(id.ModelNumber()), strings.TrimSpace(id.SerialNumber())
		}
		page, err := d.ReadSMARTData()
		if err != nil {
			return false
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
		return true
	case *smart.NVMeDevice:
		if id, _, err := d.Identify(); err == nil {
			disk.Model, disk.Serial = strings.TrimSpace(id.ModelNumber()), strings.TrimSpace(id.SerialNumber())
		}
		log, err := d.ReadSMART()
		if err != nil {
			return false
		}
		disk.CriticalWarning = log.CritWarning
		disk.MediaErrors = int64(log.MediaErrors.Val[0] & 0x7fffffffffffffff)
		return true
	}
	return false
}

// watchUsages reads the space of "/" and of the data disks.
func watchUsages() []watch.Usage {
	mounts := []string{"/"}
	for mount := range storage.ForkDataMountPaths() {
		mounts = append(mounts, mount)
	}
	usages := []watch.Usage{}
	for _, mount := range mounts {
		var fs unix.Statfs_t
		if unix.Statfs(mount, &fs) != nil {
			continue
		}
		usages = append(usages, watch.Usage{Mount: mount, Total: fs.Blocks * uint64(fs.Bsize), Free: fs.Bavail * uint64(fs.Bsize),
			ReadOnly: fs.Flags&unix.ST_RDONLY != 0})
	}
	return usages
}

// watchSnapshots gives the incremental backup's settings and the last
// finished snapshot of each of its folders.
func watchSnapshots() (utils.SnapshotsConfig, map[string]time.Time) {
	cfg := utils.GetMainConfig().Snapshots
	last := map[string]time.Time{}
	if !cfg.Enabled || cfg.Destination == "" {
		return cfg, last
	}
	for i, source := range cfg.Sources {
		if i >= 64 {
			break
		}
		for _, s := range snapshots.List(cfg.Destination, source) {
			if s.Finished.After(last[source]) {
				last[source] = s.Finished
			}
		}
	}
	return cfg, last
}
