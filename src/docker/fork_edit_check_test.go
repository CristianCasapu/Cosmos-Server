package docker

import (
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/edithints"
	"github.com/docker/docker/api/types"
	containerType "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"
)

// the shape of the owner's apps: a linuxserver.io image, binds under a data
// disk (one written as a bind string, one as a mount), a port published on
// every address, a device, a user network, DNS of its own
func ownersApp() types.ContainerJSON {
	return types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{
			Name: "/radarr",
			HostConfig: &containerType.HostConfig{
				Binds:         []string{"/mnt/data/radarr/config:/config", "/mnt/data/media:/media:ro"},
				Mounts:        []mount.Mount{{Type: mount.TypeBind, Source: "/srv/odd place/downloads", Target: "/downloads"}},
				PortBindings:  nat.PortMap{"7878/tcp": {{HostIP: "", HostPort: "7878"}}, "9000/udp": {{HostIP: "0.0.0.0", HostPort: "9000-9001"}}},
				NetworkMode:   "cosmos-network-radarr",
				DNS:           []string{"odd-but-saved"},
				RestartPolicy: containerType.RestartPolicy{Name: "unless-stopped"},
				Resources:     containerType.Resources{NanoCPUs: 64e9, Devices: []containerType.DeviceMapping{{PathOnHost: "/dev/old-name", PathInContainer: "/dev/old-name"}}},
			},
		},
		Config: &containerType.Config{
			Image: "lscr.io/linuxserver/radarr:latest",
			User:  "weird user",
			Env:   []string{"PUID=1000", "PGID=1000", "TZ=Etc/UTC"},
		},
	}
}

// edited copies the container through the update route's own steps: the
// tabs send binds back as mounts
func edited(c types.ContainerJSON, change func(*types.ContainerJSON)) types.ContainerJSON {
	host := *c.HostConfig
	config := *c.Config
	copyOf := types.ContainerJSON{ContainerJSONBase: &types.ContainerJSONBase{Name: c.Name, HostConfig: &host}, Config: &config}
	copyOf.HostConfig.Mounts = mountsOf(c)
	copyOf.HostConfig.Binds = []string{}
	copyOf.HostConfig.PortBindings = nat.PortMap{}
	for k, v := range c.HostConfig.PortBindings {
		copyOf.HostConfig.PortBindings[k] = append([]nat.PortBinding{}, v...)
	}
	copyOf.HostConfig.Devices = append([]containerType.DeviceMapping{}, c.HostConfig.Devices...)
	copyOf.HostConfig.DNS = append([]string{}, c.HostConfig.DNS...)
	if change != nil {
		change(&copyOf)
	}
	return copyOf
}

func withServer(t *testing.T) {
	t.Helper()
	oldAddresses, oldDevice, oldCPUs := serverAddresses, deviceExists, serverCPUs
	t.Cleanup(func() { serverAddresses, deviceExists, serverCPUs = oldAddresses, oldDevice, oldCPUs })
	serverAddresses = func() []net.IP {
		return []net.IP{net.ParseIP("192.168.1.10"), net.ParseIP("127.0.0.1"), net.ParseIP("fd00::10")}
	}
	deviceExists = func(p string) bool { return p == "/dev/dri/renderD128" || p == "/dev/serial/by-id/usb-dongle" }
	serverCPUs = func() int { return 4 }
}

func TestEditUnchangedSaves(t *testing.T) {
	withServer(t)
	before := ownersApp()
	// odd but valid settings that saved before are not refused, whatever the tab sends back
	if err := checkEdit(before, edited(before, nil)); err != nil {
		t.Fatalf("an unchanged container is refused: %v", err)
	}
	// the Network tab sends the address Docker reports for "every address"
	if err := checkEdit(before, edited(before, func(c *types.ContainerJSON) {
		c.HostConfig.PortBindings["7878/tcp"] = []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "7878"}}
	})); err != nil {
		t.Fatalf("every address refused: %v", err)
	}
	// a harmless edit next to the odd values
	if err := checkEdit(before, edited(before, func(c *types.ContainerJSON) {
		c.Config.Env = append(c.Config.Env, "UMASK=002")
		c.HostConfig.RestartPolicy.Name = "always"
	})); err != nil {
		t.Fatalf("a harmless edit refused: %v", err)
	}
}

