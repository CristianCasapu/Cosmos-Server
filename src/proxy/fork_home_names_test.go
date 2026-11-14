package proxy

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/utils"
)

// homeProxyConfig: a Cloudflare domain with names for use at home, an app
// whose local URL needs a Cosmos sign-in and one without.
func homeProxyConfig(t *testing.T, backend string) {
	t.Helper()
	cfg := utils.Config{}
	cfg.MonitoringDisabled = true
	cfg.HTTPConfig.Hostname = "example.org"
	cfg.HTTPConfig.HTTPPort = "2052"
	cfg.HTTPConfig.HTTPSPort = "2053"
	cfg.HTTPConfig.AuthPrivateKey = strings.Repeat("k", 128)
	cfg.HTTPConfig.DNSZones = []utils.DNSZoneConfig{{
		Zone: "example.org", HTTPSCertificateMode: utils.HTTPSCertModeList["LETSENCRYPT"],
		DNSChallengeProvider: "cloudflare", DNSChallengeConfig: map[string]string{"CF_DNS_API_TOKEN": "t"},
		ManageRecords: true, UseWildcardCertificate: true,
	}}
	cfg.HTTPConfig.Cloudflare.HomeNames = []utils.ForkHomeNamesZone{{Zone: "example.org", Label: "home"}}
	cfg.HTTPConfig.ProxyConfig.Routes = []utils.ProxyRouteConfig{
		{Name: "open", UseHost: true, Host: "open.local", Target: backend, Mode: "PROXY", DisableHeaderHardening: true},
		{Name: "gated", UseHost: true, Host: "gated.local", Target: backend, Mode: "PROXY", AuthEnabled: true, DisableHeaderHardening: true},
		// a URL that answers any host: it must not swallow the home names
		{Name: "catchall", UseHost: false, Target: backend + "/catchall", Mode: "PROXY", DisableHeaderHardening: true},
	}
	previous := utils.GetMainConfig()
	utils.LoadBaseMainConfig(cfg)
	prevMetrics := utils.PushShieldMetrics
	utils.PushShieldMetrics = func(string) {}
	realHTTPS := utils.IsHTTPS
	utils.IsHTTPS = true
	t.Cleanup(func() {
		utils.LoadBaseMainConfig(previous)
		utils.PushShieldMetrics = prevMetrics
		utils.IsHTTPS = realHTTPS
	})
}

func TestHomeNamesServeTheRouteAtHomeOnly(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "app:"+r.URL.Path)
	}))
	defer backend.Close()
	homeProxyConfig(t, backend.URL)
	events := captureSecurity(t)

	router := mux.NewRouter()
	router.Use(HomeNameGuard)
	BuildFromConfig(router, utils.GetMainConfig().HTTPConfig.ProxyConfig)
	handler := utils.ClientRealIP(router)

	get := func(host, remote string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "https://"+host+"/page", nil)
		r.Host = host
		r.RemoteAddr = remote
		// a visitor's own claim never makes it local
		r.Header.Set("X-Forwarded-For", "192.168.1.9")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	// from home: the app, not the catch-all
	if w := get("open.home.example.org:2053", "192.168.1.50:40000"); w.Code != 200 || w.Body.String() != "app:/page" {
		t.Fatalf("home visitor: %d %q", w.Code, w.Body.String())
	}
	// Tailscale counts as the local network, as for .local names
	if w := get("open.home.example.org:2053", "100.101.102.103:40000"); w.Code != 200 {
		t.Errorf("Tailscale visitor: %d", w.Code)
	}
	// from the internet, or through Cloudflare (a home name is never shielded,
	// so the visitor is Cloudflare's own address): refused and recorded
	for _, remote := range []string{"203.0.113.9:40000", "104.16.0.1:40000"} {
		if w := get("open.home.example.org:2053", remote); w.Code != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", remote, w.Code)
		}
	}
	// the panel's home name and a name nobody serves under the suffix too
	if w := get("cosmos.home.example.org:2053", "203.0.113.9:40000"); w.Code != http.StatusForbidden {
		t.Errorf("panel home name from outside: %d", w.Code)
	}
	recorded := events()
	if len(recorded) != 3 {
		t.Fatalf("recorded %d events, want 3: %+v", len(recorded), recorded)
	}
	for _, event := range recorded {
		if event.Kind != utils.SecAccessLocalDenied || event.Details["reason"] != "home_name" || !strings.HasSuffix(event.Target, ".home.example.org") {
			t.Errorf("event: %+v", event)
		}
	}
	// other names are not the guard's business
	if w := get("example.org:2053", "203.0.113.9:40000"); w.Code != 200 || len(events()) != 3 {
		t.Errorf("another name from outside: %d", w.Code)
	}

	// the route's sign-in applies to its home name, and sends the visitor to
	// the panel's home name on the port they use
	w := get("gated.home.example.org:2053", "192.168.1.50:40000")
	loc := w.Header().Get("Location")
	if w.Code != http.StatusFound || !strings.HasPrefix(loc, "https://cosmos.home.example.org:2053/cosmos-ui/openid?") {
		t.Fatalf("gated home name: %d %q", w.Code, loc)
	}
	parsed, _ := url.Parse(loc)
	if redirect := parsed.Query().Get("redirect_uri"); redirect != "https://gated.home.example.org:2053/cosmos/oauth2/detect-callback" {
		t.Errorf("return address %q", redirect)
	}
}

func TestHomeNamesFollowTheLANInterface(t *testing.T) {
	prevIfaces, prevRoute := mdnsSystemInterfaces, mdnsDefaultRoute
	defer func() { mdnsSystemInterfaces, mdnsDefaultRoute = prevIfaces, prevRoute }()
	up := net.FlagUp | net.FlagMulticast | net.FlagBroadcast
	mdnsSystemInterfaces = func() ([]systemInterface, error) {
		return []systemInterface{
			{Name: "docker0", Index: 2, Flags: up, IPv4: []net.IP{net.ParseIP("172.17.0.1")}},
			{Name: "tailscale0", Index: 3, Flags: up, IPv4: []net.IP{net.ParseIP("100.64.1.2")}},
			{Name: "eno1", Index: 4, Flags: up, IPv4: []net.IP{net.ParseIP("192.168.1.20")}},
		}, nil
	}
	mdnsDefaultRoute = func() string { return "eno1" }
	if got := lanAddressForHomeNames(); got != "192.168.1.20" {
		t.Errorf("got %q", got)
	}
	// a server whose default route has a public address has no home address
	mdnsSystemInterfaces = func() ([]systemInterface, error) {
		return []systemInterface{{Name: "eth0", Index: 2, Flags: up, IPv4: []net.IP{net.ParseIP("203.0.113.7")}}}, nil
	}
	mdnsDefaultRoute = func() string { return "eth0" }
	if got := lanAddressForHomeNames(); got != "" {
		t.Errorf("public interface: %q", got)
	}
}
