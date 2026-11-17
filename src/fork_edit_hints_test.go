package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/azukaar/cosmos-server/src/edithints"
	"github.com/azukaar/cosmos-server/src/proxy"
	"github.com/docker/docker/api/types"
	containerType "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"
)

func hintsContainer(root string) types.ContainerJSON {
	return types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{Name: "/radarr", HostConfig: &containerType.HostConfig{}},
		Config: &containerType.Config{
			Image:        "lscr.io/linuxserver/radarr:latest",
			Env:          []string{"PUID=1000", "PGID=1000", "SECRET_KEY=not-sent"},
			ExposedPorts: nat.PortSet{"7878/tcp": {}},
		},
		Mounts: []types.MountPoint{
			{Type: mount.TypeBind, Source: "/mnt/data/radarr/config", Destination: "/config", RW: true},
			{Type: mount.TypeBind, Source: "/etc/localtime", Destination: "/etc/localtime", RW: false},
			{Type: mount.TypeVolume, Name: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Destination: "/cache", RW: true},
			{Type: mount.TypeVolume, Name: "radarr_named", Destination: "/named", RW: true},
		},
	}
}

func TestBuildEditHints(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "mnt/data/radarr/config"), 0o755); err != nil {
		t.Fatal(err)
	}
	in := editHintsInput{
		Container:  hintsContainer(root),
		ImageVols:  []string{"/config"},
		ImagePorts: []string{"7878/tcp", "9898/udp"},
		ImageEnv:   []string{"PATH=/usr/bin", "HOME=/root"},
		Plan:       installPlanAnswer{DataPath: "/mnt/data", Places: []installPlace{{Path: "/mnt/data", Kind: "disk"}, {Path: "/", Kind: "system"}}},
		Server:     editHintsServer{Memory: 8 << 30, CPUs: 4},
		LAN:        []proxy.LANAddress{{Interface: "eth0", Address: "192.168.1.10", Default: true}},
		DNS:        editHintsDNS{Servers: []string{"192.168.1.1"}, Source: "resolv.conf", Router: "192.168.1.1"},
		Held:       map[string]bool{"7878/tcp": true},
		CosmosPort: []int{80, 443},
		Listen:     func(port int, protocol string) bool { return port == 7879 },
		Asked:      []string{"443", "53/udp"},
		Root:       root,
	}
	h := buildEditHints(in)
	if !h.Linuxserver || h.RunsAs.Source != "puid" || h.RunsAs.UID != 1000 {
		t.Fatalf("user: %+v %v", h.RunsAs, h.Linuxserver)
	}
	if h.Recipe != nil {
		// radarr has no recipe today; a known app may
		t.Logf("recipe %s", h.Recipe.Name)
	}
	// 7878 is held by another container and 7879 cannot be listened on: 7880
	if h.FreePorts["7878/tcp"] != 7880 || h.FreePorts["9898/udp"] != 9898 || h.FreePorts["53/udp"] != 53 {
		t.Fatalf("free ports: %v", h.FreePorts)
	}
	// 443 is Cosmos' own: from 8443
	if h.FreePorts["443/tcp"] != 8443 {
		t.Fatalf("Cosmos' port proposed: %v", h.FreePorts)
	}
	if len(h.Folders) != 2 || h.Folders[0].State != "ok" || h.Folders[0].Target != "/config" || h.Folders[0].Writable == "" {
		t.Fatalf("folders: %+v", h.Folders)
	}
	if h.Folders[1].State != "outside" || h.Folders[1].Owner != "" || !h.Folders[1].ReadOnly {
		t.Fatalf("a system file was looked at: %+v", h.Folders[1])
	}
	if len(h.Anonymous) != 1 || h.Anonymous[0] != "/cache" {
		t.Fatalf("anonymous: %v", h.Anonymous)
	}
	if len(h.Image.Env) != 2 || h.Image.Env[0] != "PATH" {
		t.Fatalf("image env names: %v", h.Image.Env)
	}
	if !h.PortsListened {
		t.Fatal("listened")
	}
	// the data places a folder is looked at in: not the system disk
	if places := editDataPlaces(in.Plan); len(places) != 1 || places[0] != "/mnt/data" {
		t.Fatalf("places: %v", places)
	}
}

func TestBuildEditHintsBounds(t *testing.T) {
	c := hintsContainer("")
	c.Mounts = nil
	for i := 0; i < 100; i++ {
		c.Mounts = append(c.Mounts, types.MountPoint{Type: mount.TypeBind, Source: "/srv/x" + strconv.Itoa(i), Destination: "/x" + strconv.Itoa(i)})
	}
	asked := []string{}
	for i := 1; i <= 40; i++ {
		asked = append(asked, strconv.Itoa(10000+i))
	}
	h := buildEditHints(editHintsInput{Container: c, Asked: asked, Listen: nil})
	if len(h.Folders) != editHintsMaxFolders || len(h.FreePorts) > editHintsMaxPorts || h.PortsListened {
		t.Fatalf("not bounded: %d folders, %d ports", len(h.Folders), len(h.FreePorts))
	}
	if h.LAN == nil || h.Devices == nil || h.Places == nil {
		t.Fatal("lists must be empty, not null")
	}
	var _ edithints.Taken = edithints.ListenTaken
}

func TestEditHintsRouteRefuses(t *testing.T) {
	// without a session the route answers like its neighbours, before Docker is asked
	rec := httptest.NewRecorder()
	EditHintsRoute(rec, httptest.NewRequest(http.MethodGet, "/cosmos/api/servapps/x/edit-hints", nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("answered without a session: %d", rec.Code)
	}
	for _, text := range []string{"8080", "53/udp", " 9000/tcp"} {
		if _, _, ok := parsePortText(text); !ok {
			t.Errorf("%q refused", text)
		}
	}
	for _, text := range []string{"0", "70000", "80/icmp", "x", "../1"} {
		if _, _, ok := parsePortText(text); ok {
			t.Errorf("%q accepted", text)
		}
	}
}
