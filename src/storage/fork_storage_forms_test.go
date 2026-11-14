package storage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/cron"
	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/gorilla/mux"
)

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if got := storageErrorCode(err); got != code {
		t.Fatalf("got %q (%v), want code %s", got, err, code)
	}
}

// --- the candidates ------------------------------------------------------

func TestCandidatesSayWhatEachDeviceIsAndWhy(t *testing.T) {
	installStorageFake(t)
	view, err := gatherStorage()
	if err != nil {
		t.Fatal(err)
	}
	devices := map[string]storageDevice{}
	for _, d := range view.Devices {
		devices[d.Device] = d
	}
	want := map[string]struct{ code, detail, disk string }{
		"/dev/sda":            {"has-partitions", "", "/dev/sda"},
		"/dev/sda2":           {"mounted", "/", "/dev/sda"},
		"/dev/sdb1":           {"", "", "/dev/sdb"},
		"/dev/sdc":            {"mounted", "/mnt/disk1", "/dev/sdc"},
		"/dev/sdd":            {"raid", "md0", "/dev/sdd"},
		"/dev/md0":            {"", "", "/dev/sdd"},
		"/dev/sde":            {"nofs", "", "/dev/sde"},
		"/dev/sdf":            {"swap", "", "/dev/sdf"},
		"/dev/sdg":            {"lvm", "vg-data", "/dev/sdg"},
		"/dev/mapper/vg-data": {"mounted", "/mnt/disk2", "/dev/sdg"},
		"/dev/sdh2":           {"mounted", "/mnt/disk4", "/dev/sdh"},
		"/dev/sdl":            {"nofs", "crypto_LUKS", "/dev/sdl"},
	}
	for device, w := range want {
		d, ok := devices[device]
		if !ok {
			t.Fatalf("%s missing", device)
		}
		if d.Use.Code != w.code || d.Use.Detail != w.detail || d.Disk != w.disk || d.Mountable != (w.code == "") {
			t.Errorf("%s: %+v, want %+v", device, d, w)
		}
	}
	if d := devices["/dev/sdb1"]; d.Size != 4000785960960 || d.Label != "media" || d.FSType != "ext4" || d.UUID != "2222-media" {
		t.Errorf("sdb1 (size as a string, older lsblk): %+v", d)
	}
	if d := devices["/dev/sdc"]; d.Model != "ST8000" || d.Size != 8000000000000 {
		t.Errorf("sdc: %+v", d)
	}
	if view.Base != "/mnt" || view.InContainer {
		t.Errorf("base %q inContainer %v", view.Base, view.InContainer)
	}
}

// Without udev (Cosmos in a container) lsblk has no file system: blkid
// fills it in, or every disk would read "no file system".
func TestCandidatesAskBlkidWhatLsblkDoesNotKnow(t *testing.T) {
	f := installStorageFake(t)
	f.results["lsblk"] = fakeResult{out: `{"blockdevices":[
	 {"name":"/dev/loop3","type":"loop","size":1048576,"fstype":null,"label":null,"uuid":null,"mountpoint":null,"model":null},
	 {"name":"/dev/sde","type":"disk","size":1048576,"fstype":null,"label":null,"uuid":null,"mountpoint":null,"model":null}]}`}
	f.results["blkid"] = fakeResult{out: "DEVNAME=/dev/loop3\nUUID=abcd-1234\nBLOCK_SIZE=4096\nLABEL=My\\ Disk\nTYPE=ext4\n\nDEVNAME=/dev/sda2\nTYPE=ext4\n", err: nil}
	view, err := gatherStorage()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]storageDevice{}
	for _, d := range view.Devices {
		got[d.Device] = d
	}
	if d := got["/dev/loop3"]; d.FSType != "ext4" || d.Label != "My Disk" || d.UUID != "abcd-1234" || !d.Mountable {
		t.Fatalf("loop3: %+v", d)
	}
	if d := got["/dev/sde"]; d.Use.Code != "nofs" {
		t.Fatalf("sde: %+v", d)
	}
	if want := append([]string{"blkid"}, blkidArgs...); !reflect.DeepEqual(f.callsOf("blkid"), [][]string{want}) {
		t.Fatalf("blkid calls %v", f.callsOf("blkid"))
	}
}

