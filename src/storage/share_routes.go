package storage

import (
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

// shareProxyScheme is the scheme of the internal target the proxy dials for a
// served share: the HTTP protocols go through the HTTP proxy, the socket
// protocols through the TCP proxy. Mirrors ServeConfig[].Proxy in the client,
// which used to build the route itself.
func shareProxyScheme(protocol string) string {
	switch strings.ToLower(protocol) {
	case "s3", "webdav", "http":
		return "http"
	default:
		return "tcp"
	}
}

// shareIsSamba reports whether a share is served by samba rather than by an
// rclone serve instance. Samba shares carry no proxy route: samba owns its
// ports itself.
func shareIsSamba(share utils.LocationRemoteStorageConfig) bool {
	p := strings.ToLower(share.Protocol)
	return p == "smb" || p == "samba"
}

// shareFirstPort is where internal serve ports start; the client used the
// same base before the allocation moved server-side.
const shareFirstPort = 12000

// ShareRouteName is the proxy route name of a share.
func ShareRouteName(name string) string {
	return "netshare_" + name
}

// BuildShareRoute completes the route stored on a share. The client only
// supplies the user-facing half (host, SmartShield, whatever else the URL form
// exposes); the identity and the internal target are derived here, and a
// target already allocated is kept so an edit never moves a running server
// to another port. `busy` lists the internal ports other shares hold.
func BuildShareRoute(share utils.LocationRemoteStorageConfig, busy map[int]bool) utils.ProxyRouteConfig {
	route := share.Route
	route.Name = ShareRouteName(share.Name)
	route.Mode = utils.ProxyMode("PROXY")
	route.UseHost = true
	if route.Host == "" {
		route.Host = share.Source
	}
	if route.Description == "" {
		route.Description = "Network share " + share.Name + " (" + share.Protocol + ")"
	}
	if shareIsSamba(share) {
		route.Disabled = true
		return utils.StampManagedBy(route, utils.ManagedByShare, share.Name)
	}

	scheme := shareProxyScheme(share.Protocol)
	port := shareRoutePort(route.Target)
	if port == 0 {
		port = shareFirstPort
		for busy[port] {
			port++
		}
	}
	route.Target = scheme + "://127.0.0.1:" + strconv.Itoa(port)
	return utils.StampManagedBy(route, utils.ManagedByShare, share.Name)
}

// shareRoutePort extracts the internal port of a share target, 0 when unset.
func shareRoutePort(target string) int {
	if target == "" {
		return 0
	}
	u, err := url.Parse(target)
	if err != nil {
		return 0
	}
	port, _ := strconv.Atoi(u.Port())
	return port
}

// ReconcileShareRoutes derives every share's route server-side, persists the
// completed copy on the share (the source of truth remountAll reads the serve
// port from) and materializes it into ProxyConfig.Routes under the share
// owner, withdrawing the routes of shares that no longer exist. One config
// rewrite and one HTTP restart for the whole pass, and neither when nothing
// changed.
//
// Runs after every wholesale config write (the share dialog saves through
// PUT /api/config) and at remount, so the materialized copies can never drift
// from the shares — the URLs page shows them, read-only, like any other
// managed route.
func ReconcileShareRoutes() {
	utils.ConfigLock.Lock()
	config := utils.ReadConfigFromFile()
	shares := config.RemoteStorage.Shares

	busy := map[int]bool{}
	for _, share := range shares {
		if p := shareRoutePort(share.Route.Target); p != 0 {
			busy[p] = true
		}
	}

	changed := false
	routes := config.HTTPConfig.ProxyConfig.Routes
	live := map[string]bool{}
	for i := range shares {
		route := BuildShareRoute(shares[i], busy)
		busy[shareRoutePort(route.Target)] = true
		if !sameRoute(shares[i].Route, route) {
			shares[i].Route = route
			changed = true
		}
		live[shares[i].Name] = true

		desired := []utils.ProxyRouteConfig{}
		if !shareIsSamba(shares[i]) {
			desired = append(desired, route)
		}
		var c bool
		routes, c = utils.MergeManagedRoutes(routes, utils.ManagedByShare, shares[i].Name, desired)
		changed = changed || c
	}

	// Shares removed from the config: withdraw whatever they materialized.
	for _, r := range routes {
		if r.ManagedByKind == utils.ManagedByShare && !live[r.ManagedByName] {
			var c bool
			routes, c = utils.MergeManagedRoutes(routes, utils.ManagedByShare, r.ManagedByName, nil)
			changed = changed || c
		}
	}

	if !changed {
		utils.ConfigLock.Unlock()
		return
	}
	config.RemoteStorage.Shares = shares
	config.HTTPConfig.ProxyConfig.Routes = routes
	utils.SetBaseMainConfig(config)
	utils.ConfigLock.Unlock()

	utils.Log("[RemoteStorage] share routes reconciled")
	utils.TriggerEvent(
		"cosmos.routes",
		"Network share routes updated",
		"success",
		"",
		map[string]interface{}{})

	go utils.RestartHTTPServer()
}

// sameRoute compares two routes structurally.
func sameRoute(a, b utils.ProxyRouteConfig) bool {
	return reflect.DeepEqual(a, b)
}
