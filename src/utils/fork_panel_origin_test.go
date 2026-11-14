package utils

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPanelOriginFor(t *testing.T) {
	realConfig, realHTTPS := GetMainConfig(), IsHTTPS
	defer func() { LoadBaseMainConfig(realConfig); IsHTTPS = realHTTPS }()

	set := func(hostname string, httpsPort string) {
		cfg := Config{}
		cfg.HTTPConfig.Hostname = hostname
		cfg.HTTPConfig.HTTPSPort = httpsPort
		cfg.HTTPConfig.HTTPPort = "2052"
		LoadBaseMainConfig(cfg)
	}
	set("example.org", "2053")
	IsHTTPS = true

	request := func(host string) *http.Request {
		req := httptest.NewRequest("GET", "https://placeholder/", nil)
		req.Host = host
		return req
	}

	for host, want := range map[string]string{
		// the visitor of an app on the server's own HTTPS port
		"radarr.example.org:2053": "https://example.org:2053",
		// a router that forwards another external port: the visitor's port counts
		"radarr.example.org:8443": "https://example.org:8443",
		// the standard port, written or not: no port
		"radarr.example.org:443": "https://example.org",
		"radarr.example.org":     "https://example.org",
		// a Host header that is not a host and a port: the port is ignored, the name is never taken from it
		"radarr.example.org:2053@evil.example": "https://example.org",
		"evil.example:99999":                   "https://example.org",
		"evil.example:0":                       "https://example.org",
		"evil.example:02053":                   "https://example.org",
		"evil.example:20 53":                   "https://example.org",
		"[::1]:2053":                           "https://example.org:2053",
	} {
		if got := PanelOriginFor(request(host)); got != want {
			t.Errorf("Host %q: got %q, want %q", host, got, want)
		}
	}

	// no request to go by: the settings
	if got := PanelOriginFor(nil); got != "https://example.org:2053" {
		t.Errorf("without a request: %q", got)
	}
	set("example.org", "443")
	if got := PanelOriginFor(nil); got != "https://example.org" {
		t.Errorf("without a request, standard port: %q", got)
	}

	// plain HTTP
	IsHTTPS = false
	if got := PanelOriginFor(request("app.example.org:2052")); got != "http://example.org:2052" {
		t.Errorf("plain HTTP: %q", got)
	}
	if got := PanelOriginFor(request("app.example.org:80")); got != "http://example.org" {
		t.Errorf("plain HTTP, standard port: %q", got)
	}

	// a hostname setting that names a port itself is used as it is
	IsHTTPS = true
	set("example.org:9443", "2053")
	if got := PanelOriginFor(request("app.example.org:2053")); got != "https://example.org:9443" {
		t.Errorf("hostname with its own port: %q", got)
	}

	if HostWithoutPort("example.org:2053") != "example.org" || HostWithoutPort("example.org") != "example.org" {
		t.Error("HostWithoutPort")
	}
}

// The addresses a visitor may come back to after the Cosmos sign-in of an app
// carry the server's HTTPS port, and both forms are registered whatever
// IsHTTPS says when the clients are registered (at start it is still false).
func TestRouteSignInComesBackOnTheServersPort(t *testing.T) {
	realConfig, realHTTPS := GetMainConfig(), IsHTTPS
	defer func() { LoadBaseMainConfig(realConfig); IsHTTPS = realHTTPS }()

	cfg := Config{}
	cfg.HTTPConfig.Hostname = "example.org"
	cfg.HTTPConfig.HTTPSPort = "2053"
	cfg.HTTPConfig.HTTPPort = "2052"
	cfg.HTTPConfig.AuthPrivateKey = "0123456789012345678901234567890123456789012345678901234567890123456789"
	LoadBaseMainConfig(cfg)
	route := ProxyRouteConfig{Name: "radarr", Host: "radarr.example.org", UseHost: true, AuthEnabled: true}

	has := func(list []string, want string) bool {
		for _, entry := range list {
			if entry == want {
				return true
			}
		}
		return false
	}

	IsHTTPS = false // as when the clients are registered
	registered := GetProxyOIDCredentials(route, false).RedirectURIs
	IsHTTPS = true // as when a visitor arrives
	asked := GetProxyOIDCredentials(route, false).RedirectURIs

	if asked[0] != "https://radarr.example.org:2053/cosmos/oauth2/detect-callback" {
		t.Errorf("the visitor is sent back to %q", asked[0])
	}
	if !has(registered, asked[0]) {
		t.Errorf("the address the visitor is sent back to is not among the registered ones: %v", registered)
	}
	for _, want := range []string{"https://radarr.example.org:2053/", "https://radarr.example.org/", "https://radarr.example.org/cosmos/oauth2/detect-callback"} {
		if !has(registered, want) || !has(asked, want) {
			t.Errorf("%q is not registered in both states", want)
		}
	}

	// the standard port: exactly upstream's list, no doubles
	cfg.HTTPConfig.HTTPSPort = "443"
	LoadBaseMainConfig(cfg)
	plain := GetProxyOIDCredentials(route, false).RedirectURIs
	if plain[0] != "https://radarr.example.org/cosmos/oauth2/detect-callback" || has(plain, "https://radarr.example.org:443/") {
		t.Errorf("on port 443: %v", plain)
	}

	// a route that names a port itself is left alone
	cfg.HTTPConfig.HTTPSPort = "2053"
	LoadBaseMainConfig(cfg)
	own := GetProxyOIDCredentials(ProxyRouteConfig{Name: "x", Host: "x.example.org:9000", UseHost: true}, false).RedirectURIs
	if own[0] != "https://x.example.org:9000/cosmos/oauth2/detect-callback" || has(own, "https://x.example.org:9000:2053/") {
		t.Errorf("route with its own port: %v", own)
	}
}
