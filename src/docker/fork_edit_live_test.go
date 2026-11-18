package docker

// Fork addition (plan 47, I2c, second review S3): an unchanged save of each
// edit tab, through the update route, against a real Docker daemon, compares
// the WHOLE container before and after: the configuration, the host
// configuration and the network endpoints. Only against a daemon of the
// test's own (docker:29-dind), never the machine's:
//
//	COSMOS_FORK_LIVE_DOCKER=1 DOCKER_HOST=tcp://<dind>:2375 go test ./src/docker/ -run UnchangedSaveLive -v
//
// It needs busybox:1.36 in that daemon; LIVE_DIGEST_IMAGE=<repo>@sha256:… makes
// the container's image one pinned by digest (pushed to a registry the
// daemon reaches).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/docker/docker/api/types"
	conttype "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	volumetype "github.com/docker/docker/api/types/volume"
	"github.com/docker/go-connections/nat"
	"github.com/gorilla/mux"
)

// what Docker sets anew on every creation, and why it is not compared
var liveRegenerated = map[string]string{
	"Config.Image": "the route sends the image as Docker reports it; compared by the image ID instead",
}

func liveUpdate(t *testing.T, name, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/cosmos/api/servapps/"+name+"/update", strings.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"containerId": name})
	admin := &utils.AuthContext{Nickname: "alice", IsSudoed: true, Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_RESOURCES}}
	req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, admin))
	w := httptest.NewRecorder()
	UpdateContainerRoute(w, req)
	return w
}

