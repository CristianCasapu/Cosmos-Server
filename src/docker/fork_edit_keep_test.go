package docker

// Fork addition (plan 47, I2c, review): a save of each tab without a change
// gives Docker exactly what the container had, running and stopped: what the
// update route hands to the creation of the new container is compared with
// the container's settings, for the payload each tab sends (built here the way
// the panel builds it: setup.jsx, network.jsx with utils/editHints.js
// configuredPortRows/portsToSend, volumes.jsx).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	containerType "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"
)

func shapeContainer(t *testing.T, running bool) types.ContainerJSON {
	dir := t.TempDir()
	for _, d := range []string{"a", "b", "c"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{
			ID: "f00d", Name: "/shape", Image: "sha256:0",
			State: &types.ContainerState{Running: running},
			HostConfig: &containerType.HostConfig{
				NetworkMode: "shapenet",
				PortBindings: nat.PortMap{
					"9000/tcp": {{HostIP: "127.0.0.1", HostPort: "18300"}},
					"9001/udp": {{HostIP: "0.0.0.0", HostPort: "18301"}},
					"9003/tcp": {{HostIP: "", HostPort: ""}},
				},
				Binds: []string{dir + "/a:/a:ro,z", dir + "/b:/b:rw,rslave", "shape-named:/n:nocopy"},
				Mounts: []mount.Mount{
					{Type: mount.TypeTmpfs, Target: "/t", TmpfsOptions: &mount.TmpfsOptions{SizeBytes: 64 << 20, Mode: 0o1777}},
					{Type: mount.TypeVolume, Source: "shape-vol", Target: "/v", VolumeOptions: &mount.VolumeOptions{NoCopy: true, Labels: map[string]string{"k": "v"}}},
					{Type: mount.TypeBind, Source: dir + "/c", Target: "/c", BindOptions: &mount.BindOptions{Propagation: mount.PropagationRSlave}},
				},
				Tmpfs:         map[string]string{"/t2": "size=32m"},
				DNS:           []string{"192.0.2.53"},
				RestartPolicy: containerType.RestartPolicy{Name: "on-failure", MaximumRetryCount: 2},
				Resources: containerType.Resources{Memory: 256 << 20, NanoCPUs: 5e8,
					Devices: []containerType.DeviceMapping{{PathOnHost: "/dev/null", PathInContainer: "/dev/fakenull", CgroupPermissions: "r"}}},
			},
		},
		Config: &containerType.Config{
			Image: "example/plainapp:1", User: "65534:65534", Env: []string{"SHAPE=1"},
			Labels:       map[string]string{"shape.note": "kept"},
			ExposedPorts: nat.PortSet{"9000/tcp": {}, "9001/udp": {}, "9002/tcp": {}, "9003/tcp": {}},
		},
		NetworkSettings: &types.NetworkSettings{},
	}
}

