package utils

// Fork addition: tests of fork_origin_pulls.go and of the certificate built
// into fork_cloudflare_origin_ca.go.

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/cloudflare/cffake"
)

// The certificate this server trusts as Cloudflare's own is the one its
// source says it is, and it is still valid.
func TestOriginPullSharedCertificate(t *testing.T) {
	cert, err := CloudflareOriginPullCA()
	if err != nil {
		t.Fatalf("the built-in certificate does not parse: %v", err)
	}
	if got := CertificateFingerprint(cert.Raw); got != CloudflareOriginPullCAFingerprint {
		t.Errorf("fingerprint %s, the source notes %s", got, CloudflareOriginPullCAFingerprint)
	}
	noted, err := time.Parse(time.RFC3339, CloudflareOriginPullCANotAfter)
	if err != nil || !cert.NotAfter.Equal(noted) {
		t.Errorf("expires %s, the source notes %s", cert.NotAfter.UTC().Format(time.RFC3339), CloudflareOriginPullCANotAfter)
	}
	if !cert.IsCA || cert.Subject.CommonName != "origin-pull.cloudflare.net" {
		t.Errorf("not Cloudflare's origin-pull authority: %s (CA: %v)", cert.Subject, cert.IsCA)
	}
	if left := time.Until(cert.NotAfter); left <= 0 {
		t.Fatalf("the built-in certificate expired on %s: put Cloudflare's current one into fork_cloudflare_origin_ca.go", cert.NotAfter.UTC().Format("2006-01-02"))
	} else if left < 180*24*time.Hour {
		t.Logf("WARNING: the built-in certificate expires in %d days (%s): Cloudflare's next one has to be added before then", int(left.Hours()/24), cert.NotAfter.UTC().Format("2006-01-02"))
	}
	// and it is what the "shared" kind verifies against, alone
	pool := originPullPool(ForkOriginPullsConfig{Kind: OriginPullShared})
	if pool == nil || !pool.Equal(poolOf(cert)) {
		t.Error("the shared kind does not trust exactly the built-in certificate")
	}
}

func poolOf(certs ...*x509.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	for _, cert := range certs {
		pool.AddCert(cert)
	}
	return pool
}

func presented(cert tls.Certificate) *tls.ConnectionState {
	return &tls.ConnectionState{PeerCertificates: cffake.Presented(cert)}
}