func TestEditAccepts(t *testing.T) {
	withServer(t)
	before := ownersApp()
	cases := map[string]func(*types.ContainerJSON){
		"the home-network address": func(c *types.ContainerJSON) {
			c.HostConfig.PortBindings["8080/tcp"] = []nat.PortBinding{{HostIP: "192.168.1.10", HostPort: "8080"}}
		},
		"this server only": func(c *types.ContainerJSON) {
			c.HostConfig.PortBindings["8081/tcp"] = []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: "8081"}}
		},
		"every address, IPv6": func(c *types.ContainerJSON) {
			c.HostConfig.PortBindings["8082/udp"] = []nat.PortBinding{{HostIP: "::", HostPort: "8082"}}
		},
		"an address of the server in brackets": func(c *types.ContainerJSON) {
			c.HostConfig.PortBindings["8083/tcp"] = []nat.PortBinding{{HostIP: "[fd00::10]", HostPort: "8083"}}
		},
		"a declared folder on the data disk": func(c *types.ContainerJSON) {
			c.HostConfig.Mounts = append(c.HostConfig.Mounts, mount.Mount{Type: mount.TypeBind, Source: "/mnt/data/radarr/cache/", Target: "/cache", ReadOnly: true})
		},
		"a named volume": func(c *types.ContainerJSON) {
			c.HostConfig.Mounts = append(c.HostConfig.Mounts, mount.Mount{Type: mount.TypeVolume, Source: "radarr_data", Target: "/data"})
		},
		"a tmpfs": func(c *types.ContainerJSON) {
			c.HostConfig.Mounts = append(c.HostConfig.Mounts, mount.Mount{Type: mount.TypeTmpfs, Target: "/tmp/cache"})
		},
		"a render node": func(c *types.ContainerJSON) {
			c.HostConfig.Devices = append(c.HostConfig.Devices, containerType.DeviceMapping{PathOnHost: "/dev/dri/renderD128", PathInContainer: "/dev/dri/renderD128"})
		},
		"a dongle by its stable name": func(c *types.ContainerJSON) {
			c.HostConfig.Devices = append(c.HostConfig.Devices, containerType.DeviceMapping{PathOnHost: "/dev/serial/by-id/usb-dongle", PathInContainer: "/dev/ttyUSB0"})
		},
		"DNS of the router": func(c *types.ContainerJSON) { c.HostConfig.DNS = append(c.HostConfig.DNS, "192.168.1.1") },
		"a numeric user":    func(c *types.ContainerJSON) { c.Config.User = "1000:1000" },
		"no user":           func(c *types.ContainerJSON) { c.Config.User = "" },
		"host network":      func(c *types.ContainerJSON) { c.HostConfig.NetworkMode = "host" },
		"another app's network": func(c *types.ContainerJSON) {
			c.HostConfig.NetworkMode = "container:gluetun"
		},
		"two CPUs": func(c *types.ContainerJSON) { c.HostConfig.NanoCPUs = 2e9 },
	}
	for name, change := range cases {
		if err := checkEdit(before, edited(before, change)); err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}
}

