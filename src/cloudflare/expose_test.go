package cloudflare

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/azukaar/cosmos-server/src/upnp"
	"github.com/azukaar/cosmos-server/src/utils"
)

var fullAdmin = []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN, utils.PERM_ADMIN_READ, utils.PERM_CONFIGURATION_READ, utils.PERM_CONFIGURATION}

type exposeWorld struct {
	*testEnv
	mu     sync.Mutex
	events []utils.SecurityEvent
	router [][]string
	reload int
}

// exposeSetup: the stand-in of setup, plus sonarr (a web interface with a
// local URL only) and minecraft (a game server, no URL), both running and
// publishing their ports. The router and the HTTP server are never touched.
func exposeSetup(t *testing.T, change func(config *utils.Config)) *exposeWorld {
	t.Helper()
	env := setup(t, func(config *utils.Config) {
		config.HTTPConfig.ProxyConfig.Routes = append(config.HTTPConfig.ProxyConfig.Routes,
			utils.ProxyRouteConfig{Name: "sonarr", UseHost: true, Host: "sonarr.local", Mode: "SERVAPP", Target: "http://sonarr:8989"})
		if change != nil {
			change(config)
		}
	})
	w := &exposeWorld{testEnv: env}
	previous := listContainers
	listContainers = func() ([]upnp.Container, error) {
		return []upnp.Container{
			{Name: "sonarr", Running: true, Ports: []upnp.Published{{ContainerPort: 8989, Protocol: "tcp", HostPort: 8989}}},
			{Name: "minecraft", Running: true, Ports: []upnp.Published{
				{ContainerPort: 25565, Protocol: "tcp", HostPort: 25565},
				{ContainerPort: 25565, Protocol: "udp", HostPort: 25565},
				{ContainerPort: 25575, Protocol: "tcp", HostPort: 25575},
			}},
			{Name: "db", Running: true, Ports: []upnp.Published{{ContainerPort: 5432, Protocol: "tcp", HostPort: 5432}}},
		}, nil
	}
	routerChanged = func(keys []string) { w.mu.Lock(); w.router = append(w.router, keys); w.mu.Unlock() }
	reloadRoutes = func() { w.mu.Lock(); w.reload++; w.mu.Unlock() }
	utils.SetSecurityRecorder(func(event utils.SecurityEvent) { w.mu.Lock(); w.events = append(w.events, event); w.mu.Unlock() }, nil)
	t.Cleanup(func() {
		listContainers = previous
		utils.SetSecurityRecorder(nil, nil)
	})
	return w
}

func exposeCall(t *testing.T, body interface{}, permissions ...utils.Permission) (int, map[string]interface{}) {
	t.Helper()
	raw, _ := json.Marshal(body)
	if len(permissions) == 0 {
		permissions = fullAdmin
	}
	return do(t, ExposeRoute, admin(httptest.NewRequest("POST", "/cosmos/api/expose", bytes.NewReader(raw)), permissions...))
}

func routeNamed(name string) *utils.ProxyRouteConfig {
	for _, route := range utils.GetMainConfig().HTTPConfig.ProxyConfig.Routes {
		if route.Name == name {
			r := route
			return &r
		}
	}
	return nil
}

