package docker

// Fork addition (plan 47, I2c, second review S1, S2, M4): what a new bind or
// a volume that binds a host path really reaches, on the edit and on the
// creation routes.
//
//   - A bind's source, and the device of a volume whose local driver binds a
//     host path, are followed through their links as Docker follows them; a
//     path that passes a link is handed on as the path it leads to when that
//     is on a data place or a sensitive folder (whose tick then names it), and
//     refused in words otherwise (resolveMounts).
//   - A named volume used without options is looked up in Docker: one that
//     was made with bind options is the host path it binds (volumeDeviceOf).
//   - A new bind, or a new volume binding a host path, that lies strictly
//     inside a folder some app can write (another container's writable bind,
//     another service of the same request, or another bind of its own) needs
//     the tick, kind "nested": that app could replace the folder with a link
//     (to /, say) before the next start, and Docker would follow it. A tick,
//     not a refusal: a media server reading a library a downloader writes is
//     the usual home server, and the tick says in words which app could
//     redirect the folder. Binds the container had are not asked again.
//   - A path that reaches another app's folder through a link needs the tick
//     too, kind "linkinto", naming that app.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/docker/docker/api/types"
	containerType "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
)

// ---- named volumes that bind a host path ----

// lookupVolume answers a named volume's driver and options; replaced by the tests.
var lookupVolume = func(name string) (driver string, options map[string]string, found bool) {
	if DockerClient == nil {
		return "", nil, false
	}
	v, err := DockerClient.VolumeInspect(DockerContext, name)
	if err != nil {
		return "", nil, false
	}
	return v.Driver, v.Options, true
}

type volumeFact struct {
	device string
	at     time.Time
}

var (
	volumeFactsMu sync.Mutex
	volumeFacts   = map[string]volumeFact{}
)

// existingVolumeDevice is the host path an existing named volume binds, ""
// for none; asked of Docker at most every 5 seconds per volume, at most 256 kept.
func existingVolumeDevice(name string) string {
	if name == "" || strings.HasPrefix(name, "/") {
		return ""
	}
	volumeFactsMu.Lock()
	if f, ok := volumeFacts[name]; ok && time.Since(f.at) < 5*time.Second {
		volumeFactsMu.Unlock()
		return f.device
	}
	volumeFactsMu.Unlock()
	device := ""
	if driver, options, found := lookupVolume(name); found {
		device = volumeBindDevice(mount.Mount{Type: mount.TypeVolume, Source: name,
			VolumeOptions: &mount.VolumeOptions{DriverConfig: &mount.Driver{Name: driver, Options: options}}})
	}
	volumeFactsMu.Lock()
	if len(volumeFacts) >= 256 {
		volumeFacts = map[string]volumeFact{}
	}
	volumeFacts[name] = volumeFact{device: device, at: time.Now()}
	volumeFactsMu.Unlock()
	return device
}

// volumeDeviceOf is the host path a mount of a volume binds: from its own
// options, or from the existing volume of that name; "" for none.
func volumeDeviceOf(m mount.Mount) string {
	if m.Type != mount.TypeVolume && m.Type != "" {
		return ""
	}
	if m.Type == "" && strings.HasPrefix(m.Source, "/") {
		return ""
	}
	if device := volumeBindDevice(m); device != "" {
		return device
	}
	if m.VolumeOptions != nil && m.VolumeOptions.DriverConfig != nil && len(m.VolumeOptions.DriverConfig.Options) > 0 {
		return ""
	}
	return existingVolumeDevice(m.Source)
}

// realPath is where a host path leads (links followed), the path itself when
// it cannot be followed or is sensitive by its own name already (then it is
// ticked as named: /var/run/docker.sock stays itself, not /run/docker.sock).
func realPath(p string) string {
	clean := cleanAbsolute(p)
	if clean == "" {
		return p
	}
	if SensitiveBind(clean, extraSensitiveDirs()) != "" {
		return clean
	}
	if resolved, _, err := resolveSource(clean); err == nil {
		return resolved
	}
	return clean
}

// ---- following links in what a request mounts ----

func isBindLike(m mount.Mount) bool {
	return m.Type == mount.TypeBind || (m.Type == "" && strings.HasPrefix(m.Source, "/"))
}

// linkedMount is a mount whose path was reached through a link.
type linkedMount struct {
	literal  string
	resolved string
}

