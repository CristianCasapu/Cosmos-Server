package certs

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestTheRecordOfADelegatedChallenge(t *testing.T) {
	if got := challengeName("*.Example.NET."); got != "_acme-challenge.example.net" {
		t.Errorf("a wildcard is proven at its domain: %q", got)
	}
	if got := challengeName("a.b.example.net"); got != "_acme-challenge.a.b.example.net" {
		t.Errorf("%q", got)
	}
	if got := DelegationTarget("example.net", "Example.org."); got != "example-net._acme.example.org" {
		t.Errorf("the proposed target: %q", got)
	}
	if got := DelegationTarget("*.a.b.example.net", "example.org"); got != "a-b-example-net._acme.example.org" {
		t.Errorf("the proposed target of a deeper wildcard: %q", got)
	}
	long := DelegationTarget(strings.Repeat("abcdefgh.", 12)+"example", "example.org")
	if label := strings.SplitN(long, ".", 2)[0]; len(label) > 63 || strings.HasSuffix(label, "-") {
		t.Errorf("the first label must fit in a DNS label: %d characters", len(label))
	}
}

func TestTheDelegationCheck(t *testing.T) {
	ctx := context.Background()
	table := tableResolver{cname: map[string]string{
		"_acme-challenge.example.net":   "example-net._acme.example.org.",
		"_acme-challenge.two.example":   "hop.elsewhere.example",
		"hop.elsewhere.example":         "Two-Example._acme.EXAMPLE.org",
		"_acme-challenge.wrong.example": "wrong-example._acme.example.com",
		"_acme-challenge.near.example":  "near-example._acme.notexample.org",
		"_acme-challenge.loop.example":  "a.loop.example",
		"a.loop.example":                "_acme-challenge.loop.example",
		"_acme-challenge.apex.example":  "example.org",
	}, fail: map[string]bool{"_acme-challenge.down.example": true}}
	for i := 0; i < 12; i++ {
		table.cname[strings.Repeat("x", i+1)+".chain.example"] = strings.Repeat("x", i+2) + ".chain.example"
	}
	table.cname["_acme-challenge.chain.example"] = "x.chain.example"

	cases := map[string]struct {
		state string
		found string
	}{
		"example.net":   {DelegationOK, "example-net._acme.example.org"},
		"*.example.net": {DelegationOK, "example-net._acme.example.org"},
		"two.example":   {DelegationOK, "two-example._acme.example.org"},
		"apex.example":  {DelegationOK, "example.org"},
		"none.example":  {DelegationMissing, ""},
		"wrong.example": {DelegationElsewhere, "wrong-example._acme.example.com"},
		"near.example":  {DelegationElsewhere, "near-example._acme.notexample.org"},
		"loop.example":  {DelegationLoop, "_acme-challenge.loop.example"},
		"chain.example": {DelegationLoop, ""},
		"down.example":  {DelegationFailed, ""},
	}
	for name, want := range cases {
		got := CheckDelegation(ctx, table, name, "example.org")
		if got.State != want.state || (want.found != "" && got.Found != want.found) {
			t.Errorf("%s: %s (found %q), want %s (%q)", name, got.State, got.Found, want.state, want.found)
		}
		if got.Record != challengeName(name) || got.Target != DelegationTarget(name, "example.org") {
			t.Errorf("%s: record %q target %q", name, got.Record, got.Target)
		}
	}
	failed := CheckDelegation(ctx, table, "down.example", "example.org")
	if failed.Detail == "" || strings.Contains(failed.Detail, "\n") {
		t.Errorf("the resolver's words are one line: %q", failed.Detail)
	}
}