func TestExposeAWebInterfaceThroughCloudflare(t *testing.T) {
	w := exposeSetup(t, nil)
	code, answer := exposeCall(t, map[string]interface{}{
		"action": "expose", "container": "sonarr", "confirm": "sonarr",
		"web": []map[string]string{{"route": "sonarr", "hostname": "Sonarr.Example.org", "through": "cloudflare", "access": "users"}},
	})
	if code != http.StatusOK {
		t.Fatalf("expose: %d %v", code, answer)
	}
	route := routeNamed("sonarr-example.org")
	if route == nil || route.Host != "sonarr.example.org" || !route.AuthEnabled || route.AdminOnly || route.Target != "http://sonarr:8989" || !route.UseHost {
		t.Fatalf("the URL copy: %+v", route)
	}
	cf := utils.GetMainConfig().HTTPConfig.Cloudflare
	if !utils.ShieldedIn(cf.Shielded, "sonarr.example.org") || !cf.TrustRanges {
		t.Errorf("not shielded: %+v", cf)
	}
	if w.reload != 1 {
		t.Errorf("the HTTP server was reloaded %d times", w.reload)
	}
	pass(t)
	if records := w.find("sonarr.example.org", "A"); len(records) != 1 || !records[0].Proxied || records[0].Content != testAddress {
		t.Errorf("record: %+v", records)
	}
	if len(w.events) != 1 || w.events[0].Kind != utils.SecExposeOpened || w.events[0].Target != "sonarr" || w.events[0].Details["web"] != "sonarr.example.org (cloudflare)" {
		t.Errorf("events: %+v", w.events)
	}

	// the same again, directly and for administrators: the copy is changed, not doubled
	code, answer = exposeCall(t, map[string]interface{}{
		"action": "expose", "container": "sonarr", "confirm": "sonarr",
		"web": []map[string]string{{"route": "sonarr", "hostname": "sonarr.example.org", "through": "direct", "access": "admins"}},
	})
	if code != http.StatusOK || routeNamed("sonarr-example.org-2") != nil || !routeNamed("sonarr-example.org").AdminOnly {
		t.Fatalf("again: %d %v", code, answer)
	}
	if utils.ShieldedIn(utils.GetMainConfig().HTTPConfig.Cloudflare.Shielded, "sonarr.example.org") {
		t.Error("still shielded")
	}
	pass(t)
	if records := w.find("sonarr.example.org", "A"); len(records) != 1 || records[0].Proxied {
		t.Errorf("record after direct: %+v", records)
	}
}