func TestEditRefuses(t *testing.T) {
	withServer(t)
	before := ownersApp()
	cases := map[string]struct {
		change func(*types.ContainerJSON)
		says   string
	}{
		"an address of another machine": {func(c *types.ContainerJSON) {
			c.HostConfig.PortBindings["8080/tcp"] = []nat.PortBinding{{HostIP: "192.168.1.99", HostPort: "8080"}}
		}, "not an address of this server"},
		"not an address": {func(c *types.ContainerJSON) {
			c.HostConfig.PortBindings["8080/tcp"] = []nat.PortBinding{{HostIP: "home", HostPort: "8080"}}
		}, "not an IP address"},
		"a host port out of range": {func(c *types.ContainerJSON) {
			c.HostConfig.PortBindings["8080/tcp"] = []nat.PortBinding{{HostPort: "70000"}}
		}, "host port 70000"},
		"a protocol": {func(c *types.ContainerJSON) {
			c.HostConfig.PortBindings["8080/icmp"] = []nat.PortBinding{{HostPort: "8080"}}
		}, "TCP or UDP"},
		"a changed address of an existing port": {func(c *types.ContainerJSON) {
			c.HostConfig.PortBindings["7878/tcp"] = []nat.PortBinding{{HostIP: "203.0.113.7", HostPort: "7878"}}
		}, "203.0.113.7"},
		"a relative bind": {func(c *types.ContainerJSON) {
			c.HostConfig.Mounts = append(c.HostConfig.Mounts, mount.Mount{Type: mount.TypeBind, Source: "data/radarr", Target: "/data"})
		}, "absolute path"},
		"a bind with ..": {func(c *types.ContainerJSON) {
			c.HostConfig.Mounts = append(c.HostConfig.Mounts, mount.Mount{Type: mount.TypeBind, Source: "/mnt/data/../../etc", Target: "/data"})
		}, "without \"..\""},
		"a target with ..": {func(c *types.ContainerJSON) {
			c.HostConfig.Mounts = append(c.HostConfig.Mounts, mount.Mount{Type: mount.TypeBind, Source: "/mnt/data/x", Target: "/config/../etc"})
		}, "in the container"},
		"a bind with a newline": {func(c *types.ContainerJSON) {
			c.HostConfig.Mounts = append(c.HostConfig.Mounts, mount.Mount{Type: mount.TypeBind, Source: "/mnt/data/x\n", Target: "/x"})
		}, "absolute path"},
		"a changed source of an existing mount": {func(c *types.ContainerJSON) {
			for i := range c.HostConfig.Mounts {
				if c.HostConfig.Mounts[i].Target == "/downloads" {
					c.HostConfig.Mounts[i].Source = "/srv/../etc"
				}
			}
		}, "without \"..\""},
		"a volume name": {func(c *types.ContainerJSON) {
			c.HostConfig.Mounts = append(c.HostConfig.Mounts, mount.Mount{Type: mount.TypeVolume, Source: "../x", Target: "/x"})
		}, "volume name"},
		"a device outside /dev": {func(c *types.ContainerJSON) {
			c.HostConfig.Devices = append(c.HostConfig.Devices, containerType.DeviceMapping{PathOnHost: "/etc/shadow", PathInContainer: "/dev/x"})
		}, "under /dev"},
		"a device with ..": {func(c *types.ContainerJSON) {
			c.HostConfig.Devices = append(c.HostConfig.Devices, containerType.DeviceMapping{PathOnHost: "/dev/../etc/shadow", PathInContainer: "/dev/x"})
		}, "under /dev"},
		"a device that does not exist": {func(c *types.ContainerJSON) {
			c.HostConfig.Devices = append(c.HostConfig.Devices, containerType.DeviceMapping{PathOnHost: "/dev/ttyUSB7", PathInContainer: "/dev/ttyUSB7"})
		}, "does not exist"},
		"a device to a relative path": {func(c *types.ContainerJSON) {
			c.HostConfig.Devices = append(c.HostConfig.Devices, containerType.DeviceMapping{PathOnHost: "/dev/dri/renderD128", PathInContainer: "renderD128"})
		}, "must be absolute"},
		"a DNS name": {func(c *types.ContainerJSON) { c.HostConfig.DNS = append(c.HostConfig.DNS, "dns.example.org") }, "not an IP address"},
		"too many DNS": {func(c *types.ContainerJSON) {
			c.HostConfig.DNS = strings.Split("192.0.2.1,192.0.2.2,192.0.2.3,192.0.2.4,192.0.2.5,192.0.2.6,192.0.2.7,192.0.2.8,192.0.2.9", ",")
		}, "more than 8"},
		"a user with a space":       {func(c *types.ContainerJSON) { c.Config.User = "my user" }, "user \"my user\""},
		"a network mode":            {func(c *types.ContainerJSON) { c.HostConfig.NetworkMode = "container:" }, "network mode"},
		"a restart policy":          {func(c *types.ContainerJSON) { c.HostConfig.RestartPolicy.Name = "sometimes" }, "restart"},
		"more CPUs than the server": {func(c *types.ContainerJSON) { c.HostConfig.NanoCPUs = 8e9 }, "this server has 4"},
	}
	for name, c := range cases {
		err := checkEdit(before, edited(before, c.change))
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: got %v, want a refusal saying %q", name, err, c.says)
		}
	}
}

func TestEditAddressesUnknownInAContainer(t *testing.T) {
	withServer(t)
	serverAddresses = func() []net.IP { return nil }
	before := ownersApp()
	// Cosmos in a container cannot know the server's addresses: any IP address goes to Docker, which decides
	if err := checkEdit(before, edited(before, func(c *types.ContainerJSON) {
		c.HostConfig.PortBindings["8080/tcp"] = []nat.PortBinding{{HostIP: "192.168.1.99", HostPort: "8080"}}
	})); err != nil {
		t.Fatalf("refused: %v", err)
	}
}

