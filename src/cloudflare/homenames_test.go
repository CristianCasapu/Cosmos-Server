package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/cloudflare/cffake"
	"github.com/azukaar/cosmos-server/src/dnsrecords"
	"github.com/azukaar/cosmos-server/src/utils"
)

const homeLAN = "192.168.1.20"

// homeSetup: setup's stand-in, with the private address the home names point at.
func homeSetup(t *testing.T, change func(config *utils.Config)) (*testEnv, *string) {
	t.Helper()
	env := setup(t, change)
	address := homeLAN
	previous := homeAddress
	homeAddress = func() (string, bool) { return address, address != "" }
	t.Cleanup(func() { homeAddress = previous })
	return env, &address
}

func homeCall(t *testing.T, body interface{}, permissions ...utils.Permission) (int, map[string]interface{}) {
	t.Helper()
	raw, _ := json.Marshal(body)
	return do(t, HomeNamesRoute, admin(httptest.NewRequest("POST", "/cosmos/api/home-names", bytes.NewReader(raw)), permissions...))
}

func TestHomeNamesGetOneDNSOnlyWildcardAtThePrivateAddress(t *testing.T) {
	env, address := homeSetup(t, nil)

	if code, answer := homeCall(t, map[string]interface{}{"zone": "example.org", "label": "home", "enabled": true}); code != 200 {
		t.Fatalf("switching on: %d %v", code, answer)
	}
	if !utils.IsHomeHost("radarr.home.example.org") {
		t.Fatal("not in force after the switch")
	}
	pass(t)

	wildcard := env.find("*.home.example.org", "A")
	if len(wildcard) != 1 || wildcard[0].Content != homeLAN || wildcard[0].Proxied {
		t.Fatalf("wildcard record: %+v", wildcard)
	}
	if markers := env.find("_cosmos-owner._wildcard.home.example.org", "TXT"); len(markers) != 1 {
		t.Errorf("owner marker: %+v", markers)
	}
	// no app's name in DNS, and never the public address under the suffix
	for _, record := range env.fake.Records("example.org") {
		if strings.HasSuffix(record.Name, ".home.example.org") && record.Name != "*.home.example.org" && !strings.HasPrefix(record.Name, "_cosmos-owner") {
			t.Errorf("a record of its own: %+v", record)
		}
		if strings.Contains(record.Name, "home.example.org") && record.Content == testAddress {
			t.Errorf("a home name at the public address: %+v", record)
		}
	}
	// the apps' own records stay
	if len(env.find("radarr.example.org", "A")) != 1 {
		t.Error("the app's public record went")
	}

	// the status says so
	status := GetHomeStatus(true)
	var row HomeZoneStatus
	for _, z := range status.Zones {
		if z.Zone == "example.org" {
			row = z
		}
	}
	if !row.Active || row.Record != "ok" || row.RecordAddress != homeLAN || row.Panel != "cosmos.home.example.org" || status.Address != homeLAN {
		t.Errorf("status: %+v (%s)", row, status.Address)
	}
	if hidden := GetHomeStatus(false); hidden.Address != "" || hidden.Zones[0].RecordAddress != "" {
		t.Error("the private address shown without the admin permission")
	}

	// the address changes (DHCP): followed at the next pass
	*address = "192.168.1.21"
	pass(t)
	if wildcard := env.find("*.home.example.org", "A"); len(wildcard) != 1 || wildcard[0].Content != "192.168.1.21" {
		t.Errorf("after the address change: %+v", wildcard)
	}

	// the address cannot be read (right after a start): the record stays
	*address = ""
	pass(t)
	if wildcard := env.find("*.home.example.org", "A"); len(wildcard) != 1 {
		t.Errorf("an unknown address removed the record: %+v", wildcard)
	}

	// switched off: Cosmos' record and marker go, nothing else
	*address = "192.168.1.21"
	before := len(env.fake.Records("example.org"))
	if code, _ := homeCall(t, map[string]interface{}{"zone": "example.org", "enabled": false}); code != 200 {
		t.Fatalf("switching off: %d", code)
	}
	pass(t)
	if len(env.find("*.home.example.org", "A")) != 0 || len(env.find("_cosmos-owner._wildcard.home.example.org", "TXT")) != 0 {
		t.Error("the wildcard stayed after switching off")
	}
	if after := len(env.fake.Records("example.org")); after != before-2 {
		t.Errorf("switching off changed %d records, want 2", before-after)
	}
	if len(env.find("example.org", "A")) != 1 || len(env.find("mail.example.org", "A")) != 1 {
		t.Error("a record Cosmos did not create was touched")
	}
	if utils.IsHomeHost("radarr.home.example.org") {
		t.Error("still in force after switching off")
	}
}

