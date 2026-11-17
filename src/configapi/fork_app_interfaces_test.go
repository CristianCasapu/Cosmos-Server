package configapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"

	"github.com/azukaar/cosmos-server/src/behind"
	"github.com/azukaar/cosmos-server/src/utils"
)

func servapp(name, target, host string) utils.ProxyRouteConfig {
	return utils.ProxyRouteConfig{Name: name, Mode: "SERVAPP", Target: target, UseHost: host != "", Host: host}
}

func TestProposeRole(t *testing.T) {
	cases := []struct {
		what   string
		route  utils.ProxyRouteConfig
		image  string
		role   string
		source string
		path   string
	}{
		{"RustFS console (recipe web port)", servapp("rustfs-console", "http://rustfs:9001", "rustfs-console.local"), "rustfs/rustfs:1.0.0", "admin", "recipe", ""},
		{"RustFS S3 (recipe service port)", servapp("rustfs", "http://rustfs:9000", "s3.example.org"), "rustfs/rustfs:latest", "api", "recipe", ""},
		{"Adminer (a database manager)", servapp("adminer", "http://adminer:8080", "adminer.local"), "adminer:5", "admin", "recipe", ""},
		{"qBittorrent web page", servapp("qbittorrent", "http://qbittorrent:8080", "qbittorrent.example.org"), "linuxserver/qbittorrent:latest", "admin", "app", ""},
		{"Radarr", servapp("radarr", "http://radarr:7878", "radarr.local"), "lscr.io/linuxserver/radarr", "admin", "app", ""},
		{"Jellyfin: users, its dashboard on a path", servapp("jellyfin", "http://jellyfin:8096", "jellyfin.example.org"), "jellyfin/jellyfin:10.10", "user", "app", "/web/#/dashboard"},
		{"Vaultwarden", servapp("vaultwarden", "http://vaultwarden:80", "vault.example.org"), "vaultwarden/server:latest", "user", "app", "/admin"},
		{"a known app on another port is not that page", servapp("qbt-peer", "http://qbittorrent:6881", ""), "linuxserver/qbittorrent", "user", "none", ""},
		{"a path to a console", utils.ProxyRouteConfig{Name: "nas", Mode: "PROXY", Target: "http://192.168.1.20:5000", UsePathPrefix: true, PathPrefix: "/console"}, "", "admin", "path", ""},
		{"a path to an API", utils.ProxyRouteConfig{Name: "nas", Mode: "PROXY", Target: "http://192.168.1.20:5000", UsePathPrefix: true, PathPrefix: "/api/v2"}, "", "api", "path", ""},
		{"the name says console", servapp("minio-console", "http://minio:9001", "minio-console.local"), "", "admin", "words", ""},
		{"the description says API", utils.ProxyRouteConfig{Name: "store", Mode: "SERVAPP", Target: "http://store:9000", Description: "S3 endpoint"}, "", "api", "words", ""},
		{"a word inside another is not that word", servapp("rapidapiary", "http://x:80", ""), "", "user", "none", ""},
		{"nothing known", servapp("whoami", "http://whoami:80", "whoami.local"), "traefik/whoami", "user", "none", ""},
		{"an image name on a PROXY route is not read", utils.ProxyRouteConfig{Name: "x", Mode: "PROXY", Target: "http://rustfs:9001"}, "rustfs/rustfs", "user", "none", ""},
	}
	for _, c := range cases {
		got := proposeRole(c.route, c.image)
		if got.Role != c.role || got.Source != c.source || got.AdminPath != c.path {
			t.Errorf("%s: proposeRole = %+v, want %s from %s, path %q", c.what, got, c.role, c.source, c.path)
		}
	}
}

// Every known app has a role, and every role there is one of the four.
func TestKnownAppRolesCoverTheKnownApps(t *testing.T) {
	for _, app := range behind.Apps() {
		known, ok := knownAppRoles[app.ID]
		if !ok {
			t.Errorf("known app %s has no role in knownAppRoles", app.ID)
			continue
		}
		if !utils.ValidInterfaceRole(known.Role) || known.Role == "" {
			t.Errorf("known app %s has role %q", app.ID, known.Role)
		}
		if known.AdminPath != "" && (!strings.HasPrefix(known.AdminPath, "/") || strings.Contains(known.AdminPath, "//") || strings.ContainsAny(known.AdminPath, " \"'<>\\")) {
			t.Errorf("known app %s has an administration path that is not a plain path: %q", app.ID, known.AdminPath)
		}
	}
}

func TestTargetPort(t *testing.T) {
	for target, want := range map[string]int{
		"http://rustfs:9001":       9001,
		"http://rustfs:9001/x":     9001,
		"https://app":              443,
		"http://app/path":          80,
		"http://[fd00::1]:8080":    8080,
		"http://[fd00::1]":         80,
		"rustfs:9001":              0,
		"http://app:99999":         0,
		"http://app:notaport":      0,
		"":                         0,
	} {
		if got := targetPort(target); got != want {
			t.Errorf("targetPort(%q) = %d, want %d", target, got, want)
		}
	}
}

