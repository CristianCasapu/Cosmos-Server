package docker

// Fork addition: exercises EditContainer against a real Docker daemon. It
// creates, edits and removes containers and a network named
// cosmos-edit-live-*, so it only runs when asked to, and only against a
// daemon of its own (docker:27-dind), never the machine's:
//
//	COSMOS_FORK_LIVE_DOCKER=1 DOCKER_HOST=tcp://<dind>:2375 go test ./src/docker/ -run SafeEditLive -v
//
// The images busybox:1.36 and hello-world must be present in that daemon.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	conttype "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
)

const liveImage = "busybox:1.36"
const livePrefix = "cosmos-edit-live-"

func liveSetup(t *testing.T) {
	t.Helper()
	if os.Getenv("COSMOS_FORK_LIVE_DOCKER") == "" {
		t.Skip("set COSMOS_FORK_LIVE_DOCKER=1 and DOCKER_HOST to a Docker daemon of your own to run this")
	}
	if os.Getenv("DOCKER_HOST") == "" {
		t.Fatal("DOCKER_HOST must point at a Docker daemon of your own: this test never uses the machine's")
	}
	if err := Connect(); err != nil {
		t.Fatal(err)
	}
	liveCleanup(t)
	t.Cleanup(func() { liveCleanup(t) })
}

