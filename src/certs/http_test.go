package certs

// Fork addition: who may call the routes, with what, and what they answer.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

var writeRoutes = map[string]http.HandlerFunc{
	"renew":      RenewRoute,
	"domain":     DomainRoute,
	"delegation": DelegationRoute,
	"settings":   SettingsRoute,
}

var readRoutes = map[string]http.HandlerFunc{
	"status":    StatusRoute,
	"providers": ProvidersRoute,
}

func TestRoutesNeedTheirPermission(t *testing.T) {
	setup(t, nil)
	renewed := 0
	RenewZone = func(string) { renewed++ }
	asked := 0
	resolver = countingResolver{count: &asked}
	stored := utils.ReadConfigFromFile()
	body := `{"domain":"example.org","via":"example.org","httpChallenge":"no"}`

	callers := map[string]func() *http.Request{
		"nobody": func() *http.Request { return httptest.NewRequest("POST", "/x", strings.NewReader(body)) },
		"a user": func() *http.Request {
			return admin(httptest.NewRequest("POST", "/x", strings.NewReader(body)), utils.PERM_LOGIN)
		},
		"a reader of the configuration": func() *http.Request {
			return admin(httptest.NewRequest("POST", "/x", strings.NewReader(body)), utils.PERM_LOGIN, utils.PERM_ADMIN_READ, utils.PERM_CONFIGURATION_READ)
		},
	}
	for name, handler := range writeRoutes {
		for who, caller := range callers {
			if code, _ := do(t, handler, caller()); code != http.StatusUnauthorized && code != http.StatusForbidden {
				t.Errorf("%s as %s: status %d", name, who, code)
			}
		}
		// without the password confirmation
		req := admin(httptest.NewRequest("POST", "/x", strings.NewReader(body)))
		utils.GetAuthContext(req).IsSudoed = false
		if code, _ := do(t, handler, req); code != http.StatusUnauthorized {
			t.Errorf("%s without the password confirmation: status %d", name, code)
		}
		// state is never changed by a GET
		for _, method := range []string{"GET", "PUT", "DELETE", "PATCH"} {
			if code, _ := do(t, handler, admin(httptest.NewRequest(method, "/x", strings.NewReader(body)))); code != http.StatusMethodNotAllowed {
				t.Errorf("%s with %s: status %d", name, method, code)
			}
		}
	}
	for name, handler := range readRoutes {
		if code, _ := do(t, handler, httptest.NewRequest("GET", "/x", nil)); code != http.StatusUnauthorized && code != http.StatusForbidden {
			t.Errorf("%s as nobody: status %d", name, code)
		}
		if code, _ := do(t, handler, admin(httptest.NewRequest("GET", "/x", nil), utils.PERM_LOGIN)); code != http.StatusUnauthorized && code != http.StatusForbidden {
			t.Errorf("%s as a user: status %d", name, code)
		}
		if code, _ := do(t, handler, admin(httptest.NewRequest("GET", "/x", nil), utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ)); code != http.StatusOK {
			t.Errorf("%s as a reader of the configuration: status %d", name, code)
		}
		if code, _ := do(t, handler, admin(httptest.NewRequest("POST", "/x", nil))); code != http.StatusMethodNotAllowed {
			t.Errorf("%s with POST: status %d", name, code)
		}
	}
	time.Sleep(20 * time.Millisecond) // an order would be started in the background
	if renewed != 0 || asked != 0 {
		t.Errorf("refused calls started %d orders and %d lookups", renewed, asked)
	}
	if after := utils.ReadConfigFromFile(); after.HTTPConfig.Certificates != stored.HTTPConfig.Certificates {
		t.Error("a refused call changed the settings")
	}
}

type countingResolver struct{ count *int }

func (r countingResolver) CNAME(_ context.Context, _ string) (string, error) {
	*r.count++
	return "", nil
}
func (r countingResolver) NS(_ context.Context, _ string) ([]string, error) {
	*r.count++
	return nil, nil
}