func TestAnOrderWithoutItsRecordNeverReachesTheCA(t *testing.T) {
	env := setup(t, func(config *utils.Config) {
		config.HTTPConfig.DNSZones = append(config.HTTPConfig.DNSZones, utils.DNSZoneConfig{
			Zone: "example.net", HTTPSCertificateMode: "LETSENCRYPT", ChallengeVia: "example.org", UseWildcardCertificate: true,
		})
	})
	resolver = tableResolver{cname: map[string]string{"_acme-challenge.example.net": "example-net._acme.example.org"}}
	zone := utils.GetMainConfig().HTTPConfig.DNSZones[1]
	key := "zone:example.net"

	// the deeper name has no record of its own: the order stops before the CA
	_, failure := order(context.Background(), Current(), key, []string{"example.net", "*.example.net", "a.b.example.net"}, &zone, utils.GetMainConfig())
	if failure == nil || failure.Code != FailDelegationMissing {
		t.Fatalf("got %+v", failure)
	}
	if !strings.Contains(failure.Detail, "_acme-challenge.a.b.example.net must be a CNAME to a-b-example-net._acme.example.org") ||
		strings.Contains(failure.Detail, "_acme-challenge.example.net ") {
		t.Errorf("the detail names the missing record only: %q", failure.Detail)
	}
	if attempts := store.get(key).Attempts; len(attempts) != 0 {
		t.Errorf("an order that never reached the CA counts as an attempt: %v", attempts)
	}
	_ = env

	// handed to a domain that cannot answer: the same, with its own reason
	other := zone
	other.ChallengeVia = "nowhere.example"
	if _, failure := order(context.Background(), Current(), key, []string{"example.net"}, &other, utils.GetMainConfig()); failure == nil || failure.Code != FailNoProvider {
		t.Errorf("got %+v", failure)
	}
	// a domain with neither
	bare := utils.DNSZoneConfig{Zone: "bare.example", HTTPSCertificateMode: "LETSENCRYPT"}
	if _, failure := challengeAccess(utils.GetMainConfig().HTTPConfig.DNSZones, bare); failure == nil || failure.Code != FailNoProvider {
		t.Errorf("got %+v", failure)
	}
	// names no public CA certifies
	if _, failure := order(context.Background(), Current(), "zone:example.org", []string{"192.168.1.10", "nas.local"}, &utils.GetMainConfig().HTTPConfig.DNSZones[0], utils.GetMainConfig()); failure == nil || failure.Code != FailCARefused {
		t.Errorf("got %+v", failure)
	}
}

func TestADelegatedChallengeUsesTheOtherDomainsAccess(t *testing.T) {
	zones := []utils.DNSZoneConfig{
		{Zone: "example.org", HTTPSCertificateMode: "LETSENCRYPT", DNSChallengeProvider: "cloudflare",
			DNSChallengeConfig: map[string]string{"CF_DNS_API_TOKEN": testToken}, DNSChallengeResolvers: "192.0.2.53:53", DNSChallengePropagationWait: 10},
		{Zone: "example.net", HTTPSCertificateMode: "LETSENCRYPT", ChallengeVia: "example.org"},
		{Zone: "own.example", HTTPSCertificateMode: "LETSENCRYPT", ChallengeVia: "example.org", DNSChallengeResolvers: "192.0.2.99:53", DisablePropagationChecks: true},
	}
	how, failure := challengeAccess(zones, zones[1])
	if failure != nil || !how.delegated || how.zone.DNSChallengeProvider != "cloudflare" || how.zone.DNSChallengeConfig["CF_DNS_API_TOKEN"] != testToken ||
		how.zone.DNSChallengeResolvers != "192.0.2.53:53" || how.zone.DNSChallengePropagationWait != 10 {
		t.Errorf("got %+v %v", how, failure)
	}
	how, _ = challengeAccess(zones, zones[2])
	if how.zone.DNSChallengeResolvers != "192.0.2.99:53" || !how.zone.DisablePropagationChecks {
		t.Errorf("the domain's own options win: %+v", how.zone)
	}
	own, failure := challengeAccess(zones, zones[0])
	if failure != nil || own.delegated || own.zone.Zone != "example.org" {
		t.Errorf("got %+v %v", own, failure)
	}
	if zones[0].DNSChallengeResolvers != "192.0.2.53:53" || zones[0].DisablePropagationChecks {
		t.Error("the other domain's stored options were changed")
	}
}

func TestResolversForTheChallenge(t *testing.T) {
	previous := lookupNS
	defer func() { lookupNS = previous }()
	lookupNS = func(string) ([]*net.NS, error) { return []*net.NS{{Host: "ada.ns.cloudflare.com."}}, nil }
	if got := strings.Join(challengeResolvers(" 192.0.2.53:53 , ,192.0.2.54 ", "example.org"), "|"); got != "192.0.2.53:53|192.0.2.54" {
		t.Errorf("the ones set for the domain: %q", got)
	}
	if got := strings.Join(challengeResolvers("", "example.org"), "|"); got != "ada.ns.cloudflare.com.|8.8.8.8|1.1.1.1" {
		t.Errorf("by default: %q", got)
	}
}