func TestCandidatesNameTheMountsAndTheirKind(t *testing.T) {
	installStorageFake(t)
	view, err := gatherStorage()
	if err != nil {
		t.Fatal(err)
	}
	mounts := map[string]storageMount{}
	for _, m := range view.Mounts {
		mounts[m.Path] = m
	}
	want := map[string]struct{ kind, disk string }{
		"/":            {"system", "/dev/sda"},
		"/mnt/disk1":   {"data", "/dev/sdc"},
		"/mnt/disk2":   {"data", "/dev/sdg"},
		"/mnt/disk3":   {"data", "/dev/sdh"},
		"/mnt/disk4":   {"data", "/dev/sdh"},
		"/mnt/storage": {"merge", ""},
		"/mnt/nas":     {"network", ""},
	}
	for path, w := range want {
		m, ok := mounts[path]
		if !ok {
			t.Fatalf("%s missing from %v", path, view.Mounts)
		}
		if m.Kind != w.kind || m.Disk != w.disk {
			t.Errorf("%s: kind %q disk %q, want %+v", path, m.Kind, m.Disk, w)
		}
	}
	for _, gone := range []string{"/boot/efi", "/proc", "/mnt/tmp"} {
		if _, ok := mounts[gone]; ok {
			t.Errorf("%s is listed", gone)
		}
	}
	if m := mounts["/mnt/disk1"]; m.Size != 4*testTB || m.Free != testTB || m.Permanent {
		t.Errorf("disk1: %+v", m)
	}
	if !mounts["/mnt/disk3"].Permanent || !mounts["/"].Permanent {
		t.Errorf("fstab entries not seen: %+v %+v", mounts["/mnt/disk3"], mounts["/"])
	}
	if m := mounts["/mnt/nas"]; m.Size != 0 {
		t.Errorf("a network share is not asked for its size (it could hang): %+v", m)
	}
}

func TestTheMountHoldingCosmosConfigurationIsTheSystem(t *testing.T) {
	installStorageFake(t)
	configFolder = func() string { return "/mnt/disk3/cosmos/" }
	mounts, _, err := listStorageMounts()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mounts {
		if (m.Path == "/mnt/disk3") != (m.Kind == "system") && m.Path != "/" {
			t.Errorf("%s: %s", m.Path, m.Kind)
		}
	}
}

func authed(req *http.Request) *http.Request {
	ctx := &utils.AuthContext{Nickname: "admin", Permissions: []utils.Permission{utils.PERM_RESOURCES, utils.PERM_RESOURCES_READ}, IsSudoed: true}
	return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, ctx))
}

func TestCandidatesRoute(t *testing.T) {
	f := installStorageFake(t)

	w := httptest.NewRecorder()
	StorageCandidatesRoute(w, httptest.NewRequest("GET", "/api/storage/candidates", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("logged out: %d", w.Code)
	}

	w = httptest.NewRecorder()
	StorageCandidatesRoute(w, authed(httptest.NewRequest("POST", "/api/storage/candidates", nil)))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", w.Code)
	}

	w = httptest.NewRecorder()
	StorageCandidatesRoute(w, authed(httptest.NewRequest("GET", "/api/storage/candidates", nil)))
	var answer struct {
		Status string
		Data   struct {
			Devices []map[string]interface{}
			Mounts  []map[string]interface{}
			Base    string
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil || answer.Status != "OK" || len(answer.Data.Devices) == 0 || len(answer.Data.Mounts) == 0 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	for _, key := range []string{"device", "disk", "type", "size", "fstype", "label", "uuid", "model", "mountpoint", "use", "mountable"} {
		if _, ok := answer.Data.Devices[0][key]; !ok {
			t.Errorf("device without %q: %v", key, answer.Data.Devices[0])
		}
	}
	for _, key := range []string{"path", "device", "disk", "fstype", "size", "free", "permanent", "kind"} {
		if _, ok := answer.Data.Mounts[0][key]; !ok {
			t.Errorf("mount without %q: %v", key, answer.Data.Mounts[0])
		}
	}
	if want := append([]string{"lsblk"}, lsblkArgs...); !reflect.DeepEqual(f.callsOf("lsblk")[0], want) {
		t.Errorf("ran %v", f.callsOf("lsblk"))
	}

	f.results["lsblk"] = fakeResult{out: "lsblk: not today", err: errors.New("exit status 1")}
	w = httptest.NewRecorder()
	StorageCandidatesRoute(w, authed(httptest.NewRequest("GET", "/api/storage/candidates", nil)))
	if w.Code != 500 || !strings.Contains(w.Body.String(), "STO010") {
		t.Fatalf("lsblk failing: %d %s", w.Code, w.Body.String())
	}
}

// --- mount ---------------------------------------------------------------

const testFolder = "/mnt/cosmos-fork-test-not-there"

// The defect: Mount ran its commands with utils.Exec, which no test could
// replace.
func TestMountRunsItsCommandsThroughTheRunner(t *testing.T) {
	f := installStorageFake(t)
	if err := Mount("/dev/sdb1", testFolder, false, false, "1000:1000"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"mount", "--", "/dev/sdb1", testFolder}, {"chown", "--", "1000:1000", testFolder}}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("ran %v, want %v", f.calls, want)
	}
	if !reflect.DeepEqual(f.folder, []string{testFolder}) {
		t.Fatalf("folders made: %v", f.folder)
	}
}

func TestMountWritesItsPermanentEntry(t *testing.T) {
	f := installStorageFake(t)
	deviceUUID = func(string) string { return "2222-media" }
	if err := Mount("/dev/sdb1", testFolder, true, false, ""); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(f.fstab)
	if !strings.Contains(string(content), "\nUUID=2222-media "+testFolder+" auto defaults,nofail,x-systemd.device-timeout=10s 0 0\n") {
		t.Fatalf("fstab:\n%s", content)
	}
}

