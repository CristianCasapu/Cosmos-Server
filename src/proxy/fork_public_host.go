package proxy

// Fork addition (plan 44): what an app is told about the address it is
// reached at.
//
// Upstream passed the visitor's Host on as it came and told the app
// X-Forwarded-Host without its port and no X-Forwarded-Port at all. On a
// server whose HTTPS port is not 443 (2053, for Cloudflare's sake) an app that
// builds links or checks origins from those headers got them wrong: links to
// port 443, an Origin "https://app.example.org:2053" compared with
// "app.example.org". Now, for a URL with a name of its own:
//
//   - Host and X-Forwarded-Host are the URL's own name with the port the
//     visitor used, X-Forwarded-Port that port (443 or 80 too), as Traefik
//     and Caddy pass them. The name is the URL's (the visitor's Host only
//     matched it); the port is the visitor's only when it is one this server
//     answers on or is reached at (the port of the connection, Cosmos' HTTP
//     and HTTPS ports, the router's port for Cosmos), or none at all (the
//     scheme's own); any other port becomes the port of the connection. So
//     neither can carry a value the visitor chose beyond the URL they reached.
//   - With ForkAppOwnHost ("tell the app its own address"), Host is the
//     address Cosmos connects to (the app's own, as its documentation for
//     reverse proxies asks of nginx: `proxy_set_header Host $proxy_host`)
//     and the public address goes in X-Forwarded-Host/-Port/-Proto. The Host
//     comes from the URL's target, never from the request, so a visitor
//     cannot reach another virtual host of the same backend with it.
//
// With ForkNoVisitorAddress the app is not told the visitor's address at all
// (no X-Forwarded-For, X-Real-IP or Cloudflare's headers): for an app that
// refuses a proxy it does not trust until it is configured (Home Assistant
// answers 400 to any X-Forwarded-For from an untrusted peer), so that its own
// settings page can be reached to trust Cosmos. The app then sees every
// visitor as Cosmos.
//
// Upstream's own options keep their meaning: OverwriteHostHeader (also used
// by the tunnels) and SpoofHostname, which runs after this and removes
// X-Forwarded-Host and -Port as before. Port routes (":8080") answer every
// name and are left as upstream has them.