func TestRouteInterfaceValidation(t *testing.T) {
	good := []*utils.ForkRouteInterface{nil, {}, {Role: "admin"}, {Role: "user", Label: "Web page"}, {Role: "api", Label: "S3 API (v4)"}, {Role: "other", HomeOnly: true}, {Label: "Consolă de administrare"}}
	for _, setting := range good {
		route := servapp("a", "http://a:80", "")
		route.ForkInterface = setting
		if msg := validateRoute(route); msg != "" {
			t.Errorf("validateRoute(%+v) = %q, want accepted", setting, msg)
		}
	}
	bad := []*utils.ForkRouteInterface{
		{Role: "javascript:alert(1)"},
		{Role: "Admin"},
		{Label: "<img src=x onerror=alert(1)>"},
		{Label: "a\nb"},
		{Label: "x y"},
		{Label: "\"quoted\""},
		{Label: strings.Repeat("a", 41)},
		{Label: " padded"},
		{Label: string([]byte{0xff, 0xfe})},
		{Label: "{{constructor}}"},
	}
	for _, setting := range bad {
		route := servapp("a", "http://a:80", "")
		route.ForkInterface = setting
		if msg := validateRoute(route); msg == "" {
			t.Errorf("validateRoute(%+v) accepted, want refused", setting)
		}
	}
}

// Upstream reads a configuration with the fork's field (it ignores it), and
// a configuration without it reads as "as proposed".
func TestRouteInterfaceInTheConfiguration(t *testing.T) {
	var route utils.ProxyRouteConfig
	if err := json.Unmarshal([]byte(`{"Name":"a","ForkInterface":{"Role":"admin","Label":"Console","HomeOnly":true}}`), &route); err != nil {
		t.Fatal(err)
	}
	if route.ForkInterface == nil || route.ForkInterface.Role != "admin" || route.ForkInterface.Label != "Console" || !route.ForkInterface.HomeOnly {
		t.Errorf("read %+v", route.ForkInterface)
	}
	var upstream struct{ Name string }
	if err := json.Unmarshal([]byte(`{"Name":"a","ForkInterface":{"Role":"admin"}}`), &upstream); err != nil || upstream.Name != "a" {
		t.Errorf("a reader without the field failed: %v", err)
	}
	out, _ := json.Marshal(utils.ProxyRouteConfig{Name: "b"})
	if strings.Contains(string(out), "ForkInterface") {
		t.Errorf("a route without a setting writes the field: %s", out)
	}
}

// The owner's shape: two-URL apps, a console with a local URL only, a
// database without a web page, an S3 app with an API URL and a console URL,
// a torrent app with a web port and a peer port.
func ownerShape() (utils.Config, []types.Container) {
	config := utils.Config{}
	config.HTTPConfig.ProxyConfig.Routes = []utils.ProxyRouteConfig{
		servapp("jellyfin", "http://jellyfin:8096", "jellyfin.local"),
		servapp("jellyfin-example.org", "http://jellyfin:8096", "jellyfin.example.org"),
		servapp("rustfs", "http://rustfs:9000", "s3.example.org"),
		servapp("rustfs-console", "http://rustfs:9001", "rustfs-console.local"),
		servapp("qbittorrent", "http://qbittorrent:8080", "qbittorrent.local"),
		{Name: "secret-admin", Mode: "SERVAPP", Target: "http://portainer:9000", UseHost: true, Host: "portainer.local", AdminOnly: true},
	}
	config.HTTPConfig.ProxyConfig.Routes[1].AuthEnabled = true
	containers := []types.Container{
		{Names: []string{"/jellyfin"}, Image: "jellyfin/jellyfin:10.10"},
		{Names: []string{"/rustfs"}, Image: "rustfs/rustfs:latest", Ports: []types.Port{
			{IP: "0.0.0.0", PrivatePort: 9000, PublicPort: 9000, Type: "tcp"},
			{IP: "::", PrivatePort: 9000, PublicPort: 9000, Type: "tcp"},
			{IP: "192.168.1.10", PrivatePort: 9001, PublicPort: 9001, Type: "tcp"},
			{PrivatePort: 7000, Type: "tcp"}, // not published
		}},
		{Names: []string{"/qbittorrent"}, Image: "linuxserver/qbittorrent", Ports: []types.Port{
			{IP: "0.0.0.0", PrivatePort: 6881, PublicPort: 6881, Type: "tcp"},
			{IP: "0.0.0.0", PrivatePort: 6881, PublicPort: 6881, Type: "udp"},
		}},
		{Names: []string{"/postgres"}, Image: "postgres:18", Ports: []types.Port{{IP: "192.168.1.10", PrivatePort: 5432, PublicPort: 5432, Type: "tcp"}}},
	}
	return config, containers
}

