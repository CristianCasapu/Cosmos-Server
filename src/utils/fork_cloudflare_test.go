package utils

// Fork addition: tests of fork_cloudflare.go and of what it adds to
// fork_forwarded_for.go.

import (
	"net/http/httptest"
	"reflect"
	"testing"
)

func withCloudflare(t *testing.T, change func(config *Config)) {
	t.Helper()
	previous := GetBaseMainConfig()
	config := Config{}
	change(&config)
	LoadBaseMainConfig(config)
	t.Cleanup(func() { LoadBaseMainConfig(previous) })
}

func TestCloudflareRangesAreValidated(t *testing.T) {
	good, ok := ValidCloudflareRanges([]string{"173.245.48.0/20", " 104.16.0.0/13 ", "173.245.48.0/20"}, false)
	if !ok || !reflect.DeepEqual(good, []string{"104.16.0.0/13", "173.245.48.0/20"}) {
		t.Errorf("a good list: %v %v", good, ok)
	}
	if _, ok := ValidCloudflareRanges([]string{"2606:4700::/32", "2a06:98c0::/29"}, true); !ok {
		t.Error("a good IPv6 list was refused")
	}

	bad := map[string][]string{
		"empty":                  {},
		"everything":             {"0.0.0.0/0"},
		"half of everything":     {"128.0.0.0/1"},
		"wider than /8":          {"104.0.0.0/7"},
		"private":                {"10.0.0.0/8"},
		"private, 192.168":       {"192.168.0.0/16"},
		"loopback":               {"127.0.0.0/8"},
		"link-local":             {"169.254.0.0/16"},
		"carrier NAT":            {"100.64.0.0/10"},
		"multicast":              {"224.0.0.0/8"},
		"not a network":          {"173.245.48.0/20", "example.org"},
		"an address, no network": {"173.245.48.1"},
		"host bits set":          {"173.245.48.1/20"},
		"a URL":                  {"https://www.cloudflare.com/ips-v4"},
		"IPv6 in an IPv4 list":   {"2606:4700::/32"},
		"mapped IPv4":            {"::ffff:10.0.0.0/104"},
	}
	for name, list := range bad {
		if cleaned, ok := ValidCloudflareRanges(list, false); ok {
			t.Errorf("%s: accepted as %v", name, cleaned)
		}
	}
	for name, list := range map[string][]string{
		"everything":           {"::/0"},
		"wider than /16":       {"2000::/3"},
		"unique local":         {"fd00::/8"},
		"link-local":           {"fe80::/10"},
		"loopback":             {"::1/128"},
		"IPv4 in an IPv6 list": {"173.245.48.0/20"},
	} {
		if cleaned, ok := ValidCloudflareRanges(list, true); ok {
			t.Errorf("IPv6, %s: accepted as %v", name, cleaned)
		}
	}
	long := make([]string, 201)
	for i := range long {
		long[i] = "173.245.48.0/20"
	}
	if _, ok := ValidCloudflareRanges(long, false); ok {
		t.Error("a list of 201 entries was accepted")
	}
}

func TestCloudflareAddresses(t *testing.T) {
	builtin := ForkCloudflareConfig{}
	for _, address := range []string{"173.245.48.1", "104.16.1.1:443", "172.71.255.255", "2606:4700::1", "[2a06:98c0:3600::103]:8443", "::ffff:104.16.1.1"} {
		if !IsCloudflareAddress(builtin, address) {
			t.Errorf("%s is not seen as Cloudflare's", address)
		}
	}
	for _, address := range []string{"", "example.org", "104.28.0.1", "8.8.8.8", "192.168.1.1", "10.0.0.1", "127.0.0.1", "2001:db8::1", "104.16.1", "173.245.48.1/20"} {
		if IsCloudflareAddress(builtin, address) {
			t.Errorf("%s is seen as Cloudflare's", address)
		}
	}

	v4, v6, isBuiltin := CloudflareRangeLists(builtin)
	if !isBuiltin || len(v4) != 15 || len(v6) != 7 {
		t.Errorf("built-in list: %d + %d, builtin %v", len(v4), len(v6), isBuiltin)
	}

	// a fetched list replaces the built-in one
	fetched := ForkCloudflareConfig{RangesV4: []string{"198.41.128.0/17"}, RangesV6: []string{"2606:4700::/32"}}
	if !IsCloudflareAddress(fetched, "198.41.200.1") || IsCloudflareAddress(fetched, "173.245.48.1") {
		t.Error("the fetched list is not the one used")
	}
	// a stored list that is not valid (a configuration file edited by hand) is not used at all
	for _, broken := range []ForkCloudflareConfig{
		{RangesV4: []string{"0.0.0.0/0"}, RangesV6: []string{"2606:4700::/32"}},
		{RangesV4: []string{"198.41.128.0/17"}, RangesV6: []string{"::/0"}},
		{RangesV4: []string{"198.41.128.0/17"}},
	} {
		if IsCloudflareAddress(broken, "8.8.8.8") || IsCloudflareAddress(broken, "2001:db8::1") || !IsCloudflareAddress(broken, "173.245.48.1") {
			t.Errorf("a broken stored list was used: %+v", broken)
		}
	}
}

