package reach

// Fork addition: what the reachability check says about hostnames at
// Cloudflare (the record's state, the shield, the port).

import (
	"net"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/upnp"
	"github.com/azukaar/cosmos-server/src/utils"
)

func cloudflareEnv(info CloudflareInfo, answer ...string) Env {
	ips := []net.IP{}
	for _, text := range answer {
		ips = append(ips, net.ParseIP(text))
	}
	_, cloudflare, _ := net.ParseCIDR("104.16.0.0/13")
	return Env{
		HTTP:     utils.HTTPConfig{Hostname: "192.168.1.10", HTTPSPort: "2053", HTTPPort: "2052"},
		PublicIP: "203.0.113.10",
		UPnP: upnp.Status{Enabled: true, ExternalIP: "203.0.113.10", Mappings: []upnp.Mapping{
			{Name: "HTTPS", InternalPort: 2053, ExternalPort: 2053, Active: true},
		}},
		DNS:          func(string) DNSAnswer { return DNSAnswer{Addresses: ips} },
		Cert:         func(string) CertResult { return CertResult{Valid: true, NotAfter: time.Now().Add(time.Hour)} },
		Cloudflare:   func(string) CloudflareInfo { return info },
		IsCloudflare: func(ip net.IP) bool { return cloudflare.Contains(ip) },
	}
}

func checkOf(result RouteResult, id string) Check {
	for _, check := range result.Checks {
		if check.ID == id {
			return check
		}
	}
	return Check{}
}

func TestReachabilityOfHostnamesAtCloudflare(t *testing.T) {
	route := utils.ProxyRouteConfig{Name: "radarr-example.org", UseHost: true, Host: "radarr.example.org", Mode: "SERVAPP"}
	managed := CloudflareInfo{Managed: true, RecordState: "ok"}
	shielded := CloudflareInfo{Managed: true, RecordState: "ok", Shielded: true}

	// served directly, the record points here
	result := Evaluate(route, cloudflareEnv(managed, "203.0.113.10"))
	if result.State != StateVerified || checkOf(result, "record").Code != "exists" || checkOf(result, "dns").Code != "pointsHere" ||
		result.URL != "https://radarr.example.org:2053" {
		t.Errorf("direct: %+v", result)
	}
	if checkOf(result, "shield").Code != "" {
		t.Errorf("a direct name has a shield check: %+v", result.Checks)
	}

	// served through Cloudflare: its addresses are the right answer
	result = Evaluate(route, cloudflareEnv(shielded, "104.16.5.5", "104.17.6.6"))
	if result.State != StateVerified || checkOf(result, "dns").Code != "throughShield" || checkOf(result, "dns").Status != CheckOK ||
		checkOf(result, "shield").Code != "on" || result.URL != "https://radarr.example.org:2053" {
		t.Errorf("shielded: %+v", result)
	}

	// the switch was just flipped: the name still points here
	result = Evaluate(route, cloudflareEnv(shielded, "203.0.113.10"))
	if result.State != StateUnverified || checkOf(result, "dns").Code != "shieldNotYet" {
		t.Errorf("shielded, old answer: %+v", result)
	}

	// proxied at Cloudflare although Cosmos serves it directly
	result = Evaluate(route, cloudflareEnv(managed, "104.16.5.5"))
	if result.State != StateUnverified || checkOf(result, "dns").Code != "behindCloudflare" {
		t.Errorf("direct but proxied: %+v", result)
	}

	// a port Cloudflare does not proxy
	env := cloudflareEnv(shielded, "104.16.5.5")
	env.HTTP.HTTPSPort = "8444"
	env.UPnP.Mappings[0] = upnp.Mapping{Name: "HTTPS", InternalPort: 8444, ExternalPort: 8444, Active: true}
	result = Evaluate(route, env)
	if result.State != StateProblem || checkOf(result, "shield").Code != "port" || checkOf(result, "shield").Params["port"] != "8444" {
		t.Errorf("shielded on a port Cloudflare does not proxy: %+v", result)
	}
	// the router's port is the one that counts
	env.UPnP.Mappings[0].ExternalPort = 2053
	if result = Evaluate(route, env); checkOf(result, "shield").Code != "on" {
		t.Errorf("shielded, the router maps a proxied port: %+v", result)
	}

	// the record's own state
	for state, want := range map[string][2]string{
		"pending":   {"pending", CheckUnknown},
		"conflict":  {"foreign", CheckNote},
		"protected": {"foreign", CheckNote},
	} {
		result = Evaluate(route, cloudflareEnv(CloudflareInfo{Managed: true, RecordState: state}, "203.0.113.10"))
		if check := checkOf(result, "record"); check.Code != want[0] || check.Status != want[1] {
			t.Errorf("record %s: %+v", state, check)
		}
	}
	for reason, code := range map[string]string{"invalid_token": "error_invalid_token", "forbidden": "error_forbidden", "<script>": "error_other", "": "error_other"} {
		result = Evaluate(route, cloudflareEnv(CloudflareInfo{Managed: true, RecordState: "error", RecordReason: reason}))
		if check := checkOf(result, "record"); check.Code != code || check.Status != CheckProblem || result.State != StateProblem {
			t.Errorf("record error %q: %+v", reason, check)
		}
	}

	// a name Cosmos does not manage at Cloudflare has no record check
	result = Evaluate(route, cloudflareEnv(CloudflareInfo{}, "203.0.113.10"))
	if checkOf(result, "record").Code != "" || result.State != StateVerified {
		t.Errorf("unmanaged: %+v", result)
	}
	// without the integration nothing changes
	env = cloudflareEnv(CloudflareInfo{}, "104.16.5.5")
	env.Cloudflare, env.IsCloudflare = nil, nil
	if result = Evaluate(route, env); checkOf(result, "dns").Code != "pointsElsewhere" {
		t.Errorf("without the integration: %+v", result)
	}
}
