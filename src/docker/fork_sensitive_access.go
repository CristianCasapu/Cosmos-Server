package docker

// Fork addition: access to the server that a definition must not get without
// the admin saying yes to each item.
//
// A compose file or a Market template can ask for the Docker socket (full
// control of the server), for "/" or "/etc", for Cosmos' own data, for
// privileged mode or the host's network. Upstream created whatever was asked.
// Here the create is refused unless the request names each such item in a
// `confirm` query value ("<service>|<kind>|<value>"); the review in the panel
// lists them in words with a tick per item, unticked. An edit (?replace=1)
// is not asked again for what the container already had.
//
// client/src/utils/sensitiveAccess.js holds the same rules for the review;
// both are tested against testdata/sensitive_cases.json.

import (
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/docker/docker/api/types/mount"
)

// SensitiveItem is one access that needs a confirmation.
type SensitiveItem struct {
	Service string `json:"service"`
	// Kind: bind (a host folder), socket (a container runtime's socket),
	// privileged, network-host, cap (a capability), unconfined (security_opt), device (a device of the server)
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Key is how the item is named in the confirmation.
func (s SensitiveItem) Key() string {
	return s.Service + "|" + s.Kind + "|" + s.Value
}

// System folders a container must not be handed by default, with everything
// below them. Cosmos' own folders and Docker's data are added at run time.
var sensitiveDirs = []string{
	"/etc", "/root", "/boot", "/proc", "/sys", "/dev", "/usr", "/bin", "/sbin",
	"/lib", "/lib32", "/lib64", "/libx32", "/run", "/var/run",
	"/var/lib/docker", "/var/lib/containerd", "/var/lib/containers",
	"/opt/cosmos", "/var/lib/cosmos",
}

// Socket names that give control of containers (and so of the server).
var sensitiveSockets = map[string]bool{
	"docker.sock": true, "containerd.sock": true, "podman.sock": true, "crio.sock": true,
}

// Capabilities that reach outside the container.
var sensitiveCaps = map[string]bool{
	"ALL": true, "SYS_ADMIN": true, "NET_ADMIN": true, "SYS_PTRACE": true, "SYS_MODULE": true,
	"SYS_RAWIO": true, "SYS_BOOT": true, "DAC_READ_SEARCH": true, "SYS_TIME": true, "BPF": true,
	"PERFMON": true, "MAC_ADMIN": true, "MAC_OVERRIDE": true,
}

// extraSensitiveDirs are this server's own: Cosmos' configuration folder and
// Docker's data root. Replaced by the tests.
var extraSensitiveDirs = func() []string {
	dirs := []string{}
	if utils.CONFIGFOLDER != "" && !utils.IsInsideContainer {
		dirs = append(dirs, utils.CONFIGFOLDER)
	}
	if root := dockerRootDir(); root != "" {
		dirs = append(dirs, root)
	}
	return dirs
}

// cleanHostPath is the plain form of a host path, "" for one that is not absolute.
func cleanHostPath(source string) string {
	source = strings.TrimSpace(source)
	if !strings.HasPrefix(source, "/") {
		return ""
	}
	return path.Clean(source)
}

func within(p string, dir string) bool {
	dir = path.Clean(dir)
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/")
}

// SensitiveBind tells how a bind source is sensitive: "socket", "bind" or "".
func SensitiveBind(source string, extra []string) string {
	p := cleanHostPath(source)
	if p == "" {
		return ""
	}
	if sensitiveSockets[path.Base(p)] {
		return "socket"
	}
	if p == "/" {
		return "bind"
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".ssh" || part == ".gnupg" {
			return "bind"
		}
	}
	for _, dir := range append(append([]string{}, sensitiveDirs...), extra...) {
		dir = cleanHostPath(dir)
		if dir == "" || dir == "/" {
			continue
		}
		// the folder itself, what is in it, and a folder that holds it
		if within(p, dir) || within(dir, p) {
			return "bind"
		}
	}
	return ""
}

func effectiveNetworkMode(service ContainerCreateRequestContainer) string {
	if forced := service.Labels["cosmos-force-network-mode"]; forced != "" {
		return forced
	}
	return service.NetworkMode
}

func serviceSensitive(key string, service ContainerCreateRequestContainer, extra []string) []SensitiveItem {
	items := []SensitiveItem{}
	for _, volume := range service.Volumes {
		if volume.Type != mount.TypeBind && volume.Type != "" {
			continue
		}
		if volume.Type == "" && !strings.HasPrefix(volume.Source, "/") {
			continue
		}
		if kind := SensitiveBind(volume.Source, extra); kind != "" {
			items = append(items, SensitiveItem{Service: key, Kind: kind, Value: cleanHostPath(volume.Source)})
		}
	}
	if service.Privileged {
		items = append(items, SensitiveItem{Service: key, Kind: "privileged", Value: "true"})
	}
	if effectiveNetworkMode(service) == "host" {
		items = append(items, SensitiveItem{Service: key, Kind: "network-host", Value: "host"})
	}
	for _, capability := range service.CapAdd {
		name := strings.ToUpper(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(capability)), "CAP_"))
		if sensitiveCaps[name] {
			items = append(items, SensitiveItem{Service: key, Kind: "cap", Value: name})
		}
	}
	for _, option := range service.SecurityOpt {
		option = strings.TrimSpace(option)
		if strings.HasSuffix(option, "=unconfined") || strings.HasSuffix(option, ":unconfined") {
			items = append(items, SensitiveItem{Service: key, Kind: "unconfined", Value: option})
		}
	}
	// Fork addition (plan 40): a device of the server (a disk, a graphics card, a USB stick) is used directly
	for _, device := range service.Devices {
		if host := strings.TrimSpace(strings.Split(device, ":")[0]); host != "" {
			items = append(items, SensitiveItem{Service: key, Kind: "device", Value: cleanHostPath(host)})
		}
	}
	return items
}

