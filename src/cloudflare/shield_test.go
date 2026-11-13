package cloudflare

// Fork addition: the records at Cloudflare, written by upstream's reconciler
// through the real libdns provider against the stand-in, with the fork's
// filter, wrapper and flag sync in place.

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/cloudflare/cffake"
	"github.com/azukaar/cosmos-server/src/dnsrecords"
	"github.com/azukaar/cosmos-server/src/utils"
)

func shield(t *testing.T, host string, on bool) map[string]interface{} {
	t.Helper()
	code, answer := post(t, ShieldRoute, map[string]interface{}{"hostname": host, "shielded": on})
	if code != 200 {
		t.Fatalf("shield %s %v: status %d, %v", host, on, code, answer)
	}
	return data(answer)
}

func TestAttachedDomainsGetRecordsAndTheWebsiteIsNotTouched(t *testing.T) {
	env := setup(t, nil)
	before := env.fake.Records("example.org")

	pass(t)

	for _, name := range []string{"radarr.example.org", "jellyfin.example.org"} {
		records := env.find(name, "A")
		if len(records) != 1 || records[0].Content != testAddress || records[0].Proxied {
			t.Errorf("%s: want one direct A record to %s, got %+v", name, testAddress, records)
		}
		if len(env.find("_cosmos-owner."+name, "TXT")) != 1 {
			t.Errorf("%s: no owner marker was written", name)
		}
	}
	// a local address is not a record
	if got := env.find("radarr.local", "A"); len(got) != 0 {
		t.Errorf("a .local name was written: %+v", got)
	}
	// everything that was there is still there, unchanged
	after := env.fake.Records("example.org")
	for _, old := range before {
		found := false
		for _, now := range after {
			found = found || now == old
		}
		if !found {
			t.Errorf("an existing record was changed or removed: %+v", old)
		}
	}

	if record, ok := RecordOf("radarr.example.org"); !ok || record.State != RecordOK || record.Proxied {
		t.Errorf("state of radarr: %+v %v", record, ok)
	}
	if _, ok := RecordOf("radarr.local"); ok {
		t.Error("a local name has a record state")
	}
}

