package edithints

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func link(t *testing.T, target, at string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, at); err != nil {
		t.Fatal(err)
	}
}

// a tree of /dev and /sys as a server with an Intel render node, a USB
// serial dongle with its stable name, an ACM modem, a camera, and things that
// must not be listed (a card node, a regular file named like a device,
// ttyS0, a name with a newline)
func deviceTree(t *testing.T) Roots {
	root := t.TempDir()
	dev, sys := filepath.Join(root, "dev"), filepath.Join(root, "sys")
	for _, n := range []string{"dri/renderD128", "dri/card0", "ttyUSB0", "ttyACM0", "video0", "ttyS0", "ttyUSB9", "video\nx"} {
		write(t, filepath.Join(dev, n), "")
	}
	// the drm device: class link → devices, with vendor and a driver link
	write(t, filepath.Join(sys, "devices/pci0000:00/0000:00:02.0/vendor"), "0x8086\n")
	link(t, "../../../bus/pci/drivers/i915", filepath.Join(sys, "devices/pci0000:00/0000:00:02.0/driver"))
	link(t, "../../devices/pci0000:00/0000:00:02.0/drm/renderD128", filepath.Join(sys, "class/drm/renderD128"))
	if err := os.MkdirAll(filepath.Join(sys, "devices/pci0000:00/0000:00:02.0/drm/renderD128"), 0o755); err != nil {
		t.Fatal(err)
	}
	link(t, "../../../0000:00:02.0", filepath.Join(sys, "devices/pci0000:00/0000:00:02.0/drm/renderD128/device"))
	// the USB serial dongle: product and manufacturer two folders above the tty's interface
	usb := filepath.Join(sys, "devices/pci0000:00/usb1/1-1")
	write(t, filepath.Join(usb, "product"), "CP2102N USB to UART Bridge Controller\n")
	write(t, filepath.Join(usb, "manufacturer"), "Silicon Labs\n")
	if err := os.MkdirAll(filepath.Join(usb, "1-1:1.0/ttyUSB0/tty/ttyUSB0"), 0o755); err != nil {
		t.Fatal(err)
	}
	link(t, "../../devices/pci0000:00/usb1/1-1/1-1:1.0/ttyUSB0/tty/ttyUSB0", filepath.Join(sys, "class/tty/ttyUSB0"))
	link(t, "../../../ttyUSB0", filepath.Join(usb, "1-1:1.0/ttyUSB0/tty/ttyUSB0/device"))
	// a device link that leads out of /sys is not read
	link(t, "/etc", filepath.Join(sys, "class/tty/ttyACM0/device"))
	write(t, filepath.Join(sys, "class/video4linux/video0/name"), "HD Webcam C270\x07\n")
	link(t, "../../ttyUSB0", filepath.Join(dev, "serial/by-id/usb-Silicon_Labs_CP2102N_0001-if00-port0"))
	return Roots{Dev: dev, Sys: sys}
}

