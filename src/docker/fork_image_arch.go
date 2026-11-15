package docker

// Fork addition: does an image run on this server, asked before it is pulled.
//
// Upstream pulled whatever the definition named; an image made only for
// another processor failed at the pull ("no matching manifest") or, worse,
// was pulled under emulation and crash-looped after start. The registry
// lists the platforms of an image without a pull (the Engine's
// /distribution/{name}/json), and the Docker daemon says which platform it
// runs on. The lookup goes through the daemon Cosmos already talks to: no
// client of Cosmos' own contacts a registry.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
	dockerConfig "github.com/docker/cli/cli/config"
)

// ImagePlatforms is what is known of an image's platforms.
type ImagePlatforms struct {
	Image string `json:"image"`
	// Platforms such as "linux/amd64", "linux/arm64/v8"; empty when the registry did not say
	Platforms []string `json:"platforms"`
	// Fits: "yes", "no" or "unknown" (registry not reachable, private, single manifest without platform)
	Fits string `json:"fits"`
}

// ServerPlatform is the platform the Docker daemon runs containers for.
type ServerPlatform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

// normalArch turns the kernel's names into the ones registries use.
func normalArch(arch string) string {
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "x86_64", "x86-64", "amd64":
		return "amd64"
	case "aarch64", "arm64", "armv8", "armv8l":
		return "arm64"
	case "armv7l", "armv7", "armhf", "armv6l", "arm":
		return "arm"
	case "i386", "i686", "386":
		return "386"
	}
	return strings.ToLower(strings.TrimSpace(arch))
}

// platformFits tells whether one of the platforms runs on the server.
func platformFits(platforms []string, server ServerPlatform) string {
	if len(platforms) == 0 || server.Arch == "" {
		return "unknown"
	}
	for _, platform := range platforms {
		parts := strings.Split(platform, "/")
		if len(parts) < 2 {
			continue
		}
		osName, arch := parts[0], normalArch(parts[1])
		if (osName == server.OS || server.OS == "") && arch == server.Arch {
			return "yes"
		}
	}
	return "no"
}

var (
	serverPlatformMu sync.Mutex
	serverPlatformAt time.Time
	serverPlatformIs ServerPlatform
)

