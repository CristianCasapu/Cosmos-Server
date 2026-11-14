package proxy

// Fork addition: names for use at home (utils/fork_home_names.go) answer the
// local network only.
//
// A home name resolves to the server's private address, so a visitor from
// the internet never gets there by name. But anyone can send it as the Host
// of a request to the server's public port, and Cloudflare's network can be
// made to: without this guard a home name would be a second way in, beside
// the app's real address and without its shield. So a request whose host is
// a home name (an app's, the panel's, or any name under an active home
// suffix) from a visitor outside the local network is refused and recorded,
// before any route sees it. "Local" is exactly what it is for .local names
// (isLocalVisitor, fork_local.go): private, loopback, link-local, the
// carrier-NAT range Tailscale uses, and the server's own networks. The
// visitor is the address the other restrictions use; a home name is never
// served through Cloudflare, so Cloudflare's headers are never believed for
// it and a request through Cloudflare comes from Cloudflare's own address.

import (
	"net"
	"net/http"

	"github.com/azukaar/cosmos-server/src/utils"
)

func init() {
	utils.ForkLANAddress = lanAddressForHomeNames
}

// lanAddressForHomeNames is the private IPv4 of the first interface the
// .local names are published on: the default route's, unless the admin
// named others in Configuration > HTTP.
func lanAddressForHomeNames() string {
	chosen, _ := chooseLANInterfaces(utils.GetMainConfig().HTTPConfig.MDNSInterfaces)
	for _, iface := range chosen {
		if ip := net.ParseIP(iface.Address); ip != nil && ip.To4() != nil && ip.IsPrivate() {
			return ip.To4().String()
		}
	}
	return ""
}

// HomeNameGuard refuses a name for use at home to visitors from outside the
// local network. One router.Use in front of the panel and every URL.
func HomeNameGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if utils.IsHomeHost(req.Host) {
			visitor, _ := req.Context().Value("ClientID").(string)
			if visitor == "" {
				visitor = utils.GetClientIP(req)
			}
			if !isLocalVisitor(visitor, serverNetworks()) {
				host := utils.CloudflareHostname(req.Host)
				if len(host) > 253 || !utils.IsValidHostnameSyntax(host) {
					host = "(a name that is not valid)"
				}
				utils.RecordSecurity(req, utils.SecAccessLocalDenied, host, "reason", "home_name") // security record
				utils.Warn("Home name " + host + " was requested from outside the local network (" + visitor + "): refused")
				http.Error(w, "This address is available on the local network only.", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, req)
	})
}

// buildHomeRoutes registers the routes of the apps' home names. Called by
// BuildFromConfig before the configured routes: a home name is host-exact
// and must win over a URL that answers any host.
func buildHomeRoutes(register func(route utils.ProxyRouteConfig)) {
	for _, route := range utils.HomeRoutes() {
		register(route)
	}
}
