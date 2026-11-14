package reach

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/upnp"
	"github.com/azukaar/cosmos-server/src/utils"
)

func ips(list ...string) []net.IP {
	out := []net.IP{}
	for _, s := range list {
		out = append(out, net.ParseIP(s))
	}
	return out
}

// baseEnv: HTTPS on 2053, public address known, no UPnP, a valid certificate,
// and every name resolving to the public address.
func baseEnv() Env {
	return Env{
		HTTP:     utils.HTTPConfig{HTTPSPort: "2053", HTTPPort: "2052", Hostname: "cosmos.example.org"},
		PublicIP: "203.0.113.7",
		DNS:      func(string) DNSAnswer { return DNSAnswer{Addresses: ips("203.0.113.7")} },
		Cert: func(string) CertResult {
			return CertResult{Valid: true, NotAfter: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)}
		},
		HTTPOnly: func(string) bool { return false },
	}
}

func upnpOn(active bool, external int) upnp.Status {
	m := upnp.Mapping{Name: "HTTPS", Protocol: "TCP", InternalPort: 2053, ExternalPort: external, Active: active}
	if !active {
		m.Error = "refused by the router"
	}
	return upnp.Status{Enabled: true, ExternalIP: "203.0.113.7", Mappings: []upnp.Mapping{m}}
}

func route(host string) utils.ProxyRouteConfig {
	return utils.ProxyRouteConfig{Name: "app", UseHost: host != "", Host: host, Mode: "SERVAPP", Target: "http://app:80"}
}

func codeOf(r RouteResult, id string) string {
	for _, c := range r.Checks {
		if c.ID == id {
			return c.Status + ":" + c.Code
		}
	}
	return "-"
}