// The defect: a folder that already was a mount point was mounted over,
// hiding the disk mounted there.
func TestMountRefusesAFolderThatIsAlreadyAMountPoint(t *testing.T) {
	f := installStorageFake(t)
	err := Mount("/dev/sdb1", "/mnt/disk1", false, false, "")
	wantCode(t, err, "STO020")
	if !strings.Contains(err.Error(), "/mnt/disk1 is already a mount point (of /dev/sdc)") {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("ran %v", f.calls)
	}
}

// The defect: a device mounted elsewhere, a swap, a RAID or LVM member and a
// whole disk with partitions were handed to mount.
func TestMountRefusesADeviceInUse(t *testing.T) {
	cases := []struct{ device, code, says string }{
		{"/dev/sdc", "STO021", "/dev/sdc is already mounted at /mnt/disk1"},
		{"/dev/mapper/vg-data", "STO021", "already mounted at /mnt/disk2"},
		{"/dev/sdf", "STO022", "in use as swap"},
		{"/dev/sdd", "STO022", "part of the RAID array md0"},
		{"/dev/sdg", "STO022", "LVM or encrypted volume vg-data"},
		{"/dev/sdh", "STO023", "holds partitions: mount one of them (for example /dev/sdh1)"},
	}
	for _, c := range cases {
		f := installStorageFake(t)
		err := Mount(c.device, testFolder, false, false, "")
		wantCode(t, err, c.code)
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v", c.device, err)
		}
		if len(f.calls) != 0 || len(f.folder) != 0 {
			t.Errorf("%s: ran %v, made %v", c.device, f.calls, f.folder)
		}
	}
}

// The defect: when chown failed after the mount, the disk stayed mounted
// and the panel said the mount failed.
func TestMountUnmountsAgainWhenTheOwnerCannotBeSet(t *testing.T) {
	f := installStorageFake(t)
	f.results["chown"] = fakeResult{out: "chown: invalid user: 'nobody9'", err: exitError(t, 1)}
	err := Mount("/dev/sdb1", testFolder, true, false, "nobody9")
	wantCode(t, err, "STO024")
	if !strings.Contains(err.Error(), "invalid user") || !strings.Contains(err.Error(), "/dev/sdb1 was unmounted again") {
		t.Fatal(err)
	}
	if got := f.callsOf("umount"); !reflect.DeepEqual(got, [][]string{{"umount", "--", testFolder}}) {
		t.Fatalf("umount calls %v", got)
	}
	if content, _ := os.ReadFile(f.fstab); strings.Contains(string(content), testFolder) {
		t.Fatalf("written to fstab after the failure:\n%s", content)
	}
}

