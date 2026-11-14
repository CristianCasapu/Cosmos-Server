package storage

// The machine the storage form tests run against: a mount table, lsblk's
// answer, sizes, /etc/fstab and the commands, all fake. Nothing here runs a
// command or reads a disk.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/moby/sys/mountinfo"
)

const testTB = 1000 * 1000 * 1000 * 1000

const testLsblk = `{"blockdevices":[
 {"name":"/dev/sda","pkname":null,"type":"disk","size":500107862016,"fstype":null,"label":null,"uuid":null,"mountpoint":null,"model":"Samsung SSD 870","children":[
  {"name":"/dev/sda1","pkname":"/dev/sda","type":"part","size":536870912,"fstype":"vfat","label":null,"uuid":"AAAA-BBBB","mountpoint":"/boot/efi","model":null},
  {"name":"/dev/sda2","pkname":"/dev/sda","type":"part","size":499570991104,"fstype":"ext4","label":"root","uuid":"1111-root","mountpoint":"/","model":null}
 ]},
 {"name":"/dev/sdb","pkname":null,"type":"disk","size":"4000787030016","fstype":null,"label":null,"uuid":null,"mountpoint":null,"model":"WDC WD40EFRX","children":[
  {"name":"/dev/sdb1","pkname":"/dev/sdb","type":"part","size":"4000785960960","fstype":"ext4","label":"media","uuid":"2222-media","mountpoint":null,"model":null}
 ]},
 {"name":"/dev/sdc","pkname":null,"type":"disk","size":8000000000000,"fstype":"ext4","label":"backup","uuid":"3333","mountpoint":"/mnt/disk1","model":"ST8000"},
 {"name":"/dev/sdd","pkname":null,"type":"disk","size":4000787030016,"fstype":"linux_raid_member","label":"nas:0","uuid":"4444","mountpoint":null,"model":"RAIDDISK","children":[
  {"name":"/dev/md0","pkname":"/dev/sdd","type":"raid1","size":4000000000000,"fstype":"ext4","label":null,"uuid":"5555","mountpoint":null,"model":null}
 ]},
 {"name":"/dev/sde","pkname":null,"type":"disk","size":1000204886016,"fstype":null,"label":null,"uuid":null,"mountpoint":null,"model":"EMPTY"},
 {"name":"/dev/sdf","pkname":null,"type":"disk","size":16000000000,"fstype":"swap","label":null,"uuid":"6666","mountpoint":"[SWAP]","model":"SWAPDISK"},
 {"name":"/dev/sdg","pkname":null,"type":"disk","size":4000000000000,"fstype":"LVM2_member","label":null,"uuid":"7777","mountpoint":null,"model":"LVMDISK","children":[
  {"name":"/dev/mapper/vg-data","pkname":"/dev/sdg","type":"lvm","size":4000000000000,"fstype":"xfs","label":null,"uuid":"8888","mountpoint":"/mnt/disk2","model":null}
 ]},
 {"name":"/dev/sdh","pkname":null,"type":"disk","size":2000000000000,"fstype":null,"label":null,"uuid":null,"mountpoint":null,"model":"TWOPARTS","children":[
  {"name":"/dev/sdh1","pkname":"/dev/sdh","type":"part","size":1000000000000,"fstype":"ext4","label":null,"uuid":"9991","mountpoint":"/mnt/disk3","model":null},
  {"name":"/dev/sdh2","pkname":"/dev/sdh","type":"part","size":1000000000000,"fstype":"ext4","label":null,"uuid":"9992","mountpoint":"/mnt/disk4","model":null}
 ]},
 {"name":"/dev/sdi","pkname":null,"type":"disk","size":20000000000000,"fstype":null,"label":null,"uuid":null,"mountpoint":null,"model":"BIG","children":[
  {"name":"/dev/sdi1","pkname":"/dev/sdi","type":"part","size":10000000000000,"fstype":"ext4","label":null,"uuid":"aaa1","mountpoint":"/mnt/parity1","model":null},
  {"name":"/dev/sdi2","pkname":"/dev/sdi","type":"part","size":10000000000000,"fstype":"ext4","label":null,"uuid":"aaa2","mountpoint":"/mnt/parity2","model":null}
 ]},
 {"name":"/dev/sdj","pkname":null,"type":"disk","size":500000000000,"fstype":null,"label":null,"uuid":null,"mountpoint":null,"model":"SMALL","children":[
  {"name":"/dev/sdj1","pkname":"/dev/sdj","type":"part","size":500000000000,"fstype":"ext4","label":null,"uuid":"bbb1","mountpoint":"/mnt/small","model":null}
 ]},
 {"name":"/dev/sdk","pkname":null,"type":"disk","size":12000000000000,"fstype":"ext4","label":null,"uuid":"ccc1","mountpoint":"/mnt/parity3","model":"OTHERBIG"},
 {"name":"/dev/sdl","pkname":null,"type":"disk","size":2000000000000,"fstype":"crypto_LUKS","label":null,"uuid":"ddd1","mountpoint":null,"model":"LOCKED"}
]}`

func info(id int, mountpoint string, fstype string, source string, major int, minor int) *mountinfo.Info {
	return &mountinfo.Info{ID: id, Parent: 1, Major: major, Minor: minor, Root: "/", Mountpoint: mountpoint, FSType: fstype, Source: source}
}