func liveCleanup(t *testing.T) {
	t.Helper()
	containers, err := DockerClient.ContainerList(DockerContext, conttype.ListOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range containers {
		for _, name := range c.Names {
			if strings.HasPrefix(name, "/"+livePrefix) {
				DockerClient.ContainerRemove(DockerContext, c.ID, conttype.RemoveOptions{Force: true})
				break
			}
		}
	}
	DockerClient.NetworkRemove(DockerContext, livePrefix+"net")
	DockerClient.ImageRemove(DockerContext, livePrefix+"image:latest", types.ImageRemoveOptions{})
}

// liveContainer creates a container that sleeps, publishing container port 80
// on hostPort when given, and starts it when running is set.
func liveContainer(t *testing.T, name string, hostPort string, running bool) string {
	t.Helper()
	config := &conttype.Config{
		Image:      liveImage,
		Cmd:        []string{"sleep", "3600"},
		StopSignal: "SIGKILL",
		Env:        []string{"MARK=original"},
	}
	hostConfig := &conttype.HostConfig{NetworkMode: "bridge"}
	if hostPort != "" {
		config.ExposedPorts = nat.PortSet{"80/tcp": struct{}{}}
		hostConfig.PortBindings = nat.PortMap{"80/tcp": []nat.PortBinding{{HostPort: hostPort}}}
	}
	created, err := DockerClient.ContainerCreate(DockerContext, config, hostConfig, nil, nil, livePrefix+name)
	if err != nil {
		t.Fatal(err)
	}
	if running {
		if err := DockerClient.ContainerStart(DockerContext, created.ID, conttype.StartOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	return created.ID
}

func liveInspect(t *testing.T, name string) types.ContainerJSON {
	t.Helper()
	inspected, err := DockerClient.ContainerInspect(DockerContext, livePrefix+name)
	if err != nil {
		t.Fatalf("the container %s must exist: %v", name, err)
	}
	return inspected
}

// liveNoLeftovers fails when a container that was set aside during an edit is
// still there.
func liveNoLeftovers(t *testing.T) {
	t.Helper()
	containers, err := DockerClient.ContainerList(DockerContext, conttype.ListOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range containers {
		for _, name := range c.Names {
			if strings.Contains(name, asideMarker) {
				t.Errorf("a set-aside container was left behind: %s", name)
			}
		}
	}
}

func liveHasEnv(c types.ContainerJSON, entry string) bool {
	for _, env := range c.Config.Env {
		if env == entry {
			return true
		}
	}
	return false
}

func TestSafeEditLiveStartFails(t *testing.T) {
	liveSetup(t)
	liveContainer(t, "blocker", "18081", true)
	id := liveContainer(t, "app", "18080", true)

	edited := liveInspect(t, "app")
	edited.Config.Env = []string{"MARK=edited"}
	edited.HostConfig.PortBindings = nat.PortMap{"80/tcp": []nat.PortBinding{{HostPort: "18081"}}}

	_, err := EditContainer(id, edited, false)
	if err == nil {
		t.Fatal("binding a port that is taken must fail and say so")
	}
	t.Logf("error returned: %v", err)

	now := liveInspect(t, "app")
	if now.ID != id {
		t.Errorf("the same container must be back, got a new one (%s, was %s)", now.ID[:12], id[:12])
	}
	if !now.State.Running {
		t.Error("the container was running before the edit and must run again")
	}
	if !liveHasEnv(now, "MARK=original") {
		t.Errorf("the previous configuration must be back, env is %v", now.Config.Env)
	}
	if got := now.HostConfig.PortBindings["80/tcp"]; len(got) != 1 || got[0].HostPort != "18080" {
		t.Errorf("the previous port binding must be back, got %v", got)
	}
	liveNoLeftovers(t)
}

func TestSafeEditLiveCreateFails(t *testing.T) {
	liveSetup(t)
	id := liveContainer(t, "app", "", true)

	edited := liveInspect(t, "app")
	edited.Config.Env = []string{"MARK=edited"}
	edited.HostConfig.Resources.Memory = 1024 // below Docker's minimum: creation is refused

	_, err := EditContainer(id, edited, false)
	if err == nil {
		t.Fatal("a configuration Docker refuses must fail and say so")
	}
	t.Logf("error returned: %v", err)

	now := liveInspect(t, "app")
	if now.ID != id {
		t.Errorf("the same container must be back, got a new one (%s, was %s)", now.ID[:12], id[:12])
	}
	if !now.State.Running {
		t.Error("the container was running before the edit and must run again")
	}
	if !liveHasEnv(now, "MARK=original") {
		t.Errorf("the previous configuration must be back, env is %v", now.Config.Env)
	}
	liveNoLeftovers(t)
}

func TestSafeEditLiveStoppedStaysStopped(t *testing.T) {
	liveSetup(t)
	id := liveContainer(t, "app", "", false)

	edited := liveInspect(t, "app")
	edited.HostConfig.Resources.Memory = 1024

	if _, err := EditContainer(id, edited, false); err == nil {
		t.Fatal("a configuration Docker refuses must fail and say so")
	}

	now := liveInspect(t, "app")
	if now.ID != id {
		t.Errorf("the same container must be back, got a new one (%s, was %s)", now.ID[:12], id[:12])
	}
	if now.State.Running {
		t.Error("a container that was stopped before the edit must not be started by a failed edit")
	}
	liveNoLeftovers(t)
}

func TestSafeEditLiveKeepsNetworkSettings(t *testing.T) {
	liveSetup(t)
	if _, err := DockerClient.NetworkCreate(DockerContext, livePrefix+"net", types.NetworkCreate{}); err != nil {
		t.Fatal(err)
	}
	id := liveContainer(t, "app", "", true)
	err := DockerClient.NetworkConnect(DockerContext, livePrefix+"net", id, &network.EndpointSettings{Aliases: []string{"database"}})
	if err != nil {
		t.Fatal(err)
	}

	edited := liveInspect(t, "app")
	edited.HostConfig.Resources.Memory = 1024

	if _, err := EditContainer(id, edited, false); err == nil {
		t.Fatal("a configuration Docker refuses must fail and say so")
	}

	now := liveInspect(t, "app")
	endpoint := now.NetworkSettings.Networks[livePrefix+"net"]
	if endpoint == nil {
		t.Fatal("the container must still be on its network")
	}
	found := false
	for _, alias := range endpoint.Aliases {
		if alias == "database" {
			found = true
		}
	}
	if !found {
		t.Errorf("the network alias must survive a failed edit, aliases are %v", endpoint.Aliases)
	}
	liveNoLeftovers(t)
}

func TestSafeEditLiveSuccess(t *testing.T) {
	liveSetup(t)
	id := liveContainer(t, "app", "18080", true)

	edited := liveInspect(t, "app")
	edited.Config.Env = []string{"MARK=edited"}
	edited.HostConfig.PortBindings = nat.PortMap{"80/tcp": []nat.PortBinding{{HostPort: "18082"}}}

	newID, err := EditContainer(id, edited, false)
	if err != nil {
		t.Fatalf("a valid edit must succeed: %v", err)
	}

	now := liveInspect(t, "app")
	if now.ID == id || now.ID != newID {
		t.Errorf("the edit must leave the new container under the old name, got %s", now.ID[:12])
	}
	if !now.State.Running {
		t.Error("the edited container must run")
	}
	if !liveHasEnv(now, "MARK=edited") {
		t.Errorf("the new configuration must be applied, env is %v", now.Config.Env)
	}
	if _, err := DockerClient.ContainerInspect(DockerContext, id); err == nil {
		t.Error("the old container must be removed once the new one runs")
	}
	liveNoLeftovers(t)
}

// An update whose new image cannot start: the tag now points at an image
// without the command the container runs. Creating the old container again
// from its settings would use the same tag, that is the broken image.
func TestSafeEditLiveBrokenImageUpdate(t *testing.T) {
	liveSetup(t)
	tag := livePrefix + "image:latest"
	if err := DockerClient.ImageTag(DockerContext, liveImage, tag); err != nil {
		t.Fatal(err)
	}
	created, err := DockerClient.ContainerCreate(DockerContext,
		&conttype.Config{Image: tag, Cmd: []string{"sleep", "3600"}, StopSignal: "SIGKILL"},
		&conttype.HostConfig{NetworkMode: "bridge"}, nil, nil, livePrefix+"app")
	if err != nil {
		t.Fatal(err)
	}
	if err := DockerClient.ContainerStart(DockerContext, created.ID, conttype.StartOptions{}); err != nil {
		t.Fatal(err)
	}

	// the "new version": no sleep in it
	if err := DockerClient.ImageTag(DockerContext, "hello-world", tag); err != nil {
		t.Fatal(err)
	}

	_, err = EditContainer(created.ID, liveInspect(t, "app"), false)
	if err == nil {
		t.Fatal("an image that cannot start must fail the update and say so")
	}
	t.Logf("error returned: %v", err)

	now := liveInspect(t, "app")
	if now.ID != created.ID {
		t.Errorf("the same container must be back, got a new one (%s, was %s)", now.ID[:12], created.ID[:12])
	}
	if !now.State.Running {
		t.Error("the container must run again, on the image it had")
	}
	liveNoLeftovers(t)
}

// liveService replaces the container "app" through the service (compose)
// path, publishing container port 80 on hostPort.
func liveService(t *testing.T, hostPort string) error {
	t.Helper()
	t.Setenv("CONFIG_FILE", filepath.Join(t.TempDir(), "cosmos.config.json"))
	return CreateService(DockerServiceCreateRequest{
		Services: map[string]ContainerCreateRequestContainer{
			"app": {
				Name:        livePrefix + "app",
				Image:       liveImage,
				Command:     "sleep 3600",
				StopSignal:  "SIGKILL",
				Environment: []string{"MARK=edited"},
				Ports:       []string{hostPort + ":80"},
			},
		},
	}, func(string) {})
}

func TestSafeEditLiveServiceUpdateFails(t *testing.T) {
	liveSetup(t)
	liveContainer(t, "blocker", "18081", true)
	id := liveContainer(t, "app", "18080", true)

	err := liveService(t, "18081")
	if err == nil {
		t.Fatal("binding a port that is taken must fail and say so")
	}
	t.Logf("error returned: %v", err)

	now := liveInspect(t, "app")
	if now.ID != id {
		t.Errorf("the same container must be back, got a new one (%s, was %s)", now.ID[:12], id[:12])
	}
	if !now.State.Running {
		t.Error("the container was running before the update and must run again")
	}
	if !liveHasEnv(now, "MARK=original") {
		t.Errorf("the previous configuration must be back, env is %v", now.Config.Env)
	}
	liveNoLeftovers(t)
}

func TestSafeEditLiveServiceUpdateSucceeds(t *testing.T) {
	liveSetup(t)
	id := liveContainer(t, "app", "18080", true)

	if err := liveService(t, "18082"); err != nil {
		t.Fatalf("a valid update must succeed: %v", err)
	}

	now := liveInspect(t, "app")
	if now.ID == id {
		t.Error("the update must replace the container")
	}
	if !now.State.Running || !liveHasEnv(now, "MARK=edited") {
		t.Errorf("the new container must run with the new configuration, env is %v", now.Config.Env)
	}
	if _, err := DockerClient.ContainerInspect(DockerContext, id); err == nil {
		t.Error("the old container must be removed once the service is up")
	}
	liveNoLeftovers(t)
}