// The layout of the server this was written for: the domain's own name has a
// proxied record that something outside Cosmos keeps pointed at the home
// address, the panel is served under that very name on Cosmos' port, and the
// applications are at subdomains, some through Cloudflare and some direct.
func TestThePanelOnAProxiedApexThatCosmosNeverWrites(t *testing.T) {
	env := setup(t, func(config *utils.Config) {
		config.HTTPConfig.ProxyConfig.Routes = append(config.HTTPConfig.ProxyConfig.Routes,
			utils.ProxyRouteConfig{Name: "www", UseHost: true, Host: "www.example.org", Mode: "SERVAPP", Target: "http://x:80"})
	})
	// the apex points at the home address, through Cloudflare
	updateApex := func(content string) {
		zone := env.fake.Records("example.org")
		for _, record := range zone {
			if record.Name == "example.org" && record.Type == "A" {
				env.fake.SetContent("example.org", record.ID, content)
			}
		}
	}
	updateApex(testAddress)
	apexBefore := env.find("example.org", "A")[0]
	websiteBefore := env.fake.Records("example.org")

	// the panel gets the domain's own name; it and radarr go through Cloudflare, jellyfin does not
	if code, answer := post(t, PanelRoute, map[string]string{"hostname": "example.org"}); code != 200 {
		t.Fatalf("the panel on the domain itself: %d %v", code, answer)
	}
	if !utils.ReadConfigFromFile().HTTPConfig.AllowHTTPLocalIPAccess {
		t.Fatal("the panel no longer answers at the server's local address")
	}
	shield(t, "example.org", true)
	shield(t, "radarr.example.org", true)
	env.fake.Calls(true)

	pass(t)
	pass(t)

	untouched := func(when string) {
		t.Helper()
		apex := env.find("example.org", "A")
		if len(apex) != 1 || apex[0].ID != apexBefore.ID || !apex[0].Proxied || apex[0].TTL != apexBefore.TTL {
			t.Fatalf("%s: the apex record was replaced or changed: %+v (was %+v)", when, apex, apexBefore)
		}
		if got := env.find("_cosmos-owner.example.org", "TXT"); len(got) != 0 {
			t.Errorf("%s: a marker was written for the apex: %+v", when, got)
		}
		for _, call := range env.fake.Calls(false) {
			if call.Method == "GET" {
				continue
			}
			if strings.Contains(call.Path, apexBefore.ID) || strings.Contains(call.Body, `"name":"@"`) || strings.Contains(call.Body, `"name":"example.org"`) {
				t.Errorf("%s: Cosmos sent a change for the apex: %+v", when, call)
			}
		}
		for _, old := range websiteBefore {
			if old.Name == "example.org" && old.Type == "A" {
				continue
			}
			kept := false
			for _, now := range env.fake.Records("example.org") {
				kept = kept || now == old
			}
			if !kept {
				t.Errorf("%s: an existing record was changed or removed: %+v", when, old)
			}
		}
	}
	untouched("after connecting")
	if got := env.find("example.org", "A")[0].Content; got != testAddress {
		t.Errorf("the apex content: %s", got)
	}

	// the applications: one proxied, one direct, each with a record of Cosmos' own
	if radarr := env.find("radarr.example.org", "A"); len(radarr) != 1 || !radarr[0].Proxied || radarr[0].Content != testAddress {
		t.Errorf("radarr: %+v", radarr)
	}
	if jellyfin := env.find("jellyfin.example.org", "A"); len(jellyfin) != 1 || jellyfin[0].Proxied || jellyfin[0].Content != testAddress {
		t.Errorf("jellyfin: %+v", jellyfin)
	}
	// www is the website's alias: an application of that name does not take it
	if len(env.find("www.example.org", "CNAME")) != 1 || len(env.find("www.example.org", "A")) != 0 {
		t.Errorf("www was taken over without being asked")
	}

	// what the screens are told
	if record, ok := RecordOf("example.org"); !ok || record.State != RecordProtected || !record.Seen || !record.Proxied || !record.PointsHere {
		t.Errorf("state of the apex: %+v", record)
	}
	if record, _ := RecordOf("www.example.org"); record.State != RecordConflict || !record.Seen || !record.Proxied || record.PointsHere {
		t.Errorf("state of www: %+v", record)
	}
	status := GetStatus(true)
	for _, host := range status.Hosts {
		switch host.Host {
		case "example.org":
			if !host.Panel || !host.Shielded || host.Managed || host.URL != "https://example.org:2053" {
				t.Errorf("the panel in the status: %+v", host)
			}
		case "radarr.example.org":
			if !host.Shielded || !host.Managed || host.URL != "https://radarr.example.org:2053" {
				t.Errorf("radarr in the status: %+v", host)
			}
		case "jellyfin.example.org":
			if host.Shielded || !host.Managed || host.CannotShield != "" {
				t.Errorf("jellyfin in the status: %+v", host)
			}
		}
	}

	// the home address changes: the outside updater moves the apex, Cosmos its own records
	updateApex("203.0.113.77")
	updateConfig(func(config *utils.Config) error {
		config.HTTPConfig.AdvertisedAddress = "203.0.113.77"
		return nil
	})
	knownAddress = func() string { return "203.0.113.77" }
	env.fake.Calls(true)
	pass(t)
	pass(t)
	untouched("after a change of address")
	if radarr := env.find("radarr.example.org", "A"); len(radarr) != 1 || !radarr[0].Proxied || radarr[0].Content != "203.0.113.77" {
		t.Errorf("radarr after the change of address: %+v", radarr)
	}

	// switching the shield of the panel off in Cosmos changes nothing at Cloudflare either
	shield(t, "example.org", false)
	env.fake.Calls(true)
	pass(t)
	untouched("after switching the panel to direct")

	// the panel moves away from the apex: the apex stays, even if an older
	// version had left a marker of this very server on it
	env.fake.AddRecord("example.org", cffake.Record{Type: "TXT", Name: "_cosmos-owner.example.org", Content: `"managed by cosmos ` + dnsrecords.OwnerID() + `"`})
	if code, answer := post(t, PanelRoute, map[string]string{"hostname": "cosmos.example.org"}); code != 200 {
		t.Fatalf("moving the panel: %d %v", code, answer)
	}
	env.fake.Calls(true)
	pass(t)
	pass(t)
	if apex := env.find("example.org", "A"); len(apex) != 1 || apex[0].ID != apexBefore.ID {
		t.Fatalf("the apex was deleted once nothing of Cosmos used it: %+v", apex)
	}
	if got := env.find("_cosmos-owner.example.org", "TXT"); len(got) != 1 {
		t.Errorf("the record at the apex marker name was changed: %+v", got)
	}
	if got := env.find("cosmos.example.org", "A"); len(got) != 1 {
		t.Errorf("the panel's new name has no record: %+v", got)
	}
}