// askServerPlatform asks the daemon, remembered for ten minutes.
var askServerPlatform = func() ServerPlatform {
	serverPlatformMu.Lock()
	defer serverPlatformMu.Unlock()
	if serverPlatformIs.Arch != "" && time.Since(serverPlatformAt) < 10*time.Minute {
		return serverPlatformIs
	}
	if DockerClient == nil {
		return ServerPlatform{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, err := DockerClient.Info(ctx)
	if err != nil {
		return ServerPlatform{}
	}
	serverPlatformIs = ServerPlatform{OS: strings.ToLower(info.OSType), Arch: normalArch(info.Architecture)}
	serverPlatformAt = time.Now()
	serverRootDir = info.DockerRootDir
	return serverPlatformIs
}

// serverRootDir is Docker's data root, read with the platform.
var serverRootDir string

// dockerRootDir is Docker's data root ("" when Docker does not answer).
func dockerRootDir() string {
	askServerPlatform()
	serverPlatformMu.Lock()
	defer serverPlatformMu.Unlock()
	return serverRootDir
}

type platformAnswer struct {
	platforms []string
	at        time.Time
}

var (
	platformCacheMu sync.Mutex
	platformCache   = map[string]platformAnswer{}
)

// registryAuthFor reads the credentials Docker has for the image's registry,
// the way DockerPullImage does. Never logged.
func registryAuthFor(image string) string {
	registry := resolveRegistry(image)
	if configfile, err := dockerConfig.Load(dockerConfig.Dir()); err == nil {
		if creds, err := configfile.GetAuthConfig(registry); err == nil && creds.Username != "" {
			encoded, _ := json.Marshal(creds)
			return base64.URLEncoding.EncodeToString(encoded)
		}
	}
	if ImagePullAuthProvider != nil && !strings.HasPrefix(registry, "https://") {
		if user, pass := ImagePullAuthProvider(registry); user != "" {
			encoded, _ := json.Marshal(map[string]string{"username": user, "password": pass, "serveraddress": registry})
			return base64.URLEncoding.EncodeToString(encoded)
		}
	}
	return ""
}

// askImagePlatforms asks the registry, through the daemon, which platforms
// an image is made for. An answer is remembered for ten minutes, a failure
// for one. Replaced by the tests.
var askImagePlatforms = func(image string) ([]string, error) {
	// an image "Install any image" read in the last half hour: no second read of
	// the registry (on Docker Hub every manifest read counts as a pull)
	if KnownPlatforms != nil {
		if platforms, ok := KnownPlatforms(image); ok {
			return platforms, nil
		}
	}
	platformCacheMu.Lock()
	if cached, ok := platformCache[image]; ok {
		keep := 10 * time.Minute
		if cached.platforms == nil {
			keep = time.Minute
		}
		if time.Since(cached.at) < keep {
			platformCacheMu.Unlock()
			if cached.platforms == nil {
				return nil, fmt.Errorf("not known")
			}
			return cached.platforms, nil
		}
	}
	if len(platformCache) > 256 {
		platformCache = map[string]platformAnswer{}
	}
	platformCacheMu.Unlock()

	if DockerClient == nil {
		return nil, fmt.Errorf("Docker is not connected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	answer, err := DockerClient.DistributionInspect(ctx, image, registryAuthFor(image))

	platforms := []string(nil)
	if err == nil {
		platforms = []string{}
		for _, platform := range answer.Platforms {
			if platform.OS == "" || platform.OS == "unknown" || platform.Architecture == "" || platform.Architecture == "unknown" {
				continue // attestations and the like
			}
			name := platform.OS + "/" + platform.Architecture
			if platform.Variant != "" {
				name += "/" + platform.Variant
			}
			platforms = append(platforms, name)
		}
		sort.Strings(platforms)
	}
	platformCacheMu.Lock()
	platformCache[image] = platformAnswer{platforms: platforms, at: time.Now()}
	platformCacheMu.Unlock()
	if err != nil {
		return nil, err
	}
	return platforms, nil
}

// CheckImagePlatforms answers for each image whether it runs on the server.
func CheckImagePlatforms(images []string) (ServerPlatform, []ImagePlatforms) {
	server := askServerPlatform()
	result := make([]ImagePlatforms, 0, len(images))
	for _, image := range images {
		entry := ImagePlatforms{Image: image, Platforms: []string{}, Fits: "unknown"}
		if platforms, err := askImagePlatforms(image); err == nil {
			entry.Platforms = platforms
			entry.Fits = platformFits(platforms, server)
		} else {
			utils.Debug("Image platforms: " + utils.SanitizeSafe(image) + ": " + err.Error())
		}
		result = append(result, entry)
	}
	return server, result
}

// platformRefusal is the sentence for images that do not run on the server,
// "" when every image fits or is not known.
func platformRefusal(request DockerServiceCreateRequest) string {
	images := []string{}
	seen := map[string]bool{}
	keys := make([]string, 0, len(request.Services))
	for key := range request.Services {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		image := strings.TrimSpace(request.Services[key].Image)
		if image != "" && !seen[image] {
			seen[image] = true
			images = append(images, image)
		}
	}
	if len(images) > 16 {
		images = images[:16]
	}
	server, answers := CheckImagePlatforms(images)
	problems := []string{}
	for _, answer := range answers {
		if answer.Fits == "no" {
			problems = append(problems, fmt.Sprintf("%s is made for %s only", answer.Image, strings.Join(answer.Platforms, ", ")))
		}
	}
	if len(problems) == 0 {
		return ""
	}
	return strings.Join(problems, "; ") + "; this server runs " + server.OS + "/" + server.Arch +
		". Nothing was pulled or created. To try anyway (under emulation, if the server has it), confirm \"install anyway\" on the review."
}

// KnownPlatforms, when set (anyimage.KnownPlatforms), answers the platforms
// of an image read recently, so that they are not asked for again.
var KnownPlatforms func(image string) ([]string, bool)

// ForkDockerRoot is Docker's data root, where named volumes are kept ("" when
// Docker does not answer).
func ForkDockerRoot() string { return dockerRootDir() }
