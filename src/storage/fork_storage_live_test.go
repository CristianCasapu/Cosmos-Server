package storage

// Mounting, merging and unmounting for real, against loop devices only.
// Opt-in, and meant for a privileged throwaway container, never a machine
// whose disks matter:
//
//	COSMOS_FORK_LOOP=1 go test ./src/storage/ -run StorageFormsLive -v
//
// The container needs util-linux (mount, losetup, unshare), e2fsprogs and,
// for the merge, mergerfs. Every command the code under test runs goes through
// a guard that refuses anything but this test's own loop devices and folders
// below /mnt/cosmos-forms4-; /etc/fstab is a scratch file.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const liveFolderPrefix = "/mnt/cosmos-forms4-"

type liveDisks struct {
	t       *testing.T
	files   map[string]string // device -> backing file
	folders []string
}

func (d *liveDisks) own(device string) bool {
	if !ownLoop.MatchString(device) {
		return false
	}
	file, ok := d.files[device]
	if !ok {
		return false
	}
	out, err := exec.Command("losetup", "-n", "-O", "BACK-FILE", device).CombinedOutput()
	return err == nil && strings.TrimSpace(string(out)) == file
}

func (d *liveDisks) mustAllow(name string, args []string) {
	d.t.Helper()
	switch name {
	case "lsblk":
		if strings.Join(args, " ") != strings.Join(lsblkArgs, " ") {
			d.t.Fatalf("refusing lsblk %v", args)
		}
		return
	case "blkid":
		if strings.Join(args, " ") != strings.Join(blkidArgs, " ") {
			d.t.Fatalf("refusing blkid %v", args)
		}
		return
	case "mount", "umount", "chown", "mergerfs":
	default:
		d.t.Fatalf("refusing to run %s %v", name, args)
	}
	for _, arg := range args {
		for _, part := range strings.Split(arg, ":") {
			switch {
			case strings.HasPrefix(part, "/dev/"):
				if !d.own(part) {
					d.t.Fatalf("refusing %s %v: %s is not the test's own loop device", name, args, part)
				}
			case strings.HasPrefix(part, "/"):
				if !strings.HasPrefix(filepath.Clean(part), liveFolderPrefix) {
					d.t.Fatalf("refusing %s %v: %s is not the test's own folder", name, args, part)
				}
			}
		}
	}
}

