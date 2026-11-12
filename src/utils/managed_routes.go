package utils

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
)

// Owner kinds a proxy route can be managed by. The set is closed on purpose:
// the URLs page renders the badge and the link to the owner from the kind, and
// an unknown kind would be a route nobody can edit anywhere.
const (
	ManagedByDeployment   = "deployment"
	ManagedByFunction     = "function"
	ManagedBySeaweedFS    = "seaweedfs"
	ManagedByManagedDB    = "manageddb"
	ManagedByRegistry     = "registry"
	ManagedByRegistrySite = "registry-site"
	ManagedByCI           = "ci"
	ManagedByShare        = "share"
)

var ManagedByKinds = []string{
	ManagedByDeployment,
	ManagedByFunction,
	ManagedBySeaweedFS,
	ManagedByManagedDB,
	ManagedByRegistry,
	ManagedByRegistrySite,
	ManagedByCI,
	ManagedByShare,
}

// IsValidManagedByKind reports whether kind is one of ManagedByKinds. The
// empty string is NOT valid here: callers that accept "unmanaged" test
// IsManaged first.
func IsValidManagedByKind(kind string) bool {
	for _, k := range ManagedByKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// IsManaged reports whether the route is owned by a feature.
func (r ProxyRouteConfig) IsManaged() bool {
	return r.ManagedByKind != ""
}

// SameOwner reports whether the route is owned by exactly this kind+name pair.
func (r ProxyRouteConfig) SameOwner(kind, name string) bool {
	return r.ManagedByKind == kind && r.ManagedByName == name
}

// ManagedBy renders the owner pair for logs and error messages ("seaweedfs:test").
func (r ProxyRouteConfig) ManagedBy() string {
	if !r.IsManaged() {
		return ""
	}
	return r.ManagedByKind + ":" + r.ManagedByName
}

// StampManagedBy returns the route with the owner pair set.
func StampManagedBy(route ProxyRouteConfig, kind, name string) ProxyRouteConfig {
	route.ManagedByKind = kind
	route.ManagedByName = name
	return route
}

// ValidateManagedBy checks the owner pair a route carries: either both empty,
// or a known kind with a non-empty name.
func ValidateManagedBy(route ProxyRouteConfig) error {
	if route.ManagedByKind == "" && route.ManagedByName == "" {
		return nil
	}
	if !IsValidManagedByKind(route.ManagedByKind) {
		return errors.New("unknown managed-by kind \"" + route.ManagedByKind + "\" on route \"" + route.Name + "\"")
	}
	if strings.TrimSpace(route.ManagedByName) == "" {
		return errors.New("managed route \"" + route.Name + "\" has no owner name")
	}
	return nil
}

// MergeManagedRoutes is the pure half of ApplyManagedRoutes: it returns the
// route list with every route owned by kind+name replaced by `desired`, and
// reports whether anything changed.
//
// Semantics, in order:
//   - an existing route owned by this pair whose name is not in desired is
//     dropped (the owner withdrew it: tags changed, record deleted...);
//   - an existing route with a desired name is replaced in place by the desired
//     copy, whatever its previous owner — the owner's materialization wins over
//     a stale or hand-made route of the same name (callers validate name
//     collisions BEFORE writing the owner record; by the time the owner applies,
//     the name is its own);
//   - desired routes not present yet are PREPENDED, like every owner did
//     before: a managed route is host-exact and must win over any broader
//     catch-all a user added below it.
//
// Every desired route is stamped with the owner pair, so an owner cannot
// accidentally materialize an unowned route through this path.
func MergeManagedRoutes(routes []ProxyRouteConfig, kind, name string, desired []ProxyRouteConfig) ([]ProxyRouteConfig, bool) {
	want := make(map[string]ProxyRouteConfig, len(desired))
	order := make([]string, 0, len(desired))
	for _, d := range desired {
		d = StampManagedBy(d, kind, name)
		if _, dup := want[d.Name]; !dup {
			order = append(order, d.Name)
		}
		want[d.Name] = d
	}

	changed := false
	kept := make([]ProxyRouteConfig, 0, len(routes)+len(desired))
	seen := map[string]bool{}
	for _, existing := range routes {
		if d, ok := want[existing.Name]; ok {
			seen[existing.Name] = true
			if !reflect.DeepEqual(existing, d) {
				changed = true
			}
			kept = append(kept, d)
			continue
		}
		if existing.SameOwner(kind, name) {
			changed = true
			continue
		}
		kept = append(kept, existing)
	}

	// Prepend in desired order: iterate backwards so the first desired route
	// ends up first.
	for i := len(order) - 1; i >= 0; i-- {
		n := order[i]
		if seen[n] {
			continue
		}
		changed = true
		kept = append([]ProxyRouteConfig{want[n]}, kept...)
	}
	return kept, changed
}

// ApplyManagedRoutes materializes an owner's desired route set into this
// node's config: ConfigLock → read the FILE (not the in-memory copy a
// concurrent edit may have moved on from) → MergeManagedRoutes →
// SetBaseMainConfig → async HTTP restart. One rewrite and one restart for the
// whole batch, and neither when nothing changed. Returns whether the config
// changed.
//
// The restart is asynchronous like every other route mutation in the codebase:
// it tears down the HTTP server the calling handler may be answering on.
func ApplyManagedRoutes(kind, name string, desired []ProxyRouteConfig) bool {
	ConfigLock.Lock()
	config := ReadConfigFromFile()
	merged, changed := MergeManagedRoutes(config.HTTPConfig.ProxyConfig.Routes, kind, name, desired)
	if !changed {
		ConfigLock.Unlock()
		return false
	}
	config.HTTPConfig.ProxyConfig.Routes = merged
	SetBaseMainConfig(config)
	ConfigLock.Unlock()

	Log("[ROUTES] materialized " + strconv.Itoa(len(desired)) + " route(s) for " + kind + ":" + name)
	TriggerEvent(
		"cosmos.routes",
		"Managed routes updated: "+kind+":"+name,
		"success",
		"",
		map[string]interface{}{"kind": kind, "name": name})

	go RestartHTTPServer()
	return true
}

// RemoveManagedRoutes withdraws every route owned by kind+name. A missing
// route is not an error — a delete retried after a partial teardown must be
// able to finish. Returns the number of routes removed.
func RemoveManagedRoutes(kind, name string) int {
	ConfigLock.Lock()
	config := ReadConfigFromFile()
	merged, changed := MergeManagedRoutes(config.HTTPConfig.ProxyConfig.Routes, kind, name, nil)
	if !changed {
		ConfigLock.Unlock()
		Debug("[ROUTES] no routes to remove for " + kind + ":" + name)
		return 0
	}
	removed := len(config.HTTPConfig.ProxyConfig.Routes) - len(merged)
	config.HTTPConfig.ProxyConfig.Routes = merged
	SetBaseMainConfig(config)
	ConfigLock.Unlock()

	Log("[ROUTES] removed " + strconv.Itoa(removed) + " route(s) of " + kind + ":" + name)
	TriggerEvent(
		"cosmos.routes",
		"Managed routes removed: "+kind+":"+name,
		"success",
		"",
		map[string]interface{}{"kind": kind, "name": name})

	go RestartHTTPServer()
	return removed
}

// ManagedRoutesOf returns the routes of the in-memory config owned by kind+name.
func ManagedRoutesOf(kind, name string) []ProxyRouteConfig {
	out := []ProxyRouteConfig{}
	for _, r := range GetMainConfig().HTTPConfig.ProxyConfig.Routes {
		if r.SameOwner(kind, name) {
			out = append(out, r)
		}
	}
	return out
}

// PreserveManagedRoutes rebuilds a wholesale route list (a PUT /api/config
// payload) so that managed routes come out exactly as the current file has
// them: an incoming route carrying a managed name is replaced by the stored
// copy, an incoming route that claims an owner the file does not know is
// dropped, and a stored managed route the payload omitted is put back at the
// front. Request order is preserved for everything else, so the settings UI
// round-trip cannot edit, rename or delete what an owner materializes.
func PreserveManagedRoutes(incoming []ProxyRouteConfig, stored []ProxyRouteConfig) []ProxyRouteConfig {
	managed := map[string]ProxyRouteConfig{}
	for _, r := range stored {
		if r.IsManaged() {
			managed[r.Name] = r
		}
	}
	seen := map[string]bool{}
	out := make([]ProxyRouteConfig, 0, len(incoming)+len(managed))
	for _, r := range incoming {
		if m, ok := managed[r.Name]; ok {
			if !seen[r.Name] {
				out = append(out, m)
				seen[r.Name] = true
			}
			continue
		}
		if r.IsManaged() {
			continue
		}
		out = append(out, r)
	}
	// Anything the payload dropped goes back in front, in stored order.
	missing := []ProxyRouteConfig{}
	for _, r := range stored {
		if r.IsManaged() && !seen[r.Name] {
			missing = append(missing, r)
		}
	}
	return append(missing, out...)
}

// RouteConflict describes why a candidate route cannot be created.
type RouteConflict struct {
	Route    string
	Existing string
	Owner    string
	Reason   string
}

func (c RouteConflict) Error() string {
	msg := "route \"" + c.Route + "\" " + c.Reason + " \"" + c.Existing + "\""
	if c.Owner != "" {
		msg += " (managed by " + c.Owner + ")"
	}
	return msg
}

// FindRouteConflicts checks candidate routes against the routes this node can
// see — its own config plus the tunnel advertisements of the cluster — and
// returns the first conflict. A candidate conflicts with an existing route of
// a different owner (or with an unmanaged route) when it reuses its name or
// its exact matcher (host + path prefix). Routes of the same owner pair never
// conflict: an update legitimately re-materializes them.
//
// Best effort by construction: proxy routes are node-local, so a route on a
// node that neither hosts nor tunnels it is invisible here. It still catches
// every case the URLs page can show the operator.
func FindRouteConflicts(candidates []ProxyRouteConfig, kind, name string) error {
	existing := append([]ProxyRouteConfig{}, GetMainConfig().HTTPConfig.ProxyConfig.Routes...)
	existing = append(existing, GetConstellationTunnelRoutes()...)

	matcher := func(r ProxyRouteConfig) string {
		if !r.UseHost && !r.UsePathPrefix {
			return ""
		}
		key := ""
		if r.UseHost {
			key += strings.ToLower(strings.TrimSpace(r.Host))
		}
		key += "|"
		if r.UsePathPrefix {
			key += r.PathPrefix
		}
		return key
	}

	for _, c := range candidates {
		ownerKind, ownerName := kind, name
		if c.IsManaged() {
			ownerKind, ownerName = c.ManagedByKind, c.ManagedByName
		}
		cKey := matcher(c)
		for _, e := range existing {
			if e.SameOwner(ownerKind, ownerName) {
				continue
			}
			if e.Name == c.Name {
				return RouteConflict{Route: c.Name, Existing: e.Name, Owner: e.ManagedBy(), Reason: "reuses the name of the existing route"}
			}
			if cKey != "" && cKey == matcher(e) {
				return RouteConflict{Route: c.Name, Existing: e.Name, Owner: e.ManagedBy(), Reason: "matches the same host and path as the existing route"}
			}
		}
	}
	return nil
}
