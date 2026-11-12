package dnsrecords

import (
	"reflect"
	"sort"
	"testing"

	"github.com/libdns/libdns"

	"github.com/azukaar/cosmos-server/src/utils"
)

func sorted(ips []string) []string {
	out := append([]string{}, ips...)
	sort.Strings(out)
	return out
}

func TestBuildDesired(t *testing.T) {
	zones := []utils.DNSZoneConfig{
		{Zone: "example.com", ManageRecords: true, WildcardRecord: true},
		{Zone: "lab.example.com", ManageRecords: true},
	}
	hosts := map[string][]string{
		"app.example.com":     {"1.1.1.1", "2.2.2.2"}, // same as the wildcard: no record of its own
		"node-2.example.com":  {"3.3.3.3"},
		"x.lab.example.com":   {"3.3.3.3"}, // belongs to the sub-zone
		"mixed.example.com":   {"192.168.1.10", "3.3.3.3"},
		"home.example.com":    {"192.168.1.10"},
		"elsewhere.other.org": {"1.1.1.1"},
	}
	wildcard := []string{"1.1.1.1", "2.2.2.2"}

	got := BuildDesired(zones, 0, hosts, wildcard)
	want := Desired{
		"*.example.com":      {"1.1.1.1", "2.2.2.2"},
		"node-2.example.com": {"3.3.3.3"},
		"mixed.example.com":  {"3.3.3.3"},
		"home.example.com":   {"192.168.1.10"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("example.com = %v, want %v", got, want)
	}

	got = BuildDesired(zones, 1, hosts, wildcard)
	want = Desired{"x.lab.example.com": {"3.3.3.3"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lab.example.com = %v, want %v", got, want)
	}
}

func TestLimitWithdrawals(t *testing.T) {
	actual := []libdns.Record{
		a("app", "1.1.1.1"), a("app", "2.2.2.2"), a("app", "3.3.3.3"), a("app", "4.4.4.4"),
		a("solo", "1.1.1.1"),
	}

	// 3 of 4 leaving at once: only half of the current set goes this pass
	got := LimitWithdrawals(Desired{"app.example.com": {"1.1.1.1"}, "solo.example.com": {"9.9.9.9"}}, "example.com", actual)
	if want := []string{"1.1.1.1", "4.4.4.4"}; !reflect.DeepEqual(sorted(got["app.example.com"]), want) {
		t.Errorf("app = %v, want %v", sorted(got["app.example.com"]), want)
	}
	// a single address moving is a change of IP, not a withdrawal
	if want := []string{"9.9.9.9"}; !reflect.DeepEqual(got["solo.example.com"], want) {
		t.Errorf("solo = %v, want %v", got["solo.example.com"], want)
	}

	// one of four leaving goes straight through
	got = LimitWithdrawals(Desired{"app.example.com": {"1.1.1.1", "2.2.2.2", "3.3.3.3"}}, "example.com", actual)
	if len(got["app.example.com"]) != 3 {
		t.Errorf("app = %v, want the 3 desired addresses", got["app.example.com"])
	}
}

func TestAdditiveOnly(t *testing.T) {
	actual := []libdns.Record{a("app", "1.1.1.1"), a("app", "2.2.2.2")}
	got := additiveOnly(Desired{"app.example.com": {"3.3.3.3"}}, "example.com", actual)

	if want := []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}; !reflect.DeepEqual(sorted(got["app.example.com"]), want) {
		t.Errorf("app = %v, want %v", sorted(got["app.example.com"]), want)
	}
}

func TestNewProviderFromLegoCredentials(t *testing.T) {
	zone := utils.DNSZoneConfig{Zone: "example.com", DNSChallengeProvider: "cloudflare", DNSChallengeConfig: map[string]string{"CF_DNS_API_TOKEN": " token "}}
	zp, err := New(zone)
	if err != nil || zp.Provider == nil || !zp.UseMarker || zp.TTL != DefaultTTL {
		t.Fatalf("cloudflare: %+v, %v", zp, err)
	}

	zone.DNSChallengeConfig = map[string]string{"CF_API_KEY": "global", "CF_API_EMAIL": "me@example.com"}
	if _, err := New(zone); err == nil {
		t.Error("the Cloudflare global key cannot manage records, expected an explicit error")
	}

	zone = utils.DNSZoneConfig{Zone: "example.com", DNSChallengeProvider: "desec", DNSChallengeConfig: map[string]string{"DESEC_TOKEN": "t"}}
	if zp, _ := New(zone); zp.TTL <= DefaultTTL {
		t.Errorf("deSEC refuses low TTLs, got %v", zp.TTL)
	}

	zone.DNSChallengeProvider = "godaddy"
	if _, err := New(zone); err == nil || Supported("godaddy") {
		t.Error("godaddy is not a supported record provider")
	}

	if zp, _ := New(utils.DNSZoneConfig{Zone: "x.duckdns.org", DNSChallengeProvider: "duckdns", DNSChallengeConfig: map[string]string{"DUCKDNS_TOKEN": "t"}}); zp.UseMarker {
		t.Error("DuckDNS cannot hold owner markers")
	}
}
