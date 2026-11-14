package docker

// Fork addition: the leftovers of interrupted edits (fork_leftovers.go)
// against a real Docker daemon, gated like fork_safe_edit_live_test.go and
// with its helpers (containers named cosmos-edit-live-*):
//
//	COSMOS_FORK_LIVE_DOCKER=1 DOCKER_HOST=tcp://<dind>:2375 go test ./src/docker/ -run LeftoversLive -v
//
// The image busybox:1.36 must be present in that daemon.

import (
	"strings"
	"testing"

	conttype "github.com/docker/docker/api/types/container"
)

// liveLeftover makes what an interrupted edit leaves: a stopped container
// renamed "<name>-cosmos-old-<its ID>".
func liveLeftover(t *testing.T, app string) (string, string) {
	t.Helper()
	id := liveContainer(t, app, "", true)
	if err := DockerClient.ContainerStop(DockerContext, id, conttype.StopOptions{}); err != nil {
		t.Fatal(err)
	}
	name := asideNameFor(livePrefix+app, id)
	if err := DockerClient.ContainerRename(DockerContext, id, name); err != nil {
		t.Fatal(err)
	}
	return id, name
}

// liveNoOwnLeftovers fails when one of this test's containers is still set
// aside (other containers of the daemon are not looked at).
func liveNoOwnLeftovers(t *testing.T) {
	t.Helper()
	found, err := ListLeftovers()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range found {
		if strings.HasPrefix(entry.Name, livePrefix) {
			t.Errorf("a set-aside container was left behind: %s", entry.Name)
		}
	}
}

func liveFindLeftover(t *testing.T, name string) (Leftover, bool) {
	t.Helper()
	found, err := ListLeftovers()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range found {
		if entry.Name == name {
			return entry, true
		}
	}
	return Leftover{}, false
}

func TestLeftoversLiveRestoreWithoutReplacement(t *testing.T) {
	liveSetup(t)
	id, name := liveLeftover(t, "lone")

	entry, ok := liveFindLeftover(t, name)
	if !ok || entry.App != livePrefix+"lone" || entry.ID != id[:12] || entry.Running || entry.AppExists || entry.Finished == "" || entry.Created == "" {
		t.Fatalf("listed %+v (%v)", entry, ok)
	}

	result, err := restoreLeftover(name)
	if err != nil {
		t.Fatal(err)
	}
	if result.SetAside != "" {
		t.Errorf("result %+v", result)
	}
	back := liveInspect(t, "lone")
	if back.ID != id || !back.State.Running {
		t.Errorf("the container is %s, running %v", back.ID, back.State.Running)
	}
	liveNoOwnLeftovers(t)
}

func TestLeftoversLiveRestoreWithReplacement(t *testing.T) {
	liveSetup(t)
	id, name := liveLeftover(t, "app")
	replacement := liveContainer(t, "app", "", true)

	entry, ok := liveFindLeftover(t, name)
	if !ok || !entry.AppExists || !entry.AppRunning {
		t.Fatalf("listed %+v (%v)", entry, ok)
	}

	result, err := restoreLeftover(name)
	if err != nil {
		t.Fatal(err)
	}
	if result.SetAside != asideNameFor(livePrefix+"app", replacement) {
		t.Errorf("result %+v", result)
	}
	back := liveInspect(t, "app")
	if back.ID != id || !back.State.Running {
		t.Errorf("the container named app is %s, running %v", back.ID, back.State.Running)
	}
	// the replacement is kept, stopped, and is a leftover itself
	aside, err := DockerClient.ContainerInspect(DockerContext, result.SetAside)
	if err != nil || aside.ID != replacement || aside.State.Running {
		t.Fatalf("the replacement: %v", err)
	}
	if entry, ok := liveFindLeftover(t, result.SetAside); !ok || !entry.AppRunning {
		t.Errorf("the replacement is not listed as a leftover: %+v", entry)
	}

	// removed, its volumes left alone
	if err := removeLeftover(result.SetAside); err != nil {
		t.Fatal(err)
	}
	if _, err := DockerClient.ContainerInspect(DockerContext, replacement); err == nil {
		t.Error("the removed leftover is still there")
	}
	liveNoOwnLeftovers(t)
}

func TestLeftoversLiveRestoreRollsBack(t *testing.T) {
	liveSetup(t)
	// the previous container cannot start (it publishes a port the replacement holds)
	old := liveContainer(t, "web", "18555", true)
	if err := DockerClient.ContainerStop(DockerContext, old, conttype.StopOptions{}); err != nil {
		t.Fatal(err)
	}
	name := asideNameFor(livePrefix+"web", old)
	if err := DockerClient.ContainerRename(DockerContext, old, name); err != nil {
		t.Fatal(err)
	}
	other := liveContainer(t, "holder", "18555", true)
	replacement := liveContainer(t, "web", "", true)

	_, err := restoreLeftover(name)
	failed, ok := err.(*restoreError)
	if !ok || !failed.undone {
		t.Fatalf("error %v", err)
	}
	if back := liveInspect(t, "web"); back.ID != replacement || !back.State.Running {
		t.Errorf("the container named web is %s, running %v", back.ID, back.State.Running)
	}
	if kept, err := DockerClient.ContainerInspect(DockerContext, name); err != nil || kept.ID != old || kept.State.Running {
		t.Errorf("the leftover is not kept as it was: %v", err)
	}
	if holder := liveInspect(t, "holder"); holder.ID != other || !holder.State.Running {
		t.Error("the container holding the port was touched")
	}
}

func TestLeftoversLiveRemove(t *testing.T) {
	liveSetup(t)
	id, name := liveLeftover(t, "gone")

	if err := DockerClient.ContainerStart(DockerContext, id, conttype.StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := removeLeftover(name); err != errLeftoverRunning {
		t.Fatalf("a running leftover: %v", err)
	}
	if err := DockerClient.ContainerStop(DockerContext, id, conttype.StopOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := removeLeftover(name); err != nil {
		t.Fatal(err)
	}
	if _, err := DockerClient.ContainerInspect(DockerContext, id); err == nil {
		t.Error("the leftover is still there")
	}
}
