package certs

import (
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func certFor(t *testing.T, status Status, domain string) Certificate {
	t.Helper()
	for _, cert := range status.Certificates {
		if cert.Domain == domain {
			return cert
		}
	}
	t.Fatalf("no domain %s in the status", domain)
	return Certificate{}
}

func hostFor(t *testing.T, status Status, name string) Host {
	t.Helper()
	for _, host := range status.Hosts {
		if host.Host == name {
			return host
		}
	}
	t.Fatalf("no address %s in the status", name)
	return Host{}
}

func TestStatusOfADomainThroughDNS(t *testing.T) {
	env := setup(t, func(config *utils.Config) {
		config.HTTPConfig.ProxyConfig.Routes = append(config.HTTPConfig.ProxyConfig.Routes,
			utils.ProxyRouteConfig{Name: "deep", UseHost: true, Host: "a.b.example.org", Mode: "PROXY", Target: "http://127.0.0.1:1"},
			utils.ProxyRouteConfig{Name: "shielded", UseHost: true, Host: "films.example.org", Mode: "PROXY", Target: "http://127.0.0.1:1"})
		config.HTTPConfig.Cloudflare.Shielded = []string{"films.example.org"}
	})

	// nothing yet: every name is uncovered, an order is possible
	status := GetStatus()
	if status.CA.Kind != "production" || status.CA.Name != "Let's Encrypt" {
		t.Errorf("the CA: %+v", status.CA)
	}
	if strings.Join(status.CanAnswer, ",") != "example.org" {
		t.Errorf("the domains that can answer for another: %v", status.CanAnswer)
	}
	cert := certFor(t, status, "example.org")
	if cert.Kind != "acme" || cert.Method != "dns" || cert.Provider != "cloudflare" || cert.Present || !cert.CanRenew ||
		strings.Join(cert.Uncovered, ",") != "a.b.example.org,app.example.org,films.example.org" || cert.Last != nil || cert.Wait != nil {
		t.Errorf("before the first order: %+v", cert)
	}
	if host := hostFor(t, status, "app.example.org"); host.Trusted || host.Problem != "none" || host.Shielded {
		t.Errorf("an address without a certificate: %+v", host)
	}

	// a 90-day certificate for the wildcard pair, issued ten days ago
	stored := env.zoneCert(t, env.ca, "example.org", []string{"example.org", "*.example.org"}, t0.Add(-10*day), t0.Add(80*day), "app.example.org", "films.example.org")
	leaf := utils.ForkLeaf(stored.TLSCert)
	succeed("zone:example.org", t0.Add(-10*day), []string{"example.org", "*.example.org"})
	store.update("zone:example.org", func(state *certState) {
		state.IssuedSerial = leaf.SerialNumber.String()
		state.IssuedDirectory = letsEncryptProduction
	})

	status = GetStatus()
	cert = certFor(t, status, "example.org")
	if !cert.Present || cert.Trust != "public" || cert.Issuer != "Test Authority (Test Root)" || cert.Expired || cert.Warn {
		t.Errorf("the certificate: %+v", cert)
	}
	if strings.Join(cert.Names, ",") != "example.org,*.example.org" || strings.Join(cert.Uncovered, ",") != "a.b.example.org" {
		t.Errorf("covered %v, not covered %v", cert.Names, cert.Uncovered)
	}
	if !cert.NotAfter.Equal(t0.Add(80*day)) || !cert.RenewAt.Equal(t0.Add(50*day)) || cert.RenewSource != "lifetime" {
		t.Errorf("until %v, renewal %v by %s", cert.NotAfter, cert.RenewAt, cert.RenewSource)
	}
	if cert.Last == nil || !cert.Last.OK || !cert.Last.At.Equal(t0.Add(-10*day)) || cert.Last.CA != "Let's Encrypt" {
		t.Errorf("the last order: %+v", cert.Last)
	}
	if host := hostFor(t, status, "app.example.org"); !host.Trusted || host.Problem != "" || !host.Until.Equal(t0.Add(80*day)) {
		t.Errorf("a covered address: %+v", host)
	}
	if host := hostFor(t, status, "films.example.org"); !host.Shielded || !host.Trusted {
		t.Errorf("an address through Cloudflare: %+v", host)
	}
	if host := hostFor(t, status, "a.b.example.org"); host.Trusted || host.Problem != "none" {
		t.Errorf("an address the certificate does not cover: %+v", host)
	}

	// the CA gave a window: the date and its source follow it
	store.update("zone:example.org", func(state *certState) {
		state.ARI = &ariState{Serial: leaf.SerialNumber.String(), Pick: t0.Add(45 * day).Unix()}
	})
	if cert = certFor(t, GetStatus(), "example.org"); !cert.RenewAt.Equal(t0.Add(45*day)) || cert.RenewSource != "ca" {
		t.Errorf("with the CA's window: %v by %s", cert.RenewAt, cert.RenewSource)
	}

	// a failed order: reason, wait, and "Renew now" stays possible
	env.now = t0.Add(51 * day)
	fail("zone:example.org", env.now, FailDNSCredentials, 0)
	cert = certFor(t, GetStatus(), "example.org")
	if cert.Last == nil || cert.Last.OK || cert.Last.Code != FailDNSCredentials || cert.Last.Detail != "detail" {
		t.Errorf("the failed order: %+v", cert.Last)
	}
	if cert.Wait == nil || cert.Wait.Reason != waitBackoff || !cert.Wait.Until.Equal(env.now.Add(time.Hour)) || !cert.CanRenew {
		t.Errorf("the wait: %+v, renew possible %v", cert.Wait, cert.CanRenew)
	}

	// the CA said to come back later: "Renew now" is not possible
	fail("zone:example.org", env.now, FailRateLimited, 10*time.Hour)
	if cert = certFor(t, GetStatus(), "example.org"); cert.CanRenew || cert.RenewBlocked != waitRateLimited {
		t.Errorf("rate limited: can renew %v, %q", cert.CanRenew, cert.RenewBlocked)
	}

	// inside the warning window, then expired
	env.now = t0.Add(70 * day)
	if cert = certFor(t, GetStatus(), "example.org"); !cert.Warn || cert.Expired {
		t.Errorf("ten days before the end: warn %v expired %v", cert.Warn, cert.Expired)
	}
	env.now = t0.Add(81 * day)
	status = GetStatus()
	if cert = certFor(t, status, "example.org"); !cert.Warn || !cert.Expired || cert.Trust != "public" {
		t.Errorf("after the end: warn %v expired %v trust %s", cert.Warn, cert.Expired, cert.Trust)
	}
	if host := hostFor(t, status, "app.example.org"); host.Trusted || host.Problem != "expired" {
		t.Errorf("an address with an expired certificate: %+v", host)
	}

	// while an order runs
	running.Store("zone:example.org", env.now)
	defer running.Delete("zone:example.org")
	if cert = certFor(t, GetStatus(), "example.org"); !cert.Running || cert.CanRenew || cert.RenewBlocked != "running" {
		t.Errorf("during an order: %+v", cert)
	}
}

func TestStatusSaysWhoIssuedIt(t *testing.T) {
	env := setup(t, nil)
	unknown := newAuthority(t, "Somebody Else")
	stored := env.zoneCert(t, unknown, "example.org", []string{"example.org", "*.example.org"}, t0.Add(-day), t0.Add(89*day), "app.example.org")
	status := GetStatus()
	if cert := certFor(t, status, "example.org"); cert.Trust != "private" {
		t.Errorf("a certificate of an authority browsers do not know: %s", cert.Trust)
	}
	if host := hostFor(t, status, "app.example.org"); host.Trusted || host.Problem != "selfSigned" {
		t.Errorf("its address: %+v", host)
	}
	// ordered from the staging authority, or from a test authority: said so,
	// even on a machine that trusts that authority
	serial := utils.ForkLeaf(stored.TLSCert).SerialNumber.String()
	systemRoots = unknown.pool()
	for directory, want := range map[string]string{letsEncryptStaging: "staging", "https://pebble:14000/dir": "test", letsEncryptProduction: "public"} {
		store.update("zone:example.org", func(state *certState) { state.IssuedSerial, state.IssuedDirectory = serial, directory })
		if cert := certFor(t, GetStatus(), "example.org"); cert.Trust != want {
			t.Errorf("issued at %s: %s, want %s", directory, cert.Trust, want)
		}
	}
}

func TestStatusOfTheOtherKindsOfDomain(t *testing.T) {
	env := setup(t, func(config *utils.Config) {
		config.HTTPConfig.DNSZones = append(config.HTTPConfig.DNSZones,
			utils.DNSZoneConfig{Zone: "example.net", HTTPSCertificateMode: "LETSENCRYPT", ChallengeVia: "example.org", UseWildcardCertificate: true},
			utils.DNSZoneConfig{Zone: "plain.example.com", HTTPSCertificateMode: "DISABLED"},
			utils.DNSZoneConfig{Zone: "self.example.com", HTTPSCertificateMode: "SELFSIGNED"},
			utils.DNSZoneConfig{Zone: "nodns.example.com", HTTPSCertificateMode: "LETSENCRYPT"},
		)
		for _, host := range []string{"app.example.net", "x.y.example.net", "a.plain.example.com", "a.self.example.com", "a.nodns.example.com", "lone.example.info"} {
			config.HTTPConfig.ProxyConfig.Routes = append(config.HTTPConfig.ProxyConfig.Routes,
				utils.ProxyRouteConfig{Name: host, UseHost: true, Host: host, Mode: "PROXY", Target: "http://127.0.0.1:1"})
		}
	})
	selfPEM, _ := newAuthority(t, "Cosmos Root CA").issue([]string{"a.self.example.com"}, t0.Add(-day), t0.Add(363*day))
	change(func(config *utils.Config) { config.HTTPConfig.SelfTLSCert = selfPEM })

	status := GetStatus()
	if status.Challenges.HTTP != "impossible" || status.Challenges.Reason != "ports" || status.Challenges.HTTPSPort != 2053 || status.Challenges.HTTPPort != 2052 {
		t.Errorf("the challenges on ports 2052/2053: %+v", status.Challenges)
	}

	delegated := certFor(t, status, "example.net")
	if delegated.Method != "delegated" || delegated.Via != "example.org" || delegated.Provider != "" || !delegated.CanRenew {
		t.Errorf("a delegated domain: %+v", delegated)
	}
	if len(delegated.Records) != 2 ||
		delegated.Records[0].Record != "_acme-challenge.example.net" || delegated.Records[0].Target != "example-net._acme.example.org" ||
		delegated.Records[1].Record != "_acme-challenge.x.y.example.net" || delegated.Records[1].Target != "x-y-example-net._acme.example.org" {
		t.Errorf("the records a delegated domain needs: %+v", delegated.Records)
	}

	if plain := certFor(t, status, "plain.example.com"); plain.Kind != "none" || plain.CanRenew || plain.RenewBlocked != "notOrdered" || len(plain.Uncovered) != 0 {
		t.Errorf("a plain HTTP domain: %+v", plain)
	}
	if host := hostFor(t, status, "a.plain.example.com"); host.Problem != "plainHTTP" || host.Trusted {
		t.Errorf("its address: %+v", host)
	}
	if self := certFor(t, status, "self.example.com"); self.Kind != "selfsigned" || !self.Present || self.Trust != "private" || self.CanRenew {
		t.Errorf("a self-signed domain: %+v", self)
	}
	for _, name := range []string{"nodns.example.com", "lone.example.info"} {
		cert := certFor(t, status, name)
		if cert.Kind != "acme" || cert.Method != "http" || cert.Blocked != "noHTTPChallenge" || cert.CanRenew || cert.RenewBlocked != "noHTTPChallenge" {
			t.Errorf("%s, which would need the HTTP challenge: %+v", name, cert)
		}
	}
	if !certFor(t, status, "lone.example.info").Derived || certFor(t, status, "nodns.example.com").Derived {
		t.Error("a hostname in use is told from a domain that was set up")
	}

	// the admin says the server is reachable on 80/443: the order is
	// upstream's, at a restart
	change(func(config *utils.Config) { config.HTTPConfig.Certificates.HTTPChallenge = HTTPChallengeYes })
	status = GetStatus()
	if cert := certFor(t, status, "nodns.example.com"); cert.Blocked != "" || cert.CanRenew || cert.RenewBlocked != "needsRestart" {
		t.Errorf("with the HTTP challenge possible: %+v", cert)
	}
	_ = env

	// with HTTPS off altogether nothing has a certificate
	change(func(config *utils.Config) { config.HTTPConfig.HTTPSCertificateMode = "DISABLED" })
	for _, cert := range GetStatus().Certificates {
		if cert.Kind != "none" {
			t.Errorf("HTTPS is off, yet %s is %s", cert.Domain, cert.Kind)
		}
	}
}

func TestWhichChallengesCanWork(t *testing.T) {
	setup(t, nil)
	ports := func(https int, http int) { ExternalPorts = func() (int, int) { return https, http } }
	config := utils.GetMainConfig()

	cases := []struct {
		https, http int
		setting     string
		want        string
		reason      string
	}{
		{2053, 2052, "", "impossible", "ports"},
		{443, 80, "", "possible", "ports"},
		{443, 2052, "", "possible", "ports"}, // TLS-ALPN-01 on 443
		{2053, 80, "", "possible", "ports"},  // HTTP-01 on 80
		{8443, 8080, "", "impossible", "ports"},
		{443, 80, "no", "impossible", "admin"},
		{2053, 2052, "yes", "possible", "admin"},
		{2053, 2052, "nonsense", "impossible", "ports"},
	}
	for _, c := range cases {
		ports(c.https, c.http)
		config.HTTPConfig.Certificates.HTTPChallenge = c.setting
		got := httpChallenge(config)
		if got.HTTP != c.want || got.Reason != c.reason {
			t.Errorf("reached on %d/%d, setting %q: %s (%s), want %s (%s)", c.https, c.http, c.setting, got.HTTP, got.Reason, c.want, c.reason)
		}
		if noHTTPChallenge(config) != (c.want == "impossible") {
			t.Errorf("reached on %d/%d, setting %q: the hook disagrees", c.https, c.http, c.setting)
		}
	}
	// without the router's view: Cosmos' own ports
	ExternalPorts = nil
	config.HTTPConfig.Certificates.HTTPChallenge = ""
	config.HTTPConfig.HTTPSPort, config.HTTPConfig.HTTPPort = "443", "80"
	if got := httpChallenge(config); got.HTTP != "possible" || got.HTTPSPort != 443 {
		t.Errorf("on 443/80: %+v", got)
	}
}