func TestEvaluate(t *testing.T) {
	cases := []struct {
		name   string
		route  func() utils.ProxyRouteConfig
		env    func(Env) Env
		state  string
		checks map[string]string
		url    string
	}{
		{name: "disabled route", route: func() utils.ProxyRouteConfig { r := route("app.example.org"); r.Disabled = true; return r },
			state: StateOff, checks: map[string]string{"access": "note:disabled"}},
		{name: ".local name", route: func() utils.ProxyRouteConfig { return route("app.local") },
			state: StateLocal, checks: map[string]string{"access": "note:localGuard", "dns": "-"}},
		{name: "private IP with port", route: func() utils.ProxyRouteConfig { return route("192.168.1.5:7201") },
			state: StateLocal},
		{name: "no host and Cosmos itself on a local name", route: func() utils.ProxyRouteConfig { return route("") },
			env: func(e Env) Env { e.HTTP.Hostname = "cosmos.local"; return e }, state: StateLocal},
		{name: "whitelist", route: func() utils.ProxyRouteConfig {
			r := route("app.example.org")
			r.WhitelistInboundIPs = []string{"198.51.100.0/24"}
			return r
		},
			state: StateLimited, checks: map[string]string{"access": "note:whitelist"}},
		{name: "constellation only", route: func() utils.ProxyRouteConfig {
			r := route("app.example.org")
			r.RestrictToConstellation = true
			return r
		},
			state: StateLimited, checks: map[string]string{"access": "note:constellation"}},
		{name: "everything right but forwarding unknown", route: func() utils.ProxyRouteConfig { return route("app.example.org") },
			state: StateUnverified, url: "https://app.example.org:2053",
			checks: map[string]string{"access": "ok:open", "dns": "ok:pointsHere", "forward": "unknown:byHand", "cert": "ok:valid"}},
		{name: "verified with UPnP forwarding 443 to 2053", route: func() utils.ProxyRouteConfig { return route("app.example.org") },
			env:   func(e Env) Env { e.UPnP = upnpOn(true, 443); return e },
			state: StateVerified, url: "https://app.example.org", checks: map[string]string{"forward": "ok:upnp"}},
		{name: "admin only is a note", route: func() utils.ProxyRouteConfig {
			r := route("app.example.org")
			r.AdminOnly = true
			r.AuthEnabled = true
			return r
		},
			env: func(e Env) Env { e.UPnP = upnpOn(true, 2053); return e }, state: StateVerified, url: "https://app.example.org:2053",
			checks: map[string]string{"access": "note:adminOnly"}},
		{name: "login asked", route: func() utils.ProxyRouteConfig { r := route("app.example.org"); r.AuthEnabled = true; return r },
			state: StateUnverified, checks: map[string]string{"access": "note:login"}},
		{name: "name does not resolve", route: func() utils.ProxyRouteConfig { return route("app.example.org") },
			env:   func(e Env) Env { e.DNS = func(string) DNSAnswer { return DNSAnswer{NotFound: true} }; return e },
			state: StateProblem, checks: map[string]string{"dns": "problem:noRecord"}},
		{name: "resolver failed", route: func() utils.ProxyRouteConfig { return route("app.example.org") },
			env:   func(e Env) Env { e.DNS = func(string) DNSAnswer { return DNSAnswer{Failed: true} }; return e },
			state: StateUnverified, checks: map[string]string{"dns": "unknown:lookupFailed"}},
		{name: "split DNS answers with the LAN address", route: func() utils.ProxyRouteConfig { return route("app.example.org") },
			env: func(e Env) Env {
				e.DNS = func(string) DNSAnswer { return DNSAnswer{Addresses: ips("192.168.1.5")} }
				return e
			},
			state: StateUnverified, checks: map[string]string{"dns": "unknown:localAnswer"}},
		{name: "points elsewhere", route: func() utils.ProxyRouteConfig { return route("app.example.org") },
			env: func(e Env) Env {
				e.DNS = func(string) DNSAnswer { return DNSAnswer{Addresses: ips("198.51.100.9")} }
				return e
			},
			state: StateProblem, checks: map[string]string{"dns": "problem:pointsElsewhere"}},
		{name: "public address unknown", route: func() utils.ProxyRouteConfig { return route("app.example.org") },
			env:   func(e Env) Env { e.PublicIP = ""; return e },
			state: StateUnverified, checks: map[string]string{"dns": "unknown:publicIPUnknown"}},
		{name: "one of several addresses is ours", route: func() utils.ProxyRouteConfig { return route("app.example.org") },
			env: func(e Env) Env {
				e.DNS = func(string) DNSAnswer { return DNSAnswer{Addresses: ips("192.168.1.5", "198.51.100.9", "203.0.113.7")} }
				return e
			},
			state: StateUnverified, checks: map[string]string{"dns": "ok:pointsHere"}},
		{name: "AAAA to the server's own IPv6", route: func() utils.ProxyRouteConfig { return route("app.example.org") },
			env: func(e Env) Env {
				e.PublicIP = ""
				e.OwnIPs = ips("2001:db8::5")
				e.UPnP = upnpOn(true, 2053)
				e.DNS = func(string) DNSAnswer { return DNSAnswer{Addresses: ips("2001:db8::5")} }
				return e
			},
			state: StateUnverified, checks: map[string]string{"dns": "ok:pointsHere", "forward": "unknown:ipv6Firewall"}},
		{name: "UPnP failed", route: func() utils.ProxyRouteConfig { return route("app.example.org") },
			env:   func(e Env) Env { e.UPnP = upnpOn(false, 2053); return e },
			state: StateProblem, checks: map[string]string{"forward": "problem:upnpFailed"}},
		{name: "router behind carrier NAT", route: func() utils.ProxyRouteConfig { return route("app.example.org") },
			env: func(e Env) Env {
				e.UPnP = upnpOn(true, 2053)
				e.UPnP.ExternalIP = "100.64.3.4"
				e.UPnP.Warning = "not public"
				return e
			},
			state: StateProblem, checks: map[string]string{"forward": "problem:routerNotPublic"}},
		{name: "route on a port of its own", route: func() utils.ProxyRouteConfig { return route("app.example.org:8443") },
			env:   func(e Env) Env { e.UPnP = upnpOn(true, 443); return e },
			state: StateUnverified, url: "https://app.example.org:8443", checks: map[string]string{"forward": "unknown:ownPort"}},
		// Fork addition (router port forwarding): a host with a port only answers that port
		{name: "host carries the external port", route: func() utils.ProxyRouteConfig { return route("app.example.org:8443") },
			env:   func(e Env) Env { e.UPnP = upnpOn(true, 8443); return e },
			state: StateVerified, url: "https://app.example.org:8443", checks: map[string]string{"forward": "ok:upnp"}},
		{name: "host carries the internal port, the router forwards another", route: func() utils.ProxyRouteConfig { return route("app.example.org:2053") },
			env:   func(e Env) Env { e.UPnP = upnpOn(true, 8443); return e },
			state: StateProblem, checks: map[string]string{"forward": "problem:hostPort"}},
		{name: "host carries the port, forwarded as it is", route: func() utils.ProxyRouteConfig { return route("app.example.org:2053") },
			env:   func(e Env) Env { e.UPnP = upnpOn(true, 2053); return e },
			state: StateVerified, url: "https://app.example.org:2053", checks: map[string]string{"forward": "ok:upnp"}},
		{name: "an app's mapping is not Cosmos' port", route: func() utils.ProxyRouteConfig { return route("app.example.org") },
			env: func(e Env) Env {
				e.UPnP = upnp.Status{Enabled: false, ExternalIP: "203.0.113.7", Apps: []upnp.Mapping{{Kind: "app", Name: "x", Protocol: "TCP", InternalPort: 2053, ExternalPort: 2053, Active: true}}}
				return e
			},
			state: StateUnverified, url: "https://app.example.org:2053", checks: map[string]string{"forward": "unknown:byHand"}},
		{name: "self-signed certificate", route: func() utils.ProxyRouteConfig { return route("app.example.org") },
			env: func(e Env) Env {
				e.Cert = func(string) CertResult { return CertResult{Problem: "selfSigned"} }
				return e
			},
			state: StateProblem, checks: map[string]string{"cert": "problem:selfSigned"}},
		{name: "plain HTTP zone", route: func() utils.ProxyRouteConfig { return route("app.example.org") },
			env:   func(e Env) Env { e.HTTPOnly = func(string) bool { return true }; return e },
			state: StateUnverified, url: "http://app.example.org:2052",
			checks: map[string]string{"cert": "note:plainHTTP", "forward": "unknown:byHand"}},
		{name: "path prefix on Cosmos' own hostname", route: func() utils.ProxyRouteConfig {
			r := route("")
			r.UsePathPrefix, r.PathPrefix = true, "/app"
			return r
		}, state: StateUnverified, url: "https://cosmos.example.org:2053/app"},
		{name: "too many names", route: func() utils.ProxyRouteConfig { return route("app.example.org") },
			env:   func(e Env) Env { e.DNS = func(string) DNSAnswer { return DNSAnswer{Skipped: "tooMany"} }; return e },
			state: StateUnverified, checks: map[string]string{"dns": "unknown:tooMany"}},
		{name: "public IP host, public address unknown", route: func() utils.ProxyRouteConfig { return route("198.51.100.9") },
			env:   func(e Env) Env { e.PublicIP = ""; e.HTTPOnly = func(string) bool { return true }; return e },
			state: StateUnverified, url: "http://198.51.100.9:2052", checks: map[string]string{"dns": "unknown:ipPublicIPUnknown"}},
		{name: "public IP host that is not ours", route: func() utils.ProxyRouteConfig { return route("198.51.100.9") },
			state: StateProblem, checks: map[string]string{"dns": "problem:ipElsewhere"}},
		{name: "public IP host that is ours", route: func() utils.ProxyRouteConfig { return route("203.0.113.7") },
			state: StateProblem, checks: map[string]string{"dns": "ok:ipIsServer", "cert": "problem:ipAddress"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := baseEnv()
			if c.env != nil {
				env = c.env(env)
			}
			got := Evaluate(c.route(), env)
			if got.State != c.state {
				t.Fatalf("state %s, want %s (%+v)", got.State, c.state, got.Checks)
			}
			for id, want := range c.checks {
				if have := codeOf(got, id); have != want {
					t.Errorf("%s: %s, want %s", id, have, want)
				}
			}
			if c.url != "" && got.URL != c.url {
				t.Errorf("url %q, want %q", got.URL, c.url)
			}
			if (got.State == StateVerified || got.State == StateUnverified) && got.URL == "" {
				t.Errorf("no link for state %s", got.State)
			}
			if (got.State == StateProblem || got.State == StateLocal || got.State == StateOff || got.State == StateLimited) && got.URL != "" {
				t.Errorf("a link for state %s", got.State)
			}
		})
	}
}

func TestValidHostname(t *testing.T) {
	for name, want := range map[string]bool{
		"app.example.org": true, "a-b.c": true, "app": false, "": false, "1.2.3.4": false,
		"bad_name.org": false, "-a.org": false, "a..org": false, strings.Repeat("a", 64) + ".org": false,
		"évil.org": false, "a.org/x": false,
	} {
		if ValidHostname(name) != want {
			t.Errorf("%q: %v", name, !want)
		}
	}
}

func TestIsLocalAddressMatchesTheGuard(t *testing.T) {
	for host, want := range map[string]bool{
		"app.local": true, "10.0.0.1": true, "172.16.0.1": true, "192.168.1.1": true, "127.0.0.1": true,
		"169.254.1.1": true, "app.example.org": false, "8.8.8.8": false, "": false,
	} {
		if isLocalAddress(host) != want {
			t.Errorf("%q: %v", host, !want)
		}
	}
}