func TestPrepareBindFolders(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "mnt/data/radarr/config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "srv"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPlaces, oldRemap := EditDataPlaces, UsernsState
	defer func() { EditDataPlaces, UsernsState = oldPlaces, oldRemap }()
	EditDataPlaces = func() []string { return []string{"/mnt/data"} }
	UsernsState = func() (bool, bool) { return false, true }
	owned := recordChowns(t, root)
	before := ownersApp()
	before.Config.User = ""
	after := edited(before, func(c *types.ContainerJSON) {
		c.HostConfig.Mounts = append(c.HostConfig.Mounts,
			mount.Mount{Type: mount.TypeBind, Source: "/mnt/data/radarr/cache/thumbs", Target: "/cache"},
			mount.Mount{Type: mount.TypeBind, Source: "/srv/new", Target: "/new"}, // not under a data place: left to Docker
		)
	})
	forkPrepareBindFolders(before, after, root)
	if owned["/mnt/data/radarr/cache"] != "1000:1000" || owned["/mnt/data/radarr/cache/thumbs"] != "1000:1000" || len(owned) != 2 {
		t.Fatalf("owners: %v", owned)
	}
	if _, err := os.Stat(filepath.Join(root, "mnt/data/radarr/cache/thumbs")); err != nil {
		t.Fatal("not made")
	}
	if _, err := os.Stat(filepath.Join(root, "srv/new")); err == nil {
		t.Fatal("a folder outside the data places was made")
	}
	// a data path that does not exist yet: made (the administrator's), the app's folder below it the app's
	clear(owned)
	EditDataPlaces = func() []string { return []string{"/mnt/data", "/cosmos-storage"} }
	fresh := edited(before, func(c *types.ContainerJSON) {
		c.HostConfig.Mounts = append(c.HostConfig.Mounts, mount.Mount{Type: mount.TypeBind, Source: "/cosmos-storage/radarr/data", Target: "/data"})
	})
	forkPrepareBindFolders(before, fresh, root)
	if _, err := os.Stat(filepath.Join(root, "cosmos-storage/radarr/data")); err != nil || owned["/cosmos-storage"] != "" || owned["/cosmos-storage/radarr/data"] != "1000:1000" {
		t.Fatalf("a missing data path: %v %v", err, owned)
	}
	// an app that runs as root: the folder is made here too (through handles), and stays root's
	clear(owned)
	rootApp := edited(before, func(c *types.ContainerJSON) {
		c.Config.Image = "nginx:1"
		c.Config.Env = nil
		c.HostConfig.Mounts = append(c.HostConfig.Mounts, mount.Mount{Type: mount.TypeBind, Source: "/mnt/data/web", Target: "/web"})
	})
	forkPrepareBindFolders(before, rootApp, root)
	if len(owned) != 0 {
		t.Fatalf("root's app: %v", owned)
	}
	if _, err := os.Stat(filepath.Join(root, "mnt/data/web")); err != nil {
		t.Fatal("not made for root's app")
	}
}

func TestEditKeepsRestartRetries(t *testing.T) {
	before := ownersApp()
	before.HostConfig.RestartPolicy = containerType.RestartPolicy{Name: "on-failure", MaximumRetryCount: 3}
	after := edited(before, func(c *types.ContainerJSON) {
		c.HostConfig.RestartPolicy = containerType.RestartPolicy{Name: "on-failure"}
	})
	keepRestartRetries(&before, &after)
	if after.HostConfig.RestartPolicy.MaximumRetryCount != 3 {
		t.Fatalf("tries lost: %+v", after.HostConfig.RestartPolicy)
	}
	changed := edited(before, func(c *types.ContainerJSON) { c.HostConfig.RestartPolicy = containerType.RestartPolicy{Name: "always"} })
	keepRestartRetries(&before, &changed)
	if changed.HostConfig.RestartPolicy.MaximumRetryCount != 0 {
		t.Fatalf("tries kept for another policy: %+v", changed.HostConfig.RestartPolicy)
	}
}

// recordChowns replaces the fchown (the tests do not run as root) and
// records, by path on the server, which folders were given to whom.
func recordChowns(t *testing.T, root string) map[string]string {
	t.Helper()
	old := chownHandle
	t.Cleanup(func() { chownHandle = old })
	owned := map[string]string{}
	chownHandle = func(fd int, uid, gid int) error {
		p, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(fd))
		if err != nil {
			t.Errorf("no path for the handle: %v", err)
		}
		owned[strings.TrimPrefix(p, root)] = strconv.Itoa(uid) + ":" + strconv.Itoa(gid)
		return nil
	}
	return owned
}

