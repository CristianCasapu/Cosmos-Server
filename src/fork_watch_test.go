package main

import (
	"errors"
	"golang.org/x/sys/unix"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anatol/smart.go"

	"github.com/azukaar/cosmos-server/src/watch"
)

// fakeSata is a SATA disk for the reader: it counts the reads that could
// wake it.
type fakeSata struct {
	mode     smart.AtaPowerMode
	modeErr  error
	reads    int
	readErr  error
	identify int
}

func (f *fakeSata) CheckPowerMode() (smart.AtaPowerMode, error) { return f.mode, f.modeErr }
func (f *fakeSata) Identify() (*smart.AtaIdentifyDevice, error) {
	f.identify++
	return nil, errors.New("no identify in the fake")
}
func (f *fakeSata) ReadSMARTData() (*smart.AtaSmartPage, error) {
	f.reads++
	if f.readErr != nil {
		return nil, f.readErr
	}
	return &smart.AtaSmartPage{Attrs: map[uint8]smart.AtaSmartAttr{5: {Id: 5, ValueRaw: 12}, 197: {Id: 197, ValueRaw: 0}}}, nil
}
func (f *fakeSata) ReadSMARTThresholds() (*smart.AtaSmartThresholdsPage, error) {
	return &smart.AtaSmartThresholdsPage{Thresholds: map[uint8]uint8{}}, nil
}

// A sleeping disk, or one that cannot say whether it sleeps, is never read
// (review, second round: the power check ignored survived).
func TestASleepingOrSilentDiskIsNotRead(t *testing.T) {
	asleep := &fakeSata{mode: smart.AtaPowerModeStandby}
	disk := watch.Disk{}
	readSata(asleep, &disk)
	if !disk.Asleep || asleep.reads != 0 || asleep.identify != 0 {
		t.Errorf("standby: %+v, %d reads, %d identify", disk, asleep.reads, asleep.identify)
	}
	silent := &fakeSata{modeErr: errors.New("bridge does not pass the command")}
	disk = watch.Disk{}
	readSata(silent, &disk)
	if !disk.Unwatchable || silent.reads != 0 {
		t.Errorf("power check failed: %+v, %d reads", disk, silent.reads)
	}
	awake := &fakeSata{mode: smart.AtaPowerModeActiveOrIdle}
	disk = watch.Disk{Reallocated: -1, Pending: -1, Uncorrectable: -1}
	readSata(awake, &disk)
	if awake.reads != 1 || disk.Reallocated != 12 || disk.Pending != 0 {
		t.Errorf("awake: %+v", disk)
	}
	broken := &fakeSata{mode: smart.AtaPowerModeIdle, readErr: errors.New("I/O error")}
	disk = watch.Disk{}
	readSata(broken, &disk)
	if !disk.Unreadable {
		t.Errorf("a failed read is not unreadable: %+v", disk)
	}
}

func TestWhichFileSystemsAreWatchedForFilling(t *testing.T) {
	for fstype, skip := range map[string]bool{
		"nfs4": true, "nfs": true, "cifs": true, "smb3": true, "fuse.rclone": true, "fuse.sshfs": true, "autofs": true,
		"ext4": false, "xfs": false, "btrfs": false, "zfs": false, "fuseblk": false, "fuse.mergerfs": false, "": false,
	} {
		if skipForFill(fstype) != skip {
			t.Errorf("%s: skipped %v, want %v", fstype, !skip, skip)
		}
	}
	if got := unescapeMount(`/mnt/My\040Disk\011x\134y`); got != "/mnt/My Disk\tx\\y" {
		t.Errorf("%q", got)
	}
}

// A mount whose statfs hangs is given up after the deadline and not asked
// again while it hangs; the others are read.
func TestAHangingMountDoesNotHoldTheOthers(t *testing.T) {
	previous := statfsDeadline
	statfsDeadline = 50 * time.Millisecond
	defer func() { statfsDeadline = previous }()
	release := make(chan struct{})
	var calls atomic.Int32
	hang := func(string, *unix.Statfs_t) error { calls.Add(1); <-release; return nil }
	if _, ok := statfsBounded("/mnt/pool", hang); ok {
		t.Error("a hanging statfs answered")
	}
	if _, ok := statfsBounded("/mnt/pool", hang); ok || calls.Load() != 1 {
		t.Errorf("asked %d times while hanging", calls.Load())
	}
	if _, ok := statfsBounded("/mnt/other", func(string, *unix.Statfs_t) error { return nil }); !ok {
		t.Error("another mount was held")
	}
	close(release)
}

// Review T2: the serial the system lists stays the disk's key after a read.
func TestTheListedSerialStays(t *testing.T) {
	disk := watch.Disk{Serial: "WD-WCC7K4ABCD12"}
	fillIdentity(&disk, "WDC WD40EFRX", "WD-WCC7K4ABCD12 ")
	fillIdentity(&disk, "WDC WD40EFRX", "OTHER")
	if disk.Serial != "WD-WCC7K4ABCD12" || disk.Model != "WDC WD40EFRX" {
		t.Errorf("%+v", disk)
	}
	bare := watch.Disk{}
	fillIdentity(&bare, "M", " S1 ")
	if bare.Serial != "S1" {
		t.Errorf("%+v", bare)
	}
}