func TestOriginPullCheck(t *testing.T) {
	mine := cffake.NewAuthority("this server's authority")
	next := cffake.NewAuthority("this server's next authority")
	other := cffake.NewAuthority("somebody else's authority")
	chained := mine.Intermediate("an intermediate")

	own := ForkCloudflareConfig{OriginPulls: ForkOriginPullsConfig{Mode: OriginPullEnforce, Kind: OriginPullOwn, Own: []ForkOriginPullCert{{CA: mine.PEM}}}}
	both := own
	both.OriginPulls.Own = []ForkOriginPullCert{{CA: mine.PEM}, {CA: next.PEM}}
	shared := ForkCloudflareConfig{OriginPulls: ForkOriginPullsConfig{Mode: OriginPullEnforce, Kind: OriginPullShared}}
	nothing := ForkCloudflareConfig{OriginPulls: ForkOriginPullsConfig{Mode: OriginPullEnforce, Kind: OriginPullOwn}}
	garbage := ForkCloudflareConfig{OriginPulls: ForkOriginPullsConfig{Mode: OriginPullEnforce, Kind: OriginPullOwn, Own: []ForkOriginPullCert{{CA: "not a certificate"}}}}
	// a certificate that is no authority is not accepted as an issuer, even from the configuration
	leafAsIssuer := ForkCloudflareConfig{OriginPulls: ForkOriginPullsConfig{Mode: OriginPullEnforce, Kind: OriginPullOwn,
		Own: []ForkOriginPullCert{{CA: pemOf(other.Issue(cffake.Leaf{}))}}}}

	cases := []struct {
		name  string
		cf    ForkCloudflareConfig
		state *tls.ConnectionState
		want  string
	}{
		{"the expected certificate", own, presented(mine.Issue(cffake.Leaf{})), ""},
		{"the expected certificate, through an intermediate it sent along", own, presented(chained.Issue(cffake.Leaf{})), ""},
		{"during a change: the old one", both, presented(mine.Issue(cffake.Leaf{})), ""},
		{"during a change: the new one", both, presented(next.Issue(cffake.Leaf{})), ""},
		{"a certificate of another authority", own, presented(other.Issue(cffake.Leaf{})), OriginPullWrongCert},
		{"the next authority's, before it is accepted", own, presented(next.Issue(cffake.Leaf{})), OriginPullWrongCert},
		{"expired", own, presented(mine.Issue(cffake.Leaf{Expired: true})), OriginPullExpired},
		{"a server's certificate, not a client's", own, presented(mine.Issue(cffake.Leaf{ServerOnly: true})), OriginPullWrongCert},
		{"none", own, &tls.ConnectionState{}, OriginPullNoCert},
		{"plain HTTP", own, nil, OriginPullNotHTTPS},
		{"the authority's own certificate presented as the client's", own, &tls.ConnectionState{PeerCertificates: []*x509.Certificate{mine.Cert}}, OriginPullWrongCert},
		{"own certificate while Cloudflare's is expected", shared, presented(mine.Issue(cffake.Leaf{})), OriginPullWrongCert},
		{"no certificate of the server's own yet", nothing, presented(mine.Issue(cffake.Leaf{})), OriginPullWrongCert},
		{"an unreadable issuer in the configuration", garbage, presented(mine.Issue(cffake.Leaf{})), OriginPullWrongCert},
		{"a certificate that is no authority as the issuer", leafAsIssuer, presented(other.Issue(cffake.Leaf{})), OriginPullWrongCert},
		{"an unknown kind", ForkCloudflareConfig{OriginPulls: ForkOriginPullsConfig{Mode: OriginPullEnforce, Kind: "any"}}, presented(mine.Issue(cffake.Leaf{})), OriginPullWrongCert},
	}
	for _, c := range cases {
		problem, issuer := OriginPullCheck(c.cf, c.state)
		if problem != c.want {
			t.Errorf("%s: %q, want %q", c.name, problem, c.want)
		}
		if (problem == "") != (issuer != "") {
			t.Errorf("%s: issuer %q with problem %q", c.name, issuer, problem)
		}
		// asked again (the verdict is remembered): the same answer
		if again, _ := OriginPullCheck(c.cf, c.state); again != problem {
			t.Errorf("%s: %q the second time, %q the first", c.name, again, problem)
		}
	}

	// the same certificate is judged anew when the issuers change
	leaf := presented(mine.Issue(cffake.Leaf{}))
	if problem, _ := OriginPullCheck(own, leaf); problem != "" {
		t.Fatalf("accepted certificate: %q", problem)
	}
	if problem, _ := OriginPullCheck(shared, leaf); problem != OriginPullWrongCert {
		t.Errorf("after the kind changed the remembered verdict was used: %q", problem)
	}
	if _, issuer := OriginPullCheck(both, presented(next.Issue(cffake.Leaf{}))); issuer != CertificateFingerprint(next.Cert.Raw) {
		t.Errorf("verified against %s, want the next authority", issuer)
	}
}

func b64(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }

func pemOf(cert tls.Certificate) string {
	return "-----BEGIN CERTIFICATE-----\n" + base64.StdEncoding.EncodeToString(cert.Certificate[0]) + "\n-----END CERTIFICATE-----\n"
}