func TestAnExistingRecordIsReplacedOnlyWhenTheAdminSaysSo(t *testing.T) {
	env := setup(t, func(config *utils.Config) {
		config.HTTPConfig.ProxyConfig.Routes = append(config.HTTPConfig.ProxyConfig.Routes,
			utils.ProxyRouteConfig{Name: "www", UseHost: true, Host: "www.example.org", Mode: "SERVAPP", Target: "http://x:80"})
	})
	pass(t)
	if len(env.find("www.example.org", "CNAME")) != 1 {
		t.Fatal("www was replaced before the admin allowed it")
	}

	// names outside the domains, and a domain's own name, are refused
	for _, name := range []string{"www.example.com", "example.org", "not a name", "10.0.0.1"} {
		if code, _ := post(t, TakeOverRoute, map[string]interface{}{"names": []string{name}}); code != 400 {
			t.Errorf("take over %q: status %d", name, code)
		}
	}

	if code, answer := post(t, TakeOverRoute, map[string]interface{}{"names": []string{"www.example.org"}}); code != 200 {
		t.Fatalf("take over: %d %v", code, answer)
	}
	pass(t)
	pass(t)

	if got := env.find("www.example.org", "CNAME"); len(got) != 0 {
		t.Errorf("the alias is still there: %+v", got)
	}
	if got := env.find("www.example.org", "A"); len(got) != 1 || got[0].Content != testAddress {
		t.Errorf("www: %+v", got)
	}
	if record, _ := RecordOf("www.example.org"); record.State != RecordOK {
		t.Errorf("state of www: %+v", record)
	}
}

