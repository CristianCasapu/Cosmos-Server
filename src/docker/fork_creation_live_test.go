package docker

// Fork addition: the creation of apps against a real Docker daemon, never the
// machine's (see fork_safe_edit_live_test.go for how to run it):
//
//	COSMOS_FORK_LIVE_DOCKER=1 DOCKER_HOST=tcp://<dind>:2375 go test ./src/docker/ -run CreationLive -v
//
// It needs busybox:1.36 in that daemon and names everything cosmos-edit-live-*.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// What the new-container form sends (client/src/utils/newContainer.test.mjs
// checks that the form builds exactly this) reaches the container, field by
// field, and the edit form gets it back.
func TestCreationLiveEveryFieldOfTheForm(t *testing.T) {
	liveSetup(t)
	t.Setenv("CONFIG_FILE", filepath.Join(t.TempDir(), "cosmos.config.json"))
	liveContainer(t, "vpn", "", true)

	content, err := os.ReadFile("testdata/new_container_form.json")
	if err != nil {
		t.Fatal(err)
	}
	var request DockerServiceCreateRequest
	if err := json.Unmarshal(content, &request); err != nil {
		t.Fatal(err)
	}
	if err := CheckServiceRequest(request); err != nil {
		t.Fatalf("the form's request is refused: %v", err)
	}
	log := []string{}
	if err := CreateService(request, func(line string) { log = append(log, line) }); err != nil {
		t.Fatalf("%v\n%s", err, strings.Join(log, ""))
	}
	created := liveInspect(t, "form")
	vpn := liveInspect(t, "vpn")
	checks := map[string]bool{
		"user":         created.Config.User == "1000:1000",
		"memory limit": created.HostConfig.Memory == 64*1024*1024,
		"CPUs":         created.HostConfig.NanoCPUs == 500000000,
		"network mode": string(created.HostConfig.NetworkMode) == "container:"+vpn.ID || string(created.HostConfig.NetworkMode) == "container:"+livePrefix+"vpn",
		"interactive":  !created.Config.Tty && !created.Config.OpenStdin,
		"restart":      string(created.HostConfig.RestartPolicy.Name) == "unless-stopped",
		"environment":  liveHasEnv(created, "MARK=form"),
	}
	for field, ok := range checks {
		if !ok {
			t.Errorf("%s did not reach the container: %+v / %+v", field, created.Config, created.HostConfig)
		}
	}

	// the edit form's compose view reads the container back through the export
	exported, err := ExportContainer(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if exported.User != "1000:1000" || exported.MemLimit != "67108864" || exported.CPUs != 0.5 || !strings.HasPrefix(exported.NetworkMode, "container:") {
		t.Errorf("export: user %q, memory %q, cpus %v, mode %q", exported.User, exported.MemLimit, exported.CPUs, exported.NetworkMode)
	}
}

// The compose keys upstream dropped or mangled reach the container.
func TestCreationLiveComposeKeys(t *testing.T) {
	liveSetup(t)
	t.Setenv("CONFIG_FILE", filepath.Join(t.TempDir(), "cosmos.config.json"))
	var request DockerServiceCreateRequest
	err := json.Unmarshal([]byte(`{"services": {"keys": {
		"container_name": "cosmos-edit-live-keys",
		"image": "busybox:1.36",
		"command": ["sh", "-c", "echo 'a b' && sleep 3600"],
		"restart": "on-failure:3",
		"shm_size": "128m",
		"tmpfs": ["/scratch:size=8m"],
		"init": true,
		"read_only": true,
		"stop_signal": "SIGKILL",
		"healthcheck": {"test": "true", "interval": "1m30s", "timeout": "5s", "start_period": "2s", "retries": 2},
		"volumes": [{"type": "volume", "source": "cosmos-edit-live-data", "target": "/data", "ReadOnly": true}]
	}}, "volumes": {"cosmos-edit-live-data": {}}}`), &request)
	if err != nil {
		t.Fatal(err)
	}
	defer DockerClient.VolumeRemove(DockerContext, livePrefix+"data", true)
	if err := CheckServiceRequest(request); err != nil {
		t.Fatal(err)
	}
	log := []string{}
	if err := CreateService(request, func(line string) { log = append(log, line) }); err != nil {
		t.Fatalf("%v\n%s", err, strings.Join(log, ""))
	}
	c := liveInspect(t, "keys")
	if !reflect.DeepEqual([]string(c.Config.Cmd), []string{"sh", "-c", "echo 'a b' && sleep 3600"}) {
		t.Errorf("command: %q", c.Config.Cmd)
	}
	if c.HostConfig.RestartPolicy.Name != "on-failure" || c.HostConfig.RestartPolicy.MaximumRetryCount != 3 {
		t.Errorf("restart: %+v", c.HostConfig.RestartPolicy)
	}
	if c.HostConfig.ShmSize != 128*1024*1024 || c.HostConfig.Tmpfs["/scratch"] != "size=8m" || c.HostConfig.Init == nil || !*c.HostConfig.Init || !c.HostConfig.ReadonlyRootfs {
		t.Errorf("extras: shm %d tmpfs %v init %v ro %v", c.HostConfig.ShmSize, c.HostConfig.Tmpfs, c.HostConfig.Init, c.HostConfig.ReadonlyRootfs)
	}
	if h := c.Config.Healthcheck; h == nil || !reflect.DeepEqual(h.Test, []string{"CMD-SHELL", "true"}) || h.Interval != 90*time.Second || h.Timeout != 5*time.Second || h.Retries != 2 {
		t.Errorf("healthcheck: %+v", c.Config.Healthcheck)
	}
	readOnly := false
	for _, m := range c.Mounts {
		if m.Destination == "/data" {
			readOnly = !m.RW
		}
	}
	if !readOnly {
		t.Errorf("the volume is not read-only: %+v", c.Mounts)
	}

	// an edit (through the export, as the compose view does) keeps all of it
	exported, err := ExportContainer(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if exported.RestartPolicy != "on-failure:3" {
		t.Errorf("export restart: %q", exported.RestartPolicy)
	}
	if exported.ShmSize == "" || !exported.Tmpfs.Given() || exported.Init == nil || !exported.ReadOnly || !reflect.DeepEqual(exported.Command.Args(), []string(c.Config.Cmd)) || exported.HealthCheck.Interval.duration() != 90*time.Second {
		t.Errorf("export lost something: %+v", exported)
	}
}