func newLiveDisks(t *testing.T, dir string, names ...string) (*liveDisks, []string) {
	t.Helper()
	d := &liveDisks{t: t, files: map[string]string{}}
	var devices []string
	t.Cleanup(func() {
		for i := len(d.folders) - 1; i >= 0; i-- {
			if strings.HasPrefix(d.folders[i], liveFolderPrefix) {
				for exec.Command("umount", d.folders[i]).Run() == nil {
				}
				os.Remove(d.folders[i])
			}
		}
		for device, file := range d.files {
			if d.own(device) {
				exec.Command("losetup", "-d", device).Run()
			}
			os.Remove(file)
		}
	})
	for _, name := range names {
		file := filepath.Join(dir, "cosmos-forms4-"+name+".img")
		if err := os.WriteFile(file, nil, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(file, 96<<20); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("losetup", "-f", "--show", file).CombinedOutput()
		if err != nil {
			t.Fatalf("losetup: %v %s", err, out)
		}
		device := strings.TrimSpace(string(out))
		d.files[device] = file
		if !d.own(device) {
			t.Fatalf("%s is not backed by %s", device, file)
		}
		if out, err := exec.Command("mkfs.ext4", "-q", device).CombinedOutput(); err != nil {
			t.Fatalf("mkfs: %v %s", err, out)
		}
		devices = append(devices, device)
	}

	real := runStorageCommand
	runStorageCommand = func(name string, args ...string) (string, error) {
		d.mustAllow(name, args)
		return real(name, args...)
	}
	saved := []interface{}{fstabFile, configFolder}
	fstabFile = filepath.Join(dir, "fstab")
	os.WriteFile(fstabFile, []byte("# scratch fstab of the live test\n"), 0600)
	configFolder = func() string { return "/cosmos-forms4-no-config/" }
	t.Cleanup(func() {
		runStorageCommand = real
		fstabFile = saved[0].(string)
		configFolder = saved[1].(func() string)
	})
	return d, devices
}

func (d *liveDisks) folder(name string) string {
	path := liveFolderPrefix + name
	d.folders = append(d.folders, path)
	return path
}

func isMounted(t *testing.T, folder string) bool {
	t.Helper()
	mounts, err := readMounts()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mounts {
		if m.Mountpoint == folder {
			return true
		}
	}
	return false
}

func TestStorageFormsLive(t *testing.T) {
	if os.Getenv("COSMOS_FORK_LOOP") != "1" {
		t.Skip("mounts loop devices for real: COSMOS_FORK_LOOP=1, in a throwaway container only")
	}
	dir := os.Getenv("COSMOS_FORK_LOOP_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	d, devices := newLiveDisks(t, dir, "a", "b", "c")
	a, b, c := d.folder("a"), d.folder("b"), d.folder("c")
	pool := d.folder("pool")

	t.Run("mount with owner and a permanent entry", func(t *testing.T) {
		if err := Mount(devices[0], a, true, false, "1000:1000"); err != nil {
			t.Fatal(err)
		}
		var st syscall.Stat_t
		if !isMounted(t, a) || syscall.Stat(a, &st) != nil || st.Uid != 1000 {
			t.Fatalf("mounted=%v uid=%d", isMounted(t, a), st.Uid)
		}
		content, _ := os.ReadFile(fstabFile)
		if !strings.Contains(string(content), "UUID=") || !strings.Contains(string(content), " "+a+" auto defaults,nofail") {
			t.Fatalf("fstab:\n%s", content)
		}
	})

	t.Run("the candidates show it", func(t *testing.T) {
		view, err := gatherStorage()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, dev := range view.Devices {
			if dev.Device == devices[0] {
				found = dev.Use.Code == "mounted" && dev.Use.Detail == a && !dev.Mountable && dev.FSType == "ext4"
			}
			if dev.Device == devices[1] && (!dev.Mountable || dev.FSType != "ext4") {
				t.Errorf("an unused loop device: %+v", dev)
			}
		}
		m, listed := findListedMount(view.Mounts, a)
		if !found || !listed || m.Kind != "data" || m.Size == 0 || !m.Permanent {
			t.Fatalf("device found=%v mount %+v", found, m)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		wantCode(t, Mount(devices[0], d.folder("a2"), false, false, ""), "STO021")
		wantCode(t, Mount(devices[1], a, false, false, ""), "STO020")
	})

	t.Run("a failure after the mount unmounts again", func(t *testing.T) {
		err := Mount(devices[1], b, false, false, "nosuchuser9")
		wantCode(t, err, "STO024")
		if isMounted(t, b) {
			t.Fatal("left mounted")
		}
		t.Logf("%v", err)
	})

	t.Run("in use by a process", func(t *testing.T) {
		os.WriteFile(a+"/held", []byte("x"), 0600)
		holder := exec.Command("sh", "-c", "exec 3<"+a+"/held; exec sleep 30")
		if err := holder.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { holder.Process.Kill(); holder.Wait() }()
		time.Sleep(300 * time.Millisecond)
		check, err := checkUnmount(a)
		if err != nil {
			t.Fatal(err)
		}
		seen := false
		for _, u := range check.Users {
			seen = seen || (u.PID == holder.Process.Pid && u.How == "files")
		}
		if !seen {
			t.Fatalf("pid %d not among %+v", holder.Process.Pid, check.Users)
		}
		wantCode(t, forkUnmountChecked(a, false, ""), "STO033")
		if !isMounted(t, a) {
			t.Fatal("unmounted although in use")
		}
	})

	t.Run("in use by another mount namespace", func(t *testing.T) {
		if err := Mount(devices[2], c, false, false, ""); err != nil {
			t.Fatal(err)
		}
		holder := exec.Command("unshare", "--mount", "--propagation", "private", "sleep", "30")
		if err := holder.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { holder.Process.Kill(); holder.Wait() }()
		time.Sleep(300 * time.Millisecond)
		check, err := checkUnmount(c)
		if err != nil {
			t.Fatal(err)
		}
		seen := false
		for _, u := range check.Users {
			seen = seen || (u.How == "mount" && u.PID == holder.Process.Pid)
		}
		if !seen {
			t.Fatalf("the namespace of pid %d not among %+v", holder.Process.Pid, check.Users)
		}
		holder.Process.Kill()
		holder.Wait()
		if check, _ := checkUnmount(c); len(check.Users) != 0 {
			t.Fatalf("still in use after the holder ended: %+v", check.Users)
		}
	})

	t.Run("a permanent unmount with the folder typed", func(t *testing.T) {
		wantCode(t, forkUnmountChecked(a, true, ""), "STO034")
		wantCode(t, forkUnmountChecked(a, true, "/mnt/other"), "STO035")
		if err := forkUnmountChecked(a, true, a); err != nil {
			t.Fatal(err)
		}
		content, _ := os.ReadFile(fstabFile)
		if isMounted(t, a) || strings.Contains(string(content), " "+a+" ") {
			t.Fatalf("mounted=%v fstab:\n%s", isMounted(t, a), content)
		}
	})

	t.Run("merge", func(t *testing.T) {
		if _, err := exec.LookPath("mergerfs"); err != nil {
			t.Skip("mergerfs is not installed")
		}
		if err := Mount(devices[1], b, false, false, ""); err != nil {
			t.Fatal(err)
		}
		wantCode(t, MountMergerFS([]string{b, b}, pool, "", false, ""), "STO041")
		wantCode(t, MountMergerFS([]string{b, c}, pool, "category.create=rand", false, ""), "STO045")
		if err := MountMergerFS([]string{b, c}, pool, "category.create=epmfs", false, ""); err != nil {
			t.Fatal(err)
		}
		mounts, _, _ := listStorageMounts()
		if m, ok := findListedMount(mounts, pool); !ok || m.Kind != "merge" {
			t.Fatalf("the merge is listed as %+v (%v)", m, ok)
		}
		wantCode(t, MountMergerFS([]string{b, pool + "/x"}, d.folder("pool2"), "", false, ""), "STO043")
		if err := forkUnmountChecked(pool, false, ""); err != nil {
			t.Fatal(err)
		}
		if isMounted(t, pool) {
			t.Fatal("the merge is still mounted")
		}
	})

	for _, folder := range []string{b, c} {
		if isMounted(t, folder) {
			if err := forkUnmountChecked(folder, false, folder); err != nil {
				t.Errorf("%s: %v", folder, err)
			}
		}
	}
	t.Logf("loop devices used: %s", strings.Join(devices, " ")+" ("+strconv.Itoa(len(devices))+")")
}
