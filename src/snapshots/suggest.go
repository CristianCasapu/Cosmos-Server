package snapshots

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

var (
	dockerRootMu  sync.Mutex
	dockerRootDir string
	dockerRootAt  time.Time
)

// dockerRoot is the folder Docker keeps its data in, "" when Docker does not
// answer. A variable so tests need no Docker.
var dockerRoot = askDockerRoot

// askDockerRoot asks the Docker daemon, through the client Cosmos already
// holds, and remembers the answer for a while: the status is read every few
// seconds while a run is in progress.
func askDockerRoot() string {
	dockerRootMu.Lock()
	defer dockerRootMu.Unlock()

	remember := time.Minute
	if dockerRootDir != "" {
		remember = 10 * time.Minute
	}
	if !dockerRootAt.IsZero() && time.Since(dockerRootAt) < remember {
		return dockerRootDir
	}
	dockerRootAt = time.Now()
	dockerRootDir = ""

	client := docker.DockerClient
	if client == nil || !docker.DockerIsConnected.Load() {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	info, err := client.Info(ctx)
	if err != nil {
		return ""
	}
	dockerRootDir = info.DockerRootDir
	return dockerRootDir
}

// suggested lists the folders worth backing up that exist on this machine:
// Cosmos' own folder, the folder the apps keep their data in, and Docker's
// volumes. The admin adds them with one click, or not.
func suggested(config utils.Config) []string {
	candidates := []string{}
	// Inside a container Cosmos' folder is a mount whose place on the host is
	// not known here.
	if !utils.IsInsideContainer {
		candidates = append(candidates, configFolder())
	}
	candidates = append(candidates, config.DockerConfig.DefaultDataPath)
	if root := dockerRoot(); root != "" {
		candidates = append(candidates, filepath.Join(root, "volumes"))
	}

	destination := config.Snapshots.Destination
	list := []string{}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		dir, err := cleanDirectory("", candidate)
		if err != nil {
			continue
		}
		if destination != "" && (dir == destination || inside(dir, destination)) {
			continue
		}
		if info, err := os.Stat(hostPath(dir)); err != nil || !info.IsDir() {
			continue
		}
		known := false
		for _, other := range list {
			known = known || other == dir
		}
		if !known {
			list = append(list, dir)
		}
	}
	return list
}