// what each tab sends for a container it shows unchanged
func tabPayloads(c types.ContainerJSON) map[string]map[string]interface{} {
	env := []string{}
	env = append(env, c.Config.Env...)
	devices := []string{}
	for _, d := range c.HostConfig.Devices {
		devices = append(devices, d.PathOnHost+":"+d.PathInContainer)
	}
	docker := map[string]interface{}{
		"image": c.Config.Image, "user": c.Config.User, "restartPolicy": string(c.HostConfig.RestartPolicy.Name),
		"envVars": env, "labels": c.Config.Labels, "devices": devices, "interactive": 1, "memLimit": "256m", "cpus": 0.5,
	}
	// network.jsx: configuredPortRows → portsToSend
	bindings := map[string][]map[string]string{}
	exposed := []string{}
	keys := []string{}
	for p := range c.Config.ExposedPorts {
		keys = append(keys, string(p))
	}
	for p := range c.HostConfig.PortBindings {
		if _, ok := c.Config.ExposedPorts[p]; !ok {
			keys = append(keys, string(p))
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		exposed = append(exposed, k)
		for _, b := range c.HostConfig.PortBindings[nat.Port(k)] {
			bindings[k] = append(bindings[k], map[string]string{"HostIp": b.HostIP, "HostPort": b.HostPort})
		}
	}
	network := map[string]interface{}{"portBindings": bindings, "exposedPorts": exposed, "networkMode": string(c.HostConfig.NetworkMode), "dns": c.HostConfig.DNS}
	// volumes.jsx: Mounts, then Binds read as mounts
	volumes := []map[string]interface{}{}
	for _, m := range c.HostConfig.Mounts {
		volumes = append(volumes, map[string]interface{}{"Type": m.Type, "Source": m.Source, "Target": m.Target, "ReadOnly": m.ReadOnly})
	}
	for _, b := range c.HostConfig.Binds {
		parts := strings.Split(b, ":")
		kind := "volume"
		if strings.HasPrefix(parts[0], "/") {
			kind = "bind"
		}
		ro := len(parts) > 2 && hasWord(parts[2], "ro")
		volumes = append(volumes, map[string]interface{}{"Type": kind, "Source": parts[0], "Target": parts[1], "ReadOnly": ro})
	}
	return map[string]map[string]interface{}{"Docker": docker, "Network": network, "Storage": {"Volumes": volumes}}
}

type createdBody struct {
	containerType.Config
	HostConfig containerType.HostConfig
}

func comparable(config containerType.Config, host containerType.HostConfig) map[string]interface{} {
	binds := append([]string{}, host.Binds...)
	sort.Strings(binds)
	mounts := append([]mount.Mount{}, host.Mounts...)
	sort.Slice(mounts, func(i, j int) bool { return mounts[i].Target < mounts[j].Target })
	labels := map[string]string{}
	for k, v := range config.Labels {
		labels[k] = v
	}
	env := append([]string{}, config.Env...)
	sort.Strings(env)
	return map[string]interface{}{
		"user": config.User, "env": env, "labels": labels, "exposed": config.ExposedPorts, "ports": host.PortBindings,
		"devices": host.Devices, "binds": binds, "mounts": mounts, "tmpfs": host.Tmpfs, "dns": host.DNS,
		"restart": host.RestartPolicy, "memory": host.Memory, "swap": host.MemorySwap, "cpus": host.NanoCPUs, "mode": host.NetworkMode,
	}
}

func TestAnUnchangedSaveOfEachTabKeepsEverything(t *testing.T) {
	withServer(t)
	for _, running := range []bool{true, false} {
		c := shapeContainer(t, running)
		want := comparable(*c.Config, *c.HostConfig)
		for tab, payload := range tabPayloads(c) {
			fake := withFakeDocker(t, c)
			fake.lenient = true
			body, _ := json.Marshal(payload)
			w := updateThroughRoute(t, string(body))
			if len(fake.created) != 1 {
				t.Fatalf("running %v, %s: the edit did not reach the creation: %d %s", running, tab, w.Code, w.Body.String())
			}
			var got createdBody
			if err := json.Unmarshal(fake.created[0], &got); err != nil {
				t.Fatal(err)
			}
			have := comparable(got.Config, got.HostConfig)
			for key := range want {
				if !reflect.DeepEqual(normalise(want[key]), normalise(have[key])) {
					a, _ := json.Marshal(want[key])
					b, _ := json.Marshal(have[key])
					t.Errorf("running %v, %s tab: %s changed\n   had  %s\n   sent %s", running, tab, key, a, b)
				}
			}
		}
	}
}

// normalise compares through JSON (nil and empty maps are the same to Docker)
func normalise(v interface{}) interface{} {
	b, _ := json.Marshal(v)
	var out interface{}
	json.Unmarshal(b, &out)
	if m, ok := out.(map[string]interface{}); ok && len(m) == 0 {
		return nil
	}
	if a, ok := out.([]interface{}); ok && len(a) == 0 {
		return nil
	}
	return out
}

// A sensitive folder confirmed read-only and made writable is new access: asked again.
func TestMakingAConfirmedFolderWritableNeedsTheTick(t *testing.T) {
	before := ownersApp()
	before.HostConfig.Binds = []string{"/etc:/hostetc:ro"}
	after := edited(before, func(c *types.ContainerJSON) {
		for i := range c.HostConfig.Mounts {
			if c.HostConfig.Mounts[i].Source == "/etc" {
				c.HostConfig.Mounts[i].ReadOnly = false
			}
		}
	})
	missing := unconfirmedEdit("radarr", before, after, nil, nil)
	if len(missing) != 1 || missing[0].Value != "/etc" {
		t.Fatalf("read-only to writable: %+v", missing)
	}
	still := edited(before, nil)
	if missing := unconfirmedEdit("radarr", before, still, nil, nil); len(missing) != 0 {
		t.Fatalf("kept read-only: %+v", missing)
	}
}

// A named volume whose local driver binds a host path is that path; shared
// propagation needs the tick; other driver shapes are refused in words. On
// the edit and on the creation routes.
func TestVolumeDriverOptionsAreTheBindTheyAre(t *testing.T) {
	withServer(t)
	disguised := func(device string) mount.Mount {
		return mount.Mount{Type: mount.TypeVolume, Source: "v1", Target: "/h", VolumeOptions: &mount.VolumeOptions{DriverConfig: &mount.Driver{Name: "local", Options: map[string]string{"type": "none", "o": "bind", "device": device}}}}
	}
	// creation
	request := DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{"app": {Image: "x", Volumes: []mount.Mount{
		disguised("/"), disguised("/mnt/data/fine"),
		{Type: mount.TypeBind, Source: "/mnt/data/shared", Target: "/s", BindOptions: &mount.BindOptions{Propagation: mount.PropagationRShared}},
	}}}}
	got := []string{}
	for _, item := range SensitiveAccess(request, nil) {
		got = append(got, item.Key())
	}
	if strings.Join(got, ",") != "app|bind|/,app|propagation|/mnt/data/shared" {
		t.Fatalf("creation: %v", got)
	}
	for name, options := range map[string]map[string]string{
		"an unknown option": {"type": "none", "o": "bind", "device": "/x", "uid": "0"},
		"an unknown type":   {"type": "btrfs", "device": "/dev/sdb"},
		"a relative device": {"type": "none", "o": "bind", "device": "x/../y"},
		"a device with ..":  {"o": "bind", "device": "/mnt/data/../../etc"},
	} {
		m := mount.Mount{Type: mount.TypeVolume, Source: "v2", Target: "/x", VolumeOptions: &mount.VolumeOptions{DriverConfig: &mount.Driver{Options: options}}}
		if err := CheckServiceRequest(DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{"app": {Volumes: []mount.Mount{m}}}}); err == nil {
			t.Errorf("creation, %s: accepted", name)
		}
		before := ownersApp()
		if err := checkEdit(before, edited(before, func(c *types.ContainerJSON) { c.HostConfig.Mounts = append(c.HostConfig.Mounts, m) })); err == nil {
			t.Errorf("edit, %s: accepted", name)
		}
	}
	for name, options := range map[string]map[string]string{
		"a host folder": {"type": "none", "o": "bind", "device": "/mnt/data/x"},
		"tmpfs":         {"type": "tmpfs", "device": "tmpfs", "o": "size=100m"},
		"an NFS share":  {"type": "nfs", "o": "addr=192.0.2.5,rw", "device": ":/export/media"},
	} {
		m := mount.Mount{Type: mount.TypeVolume, Source: "v3", Target: "/x", VolumeOptions: &mount.VolumeOptions{DriverConfig: &mount.Driver{Name: "local", Options: options}}}
		if err := checkVolumeOptions(m); err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}
	// edit: the disguised bind of / needs the tick
	before := ownersApp()
	after := edited(before, func(c *types.ContainerJSON) { c.HostConfig.Mounts = append(c.HostConfig.Mounts, disguised("/")) })
	if missing := unconfirmedEdit("radarr", before, after, nil, nil); len(missing) != 1 || missing[0].Key() != "radarr|bind|/" {
		t.Fatalf("edit: %+v", missing)
	}
	shared := edited(before, func(c *types.ContainerJSON) {
		c.HostConfig.Binds = append(c.HostConfig.Binds, "/mnt/data/s:/s:rshared")
	})
	if missing := unconfirmedEdit("radarr", before, shared, nil, nil); len(missing) != 1 || missing[0].Kind != "propagation" {
		t.Fatalf("edit, shared propagation: %+v", missing)
	}
}

// The same cases as the panel's rules (client/src/utils/sensitiveAccess.js).
func TestMountCasesAgreeWithThePanel(t *testing.T) {
	var cases struct {
		Mounts []struct {
			Mount mount.Mount `json:"mount"`
			Items []string    `json:"items"`
		} `json:"mounts"`
	}
	data, err := os.ReadFile("testdata/sensitive_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &cases); err != nil || len(cases.Mounts) == 0 {
		t.Fatalf("cases: %v", err)
	}
	for _, c := range cases.Mounts {
		got := []string{}
		for _, item := range serviceSensitive("s", ContainerCreateRequestContainer{Volumes: []mount.Mount{c.Mount}}, nil) {
			got = append(got, item.Kind+"|"+item.Value)
		}
		if strings.Join(got, ",") != strings.Join(c.Items, ",") {
			t.Errorf("%+v: got %v, want %v", c.Mount, got, c.Items)
		}
	}
}