// resolveMounts follows the links of each new mount's host path (a bind's
// source, a disguised volume's device) and rewrites it to where it leads, or
// refuses it in words. had: keys (mountKey) of mounts that are not new.
func resolveMounts(mounts []mount.Mount, had map[string]bool) ([]linkedMount, error) {
	linked := []linkedMount{}
	places := EditDataPlaces()
	allowed := func(resolved string) bool {
		return inDataPlace(resolved, places) || SensitiveBind(resolved, extraSensitiveDirs()) != ""
	}
	for i := range mounts {
		m := mounts[i]
		if had[mountKey(m)] {
			continue
		}
		var literal string
		switch {
		case isBindLike(m):
			literal = cleanAbsolute(m.Source)
		case volumeBindDevice(m) != "":
			literal = cleanAbsolute(volumeBindDevice(m))
		default:
			continue
		}
		if literal == "" {
			continue // refused by the shape checks, in words
		}
		if SensitiveBind(literal, extraSensitiveDirs()) != "" {
			continue // ticked by its own name already (a link there leads to what it names: /var/run is /run)
		}
		resolved, viaLink, err := resolveSource(literal)
		if err != nil {
			return linked, fmt.Errorf("the server folder %s: its links cannot be followed (%s)", literal, err.Error())
		}
		if !viaLink {
			continue
		}
		if !allowed(resolved) {
			return linked, fmt.Errorf("the server folder %s is a link to %s, which is not on a data disk: give %s itself if that is the folder you mean", literal, resolved, resolved)
		}
		if isBindLike(m) {
			mounts[i].Source = resolved
		} else {
			mounts[i].VolumeOptions.DriverConfig.Options["device"] = resolved
		}
		linked = append(linked, linkedMount{literal: literal, resolved: resolved})
	}
	return linked, nil
}

// ---- folders apps can write ----

// writer is a host folder an app mounts.
type writer struct {
	app      string
	source   string // real path
	writable bool
}

// listServerMounts lists what the containers of this server mount from the
// host (binds, and volumes binding a host path); replaced by the tests.
var listServerMounts = func() []writer {
	out := []writer{}
	if DockerClient == nil {
		return out
	}
	containers, err := DockerClient.ContainerList(DockerContext, containerType.ListOptions{All: true})
	if err != nil {
		return out
	}
	for i, c := range containers {
		if i >= 500 {
			break
		}
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		for j, m := range c.Mounts {
			if j >= 64 {
				break
			}
			switch {
			case m.Type == mount.TypeBind:
				out = append(out, writer{app: name, source: m.Source, writable: m.RW})
			case m.Type == mount.TypeVolume:
				if device := existingVolumeDevice(m.Name); device != "" {
					out = append(out, writer{app: name, source: device, writable: m.RW})
				}
			}
		}
	}
	return out
}

var (
	serverMountsMu sync.Mutex
	serverMountsAt time.Time
	serverMounts   []writer
)

// serverWriters is listServerMounts with real paths, kept 5 seconds.
func serverWriters() []writer {
	serverMountsMu.Lock()
	defer serverMountsMu.Unlock()
	if serverMounts != nil && time.Since(serverMountsAt) < 5*time.Second {
		return serverMounts
	}
	list := listServerMounts()
	for i := range list {
		list[i].source = realPath(list[i].source)
	}
	serverMounts, serverMountsAt = list, time.Now()
	return serverMounts
}

func strictlyWithin(p, dir string) bool {
	return p != dir && within(p, dir)
}

// accessItems are the ticks a set of new host paths needs for where they
// lie: inside a folder an app can write (nested), or reached through a link
// in another app's folder (linkinto). service names the app that gets them.
func accessItems(service string, fresh []string, linked []linkedMount, writers []writer) []SensitiveItem {
	items := []SensitiveItem{}
	seen := map[string]bool{}
	add := func(item SensitiveItem) {
		if !seen[item.Key()] {
			seen[item.Key()] = true
			items = append(items, item)
		}
	}
	sorted := append([]writer{}, writers...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].app+sorted[i].source < sorted[j].app+sorted[j].source })
	for _, p := range fresh {
		for _, w := range sorted {
			if w.writable && strictlyWithin(p, w.source) {
				add(SensitiveItem{Service: service, Kind: "nested", Value: p, Detail: w.app + " (" + w.source + ")"})
				break
			}
		}
	}
	for _, l := range linked {
		for _, w := range sorted {
			if w.app != service && within(l.resolved, w.source) {
				add(SensitiveItem{Service: service, Kind: "linkinto", Value: l.resolved, Detail: w.app + " (" + w.source + ")"})
				break
			}
		}
	}
	return items
}

// newHostPaths are the real host paths of a list's new mounts (binds and volumes binding a path).
func newHostPaths(mounts []mount.Mount, had map[string]bool) []string {
	out := []string{}
	for _, m := range mounts {
		if had[mountKey(m)] {
			continue
		}
		p := ""
		if isBindLike(m) {
			p = m.Source
		} else {
			p = volumeDeviceOf(m)
		}
		if clean := cleanAbsolute(p); clean != "" {
			out = append(out, realPath(clean))
		}
	}
	return out
}

// ---- the edit ----