func TestMountUnmountsAgainWhenFstabCannotBeWritten(t *testing.T) {
	f := installStorageFake(t)
	fstabFile = filepath.Join(t.TempDir(), "missing", "fstab")
	err := Mount("/dev/sdb1", testFolder, true, false, "")
	wantCode(t, err, "STO024")
	if len(f.callsOf("umount")) != 1 {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestMountSaysWhenUnmountingAgainFailsToo(t *testing.T) {
	f := installStorageFake(t)
	f.results["chown"] = fakeResult{err: exitError(t, 1)}
	f.results["umount"] = fakeResult{out: "umount: target is busy.", err: exitError(t, 1)}
	err := Mount("/dev/sdb1", testFolder, false, false, "1000")
	wantCode(t, err, "STO025")
	if !strings.Contains(err.Error(), "target is busy") || !strings.Contains(err.Error(), "still mounted") {
		t.Fatal(err)
	}
}

func TestAFailedMountIsNotUndone(t *testing.T) {
	f := installStorageFake(t)
	f.results["mount"] = fakeResult{out: "mount: wrong fs type", err: exitError(t, 2)}
	err := Mount("/dev/sdb1", testFolder, false, false, "")
	if err == nil || !strings.Contains(err.Error(), "wrong fs type") || len(f.callsOf("umount")) != 0 || len(f.callsOf("chown")) != 0 {
		t.Fatalf("%v %v", err, f.calls)
	}
}

func TestMountRouteAnswersARefusalWithItsCode(t *testing.T) {
	installStorageFake(t)
	w := httptest.NewRecorder()
	body := strings.NewReader(`{"path":"/dev/sdc","mountPoint":"` + testFolder + `"}`)
	MountRoute(w, authed(httptest.NewRequest("POST", "/api/mount", body)))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"code":"STO021"`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

// --- merge ---------------------------------------------------------------

func TestMergeRefusesWhatCannotBeMerged(t *testing.T) {
	cases := []struct {
		name     string
		branches []string
		target   string
		opts     string
		code     string
	}{
		{"one folder", []string{"/mnt/disk1"}, "/mnt/pool", "", "STO040"},
		{"33 folders", make([]string, 33), "/mnt/pool", "", "STO040"},
		{"twice the same", []string{"/mnt/disk1", "/mnt/disk1/"}, "/mnt/pool", "", "STO041"},
		{"the merged folder itself", []string{"/mnt/disk1", "/mnt/pool"}, "/mnt/pool", "", "STO044"},
		{"inside the merged folder", []string{"/mnt/disk1", "/mnt/pool/a"}, "/mnt/pool", "", "STO044"},
		{"merged folder inside a branch", []string{"/mnt/disk1", "/mnt/disk2"}, "/mnt/disk1/pool", "", "STO044"},
		{"a merge", []string{"/mnt/disk1", "/mnt/storage/x"}, "/mnt/pool", "", "STO043"},
		{"a network share", []string{"/mnt/disk1", "/mnt/nas"}, "/mnt/pool", "", "STO043"},
		{"the system disk", []string{"/mnt/disk1", "/srv/media"}, "/mnt/pool", "", "STO043"},
		{"not a listed mount", []string{"/mnt/disk1", "/mnt/tmp/x"}, "/mnt/pool", "", "STO042"},
		{"an unknown policy", []string{"/mnt/disk1", "/mnt/disk2"}, "/mnt/pool", "category.create=rand", "STO045"},
		{"onto a mount point", []string{"/mnt/disk1", "/mnt/disk2"}, "/mnt/disk3", "", "STO020"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := installStorageFake(t)
			for i := range c.branches {
				if c.branches[i] == "" {
					c.branches[i] = "/mnt/disk1/" + string(rune('a'+i%26)) + strings.Repeat("x", i/26)
				}
			}
			wantCode(t, MountMergerFS(c.branches, c.target, c.opts, false, ""), c.code)
			if len(f.calls) != 0 {
				t.Fatalf("ran %v", f.calls)
			}
		})
	}
}

func TestMergeOfDataDisksRunsAndAcceptsThePolicies(t *testing.T) {
	for _, policy := range []string{"", "category.create=epmfs", "category.create=mfs", "category.create=lfs", "minfreespace=20G,category.create=ff"} {
		f := installStorageFake(t)
		if err := MountMergerFS([]string{"/mnt/disk1", "/mnt/disk2/media"}, "/mnt/pool", policy, false, "1000:1000"); err != nil {
			t.Fatalf("%q: %v", policy, err)
		}
		opts := "use_ino,cache.files=partial,dropcacheonclose=true,allow_other,category.create=mfs"
		if policy != "" {
			opts += "," + policy
		}
		want := [][]string{{"mergerfs", "-o", opts, "/mnt/disk1:/mnt/disk2/media", "/mnt/pool"}, {"chown", "--", "1000:1000", "/mnt/pool"}}
		if !reflect.DeepEqual(f.calls, want) {
			t.Fatalf("ran %v, want %v", f.calls, want)
		}
	}
}

func TestMergeUnmountsAgainWhenTheOwnerCannotBeSet(t *testing.T) {
	f := installStorageFake(t)
	f.results["chown"] = fakeResult{err: exitError(t, 1)}
	err := MountMergerFS([]string{"/mnt/disk1", "/mnt/disk2"}, "/mnt/pool", "", false, "1000")
	wantCode(t, err, "STO024")
	if !strings.Contains(err.Error(), "The merged folder was unmounted again") || len(f.callsOf("umount")) != 1 {
		t.Fatalf("%v %v", err, f.calls)
	}
}

// --- unmount -------------------------------------------------------------

// The defect: any folder could be unmounted and taken out of /etc/fstab, the
// system disk and /boot included.
func TestUnmountRefusesTheSystemAndWhatIsNotListed(t *testing.T) {
	cases := map[string]string{
		"/":          "STO032",
		"/boot/efi":  "STO031",
		"/usr":       "STO031",
		"/mnt/tmp":   "STO031",
		"/mnt/a b":   "STO030",
		"relative/x": "STO030",
	}
	for path, code := range cases {
		f := installStorageFake(t)
		wantCode(t, forkUnmountChecked(path, true, path), code)
		if len(f.calls) != 0 {
			t.Fatalf("%s: ran %v", path, f.calls)
		}
	}
	installStorageFake(t)
	configFolder = func() string { return "/mnt/disk3/cosmos/" }
	wantCode(t, forkUnmountChecked("/mnt/disk3", false, "/mnt/disk3"), "STO032")
}

// a fake /proc: self in namespace 1, processes using /mnt/disk1 in each way,
// and namespaces that do and do not hold its file system
func fakeProc(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	link := func(target string, parts ...string) {
		path := filepath.Join(append([]string{root}, parts...)...)
		os.MkdirAll(filepath.Dir(path), 0700)
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	file := func(content string, parts ...string) {
		path := filepath.Join(append([]string{root}, parts...)...)
		os.MkdirAll(filepath.Dir(path), 0700)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	link("mnt:[1]", "self", "ns", "mnt")
	file("22 1 8:2 / / rw shared:1 - ext4 /dev/sda2 rw\n40 22 8:32 / /mnt/disk1 rw shared:5 - ext4 /dev/sdc rw\n", "self", "mountinfo")

	process := func(pid string, ns string, comm string) {
		link(ns, pid, "ns", "mnt")
		link("/", pid, "root")
		link("/", pid, "cwd")
		file(comm+"\n", pid, "comm")
		os.MkdirAll(filepath.Join(root, pid, "fd"), 0700)
	}
	process("1", "mnt:[1]", "systemd")
	link("/dev/null", "1", "fd", "0")
	link("socket:[123]", "1", "fd", "1")

	process("100", "mnt:[1]", "rsync")
	os.Remove(filepath.Join(root, "100", "cwd"))
	link("/mnt/disk1/media", "100", "cwd")

	process("101", "mnt:[1]", "jellyfin\x1b[31m")
	link("/mnt/disk1/films/a.mkv (deleted)", "101", "fd", "7")

	process("102", "mnt:[1]", "chrooted")
	os.Remove(filepath.Join(root, "102", "root"))
	link("/mnt/disk1/jail", "102", "root")

	process("103", "mnt:[1]", "elsewhere")
	link("/mnt/disk10/file", "103", "fd", "3")

	// a container: its bind mount of the disk is private, the unmount does not reach it
	process("200", "mnt:[2]", "nextcloud")
	file("500 400 0:99 / / rw - overlay overlay rw\n501 500 8:32 /data /data rw - ext4 /dev/sdc rw\n", "200", "mountinfo")
	process("201", "mnt:[2]", "nextcloud-worker")

	// a service sandbox: a slave of the host's mount, it receives the unmount
	process("300", "mnt:[3]", "systemd-timesyn")
	file("600 1 8:2 / / ro master:1 - ext4 /dev/sda2 rw\n601 600 8:32 / /mnt/disk1 ro master:5 - ext4 /dev/sdc rw\n", "300", "mountinfo")

	// a namespace without the disk
	process("400", "mnt:[4]", "other")
	file("700 1 0:99 / / rw - overlay overlay rw\n", "400", "mountinfo")
	return root
}

func TestFindMountUsersLooksInEveryWayAProcessCanHoldAMount(t *testing.T) {
	installStorageFake(t)
	procRoot = fakeProc(t)
	users := findMountUsers("/mnt/disk1", 8, 32)
	want := []mountUser{
		{PID: 100, Name: "rsync", How: "cwd"},
		{PID: 101, Name: "jellyfin [31m", How: "files"},
		{PID: 102, Name: "chrooted", How: "root"},
		{PID: 200, Name: "nextcloud", How: "mount"},
	}
	if !reflect.DeepEqual(users, want) {
		t.Fatalf("users %+v\nwant  %+v", users, want)
	}
	if users := findMountUsers("/mnt/disk2", 253, 0); len(users) != 0 {
		t.Fatalf("an unused mount: %+v", users)
	}
}

// The defect: a disk in use (by a container's bind mount, or a process on
// this host) was unmounted without a question.
func TestUnmountInUseNeedsTheFolderTyped(t *testing.T) {
	f := installStorageFake(t)
	procRoot = fakeProc(t)

	err := forkUnmountChecked("/mnt/disk1", false, "")
	wantCode(t, err, "STO033")
	if !strings.Contains(err.Error(), "rsync, jellyfin") || !strings.Contains(err.Error(), "type /mnt/disk1 to confirm") {
		t.Fatal(err)
	}
	wantCode(t, forkUnmountChecked("/mnt/disk1", false, "/mnt/disk2"), "STO035")
	if len(f.calls) != 0 {
		t.Fatalf("ran %v", f.calls)
	}

	forkUnmountChecked("/mnt/disk1", false, " /mnt/disk1 ")
	if got := f.callsOf("umount"); !reflect.DeepEqual(got, [][]string{{"umount", "--", "/mnt/disk1"}}) {
		t.Fatalf("confirmed unmount ran %v", f.calls)
	}
}

func TestPermanentUnmountNeedsTheFolderTyped(t *testing.T) {
	f := installStorageFake(t)
	wantCode(t, forkUnmountChecked("/mnt/disk2", true, ""), "STO034")
	if len(f.calls) != 0 {
		t.Fatalf("ran %v", f.calls)
	}
	// not in use, not permanent: no question
	forkUnmountChecked("/mnt/disk2", false, "")
	if len(f.callsOf("umount")) != 1 {
		t.Fatalf("ran %v", f.calls)
	}
}

func TestPermanentUnmountRemovesTheEntryFromTheFstabFile(t *testing.T) {
	f := installStorageFake(t)
	mountpoint := filepath.Join("/mnt", "disk3")
	forkUnmountChecked(mountpoint, true, mountpoint)
	content, _ := os.ReadFile(f.fstab)
	if strings.Contains(string(content), "/mnt/disk3") || !strings.Contains(string(content), "UUID=1111-root / ") {
		t.Fatalf("fstab:\n%s", content)
	}
}

func TestUnmountRouteAsksForTheConfirmation(t *testing.T) {
	installStorageFake(t)
	w := httptest.NewRecorder()
	UnmountRoute(w, authed(httptest.NewRequest("POST", "/api/unmount", strings.NewReader(`{"mountPoint":"/mnt/disk2","permanent":true}`))))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"code":"STO034"`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	UnmountRoute(w, authed(httptest.NewRequest("POST", "/api/unmount", strings.NewReader(`{"mountPoint":"/","permanent":false,"confirm":"/"}`))))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":"STO032"`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestUnmountCheckRoute(t *testing.T) {
	installStorageFake(t)
	procRoot = fakeProc(t)
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		StorageUnmountCheckRoute(w, authed(httptest.NewRequest("POST", "/api/storage/unmount-check", strings.NewReader(body))))
		return w
	}
	for body, want := range map[string]string{
		`{"mountPoint":"/mnt/disk1","extra":1}`:              "STO011",
		`{"mountPoint":"/mnt/disk1"}{"mountPoint":"/"}`:      "STO011",
		`{"mountPoint":"` + strings.Repeat("a", 1100) + `"}`: "STO011",
		`not json`:                              "STO011",
		`{"mountPoint":"/usr"}`:                 "STO012",
		`{"mountPoint":"/mnt/tmp"}`:             "STO012",
		`{"mountPoint":"/mnt/disk1/../../etc"}`: "STO030",
	} {
		if w := post(body); !strings.Contains(w.Body.String(), `"code":"`+want+`"`) || w.Code == 200 {
			t.Errorf("%.60s: %d %s", body, w.Code, w.Body.String())
		}
	}
	if w := post(`{"mountPoint":"/usr"}`); w.Code != http.StatusNotFound {
		t.Errorf("an unlisted folder: %d", w.Code)
	}

	w := post(`{"mountPoint":"/mnt/disk1"}`)
	var answer struct {
		Status string
		Data   unmountCheck
	}
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil || w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if answer.Data.MountPoint != "/mnt/disk1" || answer.Data.Device != "/dev/sdc" || answer.Data.Permanent || answer.Data.System || len(answer.Data.Users) != 4 {
		t.Fatalf("%+v", answer.Data)
	}
	if w := post(`{"mountPoint":"/"}`); !strings.Contains(w.Body.String(), `"system":true`) {
		t.Fatalf("/: %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	StorageUnmountCheckRoute(w, authed(httptest.NewRequest("GET", "/api/storage/unmount-check", nil)))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", w.Code)
	}
	w = httptest.NewRecorder()
	StorageUnmountCheckRoute(w, httptest.NewRequest("POST", "/api/storage/unmount-check", strings.NewReader(`{"mountPoint":"/"}`)))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("logged out: %d", w.Code)
	}
}