func testMountTable() []*mountinfo.Info {
	return []*mountinfo.Info{
		info(1, "/", "ext4", "/dev/sda2", 8, 2),
		info(2, "/boot/efi", "vfat", "/dev/sda1", 8, 1),
		info(3, "/proc", "proc", "proc", 0, 20),
		info(4, "/mnt/disk1", "ext4", "/dev/sdc", 8, 32),
		info(5, "/mnt/disk2", "xfs", "/dev/mapper/vg-data", 253, 0),
		info(6, "/mnt/disk3", "ext4", "/dev/sdh1", 8, 113),
		info(7, "/mnt/disk4", "ext4", "/dev/sdh2", 8, 114),
		info(8, "/mnt/parity1", "ext4", "/dev/sdi1", 8, 129),
		info(9, "/mnt/parity2", "ext4", "/dev/sdi2", 8, 130),
		info(10, "/mnt/small", "ext4", "/dev/sdj1", 8, 145),
		info(11, "/mnt/parity3", "ext4", "/dev/sdk", 8, 160),
		info(12, "/mnt/storage", "fuse.mergerfs", "disk3:disk4", 0, 50),
		info(13, "/mnt/nas", "nfs4", "nas.example.org:/export", 0, 60),
		info(14, "/mnt/tmp", "tmpfs", "tmpfs", 0, 70),
	}
}

type storageFake struct {
	*fakeMachine
	infos   []*mountinfo.Info
	lsblk   string
	fstab   string // the file standing for /etc/fstab
	folder  []string
	removed []string
}

// installStorageFake replaces the whole machine; the mount table read by
// readMounts follows infos.
func installStorageFake(t *testing.T) *storageFake {
	t.Helper()
	m := newFake()
	installFake(t, m)
	f := &storageFake{fakeMachine: m, infos: testMountTable(), lsblk: testLsblk}

	dir := t.TempDir()
	f.fstab = filepath.Join(dir, "fstab")
	if err := os.WriteFile(f.fstab, []byte("UUID=1111-root / ext4 defaults 0 1\nUUID=9991 /mnt/disk3 auto defaults,nofail 0 0\n"), 0600); err != nil {
		t.Fatal(err)
	}

	saved := struct {
		table     func() ([]*mountinfo.Info, error)
		docker    func() []byte
		stat      func(string) (uint64, uint64, error)
		inFstab   func(string) (bool, error)
		config    func() string
		inside    func() bool
		fstab     string
		mkdir     func(string, os.FileMode) error
		rmdir     func(string) error
		proc      string
		uuid      func(string) string
		snapraids func() []utils.SnapRAIDConfig
		save      func(utils.SnapRAIDConfig, string) error
	}{readMountTable, readDockerConf, statFS, mountInFstab, configFolder, insideContainer, fstabFile, makeMountFolder, removeMountFolder, procRoot, deviceUUID, existingSnapRAIDs, saveSnapRAID}
	t.Cleanup(func() {
		readMountTable, readDockerConf, statFS, mountInFstab = saved.table, saved.docker, saved.stat, saved.inFstab
		configFolder, insideContainer, fstabFile, makeMountFolder = saved.config, saved.inside, saved.fstab, saved.mkdir
		removeMountFolder = saved.rmdir
		procRoot, deviceUUID, existingSnapRAIDs, saveSnapRAID = saved.proc, saved.uuid, saved.snapraids, saved.save
	})

	readMountTable = func() ([]*mountinfo.Info, error) { return f.infos, nil }
	readMounts = func() ([]mountEntry, error) {
		var entries []mountEntry
		for _, i := range f.infos {
			entries = append(entries, mountEntry{Source: i.Source, Mountpoint: i.Mountpoint})
		}
		return entries, nil
	}
	readDockerConf = func() []byte { return nil }
	statFS = func(path string) (uint64, uint64, error) { return 4 * testTB, 1 * testTB, nil }
	mountInFstab = func(path string) (bool, error) { return isMountPointInFstab(path) }
	configFolder = func() string { return "/var/lib/cosmos/" }
	insideContainer = func() bool { return false }
	fstabFile = f.fstab
	makeMountFolder = func(path string, mode os.FileMode) error { f.folder = append(f.folder, path); return nil }
	removeMountFolder = func(path string) error { f.removed = append(f.removed, path); return nil }
	procRoot = filepath.Join(dir, "proc-missing")
	deviceUUID = func(device string) string { return "" }
	existingSnapRAIDs = func() []utils.SnapRAIDConfig { return nil }
	saveSnapRAID = func(utils.SnapRAIDConfig, string) error { t.Fatal("the configuration was saved"); return nil }

	m.links["/dev/mapper/vg-data"] = "/dev/dm-0"
	m.holders["sdd"] = []string{"md0"}
	m.holders["sdg"] = []string{"dm-0"}
	m.partitions["sdh"] = []string{"sdh1", "sdh2"}
	m.swaps = []string{"/dev/sdf"}
	m.results["lsblk"] = fakeResult{out: f.lsblk}
	return f
}

// callsOf returns the commands run with this name.
func (f *storageFake) callsOf(name string) [][]string {
	var calls [][]string
	for _, c := range f.calls {
		if c[0] == name {
			calls = append(calls, c)
		}
	}
	return calls
}
