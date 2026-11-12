package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

// DeploymentSpecHashLabel holds a hash of the compose spec WITHOUT its proxy
// routes (and without the labels stamped here). A node compares the label on
// its running containers with the hash of an incoming spec to tell a
// routes-only version bump — which it applies without touching containers —
// from a real change.
const DeploymentSpecHashLabel = "cosmos-deployment-spec-hash"

// isDeploymentLabel reports whether a label key is scheduler bookkeeping —
// identity, version, spec hash, route owners — all of it stamped AFTER the
// hash is taken, and therefore invisible to it. Every other label, including
// cosmos-deployment-keep-volumes and cosmos-lazy, is part of what the
// container IS: docker labels are immutable, so flipping one can only be
// applied by recreating the container, which means it must move the hash.
func isDeploymentLabel(key string) bool {
	switch key {
	case DeploymentLabel, DeploymentVersionLabel, DeploymentSpecHashLabel, DeploymentRoutesLabel:
		return true
	}
	return false
}

// ComposeSpecHash hashes everything about a compose that requires a container
// (re)create: services minus their routes, plus networks and volumes, with the
// scheduler's own labels stripped. Map keys are sorted by encoding/json, so
// the hash is stable across marshal orders.
func ComposeSpecHash(spec DockerServiceCreateRequest) string {
	clone := DockerServiceCreateRequest{
		Services: map[string]ContainerCreateRequestContainer{},
		Networks: map[string]ContainerCreateRequestNetwork{},
		Volumes:  map[string]ContainerCreateRequestVolume{},
	}
	stripLabels := func(labels map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range labels {
			if !isDeploymentLabel(k) {
				out[k] = v
			}
		}
		return out
	}
	for name, svc := range spec.Services {
		svc.Routes = nil
		svc.Labels = stripLabels(svc.Labels)
		clone.Services[name] = svc
	}
	for name, net := range spec.Networks {
		net.Labels = stripLabels(net.Labels)
		clone.Networks[name] = net
	}
	for name, vol := range spec.Volumes {
		vol.Labels = stripLabels(vol.Labels)
		clone.Volumes[name] = vol
	}
	raw, err := json.Marshal(clone)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// ComposeRoutes lists every route the compose carries, service by service.
func ComposeRoutes(spec DockerServiceCreateRequest) []utils.ProxyRouteConfig {
	out := []utils.ProxyRouteConfig{}
	for _, svc := range spec.Services {
		out = append(out, svc.Routes...)
	}
	return out
}

// MergeComposeRoutes folds the compose's routes into a route list. Routes
// stamped with an owner go through the owner merge (prune + upsert per owner
// pair); unstamped routes — a compose applied from the ServApps UI — keep the
// historical upsert-by-name so a user route imported with a stack stays a
// plain user route. Returns the merged list and whether it changed.
func MergeComposeRoutes(routes []utils.ProxyRouteConfig, spec DockerServiceCreateRequest, onLog func(string)) ([]utils.ProxyRouteConfig, bool) {
	changed := false
	byOwner := map[string][]utils.ProxyRouteConfig{}
	owners := []string{}
	for _, route := range ComposeRoutes(spec) {
		if !route.IsManaged() {
			idx := -1
			for i, existing := range routes {
				if existing.Name == route.Name {
					idx = i
					break
				}
			}
			if idx == -1 {
				changed = true
				routes = append([]utils.ProxyRouteConfig{route}, routes...)
				continue
			}
			if !sameRoute(routes[idx], route) {
				changed = true
			}
			routes[idx] = route
			utils.Warn("CreateService: Route " + route.Name + " already exist, overwriting.")
			if onLog != nil {
				onLog(utils.DoWarn("%s", "Route "+route.Name+" already exist, overwriting.\n"))
			}
			continue
		}
		key := route.ManagedBy()
		if _, seen := byOwner[key]; !seen {
			owners = append(owners, key)
		}
		byOwner[key] = append(byOwner[key], route)
	}
	for _, key := range owners {
		group := byOwner[key]
		var c bool
		routes, c = utils.MergeManagedRoutes(routes, group[0].ManagedByKind, group[0].ManagedByName, group)
		changed = changed || c
	}
	return routes, changed
}

func sameRoute(a, b utils.ProxyRouteConfig) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// ApplyComposeRoutes materializes a compose's routes into this node's config
// without touching any container: the routes-only apply path. Same lock and
// restart discipline as utils.ApplyManagedRoutes.
func ApplyComposeRoutes(spec DockerServiceCreateRequest) bool {
	utils.ConfigLock.Lock()
	config := utils.ReadConfigFromFile()
	merged, changed := MergeComposeRoutes(config.HTTPConfig.ProxyConfig.Routes, spec, nil)
	if !changed {
		utils.ConfigLock.Unlock()
		return false
	}
	config.HTTPConfig.ProxyConfig.Routes = merged
	utils.SetBaseMainConfig(config)
	utils.ConfigLock.Unlock()

	utils.TriggerEvent(
		"cosmos.routes",
		"Deployment routes updated",
		"success",
		"",
		map[string]interface{}{})

	go utils.RestartHTTPServer()
	return true
}

// ComposeRoutesAtVersion reports whether every route the compose carries is
// present in this node's config, owned, and rendered from at least the given
// version — i.e. whether a routes-only apply of that version already landed.
// A compose without routes can never be "at version" this way; the container
// label is the only witness then.
func ComposeRoutesAtVersion(spec DockerServiceCreateRequest, version int) bool {
	wanted := ComposeRoutes(spec)
	if len(wanted) == 0 {
		return false
	}
	have := map[string]utils.ProxyRouteConfig{}
	for _, r := range utils.GetMainConfig().HTTPConfig.ProxyConfig.Routes {
		have[r.Name] = r
	}
	for _, w := range wanted {
		h, ok := have[w.Name]
		if !ok || !h.IsManaged() || h.ManagedByVersion < version {
			return false
		}
	}
	return true
}

// EffectiveDeploymentVersion lifts a container-label version to the version
// a routes-only apply brought this node to. Labels are immutable, so the
// witness is the ManagedByVersion stamped on the routes owned by the pairs
// recorded in the deployment's routes label: when every owner has routes in
// config and the lowest version among them is newer than the label, the
// routes-only apply landed at that version. Read from config, so it survives
// a restart. `owners` empty (no routes label) means the label is the truth.
func EffectiveDeploymentVersion(routes []utils.ProxyRouteConfig, labelVersion int, owners []string) int {
	if len(owners) == 0 {
		return labelVersion
	}
	lowest := -1
	for _, owner := range owners {
		kind, name, ok := strings.Cut(owner, ":")
		if !ok {
			return labelVersion
		}
		found := false
		for _, r := range routes {
			if !r.SameOwner(kind, name) {
				continue
			}
			found = true
			if lowest == -1 || r.ManagedByVersion < lowest {
				lowest = r.ManagedByVersion
			}
		}
		if !found {
			return labelVersion
		}
	}
	if lowest > labelVersion {
		return lowest
	}
	return labelVersion
}

// DeploymentRoutesLabelEntries renders the routes label for a compose: one
// "kind:name" owner pair per distinct owner. Teardown withdraws by owner, so
// the label survives a rename of the route inside the compose.
func DeploymentRoutesLabelEntries(spec DockerServiceCreateRequest) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, r := range ComposeRoutes(spec) {
		if !r.IsManaged() {
			continue
		}
		key := r.ManagedBy()
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	return out
}

// RemoveDeploymentRoutes withdraws the routes recorded on a deployment's
// containers. Entries are owner pairs ("kind:name"); anything else is
// ignored. Missing routes are not errors — a retried remove must be able to
// finish.
func RemoveDeploymentRoutes(entries []string) int {
	if len(entries) == 0 {
		return 0
	}
	utils.ConfigLock.Lock()
	config := utils.ReadConfigFromFile()
	routes := config.HTTPConfig.ProxyConfig.Routes
	before := len(routes)
	for _, entry := range entries {
		kind, name, isOwner := strings.Cut(entry, ":")
		if !isOwner || !utils.IsValidManagedByKind(kind) {
			utils.Warn("[SCHED-NODE] ignoring malformed routes label entry " + entry)
			continue
		}
		routes, _ = utils.MergeManagedRoutes(routes, kind, name, nil)
	}
	removed := before - len(routes)
	if removed == 0 {
		utils.ConfigLock.Unlock()
		utils.Debug("[SCHED-NODE] no deployment routes to remove (" + strings.Join(entries, ",") + ")")
		return 0
	}
	config.HTTPConfig.ProxyConfig.Routes = routes
	utils.SetBaseMainConfig(config)
	utils.ConfigLock.Unlock()

	utils.Log("[SCHED-NODE] removed " + strconv.Itoa(removed) + " deployment route(s): " + strings.Join(entries, ","))
	utils.TriggerEvent(
		"cosmos.routes",
		"Deployment routes removed",
		"success",
		"",
		map[string]interface{}{})

	go utils.RestartHTTPServer()
	return removed
}