func TestHomeNamesLeaveAForeignWildcardAlone(t *testing.T) {
	env, _ := homeSetup(t, nil)
	env.fake.AddRecord("example.org", cffake.Record{Type: "A", Name: "*.home.example.org", Content: "10.9.9.9"})
	if code, _ := homeCall(t, map[string]interface{}{"zone": "example.org", "label": "home", "enabled": true}); code != 200 {
		t.Fatal(code)
	}
	pass(t)
	if wildcard := env.find("*.home.example.org", "A"); len(wildcard) != 1 || wildcard[0].Content != "10.9.9.9" {
		t.Errorf("a record of someone else's was changed: %+v", wildcard)
	}
	if row := GetHomeStatus(true).Zones[0]; row.Record != "conflict" {
		t.Errorf("state %q, want conflict", row.Record)
	}
}

func TestHomeNamesRouteChecks(t *testing.T) {
	homeSetup(t, func(config *utils.Config) {
		config.HTTPConfig.DNSZones = append(config.HTTPConfig.DNSZones,
			utils.DNSZoneConfig{Zone: "example.net", HTTPSCertificateMode: "LETSENCRYPT", DNSChallengeProvider: "cloudflare", DNSChallengeConfig: map[string]string{"CF_DNS_API_TOKEN": goodToken}},
			utils.DNSZoneConfig{Zone: "example.com", HTTPSCertificateMode: "SELFSIGNED"})
		config.HTTPConfig.ProxyConfig.Routes = append(config.HTTPConfig.ProxyConfig.Routes,
			utils.ProxyRouteConfig{Name: "media", UseHost: true, Host: "tv.media.example.org", Mode: "SERVAPP", Target: "http://tv:1"})
	})

	for _, c := range []struct {
		body   interface{}
		code   int
		reason string
	}{
		{map[string]interface{}{"zone": "example.org", "label": "Ho_me", "enabled": true}, 400, "label"},
		{map[string]interface{}{"zone": "example.org", "label": "-x", "enabled": true}, 400, "label"},
		{map[string]interface{}{"zone": "nowhere.example", "enabled": true}, 400, utils.HomeProblemNoZone},
		{map[string]interface{}{"zone": "not a domain", "enabled": true}, 400, "zone"},
		{map[string]interface{}{"zone": "example.net", "enabled": true}, 400, utils.HomeProblemNoRecords},
		{map[string]interface{}{"zone": "example.com", "enabled": true}, 400, utils.HomeProblemNotCloudflare},
		// a public URL already answers under the suffix: it would become local-only
		{map[string]interface{}{"zone": "example.org", "label": "media", "enabled": true}, 400, "in_use"},
		{map[string]interface{}{"zone": "example.org", "label": "home", "enabled": true, "extra": 1}, 400, ""},
	} {
		code, answer := homeCall(t, c.body)
		if code != c.code || (c.reason != "" && answer["reason"] != c.reason) {
			t.Errorf("%v: %d %v", c.body, code, answer)
		}
	}
	if len(utils.GetMainConfig().HTTPConfig.Cloudflare.HomeNames) != 0 {
		t.Fatal("a refused request was stored")
	}

	// permissions: reading needs the configuration read, changing the configuration permission
	if code, _ := homeCall(t, map[string]interface{}{"zone": "example.org", "enabled": true}, utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ); code == 200 {
		t.Error("switched on without the configuration permission")
	}
	if code, _ := do(t, HomeNamesRoute, admin(httptest.NewRequest("GET", "/cosmos/api/home-names", nil), utils.PERM_LOGIN)); code == 200 {
		t.Error("read without the configuration read permission")
	}
	if code, _ := do(t, HomeNamesRoute, admin(httptest.NewRequest("PUT", "/cosmos/api/home-names", nil))); code != 405 {
		t.Errorf("PUT: %d", code)
	}
	big := bytes.Repeat([]byte("a"), 2000)
	if code, _ := do(t, HomeNamesRoute, admin(httptest.NewRequest("POST", "/cosmos/api/home-names", bytes.NewReader(append([]byte(`{"zone":"`), big...))))); code != 413 {
		t.Errorf("large body: %d", code)
	}

	// the label proposed when none is given
	if code, answer := homeCall(t, map[string]interface{}{"zone": "example.org", "enabled": true}); code != 200 {
		t.Fatalf("%d %v", code, answer)
	}
	if got := utils.GetMainConfig().HTTPConfig.Cloudflare.HomeNames; len(got) != 1 || got[0].Label != "home" {
		t.Errorf("stored %+v", got)
	}

	// a home name can never be served through Cloudflare or exposed
	updateConfig(func(config *utils.Config) error {
		config.HTTPConfig.ProxyConfig.Routes = append(config.HTTPConfig.ProxyConfig.Routes,
			utils.ProxyRouteConfig{Name: "athome", UseHost: true, Host: "tool.home.example.org", Mode: "SERVAPP", Target: "http://tool:1"})
		return nil
	})
	if code, answer := post(t, ShieldRoute, map[string]interface{}{"hostname": "tool.home.example.org", "shielded": true}); code == 200 || answer["reason"] != "home_name" {
		t.Errorf("shielding a home name: %d %v", code, answer)
	}
	if reason, _ := nameProblem(utils.GetMainConfig(), "radarr.home.example.org", "radarr", "web"); reason != "home_name" {
		t.Errorf("exposing a home name: %q", reason)
	}
}

