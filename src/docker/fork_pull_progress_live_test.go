package docker

// Fork addition: the pull step of a creation against a real Docker daemon, never the machine's:
//
//	COSMOS_FORK_LIVE_DOCKER=1 DOCKER_HOST=tcp://<dind>:2375 \
//	  PULL_LIVE_REGISTRY=<slow line host:port> PULL_LIVE_CORRUPT=<corrupting line host:port> \
//	  go test ./src/docker/ -run PullLive -v -count=1
//
// The Docker in Docker is on an --internal network (so a name outside it does not resolve), runs a
// registry:3 holding fork/bigapp:1 (a few tens of MB of random layers) and trusts the two lines as
// insecure registries; the lines are scripts/fork-fake-slow-registry.go in front of that registry,
// one slow, one that changes a byte after 3 MB ("corrupt"). Containers are named cosmos-edit-live-*.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
)

func pullLiveImage() string {
	if image := os.Getenv("PULL_LIVE_IMAGE"); image != "" {
		return image
	}
	return "fork/bigapp:1"
}

// pullLiveCreate runs a one-container creation of image and answers its error, its log and its markers.
func pullLiveCreate(t *testing.T, image string) (error, string, []PullProgress) {
	t.Helper()
	liveSetup(t)
	t.Setenv("CONFIG_FILE", filepath.Join(t.TempDir(), "cosmos.config.json"))
	DockerClient.ImageRemove(DockerContext, image, types.ImageRemoveOptions{Force: true})
	t.Cleanup(func() {
		liveCleanup(t)
		DockerClient.ImageRemove(DockerContext, image, types.ImageRemoveOptions{Force: true})
	})
	var request DockerServiceCreateRequest
	body, _ := json.Marshal(map[string]any{"services": map[string]any{"pull": map[string]any{
		"container_name": livePrefix + "pull", "image": image, "command": []string{"sleep", "3600"},
	}}})
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	r := &pullRecorder{}
	err := CreateService(request, r.onLog)
	return err, strings.Join(r.lines, ""), r.markers(t)
}

func pullLiveRegistry(t *testing.T, name string) string {
	t.Helper()
	registry := os.Getenv(name)
	if registry == "" && os.Getenv("COSMOS_FORK_LIVE_DOCKER") != "" {
		t.Skip("set " + name + " to the test registry's line (see the file's header)")
	}
	return registry
}

func TestPullLiveWorksWithProgress(t *testing.T) {
	image := pullLiveRegistry(t, "PULL_LIVE_REGISTRY") + "/" + pullLiveImage()
	err, log, markers := pullLiveCreate(t, image)
	if err != nil {
		t.Fatalf("%v\n%s", err, log)
	}
	if !strings.Contains(log, "Image "+image+" pulled\n") || !strings.Contains(log, "[OPERATION SUCCEEDED]") {
		t.Fatalf("upstream's lines are missing:\n%s", log)
	}
	if !strings.Contains(log, `"status":"Pull complete"`) {
		t.Fatalf("Docker's own lines are missing:\n%s", log)
	}
	moving := false
	for _, m := range markers {
		if m.State == "pulling" && m.Total > 0 && m.Current > 0 && m.Current < m.Total {
			moving = true
		}
	}
	last := markers[len(markers)-1]
	if !moving || last.State != "done" || last.Layers == 0 || last.Ready != last.Layers {
		t.Fatalf("progress not seen: %+v", markers)
	}
	t.Logf("%d markers, last %+v", len(markers), last)
}

func TestPullLiveTagNotFound(t *testing.T) {
	image := pullLiveRegistry(t, "PULL_LIVE_REGISTRY") + "/fork/bigapp:nope"
	err, log, markers := pullLiveCreate(t, image)
	if err == nil || strings.Contains(log, " pulled\n") {
		t.Fatalf("a missing tag was taken for a pull:\n%s", log)
	}
	if last := markers[len(markers)-1]; last.State != "failed" || last.Reason != PullReasonNotFound {
		t.Fatalf("last marker %+v\n%s", last, log)
	}
	t.Log(markers[len(markers)-1].Detail)
}

func TestPullLiveNameDoesNotResolve(t *testing.T) {
	err, log, markers := pullLiveCreate(t, "registry.example.org/app:1")
	if err == nil || strings.Contains(log, " pulled\n") {
		t.Fatalf("an unknown registry was taken for a pull:\n%s", log)
	}
	if last := markers[len(markers)-1]; last.State != "failed" || last.Reason != PullReasonDNS {
		t.Fatalf("last marker %+v\n%s", last, log)
	}
	t.Log(markers[len(markers)-1].Detail)
}

// A download that fails its checksum: Docker says it in the stream only; upstream went on with
// "Image … pulled" and failed later at the container.
func TestPullLiveFailsInsideTheStream(t *testing.T) {
	image := pullLiveRegistry(t, "PULL_LIVE_CORRUPT") + "/" + pullLiveImage()
	err, log, markers := pullLiveCreate(t, image)
	if err == nil || strings.Contains(log, "Image "+image+" pulled") {
		t.Fatalf("a pull that failed in its stream was taken for a pull:\n%s", log)
	}
	if !strings.Contains(log, `"errorDetail"`) || !strings.Contains(log, "[OPERATION FAILED]") {
		t.Fatalf("Docker's error line or the failure is missing:\n%s", log)
	}
	if last := markers[len(markers)-1]; last.State != "failed" || last.Reason != PullReasonDamaged || last.Detail == "" {
		t.Fatalf("last marker %+v\n%s", last, log)
	}
	t.Log(markers[len(markers)-1].Reason, markers[len(markers)-1].Detail)
}
