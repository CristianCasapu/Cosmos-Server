package proxy

// Fork addition (plan 44): what an app is told about the address it is
// reached at.
//
// Upstream passed the visitor's Host on as it came and told the app
// X-Forwarded-Host without its port and no X-Forwarded-Port at all. On a
// server whose HTTPS port is not 443 (2053, for Cloudflare's sake) an app that
// builds links or checks origins from those headers got them wrong: links to
// port 443, an Origin "https://app.example.org:2053" compared with
// "app.example.org". Now:
//
//   - Host is passed as the visitor sent it, byte for byte (as before: a signed
//     S3 request depends on it), and X-Forwarded-Host is that same Host, port
//     included, X-Forwarded-Port its port (the scheme's own when it names
//     none), as Traefik and Caddy pass them. The name in it matched the URL;
//     the port is the visitor's, because Cosmos cannot know the public port in
//     general (Docker publishing 2053 to Cosmos' 443, a router or Cloudflare
//     forwarding another port, a port forwarded by hand): replacing it would be
//     a guess, and wrong exactly where the app compares it with the browser's
//     Origin. So the forwarded headers never say anything the Host the app
//     receives does not already say; whatever the visitor sends as
//     X-Forwarded-Host/-Port/-Proto and their look-alikes is dropped.
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
// name and are left as upstream has them. The two fork options never apply
// to a tunnelled URL (its copy on another node would send the hosting node an
// address it does not route, or hide the visitor from its shield).

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

// PublicAddress is what an app is told about where it is reached.
type PublicAddress struct {
	Name string // the URL's name, lower case, without port
	Port string // the port visitors use, always set
	// Host is the visitor's Host exactly as received: what the app gets as Host
	Host string
	// Forwarded is what X-Forwarded-Host carries: the name and, when the
	// visitor's Host had one, its port as a number. Never anything else of
	// what the visitor wrote after the colon.
	Forwarded string
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

// publicAddressOf works out the address an app is told about from the
// visitor's Host as received. ok is false for the URLs left as upstream has
// them (OverwriteHostHeader, port routes) and for a request without a Host.
func publicAddressOf(route utils.ProxyRouteConfig, requestHost, scheme string) (PublicAddress, bool) {
	if route.OverwriteHostHeader != "" {
		return PublicAddress{}, false
	}
	if route.UseHost {
		routeName, _ := splitHost(strings.TrimSpace(route.Host))
		if routeName == "" || routeName == "0.0.0.0" {
			return PublicAddress{}, false // a port route answers any name
		}
	}
	if !route.UseHost {
		// a URL without a name of its own answers any Host: nothing the visitor
		// wrote there is repeated to the app (upstream's headers stay: the
		// server's own hostname)
		return PublicAddress{}, false
	}
	host := strings.TrimSpace(requestHost)
	name, port := splitHost(host)
	name = strings.ToLower(name)
	if !forwardableName(name) {
		return PublicAddress{}, false
	}
	// The router pins the name of a URL, not what follows the colon: only a
	// port number is ever repeated ("name:,evil.example" would make an app
	// that reads X-Forwarded-Host as a list believe another name).
	number := portNumber(port)
	forwarded := name
	if strings.Contains(name, ":") {
		forwarded = "[" + name + "]"
	}
	if number == "" {
		port = defaultPort(scheme)
	} else {
		port = number
		forwarded = net.JoinHostPort(name, number)
	}
	return PublicAddress{Name: name, Port: port, Host: host, Forwarded: forwarded}, true
}

// forwardableName: a host name or an address, nothing else.
func forwardableName(name string) bool {
	if name == "" || len(name) > 253 {
		return false
	}
	if net.ParseIP(name) != nil {
		return true
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_') {
			return false
		}
	}
	return true
}

// portNumber: "2053" for a port from 1 to 65535 written in digits, "" otherwise.
func portNumber(port string) string {
	if port == "" || len(port) > 5 {
		return ""
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return ""
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return ""
	}
	return strconv.Itoa(n)
}

// forkOptions are the fork's options of a URL as they apply: never on a tunnelled URL.
func forkOptions(route utils.ProxyRouteConfig) (appOwnHost bool, noVisitorAddress bool) {
	if route.Tunnel != "" || route.Const_IsTunneled {
		return false, false
	}
	return route.ForkAppOwnHost, route.ForkNoVisitorAddress
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
	own, noVisitor := forkOptions(route)
	forwarded := public.Forwarded
	if forwarded == "" {
		forwarded = public.Host
	}
	out := AppHeaders{Host: public.Host, ForwardedHost: forwarded, ForwardedPort: public.Port, ForwardedProto: scheme, NoVisitorAddress: noVisitor}
	if own && dialled != "" {
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
	if !route.UseHost || name == "" || strings.ContainsAny(host, "{}, ") {
		return PublicAddress{}, scheme, false // no name of its own, or a template: no single address to ask with
	}
	public, ok := publicAddressOf(route, joinPublic(strings.ToLower(name), port, scheme), scheme)
	return public, scheme, ok
}

// forkPublicHeaders is the Director's hook (routeTo.go), after upstream set
// its X-Forwarded-* and before SpoofHostname and the route's extra headers.
func forkPublicHeaders(req *http.Request, route utils.ProxyRouteConfig, scheme string, target string) {
	_, noVisitor := forkOptions(route)
	if noVisitor {
		// a nil value keeps the reverse proxy from adding X-Forwarded-For itself
		req.Header["X-Forwarded-For"] = nil
		for _, h := range []string{"X-Real-Ip", "Cf-Connecting-Ip", "Cf-Connecting-Ipv6", "True-Client-Ip"} {
			req.Header.Del(h)
		}
	}
	public, ok := publicAddressOf(route, req.Host, scheme)
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
	// for the answer: where the visitor arrived, when it arrived over TLS (forkFixLocation)
	if req.TLS != nil {
		*req = *req.WithContext(context.WithValue(req.Context(), publicKey{}, publicSeen{public, scheme}))
	}
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