// SensitiveAccess lists what a request asks for that needs a confirmation.
func SensitiveAccess(request DockerServiceCreateRequest, extra []string) []SensitiveItem {
	keys := make([]string, 0, len(request.Services))
	for key := range request.Services {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := []SensitiveItem{}
	for _, key := range keys {
		items = append(items, serviceSensitive(key, request.Services[key], extra)...)
	}
	return items
}

// existingSensitive returns the keys of what the existing container of that
// name already has, so that an edit is not asked again. Replaced by the tests.
var existingSensitive = func(service string, name string, extra []string) map[string]bool {
	found := map[string]bool{}
	if DockerClient == nil {
		return found
	}
	existing, err := DockerClient.ContainerInspect(DockerContext, name)
	if err != nil || existing.HostConfig == nil || existing.Config == nil {
		return found
	}
	asService := ContainerCreateRequestContainer{
		Privileged:  existing.HostConfig.Privileged,
		NetworkMode: string(existing.HostConfig.NetworkMode),
		CapAdd:      existing.HostConfig.CapAdd,
		SecurityOpt: existing.HostConfig.SecurityOpt,
		Labels:      existing.Config.Labels,
	}
	for _, device := range existing.HostConfig.Devices {
		asService.Devices = append(asService.Devices, device.PathOnHost+":"+device.PathInContainer)
	}
	for _, m := range existing.Mounts {
		if m.Type == mount.TypeBind {
			asService.Volumes = append(asService.Volumes, mount.Mount{Type: mount.TypeBind, Source: m.Source})
		}
	}
	for _, item := range serviceSensitive(service, asService, extra) {
		found[item.Key()] = true
	}
	return found
}

// unconfirmedSensitive lists what the request asks for and the admin has not
// confirmed (query values `confirm`).
func unconfirmedSensitive(request DockerServiceCreateRequest, query url.Values) []SensitiveItem {
	extra := extraSensitiveDirs()
	confirmed := map[string]bool{}
	values := query["confirm"]
	if len(values) > 64 {
		values = values[:64]
	}
	for _, value := range values {
		if len(value) <= 512 {
			confirmed[value] = true
		}
	}
	replacing := query.Get("replace") == "1"
	missing := []SensitiveItem{}
	had := map[string]map[string]bool{}
	for _, item := range SensitiveAccess(request, extra) {
		if confirmed[item.Key()] {
			continue
		}
		if replacing {
			if had[item.Service] == nil {
				name := request.Services[item.Service].Name
				if name == "" {
					name = item.Service
				}
				had[item.Service] = existingSensitive(item.Service, name, extra)
			}
			if had[item.Service][item.Key()] {
				continue
			}
		}
		missing = append(missing, item)
	}
	return missing
}

// sensitiveSentence names the items in words for the refusal.
func sensitiveSentence(items []SensitiveItem) string {
	parts := []string{}
	for _, item := range items {
		switch item.Kind {
		case "socket":
			parts = append(parts, item.Service+": the container runtime's socket "+item.Value+" (full control of the server)")
		case "bind":
			parts = append(parts, item.Service+": the server folder "+item.Value)
		case "privileged":
			parts = append(parts, item.Service+": privileged mode (everything the server can do)")
		case "network-host":
			parts = append(parts, item.Service+": the server's own network")
		case "cap":
			parts = append(parts, item.Service+": the capability "+item.Value)
		case "unconfined":
			parts = append(parts, item.Service+": "+item.Value)
		case "device":
			parts = append(parts, item.Service+": the device "+item.Value)
		}
	}
	return strings.Join(parts, "; ")
}

// ForkProtectedDirs are this server's own folders that need a confirmation
// (Cosmos' configuration folder, Docker's data root), for the review.
func ForkProtectedDirs() []string { return extraSensitiveDirs() }
