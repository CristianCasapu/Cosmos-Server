package proxy

// Fork addition (plan 44): tests of fork_public_host.go, what every app is
// told about the address it is reached at.

import (
	"bufio"
	"context"
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

func TestVisitorCannotChooseTheNameOrPortTheAppIsTold(t *testing.T) {
	onServer(t, nil)
	route := utils.ProxyRouteConfig{Name: "app", UseHost: true, Host: "app.example.org"}

	// a port this server is not reached at becomes the port of the connection
	got, _ := asApp(t, route, "app.example.org:9999", "2053", nil)
	expectApp(t, got, "app.example.org:2053", "app.example.org:2053", "2053", "https")

	// the name is the URL's, whatever the case the visitor wrote it in, and its own headers are dropped
	got, _ = asApp(t, route, "APP.Example.ORG:2053", "2053", map[string]string{
		"X-Forwarded-Host":  "evil.example.net",
		"X-Forwarded-Port":  "1",
		"X-Forwarded-Proto": "gopher",
	})
	expectApp(t, got, "app.example.org:2053", "app.example.org:2053", "2053", "https")
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
	got, _ := asApp(t, route, "server.example.org:2053", "2053", nil)
	expectApp(t, got, "server.example.org:2053", "server.example.org:2053", "2053", "https")
	// a name that is not the server's is not passed on
	got, _ = asApp(t, route, "other.example.net:2053", "2053", nil)
	expectApp(t, got, "server.example.org:2053", "server.example.org:2053", "2053", "https")
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
		"/login":                                  "/login",
		"https://sonarr.server.local:2053/":       "https://sonarr.server.local:2053/",
		"http://sonarr.server.local/":             "http://sonarr.server.local/",
		"http://sonarr.server.local:8989/":        "http://sonarr.server.local:8989/",
		"http://other.example.org:2053/":          "http://other.example.org:2053/",
		"http://user@sonarr.server.local:2053/":   "http://user@sonarr.server.local:2053/",
		"http://172.30.0.23:8989/login":           "http://172.30.0.23:8989/login",
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
	r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 2053}))
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, r)
	if got := rec.Header().Get("Location"); got != "https://sonarr.server.local:2053/login" {
		t.Errorf("the visitor was sent to %s", got)
	}
}
