package reach

// Fork addition: the panel's own address in the reachability check.

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The panel is judged like a URL at Cosmos' own hostname that needs a login:
// the same checks, the same states.
func TestThePanelsOwnAddressIsEvaluated(t *testing.T) {
	// on a domain, reached through Cloudflare (the layout this was written for)
	shielded := cloudflareEnv(CloudflareInfo{Managed: true, RecordState: "protected", Shielded: true}, "104.16.1.1")
	shielded.HTTP.Hostname = "example.org"
	result := EvaluatePanel(shielded)
	if result.Route != "" || result.Host != "example.org" || !strings.HasPrefix(result.URL, "https://example.org") {
		t.Errorf("the panel's result: %+v", result)
	}
	if got := checkOf(result, "access"); got.Code != "panel" {
		t.Errorf("who may open it: %+v", got)
	}
	if checkOf(result, "dns").Code != "throughShield" || checkOf(result, "shield").Code != "on" || checkOf(result, "cert").Code != "valid" || checkOf(result, "record").Code != "foreign" {
		t.Errorf("through Cloudflare: %+v", result.Checks)
	}
	if result.State != StateVerified {
		t.Errorf("state %q, want verified", result.State)
	}

	// on a name of its own, served directly
	direct := cloudflareEnv(CloudflareInfo{}, "203.0.113.10")
	direct.HTTP.Hostname = "cosmos.example.org"
	result = EvaluatePanel(direct)
	if result.State != StateVerified || checkOf(result, "dns").Code != "pointsHere" || checkOf(result, "forward").Code != "upnp" || checkOf(result, "access").Code != "panel" {
		t.Errorf("served directly: %s %+v", result.State, result.Checks)
	}
	// the name points somewhere else: a problem, as for any URL
	elsewhere := cloudflareEnv(CloudflareInfo{}, "198.51.100.9")
	elsewhere.HTTP.Hostname = "cosmos.example.org"
	if result = EvaluatePanel(elsewhere); result.State != StateProblem || checkOf(result, "dns").Code != "pointsElsewhere" {
		t.Errorf("pointing elsewhere: %s %+v", result.State, result.Checks)
	}
	// a certificate browsers do not trust
	untrusted := cloudflareEnv(CloudflareInfo{}, "203.0.113.10")
	untrusted.HTTP.Hostname = "cosmos.example.org"
	untrusted.Cert = func(string) CertResult { return CertResult{Problem: "selfSigned"} }
	if result = EvaluatePanel(untrusted); result.State != StateProblem || checkOf(result, "cert").Code != "selfSigned" {
		t.Errorf("an untrusted certificate: %s %+v", result.State, result.Checks)
	}

	// at the server's local address, or a .local name: local, nothing to look up
	for _, host := range []string{"192.168.1.10", "cosmos.local", "192.168.1.10:2053"} {
		local := cloudflareEnv(CloudflareInfo{}, "203.0.113.10")
		local.HTTP.Hostname = host
		result = EvaluatePanel(local)
		if result.State != StateLocal || len(result.Checks) != 1 || result.Checks[0].Code != "panelLocal" {
			t.Errorf("%s: %s %+v", host, result.State, result.Checks)
		}
	}
}

func TestThePanelIsPartOfARun(t *testing.T) {
	asked := fakes(t, []utils.ProxyRouteConfig{route("app.example.org")}, func(ctx context.Context, host string) ([]net.IP, error) {
		if host == "example.org" {
			return []net.IP{net.ParseIP("198.51.100.9")}, nil
		}
		return ours(ctx, host)
	})
	dynDNSAddress = func() string { return "203.0.113.7" }

	// no hostname (a server that is not set up): no verdict on the panel, nothing looked up for it
	_, none := serve(request("GET", "admin", ""))
	if none.Panel != nil || *asked != 1 {
		t.Fatalf("without a hostname: %+v (%d lookups)", none.Panel, *asked)
	}

	config := utils.GetMainConfig()
	config.HTTPConfig.Hostname = "example.org"
	utils.LoadBaseMainConfig(config)
	cache = map[string]dnsEntry{}
	rec, admin := serve(request("GET", "admin", ""))
	if admin.Panel == nil || admin.Panel.Host != "example.org" || admin.Panel.State != StateProblem || checkOf(*admin.Panel, "dns").Code != "pointsElsewhere" {
		t.Fatalf("the panel in a run: %+v", admin.Panel)
	}
	if len(admin.Routes) != 1 || admin.Routes[0].Route != "app" {
		t.Errorf("the URLs of a run: %+v", admin.Routes)
	}
	if *asked != 3 {
		t.Errorf("%d lookups, want 3: the application's twice (the cache was emptied), the panel's once", *asked)
	}
	if !strings.Contains(rec.Body.String(), "198.51.100.9") {
		t.Errorf("an admin does not see where the panel's name points: %s", rec.Body.String())
	}
	// who is not an admin gets the verdict, not the addresses
	rec, reader := serve(request("GET", "reader", ""))
	if reader.Panel == nil || reader.Panel.State != StateProblem || strings.Contains(rec.Body.String(), "198.51.100.9") || strings.Contains(rec.Body.String(), "203.0.113.7") {
		t.Errorf("a reader: %s", rec.Body.String())
	}

	// the panel at the server's local address: nothing is looked up for it
	config.HTTPConfig.Hostname = "192.168.1.10"
	utils.LoadBaseMainConfig(config)
	before := *asked
	_, local := serve(request("GET", "admin", ""))
	if local.Panel == nil || local.Panel.State != StateLocal || *asked != before {
		t.Errorf("at a local address: %+v (%d more lookups)", local.Panel, *asked-before)
	}
}