import (
	"context"
	"net"
	"net/url"
	"net/http"
	"strconv"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

// PublicAddress is what an app is told about where it is reached.
type PublicAddress struct {
	Name string // the URL's name, lower case, without port
	Port string // the port visitors use, always set
	// Host is Name with the port, the port left out when it is the scheme's own
	Host string
}

func defaultPort(scheme string) string {
	if scheme == "https" {
		return "443"
	}
	return "80"
}

// joinPublic writes a name with its port, without the scheme's own port.
func joinPublic(name, port, scheme string) string {
	if port == "" || port == defaultPort(scheme) {
		if strings.Contains(name, ":") {
			return "[" + name + "]"
		}
		return name
	}
	return net.JoinHostPort(name, port)
}

// splitHost cuts "name:port", "[v6]:port", "name" and "[v6]".
func splitHost(host string) (string, string) {
	if name, port, err := net.SplitHostPort(host); err == nil {
		return name, port
	}
	return strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"), ""
}

// knownPorts are the ports a visitor may truthfully name in Host: this
// server's own and the ones it is reached at through the router.
func knownPorts(localPort string) map[string]bool {
	known := map[string]bool{"80": true, "443": true}
	if localPort != "" {
		known[localPort] = true
	}
	config := utils.GetMainConfig().HTTPConfig
	for _, p := range []string{config.HTTPPort, config.HTTPSPort} {
		if p = strings.TrimSpace(p); p != "" {
			known[p] = true
		}
	}
	if p := utils.PublicHTTPSPort(config); p > 0 {
		known[strconv.Itoa(p)] = true
	}
	if config.UPnP.Enabled && config.UPnP.ForwardHTTP {
		if p := strings.TrimSpace(config.UPnP.ExternalHTTPPort); p != "" {
			known[p] = true
		}
	}
	return known
}

// visitorPort is the port the app is told: the one in the visitor's Host when
// this server answers on it, the scheme's own when Host has none, else the
// port the connection came in on.
func visitorPort(hostPort, localPort, scheme string) string {
	if hostPort == "" {
		return defaultPort(scheme)
	}
	if n, err := strconv.Atoi(hostPort); err == nil && n > 0 && n < 65536 && knownPorts(localPort)[strconv.Itoa(n)] {
		return strconv.Itoa(n)
	}
	if localPort != "" {
		return localPort
	}
	return defaultPort(scheme)
}

// localPortOf is the port of this server the request came in on.
func localPortOf(req *http.Request) string {
	if port, ok := HandoffPort(req); ok {
		return port
	}
	if addr, ok := req.Context().Value(http.LocalAddrContextKey).(net.Addr); ok && addr != nil {
		if _, port, err := net.SplitHostPort(addr.String()); err == nil {
			return port
		}
	}
	return ""
}

// publicAddressOf works out the address an app is told about. requestHost is
// the visitor's Host. ok is false for the URLs left as upstream has them.
func publicAddressOf(route utils.ProxyRouteConfig, requestHost, localPort, scheme string) (PublicAddress, bool) {
	if route.OverwriteHostHeader != "" {
		return PublicAddress{}, false
	}
	_, askedPort := splitHost(requestHost)
	var name, port string
	if route.UseHost {
		if strings.ContainsAny(route.Host, ", ") {
			return PublicAddress{}, false
		}
		routeName, routePort := splitHost(strings.TrimSpace(route.Host))
		if routeName == "" || routeName == "0.0.0.0" {
			return PublicAddress{}, false // a port route answers any name
		}
		name, port = routeName, routePort
	} else {
		// a URL without a name of its own answers the server's names: the one
		// the visitor used when it is one of them, else the server's own
		asked, _ := splitHost(requestHost)
		name, _ = splitHost(utils.GetMainConfig().HTTPConfig.Hostname)
		for _, known := range utils.GetAllHostnames(true) {
			if asked != "" && strings.EqualFold(known, asked) {
				name = known
				break
			}
		}
		if name == "" || name == "0.0.0.0" {
			return PublicAddress{}, false
		}
	}
	if port == "" {
		port = visitorPort(askedPort, localPort, scheme)
	}
	name = strings.ToLower(name)
	return PublicAddress{Name: name, Port: port, Host: joinPublic(name, port, scheme)}, true
}

// ownHost is the Host an app is told with ForkAppOwnHost: the address Cosmos
// connects to, without the port when it is the target scheme's own.
func ownHost(dialled string, targetScheme string) string {
	name, port := splitHost(dialled)
	if name == "" {
		return dialled
	}
	return joinPublic(name, port, targetScheme)
}

// AppHeaders is what the app receives about its address.
type AppHeaders struct {
	Host           string
	ForwardedHost  string
	ForwardedPort  string
	ForwardedProto string
	// NoVisitorAddress: no X-Forwarded-For nor X-Real-IP
	NoVisitorAddress bool
}

// AppHeadersFor is what a route tells its app, for the check of
// src/behind as for the proxy: public is where the visitor arrived, dialled
// the address Cosmos connects to.
func AppHeadersFor(route utils.ProxyRouteConfig, public PublicAddress, scheme, dialled, targetScheme string) AppHeaders {
	out := AppHeaders{Host: public.Host, ForwardedHost: public.Host, ForwardedPort: public.Port, ForwardedProto: scheme, NoVisitorAddress: route.ForkNoVisitorAddress}
	if route.ForkAppOwnHost && dialled != "" {
		out.Host = ownHost(dialled, targetScheme)
	}
	return out
}

// PublicAddressFor is the address a route is reached at from outside, as the
// check of src/behind asks it: the URL's name on Cosmos' public HTTPS port
// (or HTTP port for a name served over plain HTTP).
func PublicAddressFor(route utils.ProxyRouteConfig) (PublicAddress, string, bool) {
	scheme := "http"
	host := strings.TrimSpace(route.Host)
	if utils.HostServedOverHTTPS(host) { // as the Director decides it
		scheme = "https"
	}
	config := utils.GetMainConfig().HTTPConfig
	port := strings.TrimSpace(config.HTTPPort)
	if scheme == "https" {
		port = strings.TrimSpace(config.HTTPSPort)
		if p := utils.PublicHTTPSPort(config); p > 0 {
			port = strconv.Itoa(p)
		}
	}
	if port == "" {
		port = defaultPort(scheme)
	}
	name, _ := splitHost(host)
	public, ok := publicAddressOf(route, joinPublic(name, port, scheme), port, scheme)
	return public, scheme, ok
}

// forkPublicHeaders is the Director's hook (routeTo.go), after upstream set
// its X-Forwarded-* and before SpoofHostname and the route's extra headers.
func forkPublicHeaders(req *http.Request, route utils.ProxyRouteConfig, scheme string, target string) {
	if route.ForkNoVisitorAddress {
		// a nil value keeps the reverse proxy from adding X-Forwarded-For itself
		req.Header["X-Forwarded-For"] = nil
		for _, h := range []string{"X-Real-Ip", "Cf-Connecting-Ip", "Cf-Connecting-Ipv6", "True-Client-Ip"} {
			req.Header.Del(h)
		}
	}
	public, ok := publicAddressOf(route, req.Host, localPortOf(req), scheme)
	if !ok {
		return
	}
	targetScheme := "http"
	if strings.HasPrefix(strings.ToLower(target), "https://") {
		targetScheme = "https"
	}
	h := AppHeadersFor(route, public, scheme, req.URL.Host, targetScheme)
	req.Host = h.Host
	req.Header.Set("X-Forwarded-Host", h.ForwardedHost)
	req.Header.Set("X-Forwarded-Port", h.ForwardedPort)
	// for the answer: where the visitor arrived (forkFixLocation)
	*req = *req.WithContext(context.WithValue(req.Context(), publicKey{}, publicSeen{public, scheme}))
}

type publicKey struct{}

type publicSeen struct {
	address PublicAddress
	scheme  string
}

// FixLocation repairs the one redirect that can never work: to the URL's own
// name and port, over plain HTTP, when that port serves HTTPS (Sonarr and
// Radarr send it while they do not trust the proxy's X-Forwarded-Proto: the
// browser would speak HTTP to the HTTPS port and get a 400). Nothing else of
// the address is changed, and no other address is touched.
func FixLocation(location string, public PublicAddress, scheme string) string {
	if scheme != "https" || !strings.HasPrefix(strings.ToLower(location), "http://") {
		return location
	}
	u, err := url.Parse(location)
	if err != nil || u.Port() == "" || u.Port() != public.Port || !strings.EqualFold(u.Hostname(), public.Name) || u.User != nil {
		return location
	}
	u.Scheme = "https"
	return u.String()
}

// forkFixLocation is the hook of the proxy's ModifyResponse (routeTo.go).
func forkFixLocation(resp *http.Response) {
	if resp == nil || resp.Request == nil {
		return
	}
	seen, ok := resp.Request.Context().Value(publicKey{}).(publicSeen)
	if !ok {
		return
	}
	if location := resp.Header.Get("Location"); location != "" {
		if fixed := FixLocation(location, seen.address, seen.scheme); fixed != location {
			resp.Header.Set("Location", fixed)
		}
	}
}
