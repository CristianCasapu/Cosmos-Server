package main

// Fork addition: the API answers the panel's own pages, not other sites.
//
// The session lives in a cookie, and a browser attaches a cookie to a request
// whoever started it. So every request to the API (/cosmos/api, /cosmos/rclone,
// /cosmos/_logs) is asked where it comes from before anything else looks at it:
//
//   - a request the browser marks as started by the page of the same origin
//     (Sec-Fetch-Site: same-origin) passes: that is every call, download and
//     terminal of the panel itself;
//   - a request the browser marks otherwise is refused: another site
//     (cross-site), another application of the same domain (same-site), and
//     an address opened from the address bar, a bookmark or another program
//     (none). The panel never needs the last one, and a link somebody sends
//     must not be able to act;
//   - a browser that sends no such mark (an old one, or any browser on a plain
//     HTTP page) still sends Origin with everything but simple reads: when it
//     is there it has to name this host;
//   - a request with neither header does not come from a web page (curl, a
//     script, the command line client) and passes as before;
//   - a request that presents an API token passes as before: a browser never
//     adds one by itself.
//
// Left alone: /cosmos/oauth2/..., the last step of the sign-in of an
// application, which arrives from the panel's hostname on purpose.
//
// Nothing here replaces a permission check; it runs in front of them.

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/gorilla/mux"
)

// requireSameOrigin is the hook of InitServer: it puts the check in front of
// the API routers.
func requireSameOrigin(routers ...*mux.Router) {
	for _, router := range routers {
		router.Use(sameOriginOnly)
	}
}

func sameOriginOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reason := foreignRequest(r); reason != "" {
			utils.Warn("Refused a request to the API that the panel did not start (" + reason + "): " + r.Method + " " + printable(r.URL.Path, 120))
			utils.HTTPError(w, "This request did not come from the panel and was refused. Open the panel and use it from there.", http.StatusForbidden, "HTTP009")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// foreignRequest returns why a request must be refused, or "" when it may pass.
func foreignRequest(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/cosmos/oauth2/") {
		return ""
	}
	if presentsAPIToken(r) {
		return ""
	}

	switch site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); site {
	case "same-origin":
		return ""
	case "":
		origin := r.Header.Get("Origin")
		if origin == "" || originNamesHost(origin, r) {
			return ""
		}
		return "Origin is another site"
	case "cross-site", "same-site", "none":
		return "Sec-Fetch-Site: " + site
	default:
		return "Sec-Fetch-Site not understood"
	}
}

// presentsAPIToken mirrors tokenMiddleware: a request with an API token is
// authenticated by that token alone, never by the cookie.
func presentsAPIToken(r *http.Request) bool {
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "+utils.APITokenPrefix) {
		return true
	}
	return strings.HasPrefix(r.URL.Query().Get("token"), utils.APITokenPrefix)
}

// originNamesHost reports whether an Origin header names the host the request
// was sent to (or the one a proxy in front says the browser asked for; a web
// page cannot set that header on a request to another site).
func originNamesHost(origin string, r *http.Request) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	if sameHost(parsed, r.Host) {
		return true
	}
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" && sameHost(parsed, forwarded) {
		return true
	}
	return false
}

// sameHost compares the host of an origin with a Host header, the default
// port of the origin's scheme counting as no port.
func sameHost(origin *url.URL, host string) bool {
	return strings.EqualFold(withoutDefaultPort(origin.Host, origin.Scheme), withoutDefaultPort(strings.TrimSpace(host), origin.Scheme))
}

func withoutDefaultPort(host string, scheme string) string {
	name, port, err := net.SplitHostPort(host)
	if err != nil {
		return host
	}
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		if strings.Contains(name, ":") {
			return "[" + name + "]"
		}
		return name
	}
	return host
}

// printable keeps what is safe to write into a log line: no control
// characters, and not more than max bytes.
func printable(s string, max int) string {
	if len(s) > max {
		s = s[:max]
	}
	return strings.Map(func(c rune) rune {
		if c < 0x20 || c == 0x7f {
			return '?'
		}
		return c
	}, s)
}