func TestExposeAGameServerPortUnderADirectName(t *testing.T) {
	w := exposeSetup(t, nil)
	code, answer := exposeCall(t, map[string]interface{}{
		"action": "expose", "container": "minecraft", "confirm": "minecraft",
		"ports": []map[string]interface{}{
			{"port": 25565, "protocol": "tcp", "hostname": "minecraft.example.org"},
			{"port": 25565, "protocol": "udp", "hostname": "minecraft.example.org"},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("expose: %d %v", code, answer)
	}
	choices := utils.GetMainConfig().HTTPConfig.UPnP.AppPorts
	if len(choices) != 2 || choices[0].Hostname != "minecraft.example.org" || choices[1].Protocol != "udp" {
		t.Fatalf("choices: %+v", choices)
	}
	if len(w.router) != 1 || len(w.router[0]) != 2 {
		t.Errorf("router told: %v", w.router)
	}
	if w.reload != 0 {
		t.Error("no URL changed, yet the HTTP server was reloaded")
	}
	pass(t)
	records := w.find("minecraft.example.org", "A")
	if len(records) != 1 || records[0].Proxied || records[0].Content != testAddress {
		t.Fatalf("the direct name: %+v", records)
	}
	if markers := w.find("_cosmos-owner.minecraft.example.org", "TXT"); len(markers) != 1 {
		t.Errorf("no owner marker: %+v", markers)
	}
	// shown in the overview with its ports, no link, no switch
	status := GetStatus(true)
	found := false
	for _, host := range status.Hosts {
		if host.Host == "minecraft.example.org" {
			found = true
			if strings.Join(host.DirectPorts, ",") != "25565/tcp,25565/udp" || host.URL != "" || host.CannotShield != "direct_port" {
				t.Errorf("overview: %+v", host)
			}
		}
	}
	if !found {
		t.Error("not in the overview")
	}

	// such a name can never be shielded
	if code, answer := post(t, ShieldRoute, map[string]interface{}{"hostname": "minecraft.example.org", "shielded": true}); code != http.StatusBadRequest || answer["reason"] != "direct_port" {
		t.Errorf("shield of a port name: %d %v", code, answer)
	}

	// withdrawn: the choice goes, and with the next pass the record and its marker
	code, answer = exposeCall(t, map[string]interface{}{
		"action": "withdraw", "container": "minecraft",
		"ports": []map[string]interface{}{{"port": 25565, "protocol": "tcp"}, {"port": 25565, "protocol": "udp"}},
	})
	if code != http.StatusOK || len(utils.GetMainConfig().HTTPConfig.UPnP.AppPorts) != 0 {
		t.Fatalf("withdraw: %d %v %+v", code, answer, utils.GetMainConfig().HTTPConfig.UPnP.AppPorts)
	}
	pass(t)
	if records := w.find("minecraft.example.org", "A"); len(records) != 0 {
		t.Errorf("the record stayed: %+v", records)
	}
	if markers := w.find("_cosmos-owner.minecraft.example.org", "TXT"); len(markers) != 0 {
		t.Errorf("the marker stayed: %+v", markers)
	}
	if last := w.events[len(w.events)-1]; last.Kind != utils.SecExposeClosed || last.Details["ports"] != "25565/tcp, 25565/udp" {
		t.Errorf("events: %+v", w.events)
	}
}

func TestAShieldedWebNameAndADirectPortNameLiveSideBySide(t *testing.T) {
	w := exposeSetup(t, func(config *utils.Config) {
		config.HTTPConfig.ProxyConfig.Routes = append(config.HTTPConfig.ProxyConfig.Routes,
			utils.ProxyRouteConfig{Name: "minecraft", UseHost: true, Host: "minecraft.local", Mode: "SERVAPP", Target: "http://minecraft:8123"})
	})
	// the web name through Cloudflare cannot also carry the port
	code, answer := exposeCall(t, map[string]interface{}{
		"action": "expose", "container": "minecraft", "confirm": "minecraft",
		"web":   []map[string]string{{"route": "minecraft", "hostname": "minecraft.example.org", "through": "cloudflare"}},
		"ports": []map[string]interface{}{{"port": 25565, "protocol": "tcp", "hostname": "minecraft.example.org"}},
	})
	if code != http.StatusBadRequest || answer["reason"] != "shielded" {
		t.Fatalf("one name proxied and direct: %d %v", code, answer)
	}
	if routeNamed("minecraft-example.org") != nil || len(utils.GetMainConfig().HTTPConfig.UPnP.AppPorts) != 0 {
		t.Fatal("a refused request changed something")
	}
	// with a name of its own it works
	code, answer = exposeCall(t, map[string]interface{}{
		"action": "expose", "container": "minecraft", "confirm": "minecraft",
		"web":   []map[string]string{{"route": "minecraft", "hostname": "minecraft.example.org", "through": "cloudflare"}},
		"ports": []map[string]interface{}{{"port": 25565, "protocol": "tcp", "hostname": "minecraft-direct.example.org"}},
	})
	if code != http.StatusOK {
		t.Fatalf("expose: %d %v", code, answer)
	}
	pass(t)
	if web := w.find("minecraft.example.org", "A"); len(web) != 1 || !web[0].Proxied {
		t.Errorf("web: %+v", web)
	}
	if direct := w.find("minecraft-direct.example.org", "A"); len(direct) != 1 || direct[0].Proxied {
		t.Errorf("direct: %+v", direct)
	}
	// and the other way round: a port on the shielded name is refused
	code, answer = exposeCall(t, map[string]interface{}{
		"action": "expose", "container": "minecraft", "confirm": "minecraft",
		"ports": []map[string]interface{}{{"port": 25575, "protocol": "tcp", "hostname": "minecraft.example.org", "risky": false}},
	})
	if code != http.StatusBadRequest || answer["reason"] != "shielded" {
		t.Errorf("port on a shielded name: %d %v", code, answer)
	}

	// withdrawing the web name deletes the copy and forgets its shield
	code, answer = exposeCall(t, map[string]interface{}{"action": "withdraw", "container": "minecraft", "web": []map[string]string{{"hostname": "minecraft.example.org"}}})
	if code != http.StatusOK || routeNamed("minecraft-example.org") != nil || w.reload != 2 {
		t.Fatalf("withdraw web: %d %v reloads %d", code, answer, w.reload)
	}
	if utils.ShieldedIn(utils.GetMainConfig().HTTPConfig.Cloudflare.Shielded, "minecraft.example.org") {
		t.Error("the shield of a withdrawn name stayed")
	}
	// the app's last URL is not withdrawn this way
	code, answer = exposeCall(t, map[string]interface{}{"action": "withdraw", "container": "minecraft", "web": []map[string]string{{"hostname": "minecraft.local"}}})
	if code != http.StatusBadRequest {
		t.Errorf("withdraw of the local URL: %d %v", code, answer)
	}
}

func TestExposeRefusals(t *testing.T) {
	exposeSetup(t, func(config *utils.Config) {
		config.HTTPConfig.DNSZones = append(config.HTTPConfig.DNSZones, utils.DNSZoneConfig{Zone: "example.net", HTTPSCertificateMode: "LETSENCRYPT", DNSChallengeProvider: "ovh", ManageRecords: true})
		config.HTTPConfig.Hostname = "cosmos.example.org"
	})
	web := func(host string, through string) map[string]interface{} {
		return map[string]interface{}{"action": "expose", "container": "sonarr", "confirm": "sonarr",
			"web": []map[string]string{{"route": "sonarr", "hostname": host, "through": through}}}
	}
	port := func(container string, number int, protocol string, host string, extra map[string]interface{}) map[string]interface{} {
		p := map[string]interface{}{"port": number, "protocol": protocol, "hostname": host}
		for k, v := range extra {
			p[k] = v
		}
		return map[string]interface{}{"action": "expose", "container": container, "confirm": container, "ports": []interface{}{p}}
	}
	cases := []struct {
		name   string
		body   map[string]interface{}
		status int
		reason string
	}{
		{"the domain's own name", web("example.org", "direct"), 400, "apex"},
		{"the panel's name", web("cosmos.example.org", "direct"), 400, "panel"},
		{"outside every domain", web("sonarr.example.com", "direct"), 400, "not_under_domain"},
		{"another app's address", web("radarr.example.org", "direct"), 400, "taken"},
		{"how it is served", web("sonarr.example.org", "maybe"), 400, "invalid"},
		{"a URL that is not the app's", map[string]interface{}{"action": "expose", "container": "sonarr", "confirm": "sonarr",
			"web": []map[string]string{{"route": "radarr", "hostname": "sonarr.example.org", "through": "direct"}}}, 400, "no_route"},
		{"a port's name outside Cloudflare", port("minecraft", 25565, "tcp", "mc.example.net", nil), 400, "not_managed"},
		{"a port's name on another app's address", port("minecraft", 25565, "tcp", "radarr.example.org", nil), 400, "taken"},
		{"plain HTTP", port("minecraft", 25565, "tcp", "", map[string]interface{}{"scheme": "http"}), 400, "http"},
		{"a database without saying so", port("db", 5432, "tcp", "", nil), 400, "risky"},
		{"a reserved external port", port("minecraft", 25565, "tcp", "", map[string]interface{}{"externalPort": 443}), 400, "router"},
		{"a port the container does not publish", port("minecraft", 9999, "tcp", "", nil), 409, "router"},
	}
	for _, c := range cases {
		code, answer := exposeCall(t, c.body)
		if code != c.status || answer["reason"] != c.reason {
			t.Errorf("%s: %d %v", c.name, code, answer)
		}
	}
	// nothing was changed by any of them
	config := utils.GetMainConfig()
	if len(config.HTTPConfig.UPnP.AppPorts) != 0 || len(config.HTTPConfig.Cloudflare.Shielded) != 0 || len(config.HTTPConfig.ProxyConfig.Routes) != 4 {
		t.Errorf("a refusal changed the configuration: %+v", config.HTTPConfig.UPnP.AppPorts)
	}
	// with the risk said, the database port is accepted
	if code, answer := exposeCall(t, port("db", 5432, "tcp", "", map[string]interface{}{"risky": true})); code != http.StatusOK {
		t.Errorf("risky accepted: %d %v", code, answer)
	}
}

func TestExposeShieldRefusedWhenTheHTTPSPortIsNotCloudflares(t *testing.T) {
	exposeSetup(t, func(config *utils.Config) { config.HTTPConfig.HTTPSPort = "4443" })
	code, answer := exposeCall(t, map[string]interface{}{"action": "expose", "container": "sonarr", "confirm": "sonarr",
		"web": []map[string]string{{"route": "sonarr", "hostname": "sonarr.example.org", "through": "cloudflare"}}})
	if code != http.StatusBadRequest || answer["reason"] != "port" {
		t.Errorf("shield on 4443: %d %v", code, answer)
	}
	if code, answer := exposeCall(t, map[string]interface{}{"action": "expose", "container": "sonarr", "confirm": "sonarr",
		"web": []map[string]string{{"route": "sonarr", "hostname": "sonarr.example.org", "through": "direct"}}}); code != http.StatusOK {
		t.Errorf("direct on 4443: %d %v", code, answer)
	}
}

func TestExposeRouteChecksMethodPermissionAndBounds(t *testing.T) {
	exposeSetup(t, nil)
	body := map[string]interface{}{"action": "expose", "container": "sonarr", "confirm": "sonarr",
		"web": []map[string]string{{"route": "sonarr", "hostname": "sonarr.example.org", "through": "direct"}}}
	// the configuration permission is not enough: it opens the router
	if code, _ := exposeCall(t, body, utils.PERM_LOGIN, utils.PERM_CONFIGURATION, utils.PERM_CONFIGURATION_READ); code != http.StatusForbidden && code != http.StatusUnauthorized {
		t.Errorf("without admin: %d", code)
	}
	if code, _ := do(t, ExposeRoute, admin(httptest.NewRequest("GET", "/cosmos/api/expose", nil), fullAdmin...)); code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", code)
	}
	for name, bad := range map[string]interface{}{
		"no confirmation":   map[string]interface{}{"action": "expose", "container": "sonarr", "web": body["web"]},
		"unknown field":     map[string]interface{}{"action": "expose", "container": "sonarr", "confirm": "sonarr", "web": body["web"], "force": true},
		"bad container":     map[string]interface{}{"action": "expose", "container": "../x", "confirm": "../x", "web": body["web"]},
		"nothing to expose": map[string]interface{}{"action": "expose", "container": "sonarr", "confirm": "sonarr"},
		"unknown action":    map[string]interface{}{"action": "open", "container": "sonarr", "confirm": "sonarr", "web": body["web"]},
		"too many ports":    map[string]interface{}{"action": "expose", "container": "sonarr", "confirm": "sonarr", "ports": make([]map[string]int, 17)},
	} {
		if code, answer := exposeCall(t, bad); code != http.StatusBadRequest {
			t.Errorf("%s: %d %v", name, code, answer)
		}
	}
	big := strings.Repeat("a", maxExposeBody)
	if code, _ := exposeCall(t, map[string]interface{}{"action": "expose", "container": "sonarr", "confirm": big}); code != http.StatusRequestEntityTooLarge {
		t.Errorf("large body: %d", code)
	}
	if len(utils.GetMainConfig().HTTPConfig.ProxyConfig.Routes) != 4 {
		t.Error("a refused request changed the routes")
	}
}

func TestStaleShieldsArePruned(t *testing.T) {
	exposeSetup(t, func(config *utils.Config) {
		config.HTTPConfig.Cloudflare.Shielded = []string{"gone.example.org", "radarr.example.org"}
		config.HTTPConfig.Cloudflare.TakeOver = []string{"gone.example.org"}
	})
	pruneStale()
	cf := utils.GetMainConfig().HTTPConfig.Cloudflare
	if strings.Join(cf.Shielded, ",") != "radarr.example.org" || len(cf.TakeOver) != 0 {
		t.Errorf("after pruning: %+v", cf)
	}
}

func TestRiskyPortsAreTheSameInTheUI(t *testing.T) {
	// client/src/utils/exposure.js keeps the same list for the proposals
	for _, port := range []int{22, 2375, 3306, 5432, 6379, 27017, 9229} {
		if !IsRiskyPort(port) {
			t.Errorf("%d is not risky", port)
		}
	}
	for _, port := range []int{25565, 1883, 8096, 51413, 443} {
		if IsRiskyPort(port) {
			t.Errorf("%d is risky", port)
		}
	}
}