// A link an app planted in its own data folder never leads the creation or
// the chown out of the data place, and a folder that exists is left alone.
func TestMakeOwnedFoldersStaysInThePlace(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"mnt/data/app/config", "outside"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// planted by the app (it can write in /config): out of the place, absolute and relative, and one inside it
	for target, at := range map[string]string{
		"/outside":            "mnt/data/app/config/abs",
		"../../../../outside": "mnt/data/app/config/rel",
		"../config":           "mnt/data/app/config/inside",
	} {
		if err := os.Symlink(target, filepath.Join(root, at)); err != nil {
			t.Fatal(err)
		}
	}
	owned := recordChowns(t, root)

	for _, source := range []string{"/mnt/data/app/config/abs/new", "/mnt/data/app/config/rel/new", "/mnt/data/app/config/rel"} {
		if err := makeOwnedFolders(root, "/mnt/data", source, 1000, 1000); err == nil && strings.Contains(source, "new") {
			t.Errorf("%s: no refusal through a link out of the place", source)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "outside")); len(entries) != 0 {
		t.Fatalf("something was made outside the data place: %v", entries)
	}
	if len(owned) != 0 {
		t.Fatalf("chowned through a link: %v", owned)
	}
	// with the absolute link read as the server sees it: /outside of the real machine is not touched either
	if _, err := os.Stat("/outside/new"); err == nil {
		t.Fatal("made on the machine itself")
	}

	// a link, even one that stays inside the place, is not followed by the creation: the
	// update route hands it the path without links (resolveNewBinds)
	if err := makeOwnedFolders(root, "/mnt/data", "/mnt/data/app/config/inside/cache", 1000, 1001); err == nil {
		t.Fatal("made through a link")
	}
	if _, err := os.Stat(filepath.Join(root, "mnt/data/app/config/cache")); err == nil || len(owned) != 0 {
		t.Fatalf("made through the link inside the place: %v", owned)
	}
	// two levels: each made and given through its own handle
	if err := makeOwnedFolders(root, "/mnt/data", "/mnt/data/app/new/deeper", 1000, 1001); err != nil {
		t.Fatal(err)
	}
	if len(owned) != 2 || owned["/mnt/data/app/new"] != "1000:1001" || owned["/mnt/data/app/new/deeper"] != "1000:1001" {
		t.Fatalf("owners: %v", owned)
	}
	// root's app: made, not chowned
	clear(owned)
	if err := makeOwnedFolders(root, "/mnt/data", "/mnt/data/app/rootfolder", 0, 0); err != nil || len(owned) != 0 {
		t.Fatalf("root's folder: %v %v", err, owned)
	}

	// existing folders (the place, app, config) are left as they are: no chown, mode kept
	clear(owned)
	if err := os.Chmod(filepath.Join(root, "mnt/data/app/config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := makeOwnedFolders(root, "/mnt/data", "/mnt/data/app/config/thumbs", 1000, 1000); err != nil {
		t.Fatal(err)
	}
	if len(owned) != 1 || owned["/mnt/data/app/config/thumbs"] != "1000:1000" {
		t.Fatalf("an existing folder was chowned: %v", owned)
	}
	if info, _ := os.Stat(filepath.Join(root, "mnt/data/app/config")); info.Mode().Perm() != 0o700 {
		t.Fatalf("an existing folder was changed: %v", info.Mode())
	}
	clear(owned)
	if err := makeOwnedFolders(root, "/mnt/data", "/mnt/data/app/config", 1000, 1000); err != nil || len(owned) != 0 {
		t.Fatalf("a folder that exists: %v %v", err, owned)
	}
	// not below the place
	if err := makeOwnedFolders(root, "/mnt/data", "/mnt/data", 1000, 1000); err == nil {
		t.Fatal("the place itself accepted")
	}
	if err := makeOwnedFolders(root, "/mnt/data", "/mnt/database/x", 1000, 1000); err == nil {
		t.Fatal("a sibling of the place accepted")
	}
}

// The route's own path: a planted link in the bind's path is not walked by
// the detection either (state "link"): nothing is made, nothing chowned.
func TestPrepareBindFoldersPlantedLink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "mnt/data/radarr/config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc", filepath.Join(root, "mnt/data/radarr/config/escape")); err != nil {
		t.Fatal(err)
	}
	oldPlaces := EditDataPlaces
	defer func() { EditDataPlaces = oldPlaces }()
	EditDataPlaces = func() []string { return []string{"/mnt/data"} }
	owned := recordChowns(t, root)
	before := ownersApp()
	before.Config.User = ""
	after := edited(before, func(c *types.ContainerJSON) {
		c.HostConfig.Mounts = append(c.HostConfig.Mounts, mount.Mount{Type: mount.TypeBind, Source: "/mnt/data/radarr/config/escape/cron.d", Target: "/x"})
	})
	forkPrepareBindFolders(before, after, root)
	if entries, _ := os.ReadDir(filepath.Join(root, "etc")); len(entries) != 0 || len(owned) != 0 {
		t.Fatalf("followed the planted link: %v %v", entries, owned)
	}
}

