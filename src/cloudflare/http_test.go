package cloudflare

// Fork addition: who may call the routes, with what, and what they answer.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

var writeRoutes = map[string]http.HandlerFunc{
	"check":        CheckRoute,
	"preview":      PreviewRoute,
	"takeover":     TakeOverRoute,
	"shield":       ShieldRoute,
	"panel":        PanelRoute,
	"ranges":       RangesRoute,
	"origin-pulls": OriginPullsRoute,
}

func TestRoutesNeedThePermissionToChangeTheConfiguration(t *testing.T) {
	env := setup(t, nil)
	stored := utils.ReadConfigFromFile()
	body := `{"hostname":"radarr.example.org","shielded":true,"token":"` + goodToken + `","zone":"example.org","names":["radarr.example.org"],"action":"trust","kind":"own","mode":"test"}`

	callers := map[string]*http.Request{
		"nobody":                 httptest.NewRequest("POST", "/x", nil),
		"a user":                 admin(httptest.NewRequest("POST", "/x", nil), utils.PERM_LOGIN),
		"a reader of the config": admin(httptest.NewRequest("POST", "/x", nil), utils.PERM_LOGIN, utils.PERM_ADMIN_READ, utils.PERM_CONFIGURATION_READ),
	}
	for name, handler := range writeRoutes {
		for who, caller := range callers {
			req := caller.Clone(caller.Context())
			req.Body = http.NoBody
			req = req.WithContext(caller.Context())
			req.Body = readCloser(body)
			code, _ := do(t, handler, req)
			if code != http.StatusUnauthorized && code != http.StatusForbidden {
				t.Errorf("%s as %s: status %d", name, who, code)
			}
		}
		// without the password confirmation
		req := httptest.NewRequest("POST", "/x", strings.NewReader(body))
		req = admin(req)
		utils.GetAuthContext(req).IsSudoed = false
		if code, _ := do(t, handler, req); code != http.StatusUnauthorized {
			t.Errorf("%s without the password confirmation: status %d", name, code)
		}
		// state is never changed by a GET
		for _, method := range []string{"GET", "PUT", "DELETE"} {
			if code, _ := do(t, handler, admin(httptest.NewRequest(method, "/x", strings.NewReader(body)))); code != http.StatusMethodNotAllowed {
				t.Errorf("%s with %s: status %d", name, method, code)
			}
		}
	}
	if calls := env.fake.Calls(false); len(calls) != 0 {
		t.Errorf("a refused request reached Cloudflare: %+v", calls)
	}
	after := utils.ReadConfigFromFile()
	if len(after.HTTPConfig.Cloudflare.Shielded) != 0 || after.HTTPConfig.Cloudflare.TrustRanges || after.HTTPConfig.Hostname != stored.HTTPConfig.Hostname ||
		after.HTTPConfig.Cloudflare.OriginPulls.Mode != "" || after.HTTPConfig.Cloudflare.OriginPulls.Kind != "" || len(after.HTTPConfig.Cloudflare.OriginPulls.Own) != 0 {
		t.Errorf("a refused request changed the configuration: %+v", after.HTTPConfig.Cloudflare)
	}

	// the status: readers of the configuration, nobody else, GET only
	if code, _ := do(t, StatusRoute, httptest.NewRequest("GET", "/x", nil)); code != http.StatusUnauthorized {
		t.Errorf("status as nobody: %d", code)
	}
	if code, _ := do(t, StatusRoute, admin(httptest.NewRequest("GET", "/x", nil), utils.PERM_LOGIN)); code != http.StatusUnauthorized {
		t.Errorf("status as a user: %d", code)
	}
	if code, _ := do(t, StatusRoute, admin(httptest.NewRequest("POST", "/x", nil))); code != http.StatusMethodNotAllowed {
		t.Errorf("status with POST: %d", code)
	}
}

func readCloser(text string) *nopCloser { return &nopCloser{strings.NewReader(text)} }

type nopCloser struct{ *strings.Reader }

func (nopCloser) Close() error { return nil }

