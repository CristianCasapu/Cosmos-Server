package proxy

// Fork addition (plan 44): tests of fork_public_host.go, what every app is
// told about the address it is reached at.

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

type seenRequest struct {
	host   string
	header http.Header
}

// asApp sends one request through a route's proxy, as a visitor of Host
// requestHost who connected to this server's port localPort, and returns
// what the app received.
func asApp(t *testing.T, route utils.ProxyRouteConfig, requestHost, localPort string, headers map[string]string) (seenRequest, string) {
	t.Helper()
	seen := make(chan seenRequest, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- seenRequest{host: r.Host, header: r.Header.Clone()}
	}))
	t.Cleanup(backend.Close)
	if route.Mode == "" {
		route.Mode = "PROXY"
	}
	route.Target = backend.URL
	proxy, err := NewProxy(backend.URL, false, false, route)
	if err != nil {
		t.Fatal("NewProxy:", err)
	}
	r := httptest.NewRequest("GET", "http://placeholder/", nil)
	r.Host = requestHost
	r.RemoteAddr = "198.51.100.20:40000"
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	if localPort != "" {
		addr := &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: atoiOr(localPort)}
		r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, addr))
	}
	proxy.ServeHTTP(httptest.NewRecorder(), r)
	return <-seen, strings.TrimPrefix(backend.URL, "http://")
}