// endpoints are the networks and what the container chose on each
func endpoints(c types.ContainerJSON) string {
	out := []string{}
	for name, ep := range c.NetworkSettings.Networks {
		aliases := []string{}
		for _, a := range ep.Aliases {
			if a != shortID(c.ID) {
				aliases = append(aliases, a)
			}
		}
		sort.Strings(aliases)
		ipam, _ := json.Marshal(ep.IPAMConfig)
		mac := ep.MacAddress
		if name != string(c.HostConfig.NetworkMode) {
			// Docker chooses the MAC address of a network connected after the creation itself
			// (docker network connect takes none): not the container's choice
			mac = "(chosen by Docker)"
		}
		out = append(out, name+" aliases="+strings.Join(aliases, ",")+" ipam="+string(ipam)+" mac="+mac+" ip="+ep.IPAddress+" links="+strings.Join(ep.Links, ","))
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

func liveWorstCase(t *testing.T, name string, running bool) {
	t.Helper()
	image := liveImage
	if digest := os.Getenv("LIVE_DIGEST_IMAGE"); digest != "" {
		image = digest
	}
	stop := 1
	config := &conttype.Config{
		Image: image, Cmd: []string{"sleep", "3600"}, StopSignal: "SIGKILL", StopTimeout: &stop,
		Hostname: "myhost", Domainname: "home.example.org", OpenStdin: true, AttachStdin: true, Tty: false,
		User: "65534:65534", Env: []string{"MARK=original"}, Labels: map[string]string{"shape.note": "kept"},
		Healthcheck:  &conttype.HealthConfig{Test: []string{"CMD", "true"}, Interval: 30e9, Retries: 3},
		ExposedPorts: nat.PortSet{"80/tcp": {}, "81/tcp": {}},
		WorkingDir:   "/tmp",
	}
	host := &conttype.HostConfig{
		NetworkMode:   livePrefix + "net",
		CapDrop:       []string{"ALL"},
		CapAdd:        []string{"CHOWN"},
		SecurityOpt:   []string{"no-new-privileges:true"},
		LogConfig:     conttype.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "2"}},
		PortBindings:  nat.PortMap{"80/tcp": {{HostIP: "127.0.0.1", HostPort: map[bool]string{true: "18400", false: "18401"}[running]}}},
		RestartPolicy: conttype.RestartPolicy{Name: "on-failure", MaximumRetryCount: 3},
		Tmpfs:         map[string]string{"/t2": "size=32m"},
		Binds:         []string{"/tmp:/hosttmp:ro"},
		Mounts: []mount.Mount{
			{Type: mount.TypeTmpfs, Target: "/t", TmpfsOptions: &mount.TmpfsOptions{SizeBytes: 64 << 20, Mode: 0o1777}},
			{Type: mount.TypeVolume, Source: livePrefix + "vol", Target: "/v", VolumeOptions: &mount.VolumeOptions{NoCopy: true}},
		},
		DNS:        []string{"192.0.2.53"},
		ExtraHosts: []string{"nas.example.org:192.0.2.20"},
		Init:       &trueValue,
		Resources: conttype.Resources{Memory: 256 << 20, NanoCPUs: 5e8,
			Devices: []conttype.DeviceMapping{{PathOnHost: "/dev/null", PathInContainer: "/dev/fakenull", CgroupPermissions: "r"}}},
	}
	netConfig := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{livePrefix + "net": {
		Aliases: []string{"db", "database"}, IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: map[bool]string{true: "172.30.0.50", false: "172.30.0.51"}[running]},
		MacAddress: map[bool]string{true: "02:42:ac:1e:00:32", false: "02:42:ac:1e:00:33"}[running],
	}}}
	created, err := DockerClient.ContainerCreate(DockerContext, config, host, netConfig, nil, livePrefix+name)
	if err != nil {
		t.Fatal(err)
	}
	// a second network with an alias of its own
	if err := DockerClient.NetworkConnect(DockerContext, livePrefix+"net2", created.ID, &network.EndpointSettings{Aliases: []string{name + "-two"}}); err != nil {
		t.Fatal(err)
	}
	if running {
		if err := DockerClient.ContainerStart(DockerContext, created.ID, conttype.StartOptions{}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUnchangedSaveLive(t *testing.T) {
	liveSetup(t)
	for _, n := range []string{"net", "net2"} {
		DockerClient.NetworkRemove(DockerContext, livePrefix+n)
	}
	t.Cleanup(func() {
		liveCleanup(t)
		DockerClient.NetworkRemove(DockerContext, livePrefix+"net2")
		DockerClient.VolumeRemove(DockerContext, livePrefix+"vol", true)
	})
	if _, err := DockerClient.NetworkCreate(DockerContext, livePrefix+"net", types.NetworkCreate{IPAM: &network.IPAM{Config: []network.IPAMConfig{{Subnet: "172.30.0.0/24"}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := DockerClient.NetworkCreate(DockerContext, livePrefix+"net2", types.NetworkCreate{}); err != nil {
		t.Fatal(err)
	}
	if _, err := DockerClient.VolumeCreate(DockerContext, volumetype.CreateOptions{Name: livePrefix + "vol", Labels: map[string]string{"k": "v"}}); err != nil {
		t.Fatal(err)
	}
	for _, running := range []bool{true, false} {
		name := map[bool]string{true: "worst-run", false: "worst-stop"}[running]
		liveWorstCase(t, name, running)
		for _, tab := range []string{"Docker", "Network", "Storage"} {
			before := liveInspect(t, name)
			body, _ := json.Marshal(tabPayloads(before)[tab])
			if w := liveUpdate(t, livePrefix+name, string(body)); w.Code != http.StatusOK {
				t.Fatalf("%s, %s tab: %d %s", name, tab, w.Code, w.Body.String())
			}
			after := liveInspect(t, name)
			if after.ID == before.ID {
				t.Fatalf("%s, %s tab: not made again", name, tab)
			}
			if after.State.Running != running {
				t.Errorf("%s, %s tab: running %v, was %v", name, tab, after.State.Running, running)
			}
			a, _ := json.Marshal(before.Config)
			b, _ := json.Marshal(after.Config)
			if d := liveDiff("Config.", a, b); d != "" {
				t.Errorf("%s, %s tab: Config changed: %s", name, tab, d)
			}
			a, _ = json.Marshal(before.HostConfig)
			b, _ = json.Marshal(after.HostConfig)
			if d := liveDiff("HostConfig.", a, b); d != "" {
				t.Errorf("%s, %s tab: HostConfig changed: %s", name, tab, d)
			}
			if before.Image != after.Image {
				t.Errorf("%s, %s tab: the image changed: %s → %s", name, tab, before.Image, after.Image)
			}
			// the addresses are what the container chose: the static one is kept; a dynamic one (the second network) may change
			wantEP, gotEP := endpoints(before), endpoints(after)
			if strings.Split(wantEP, "\n")[0] != strings.Split(gotEP, "\n")[0] || stripIP(wantEP) != stripIP(gotEP) {
				t.Errorf("%s, %s tab: networks changed:\n  had  %s\n  has  %s", name, tab, strings.ReplaceAll(wantEP, "\n", "\n       "), strings.ReplaceAll(gotEP, "\n", "\n       "))
			}
		}
	}
	// on the default bridge: a hostname of its own and -i without -t
	created, err := DockerClient.ContainerCreate(DockerContext, &conttype.Config{Image: liveImage, Cmd: []string{"sleep", "3600"}, StopSignal: "SIGKILL",
		Hostname: "myhost", OpenStdin: true, AttachStdin: true}, &conttype.HostConfig{NetworkMode: "bridge"}, nil, nil, livePrefix+"bridge")
	if err != nil {
		t.Fatal(err)
	}
	if err := DockerClient.ContainerStart(DockerContext, created.ID, conttype.StartOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, tab := range []string{"Docker", "Network", "Storage"} {
		before := liveInspect(t, "bridge")
		body, _ := json.Marshal(tabPayloads(before)[tab])
		if w := liveUpdate(t, livePrefix+"bridge", string(body)); w.Code != http.StatusOK {
			t.Fatalf("bridge, %s tab: %d %s", tab, w.Code, w.Body.String())
		}
		after := liveInspect(t, "bridge")
		a, _ := json.Marshal(before.Config)
		b, _ := json.Marshal(after.Config)
		if d := liveDiff("Config.", a, b); d != "" || after.Config.Hostname != "myhost" || !after.Config.OpenStdin {
			t.Errorf("bridge, %s tab: Config changed: %s", tab, d)
		}
	}
	liveNoLeftovers(t)
}

// stripIP leaves out the dynamic address of each network (only a static one is the container's choice)
func stripIP(endpoints string) string {
	lines := strings.Split(endpoints, "\n")
	for i, l := range lines {
		if strings.Contains(l, `ipam=null`) || strings.Contains(l, `ipam={}`) {
			lines[i] = l[:strings.Index(l, " ip=")] + l[strings.Index(l, " links="):]
		}
	}
	return strings.Join(lines, "\n")
}

func liveDiff(prefix string, a, b []byte) string {
	var x, y map[string]interface{}
	json.Unmarshal(a, &x)
	json.Unmarshal(b, &y)
	diffs := []string{}
	for k := range x {
		if _, skip := liveRegenerated[prefix+k]; skip {
			continue
		}
		if !jsonEqual(x[k], y[k]) {
			p, _ := json.Marshal(x[k])
			q, _ := json.Marshal(y[k])
			diffs = append(diffs, k+": "+string(p)+" → "+string(q))
		}
	}
	sort.Strings(diffs)
	return strings.Join(diffs, "; ")
}

func jsonEqual(a, b interface{}) bool {
	p, _ := json.Marshal(normalise(a))
	q, _ := json.Marshal(normalise(b))
	return string(p) == string(q)
}

// An existing volume made with bind options, named without options in an
// edit, is the folder it binds: the edit needs the tick (second review S1).
func TestDisguisedVolumeLive(t *testing.T) {
	liveSetup(t)
	resetEditCaches()
	DockerClient.VolumeRemove(DockerContext, livePrefix+"v9", true)
	t.Cleanup(func() { liveCleanup(t); DockerClient.VolumeRemove(DockerContext, livePrefix+"v9", true) })
	if _, err := DockerClient.VolumeCreate(DockerContext, volumetype.CreateOptions{Name: livePrefix + "v9", Driver: "local",
		DriverOpts: map[string]string{"type": "none", "o": "bind", "device": "/etc"}}); err != nil {
		t.Fatal(err)
	}
	liveContainer(t, "plain", "", true)
	w := liveUpdate(t, livePrefix+"plain", `{"Volumes":[{"Type":"volume","Source":"`+livePrefix+`v9","Target":"/h"}]}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "/etc") {
		t.Fatalf("an existing disguised volume: %d %s", w.Code, w.Body.String())
	}
}

// A plain container (no settings of its own) is made again as upstream makes
// it: no MAC address of its own afterwards, no alias, no fixed address
// (third review m-1).
func TestPlainRecreateLive(t *testing.T) {
	liveSetup(t)
	DockerClient.NetworkRemove(DockerContext, livePrefix+"plainnet")
	t.Cleanup(func() { liveCleanup(t); DockerClient.NetworkRemove(DockerContext, livePrefix+"plainnet") })
	if _, err := DockerClient.NetworkCreate(DockerContext, livePrefix+"plainnet", types.NetworkCreate{}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"bridge", livePrefix + "plainnet"} {
		name := "plain-" + map[bool]string{true: "bridge", false: "user"}[mode == "bridge"]
		created, err := DockerClient.ContainerCreate(DockerContext, &conttype.Config{Image: liveImage, Cmd: []string{"sleep", "3600"}, StopSignal: "SIGKILL"},
			&conttype.HostConfig{NetworkMode: conttype.NetworkMode(mode)}, nil, nil, livePrefix+name)
		if err != nil {
			t.Fatal(err)
		}
		if err := DockerClient.ContainerStart(DockerContext, created.ID, conttype.StartOptions{}); err != nil {
			t.Fatal(err)
		}
		for _, tab := range []string{"Docker", "Network", "Storage"} {
			before := liveInspect(t, name)
			body, _ := json.Marshal(tabPayloads(before)[tab])
			if w := liveUpdate(t, livePrefix+name, string(body)); w.Code != http.StatusOK {
				t.Fatalf("%s, %s: %d %s", name, tab, w.Code, w.Body.String())
			}
			after := liveInspect(t, name)
			beforeConfig := *before.Config
			if mode == "bridge" && beforeConfig.Hostname == shortID(before.ID) {
				// upstream gives a container on the bridge its name as hostname when it had Docker's default
				beforeConfig.Hostname = name
				beforeConfig.Hostname = strings.TrimPrefix(after.Name, "/")
			}
			a, _ := json.Marshal(beforeConfig)
			b, _ := json.Marshal(after.Config)
			if d := liveDiff("Config.", a, b); d != "" {
				t.Errorf("%s, %s: Config changed: %s", name, tab, d)
			}
			a, _ = json.Marshal(before.HostConfig)
			b, _ = json.Marshal(after.HostConfig)
			if d := liveDiff("HostConfig.", a, b); d != "" {
				t.Errorf("%s, %s: HostConfig changed: %s", name, tab, d)
			}
			ep := after.NetworkSettings.Networks[map[bool]string{true: "bridge", false: mode}[mode == "bridge"]]
			if after.Config.MacAddress != "" || ep == nil || ep.IPAMConfig != nil && (ep.IPAMConfig.IPv4Address != "" || ep.IPAMConfig.IPv6Address != "") { //nolint:staticcheck
				t.Errorf("%s, %s: settings it never had: mac %q %+v", name, tab, after.Config.MacAddress, ep) //nolint:staticcheck
			}
			for _, alias := range ep.Aliases {
				if alias != shortID(after.ID) && alias != name {
					t.Errorf("%s, %s: an alias it never had: %s", name, tab, alias)
				}
			}
		}
	}
}

// A stopped container whose network was made again with another range: the
// update succeeds, the fixed address that no longer fits is dropped and said
// (third review m-2).
func TestSubnetChangedLive(t *testing.T) {
	liveSetup(t)
	net := livePrefix + "subnet"
	DockerClient.NetworkRemove(DockerContext, net)
	t.Cleanup(func() { liveCleanup(t); DockerClient.NetworkRemove(DockerContext, net) })
	if _, err := DockerClient.NetworkCreate(DockerContext, net, types.NetworkCreate{IPAM: &network.IPAM{Config: []network.IPAMConfig{{Subnet: "172.31.5.0/24"}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := DockerClient.ContainerCreate(DockerContext, &conttype.Config{Image: liveImage, Cmd: []string{"sleep", "3600"}, StopSignal: "SIGKILL"},
		&conttype.HostConfig{NetworkMode: conttype.NetworkMode(net)},
		&network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{net: {Aliases: []string{"db"}, IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: "172.31.5.9"}}}},
		nil, livePrefix+"moved"); err != nil {
		t.Fatal(err)
	}
	if err := DockerClient.NetworkRemove(DockerContext, net); err != nil {
		t.Fatalf("the network of a stopped container could not be removed: %v", err)
	}
	if _, err := DockerClient.NetworkCreate(DockerContext, net, types.NetworkCreate{IPAM: &network.IPAM{Config: []network.IPAMConfig{{Subnet: "172.31.6.0/24"}}}}); err != nil {
		t.Fatal(err)
	}
	before := liveInspect(t, "moved")
	body, _ := json.Marshal(tabPayloads(before)["Docker"])
	w := liveUpdate(t, livePrefix+"moved", string(body))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "172.31.5.9 no longer fits the network") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	after := liveInspect(t, "moved")
	ep := after.NetworkSettings.Networks[net]
	if ep == nil || (ep.IPAMConfig != nil && ep.IPAMConfig.IPv4Address != "") || strings.Join(ep.Aliases, ",") != "db" {
		t.Fatalf("after: %+v", ep)
	}
	if err := DockerClient.ContainerStart(DockerContext, after.ID, conttype.StartOptions{}); err != nil {
		t.Fatalf("it does not start: %v", err)
	}
}