func TestTheShieldIsSetPerApplicationAndFlipped(t *testing.T) {
	env := setup(t, nil)
	pass(t)

	// first shielded application: Cloudflare's addresses become trusted
	answer := shield(t, "radarr.example.org", true)
	if answer["trustRanges"] != true {
		t.Errorf("the first shield did not switch the trust on: %v", answer)
	}
	env.fake.Calls(true)
	pass(t)

	radarr := env.find("radarr.example.org", "A")
	if len(radarr) != 1 || !radarr[0].Proxied || radarr[0].TTL != 1 || radarr[0].Content != testAddress {
		t.Fatalf("radarr is not proxied: %+v", radarr)
	}
	if jellyfin := env.find("jellyfin.example.org", "A"); len(jellyfin) != 1 || jellyfin[0].Proxied {
		t.Fatalf("jellyfin was proxied too: %+v", jellyfin)
	}
	patches := 0
	for _, call := range env.fake.Calls(false) {
		if call.Method == "PATCH" {
			patches++
			if !strings.Contains(call.Body, `"proxied":true`) || !strings.Contains(call.Body, `"ttl":1`) {
				t.Errorf("unexpected change: %s", call.Body)
			}
		}
	}
	if patches != 1 {
		t.Errorf("want exactly one change at Cloudflare, got %d: %+v", patches, env.fake.Calls(false))
	}
	if record, _ := RecordOf("radarr.example.org"); record.State != RecordOK || !record.Proxied {
		t.Errorf("state of radarr: %+v", record)
	}

	// nothing changed: a further pass asks Cloudflare nothing about flags
	env.fake.Calls(true)
	pass(t)
	if calls := env.fake.Calls(false); len(calls) != 0 {
		t.Errorf("an idle pass called Cloudflare: %+v", calls)
	}

	// back to direct
	answer = shield(t, "radarr.example.org", false)
	if answer["lastShield"] != true {
		t.Errorf("removing the last shield was not reported: %v", answer)
	}
	pass(t)
	radarr = env.find("radarr.example.org", "A")
	if len(radarr) != 1 || radarr[0].Proxied || radarr[0].TTL != 60 {
		t.Fatalf("radarr is still proxied: %+v", radarr)
	}
	// the trust stays until the admin answers the question
	if !utils.GetMainConfig().HTTPConfig.Cloudflare.TrustRanges {
		t.Error("the trust was removed without asking")
	}
}

func TestANewAddressIsPublishedProxiedForAShieldedName(t *testing.T) {
	env := setup(t, nil)
	shield(t, "radarr.example.org", true)
	pass(t)
	env.fake.Calls(true)

	// the server's public address changes
	updateConfig(func(config *utils.Config) error {
		config.HTTPConfig.AdvertisedAddress = "203.0.113.77"
		return nil
	})
	pass(t)
	pass(t)

	radarr := env.find("radarr.example.org", "A")
	if len(radarr) != 1 || radarr[0].Content != "203.0.113.77" || !radarr[0].Proxied {
		t.Fatalf("radarr after the change of address: %+v", radarr)
	}
	// it was created proxied, not created direct and corrected afterwards
	for _, call := range env.fake.Calls(false) {
		if call.Method == "POST" && strings.Contains(call.Body, "radarr") && strings.Contains(call.Body, `"type":"A"`) &&
			!strings.Contains(call.Body, `"proxied":true`) {
			t.Errorf("the new address of a shielded name was published direct: %s", call.Body)
		}
	}
	if jellyfin := env.find("jellyfin.example.org", "A"); len(jellyfin) != 1 || jellyfin[0].Proxied || jellyfin[0].Content != "203.0.113.77" {
		t.Errorf("jellyfin after the change of address: %+v", jellyfin)
	}
}

func TestFlagsOfNamesCosmosDoesNotManageAreLeftAlone(t *testing.T) {
	env := setup(t, nil)
	shield(t, "radarr.example.org", true)
	pass(t)

	// someone edits records by hand at Cloudflare: one Cosmos manages, others it does not
	env.fake.SetProxied("example.org", "radarr.example.org", "A", false)
	env.fake.SetProxied("example.org", "example.org", "A", false)
	env.fake.SetProxied("example.org", "mail.example.org", "A", true)

	forgetSync() // what the ten-minute read would do
	env.fake.Calls(true)
	pass(t)

	if radarr := env.find("radarr.example.org", "A"); !radarr[0].Proxied {
		t.Error("the flag of a managed name was not put back")
	}
	if apex := env.find("example.org", "A"); apex[0].Proxied {
		t.Error("the apex flag was changed back by Cosmos")
	}
	if mail := env.find("mail.example.org", "A"); !mail[0].Proxied {
		t.Error("the flag of a name Cosmos does not manage was changed")
	}
	if writes := cffake.Writes(env.fake.Calls(false)); writes != 1 {
		t.Errorf("want one change (radarr), got %d: %+v", writes, env.fake.Calls(false))
	}
}