func TestHomeNamesCheckFromTheServer(t *testing.T) {
	homeSetup(t, nil)
	if code, _ := homeCall(t, map[string]interface{}{"zone": "example.org", "enabled": true}); code != 200 {
		t.Fatal(code)
	}
	asked := []string{}
	answers := []string{homeLAN}
	var failure error
	previous := lookupHome
	lookupHome = func(ctx context.Context, name string) ([]string, error) {
		asked = append(asked, name)
		return answers, failure
	}
	defer func() { lookupHome = previous }()
	homeChecks.times = nil

	check := func(zone string) (int, map[string]interface{}) {
		raw, _ := json.Marshal(map[string]string{"zone": zone})
		return do(t, HomeNamesCheckRoute, admin(httptest.NewRequest("POST", "/cosmos/api/home-names/check", bytes.NewReader(raw))))
	}
	if code, answer := check("example.org"); code != 200 || data(answer)["result"] != "ok" || data(answer)["name"] != "cosmos.home.example.org" {
		t.Errorf("%d %v", code, answer)
	}
	answers = []string{"203.0.113.10"}
	if _, answer := check("example.org"); data(answer)["result"] != "other" {
		t.Errorf("other address: %v", answer)
	}
	answers, failure = nil, &net.DNSError{Err: "no such host", Name: "x", IsNotFound: true}
	if _, answer := check("example.org"); data(answer)["result"] != "none" {
		t.Errorf("filtered: %v", answer)
	}
	// only the panel's home name of an active domain is ever asked
	if code, _ := check("example.net"); code != 400 {
		t.Errorf("another domain: %d", code)
	}
	for _, name := range asked {
		if name != "cosmos.home.example.org" {
			t.Errorf("asked for %s", name)
		}
	}
	// bounded
	failure = nil
	limited := false
	for i := 0; i < homeChecksAnHour+2; i++ {
		if code, _ := check("example.org"); code == http.StatusTooManyRequests {
			limited = true
		}
	}
	if !limited {
		t.Error("no limit on the checks")
	}
	if code, _ := do(t, HomeNamesCheckRoute, admin(httptest.NewRequest("GET", "/cosmos/api/home-names/check", nil))); code != 405 {
		t.Errorf("GET: %d", code)
	}
}

func TestPreviewDoesNotWarnAboutCosmosOwnWildcard(t *testing.T) {
	owner := "abc"
	records := []Record{
		{Type: "A", Name: "*.home.example.org", Content: homeLAN},
		{Type: "TXT", Name: "_cosmos-owner._wildcard.home.example.org", Content: `"managed by cosmos abc"`},
	}
	preview := buildPreview("example.org", nil, "", records, testAddress, nil, owner)
	for _, w := range preview.Warnings {
		if w == "wildcard" {
			t.Error("warned about Cosmos' own wildcard")
		}
	}
	preview = buildPreview("example.org", nil, "", []Record{{Type: "A", Name: "*.lan.example.org", Content: homeLAN}}, testAddress, nil, owner)
	found := false
	for _, w := range preview.Warnings {
		found = found || w == "wildcard"
	}
	if !found {
		t.Error("no warning for a wildcard of someone else's")
	}
	_ = dnsrecords.OwnerID
}
