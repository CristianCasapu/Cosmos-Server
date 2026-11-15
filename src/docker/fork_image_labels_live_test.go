package docker

// Fork addition: image labels against a real Docker daemon (a daemon of the
// test's own, see fork_safe_edit_live_test.go):
//
//	COSMOS_FORK_LIVE_DOCKER=1 DOCKER_HOST=tcp://<dind>:2375 go test ./src/docker/ -run ImageLabelsLive -v

import (
	"testing"

	"github.com/docker/docker/api/types"
	conttype "github.com/docker/docker/api/types/container"
)

const liveLabelImage = livePrefix + "labels:latest"

func TestImageLabelsLive(t *testing.T) {
	liveSetup(t)
	t.Cleanup(func() { DockerClient.ImageRemove(DockerContext, liveLabelImage, types.ImageRemoveOptions{Force: true}) })

	// an image that asks for the host's network and for automatic updates
	base := liveContainer(t, "label-base", "", false)
	if _, err := DockerClient.ContainerCommit(DockerContext, base, conttype.CommitOptions{
		Reference: liveLabelImage,
		Changes:   []string{`LABEL cosmos-force-network-mode="host" cosmos-auto-update="true"`},
	}); err != nil {
		t.Fatal(err)
	}

	created, err := DockerClient.ContainerCreate(DockerContext,
		&conttype.Config{Image: liveLabelImage, Cmd: []string{"sleep", "3600"}, StopSignal: "SIGKILL"},
		&conttype.HostConfig{NetworkMode: "bridge"}, nil, nil, livePrefix+"labelled")
	if err != nil {
		t.Fatal(err)
	}
	if err := DockerClient.ContainerStart(DockerContext, created.ID, conttype.StartOptions{}); err != nil {
		t.Fatal(err)
	}

	inspected := liveInspect(t, "labelled")
	if inspected.Config.Labels["cosmos-force-network-mode"] != "host" {
		t.Fatalf("Docker did not copy the image's labels into the container (%v): the test proves nothing", inspected.Config.Labels)
	}
	if HasAutoUpdateOn(inspected) {
		t.Error("automatic updates are on because the image says so")
	}

	// a recreate (what an update, an edit or a switch of the panel does)
	if _, err := EditContainer(created.ID, inspected, false); err != nil {
		t.Fatalf("recreate: %v", err)
	}
	now := liveInspect(t, "labelled")
	if mode := string(now.HostConfig.NetworkMode); mode != "bridge" {
		t.Errorf("after a recreate the container runs in network mode %q, the image's choice; want bridge", mode)
	}
	// and once more: the label Docker copied again is still the image's
	if _, err := EditContainer(now.ID, now, false); err != nil {
		t.Fatalf("second recreate: %v", err)
	}
	if mode := string(liveInspect(t, "labelled").HostConfig.NetworkMode); mode != "bridge" {
		t.Errorf("after a second recreate the network mode is %q; want bridge", mode)
	}
	liveNoLeftovers(t)
}
