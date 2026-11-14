package utils

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// homeTestConfig is a server with a Cloudflare domain example.org that
// qualifies for names for use at home, the panel at example.org, and a few
// apps.
func homeTestConfig(label string) Config {
	cfg := Config{}
	cfg.HTTPConfig.Hostname = "example.org"
	cfg.HTTPConfig.HTTPSPort = "2053"
	cfg.HTTPConfig.HTTPPort = "2052"
	cfg.HTTPConfig.DNSZones = []DNSZoneConfig{{
		Zone:                   "example.org",
		HTTPSCertificateMode:   HTTPSCertModeList["LETSENCRYPT"],
		DNSChallengeProvider:   "cloudflare",
		DNSChallengeConfig:     map[string]string{"CF_DNS_API_TOKEN": "t0ken"},
		ManageRecords:          true,
		UseWildcardCertificate: true,
	}}
	route := func(name, host, target string) ProxyRouteConfig {
		return ProxyRouteConfig{Name: name, UseHost: true, Host: host, Target: target, Mode: "SERVAPP"}
	}
	cfg.HTTPConfig.ProxyConfig.Routes = []ProxyRouteConfig{
		route("radarr-example.org", "radarr.example.org", "http://radarr:7878"),
		route("radarr", "radarr.local", "http://radarr:7878"),
		route("sonarr", "sonarr.example.org", "http://sonarr:8989"),
		{Name: "sonarr-api", UseHost: true, Host: "sonarr.example.org", UsePathPrefix: true, PathPrefix: "/api", Target: "http://sonarr:8989", Mode: "SERVAPP"},
		route("byport", "sonarr.local:8443", "http://sonarr:8989"),
		route("byip", "192.168.1.5", "http://x:1"),
		route("off", "off.local", "http://off:1"),
		route("bad", "under_score.local", "http://x:1"),
		route("cosmos-app", "cosmos.local", "http://x:1"),
	}
	cfg.HTTPConfig.ProxyConfig.Routes[6].Disabled = true
	cfg.HTTPConfig.ProxyConfig.Routes[0].AuthEnabled = true
	cfg.HTTPConfig.ProxyConfig.Routes[1].AuthEnabled = true
	cfg.HTTPConfig.ProxyConfig.Routes[1].SmartShield.Enabled = true
	if label != "" {
		cfg.HTTPConfig.Cloudflare.HomeNames = []ForkHomeNamesZone{{Zone: "example.org", Label: label}}
	}
	return cfg
}

func withConfig(t *testing.T, cfg Config) {
	previous := GetMainConfig()
	LoadBaseMainConfig(cfg)
	t.Cleanup(func() { LoadBaseMainConfig(previous) })
}