func TestOnlyRecordsCosmosMarkedAreDeleted(t *testing.T) {
	env := setup(t, nil)
	pass(t)
	if len(env.find("jellyfin.example.org", "A")) != 1 {
		t.Fatal("jellyfin was not created")
	}

	// the domain is detached from jellyfin; mail was never Cosmos'
	updateConfig(func(config *utils.Config) error {
		config.HTTPConfig.ProxyConfig.Routes = config.HTTPConfig.ProxyConfig.Routes[:2]
		return nil
	})
	pass(t)
	pass(t)

	if got := env.find("jellyfin.example.org", "A"); len(got) != 0 {
		t.Errorf("the record of a detached domain stayed: %+v", got)
	}
	if got := env.find("_cosmos-owner.jellyfin.example.org", "TXT"); len(got) != 0 {
		t.Errorf("its marker stayed: %+v", got)
	}
	for _, name := range []string{"example.org", "mail.example.org"} {
		if len(env.find(name, "A")) != 1 {
			t.Errorf("%s, which Cosmos never marked, was deleted", name)
		}
	}
	if len(env.find("www.example.org", "CNAME")) != 1 || len(env.find("radarr.example.org", "A")) != 1 {
		t.Errorf("more than the detached name went: %+v", env.fake.Records("example.org"))
	}
}

func TestAFailureAtCloudflareIsReportedWithoutTheToken(t *testing.T) {
	var logged bytes.Buffer
	log.SetOutput(&logged)
	defer log.SetOutput(os.Stderr)

	env := setup(t, func(config *utils.Config) {
		config.HTTPConfig.DNSZones[0].DNSChallengeConfig["CF_DNS_API_TOKEN"] = readToken
	})
	pass(t)

	record, ok := RecordOf("radarr.example.org")
	if !ok || record.State != RecordError || record.Reason != "forbidden" {
		t.Errorf("state with a token that may only read: %+v", record)
	}
	if len(env.find("radarr.example.org", "A")) != 0 {
		t.Error("a record was created with a read-only token")
	}
	if strings.Contains(record.Message, readToken) || strings.Contains(logged.String(), readToken) {
		t.Error("the token is in a message or in the log")
	}

	// a token Cloudflare does not know
	updateConfig(func(config *utils.Config) error {
		config.HTTPConfig.DNSZones[0].DNSChallengeConfig["CF_DNS_API_TOKEN"] = "unknown0123456789abcdefghijklmnopqrstuvwxYZ"
		return nil
	})
	pass(t)
	if record, _ := RecordOf("radarr.example.org"); record.State != RecordError || record.Reason != "invalid_token" {
		t.Errorf("state with an unknown token: %+v", record)
	}
	if strings.Contains(logged.String(), "unknown0123456789") {
		t.Error("the token is in the log")
	}
}