func TestBodiesAreBoundedAndChecked(t *testing.T) {
	setup(t, nil)
	for name, handler := range writeRoutes {
		big := `{"domain":"` + strings.Repeat("a", 2000) + `.example.org"}`
		if code, _ := do(t, handler, admin(httptest.NewRequest("POST", "/x", strings.NewReader(big)))); code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s with a body of 2 KB: status %d", name, code)
		}
		for _, bad := range []string{`{"domain":"example.org","more":1}`, `not json`, `[]`, ``} {
			if code, _ := do(t, handler, admin(httptest.NewRequest("POST", "/x", strings.NewReader(bad)))); code != http.StatusBadRequest {
				t.Errorf("%s with %q: status %d", name, bad, code)
			}
		}
	}
	for _, name := range []string{"", "localhost", "192.168.1.10", "*.example.org", "example.org:2053", "exa mple.org", "example.org/x", "-bad-.example.org", "http://example.org", strings.Repeat("a", 64) + ".example.org"} {
		for route, handler := range map[string]http.HandlerFunc{"renew": RenewRoute, "domain": DomainRoute, "delegation": DelegationRoute} {
			code, _ := post(t, handler, map[string]string{"domain": name, "via": "example.org"})
			if code != http.StatusBadRequest {
				t.Errorf("%s with the name %q: status %d", route, name, code)
			}
		}
	}
	// a name under a made-up top-level domain is not looked up
	if code, _ := post(t, DomainRoute, map[string]string{"domain": "nas.lan"}); code != http.StatusBadRequest {
		t.Errorf("a name no public CA certifies: status %d", code)
	}
}

func TestRenewNow(t *testing.T) {
	env := setup(t, func(config *utils.Config) {
		config.HTTPConfig.DNSZones = append(config.HTTPConfig.DNSZones, utils.DNSZoneConfig{Zone: "self.example.com", HTTPSCertificateMode: "SELFSIGNED"})
		config.HTTPConfig.ProxyConfig.Routes = append(config.HTTPConfig.ProxyConfig.Routes,
			utils.ProxyRouteConfig{Name: "lone", UseHost: true, Host: "lone.example.info", Mode: "PROXY", Target: "http://127.0.0.1:1"})
	})
	started := make(chan string, 10)
	RenewZone = func(zone string) { started <- zone }
	wait := func() string {
		select {
		case zone := <-started:
			return zone
		case <-time.After(2 * time.Second):
			return ""
		}
	}

	if code, _ := post(t, RenewRoute, map[string]string{"domain": "unknown.example"}); code != http.StatusNotFound {
		t.Errorf("a domain this server does not have: status %d", code)
	}
	for domain, reason := range map[string]string{"self.example.com": "notOrdered", "lone.example.info": "noHTTPChallenge"} {
		code, answer := post(t, RenewRoute, map[string]string{"domain": domain})
		if code != http.StatusConflict || answer["reason"] != reason || answer["message"] == "" {
			t.Errorf("%s: status %d, %v", domain, code, answer)
		}
	}

	// in the back-off after a failure: the order is started and may go
	fail("zone:example.org", env.now, FailDNSCredentials, 0)
	code, answer := post(t, RenewRoute, map[string]string{"domain": " Example.ORG. "})
	if code != http.StatusOK || data(answer)["started"] != true || wait() != "example.org" {
		t.Fatalf("renew: status %d, %v", code, answer)
	}
	if !store.isForced("zone:example.org") {
		t.Error("the certificate is not marked as asked for")
	}
	if ok, _, _ := store.mayOrder("zone:example.org", pair, false, env.now); !ok {
		t.Error("the back-off still holds the order")
	}

	// the CA said to wait: refused, and nothing is started
	fail("zone:example.org", env.now, FailRateLimited, 5*time.Hour)
	code, answer = post(t, RenewRoute, map[string]string{"domain": "example.org"})
	if code != http.StatusConflict || answer["reason"] != waitRateLimited {
		t.Errorf("rate limited: status %d, %v", code, answer)
	}
	// while an order runs
	env.now = env.now.Add(6 * time.Hour)
	running.Store("zone:example.org", env.now)
	code, answer = post(t, RenewRoute, map[string]string{"domain": "example.org"})
	running.Delete("zone:example.org")
	if code != http.StatusConflict || answer["reason"] != "running" {
		t.Errorf("during an order: status %d, %v", code, answer)
	}

	// four in an hour, then no more
	for i := 0; i < 4; i++ {
		if code, _ := post(t, RenewRoute, map[string]string{"domain": "example.org"}); code != http.StatusOK {
			t.Fatalf("renew %d: status %d", i+1, code)
		}
		wait()
	}
	if code, _ := post(t, RenewRoute, map[string]string{"domain": "example.org"}); code != http.StatusTooManyRequests {
		t.Errorf("the fifth \"Renew now\" in an hour: status %d", code)
	}
	env.now = env.now.Add(61 * time.Minute)
	if code, _ := post(t, RenewRoute, map[string]string{"domain": "example.org"}); code != http.StatusOK {
		t.Errorf("an hour later: status %d", code)
	}
	if wait() == "" {
		t.Error("no order was started")
	}
	select {
	case zone := <-started:
		t.Errorf("an order too many was started for %s", zone)
	default:
	}
}