func TestCloudflareIsTrustedOnlyWhenSwitchedOn(t *testing.T) {
	withCloudflare(t, func(config *Config) {})
	if IsTrustedProxy("173.245.48.1") {
		t.Error("Cloudflare is trusted although nothing is served through it")
	}
	withCloudflare(t, func(config *Config) { config.HTTPConfig.Cloudflare.TrustRanges = true })
	if !IsTrustedProxy("173.245.48.1") || IsTrustedProxy("8.8.8.8") || IsTrustedProxy("192.168.1.1") {
		t.Error("with the trust on: Cloudflare's addresses, and only they, are trusted")
	}
}

// One application through Cloudflare, one direct, on one server: who is the
// visitor?
func TestVisitorAddressWithShieldedAndDirectApplications(t *testing.T) {
	withCloudflare(t, func(config *Config) {
		config.HTTPConfig.Cloudflare.TrustRanges = true
		config.HTTPConfig.Cloudflare.Shielded = []string{"shielded.example.org", "example.org"}
		config.HTTPConfig.TrustedProxies = []string{"10.0.0.2"}
	})
	const cloudflare, visitor, attacker = "173.245.48.9:40000", "86.120.1.1", "198.51.100.66:50000"

	cases := []struct {
		name    string
		host    string
		peer    string
		forward []string
		cfIP    string
		want    string
	}{
		// through Cloudflare, for a shielded name: the address Cloudflare itself names is the visitor
		{"shielded, through Cloudflare", "shielded.example.org:2053", cloudflare, []string{visitor}, visitor, visitor},
		{"shielded, the panel on the apex", "example.org:2053", cloudflare, []string{visitor}, visitor, visitor},
		{"shielded, the visitor sent addresses of its own in front", "shielded.example.org", cloudflare, []string{"192.168.1.5, 127.0.0.1, " + visitor}, visitor, visitor},
		{"shielded, an IPv6 visitor", "shielded.example.org", cloudflare, []string{"2a02:2f04::77"}, "2a02:2f04::77", "2a02:2f04::77"},
		// a Worker's request through the proxy carries Cloudflare's own address for it; the list is not what decides
		{"shielded, a Worker: the list it sent is not read", "shielded.example.org", cloudflare, []string{"192.168.1.5, 198.51.100.9"}, "2a06:98c0:3600::103", "2a06:98c0:3600::103"},
		{"shielded, the list ends in an address of the visitor's choosing", "shielded.example.org", cloudflare, []string{"198.51.100.9"}, visitor, visitor},
		{"shielded, Cloudflare's header without a list", "shielded.example.org", cloudflare, nil, visitor, visitor},
		// what is not one public address is no visitor: the peer then
		{"shielded, no CF-Connecting-IP", "shielded.example.org", cloudflare, []string{visitor}, "", "173.245.48.9"},
		{"shielded, a local address claimed", "shielded.example.org", cloudflare, []string{"192.168.1.5"}, "192.168.1.5", "173.245.48.9"},
		{"shielded, the server itself claimed", "shielded.example.org", cloudflare, []string{"127.0.0.1"}, "127.0.0.1", "173.245.48.9"},
		{"shielded, a carrier NAT address claimed", "shielded.example.org", cloudflare, []string{"100.64.1.1"}, "100.64.1.1", "173.245.48.9"},
		{"shielded, a local IPv6 address claimed", "shielded.example.org", cloudflare, []string{"fd00::9"}, "fd00::9", "173.245.48.9"},
		{"shielded, two addresses in CF-Connecting-IP", "shielded.example.org", cloudflare, []string{visitor}, visitor + ", 192.168.1.5", "173.245.48.9"},
		{"shielded, text in CF-Connecting-IP", "shielded.example.org", cloudflare, []string{visitor}, "not-an-address", "173.245.48.9"},
		{"shielded, an address with a port in CF-Connecting-IP", "shielded.example.org", cloudflare, []string{visitor}, visitor + ":443", "173.245.48.9"},

		// a direct visitor cannot name an address, for either kind of name
		{"direct name, direct visitor with forged headers", "direct.example.org:2053", attacker, []string{"192.168.1.5"}, "192.168.1.5", "198.51.100.66"},
		{"direct name, direct visitor claiming Cloudflare", "direct.example.org", attacker, []string{"192.168.1.5, 173.245.48.9"}, visitor, "198.51.100.66"},
		{"shielded name, direct visitor with forged headers", "shielded.example.org", attacker, []string{visitor}, visitor, "198.51.100.66"},

		// from Cloudflare's addresses for a name that is NOT served through it
		// (a Worker fetching the direct name): nothing it says is believed
		{"direct name, asked from Cloudflare's addresses", "direct.example.org:2053", cloudflare, []string{visitor}, visitor, "173.245.48.9"},
		{"direct name, asked from Cloudflare's addresses, long list", "direct.example.org", cloudflare, []string{"192.168.1.5, " + visitor}, visitor, "173.245.48.9"},
		{"the server's address as the name, from Cloudflare's addresses", "203.0.113.10:2053", cloudflare, []string{visitor}, visitor, "173.245.48.9"},
		{"a local name, from Cloudflare's addresses", "radarr.local", cloudflare, []string{"192.168.1.5"}, "192.168.1.5", "173.245.48.9"},

		// a proxy of the admin's own in front of everything keeps working
		{"own proxy, direct name", "direct.example.org", "10.0.0.2:5000", []string{"192.168.1.5, " + visitor}, "", visitor},
		{"own proxy behind Cloudflare, shielded name", "shielded.example.org", "10.0.0.2:5000", []string{"192.168.1.5, " + visitor + ", 173.245.48.9"}, "", visitor},
		{"own proxy behind Cloudflare, shielded name, a local address claimed", "shielded.example.org", "10.0.0.2:5000", []string{"192.168.1.5, 173.245.48.9"}, "", "173.245.48.9"},
		{"own proxy behind Cloudflare, direct name", "direct.example.org", "10.0.0.2:5000", []string{visitor + ", 173.245.48.9"}, "", "173.245.48.9"},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "https://"+c.host+"/", nil)
		req.Host = c.host
		req.RemoteAddr = c.peer
		for _, line := range c.forward {
			req.Header.Add("X-Forwarded-For", line)
		}
		if c.cfIP != "" {
			req.Header.Set("CF-Connecting-IP", c.cfIP)
		}
		if got := GetClientIP(req); got != c.want {
			t.Errorf("%s: the visitor is %q, want %q", c.name, got, c.want)
		}
	}

	// with the trust switched off, Cloudflare is a peer like any other
	withCloudflare(t, func(config *Config) {
		config.HTTPConfig.Cloudflare.Shielded = []string{"shielded.example.org"}
	})
	req := httptest.NewRequest("GET", "https://shielded.example.org/", nil)
	req.RemoteAddr = cloudflare
	req.Header.Set("X-Forwarded-For", visitor)
	req.Header.Set("CF-Connecting-IP", visitor)
	if got := GetClientIP(req); got != "173.245.48.9" {
		t.Errorf("without the trust: the visitor is %q", got)
	}
}