// what the upstream loop gives, for the hook
func dashboardOf(config utils.Config, isAdmin bool) []DashboardRoute {
	out := []DashboardRoute{}
	for _, route := range config.HTTPConfig.ProxyConfig.Routes {
		if !isAdmin && route.AdminOnly {
			continue
		}
		out = append(out, DashboardRoute{Name: route.Name, Target: route.Target, Mode: string(route.Mode), Host: route.Host, UseHost: route.UseHost})
	}
	return out
}

func TestDashboardFieldsForAnAdministrator(t *testing.T) {
	config, containers := ownerShape()
	config.HTTPConfig.ProxyConfig.Routes[4].ForkInterface = &utils.ForkRouteInterface{Role: "other", Label: "Torrents", HomeOnly: true}
	homes := []utils.HomeName{{Host: "jellyfin.home.example.org", Route: "jellyfin", Source: "jellyfin.local"}}
	got := forkDashboardRoutes(dashboardOf(config, true), config, containers, true, homes)
	by := map[string]DashboardRoute{}
	for _, r := range got {
		by[r.Name] = r
	}
	if r := by["rustfs-console"]; r.ForkRole != "admin" || r.ForkRoleSource != "recipe" || r.ForkImage != "rustfs/rustfs:latest" || r.ForkContainer != "rustfs" {
		t.Errorf("rustfs-console: %+v", r.ForkDashboardFields)
	}
	if r := by["rustfs"]; r.ForkRole != "api" || len(r.ForkPorts) != 2 || r.ForkPorts[0].Public != 9000 || r.ForkPorts[0].IP != "" || r.ForkPorts[1].IP != "192.168.1.10" {
		t.Errorf("rustfs: %+v", r.ForkDashboardFields)
	}
	if r := by["jellyfin"]; r.ForkRole != "user" || r.ForkAdminPath != "/web/#/dashboard" || len(r.ForkHomeHosts) != 1 || r.ForkHomeHosts[0] != "jellyfin.home.example.org" {
		t.Errorf("jellyfin: %+v", r.ForkDashboardFields)
	}
	if r := by["jellyfin-example.org"]; !r.AuthEnabled || len(r.ForkHomeHosts) != 0 {
		t.Errorf("jellyfin's public URL: %+v", r.ForkDashboardFields)
	}
	if r := by["qbittorrent"]; r.ForkRole != "other" || r.ForkRoleSource != "set" || r.ForkLabel != "Torrents" || !r.ForkHomeOnly || len(r.ForkPorts) != 2 {
		t.Errorf("qbittorrent: %+v", r.ForkDashboardFields)
	}
	if _, ok := by["secret-admin"]; !ok {
		t.Errorf("an administrator lost an admin-only URL")
	}
}

// A plain user gets the routes he got before, with their roles, and nothing
// about containers: no image, no container name, no ports.
func TestDashboardFieldsForAPlainUser(t *testing.T) {
	config, containers := ownerShape()
	got := forkDashboardRoutes(dashboardOf(config, false), config, containers, false, nil)
	if len(got) != 5 {
		t.Fatalf("a plain user got %d routes, want the 5 that are not admin-only", len(got))
	}
	for _, r := range got {
		if r.Name == "secret-admin" {
			t.Errorf("an admin-only URL reached a plain user")
		}
		if r.ForkImage != "" || r.ForkContainer != "" || len(r.ForkPorts) != 0 {
			t.Errorf("%s: a plain user got container details: %+v", r.Name, r.ForkDashboardFields)
		}
		if r.ForkRole == "" {
			t.Errorf("%s: no role", r.Name)
		}
	}
	raw, _ := json.Marshal(got)
	for _, word := range []string{"ForkImage", "ForkPorts", "ForkContainer", "192.168.1.10", "6881", "5432", "postgres"} {
		if strings.Contains(string(raw), word) {
			t.Errorf("the plain user's answer holds %q: %s", word, raw)
		}
	}
}

// A hostile setting that slipped into the file (written by hand) is not
// passed on: the proposal stands.
func TestDashboardIgnoresAHostileSetting(t *testing.T) {
	config, containers := ownerShape()
	config.HTTPConfig.ProxyConfig.Routes[0].ForkInterface = &utils.ForkRouteInterface{Role: "javascript:alert(1)", Label: "<script>"}
	got := forkDashboardRoutes(dashboardOf(config, true), config, containers, true, nil)
	if got[0].ForkRole != "user" || got[0].ForkLabel != "" || got[0].ForkRoleSource != "app" {
		t.Errorf("a hostile setting came through: %+v", got[0].ForkDashboardFields)
	}
}