func TestWhatIsFoundOutAboutADomain(t *testing.T) {
	setup(t, func(config *utils.Config) {
		config.HTTPConfig.DNSZones = append(config.HTTPConfig.DNSZones, utils.DNSZoneConfig{Zone: "example.net", HTTPSCertificateMode: "LETSENCRYPT", ChallengeVia: "example.org"})
	})
	resolver = tableResolver{ns: map[string][]string{
		"example.com":  {"hydrogen.ns.hetzner.com", "helium.ns.hetzner.de"},
		"example.info": {"ns1.example.info"},
		"example.net":  {"ns1.example.net"},
		"cf.example":   {"ada.ns.cloudflare.com"},
		"many.example": {"n1.x", "n2.x", "n3.x", "n4.x", "n5.x", "n6.x", "n7.x", "n8.x", "n9.x", "n10.x"},
	}, fail: map[string]bool{"down.example.org": true, "example.org": true}}
	// the made-up top-level names above are refused; use real ones for the lookups
	code, answer := post(t, DomainRoute, map[string]string{"domain": "Example.COM"})
	info := data(answer)
	if code != http.StatusOK || info["provider"] != "hetzner" || info["providerName"] != "Hetzner" || info["configured"] != false || info["nameserversOf"] != "example.com" {
		t.Errorf("a domain at a known provider: %d %v", code, info)
	}
	offers, _ := info["canAnswer"].([]interface{})
	if len(offers) != 1 {
		t.Fatalf("the domains that can answer for it: %v", offers)
	}
	offer := offers[0].(map[string]interface{})
	if offer["record"] != "_acme-challenge.example.com" || offer["target"] != "example-com._acme.example.org" {
		t.Errorf("the record proposed: %v", offer)
	}

	// a name inside a domain: the name servers of the domain above
	_, answer = post(t, DomainRoute, map[string]string{"domain": "home.sub.example.info"})
	if info = data(answer); info["nameserversOf"] != "example.info" || info["provider"] != "" || info["lookupFailed"] != false {
		t.Errorf("a name inside a domain: %v", info)
	}
	// already a domain of this server
	_, answer = post(t, DomainRoute, map[string]string{"domain": "example.net"})
	if info = data(answer); info["configured"] != true {
		t.Errorf("a domain this server has: %v", info)
	}
	// a name under the domain that would answer is not offered a delegation to it
	_, answer = post(t, DomainRoute, map[string]string{"domain": "down.example.org"})
	info = data(answer)
	if offers, _ := info["canAnswer"].([]interface{}); len(offers) != 0 || info["lookupFailed"] != true {
		t.Errorf("a name under example.org, lookups failing: %v", info)
	}

	// thirty lookups an hour
	lookupLimit.calls = nil
	for i := 0; i < 30; i++ {
		if code, _ := post(t, DomainRoute, map[string]string{"domain": "example.com"}); code != http.StatusOK {
			t.Fatalf("lookup %d: status %d", i+1, code)
		}
	}
	if code, _ := post(t, DomainRoute, map[string]string{"domain": "example.com"}); code != http.StatusTooManyRequests {
		t.Errorf("the thirty-first lookup: status %d", code)
	}
	if code, _ := post(t, DelegationRoute, map[string]string{"domain": "example.com", "via": "example.org"}); code != http.StatusTooManyRequests {
		t.Errorf("the delegation check shares the bound: status %d", code)
	}
}