func TestShieldedHostnames(t *testing.T) {
	list := []string{"radarr.example.org", "example.org"}
	for host, want := range map[string]bool{
		"radarr.example.org":      true,
		"RADARR.Example.Org":      true,
		"radarr.example.org:2053": true,
		"radarr.example.org.":     true,
		"example.org":             true,
		"jellyfin.example.org":    false,
		"xradarr.example.org":     false,
		"radarr.example.org.evil": false,
		"192.168.1.10":            false,
		"radarr.local":            false,
		"":                        false,
	} {
		if got := ShieldedIn(list, host); got != want {
			t.Errorf("ShieldedIn(%q) = %v, want %v", host, got, want)
		}
	}
	if ShieldedIn(nil, "radarr.example.org") {
		t.Error("shielded with an empty list")
	}
}

func TestPublicHTTPSPort(t *testing.T) {
	http := HTTPConfig{HTTPSPort: "2053"}
	if got := PublicHTTPSPort(http); got != 2053 || !IsCloudflareHTTPSPort(got) {
		t.Errorf("Cosmos' own port: %d", got)
	}
	http.UPnP = UPnPConfig{Enabled: true, ExternalHTTPSPort: "4443"}
	if got := PublicHTTPSPort(http); got != 4443 || IsCloudflareHTTPSPort(got) {
		t.Errorf("the router's port: %d", got)
	}
	http.UPnP.Enabled = false
	if got := PublicHTTPSPort(http); got != 2053 {
		t.Errorf("the router's port while forwarding is off: %d", got)
	}
	if PublicHTTPSPort(HTTPConfig{HTTPSPort: "x"}) != 0 || IsCloudflareHTTPSPort(0) || IsCloudflareHTTPSPort(80) || IsCloudflareHTTPSPort(2052) {
		t.Error("what is not an HTTPS port of Cloudflare's was taken for one")
	}
}

