package docker

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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
	oldPlaces, oldChown := EditDataPlaces, chownFolder
	defer func() { EditDataPlaces, chownFolder = oldPlaces, oldChown }()
	EditDataPlaces = func() []string { return []string{"/mnt/data"} }
	owned := map[string]string{}
	chownFolder = func(p string, uid, gid int) error {
		owned[strings.TrimPrefix(p, root)] = strconv.Itoa(uid) + ":" + strconv.Itoa(gid)
		return nil
	}
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
	// an app that runs as root: nothing is done (EditContainer makes the folder as before)
	owned = map[string]string{}
	rootApp := edited(before, func(c *types.ContainerJSON) {
		c.Config.Image = "nginx:1"
		c.Config.Env = nil
		c.HostConfig.Mounts = append(c.HostConfig.Mounts, mount.Mount{Type: mount.TypeBind, Source: "/mnt/data/web", Target: "/web"})
	})
	forkPrepareBindFolders(before, rootApp, root)
	if len(owned) != 0 {
		t.Fatalf("root's app: %v", owned)
	}
	if _, err := os.Stat(filepath.Join(root, "mnt/data/web")); err == nil {
		t.Fatal("made for root's app")
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