func TestShieldIsRefusedWhereItCannotWork(t *testing.T) {
	cases := []struct {
		name   string
		change func(config *utils.Config)
		host   string
		reason string
	}{
		{"a port Cloudflare does not proxy", func(c *utils.Config) { c.HTTPConfig.HTTPSPort = "8444" }, "radarr.example.org", "port"},
		{"the router's port, when it differs", func(c *utils.Config) {
			c.HTTPConfig.UPnP = utils.UPnPConfig{Enabled: true, ExternalHTTPSPort: "4443"}
		}, "radarr.example.org", "port"},
		{"a wildcard record", func(c *utils.Config) { c.HTTPConfig.DNSZones[0].WildcardRecord = true }, "radarr.example.org", "wildcard_record"},
		{"no token", func(c *utils.Config) { c.HTTPConfig.DNSZones[0].DNSChallengeConfig = nil }, "radarr.example.org", "no_token"},
		{"another provider", func(c *utils.Config) { c.HTTPConfig.DNSZones[0].DNSChallengeProvider = "ovh" }, "radarr.example.org", "not_cloudflare"},
		{"a name Cosmos does not serve", nil, "sonarr.example.org", "not_served"},
		{"a name outside the domains", nil, "radarr.example.com", "not_served"},
		{"a URL on a port of its own", func(c *utils.Config) {
			c.HTTPConfig.ProxyConfig.Routes = append(c.HTTPConfig.ProxyConfig.Routes,
				utils.ProxyRouteConfig{Name: "radarr-raw", UseHost: true, Host: "radarr.example.org:7878", Mode: "SERVAPP", Target: "http://radarr:7878"})
		}, "radarr.example.org", "own_port"},
		{"records that point at a local address", func(c *utils.Config) {}, "radarr.example.org", "private_address"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setup(t, c.change)
			if c.reason == "private_address" {
				knownAddress = func() string { return "192.168.1.10" }
			}
			code, answer := post(t, ShieldRoute, map[string]interface{}{"hostname": c.host, "shielded": true})
			if code != 400 || answer["reason"] != c.reason {
				t.Errorf("status %d, answer %v, want reason %s", code, answer, c.reason)
			}
			cf := utils.GetMainConfig().HTTPConfig.Cloudflare
			if len(cf.Shielded) != 0 || cf.TrustRanges {
				t.Errorf("a refused shield changed the configuration: %+v", cf)
			}
		})
	}

	// records the admin keeps by hand: the choice only tells Cosmos what to
	// expect, and Cosmos changes nothing at Cloudflare
	env := setup(t, func(c *utils.Config) {
		c.HTTPConfig.DNSZones[0].ManageRecords = false
		c.HTTPConfig.DNSZones[0].DNSChallengeConfig = nil
	})
	shield(t, "radarr.example.org", true)
	if !utils.IsShieldedHost("radarr.example.org") || len(env.fake.Calls(false)) != 0 {
		t.Errorf("a shield for a record kept by hand: shielded %v, calls %+v", utils.IsShieldedHost("radarr.example.org"), env.fake.Calls(false))
	}

	// the port the router opens is what counts when it is one Cloudflare proxies
	setup(t, func(c *utils.Config) {
		c.HTTPConfig.HTTPSPort = "4443"
		c.HTTPConfig.UPnP = utils.UPnPConfig{Enabled: true, ExternalHTTPSPort: "2053"}
	})
	shield(t, "radarr.example.org", true)
}

func TestTrustCannotBeRemovedWhileAnApplicationIsShielded(t *testing.T) {
	setup(t, nil)
	shield(t, "radarr.example.org", true)

	if code, _ := post(t, RangesRoute, map[string]string{"action": "distrust"}); code != 400 {
		t.Errorf("trust removed with a shielded application: status %d", code)
	}
	if !utils.GetMainConfig().HTTPConfig.Cloudflare.TrustRanges {
		t.Fatal("trust is off")
	}

	shield(t, "radarr.example.org", false)
	if code, answer := post(t, RangesRoute, map[string]string{"action": "distrust"}); code != 200 || data(answer)["trusted"] != false {
		t.Errorf("trust could not be removed afterwards: %d %v", code, answer)
	}
	if utils.GetMainConfig().HTTPConfig.Cloudflare.TrustRanges {
		t.Error("trust is still on")
	}
}

func TestAShieldOfADetachedDomainIsForgotten(t *testing.T) {
	setup(t, nil)
	shield(t, "radarr.example.org", true)
	shield(t, "jellyfin.example.org", true)

	// jellyfin's domain is detached; the next change of a shield drops its entry
	updateConfig(func(config *utils.Config) error {
		config.HTTPConfig.ProxyConfig.Routes = config.HTTPConfig.ProxyConfig.Routes[:2]
		return nil
	})
	answer := shield(t, "radarr.example.org", false)
	if got := utils.GetMainConfig().HTTPConfig.Cloudflare.Shielded; len(got) != 0 {
		t.Errorf("shielded: %v", got)
	}
	if answer["lastShield"] != true {
		t.Errorf("the last shield was not reported: %v", answer)
	}
}