func TestOriginPullModeAndWhoIsAsked(t *testing.T) {
	mine := cffake.NewAuthority("this server's authority")
	set := func(pulls ForkOriginPullsConfig) {
		config := Config{}
		config.HTTPConfig.Cloudflare = ForkCloudflareConfig{Shielded: []string{"radarr.example.org"}, TrustRanges: true, OriginPulls: pulls}
		LoadBaseMainConfig(config)
	}
	previous := GetBaseMainConfig()
	t.Cleanup(func() { LoadBaseMainConfig(previous) })

	const cloudflare, cloudflareV6, stranger, local = "173.245.48.9:40000", "[2606:4700::9]:40000", "198.51.100.66:50000", "192.168.1.23:51000"
	everyone := []string{cloudflare, cloudflareV6, stranger, local, "127.0.0.1:1", ""}

	// off, which is how every server starts: nobody is asked
	set(ForkOriginPullsConfig{})
	for _, peer := range everyone {
		if OriginPullAsk(peer) != nil {
			t.Errorf("with the option off, %q is asked for a certificate", peer)
		}
	}
	// a kind and certificates alone switch nothing on
	set(ForkOriginPullsConfig{Kind: OriginPullOwn, Own: []ForkOriginPullCert{{CA: mine.PEM}}})
	if OriginPullAsk(cloudflare) != nil {
		t.Error("a certificate was made, the option is off: Cloudflare's peer is asked")
	}

	for _, mode := range []string{OriginPullTest, OriginPullEnforce} {
		set(ForkOriginPullsConfig{Mode: mode, Kind: OriginPullOwn, Own: []ForkOriginPullCert{{CA: mine.PEM}}})
		for _, peer := range []string{cloudflare, cloudflareV6} {
			pool := OriginPullAsk(peer)
			if pool == nil || !pool.Equal(poolOf(mine.Cert)) {
				t.Errorf("%s: %q is not asked for exactly this server's certificate", mode, peer)
			}
		}
		for _, peer := range []string{stranger, local, "127.0.0.1:1", ""} {
			if OriginPullAsk(peer) != nil {
				t.Errorf("%s: %q is asked for a certificate", mode, peer)
			}
		}
	}

	// the shared kind names Cloudflare's own certificate
	set(ForkOriginPullsConfig{Mode: OriginPullTest, Kind: OriginPullShared})
	builtin, _ := CloudflareOriginPullCA()
	if pool := OriginPullAsk(cloudflare); pool == nil || !pool.Equal(poolOf(builtin)) {
		t.Error("the shared kind does not ask for Cloudflare's own certificate")
	}

	// the mode in force
	now := time.Now()
	modes := map[string]struct {
		pulls ForkOriginPullsConfig
		want  string
	}{
		"nothing set":                      {ForkOriginPullsConfig{}, OriginPullOff},
		"an unknown mode":                  {ForkOriginPullsConfig{Mode: "on"}, OriginPullOff},
		"test":                             {ForkOriginPullsConfig{Mode: OriginPullTest}, OriginPullTest},
		"enforce, confirmed":               {ForkOriginPullsConfig{Mode: OriginPullEnforce, Confirmed: true, ConfirmBy: now.Add(-time.Hour)}, OriginPullEnforce},
		"enforce, waiting to be confirmed": {ForkOriginPullsConfig{Mode: OriginPullEnforce, ConfirmBy: now.Add(time.Minute)}, OriginPullEnforce},
		"enforce, never confirmed":         {ForkOriginPullsConfig{Mode: OriginPullEnforce, ConfirmBy: now.Add(-time.Second)}, OriginPullTest},
	}
	for name, c := range modes {
		if got := OriginPullMode(ForkCloudflareConfig{OriginPulls: c.pulls}); got != c.want {
			t.Errorf("%s: mode %q, want %q", name, got, c.want)
		}
	}
}

