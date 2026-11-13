package main

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: on a server that serves HTTPS, the plain-HTTP port sends the
// hostnames of that server to HTTPS instead of answering 404.
//
// Upstream announces "starting HTTPS servers and redirecting HTTP to HTTPS",
// but the redirect was lost in 2026 and plainHTTPHandler answers 404 to
// everything that is not in an HTTP-only zone: http://172.16.0.14:2052/ and
// http://app.example.com/ both end there, although the same addresses work
// over HTTPS on the other port.
//
// What changes is that 404 alone:
//   - a hostname of an HTTP-only zone is served by the router, as before;
//   - a hostname the HTTPS side accepts (utils.IsValidHostname: the server's
//     own, the ones of its routes, any local IP when "allow insecure access
//     via local IP" is on, everything when every hostname is accepted) gets a
//     307 to the same path on the HTTPS port;
//   - anything else still gets the 404.
//
// Nothing more is served over plain HTTP than before: a redirect carries no
// page, no API answer and no cookie. In particular a local IP is not served
// directly here, even with "allow insecure access via local IP": the session
// cookie of a hostname served over HTTPS is marked Secure (user/token.go), so
// a login over plain HTTP could not hold, and making it hold means issuing
// that cookie without Secure, which is a decision for the owner and not part
// of this fix.
//
// The redirect is temporary (307) on purpose: browsers keep a permanent one
// for good, which would send them to a dead port the day HTTPS is switched
// off on this server. 307 also keeps the method and the body.

// plainHTTPRedirectTarget returns the HTTPS address of a request received on
// the plain-HTTP port, and false when the request is not for a hostname this
// server serves over HTTPS.
func plainHTTPRedirectTarget(host string, requestURL *url.URL, httpsPort string) (string, bool) {
	if requestURL == nil || utils.HostIsHTTPOnly(utils.GetMainConfig(), host) || !utils.IsValidHostname(host) {
		return "", false
	}

	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	name = strings.TrimSuffix(strings.TrimPrefix(name, "["), "]")

	if net.ParseIP(name) != nil {
		if strings.Contains(name, ":") {
			name = "[" + name + "]"
		}
	} else if !plainHostname(name) {
		// the Host header goes into the Location: only what is a plain name
		return "", false
	}

	target := "https://" + name
	if httpsPort != "" && httpsPort != "443" {
		target += ":" + httpsPort
	}

	// path and query only: whatever the request line held, the redirect stays on this host
	path := requestURL.EscapedPath()
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	target += path
	if requestURL.RawQuery != "" {
		target += "?" + requestURL.RawQuery
	}
	return target, true
}

// plainHostname reports whether a name is made of letters, digits, dots,
// hyphens and underscores only
func plainHostname(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// redirectPlainHTTPToHTTPS is the hook of plainHTTPHandler. It returns true
// when it answered the request.
func redirectPlainHTTPToHTTPS(w http.ResponseWriter, r *http.Request) bool {
	target, ok := plainHTTPRedirectTarget(r.Host, r.URL, serverPortHTTPS)
	if !ok {
		return false
	}
	http.Redirect(w, r, target, http.StatusTemporaryRedirect)
	return true
}
