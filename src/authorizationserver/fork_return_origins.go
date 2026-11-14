package authorizationserver

// Fork addition: the origins a sign-in may end on, for the panel's script
// policy (src/fork_csp.go). The consent page of the panel posts a form to
// /oauth2/auth, which answers with a redirect to the client's return address;
// browsers hold that redirect to the policy's form-action, so form-action
// lists 'self' and the origins of the registered return addresses, and no
// other place a form of the panel may send its fields to.
//
// The list is taken from the clients Init registered (the OpenID clients of
// the configuration, the sign-in clients of the routes and their names for
// use at home), so it changes with them.

import (
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/ory/fosite"
)

// at most this many origins, so that the header stays small whatever the configuration holds
const maxReturnOrigins = 200

var returnOrigins atomic.Value // []string

var (
	cspScheme = regexp.MustCompile(`^[a-z][a-z0-9+.-]*$`)
	cspHost   = regexp.MustCompile(`^[a-z0-9.-]+$|^\[[0-9a-f:.]+\]$`)
)

// rememberReturnOrigins is the hook of Init, once every client is registered.
func rememberReturnOrigins(clients map[string]fosite.Client) {
	seen := map[string]bool{}
	list := []string{}
	for _, client := range clients {
		for _, uri := range client.GetRedirectURIs() {
			source, ok := returnOriginSource(uri)
			if ok && !seen[source] {
				seen[source] = true
				list = append(list, source)
			}
		}
	}
	sort.Strings(list)
	if len(list) > maxReturnOrigins {
		list = list[:maxReturnOrigins]
	}
	returnOrigins.Store(list)
}

// ReturnOrigins lists the origins of the registered return addresses, as
// source expressions of a script policy.
func ReturnOrigins() []string {
	list, _ := returnOrigins.Load().([]string)
	return list
}

// returnOriginSource turns a return address into the source expression of
// its origin: scheme, host and port, or the scheme alone for an app's own
// scheme (com.example.app:/callback). Anything that is not plainly an address
// is left out rather than written into a header.
func returnOriginSource(uri string) (string, bool) {
	uri = strings.TrimSpace(uri)
	if uri == "" || len(uri) > 2048 {
		return "", false
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if !cspScheme.MatchString(scheme) {
		return "", false
	}
	if scheme != "http" && scheme != "https" {
		return scheme + ":", true
	}
	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if host == "" {
		return "", false
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if !cspHost.MatchString(host) {
		return "", false
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return "", false
		}
	}
	// a native app's loopback address may come back on another port (RFC 8252)
	if host == "localhost" || isLoopback(parsed.Hostname()) {
		return scheme + "://" + host + ":*", true
	}
	if port != "" {
		return scheme + "://" + host + ":" + port, true
	}
	return scheme + "://" + host, true
}

func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