// A new bind is followed through its links as Docker will follow them: a
// link planted at any level that leads out of the data places is refused, one
// that leads to a sensitive folder is handed on as that folder (the tick then
// names it), one that leads to another data place is used by its real path.
func TestNewBindsAreResolved(t *testing.T) {
	withServer(t)
	root := t.TempDir()
	for _, d := range []string{"mnt/data/app/config", "mnt/other/movies", "srv/elsewhere", "etc"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	links := map[string]string{
		"mnt/data/app/config/escape": "/srv/elsewhere",    // the deepest level, absolute
		"mnt/data/app/up":            "../../../srv",      // a middle level, relative
		"mnt/data/app/config/etc":    "/etc",              // to a sensitive folder
		"mnt/data/app/config/rootfs": "/",                 // to the whole server
		"mnt/data/app/config/movies": "/mnt/other/movies", // to another data place
		"mnt/data/app/config/inside": "../config",         // inside the same place
		"mnt/linked":                 "/mnt/data",         // above the place
	}
	for at, target := range links {
		if err := os.Symlink(target, filepath.Join(root, at)); err != nil {
			t.Fatal(err)
		}
	}
	oldResolve, oldPlaces := resolveSource, EditDataPlaces
	t.Cleanup(func() { resolveSource, EditDataPlaces = oldResolve, oldPlaces })
	resolveSource = func(p string) (string, bool, error) { return edithints.ResolvePath(root, p) }
	EditDataPlaces = func() []string { return []string{"/mnt/data", "/mnt/other"} }

	before := ownersApp()
	cases := []struct {
		source, want, refused string
	}{
		{"/mnt/data/app/config/escape/new", "", "is a link to /srv/elsewhere/new"},
		{"/mnt/data/app/up/elsewhere", "", "is a link to /srv/elsewhere"},
		{"/mnt/data/app/config/etc/cron.d", "/etc/cron.d", ""},
		{"/mnt/data/app/config/rootfs", "/", ""},
		{"/mnt/data/app/config/movies/new", "/mnt/other/movies/new", ""},
		{"/mnt/data/app/config/inside/cache", "/mnt/data/app/config/cache", ""},
		{"/mnt/linked/app/data", "/mnt/data/app/data", ""},
		{"/mnt/data/app/config/plain", "/mnt/data/app/config/plain", ""},
	}
	for _, c := range cases {
		after := edited(before, func(x *types.ContainerJSON) {
			x.HostConfig.Mounts = append(x.HostConfig.Mounts, mount.Mount{Type: mount.TypeBind, Source: c.source, Target: "/new"})
		})
		err := resolveNewBinds(before, &after)
		got := after.HostConfig.Mounts[len(after.HostConfig.Mounts)-1].Source
		if c.refused != "" {
			if err == nil || !strings.Contains(err.Error(), c.refused) {
				t.Errorf("%s: got %v, want a refusal saying %q", c.source, err, c.refused)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: got %q %v, want %q", c.source, got, err, c.want)
		}
	}
	// the tick follows the resolved path: the planted link to /etc needs it, named as /etc/cron.d
	after := edited(before, func(x *types.ContainerJSON) {
		x.HostConfig.Mounts = append(x.HostConfig.Mounts, mount.Mount{Type: mount.TypeBind, Source: "/mnt/data/app/config/etc/cron.d", Target: "/new"})
	})
	if err := resolveNewBinds(before, &after); err != nil {
		t.Fatal(err)
	}
	missing := unconfirmedEdit("radarr", before, after, url.Values{}, nil)
	if len(missing) != 1 || missing[0].Value != "/etc/cron.d" {
		t.Fatalf("the tick: %+v", missing)
	}
	// a bind the container had is not looked at again
	kept := edited(before, nil)
	if err := resolveNewBinds(before, &kept); err != nil {
		t.Fatal(err)
	}
}