func TestUnmountCheckScanIsBounded(t *testing.T) {
	installStorageFake(t)
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "self", "ns"), 0700)
	os.Symlink("mnt:[1]", filepath.Join(root, "self", "ns", "mnt"))
	for i := 1; i <= 60; i++ {
		dir := filepath.Join(root, strconv.Itoa(i))
		os.MkdirAll(filepath.Join(dir, "ns"), 0700)
		os.Symlink("mnt:[1]", filepath.Join(dir, "ns", "mnt"))
		os.Symlink("/mnt/disk1", filepath.Join(dir, "cwd"))
	}
	procRoot = root
	if users := findMountUsers("/mnt/disk1", 8, 32); len(users) != maxMountUsers {
		t.Fatalf("%d users answered", len(users))
	}
}

func jobOf(t *testing.T, name string) cron.ConfigJob {
	t.Helper()
	job, ok := lookupJob(name)
	if !ok {
		t.Fatalf("no job %q", name)
	}
	return job
}

// lookupJob reads the scheduler's list under its lock: it is being rewritten in the background.
func lookupJob(name string) (cron.ConfigJob, bool) {
	cron.CRONLock <- true
	defer func() { <-cron.CRONLock }()
	job, ok := cron.GetJobsList()["SnapRAID"][name]
	return job, ok
}