func atoiOr(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// onServer sets the server's ports as the owner's kind of setup has them.
func onServer(t *testing.T, mutate func(*utils.Config)) {
	t.Helper()
	config := utils.Config{}
	config.HTTPConfig.Hostname = "server.example.org"
	config.HTTPConfig.HTTPPort = "2052"
	config.HTTPConfig.HTTPSPort = "2053"
	config.HTTPConfig.HTTPSCertificateMode = utils.HTTPSCertModeList["SELFSIGNED"]
	if mutate != nil {
		mutate(&config)
	}
	utils.LoadBaseMainConfig(config)
	listening := utils.HTTPSListening
	utils.HTTPSListening = true
	t.Cleanup(func() {
		utils.HTTPSListening = listening
		utils.LoadBaseMainConfig(utils.Config{})
	})
}

func expectApp(t *testing.T, got seenRequest, host, forwardedHost, port, proto string) {
	t.Helper()
	if got.host != host {
		t.Errorf("Host: the app received %q, want %q", got.host, host)
	}
	if v := got.header.Values("X-Forwarded-Host"); len(v) != 1 || v[0] != forwardedHost {
		t.Errorf("X-Forwarded-Host: the app received %q, want %q", v, forwardedHost)
	}
	if v := got.header.Values("X-Forwarded-Port"); len(v) != 1 || v[0] != port {
		t.Errorf("X-Forwarded-Port: the app received %q, want %q", v, port)
	}
	if v := got.header.Get("X-Forwarded-Proto"); v != proto {
		t.Errorf("X-Forwarded-Proto: the app received %q, want %q", v, proto)
	}
}

func TestAppIsToldThePublicAddressWithItsPort(t *testing.T) {
	onServer(t, nil)
	route := utils.ProxyRouteConfig{Name: "qbt", UseHost: true, Host: "qbittorrent.server.local"}

	// at home on the .local name, and through Cloudflare on 2053 (Cloudflare keeps the port in Host)
	got, _ := asApp(t, route, "qbittorrent.server.local:2053", "2053", nil)
	expectApp(t, got, "qbittorrent.server.local:2053", "qbittorrent.server.local:2053", "2053", "https")

	domain := utils.ProxyRouteConfig{Name: "qbt-ext", UseHost: true, Host: "qbittorrent.example.org"}
	got, _ = asApp(t, domain, "qbittorrent.example.org:2053", "2053", map[string]string{"CF-Connecting-IP": "203.0.113.9"})
	expectApp(t, got, "qbittorrent.example.org:2053", "qbittorrent.example.org:2053", "2053", "https")

	// no port in Host: the scheme's own, said as a number
	got, _ = asApp(t, domain, "qbittorrent.example.org", "2053", nil)
	expectApp(t, got, "qbittorrent.example.org", "qbittorrent.example.org", "443", "https")
}

func TestTheVisitorsHostIsPassedAsReceived(t *testing.T) {
	onServer(t, nil)
	route := utils.ProxyRouteConfig{Name: "app", UseHost: true, Host: "app.example.org"}

	// Cosmos cannot know the public port in general: the visitor's is passed, in Host and the forwarded headers alike
	got, _ := asApp(t, route, "app.example.org:9999", "2053", nil)
	expectApp(t, got, "app.example.org:9999", "app.example.org:9999", "9999", "https")

	// as written, and the visitor's own forwarded headers are dropped
	got, _ = asApp(t, route, "APP.Example.ORG:2053", "2053", map[string]string{
		"X-Forwarded-Host":  "evil.example.net",
		"X-Forwarded-Port":  "1",
		"X-Forwarded-Proto": "gopher",
	})
	expectApp(t, got, "APP.Example.ORG:2053", "app.example.org:2053", "2053", "https")

	// an upper-case URL name changes nothing of what the visitor sent
	upper := utils.ProxyRouteConfig{Name: "up", UseHost: true, Host: "App.Example.ORG"}
	got, _ = asApp(t, upper, "app.example.org:2053", "2053", nil)
	expectApp(t, got, "app.example.org:2053", "app.example.org:2053", "2053", "https")
}

// Cosmos in Docker with `-p 2053:443` (its HTTPS port 443), no UPnP: the
// connection arrives on 443, the visitor typed 2053. A signed S3 request
// depends on Host reaching the backend byte for byte, in every setup.
func TestDockerRemappedPortAndSignedRequests(t *testing.T) {
	onServer(t, func(c *utils.Config) { c.HTTPConfig.HTTPSPort = "443" })
	s3 := "s3.example.org:2053"
	for _, route := range []utils.ProxyRouteConfig{
		{Name: "s3", UseHost: true, Host: "s3.example.org"},
		{Name: "bucket", UseHost: true, Host: "{bucket:[a-z0-9-]+}.s3.example.org"},
		{Name: "path", UsePathPrefix: true, PathPrefix: "/"},
		{Name: "upper", UseHost: true, Host: "S3.example.org"},
	} {
		for _, host := range []string{s3, "photos.s3.example.org:2053", "s3.example.org", "192.0.2.10:2053"} {
			got, _ := asApp(t, route, host, "443", nil)
			if got.host != host {
				t.Errorf("%s: Host %q reached the backend as %q", route.Name, host, got.host)
			}
		}
	}
	got, _ := asApp(t, utils.ProxyRouteConfig{Name: "s3", UseHost: true, Host: "s3.example.org"}, s3, "443", nil)
	expectApp(t, got, s3, s3, "2053", "https")
}

func TestTheOptionsNeverApplyToATunnelledURL(t *testing.T) {
	onServer(t, nil)
	for _, route := range []utils.ProxyRouteConfig{
		{Name: "t1", UseHost: true, Host: "app.example.org", Tunnel: "node-b", ForkAppOwnHost: true, ForkNoVisitorAddress: true},
		{Name: "t2", UseHost: true, Host: "app.example.org", Const_IsTunneled: true, ForkAppOwnHost: true, ForkNoVisitorAddress: true},
	} {
		got, _ := asApp(t, route, "app.example.org:2053", "2053", nil)
		if got.host != "app.example.org:2053" || got.header.Get("X-Forwarded-For") == "" {
			t.Errorf("%s: Host %q, X-Forwarded-For %q: an option travelled with the tunnel", route.Name, got.host, got.header.Get("X-Forwarded-For"))
		}
	}
}

func TestNoLookAlikeTellsTheAppAnything(t *testing.T) {
	onServer(t, nil)
	sent := map[string]string{}
	for _, h := range addressLookAlikes {
		sent[h] = "evil.example.net"
	}
	sent["X_Forwarded_Server"] = "evil.example.net"
	sent["HTTP_X_ORIGINAL_HOST"] = "evil.example.net"
	for _, served := range []string{"https", "http"} {
		if served == "http" {
			utils.HTTPSListening = false
		}
		got, _ := asApp(t, utils.ProxyRouteConfig{Name: "app", UseHost: true, Host: "app.example.org"}, "app.example.org:2053", "2053", sent)
		for name := range got.header {
			for _, v := range got.header.Values(name) {
				if strings.Contains(v, "evil") {
					t.Errorf("%s: the app was told %s = %q", served, name, v)
				}
			}
		}
		if served == "http" && got.header.Get("X-Forwarded-Ssl") != "" {
			t.Error("X-Forwarded-Ssl on a name served over plain HTTP")
		}
	}
	// the URL's own extra headers still may set them, and override X-Forwarded-Host/-Port
	extra := utils.ProxyRouteConfig{Name: "app", UseHost: true, Host: "app.example.org",
		ExtraHeaders: map[string]string{"X-Forwarded-Prefix": "/app", "X-Forwarded-Host": "inner.example.org", "X-Forwarded-Port": "8443"}}
	got, _ := asApp(t, extra, "app.example.org:2053", "2053", map[string]string{"X-Forwarded-Prefix": "/evil"})
	if got.header.Get("X-Forwarded-Prefix") != "/app" || got.header.Get("X-Forwarded-Host") != "inner.example.org" || got.header.Get("X-Forwarded-Port") != "8443" {
		t.Errorf("extra headers: %v", got.header)
	}
}

func TestRouterPortIsAPortTheAppMayBeTold(t *testing.T) {
	onServer(t, func(c *utils.Config) {
		c.HTTPConfig.UPnP.Enabled = true
		c.HTTPConfig.UPnP.ExternalHTTPSPort = "8443"
	})
	route := utils.ProxyRouteConfig{Name: "app", UseHost: true, Host: "app.example.org"}
	got, _ := asApp(t, route, "app.example.org:8443", "2053", nil)
	expectApp(t, got, "app.example.org:8443", "app.example.org:8443", "8443", "https")
}

func TestAppOwnHostTellsTheAppItsAddress(t *testing.T) {
	onServer(t, nil)
	route := utils.ProxyRouteConfig{Name: "qbt", UseHost: true, Host: "qbittorrent.server.local", ForkAppOwnHost: true}
	got, backend := asApp(t, route, "qbittorrent.server.local:2053", "2053", map[string]string{"Origin": "https://qbittorrent.server.local:2053"})
	expectApp(t, got, backend, "qbittorrent.server.local:2053", "2053", "https")
	if got.header.Get("Origin") != "https://qbittorrent.server.local:2053" {
		t.Errorf("the visitor's Origin must reach the app unchanged (its own CSRF check needs it): %q", got.header.Get("Origin"))
	}

	// the visitor cannot pick another virtual host of the backend
	got, backend = asApp(t, route, "admin.internal:2053", "2053", nil)
	if got.host != backend {
		t.Errorf("Host followed the visitor: %q, want the app's own %q", got.host, backend)
	}
}

func TestUpstreamOptionsKeepTheirMeaning(t *testing.T) {
	onServer(t, nil)
	spoof := utils.ProxyRouteConfig{Name: "s", UseHost: true, Host: "app.example.org", SpoofHostname: true}
	got, backend := asApp(t, spoof, "app.example.org:2053", "2053", nil)
	if got.host != backend || got.header.Get("X-Forwarded-Host") != "" || got.header.Get("X-Forwarded-Port") != "" {
		t.Errorf("SpoofHostname: Host %q, X-Forwarded-Host %q, X-Forwarded-Port %q; want the target, none, none",
			got.host, got.header.Get("X-Forwarded-Host"), got.header.Get("X-Forwarded-Port"))
	}

	overwrite := utils.ProxyRouteConfig{Name: "o", UseHost: true, Host: "app.example.org", OverwriteHostHeader: "inner.example.net:8080"}
	got, _ = asApp(t, overwrite, "app.example.org:2053", "2053", nil)
	if got.host != "inner.example.net:8080" || got.header.Get("X-Forwarded-Host") != "inner.example.net" || got.header.Get("X-Forwarded-Port") != "8080" {
		t.Errorf("OverwriteHostHeader: Host %q, X-Forwarded-Host %q, X-Forwarded-Port %q", got.host, got.header.Get("X-Forwarded-Host"), got.header.Get("X-Forwarded-Port"))
	}

	// a port route answers every name: left as upstream has it
	port := utils.ProxyRouteConfig{Name: "p", UseHost: true, Host: ":8080"}
	got, _ = asApp(t, port, "192.0.2.10:8080", "8080", nil)
	if got.host != "192.0.2.10:8080" || got.header.Get("X-Forwarded-Port") != "8080" {
		t.Errorf("port route: Host %q, X-Forwarded-Port %q", got.host, got.header.Get("X-Forwarded-Port"))
	}
}

func TestURLWithoutANameOfItsOwn(t *testing.T) {
	onServer(t, nil)
	route := utils.ProxyRouteConfig{Name: "path", UsePathPrefix: true, PathPrefix: "/app"}
	// such a URL answers ANY Host: the app keeps the visitor's Host (as before),
	// but nothing the visitor wrote there is repeated in the forwarded headers:
	// those stay upstream's (the server's own hostname)
	for _, visitor := range []string{"server.example.org:2053", "192.168.1.10:2053", "evil.example.net:2053"} {
		got, _ := asApp(t, route, visitor, "2053", nil)
		if got.host != visitor {
			t.Errorf("Host: the app received %q, want %q", got.host, visitor)
		}
		if v := got.header.Values("X-Forwarded-Host"); len(v) != 1 || v[0] != "server.example.org" {
			t.Errorf("%s: X-Forwarded-Host is %q, want the server's own name", visitor, v)
		}
	}
}

// Second review F1: the router pins a URL's name, not what follows the
// colon. Only a port NUMBER is ever repeated to the app: a list
// ("name:,evil.example"), letters, markup or a space after the colon never
// reach X-Forwarded-Host or X-Forwarded-Port (frameworks that read
// X-Forwarded-Host as a list would believe the last name). Host itself
// passes as the visitor sent it, as upstream.
func TestOnlyAPortNumberIsRepeatedToTheApp(t *testing.T) {
	onServer(t, nil)
	route := utils.ProxyRouteConfig{Name: "app", UseHost: true, Host: "app.example.org"}
	for visitor, want := range map[string][2]string{
		"app.example.org:1":               {"app.example.org:1", "1"},
		"app.example.org:02053":           {"app.example.org:2053", "2053"},
		"app.example.org:abc":             {"app.example.org", "443"},
		"app.example.org:,evil.example":   {"app.example.org", "443"},
		"app.example.org:%40evil.example": {"app.example.org", "443"},
		"app.example.org:\"><x>":          {"app.example.org", "443"},
		"app.example.org: evil":           {"app.example.org", "443"},
		"app.example.org:/evil":           {"app.example.org", "443"},
		"app.example.org:99999":           {"app.example.org", "443"},
		"app.example.org:0":               {"app.example.org", "443"},
		"app.example.org":                 {"app.example.org", "443"},
	} {
		got, _ := asApp(t, route, visitor, "2053", nil)
		if v := got.header.Values("X-Forwarded-Host"); len(v) != 1 || v[0] != want[0] {
			t.Errorf("%q: X-Forwarded-Host is %q, want %q", visitor, v, want[0])
		}
		if v := got.header.Values("X-Forwarded-Port"); len(v) != 1 || v[0] != want[1] {
			t.Errorf("%q: X-Forwarded-Port is %q, want %q", visitor, v, want[1])
		}
		for name, values := range got.header {
			if strings.HasPrefix(name, "X-Forwarded-") && strings.Contains(strings.Join(values, " "), "evil") {
				t.Errorf("%q: %s carries the visitor's text: %q", visitor, name, values)
			}
		}
	}
}

func TestAppOwnHostOverWebSocket(t *testing.T) {
	onServer(t, nil)
	hosts := make(chan string, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hosts <- r.Host + " " + r.Header.Get("X-Forwarded-Host")
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Upgrade", "websocket")
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	defer backend.Close()
	route := utils.ProxyRouteConfig{Name: "ws", Mode: "PROXY", UseHost: true, Host: "app.example.org", ForkAppOwnHost: true, Target: backend.URL}
	proxy, err := NewProxy(backend.URL, false, false, route)
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(proxy)
	defer front.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(front.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("GET /ws HTTP/1.1\r\nHost: app.example.org:2053\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nOrigin: https://app.example.org:2053\r\n\r\n"))
	status, _ := bufio.NewReader(conn).ReadString('\n')
	if !strings.Contains(status, "101") {
		t.Fatalf("the upgrade did not pass: %q", status)
	}
	want := strings.TrimPrefix(backend.URL, "http://") + " app.example.org:2053"
	if got := <-hosts; got != want {
		t.Errorf("over a WebSocket the app received %q, want %q", got, want)
	}
}

func TestOnlyTheImpossibleRedirectIsRepaired(t *testing.T) {
	public := PublicAddress{Name: "sonarr.server.local", Port: "2053", Host: "sonarr.server.local:2053"}
	for in, want := range map[string]string{
		// what Sonarr and Radarr send while they do not trust X-Forwarded-Proto
		"http://sonarr.server.local:2053/login?returnUrl=%2F": "https://sonarr.server.local:2053/login?returnUrl=%2F",
		"http://SONARR.server.local:2053/":                    "https://SONARR.server.local:2053/",
		// everything else stays as the app said it
		"/login":                                "/login",
		"https://sonarr.server.local:2053/":     "https://sonarr.server.local:2053/",
		"http://sonarr.server.local/":           "http://sonarr.server.local/",
		"http://sonarr.server.local:8989/":      "http://sonarr.server.local:8989/",
		"http://other.example.org:2053/":        "http://other.example.org:2053/",
		"http://user@sonarr.server.local:2053/": "http://user@sonarr.server.local:2053/",
		"http://172.30.0.23:8989/login":         "http://172.30.0.23:8989/login",
	} {
		if got := FixLocation(in, public, "https"); got != want {
			t.Errorf("%s became %s, want %s", in, got, want)
		}
	}
	if got := FixLocation("http://sonarr.server.local:2052/", PublicAddress{Name: "sonarr.server.local", Port: "2052"}, "http"); got != "http://sonarr.server.local:2052/" {
		t.Errorf("a name served over HTTP was changed: %s", got)
	}
}

func TestRedirectThroughTheProxy(t *testing.T) {
	onServer(t, nil)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// as Sonarr: from Host, with the scheme it saw
		http.Redirect(w, r, "http://"+r.Host+"/login", http.StatusFound)
	}))
	defer backend.Close()
	route := utils.ProxyRouteConfig{Name: "sonarr", Mode: "PROXY", UseHost: true, Host: "sonarr.server.local", Target: backend.URL}
	proxy, _ := NewProxy(backend.URL, false, false, route)
	r := httptest.NewRequest("GET", "http://placeholder/", nil)
	r.Host = "sonarr.server.local:2053"
	r.TLS = &tls.ConnectionState{}
	r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 2053}))
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, r)
	if got := rec.Header().Get("Location"); got != "https://sonarr.server.local:2053/login" {
		t.Errorf("the visitor was sent to %s", got)
	}
}

func TestNoVisitorAddressTellsTheAppNothingAboutTheVisitor(t *testing.T) {
	onServer(t, nil)
	route := utils.ProxyRouteConfig{Name: "ha", UseHost: true, Host: "homeassistant.server.local", ForkNoVisitorAddress: true}
	got, _ := asApp(t, route, "homeassistant.server.local:2053", "2053", map[string]string{"X-Forwarded-For": "203.0.113.7", "X-Real-IP": "203.0.113.7"})
	for _, h := range []string{"X-Forwarded-For", "X-Real-Ip", "Cf-Connecting-Ip", "True-Client-Ip", "Http_x_forwarded_for", "Http_x_real_ip"} {
		if v := got.header.Values(h); len(v) > 0 {
			t.Errorf("the app was told %s = %q", h, v)
		}
	}
	expectApp(t, got, "homeassistant.server.local:2053", "homeassistant.server.local:2053", "2053", "https")
}