func TestDevices(t *testing.T) {
	roots := deviceTree(t)
	old := isCharDevice
	defer func() { isCharDevice = old }()
	// the tree's files stand for device nodes, except the one named like a device that is not one
	isCharDevice = func(info fs.FileInfo) bool { return info.Mode().IsRegular() && info.Name() != "ttyUSB9" }

	got := Devices(roots)
	want := []Device{
		{Path: "/dev/dri/renderD128", Node: "/dev/dri/renderD128", Kind: "render", Name: "Intel graphics (i915)"},
		{Path: "/dev/serial/by-id/usb-Silicon_Labs_CP2102N_0001-if00-port0", Node: "/dev/ttyUSB0", Kind: "serial", Name: "Silicon Labs CP2102N USB to UART Bridge Controller"},
		{Path: "/dev/ttyACM0", Node: "/dev/ttyACM0", Kind: "serial", Name: ""},
		{Path: "/dev/video0", Node: "/dev/video0", Kind: "video", Name: "HD Webcam C270"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d devices, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("device %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDevicesNeedCharacterNodes(t *testing.T) {
	roots := deviceTree(t)
	// the real rule: regular files of the tree are not devices
	if got := Devices(roots); len(got) != 0 {
		t.Fatalf("regular files listed as devices: %+v", got)
	}
}

func TestDevicesAreBounded(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 50; i++ {
		write(t, filepath.Join(root, "dev", "ttyUSB"+strconv.Itoa(i)), "")
	}
	old := isCharDevice
	defer func() { isCharDevice = old }()
	isCharDevice = func(info fs.FileInfo) bool { return true }
	if got := Devices(Roots{Dev: filepath.Join(root, "dev"), Sys: filepath.Join(root, "sys")}); len(got) != maxPerKind {
		t.Fatalf("got %d, want at most %d", len(got), maxPerKind)
	}
	if got := cleanName(strings.Repeat("x", 300)); len(got) != maxName {
		t.Fatalf("name not bounded: %d", len(got))
	}
}

func TestResolvers(t *testing.T) {
	dir := t.TempDir()
	etc, resolved := filepath.Join(dir, "resolv.conf"), filepath.Join(dir, "resolved.conf")
	write(t, etc, "# comment\nnameserver 192.168.1.1\nnameserver fe80::1%eth0\nnameserver nonsense\nsearch example.org\n")
	if got, source := Resolvers(etc, resolved); strings.Join(got, ",") != "192.168.1.1,fe80::1" || source != "resolv.conf" {
		t.Fatalf("got %v %q", got, source)
	}
	write(t, etc, "nameserver 127.0.0.53\noptions edns0\n")
	write(t, resolved, "nameserver 192.0.2.53\nnameserver 198.51.100.53\n")
	if got, source := Resolvers(etc, resolved); strings.Join(got, ",") != "192.0.2.53,198.51.100.53" || source != "systemd-resolved" {
		t.Fatalf("loopback only: got %v %q", got, source)
	}
	os.Remove(resolved)
	if got, source := Resolvers(etc, resolved); len(got) != 0 || source != "" {
		t.Fatalf("none usable: got %v %q", got, source)
	}
	many := ""
	for i := 1; i < 20; i++ {
		many += "nameserver 192.0.2." + strconv.Itoa(i) + "\n"
	}
	if got := ParseResolvConf(strings.NewReader(many)); len(got) != maxResolvers {
		t.Fatalf("not bounded: %d", len(got))
	}
}

func TestGateway(t *testing.T) {
	table := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"wlan0\t00000000\t0101A8C0\t0003\t0\t0\t600\t00000000\t0\t0\t0\n" +
		"eth0\t00000000\t0102A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
		"eth0\t0001A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n"
	if got := ParseGateway(strings.NewReader(table)); got != "192.168.2.1" {
		t.Fatalf("got %q", got)
	}
	if got := ParseGateway(strings.NewReader("Iface\tDestination\tGateway\neth0\t0001A8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\n")); got != "" {
		t.Fatalf("no default route: got %q", got)
	}
	if got := Gateway(filepath.Join(t.TempDir(), "none")); got != "" {
		t.Fatalf("no file: %q", got)
	}
}

// fakeInfo is a FileInfo with the owner and mode a test wants
type fakeInfo struct {
	mode     fs.FileMode
	uid, gid uint32
}

func (f fakeInfo) Name() string       { return "x" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() interface{}   { return &syscall.Stat_t{Uid: f.uid, Gid: f.gid} }

func TestCanWrite(t *testing.T) {
	dir := fs.ModeDir
	cases := []struct {
		info fakeInfo
		user RunAs
		want string
	}{
		{fakeInfo{dir | 0o755, 1000, 1000}, RunAs{UID: 1000, GID: 1000, Known: true}, "yes"},
		{fakeInfo{dir | 0o755, 0, 0}, RunAs{UID: 1000, GID: 1000, Known: true}, "no"},
		{fakeInfo{dir | 0o775, 0, 1000}, RunAs{UID: 1000, GID: 1000, Known: true}, "yes"},
		{fakeInfo{dir | 0o777, 0, 0}, RunAs{UID: 1000, GID: 1000, Known: true}, "yes"},
		{fakeInfo{dir | 0o555, 1000, 1000}, RunAs{UID: 1000, GID: 1000, Known: true}, "no"},
		{fakeInfo{dir | 0o700, 1001, 1001}, RunAs{UID: 0, GID: 0, Known: true}, "yes"},
		{fakeInfo{dir | 0o755, 1000, 1000}, RunAs{Known: false, Text: "node"}, "unknown"},
		{fakeInfo{dir | 0o770, 0, 1000}, RunAs{UID: 1000, GID: -1, Known: true}, "no"},
	}
	for i, c := range cases {
		if got := canWrite(c.info, c.user); got != c.want {
			t.Errorf("case %d: got %q, want %q", i, got, c.want)
		}
	}
}

func TestInspectFolder(t *testing.T) {
	root := t.TempDir()
	me := RunAs{UID: os.Getuid(), GID: os.Getgid(), Known: true}
	owner := strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
	for _, d := range []string{"mnt/data/app/config", "mnt/data/other", "srv/elsewhere", "opt/cosmos-data/app"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(root, "mnt/data/app/file.txt"), "x")
	if err := os.Chmod(filepath.Join(root, "mnt/data/other"), 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(root, "mnt/data/other"), 0o755)
	link(t, "config", filepath.Join(root, "mnt/data/app/inside"))                  // relative, inside
	link(t, "/mnt/data/app/config", filepath.Join(root, "mnt/data/app/absinside")) // absolute, inside
	link(t, "../../../srv/elsewhere", filepath.Join(root, "mnt/data/app/escape"))  // relative, out of the disk
	link(t, "/etc", filepath.Join(root, "mnt/data/app/etc"))                       // absolute, out
	link(t, "loop2", filepath.Join(root, "mnt/data/app/loop1"))                    // a loop
	link(t, "loop1", filepath.Join(root, "mnt/data/app/loop2"))
	places := []string{"/mnt/data", "/opt/cosmos-data", "/"}

	cases := []struct {
		source, state, writable string
	}{
		{"/mnt/data/app/config", "ok", "yes"},
		{"/mnt/data/app/config/", "ok", "yes"},
		{"/mnt/data/app/inside", "ok", "yes"},
		{"/mnt/data/app/absinside", "ok", "yes"},
		{"/mnt/data/app/new/deeper", "missing", ""},
		{"/mnt/data/app/file.txt", "file", ""},
		{"/mnt/data/app/escape", "link", ""},
		{"/mnt/data/app/escape/x", "link", ""},
		{"/mnt/data/app/etc", "link", ""},
		{"/mnt/data/app/loop1", "link", ""},
		{"/srv/elsewhere", "outside", ""},
		{"/etc", "outside", ""},
		{"/mnt/data/../etc", "invalid", ""},
		{"relative/path", "invalid", ""},
		{"/opt/cosmos-data/app", "ok", "yes"},
	}
	if os.Getuid() != 0 {
		// root writes everywhere: a read-only folder is a "no" only for another user
		cases = append(cases, struct{ source, state, writable string }{"/mnt/data/other", "ok", "no"})
	}
	for _, c := range cases {
		got := InspectFolder(root, c.source, places, me)
		if got.State != c.state || got.Writable != c.writable {
			t.Errorf("%s: got state %q writable %q, want %q %q", c.source, got.State, got.Writable, c.state, c.writable)
		}
		if c.state == "ok" && (got.Owner != owner || got.Mode == "" || got.Total == 0 || got.Place == "") {
			t.Errorf("%s: facts missing: %+v", c.source, got)
		}
		if (c.state == "outside" || c.state == "link" || c.state == "invalid") && got.Owner != "" {
			t.Errorf("%s: looked at what it must not: %+v", c.source, got)
		}
	}
	// "/" is never a data place: nothing under it is looked at through it
	if got := InspectFolder(root, "/srv/elsewhere", []string{"/"}, me); got.State != "outside" {
		t.Errorf("/ taken as a place: %+v", got)
	}
}

func TestEffectiveUser(t *testing.T) {
	cases := []struct {
		user, image string
		env         []string
		ls          bool
		want        RunAs
	}{
		{"", "", nil, false, RunAs{UID: 0, GID: 0, Known: true, Source: "root"}},
		{"1000:1000", "1000:1000", nil, false, RunAs{UID: 1000, GID: 1000, Known: true, Source: "image", Text: "1000:1000"}},
		{"1001", "", nil, false, RunAs{UID: 1001, GID: -1, Known: true, Source: "user", Text: "1001"}},
		{"node", "node", nil, false, RunAs{GID: -1, Known: false, Source: "image", Text: "node"}},
		{"root", "", nil, false, RunAs{UID: 0, GID: 0, Known: true, Source: "user", Text: "root"}},
		{"", "", []string{"PUID=1000", "PGID=1001", "TZ=Etc/UTC"}, true, RunAs{UID: 1000, GID: 1001, Known: true, Source: "puid", Text: "1000:1001"}},
		{"", "", []string{"TZ=Etc/UTC"}, true, RunAs{UID: 911, GID: 911, Known: true, Source: "puidDefault", Text: "911:911"}},
		{"", "", []string{"PUID=abc"}, true, RunAs{UID: 911, GID: 911, Known: false, Source: "puid", Text: "911:911"}},
		// a user set on a linuxserver image wins (and breaks its start, which the panel says)
		{"1000:1000", "", []string{"PUID=1000"}, true, RunAs{UID: 1000, GID: 1000, Known: true, Source: "user", Text: "1000:1000"}},
	}
	for i, c := range cases {
		if got := EffectiveUser(c.user, c.image, c.env, c.ls); got != c.want {
			t.Errorf("case %d: got %+v, want %+v", i, got, c.want)
		}
	}
	if !LinuxserverStyle("lscr.io/linuxserver/radarr:latest", nil) || !LinuxserverStyle("linuxserver/sonarr", nil) ||
		!LinuxserverStyle("ghcr.io/hotio/qbittorrent", nil) || LinuxserverStyle("jellyfin/jellyfin", nil) ||
		!LinuxserverStyle("example/app", []string{"TZ", "PUID"}) {
		t.Error("linuxserver.io-style images not told apart")
	}
	folders := []Folder{{State: "ok", Owner: "0:0"}, {State: "ok", Owner: "1000:1000"}, {State: "missing", Owner: "1001:1001"}}
	if got := CommonOwner(folders); got != "1000:1000" {
		t.Errorf("common owner: %q", got)
	}
	if got := CommonOwner([]Folder{{State: "ok", Owner: "0:0"}}); got != "0:0" {
		t.Errorf("only root's: %q", got)
	}
}

func TestProposeHostPort(t *testing.T) {
	taken := map[int]bool{8080: true, 8081: true, 80: true}
	is := func(port int, protocol string) bool { return protocol == "tcp" && taken[port] }
	cases := []struct {
		wanted   int
		protocol string
		want     int
	}{
		// free: the same number; taken: the next free; another protocol is apart;
		// 80 taken: from 8080 (taken with 8081); out of range: nothing
		{8096, "tcp", 8096}, {8080, "tcp", 8082}, {8080, "udp", 8080}, {80, "tcp", 8082}, {0, "tcp", 0}, {70000, "tcp", 0},
	}
	for _, c := range cases {
		if got := ProposeHostPort(c.wanted, c.protocol, is); got != c.want {
			t.Errorf("%d/%s: got %d, want %d", c.wanted, c.protocol, got, c.want)
		}
	}
	tries := 0
	all := func(int, string) bool { tries++; return true }
	if got := ProposeHostPort(9000, "tcp", all); got != 0 || tries > scanWidth+1 {
		t.Errorf("everything taken: got %d after %d tries", got, tries)
	}
}

func TestMemoryFromHint(t *testing.T) {
	const mb, gb = 1 << 20, 1 << 30
	cases := map[string]int64{
		"256 MB to 1 GB":                   gb,
		"512 MB to 2 GB":                   2 * gb,
		"under 64 MB":                      64 * mb,
		"512 MB and more":                  1024 * mb,
		"64 MB and more, by what it holds": 128 * mb,
		"256 MB":                           256 * mb,
		"64 MB (its -m setting)":           64 * mb,
		"2 GB and more":                    4 * gb,
		"":                                 0,
		"depends":                          0,
	}
	for hint, want := range cases {
		if got := MemoryFromHint(hint); got != want {
			t.Errorf("%q: got %d, want %d", hint, got, want)
		}
	}
}
