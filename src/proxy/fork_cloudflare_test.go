package proxy

// Fork addition: tests of fork_cloudflare.go, the guard of the hostnames
// served through Cloudflare.

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/cloudflare/cffake"
	"github.com/azukaar/cosmos-server/src/utils"
)

func TestShieldedHostnamesAnswerCloudflareAndTheLocalNetworkOnly(t *testing.T) {
	previous := utils.GetBaseMainConfig()
	config := utils.Config{}
	// the panel on the domain itself and one application through Cloudflare, one direct
	config.HTTPConfig.Hostname = "example.org"
	config.HTTPConfig.Cloudflare.Shielded = []string{"example.org", "radarr.example.org"}
	config.HTTPConfig.Cloudflare.TrustRanges = true
	utils.LoadBaseMainConfig(config)
	t.Cleanup(func() { utils.LoadBaseMainConfig(previous) })

	events := captureSecurity(t)
	served := 0
	guarded := CloudflareOriginGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served++
		w.WriteHeader(http.StatusNoContent)
	}))

	const cloudflare, cloudflareV6, stranger, local = "173.245.48.9:40000", "[2606:4700::9]:40000", "198.51.100.66:50000", "192.168.1.23:51000"
	cases := []struct {
		name   string
		host   string
		peer   string
		header map[string]string
		want   int
	}{
		{"through Cloudflare", "radarr.example.org:2053", cloudflare, nil, 204},
		{"through Cloudflare over IPv6", "radarr.example.org:2053", cloudflareV6, nil, 204},
		{"the panel through Cloudflare", "example.org:2053", cloudflare, nil, 204},
		{"around Cloudflare", "radarr.example.org:2053", stranger, nil, 403},
		{"the panel around Cloudflare", "example.org:2053", stranger, nil, 403},
		{"around Cloudflare, written differently", "RADARR.example.org.:2053", stranger, nil, 403},
		{"around Cloudflare, claiming to be Cloudflare", "radarr.example.org", stranger,
			map[string]string{"X-Forwarded-For": "173.245.48.9", "CF-Connecting-IP": "173.245.48.9", "X-Real-IP": "173.245.48.9", "Forwarded": "for=173.245.48.9"}, 403},
		{"around Cloudflare, claiming to be local", "radarr.example.org", stranger,
			map[string]string{"X-Forwarded-For": "192.168.1.23", "CF-Connecting-IP": "127.0.0.1", "X-Real-IP": "192.168.1.23"}, 403},

		// the owner at home: the shielded names by the local network, and the
		// ways in that are not names of Cloudflare's at all
		{"a device at home asks for the shielded name", "radarr.example.org:2053", local, nil, 204},
		{"a device at home asks for the panel's name", "example.org:2053", local, nil, 204},
		{"the server itself", "example.org:2053", "127.0.0.1:40000", nil, 204},
		{"the panel by the server's local address", "192.168.1.10:2053", local, nil, 204},
		{"an application by its local name", "radarr.local", local, nil, 204},
		// a name that is not shielded is not this guard's business, whoever asks
		{"a direct application, from anywhere", "jellyfin.example.org:2053", stranger, nil, 204},
		{"the server's address as the name, from anywhere", "192.168.1.10:2053", stranger, nil, 204},
		{"a local name, from anywhere (the local guard's business)", "radarr.local", stranger, nil, 204},
	}
	refused := 0
	for _, c := range cases {
		req := httptest.NewRequest("GET", "https://"+c.host+"/", nil)
		req.Host = c.host
		req.RemoteAddr = c.peer
		for name, value := range c.header {
			req.Header.Set(name, value)
		}
		recorder := httptest.NewRecorder()
		guarded.ServeHTTP(recorder, req)
		if recorder.Code != c.want {
			t.Errorf("%s: status %d, want %d", c.name, recorder.Code, c.want)
		}
		if c.want == 403 {
			refused++
		}
	}
	if served != len(cases)-refused {
		t.Errorf("%d requests reached the application, want %d", served, len(cases)-refused)
	}

	recorded := 0
	for _, event := range events() {
		if event.Kind == utils.SecAccessOriginDenied {
			recorded++
			if event.IP != "198.51.100.66" {
				t.Errorf("the refusal names %q, not the peer", event.IP)
			}
		}
	}
	if recorded != refused {
		t.Errorf("%d refusals were recorded, want %d", recorded, refused)
	}

	// nothing shielded: the guard does nothing, even without any ranges
	config.HTTPConfig.Cloudflare = utils.ForkCloudflareConfig{}
	utils.LoadBaseMainConfig(config)
	req := httptest.NewRequest("GET", "https://radarr.example.org/", nil)
	req.RemoteAddr = stranger
	recorder := httptest.NewRecorder()
	guarded.ServeHTTP(recorder, req)
	if recorder.Code != 204 {
		t.Errorf("with nothing shielded: status %d", recorder.Code)
	}

	// a range list in the configuration that is no list of Cloudflare's does
	// not open the guard to the world: the built-in one is used
	config.HTTPConfig.Cloudflare = utils.ForkCloudflareConfig{Shielded: []string{"radarr.example.org"}, RangesV4: []string{"0.0.0.0/0"}, RangesV6: []string{"::/0"}}
	utils.LoadBaseMainConfig(config)
	req = httptest.NewRequest("GET", "https://radarr.example.org/", nil)
	req.RemoteAddr = stranger
	recorder = httptest.NewRecorder()
	guarded.ServeHTTP(recorder, req)
	if recorder.Code != 403 {
		t.Errorf("with a broken range list: status %d", recorder.Code)
	}
}