func jobDisabled(name string) bool {
	job, ok := lookupJob(name)
	return ok && job.Disabled
}

func jobEnabled(name string) bool {
	job, ok := lookupJob(name)
	return ok && !job.Disabled
}

// eventually waits up to two seconds for a condition the background re-registration makes true.
func eventually(condition func() bool) bool {
	for i := 0; i < 200; i++ {
		if condition() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func resetSnapRAIDJobs() { cron.ResetScheduler("SnapRAID") }

func muxVars(req *http.Request, vars map[string]string) *http.Request {
	return mux.SetURLVars(req, vars)
}

// --- SnapRAID ------------------------------------------------------------

func goodSnapRAID() utils.SnapRAIDConfig {
	return utils.SnapRAIDConfig{
		Name:         "Storage parity",
		Enabled:      true,
		Data:         map[string]string{"d1": "/mnt/disk2", "d2": "/mnt/disk3"},
		Parity:       []string{"/mnt/parity1"},
		SyncCrontab:  "0 0 2 * * *",
		ScrubCrontab: "0 0 4 */2 * *",
	}
}

func TestSnapRAIDGoodConfigurationPasses(t *testing.T) {
	installStorageFake(t)
	view, err := gatherStorage()
	if err != nil {
		t.Fatal(err)
	}
	if err := checkSnapRAIDConfig(goodSnapRAID(), "", nil, view.Mounts); err != nil {
		t.Fatal(err)
	}
	c := goodSnapRAID()
	c.Name = "pool_1-a"
	c.Parity = []string{"/mnt/parity1", "/mnt/parity3"}
	if err := checkSnapRAIDConfig(c, "pool_1-a", []utils.SnapRAIDConfig{{Name: "pool_1-a"}, {Name: "other"}}, view.Mounts); err != nil {
		t.Fatalf("an edit keeping its name: %v", err)
	}
}

// upstream's lookup of a folder: "" matches the first device that is not
// mounted, so a row without a folder passed its "must be a disk" check
func TestUpstreamFindParentMatchesAnEmptyFolder(t *testing.T) {
	var disks []BlockDevice
	if err := json.Unmarshal([]byte(`[{"name":"/dev/sdb","mountpoint":"","children":[]},{"name":"/dev/sdc","mountpoint":"/mnt/disk1"}]`), &disks); err != nil {
		t.Fatal(err)
	}
	if found, err := findParent(disks, ""); err != nil || found.Name != "/dev/sdb" {
		t.Fatalf("findParent(\"\") = %v, %v", found.Name, err)
	}
}

// The defect: a data or parity row without a folder was saved.
func TestCreateSnapRAIDRefusesARowWithoutAFolder(t *testing.T) {
	installStorageFake(t)
	c := goodSnapRAID()
	c.Data["d3"] = ""
	wantCode(t, CreateSnapRAID(c, ""), "STO052")

	c = goodSnapRAID()
	c.Parity = []string{"/mnt/parity1", ""}
	wantCode(t, CreateSnapRAID(c, ""), "STO052")
}

func TestSnapRAIDRules(t *testing.T) {
	installStorageFake(t)
	view, err := gatherStorage()
	if err != nil {
		t.Fatal(err)
	}
	existing := []utils.SnapRAIDConfig{{Name: "Storage parity"}, {Name: "Second"}}
	cases := []struct {
		name   string
		change func(c *utils.SnapRAIDConfig)
		edit   string
		code   string
	}{
		{"a name with a line break", func(c *utils.SnapRAIDConfig) { c.Name = "pool\nx" }, "", "STO050"},
		{"a name too short", func(c *utils.SnapRAIDConfig) { c.Name = "ab" }, "", "STO050"},
		{"a name with a slash", func(c *utils.SnapRAIDConfig) { c.Name = "../etc" }, "", "STO050"},
		{"a data name with a space", func(c *utils.SnapRAIDConfig) { c.Data["d 3"] = "/mnt/parity3" }, "", "STO051"},
		{"one data disk", func(c *utils.SnapRAIDConfig) { delete(c.Data, "d2") }, "", "STO053"},
		{"no parity", func(c *utils.SnapRAIDConfig) { c.Parity = nil }, "", "STO054"},
		{"seven parity", func(c *utils.SnapRAIDConfig) { c.Parity = make([]string, 7) }, "", "STO054"},
		{"a folder that is no mount", func(c *utils.SnapRAIDConfig) { c.Data["d2"] = "/mnt/disk3/sub" }, "", "STO055"},
		{"a merge", func(c *utils.SnapRAIDConfig) { c.Data["d2"] = "/mnt/storage" }, "", "STO055"},
		{"the system disk", func(c *utils.SnapRAIDConfig) { c.Data["d2"] = "/" }, "", "STO055"},
		{"a network share", func(c *utils.SnapRAIDConfig) { c.Parity = []string{"/mnt/nas"} }, "", "STO055"},
		{"two data on one disk", func(c *utils.SnapRAIDConfig) { c.Data["d1"] = "/mnt/disk4" }, "", "STO056"},
		{"parity on a data disk's drive", func(c *utils.SnapRAIDConfig) { c.Parity = []string{"/mnt/disk4"} }, "", "STO056"},
		{"two parity on one drive", func(c *utils.SnapRAIDConfig) { c.Parity = []string{"/mnt/parity1", "/mnt/parity2"} }, "", "STO056"},
		{"a parity too small", func(c *utils.SnapRAIDConfig) { c.Parity = []string{"/mnt/small"} }, "", "STO057"},
		{"a parity that is a data disk", func(c *utils.SnapRAIDConfig) { c.Parity = []string{"/mnt/disk2"} }, "", "STO058"},
		{"a data disk twice", func(c *utils.SnapRAIDConfig) { c.Data["d3"] = "/mnt/disk2" }, "", "STO063"},
		{"a name taken", func(c *utils.SnapRAIDConfig) {}, "", "STO059"},
		{"renamed onto another", func(c *utils.SnapRAIDConfig) { c.Name = "Second" }, "Storage parity", "STO059"},
		{"an edit of nothing", func(c *utils.SnapRAIDConfig) { c.Name = "Third" }, "Gone", "STO062"},
		{"a sync schedule without seconds", func(c *utils.SnapRAIDConfig) { c.Name = "Third"; c.SyncCrontab = "0 2 * * *" }, "", "STO060"},
		{"an empty scrub schedule", func(c *utils.SnapRAIDConfig) { c.Name = "Third"; c.ScrubCrontab = "" }, "", "STO061"},
		{"a scrub schedule out of range", func(c *utils.SnapRAIDConfig) { c.Name = "Third"; c.ScrubCrontab = "0 0 25 * * *" }, "", "STO061"},
	}
	for _, c := range cases {
		config := goodSnapRAID()
		c.change(&config)
		err := checkSnapRAIDConfig(config, c.edit, existing, view.Mounts)
		if got := storageErrorCode(err); got != c.code {
			t.Errorf("%s: %q (%v), want %s", c.name, got, err, c.code)
		}
	}
	config := goodSnapRAID()
	config.Name = "Third"
	if err := checkSnapRAIDConfig(config, "", existing, view.Mounts); err != nil {
		t.Fatalf("a free name: %v", err)
	}
}

func TestSnapRAIDSaveIsReached(t *testing.T) {
	installStorageFake(t)
	var saved utils.SnapRAIDConfig
	saveSnapRAID = func(c utils.SnapRAIDConfig, edit string) error {
		saved = c
		return refuse(409, "STO059", "stop here")
	}
	wantCode(t, CreateSnapRAID(goodSnapRAID(), ""), "STO059")
	if saved.Name != "Storage parity" {
		t.Fatalf("saved %+v", saved)
	}
}

func snapRAIDConfigFolder(t *testing.T, raids []utils.SnapRAIDConfig) {
	t.Helper()
	// LoadBaseMainConfig starts these in the background: nothing to restart in a test
	noop := func() {}
	utils.WaitForAllJobs, utils.RestartConstellation, utils.InitRemoteStorage = noop, noop, noop
	utils.InitBackups, utils.InitSnapRAIDConfig, utils.RestartCRON = noop, noop, noop
	saved := utils.CONFIGFOLDER
	utils.CONFIGFOLDER = t.TempDir() + "/"
	t.Cleanup(func() { utils.CONFIGFOLDER = saved })
	config := utils.Config{}
	config.Storage.SnapRAIDs = raids
	content, _ := json.Marshal(config)
	if err := os.WriteFile(utils.CONFIGFOLDER+"cosmos.config.json", content, 0600); err != nil {
		t.Fatal(err)
	}
	utils.LoadBaseMainConfig(config)
}

// The defects: the jobs were registered without Disabled, so "disable"
// changed nothing, and the toggle did not register them again.
func TestDisablingSnapRAIDDisablesItsJobs(t *testing.T) {
	raid := goodSnapRAID()
	snapRAIDConfigFolder(t, []utils.SnapRAIDConfig{raid})
	InitSnapRAIDConfig()
	if job := jobOf(t, "SnapRAID sync "+raid.Name); job.Disabled {
		t.Fatal("an enabled SnapRAID has disabled jobs")
	}
	if err := ToggleSnapRAID(raid.Name, false); err != nil {
		t.Fatal(err)
	}
	snapRAIDReinits.Wait()
	if !eventually(func() bool {
		return jobDisabled("SnapRAID sync "+raid.Name) && jobDisabled("SnapRAID scrub "+raid.Name)
	}) {
		t.Fatal("disabling left the jobs scheduled")
	}
	if err := ToggleSnapRAID(raid.Name, true); err != nil {
		t.Fatal(err)
	}
	snapRAIDReinits.Wait()
	if !eventually(func() bool { return jobEnabled("SnapRAID sync " + raid.Name) }) {
		t.Fatal("enabling left the jobs off")
	}
	if err := ToggleSnapRAID("missing", true); err == nil {
		t.Fatal("a missing SnapRAID toggled")
	}
}

// The defect: sync, scrub, fix, enable and disable answered OK whatever
// happened.
func TestSnapRAIDActionsAnswerTheirFailure(t *testing.T) {
	raid := goodSnapRAID()
	raid.Name = "never-registered"
	snapRAIDConfigFolder(t, []utils.SnapRAIDConfig{raid})
	resetSnapRAIDJobs()

	if RunSnapRAIDSync(raid) == nil || RunSnapRAIDScrub(raid) == nil || RunSnapRAIDFix(raid) == nil {
		t.Fatal("an action on a SnapRAID without jobs or file reported no error")
	}
	for action, code := range map[string]string{"sync": "STO070", "scrub": "STO070", "fix": "STO070"} {
		w := httptest.NewRecorder()
		req := authed(httptest.NewRequest("GET", "/api/snapraid/never-registered/"+action, nil))
		req = muxVars(req, map[string]string{"name": "never-registered", "action": action})
		SnapRAIDRunRoute(w, req)
		if w.Code != 500 || !strings.Contains(w.Body.String(), `"code":"`+code+`"`) {
			t.Errorf("%s: %d %s", action, w.Code, w.Body.String())
		}
	}
}