func TestHomeLabelSyntax(t *testing.T) {
	for _, ok := range []string{"home", "h", "my-home", "a1", strings.Repeat("a", 63)} {
		if !ValidHomeLabel(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "Home", "-home", "home-", "ho_me", "*", "ho.me", "hôme", strings.Repeat("a", 64), "home\n"} {
		if ValidHomeLabel(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestHomeZoneProblems(t *testing.T) {
	base := homeTestConfig("")
	if p := HomeZoneProblem(base, "example.org", "home"); p != "" {
		t.Fatalf("a qualifying domain: %q", p)
	}
	cases := map[string]func(*Config){
		HomeProblemNoZone:        func(c *Config) { c.HTTPConfig.DNSZones[0].Zone = "example.net" },
		HomeProblemNotCloudflare: func(c *Config) { c.HTTPConfig.DNSZones[0].DNSChallengeProvider = "ovh" },
		HomeProblemNoToken:       func(c *Config) { c.HTTPConfig.DNSZones[0].DNSChallengeConfig = map[string]string{} },
		HomeProblemNoRecords:     func(c *Config) { c.HTTPConfig.DNSZones[0].ManageRecords = false },
		HomeProblemNoCertificate: func(c *Config) { c.HTTPConfig.DNSZones[0].HTTPSCertificateMode = HTTPSCertModeList["SELFSIGNED"] },
	}
	for want, change := range cases {
		cfg := homeTestConfig("")
		cfg.HTTPConfig.DNSZones = append([]DNSZoneConfig{}, cfg.HTTPConfig.DNSZones...)
		change(&cfg)
		if got := HomeZoneProblem(cfg, "example.org", "home"); got != want {
			t.Errorf("want %q, got %q", want, got)
		}
		cfg.HTTPConfig.Cloudflare.HomeNames = []ForkHomeNamesZone{{Zone: "example.org", Label: "home"}}
		if len(ActiveHomeZones(cfg)) != 0 {
			t.Errorf("%s: a domain that does not qualify is in force", want)
		}
	}
	if got := HomeZoneProblem(base, "example.org", strings.Repeat("a", 63)+"."+strings.Repeat("b", 63)+"."+strings.Repeat("c", 63)); got != HomeProblemTooLong {
		t.Errorf("no room for an app name: %q", got)
	}
	// an invalid stored label is never in force
	bad := homeTestConfig("Ho_me")
	if len(ActiveHomeZones(bad)) != 0 {
		t.Error("an invalid label is in force")
	}
}

func TestHomeNamesPlan(t *testing.T) {
	plan := PlanHomeNames(homeTestConfig("home"))
	got := map[string][]string{}
	for _, name := range plan.Names {
		got[name.Host] = append(got[name.Host], name.Route)
	}
	want := map[string][]string{
		// the local route wins over the external copy of the same app
		"radarr.home.example.org": {"radarr"},
		// every route of the chosen host, path prefixes included
		"sonarr.home.example.org": {"sonarr", "sonarr-api"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("names: %v, want %v", got, want)
	}
	if !reflect.DeepEqual(plan.Panel, []string{"cosmos.home.example.org"}) {
		t.Errorf("panel: %v", plan.Panel)
	}
	skipped := map[string]string{}
	for _, s := range plan.Skipped {
		skipped[s.Route] = s.Reason
	}
	for route, reason := range map[string]string{"radarr-example.org": "taken", "bad": "label", "cosmos-app": "panel"} {
		if skipped[route] != reason {
			t.Errorf("%s: skipped as %q, want %q", route, skipped[route], reason)
		}
	}
	for _, never := range []string{"byport", "byip", "off"} {
		for _, name := range plan.Names {
			if name.Route == never {
				t.Errorf("%s got a home name", never)
			}
		}
	}

	// a twin carries every setting of its route
	for _, twin := range plan.routes {
		if twin.Name == "radarr" && (!twin.AuthEnabled || !twin.SmartShield.Enabled || twin.Target != "http://radarr:7878" || twin.Host != "radarr.home.example.org") {
			t.Errorf("twin lost settings: %+v", twin)
		}
		if twin.Name == "sonarr-api" && (!twin.UsePathPrefix || twin.PathPrefix != "/api") {
			t.Errorf("path prefix lost: %+v", twin)
		}
	}

	// a URL the owner put under the suffix himself keeps its name: no twin
	cfg := homeTestConfig("home")
	cfg.HTTPConfig.ProxyConfig.Routes = append(cfg.HTTPConfig.ProxyConfig.Routes,
		ProxyRouteConfig{Name: "mine", UseHost: true, Host: "radarr.home.example.org", Target: "http://other:1", Mode: "SERVAPP"})
	plan = PlanHomeNames(cfg)
	for _, name := range plan.Names {
		if name.Host == "radarr.home.example.org" {
			t.Errorf("a twin over an explicit name: %+v", name)
		}
	}

	// switched off: nothing
	if p := PlanHomeNames(homeTestConfig("")); len(p.Names) != 0 || len(p.routes) != 0 || len(p.Panel) != 0 {
		t.Errorf("off: %+v", p)
	}
}

func TestHomeNamesBound(t *testing.T) {
	cfg := homeTestConfig("home")
	cfg.HTTPConfig.ProxyConfig.Routes = nil
	for i := 0; i < HomeNamesMaxPerZone+20; i++ {
		host := "app" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + itoa(i) + ".local"
		cfg.HTTPConfig.ProxyConfig.Routes = append(cfg.HTTPConfig.ProxyConfig.Routes, ProxyRouteConfig{Name: host, UseHost: true, Host: host, Target: "http://x:1"})
	}
	plan := PlanHomeNames(cfg)
	if len(plan.Names) != HomeNamesMaxPerZone {
		t.Errorf("%d names, want %d", len(plan.Names), HomeNamesMaxPerZone)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for ; i > 0; i /= 10 {
		s = string(rune('0'+i%10)) + s
	}
	return s
}

func TestPanelHomeName(t *testing.T) {
	for hostname, want := range map[string]string{
		"example.org":        "cosmos.home.example.org", // the domain's own name
		"172.16.0.10":        "cosmos.home.example.org",
		"panel.example.org":  "panel.home.example.org",
		"homebox.local":          "homebox.home.example.org",
		"under_score.example.org": "cosmos.home.example.org",
	} {
		cfg := homeTestConfig("home")
		cfg.HTTPConfig.Hostname = hostname
		if got := PanelHomeHost(cfg, ActiveHomeZones(cfg)[0]); got != want {
			t.Errorf("%s: %s, want %s", hostname, got, want)
		}
	}
}

func TestHomeNamesInTheHostLookups(t *testing.T) {
	withConfig(t, homeTestConfig("home"))

	hosts := GetAllHostnames(true)
	for _, want := range []string{"radarr.home.example.org", "sonarr.home.example.org", "cosmos.home.example.org"} {
		found := false
		for _, host := range hosts {
			found = found || host == want
		}
		if !found {
			t.Errorf("%s missing from GetAllHostnames: %v", want, hosts)
		}
	}
	if _, route := FindRouteByReqHost("radarr.home.example.org"); route == nil || route.Name != "radarr" || !route.AuthEnabled {
		t.Errorf("the sign-in callback finds no route for the home name: %+v", route)
	}
	if !IsHomeHost("Radarr.Home.Example.org:2053") || !IsHomeHost("anything.home.example.org") || IsHomeHost("home.example.org") || IsHomeHost("radarr.example.org") {
		t.Error("IsHomeHost")
	}
	if !IsPanelHomeHost("cosmos.home.example.org") || IsPanelHomeHost("radarr.home.example.org") {
		t.Error("IsPanelHomeHost")
	}

	// a new configuration is seen at once (the plan is cached per configuration)
	cfg := homeTestConfig("")
	LoadBaseMainConfig(cfg)
	if _, route := FindRouteByReqHost("radarr.home.example.org"); route != nil {
		t.Error("switched off, the home name still finds a route")
	}
	for _, host := range GetAllHostnames(true) {
		if strings.Contains(host, ".home.") {
			t.Errorf("switched off, still served: %s", host)
		}
	}
	cfg = homeTestConfig("home")
	cfg.HTTPConfig.ProxyConfig.Routes[1].AuthEnabled = false
	LoadBaseMainConfig(cfg)
	if _, route := FindRouteByReqHost("radarr.home.example.org"); route == nil || route.AuthEnabled {
		t.Error("an edit of the route does not reach its home name")
	}
}

func TestHomeCertificateNames(t *testing.T) {
	cfg := homeTestConfig("home")
	withConfig(t, cfg)
	zone := cfg.HTTPConfig.DNSZones[0]
	hosts := []string{"example.org", "radarr.example.org", "radarr.home.example.org", "cosmos.home.example.org", "deep.er.example.org"}

	got := ZoneCertHostnames(zone, hosts)
	want := []string{"example.org", "*.example.org", "*.home.example.org", "deep.er.example.org"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("wildcard certificate: %v, want %v", got, want)
	}
	// without the domain's wildcard: still one name for every home name, none on its own
	zone.UseWildcardCertificate = false
	got = ZoneCertHostnames(zone, hosts)
	sort.Strings(got)
	want = []string{"*.home.example.org", "deep.er.example.org", "example.org", "radarr.example.org"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("certificate per name: %v, want %v", got, want)
	}

	// switched off: the certificate that has the wildcard still covers what is wanted, no new order
	LoadBaseMainConfig(homeTestConfig(""))
	zone.UseWildcardCertificate = true
	wanted := ZoneCertHostnames(zone, []string{"example.org", "radarr.example.org"})
	if !ZoneCertCovers(ZoneCert{Hosts: []string{"example.org", "*.example.org", "*.home.example.org"}}, wanted) {
		t.Error("switching off would order again")
	}
	// on again: the old certificate covers it
	LoadBaseMainConfig(homeTestConfig("home"))
	wanted = ZoneCertHostnames(zone, append([]string{"example.org"}, hosts...))
	if !ZoneCertCovers(ZoneCert{Hosts: []string{"example.org", "*.example.org", "*.home.example.org", "deep.er.example.org"}}, wanted) {
		t.Errorf("on again: %v not covered", wanted)
	}
}

func TestPanelOriginOnAHomeName(t *testing.T) {
	realHTTPS := IsHTTPS
	defer func() { IsHTTPS = realHTTPS }()
	IsHTTPS = true
	withConfig(t, homeTestConfig("home"))
	req := func(host string) *http.Request {
		r := httptest.NewRequest("GET", "https://placeholder/", nil)
		r.Host = host
		return r
	}
	for host, want := range map[string]string{
		"radarr.home.example.org:2053": "https://cosmos.home.example.org:2053",
		"radarr.home.example.org":      "https://cosmos.home.example.org",
		"evil.home.example.org:2053":   "https://cosmos.home.example.org:2053", // always the configured name
		"radarr.example.org:2053":      "https://example.org:2053",
		"radarr.local:2053":            "https://example.org:2053",
	} {
		if got := PanelOriginFor(req(host)); got != want {
			t.Errorf("%s: %s, want %s", host, got, want)
		}
	}
}

func TestPanelAPIAnswersOnItsHomeName(t *testing.T) {
	withConfig(t, homeTestConfig("home"))
	handler := EnsureHostnameCosmosAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for host, want := range map[string]int{
		"cosmos.home.example.org:2053": 204,
		"example.org:2053":             204,
		// (the refusal itself needs the metrics store: an app's home name is
		// not the panel's, TestHomeNamesInTheHostLookups)
	} {
		r := httptest.NewRequest("GET", "https://placeholder/cosmos/api/status-x", nil)
		r.Host = host
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("%s: %d, want %d", host, w.Code, want)
		}
	}
}

func TestHomeLANAddressKeepsTheLastOne(t *testing.T) {
	previous := ForkLANAddress
	defer func() { ForkLANAddress = previous; homeLAN.last = "" }()
	ForkLANAddress = func() string { return "192.168.1.20" }
	if a, fresh := HomeLANAddress(); a != "192.168.1.20" || !fresh {
		t.Errorf("%s %v", a, fresh)
	}
	ForkLANAddress = func() string { return "" }
	if a, fresh := HomeLANAddress(); a != "192.168.1.20" || fresh {
		t.Errorf("kept: %s %v", a, fresh)
	}
	// never a public address
	ForkLANAddress = func() string { return "203.0.113.5" }
	if a, fresh := HomeLANAddress(); a != "192.168.1.20" || fresh {
		t.Errorf("public: %s %v", a, fresh)
	}
}