// What an application is told about the visitor comes from this server: the
// headers Cloudflare uses for it are passed on only when they were believed.
func TestApplicationsGetCloudflareVisitorHeadersOnlyWhenBelieved(t *testing.T) {
	previous := utils.GetBaseMainConfig()
	config := utils.Config{}
	config.HTTPConfig.Cloudflare.Shielded = []string{"radarr.example.org"}
	config.HTTPConfig.Cloudflare.TrustRanges = true
	utils.LoadBaseMainConfig(config)
	t.Cleanup(func() { utils.LoadBaseMainConfig(previous) })

	route := utils.ProxyRouteConfig{Name: "radarr"}
	cases := []struct {
		name     string
		host     string
		peer     string
		claimed  string
		wantCF   string
		wantReal string
	}{
		{"through Cloudflare, shielded name", "radarr.example.org:2053", "173.245.48.9:40000", "86.120.1.1", "86.120.1.1", "86.120.1.1"},
		{"a direct visitor forging them, shielded name", "radarr.example.org", "198.51.100.66:50000", "86.120.1.1", "", "198.51.100.66"},
		{"a direct visitor forging them, direct name", "jellyfin.example.org", "198.51.100.66:50000", "192.168.1.5", "", "198.51.100.66"},
		{"from Cloudflare's addresses for a direct name", "jellyfin.example.org", "173.245.48.9:40000", "86.120.1.1", "", "173.245.48.9"},
		{"through Cloudflare, a local address claimed", "radarr.example.org", "173.245.48.9:40000", "192.168.1.5", "", "173.245.48.9"},
		{"a device at home", "radarr.example.org", "192.168.1.23:51000", "86.120.1.1", "", "192.168.1.23"},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "https://"+c.host+"/", nil)
		req.Host = c.host
		req.RemoteAddr = c.peer
		req.Header.Set("X-Forwarded-For", c.claimed)
		req.Header.Set("CF-Connecting-IP", c.claimed)
		req.Header.Set("CF-Connecting-IPv6", "2001:db8::1")
		req.Header.Set("True-Client-IP", c.claimed)
		req.Header.Set("Cf_Connecting_Ip", c.claimed)
		forwardedHeaders(req, route)

		if got := req.Header.Get("CF-Connecting-IP"); got != c.wantCF {
			t.Errorf("%s: the application gets CF-Connecting-IP %q, want %q", c.name, got, c.wantCF)
		}
		if got := req.Header.Get("X-Real-IP"); got != c.wantReal {
			t.Errorf("%s: the application gets X-Real-IP %q, want %q", c.name, got, c.wantReal)
		}
		if c.wantCF == "" && (req.Header.Get("True-Client-IP") != "" || req.Header.Get("CF-Connecting-IPv6") != "") {
			t.Errorf("%s: headers the visitor wrote were passed on: %v", c.name, req.Header)
		}
		if _, there := req.Header["Cf_connecting_ip"]; there || req.Header.Get("Cf_Connecting_Ip") != "" {
			t.Errorf("%s: the underscore spelling was passed on", c.name)
		}
	}
}