func TestCloudflareTokenIsMaskedAndRestored(t *testing.T) {
	stored := []DNSZoneConfig{
		{Zone: "example.org", DNSChallengeProvider: "cloudflare", DNSChallengeConfig: map[string]string{"CF_DNS_API_TOKEN": "secret-token", "CF_ZONE_API_TOKEN": ""}},
		{Zone: "example.net", DNSChallengeProvider: "ovh", DNSChallengeConfig: map[string]string{"OVH_ENDPOINT": "ovh-eu"}},
		{Zone: "example.com"},
	}
	masked := MaskCloudflareSecrets(stored)
	if masked[0].DNSChallengeConfig["CF_DNS_API_TOKEN"] != MaskedSecret || masked[0].DNSChallengeConfig["CF_ZONE_API_TOKEN"] != "" {
		t.Errorf("masked: %v", masked[0].DNSChallengeConfig)
	}
	if masked[1].DNSChallengeConfig["OVH_ENDPOINT"] != "ovh-eu" || masked[2].DNSChallengeConfig != nil {
		t.Errorf("other zones were changed: %+v", masked)
	}
	if stored[0].DNSChallengeConfig["CF_DNS_API_TOKEN"] != "secret-token" {
		t.Fatal("masking changed the stored configuration")
	}

	// what the form sends back: the mask keeps the token, a new value replaces it
	incoming := MaskCloudflareSecrets(stored)
	RestoreCloudflareSecrets(incoming, stored)
	if incoming[0].DNSChallengeConfig["CF_DNS_API_TOKEN"] != "secret-token" {
		t.Errorf("the mask did not keep the token: %v", incoming[0].DNSChallengeConfig)
	}
	incoming[0].DNSChallengeConfig["CF_DNS_API_TOKEN"] = "new-token"
	RestoreCloudflareSecrets(incoming, stored)
	if incoming[0].DNSChallengeConfig["CF_DNS_API_TOKEN"] != "new-token" {
		t.Error("a new token was replaced by the stored one")
	}
	// the mask for a zone that has nothing stored is never saved as a token
	fresh := []DNSZoneConfig{{Zone: "new.example", DNSChallengeConfig: map[string]string{"CF_DNS_API_TOKEN": MaskedSecret}}}
	RestoreCloudflareSecrets(fresh, stored)
	if fresh[0].DNSChallengeConfig["CF_DNS_API_TOKEN"] != "" {
		t.Errorf("the mask was kept as a token: %v", fresh[0].DNSChallengeConfig)
	}
}