// While origin pulls are enforced, what a peer from Cloudflare's addresses
// says about the visitor counts only on a connection that presented the
// expected certificate.
func TestVisitorAddressNeedsTheCertificateWhileEnforcing(t *testing.T) {
	mine := cffake.NewAuthority("this server's authority")
	other := cffake.NewAuthority("somebody else's authority")
	previous := GetBaseMainConfig()
	t.Cleanup(func() { LoadBaseMainConfig(previous) })

	const cloudflare, visitor = "173.245.48.9", "86.120.1.1"
	request := func(state *tls.ConnectionState) *http.Request {
		req := httptest.NewRequest("GET", "https://radarr.example.org:2053/", nil)
		req.Host = "radarr.example.org:2053"
		req.RemoteAddr = cloudflare + ":40000"
		req.Header.Set("CF-Connecting-IP", visitor)
		req.Header.Set("X-Forwarded-For", visitor)
		req.TLS = state
		return req
	}
	good, wrong, expired := presented(mine.Issue(cffake.Leaf{})), presented(other.Issue(cffake.Leaf{})), presented(mine.Issue(cffake.Leaf{Expired: true}))

	cases := []struct {
		name  string
		mode  string
		state *tls.ConnectionState
		want  string
	}{
		{"off: believed as before", OriginPullOff, &tls.ConnectionState{}, visitor},
		{"test, no certificate: believed, nothing is enforced yet", OriginPullTest, &tls.ConnectionState{}, visitor},
		{"test, a wrong certificate: believed, nothing is enforced yet", OriginPullTest, wrong, visitor},
		{"enforce, the expected certificate", OriginPullEnforce, good, visitor},
		{"enforce, no certificate", OriginPullEnforce, &tls.ConnectionState{}, cloudflare},
		{"enforce, another authority's certificate", OriginPullEnforce, wrong, cloudflare},
		{"enforce, an expired certificate", OriginPullEnforce, expired, cloudflare},
		{"enforce, plain HTTP", OriginPullEnforce, nil, cloudflare},
	}
	for _, c := range cases {
		config := Config{}
		config.HTTPConfig.Cloudflare = ForkCloudflareConfig{
			Shielded: []string{"radarr.example.org"}, TrustRanges: true,
			OriginPulls: ForkOriginPullsConfig{Mode: c.mode, Kind: OriginPullOwn, Confirmed: true, Own: []ForkOriginPullCert{{CA: mine.PEM}}},
		}
		LoadBaseMainConfig(config)
		if got := GetClientIP(request(c.state)); got != c.want {
			t.Errorf("%s: the visitor is %q, want %q", c.name, got, c.want)
		}
	}
}

func TestOriginPullCounts(t *testing.T) {
	ResetOriginPullCounts()
	t.Cleanup(ResetOriginPullCounts)
	before := time.Now()

	CountOriginPull("radarr.example.org", "", "issuer-a")
	CountOriginPull("radarr.example.org", "", "issuer-a")
	CountOriginPull("radarr.example.org", OriginPullNoCert, "")
	CountOriginPull("example.org", OriginPullWrongCert, "")
	CountOriginPull("example.org", OriginPullWrongCert, "")

	counts := OriginPullSnapshot()
	if counts.Verified != 2 || counts.Failed != 3 || counts.VerifiedLastMinute != 2 || counts.FailedLastMinute != 3 {
		t.Errorf("totals: %+v", counts)
	}
	if counts.Problems[OriginPullNoCert] != 1 || counts.Problems[OriginPullWrongCert] != 2 || len(counts.Problems) != 2 {
		t.Errorf("by reason: %v", counts.Problems)
	}
	if counts.Since.Before(before.Add(-time.Second)) || counts.Issuers["issuer-a"].Before(before.Add(-time.Second)) {
		t.Errorf("times: since %v, issuers %v", counts.Since, counts.Issuers)
	}
	if len(counts.Hosts) != 2 || counts.Hosts[0].Host != "example.org" || counts.Hosts[1].Host != "radarr.example.org" {
		t.Fatalf("hosts: %+v", counts.Hosts)
	}
	apex, radarr := counts.Hosts[0], counts.Hosts[1]
	if apex.Verified != 0 || apex.Failed != 2 || apex.LastProblem != OriginPullWrongCert || !apex.FailedLastMinute || apex.LastVerified != nil || apex.LastFailed == nil {
		t.Errorf("example.org: %+v", apex)
	}
	if radarr.Verified != 2 || radarr.Failed != 1 || radarr.LastVerified == nil || radarr.LastProblem != OriginPullNoCert {
		t.Errorf("radarr.example.org: %+v", radarr)
	}

	// what is kept is bounded, whatever is counted
	for i := 0; i < CloudflareMaxNames+50; i++ {
		CountOriginPull("host-"+b64([]byte{byte(i), byte(i >> 8)})+".example.org", OriginPullNoCert, "")
	}
	for i := 0; i < 50; i++ {
		CountOriginPull("radarr.example.org", "", "issuer-"+b64([]byte{byte(i)}))
	}
	counts = OriginPullSnapshot()
	if len(counts.Hosts) > CloudflareMaxNames || len(counts.Issuers) > 2*OriginPullMaxOwn {
		t.Errorf("%d hosts and %d issuers are kept", len(counts.Hosts), len(counts.Issuers))
	}

	ResetOriginPullCounts()
	if counts = OriginPullSnapshot(); counts.Verified != 0 || counts.Failed != 0 || len(counts.Hosts) != 0 || len(counts.Problems) != 0 || counts.FailedLastMinute != 0 {
		t.Errorf("after a reset: %+v", counts)
	}
}