// The guard's whole table: who asks (Cloudflare's addresses, the local
// network, anyone else), what the connection presented, and what the admin
// switched on. A refusal says which condition failed.
func TestShieldedHostnamesAndTheCertificateOfCloudflaresProxy(t *testing.T) {
	previous := utils.GetBaseMainConfig()
	t.Cleanup(func() { utils.LoadBaseMainConfig(previous) })
	t.Cleanup(utils.ResetOriginPullCounts)

	mine := cffake.NewAuthority("this server's authority")
	other := cffake.NewAuthority("somebody else's authority")
	state := func(cert *tls.Certificate) *tls.ConnectionState {
		if cert == nil {
			return &tls.ConnectionState{}
		}
		return &tls.ConnectionState{PeerCertificates: cffake.Presented(*cert)}
	}
	good, wrong, expired := mine.Issue(cffake.Leaf{}), other.Issue(cffake.Leaf{}), mine.Issue(cffake.Leaf{Expired: true})

	const cloudflare, stranger, local = "173.245.48.9:40000", "198.51.100.66:50000", "192.168.1.23:51000"
	const off, test, enforce = utils.OriginPullOff, utils.OriginPullTest, utils.OriginPullEnforce
	cases := []struct {
		name   string
		mode   string
		peer   string
		cert   *tls.Certificate
		plain  bool
		want   int
		reason string
	}{
		// the option off: exactly what it was before
		{"off, Cloudflare, no certificate", off, cloudflare, nil, false, 204, ""},
		{"off, Cloudflare, any certificate", off, cloudflare, &wrong, false, 204, ""},
		{"off, home", off, local, nil, false, 204, ""},
		{"off, anyone else", off, stranger, nil, false, 403, "not_cloudflare"},
		{"off, anyone else with the right certificate", off, stranger, &good, false, 403, "not_cloudflare"},

		// test: counted, never refused
		{"test, Cloudflare, the right certificate", test, cloudflare, &good, false, 204, ""},
		{"test, Cloudflare, no certificate", test, cloudflare, nil, false, 204, ""},
		{"test, Cloudflare, a wrong certificate", test, cloudflare, &wrong, false, 204, ""},
		{"test, Cloudflare, an expired certificate", test, cloudflare, &expired, false, 204, ""},
		{"test, home", test, local, nil, false, 204, ""},
		{"test, anyone else", test, stranger, &good, false, 403, "not_cloudflare"},

		// enforce
		{"enforce, Cloudflare, the right certificate", enforce, cloudflare, &good, false, 204, ""},
		{"enforce, Cloudflare, no certificate", enforce, cloudflare, nil, false, 403, "no_certificate"},
		{"enforce, Cloudflare, another authority's certificate", enforce, cloudflare, &wrong, false, 403, "wrong_certificate"},
		{"enforce, Cloudflare, an expired certificate", enforce, cloudflare, &expired, false, 403, "expired_certificate"},
		{"enforce, Cloudflare, over plain HTTP", enforce, cloudflare, nil, true, 403, "not_https"},
		// the way in that never depends on Cloudflare
		{"enforce, home, no certificate", enforce, local, nil, false, 204, ""},
		{"enforce, home, over plain HTTP", enforce, local, nil, true, 204, ""},
		{"enforce, the server itself", enforce, "127.0.0.1:40000", nil, false, 204, ""},
		// the certificate does not replace the address
		{"enforce, anyone else, no certificate", enforce, stranger, nil, false, 403, "not_cloudflare"},
		{"enforce, anyone else with the right certificate", enforce, stranger, &good, false, 403, "not_cloudflare"},
	}

	for _, c := range cases {
		config := utils.Config{}
		config.HTTPConfig.Cloudflare = utils.ForkCloudflareConfig{
			Shielded: []string{"radarr.example.org"}, TrustRanges: true,
			OriginPulls: utils.ForkOriginPullsConfig{Mode: c.mode, Kind: utils.OriginPullOwn, Confirmed: true, Own: []utils.ForkOriginPullCert{{CA: mine.PEM}}},
		}
		utils.LoadBaseMainConfig(config)
		events := captureSecurity(t)

		served := false
		guarded := CloudflareOriginGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			served = true
			w.WriteHeader(http.StatusNoContent)
		}))
		req := httptest.NewRequest("GET", "https://radarr.example.org:2053/", nil)
		req.Host = "radarr.example.org:2053"
		req.RemoteAddr = c.peer
		// nothing a request says about itself opens the guard
		req.Header.Set("CF-Connecting-IP", "192.168.1.23")
		req.Header.Set("X-Forwarded-For", "173.245.48.9")
		req.TLS = state(c.cert)
		if c.plain {
			req.TLS = nil
		}
		recorder := httptest.NewRecorder()
		guarded.ServeHTTP(recorder, req)

		if recorder.Code != c.want || served != (c.want == 204) {
			t.Errorf("%s: status %d (served: %v), want %d", c.name, recorder.Code, served, c.want)
		}
		reasons := []string{}
		for _, event := range events() {
			if event.Kind == utils.SecAccessOriginDenied {
				reasons = append(reasons, event.Details["reason"])
			}
		}
		if c.reason == "" && len(reasons) != 0 {
			t.Errorf("%s: a refusal was recorded: %v", c.name, reasons)
		}
		if c.reason != "" && (len(reasons) != 1 || reasons[0] != c.reason) {
			t.Errorf("%s: recorded reasons %v, want %q", c.name, reasons, c.reason)
		}
	}

	// a hostname that is not shielded is none of this, whatever is enforced
	req := httptest.NewRequest("GET", "https://jellyfin.example.org:2053/", nil)
	req.Host = "jellyfin.example.org:2053"
	req.RemoteAddr = cloudflare
	req.TLS = &tls.ConnectionState{}
	recorder := httptest.NewRecorder()
	CloudflareOriginGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(recorder, req)
	if recorder.Code != 204 {
		t.Errorf("a direct name while enforcing: status %d", recorder.Code)
	}

	// enforcement nobody confirmed in time is not in force
	config := utils.Config{}
	config.HTTPConfig.Cloudflare = utils.ForkCloudflareConfig{
		Shielded: []string{"radarr.example.org"}, TrustRanges: true,
		OriginPulls: utils.ForkOriginPullsConfig{Mode: enforce, Kind: utils.OriginPullOwn, ConfirmBy: time.Now().Add(-time.Second), Own: []utils.ForkOriginPullCert{{CA: mine.PEM}}},
	}
	utils.LoadBaseMainConfig(config)
	req = httptest.NewRequest("GET", "https://radarr.example.org:2053/", nil)
	req.Host = "radarr.example.org:2053"
	req.RemoteAddr = cloudflare
	req.TLS = &tls.ConnectionState{}
	recorder = httptest.NewRecorder()
	CloudflareOriginGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(recorder, req)
	if recorder.Code != 204 {
		t.Errorf("unconfirmed enforcement refused a request: status %d", recorder.Code)
	}
}