func TestBodiesAreBounded(t *testing.T) {
	env := setup(t, nil)
	limits := map[string]int{"check": maxCheckBody, "preview": maxPreviewBody, "takeover": maxPreviewBody, "shield": maxSmallBody, "panel": maxSmallBody, "ranges": maxSmallBody, "origin-pulls": maxPullsBody}
	for name, handler := range writeRoutes {
		large := `{"token":"` + strings.Repeat("a", limits[name]) + `"}`
		code, _ := do(t, handler, admin(httptest.NewRequest("POST", "/x", strings.NewReader(large))))
		if code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s with a body over its limit: status %d", name, code)
		}
		for _, text := range []string{"", "{", "[]", `"text"`, `{"extra":1}`} {
			if code, _ := do(t, handler, admin(httptest.NewRequest("POST", "/x", strings.NewReader(text)))); code != http.StatusBadRequest {
				t.Errorf("%s with the body %q: status %d", name, text, code)
			}
		}
	}
	if calls := env.fake.Calls(false); len(calls) != 0 {
		t.Errorf("a refused request reached Cloudflare: %+v", calls)
	}
	for _, host := range []string{"", "radarr", "radarr.example.org:2053", "https://radarr.example.org", "radarr.example.org/x", "192.168.1.10", "::1",
		"-a.example.org", strings.Repeat("a", 64) + ".example.org", strings.Repeat("a.", 130) + "org", "rad arr.example.org", "radarr.example.org\nx"} {
		if code, _ := post(t, ShieldRoute, map[string]interface{}{"hostname": host, "shielded": true}); code != http.StatusBadRequest {
			t.Errorf("shield for %q: status %d", host, code)
		}
		if code, _ := post(t, PanelRoute, map[string]interface{}{"hostname": host}); code != http.StatusBadRequest {
			t.Errorf("panel name %q: status %d", host, code)
		}
	}
}

func TestPanelGetsANameAndKeepsItsLocalAddress(t *testing.T) {
	restarts := 0
	setup(t, func(config *utils.Config) { config.HTTPConfig.AllowHTTPLocalIPAccess = false })
	previousMetrics := utils.PushShieldMetrics
	utils.PushShieldMetrics = func(string) {}
	defer func() { utils.PushShieldMetrics = previousMetrics }()
	restartServer = func() { restarts++ }

	for host, why := range map[string]string{
		"cosmos.example.com":  "outside the domains",
		"radarr.example.org":  "an application's address",
		"RADARR.example.org.": "the same, written differently",
	} {
		if code, _ := post(t, PanelRoute, map[string]string{"hostname": host}); code != http.StatusBadRequest {
			t.Errorf("%s (%s): status %d", host, why, code)
		}
	}
	if restarts != 0 || utils.ReadConfigFromFile().HTTPConfig.Hostname != "192.168.1.10" {
		t.Fatal("a refused name changed the panel's address")
	}

	code, answer := post(t, PanelRoute, map[string]string{"hostname": "Cosmos.Example.org"})
	if code != 200 || data(answer)["hostname"] != "cosmos.example.org" || data(answer)["changed"] != true {
		t.Fatalf("naming the panel: %d %v", code, answer)
	}
	stored := utils.ReadConfigFromFile()
	if stored.HTTPConfig.Hostname != "cosmos.example.org" {
		t.Errorf("hostname: %q", stored.HTTPConfig.Hostname)
	}
	if !stored.HTTPConfig.AllowHTTPLocalIPAccess {
		t.Error("the panel no longer answers at the server's local address")
	}
	if restarts != 1 {
		t.Errorf("the server reloaded %d times", restarts)
	}

	// the hostname guard lets the local address through, and the new name, and nothing else
	guarded := utils.EnsureHostnameCosmosAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for host, want := range map[string]int{"192.168.1.10:2053": 204, "cosmos.example.org:2053": 204, "radarr.example.org:2053": 400, "203.0.113.10:2053": 400} {
		req := httptest.NewRequest("GET", "https://"+host+"/cosmos/api/config", nil)
		recorder := httptest.NewRecorder()
		guarded.ServeHTTP(recorder, req)
		if recorder.Code != want {
			t.Errorf("the panel's API asked as %s: status %d, want %d", host, recorder.Code, want)
		}
	}

	// the same name again changes nothing and does not reload
	if _, answer := post(t, PanelRoute, map[string]string{"hostname": "cosmos.example.org"}); data(answer)["changed"] != false || restarts != 1 {
		t.Errorf("naming the panel twice: %v, %d reloads", answer, restarts)
	}
}

