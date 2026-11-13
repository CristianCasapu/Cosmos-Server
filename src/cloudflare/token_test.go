package cloudflare

// Fork addition: what the token check says about each kind of credential.

import (
	"context"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/cloudflare/cffake"
)

func TestTokenFormat(t *testing.T) {
	cases := map[string]string{
		goodToken: "token",
		"abcdefghij0123456789ABCDEFGHIJ0123456789":            "token",         // the form of tokens before 2026
		"cfat_" + strings.Repeat("a1B2", 10) + "deadbeef":     "token",         // owned by an account
		"cfk_" + strings.Repeat("0a", 20) + "1234":            "global_key",    // the Global API Key
		strings.Repeat("0123456789abcdef", 2) + "01234":       "global_key",    // its older form: 37 hex characters
		"v1.0-" + strings.Repeat("ab", 40):                    "origin_ca_key", // an Origin CA key
		"cfoat_" + strings.Repeat("x", 44):                    "oauth",
		"":                                                    "malformed",
		"short":                                               "malformed",
		"with spaces 0123456789 0123456789 0123456789 01234":  "malformed",
		"line\nbreak0123456789012345678901234567890123456789": "malformed",
		strings.Repeat("a", 201):                              "malformed",
	}
	for token, want := range cases {
		if got := TokenFormat(token); got != want {
			t.Errorf("TokenFormat(%.20q…) = %s, want %s", token, got, want)
		}
	}
}

func TestCheckToken(t *testing.T) {
	env := setup(t, nil)
	expired := "expired0123456789abcdefghijklmnopqrstuvwxYZ"
	disabled := "disabled123456789abcdefghijklmnopqrstuvwxYZ"
	account := "cfat_account0123456789abcdefghijklmnopqrstuvwxYZ"
	blind := "nozones0123456789abcdefghijklmnopqrstuvwxYZ"
	env.fake.AddToken(expired, cffake.Token{Status: "expired"})
	env.fake.AddToken(disabled, cffake.Token{Status: "disabled"})
	env.fake.AddToken(account, cffake.Token{Account: true, Zones: []string{"example.org"}, ReadDNS: true, EditDNS: true})
	env.fake.AddToken(blind, cffake.Token{NoZoneRead: true, EditDNS: true})

	check := func(token string) TokenReport {
		env.fake.Calls(true)
		report := CheckToken(context.Background(), token)
		if writes := cffake.Writes(env.fake.Calls(false)); writes != 0 {
			t.Errorf("checking a token changed something at Cloudflare: %+v", env.fake.Calls(false))
		}
		return report
	}

	// the one that fits: one domain, may edit it
	report := check(goodToken)
	if report.Status != "ok" || len(report.Zones) != 1 || report.Broad {
		t.Fatalf("good token: %+v", report)
	}
	if zone := report.Zones[0]; zone.Name != "example.org" || zone.CanRead != "yes" || zone.CanEdit != "yes" || !zone.Connected {
		t.Errorf("good token, zone: %+v", zone)
	}

	// too narrow: may read, may not edit
	report = check(readToken)
	if report.Status != "ok" || report.Zones[0].CanRead != "yes" || report.Zones[0].CanEdit != "no" {
		t.Errorf("read-only token: %+v", report)
	}

	// too broad: sees every domain of the account
	report = check(otherToken)
	if report.Status != "ok" || !report.Broad || len(report.Zones) != 2 {
		t.Errorf("all-zones token: %+v", report)
	}

	// owned by an account: unknown to the user endpoint, yet it works
	report = check(account)
	if report.Status != "ok" || len(report.Zones) != 1 || report.Zones[0].CanEdit != "yes" {
		t.Errorf("account token: %+v", report)
	}

	for token, want := range map[string]string{
		expired:  "expired",
		disabled: "disabled",
		"wrong0123456789abcdefghijklmnopqrstuvwxyzABCD": "invalid",
		blind: "no_zones",
		"cfk_" + strings.Repeat("0a", 20) + "1234": "global_key",
		"v1.0-" + strings.Repeat("ab", 40):         "origin_ca_key",
		"not a token":                              "malformed",
	} {
		if report := check(token); report.Status != want || len(report.Zones) != 0 {
			t.Errorf("token %.12s…: %+v, want status %s", token, report, want)
		}
	}

	// a key that is refused by its form never leaves the server
	env.fake.Calls(true)
	CheckToken(context.Background(), "cfk_"+strings.Repeat("0a", 20)+"1234")
	if calls := env.fake.Calls(false); len(calls) != 0 {
		t.Errorf("a Global API Key was sent to Cloudflare: %+v", calls)
	}

	env.fake.SetDown(true)
	if report := check(goodToken); report.Status != "unreachable" {
		t.Errorf("Cloudflare down: %+v", report)
	}
}

func TestCheckRouteUsesTheStoredTokenAndNeverReturnsOne(t *testing.T) {
	setup(t, nil)

	// the form sends the mask for a domain that is already connected
	code, answer := post(t, CheckRoute, map[string]string{"token": "***", "zone": "example.org"})
	if code != 200 || data(answer)["status"] != "ok" {
		t.Fatalf("check with the stored token: %d %v", code, answer)
	}
	// nothing stored for that name: nothing to check
	if code, _ := post(t, CheckRoute, map[string]string{"token": "", "zone": "example.net"}); code != 400 {
		t.Errorf("check without a token: status %d", code)
	}
	// a pasted one
	code, answer = post(t, CheckRoute, map[string]string{"token": readToken})
	zones, _ := data(answer)["zones"].([]interface{})
	if code != 200 || len(zones) != 1 || zones[0].(map[string]interface{})["canEdit"] != "no" {
		t.Errorf("check of a pasted token: %d %v", code, answer)
	}
}