// What is counted for the panel: requests for shielded names from
// Cloudflare's addresses while the option is on, nothing else.
func TestOriginPullRequestsAreCounted(t *testing.T) {
	previous := utils.GetBaseMainConfig()
	t.Cleanup(func() { utils.LoadBaseMainConfig(previous) })
	t.Cleanup(utils.ResetOriginPullCounts)
	mine := cffake.NewAuthority("this server's authority")
	good := mine.Issue(cffake.Leaf{})

	config := utils.Config{}
	config.HTTPConfig.Cloudflare = utils.ForkCloudflareConfig{
		Shielded: []string{"radarr.example.org", "example.org"}, TrustRanges: true,
		OriginPulls: utils.ForkOriginPullsConfig{Mode: utils.OriginPullTest, Kind: utils.OriginPullOwn, Own: []utils.ForkOriginPullCert{{CA: mine.PEM}}},
	}
	utils.LoadBaseMainConfig(config)
	utils.ResetOriginPullCounts()

	guarded := CloudflareOriginGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	ask := func(host string, peer string, cert *tls.Certificate) {
		req := httptest.NewRequest("GET", "https://"+host+"/", nil)
		req.Host = host
		req.RemoteAddr = peer
		req.TLS = &tls.ConnectionState{}
		if cert != nil {
			req.TLS.PeerCertificates = cffake.Presented(*cert)
		}
		guarded.ServeHTTP(httptest.NewRecorder(), req)
	}
	ask("radarr.example.org:2053", "173.245.48.9:40000", &good)
	ask("RADARR.example.org.:2053", "173.245.48.9:40001", &good)
	ask("example.org:2053", "173.245.48.9:40000", nil)
	ask("radarr.example.org:2053", "192.168.1.23:51000", nil)    // home: not counted
	ask("radarr.example.org:2053", "198.51.100.66:50000", &good) // around Cloudflare: refused, not counted
	ask("jellyfin.example.org:2053", "173.245.48.9:40000", nil)  // not shielded: not counted
	ask("unknown.example.org:2053", "173.245.48.9:40000", nil)   // not served: not counted

	counts := utils.OriginPullSnapshot()
	if counts.Verified != 2 || counts.Failed != 1 || counts.Problems[utils.OriginPullNoCert] != 1 {
		t.Errorf("counted %+v", counts)
	}
	if len(counts.Hosts) != 2 || counts.Hosts[0].Host != "example.org" || counts.Hosts[1].Host != "radarr.example.org" || counts.Hosts[1].Verified != 2 {
		t.Errorf("per hostname: %+v", counts.Hosts)
	}
	if _, seen := counts.Issuers[utils.CertificateFingerprint(mine.Cert.Raw)]; !seen || len(counts.Issuers) != 1 {
		t.Errorf("issuers: %v", counts.Issuers)
	}
}

