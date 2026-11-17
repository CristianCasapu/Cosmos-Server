package main

// Fork addition: what the review of an installation needs to know about the
// server before anything is pulled or created.
//
//   POST /cosmos/api/install/plan  {"images": ["jellyfin/jellyfin:10.9", ...]}
//
// answers the platform the Docker daemon runs (os/arch), per image the
// platforms its registry lists and whether one fits (docker/fork_image_arch.go:
// asked through the daemon, nothing pulled), and the places an app's data can
// go: the default data path, each data disk with its free space, Docker's
// data root (where named volumes are kept), each marked when it is on the
// disk the system runs from. The review proposes a place per volume from it
// (client/src/utils/dataPlaces.js). PERM_RESOURCES, like creating the service.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/azukaar/cosmos-server/src/anyimage"
	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/storage"
	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	installPlanMaxBody   = 16 << 10
	installPlanMaxImages = 8
)

// installPlace is one place an app's data can go.
type installPlace struct {
	Path string `json:"path"`
	// Kind: "data-path" (Configuration > Docker), "disk" (a data disk),
	// "docker-root" (where named volumes go), "system" (the disk the system runs from)
	Kind   string `json:"kind"`
	Free   uint64 `json:"free"`
	Total  uint64 `json:"total"`
	OSDisk bool   `json:"osDisk"`
	// Disk is the mount point of the disk the place is on
	Disk string `json:"disk"`
	// Exists is false for a path that would be created (the data path before the first install)
	Exists bool `json:"exists"`
}

type installPlanAnswer struct {
	Server   docker.ServerPlatform   `json:"server"`
	Images   []docker.ImagePlatforms `json:"images"`
	DataPath string                  `json:"dataPath"`
	Places   []installPlace          `json:"places"`
	// Owner of the data path ("1000:1000"), "" when not known: proposed for PUID/PGID
	DataOwner string `json:"dataOwner"`
	// This server's folders that need a confirmation besides the system's (docker/fork_sensitive_access.go)
	Protected []string `json:"protected"`
	// Plan 45: per image, how one signs in to it and what its passwords
	// must be (its recipe's login), or that it makes its first account in its
	// own page; the review fills the sign-in of every door from it
	SignIn map[string]installSignIn `json:"signIn"`
}

type installSignIn struct {
	Name string `json:"name,omitempty"`
	// AdminOnly: its address is made for administrators behind Cosmos' sign-in by the server (appsignin/check.go)
	AdminOnly bool                  `json:"adminOnly,omitempty"`
	Login     *anyimage.RecipeLogin `json:"login,omitempty"`
	FirstRun  *anyimage.FirstRunApp `json:"firstRun,omitempty"`
}

func signInOfImages(images []string) map[string]installSignIn {
	out := map[string]installSignIn{}
	for _, image := range images {
		entry := installSignIn{}
		if recipe := anyimage.RecipeForImage(image); recipe != nil && recipe.Login != nil {
			entry.Name, entry.Login = recipe.Name, recipe.Login
		} else if first := anyimage.FirstRunForImage(image); first != nil {
			entry.Name, entry.FirstRun = first.Name, first
		}
		if recipe := anyimage.RecipeForImage(image); recipe != nil && (recipe.Manager != nil || (recipe.Login != nil && recipe.Login.Via == "database") || recipeHasNote(recipe, "noAuth")) {
			entry.AdminOnly = true
			if entry.Name == "" {
				entry.Name = recipe.Name
			}
		}
		if entry.Login != nil || entry.FirstRun != nil || entry.AdminOnly {
			out[image] = entry
		}
	}
	return out
}

// hostFile is where a host path is seen from Cosmos.
func hostFile(path string) string {
	if utils.IsInsideContainer {
		return "/mnt/host" + path
	}
	return path
}

// existingAncestor is the path itself or its nearest parent that exists.
func existingAncestor(path string) (string, bool) {
	current := filepath.Clean(path)
	exists := true
	for {
		if _, err := os.Stat(hostFile(current)); err == nil {
			return current, exists
		}
		exists = false
		if current == "/" {
			return "/", false
		}
		current = filepath.Dir(current)
	}
}