// An event about a request knows whether its address is the peer's or
// somebody's report, and reported addresses are remembered for what is
// decided without a request at hand.
func TestEventsKnowWhetherTheirAddressWasReported(t *testing.T) {
	previous := GetBaseMainConfig()
	t.Cleanup(func() { LoadBaseMainConfig(previous) })
	config := Config{}
	config.HTTPConfig.Cloudflare = ForkCloudflareConfig{Shielded: []string{"radarr.example.org"}, TrustRanges: true}
	config.HTTPConfig.TrustedProxies = []string{"192.0.2.10"}
	LoadBaseMainConfig(config)
	events := captureSecurity(t)
	// what earlier tests left
	forwardedClients.Lock()
	forwardedClients.seen = map[string]int64{}
	forwardedClients.Unlock()

	request := func(host string, peer string, headers map[string]string) *http.Request {
		req := httptest.NewRequest("GET", "https://"+host+"/", nil)
		req.Host = host
		req.RemoteAddr = peer
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		return req
	}
	cases := []struct {
		name      string
		req       *http.Request
		ip        string
		forwarded bool
	}{
		{"through Cloudflare, shielded name", request("radarr.example.org", "173.245.48.9:40000", map[string]string{"CF-Connecting-IP": "86.120.1.1"}), "86.120.1.1", true},
		{"from Cloudflare's addresses, direct name", request("jellyfin.example.org", "173.245.48.9:40000", map[string]string{"CF-Connecting-IP": "86.120.1.2"}), "173.245.48.9", false},
		{"a direct visitor writing the headers", request("jellyfin.example.org", "198.51.100.66:50000", map[string]string{"CF-Connecting-IP": "86.120.1.3", "X-Forwarded-For": "86.120.1.3"}), "198.51.100.66", false},
		{"through a trusted proxy", request("jellyfin.example.org", "192.0.2.10:50000", map[string]string{"X-Forwarded-For": "86.120.1.4"}), "86.120.1.4", true},
		{"a device at home", request("radarr.example.org", "192.168.1.23:51000", nil), "192.168.1.23", false},
	}
	for _, c := range cases {
		RecordSecurity(c.req, SecLoginFailed, "admin")
	}
	got := events()
	if len(got) != len(cases) {
		t.Fatalf("%d events, want %d", len(got), len(cases))
	}
	for i, c := range cases {
		if got[i].IP != c.ip || got[i].Forwarded != c.forwarded {
			t.Errorf("%s: address %q reported %v, want %q %v", c.name, got[i].IP, got[i].Forwarded, c.ip, c.forwarded)
		}
	}

	for address, want := range map[string]bool{
		"86.120.1.1": true, "86.120.1.4": true,
		"86.120.1.2": false, "86.120.1.3": false, "198.51.100.66": false, "173.245.48.9": false, "192.168.1.23": false, "": false,
	} {
		if WasForwardedClient(address) != want {
			t.Errorf("%q remembered as reported: %v, want %v", address, !want, want)
		}
	}

	// what is remembered is bounded
	for i := 0; i < forwardedClientsMax+500; i++ {
		noteForwardedClient("198.18." + strconv.Itoa(i/256) + "." + strconv.Itoa(i%256))
	}
	forwardedClients.Lock()
	kept := len(forwardedClients.seen)
	forwardedClients.Unlock()
	if kept > forwardedClientsMax {
		t.Errorf("%d reported addresses are remembered, more than %d", kept, forwardedClientsMax)
	}

	// Cloudflare's own addresses are never acted on while a name is served through it
	if !CloudflareNeverBan("173.245.48.9") || !CloudflareNeverBan("2606:4700::9") || CloudflareNeverBan("198.51.100.66") {
		t.Error("CloudflareNeverBan with a shielded name")
	}
	config.HTTPConfig.Cloudflare.Shielded = nil
	LoadBaseMainConfig(config)
	if CloudflareNeverBan("173.245.48.9") {
		t.Error("with nothing served through Cloudflare its addresses are still left out")
	}
}