func TestTheDelegationRoute(t *testing.T) {
	setup(t, func(config *utils.Config) {
		config.HTTPConfig.DNSZones = append(config.HTTPConfig.DNSZones,
			utils.DNSZoneConfig{Zone: "example.net", HTTPSCertificateMode: "LETSENCRYPT", ChallengeVia: "example.org"},
			utils.DNSZoneConfig{Zone: "self.example.com", HTTPSCertificateMode: "SELFSIGNED", DNSChallengeProvider: "cloudflare"})
	})
	resolver = tableResolver{cname: map[string]string{"_acme-challenge.example.com": "example-com._acme.example.org"}}

	code, answer := post(t, DelegationRoute, map[string]string{"domain": "example.com", "via": "example.org"})
	if code != http.StatusOK || data(answer)["state"] != DelegationOK || data(answer)["found"] != "example-com._acme.example.org" {
		t.Errorf("the record is there: %d %v", code, answer)
	}
	code, answer = post(t, DelegationRoute, map[string]string{"domain": "example.info", "via": "example.org"})
	if code != http.StatusOK || data(answer)["state"] != DelegationMissing || data(answer)["target"] != "example-info._acme.example.org" {
		t.Errorf("the record is not there: %d %v", code, answer)
	}
	// only a domain of this server that can answer may be named
	for _, via := range []string{"unknown.example", "example.net", "self.example.com", "example.com"} {
		if code, _ := post(t, DelegationRoute, map[string]string{"domain": "example.com", "via": via}); code != http.StatusBadRequest {
			t.Errorf("via %s: status %d", via, code)
		}
	}
	if code, _ := post(t, DelegationRoute, map[string]string{"domain": "app.example.org", "via": "example.org"}); code != http.StatusBadRequest {
		t.Errorf("a name under the answering domain itself: status %d", code)
	}
}

func TestTheSetting(t *testing.T) {
	setup(t, nil)
	restarts := 0
	restartServer = func() { restarts++ }

	for _, bad := range []interface{}{"maybe", "YES", " no", 1, true} {
		if code, _ := post(t, SettingsRoute, map[string]interface{}{"httpChallenge": bad}); code != http.StatusBadRequest {
			t.Errorf("the value %v: status %d", bad, code)
		}
	}
	if code, _ := post(t, SettingsRoute, map[string]interface{}{}); code != http.StatusBadRequest {
		t.Errorf("nothing to change: status %d", code)
	}
	if restarts != 0 || utils.ReadConfigFromFile().HTTPConfig.Certificates.HTTPChallenge != "" {
		t.Fatal("a refused value was stored")
	}

	code, answer := post(t, SettingsRoute, map[string]string{"httpChallenge": "yes"})
	if code != http.StatusOK || data(answer)["changed"] != true || restarts != 1 || utils.ReadConfigFromFile().HTTPConfig.Certificates.HTTPChallenge != "yes" {
		t.Errorf("yes: %d %v, restarts %d", code, answer, restarts)
	}
	if GetStatus().Challenges.HTTP != "possible" {
		t.Error("the status does not follow the setting")
	}
	// the same value again changes nothing and restarts nothing
	if _, answer := post(t, SettingsRoute, map[string]string{"httpChallenge": "yes"}); data(answer)["changed"] != false || restarts != 1 {
		t.Errorf("the same value again: %v, restarts %d", answer, restarts)
	}
	post(t, SettingsRoute, map[string]string{"httpChallenge": ""})
	if utils.ReadConfigFromFile().HTTPConfig.Certificates.HTTPChallenge != "" || GetStatus().Challenges.Reason != "ports" {
		t.Error("back to working it out")
	}
}

func TestTheStatusAndTheCatalogueCarryNoSecret(t *testing.T) {
	env := setup(t, nil)
	env.zoneCert(t, env.ca, "example.org", pair, t0.Add(-day), t0.Add(89*day), "app.example.org")
	store.started("zone:example.org", env.now)
	store.finished("zone:example.org", env.now, Current(), pair, "", classify(stageOrder, errUsing(testToken), []string{testToken}))

	recorder := httptest.NewRecorder()
	StatusRoute(recorder, admin(httptest.NewRequest("GET", "/x", nil)))
	body := recorder.Body.String()
	for _, secret := range []string{testToken, "PRIVATE KEY", "BEGIN CERTIFICATE"} {
		if strings.Contains(body, secret) {
			t.Errorf("the status carries %q", secret)
		}
	}
	if !strings.Contains(body, `"code":"dnsCredentials"`) || !strings.Contains(body, `"domain":"example.org"`) {
		t.Errorf("the status: %s", body)
	}
	_, catalogue := do(t, ProvidersRoute, admin(httptest.NewRequest("GET", "/x", nil)))
	entry, _ := data(catalogue)["hetzner"].(map[string]interface{})
	credentials, _ := entry["credentials"].([]interface{})
	if entry["name"] != "Hetzner" || len(credentials) != 1 || credentials[0].(map[string]interface{})["name"] != "HETZNER_API_TOKEN" {
		t.Errorf("the catalogue's entry for Hetzner: %v", entry)
	}
}

func errUsing(token string) error {
	return errors.New("dns01: error presenting token (example.org): cloudflare: failed to find zone example.org.: [status code 403] 9109: Invalid access token for Bearer " + token)
}