// deviceOf is the filesystem a path is on.
func deviceOf(path string) (uint64, bool) {
	info, err := os.Stat(hostFile(path))
	if err != nil {
		return 0, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(stat.Dev), true
}

// measurePlace fills free and total space and the disk of a place.
var measurePlace = func(place installPlace, disks []string) installPlace {
	existing, exists := existingAncestor(place.Path)
	place.Exists = exists
	var fs syscall.Statfs_t
	if syscall.Statfs(hostFile(existing), &fs) == nil {
		place.Free = fs.Bavail * uint64(fs.Bsize)
		place.Total = fs.Blocks * uint64(fs.Bsize)
	}
	device, okDevice := deviceOf(existing)
	root, okRoot := deviceOf("/")
	place.OSDisk = okDevice && okRoot && device == root
	place.Disk = "/"
	for _, disk := range disks {
		if (existing == disk || strings.HasPrefix(existing, strings.TrimSuffix(disk, "/")+"/")) && len(disk) > len(place.Disk) {
			place.Disk = disk
		}
	}
	return place
}

// dataDisks lists the data disks' mount points. Replaced by the tests.
var dataDisks = func() []string {
	paths := []string{}
	for path := range storage.ForkDataMountPaths() {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

var dockerRootForPlan = docker.ForkDockerRoot

var checkImagesForPlan = docker.CheckImagePlatforms

// the processor check reuses what "Install any image" read (plan 40)
func init() { docker.KnownPlatforms = anyimage.KnownPlatforms }

var protectedForPlan = docker.ForkProtectedDirs

// ownerOf is "uid:gid" of the path, "" when it does not exist.
func ownerOf(path string) string {
	info, err := os.Stat(hostFile(path))
	if err != nil {
		return ""
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return strconv.Itoa(int(stat.Uid)) + ":" + strconv.Itoa(int(stat.Gid))
}

func buildInstallPlan(images []string, config utils.Config) installPlanAnswer {
	answer := installPlanAnswer{DataPath: config.DockerConfig.DefaultDataPath}
	answer.Server, answer.Images = checkImagesForPlan(images)

	disks := dataDisks()
	places := []installPlace{}
	seen := map[string]bool{}
	add := func(path string, kind string) {
		path = filepath.Clean(path)
		if path == "" || path == "." || seen[path] {
			return
		}
		seen[path] = true
		place := measurePlace(installPlace{Path: path, Kind: kind}, disks)
		places = append(places, place)
	}
	if answer.DataPath != "" {
		add(answer.DataPath, "data-path")
	}
	for _, disk := range disks {
		add(disk, "disk")
	}
	if root := dockerRootForPlan(); root != "" {
		add(root, "docker-root")
	}
	add("/", "system")
	answer.Places = places
	answer.Protected = protectedForPlan()
	answer.SignIn = signInOfImages(images)

	if answer.DataPath != "" {
		answer.DataOwner = ownerOf(answer.DataPath)
		if answer.DataOwner == "0:0" {
			answer.DataOwner = "" // root's: not a proposal for an app's user
		}
	}
	return answer
}

// InstallPlanRoute answers POST /cosmos/api/install/plan.
func InstallPlanRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_RESOURCES) != nil {
		return
	}
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	var body struct {
		Images []string `json:"images"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, installPlanMaxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		utils.HTTPError(w, "Invalid request", http.StatusBadRequest, "IP001")
		return
	}
	if len(body.Images) > installPlanMaxImages {
		utils.HTTPError(w, "At most 8 images", http.StatusBadRequest, "IP002")
		return
	}
	images := []string{}
	for _, image := range body.Images {
		image = strings.TrimSpace(image)
		if image == "" || len(image) > 256 || strings.ContainsAny(image, " \t\r\n\x00") {
			utils.HTTPError(w, "Invalid image name", http.StatusBadRequest, "IP003")
			return
		}
		images = append(images, image)
	}

	answer := buildInstallPlan(images, utils.GetMainConfig())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": answer})
}

func recipeHasNote(recipe *anyimage.Recipe, note string) bool {
	for _, n := range recipe.Notes {
		if n == note {
			return true
		}
	}
	return false
}