// editAccess resolves the new mounts of an edit (rewriting their paths) and
// lists the ticks they need for where they lie, or refuses them in words.
func editAccess(before types.ContainerJSON, after *types.ContainerJSON) ([]SensitiveItem, error) {
	had := map[string]bool{}
	for _, m := range mountsOf(before) {
		had[mountKey(m)] = true
	}
	linked, err := resolveMounts(after.HostConfig.Mounts, had)
	if err != nil {
		return nil, err
	}
	service := strings.TrimPrefix(before.Name, "/")
	writers := append([]writer{}, serverWriters()...)
	// its own writable folders, those it had and those it gets
	for _, list := range [][]mount.Mount{mountsOf(before), after.HostConfig.Mounts} {
		for _, m := range list {
			p := ""
			if isBindLike(m) {
				p = m.Source
			} else {
				p = volumeDeviceOf(m)
			}
			if clean := cleanAbsolute(p); clean != "" && !m.ReadOnly {
				writers = append(writers, writer{app: service, source: realPath(clean), writable: true})
			}
		}
	}
	return accessItems(service, newHostPaths(after.HostConfig.Mounts, had), linked, writers), nil
}

// ---- the creation ----

// serviceAccess resolves the new mounts of a creation request (rewriting
// their paths in the request) and lists the ticks they need; with replace,
// what the container of that name already mounts is not new.
func serviceAccess(request DockerServiceCreateRequest, replacing bool) ([]SensitiveItem, error) {
	keys := make([]string, 0, len(request.Services))
	for key := range request.Services {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	type resolvedService struct {
		key    string
		had    map[string]bool
		linked []linkedMount
	}
	resolved := []resolvedService{}
	for _, key := range keys {
		service := request.Services[key]
		had := map[string]bool{}
		if replacing {
			name := service.Name
			if name == "" {
				name = key
			}
			for _, k := range existingMountKeys(name) {
				had[k] = true
			}
		}
		linked, err := resolveMounts(service.Volumes, had)
		if err != nil {
			return nil, fmt.Errorf("service %s, %s", key, err.Error())
		}
		resolved = append(resolved, resolvedService{key: key, had: had, linked: linked})
	}
	server := serverWriters()
	items := []SensitiveItem{}
	for _, r := range resolved {
		writers := append([]writer{}, server...)
		// the other services of the request, and the other mounts of this one
		for _, other := range keys {
			for _, m := range request.Services[other].Volumes {
				p := ""
				if isBindLike(m) {
					p = m.Source
				} else {
					p = volumeDeviceOf(m)
				}
				if clean := cleanAbsolute(p); clean != "" && !m.ReadOnly {
					writers = append(writers, writer{app: other, source: realPath(clean), writable: true})
				}
			}
		}
		items = append(items, accessItems(r.key, newHostPaths(request.Services[r.key].Volumes, r.had), r.linked, writers)...)
	}
	return items, nil
}

// existingMountKeys are the mount keys of the container of that name; replaced by the tests.
var existingMountKeys = func(name string) []string {
	out := []string{}
	if DockerClient == nil {
		return out
	}
	c, err := DockerClient.ContainerInspect(DockerContext, name)
	if err != nil {
		return out
	}
	for _, m := range mountsOf(c) {
		out = append(out, mountKey(m))
	}
	return out
}

// cleanMountSource is a bind's source in its clean form (a trailing slash is not another folder).
func cleanMountSource(m mount.Mount) string {
	if isBindLike(m) {
		if clean := cleanAbsolute(m.Source); clean != "" {
			return clean
		}
	}
	return m.Source
}

// ---- what the creation review asks the server ----

// SensitiveRoute answers POST /cosmos/api/install/sensitive {"definition": …, "replace": bool}:
// every tick the creation route will ask for this definition, the ones only
// the server can see included (links followed, existing volumes looked up,
// folders other apps can write), or the refusal in words. Nothing is made.
// PERM_RESOURCES, like the creation.
func SensitiveRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_RESOURCES) != nil {
		return
	}
	if req.Method != http.MethodPost {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	var body struct {
		Definition DockerServiceCreateRequest `json:"definition"`
		Replace    bool                       `json:"replace"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&body); err != nil {
		utils.HTTPError(w, "Invalid request", http.StatusBadRequest, "IS010")
		return
	}
	if len(body.Definition.Services) > 32 {
		utils.HTTPError(w, "At most 32 services", http.StatusBadRequest, "IS011")
		return
	}
	answer := map[string]interface{}{"sensitive": []SensitiveItem{}}
	where, err := serviceAccess(body.Definition, body.Replace)
	if err != nil {
		answer["refused"] = err.Error()
	} else {
		query := url.Values{}
		if body.Replace {
			query.Set("replace", "1")
		}
		answer["sensitive"] = unconfirmedSensitive(body.Definition, query, where...)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": answer})
}
