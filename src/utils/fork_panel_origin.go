package utils

// Fork addition: the address of the panel as the visitor has to type it.
//
// When an app needs a Cosmos sign-in, the visitor is sent to the panel's
// sign-in page and back. Upstream built the panel's address from the
// hostname setting alone ("https://example.org/cosmos-ui/openid"), without a
// port. On a server whose HTTPS port is not 443 that address belongs to
// whatever answers on 443 (here another machine): the sign-in never opened
// and every app behind "people signed in to Cosmos" was unreachable.
//
// The panel and the apps are served by the same listener, so the port the
// visitor used for the app is the port of the panel: it is taken from the
// request (which also covers a router that forwards another external port),
// and from the settings only when there is no request to go by. The
// hostname always comes from the settings, never from the request.

import (
	"net"
	"net/http"
	"strconv"
)

// requestPort returns the port of a request's Host header, "" when it names
// none or something that is not a port.
func requestPort(req *http.Request) string {
	if req == nil {
		return ""
	}
	_, port, err := net.SplitHostPort(req.Host)
	if err != nil {
		return ""
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return ""
	}
	return port
}

// PanelOriginFor returns "https://<panel hostname>[:port]", without a
// trailing slash, for a visitor who made req.
func PanelOriginFor(req *http.Request) string {
	config := GetMainConfig().HTTPConfig
	scheme, standard := "http://", "80"
	if IsHTTPS {
		scheme, standard = "https://", "443"
	}

	host := config.Hostname
	if _, _, err := net.SplitHostPort(host); err == nil {
		// the setting names a port itself
		return scheme + host
	}

	port := ""
	if req != nil {
		port = requestPort(req)
	} else if IsHTTPS {
		port = config.HTTPSPort
	} else {
		port = config.HTTPPort
	}
	if port == "" || port == standard {
		return scheme + host
	}
	return scheme + net.JoinHostPort(host, port)
}

// HostWithoutPort returns the host of a Host header without its port.
func HostWithoutPort(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return hostport
}