// While origin pulls are enforced, an application is told the visitor's
// address Cloudflare reported only on a connection that proved itself.
func TestApplicationsGetCloudflareVisitorHeadersOnlyWithTheCertificate(t *testing.T) {
	previous := utils.GetBaseMainConfig()
	t.Cleanup(func() { utils.LoadBaseMainConfig(previous) })
	mine := cffake.NewAuthority("this server's authority")
	other := cffake.NewAuthority("somebody else's authority")
	good, wrong := mine.Issue(cffake.Leaf{}), other.Issue(cffake.Leaf{})

	config := utils.Config{}
	config.HTTPConfig.Cloudflare = utils.ForkCloudflareConfig{
		Shielded: []string{"radarr.example.org"}, TrustRanges: true,
		OriginPulls: utils.ForkOriginPullsConfig{Mode: utils.OriginPullEnforce, Kind: utils.OriginPullOwn, Confirmed: true, Own: []utils.ForkOriginPullCert{{CA: mine.PEM}}},
	}
	utils.LoadBaseMainConfig(config)

	for name, c := range map[string]struct {
		cert     *tls.Certificate
		wantCF   string
		wantReal string
	}{
		"the expected certificate": {&good, "86.120.1.1", "86.120.1.1"},
		"another certificate":      {&wrong, "", "173.245.48.9"},
		"no certificate":           {nil, "", "173.245.48.9"},
	} {
		req := httptest.NewRequest("GET", "https://radarr.example.org:2053/", nil)
		req.Host = "radarr.example.org:2053"
		req.RemoteAddr = "173.245.48.9:40000"
		req.Header.Set("CF-Connecting-IP", "86.120.1.1")
		req.Header.Set("True-Client-IP", "86.120.1.1")
		req.TLS = &tls.ConnectionState{}
		if c.cert != nil {
			req.TLS.PeerCertificates = cffake.Presented(*c.cert)
		}
		forwardedHeaders(req, utils.ProxyRouteConfig{Name: "radarr"})
		if got := req.Header.Get("CF-Connecting-IP"); got != c.wantCF {
			t.Errorf("%s: the application gets CF-Connecting-IP %q, want %q", name, got, c.wantCF)
		}
		if got := req.Header.Get("X-Real-IP"); got != c.wantReal {
			t.Errorf("%s: the application gets X-Real-IP %q, want %q", name, got, c.wantReal)
		}
		if c.wantCF == "" && req.Header.Get("True-Client-IP") != "" {
			t.Errorf("%s: True-Client-IP was passed on", name)
		}
	}
}
