package cloudflare

import (
	"context"
	"strconv"
	"testing"

	"github.com/azukaar/cosmos-server/src/cloudflare/cffake"
)

// Fork addition: the IP Access Rules calls against the stand-in.
func TestAccessRules(t *testing.T) {
	env := setup(t, nil)
	fw := "cfut_" + "Rmlyd2FsbHRlc3R0b2tlbl9maXJld2FsbHRlc3R0b2tlbl8wMTIzNDU" + "1234"
	env.fake.AddToken(fw, cffake.Token{Zones: []string{"example.org"}, ReadFirewall: true, EditFirewall: true})
	ctx := context.Background()
	zoneID, err := ZoneID(ctx, goodToken, "example.org")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(fw)

	// the DNS token may not: forbidden
	if _, _, err := NewClient(goodToken).ListAccessRules(ctx, zoneID, "", ""); kindOf(err) != "forbidden" {
		t.Fatalf("DNS token: %v", err)
	}
	// pages are followed: 230 rules with the marker, 5 without
	for i := 0; i < 230; i++ {
		env.fake.AddAccessRule("example.org", "ip", "198.51.100."+strconv.Itoa(i%250), "block", "x [cosmos-edge:abc]")
	}
	for i := 0; i < 5; i++ {
		env.fake.AddAccessRule("example.org", "ip", "192.0.2."+strconv.Itoa(i), "block", "the owner's")
	}
	rules, more, err := client.ListAccessRules(ctx, zoneID, "COSMOS-EDGE:abc", "")
	if err != nil || more || len(rules) != 230 {
		t.Fatalf("%d rules, more %v, %v", len(rules), more, err)
	}
	if rules, _, _ := client.ListAccessRules(ctx, zoneID, "", "192.0.2.3"); len(rules) != 1 || rules[0].Notes != "the owner's" {
		t.Fatalf("by value: %+v", rules)
	}

	rule, err := client.CreateAccessRule(ctx, zoneID, "2001:db8:5:6::/64", "managed_challenge", "note")
	if err != nil || rule.Configuration.Target != "ip_range" || rule.Mode != "managed_challenge" {
		t.Fatalf("%+v %v", rule, err)
	}
	if _, err := client.CreateAccessRule(ctx, zoneID, "2001:db8:5:6::/64", "block", "again"); err == nil {
		t.Fatal("a duplicate was accepted")
	} else if _, status, _ := ErrorKind(err); status != 400 {
		t.Fatalf("duplicate: %v", err)
	}
	// only the modes and targets Cosmos uses, checked before sending
	for _, bad := range [][2]string{{"198.51.100.0/24", "block"}, {"198.51.100.1", "whitelist"}, {"example.org", "block"}} {
		if _, err := client.CreateAccessRule(ctx, zoneID, bad[0], bad[1], ""); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if err := client.DeleteAccessRule(ctx, zoneID, rule.ID); err != nil {
		t.Fatal(err)
	}
	// deleting what is not there any more is fine
	if err := client.DeleteAccessRule(ctx, zoneID, rule.ID); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if client.CanDeleteAccessRules(ctx, zoneID) != "yes" || NewClient(goodToken).CanDeleteAccessRules(ctx, zoneID) != "no" {
		t.Fatal("CanDeleteAccessRules")
	}

	// a 429 says how long to wait
	env.fake.RateLimitNext(1, 42)
	_, _, err = client.ListAccessRules(ctx, zoneID, "", "")
	if kind, status, wait := ErrorKind(err); kind != "rate_limited" || status != 429 || wait.Seconds() != 42 {
		t.Fatalf("%v %d %v", kind, status, wait)
	}
	// and the stand-in counts like Cloudflare: 1200 in five minutes
	env.fake.SetRateLimit(3)
	for i := 0; i < 5; i++ {
		_, _, err = client.ListAccessRules(ctx, zoneID, "", "192.0.2.1")
	}
	if kind, _, _ := ErrorKind(err); kind != "rate_limited" {
		t.Fatalf("over the limit: %v", err)
	}
}

func TestAccessTargets(t *testing.T) {
	for key, want := range map[string]string{
		"198.51.100.7": "ip 198.51.100.7", "2001:db8::1": "ip6 2001:db8::1", "2001:db8:1:2::/64": "ip_range 2001:db8:1:2::/64",
		"::ffff:198.51.100.7": "ip 198.51.100.7",
	} {
		target, value, ok := AccessTarget(key)
		if !ok || target+" "+value != want {
			t.Errorf("%s: %s %s", key, target, value)
		}
	}
	for _, bad := range []string{"198.51.100.0/24", "2001:db8::/48", "2001:db8:1:2::1/64", "fe80::1%eth0", ""} {
		if _, _, ok := AccessTarget(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}