func TestRangesAreRefreshedFromCloudflareOnly(t *testing.T) {
	env := setup(t, nil)

	status := GetStatus(true)
	if !status.Ranges.Builtin || status.Ranges.Date != utils.CloudflareBuiltinDate || status.Ranges.Count != 22 || status.Ranges.Trusted {
		t.Fatalf("ranges at the start: %+v", status.Ranges)
	}

	env.fake.SetRanges([]string{"173.245.48.0/20", "104.16.0.0/13", "198.41.128.0/17"}, []string{"2606:4700::/32"})
	code, answer := post(t, RangesRoute, map[string]string{"action": "refresh"})
	if code != 200 || data(answer)["builtin"] != false || data(answer)["count"] != float64(4) {
		t.Fatalf("refresh: %d %v", code, answer)
	}
	if !utils.IsCloudflareIP("198.41.200.1") || utils.IsCloudflareIP("141.101.64.1") {
		t.Error("the fetched list is not the one in force")
	}

	// a list that is not a list of public networks is not taken
	for _, bad := range [][2][]string{
		{{"0.0.0.0/0"}, {"2606:4700::/32"}},
		{{"10.0.0.0/8"}, {"2606:4700::/32"}},
		{{"173.245.48.0/20", "not a range"}, {"2606:4700::/32"}},
		{{"173.245.48.0/20"}, {"::/0"}},
		{{"173.245.48.0/20"}, {"fd00::/8"}},
		{{"173.245.48.1/20"}, {"2606:4700::/32"}}, // host bits set
		{{}, {"2606:4700::/32"}},
		{{"2606:4700::/32"}, {"173.245.48.0/20"}}, // the families swapped
	} {
		env.fake.SetRanges(bad[0], bad[1])
		if code, _ := post(t, RangesRoute, map[string]string{"action": "refresh"}); code != http.StatusBadGateway {
			t.Errorf("the list %v was accepted: status %d", bad, code)
		}
		if !utils.IsCloudflareIP("198.41.200.1") || utils.IsCloudflareIP("10.1.2.3") || utils.IsCloudflareIP("8.8.8.8") {
			t.Errorf("after the refused list %v the ranges in force changed", bad)
		}
	}

	env.fake.SetDown(true)
	if code, answer := post(t, RangesRoute, map[string]string{"action": "refresh"}); code != http.StatusBadGateway || answer["reason"] != "server" {
		t.Errorf("refresh with Cloudflare down: %d %v", code, answer)
	}
	if code, _ := post(t, RangesRoute, map[string]string{"action": "anything"}); code != http.StatusBadRequest {
		t.Errorf("unknown action: %d", code)
	}
}

func TestStatusListsHostsAndHidesAddressesFromNonAdmins(t *testing.T) {
	setup(t, func(config *utils.Config) { config.HTTPConfig.Hostname = "cosmos.example.org" })
	shield(t, "radarr.example.org", true)
	pass(t)

	code, answer := do(t, StatusRoute, admin(httptest.NewRequest("GET", "/x", nil)))
	if code != 200 {
		t.Fatalf("status: %d", code)
	}
	raw, _ := json.Marshal(data(answer))
	var status Status
	json.Unmarshal(raw, &status)

	if len(status.Zones) != 1 || !status.Zones[0].HasToken || status.Zones[0].Name != "example.org" {
		t.Errorf("zones: %+v", status.Zones)
	}
	if status.PublicPort != 2053 || !status.PortOK || status.Address != testAddress || len(status.Ranges.Ranges) != 22 {
		t.Errorf("port, address, ranges: %+v", status)
	}
	hosts := map[string]HostState{}
	for _, host := range status.Hosts {
		hosts[host.Host] = host
	}
	if len(hosts) != 3 {
		t.Fatalf("hosts: %+v", status.Hosts)
	}
	if radarr := hosts["radarr.example.org"]; !radarr.Shielded || radarr.Record == nil || radarr.Record.State != RecordOK || !radarr.Record.Proxied ||
		radarr.URL != "https://radarr.example.org:2053" {
		t.Errorf("radarr: %+v %+v", radarr, radarr.Record)
	}
	if jellyfin := hosts["jellyfin.example.org"]; jellyfin.Shielded || jellyfin.CannotShield != "" || jellyfin.Record.Proxied {
		t.Errorf("jellyfin: %+v", jellyfin)
	}
	if panel := hosts["cosmos.example.org"]; !panel.Panel || panel.Record.State != RecordOK {
		t.Errorf("the panel: %+v %+v", panel, panel.Record)
	}

	// a reader of the configuration who is not an admin: no addresses
	reader := admin(httptest.NewRequest("GET", "/x", nil), utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ)
	recorder := httptest.NewRecorder()
	StatusRoute(recorder, reader)
	if recorder.Code != 200 || bytes.Contains(recorder.Body.Bytes(), []byte(testAddress)) || bytes.Contains(recorder.Body.Bytes(), []byte("173.245.48.0")) {
		t.Errorf("status for a non-admin: %d %s", recorder.Code, recorder.Body.String())
	}
}
