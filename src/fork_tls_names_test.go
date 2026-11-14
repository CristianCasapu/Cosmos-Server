package main

// Fork addition: what a handshake and a request tell each kind of peer
// (fork_tls_names.go). The peer's class is given to the rule by the test
// (tlsNames.classify); names, certificates and authorities are made up.

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/proxy"
	"github.com/azukaar/cosmos-server/src/utils"
)

type testAuthority struct {
	cert *x509.Certificate
	key  crypto.Signer
}

func newTestAuthority(t *testing.T, name string) testAuthority {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, SubjectKeyId: []byte{1, 2, 3},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return testAuthority{cert: cert, key: key}
}

func (a testAuthority) issue(t *testing.T, names ...string) *tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: names[0]},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, name := range names {
		if ip := net.ParseIP(name); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, name)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.cert, &key.PublicKey, a.key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

const (
	tnLocal      = "192.168.1.23:51000"
	tnCloudflare = "173.245.48.9:40000"
	tnOther      = "198.51.100.66:50000"
)

// the configuration: the panel and jellyfin served directly, radarr through
// Cloudflare, a .local name, names for use at home under home.example.org,
// one name only the self-signed certificate covers, one under a deeper
// level of the zone
func tlsNamesConfig() utils.Config {
	config := utils.Config{}
	config.HTTPConfig.Hostname = "cosmos.example.org"
	config.HTTPConfig.HTTPSCertificateMode = utils.HTTPSCertModeList["LETSENCRYPT"]
	config.HTTPConfig.DNSZones = []utils.DNSZoneConfig{{
		Zone: "example.org", HTTPSCertificateMode: utils.HTTPSCertModeList["LETSENCRYPT"],
		DNSChallengeProvider: "cloudflare", DNSChallengeConfig: map[string]string{"CF_DNS_API_TOKEN": "made-" + "up"},
		ManageRecords: true, UseWildcardCertificate: true,
	}}
	route := func(host string) utils.ProxyRouteConfig {
		return utils.ProxyRouteConfig{Name: host, UseHost: true, Host: host, Target: "http://app:80", Mode: "SERVAPP"}
	}
	config.HTTPConfig.ProxyConfig.Routes = []utils.ProxyRouteConfig{
		route("jellyfin.example.org"), route("radarr.example.org"), route("nas.local"),
		route("selfonly.example.net"), route("deep.sub.example.org"), route("192.168.1.10:8080"),
	}
	config.HTTPConfig.Cloudflare.Shielded = []string{"radarr.example.org"}
	config.HTTPConfig.Cloudflare.TrustRanges = true
	config.HTTPConfig.Cloudflare.HomeNames = []utils.ForkHomeNamesZone{{Zone: "example.org", Label: "home"}}
	return config
}

type tlsNamesFixture struct {
	rule                         *tlsNames
	cosmos, web                  testAuthority
	self, primary, zone, deep    *tls.Certificate
	pick                         func(*tls.ClientHelloInfo) (*tls.Certificate, error)
	peers                        map[string]tlsPeer
}

// newTLSNamesFixture loads config and builds the rule with a classifier
// that knows the three test peers, and a GetCertificate that picks the way
// the server's does: the self-signed one (Cosmos' authority, every name) for
// no name, an address, a .local name and what nothing else covers; the
// zone's certificate (the web's authority, the domain, its wildcard and the
// home wildcard) for the zone's names one level deep; a certificate that
// lists two deeper names for those; the server's own (Cosmos' authority,
// the domain) for the rest.
func newTLSNamesFixture(t *testing.T, config utils.Config) *tlsNamesFixture {
	t.Helper()
	previous := utils.GetBaseMainConfig()
	t.Cleanup(func() { utils.LoadBaseMainConfig(previous) })
	utils.LoadBaseMainConfig(config)

	f := &tlsNamesFixture{cosmos: newTestAuthority(t, "test Cosmos authority"), web: newTestAuthority(t, "test web authority")}
	f.self = f.cosmos.issue(t, "cosmos.example.org", "jellyfin.example.org", "radarr.example.org", "nas.local", "selfonly.example.net", "192.168.1.10")
	f.primary = f.cosmos.issue(t, "example.org")
	f.zone = f.web.issue(t, "example.org", "*.example.org", "*.home.example.org")
	f.deep = f.web.issue(t, "example.org", "*.example.org", "deep.sub.example.org", "other.sub.example.org")
	f.pick = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		name := strings.ToLower(strings.TrimSuffix(hello.ServerName, "."))
		switch {
		case name == "" || net.ParseIP(name) != nil || strings.HasSuffix(name, ".local") || name == "selfonly.example.net":
			return f.self, nil
		case strings.HasSuffix(name, ".sub.example.org"):
			return f.deep, nil
		case strings.HasSuffix(name, ".example.org") && strings.Count(name, ".") == 2,
			strings.HasSuffix(name, ".home.example.org"):
			return f.zone, nil
		}
		return f.primary, nil
	}
	f.peers = map[string]tlsPeer{tnLocal: tlsPeerLocal, tnCloudflare: tlsPeerCloudflare, tnOther: tlsPeerOther}
	f.rule = &tlsNames{
		classify: func(remote string) tlsPeer {
			if class, ok := f.peers[remote]; ok {
				return class
			}
			return tlsPeerOther
		},
		names: utils.CurrentTLSNames,
		ca:    func() (*x509.Certificate, crypto.Signer) { return f.cosmos.cert, f.cosmos.key },
		own:   func(cert *tls.Certificate) bool { return cert == f.self },
	}
	return f
}

// a connection that claims a peer and a local address
type helloConn struct {
	net.Conn
	remote, local net.Addr
}

func (c helloConn) RemoteAddr() net.Addr { return c.remote }
func (c helloConn) LocalAddr() net.Addr  { return c.local }

func tcpAddr(address string) *net.TCPAddr {
	host, port, _ := net.SplitHostPort(address)
	number, _ := net.LookupPort("tcp", port)
	return &net.TCPAddr{IP: net.ParseIP(host), Port: number}
}

// handshake runs the rule as the TLS stack would: GetConfigForClient, then
// the configuration's GetCertificate. nil: refused.
func (f *tlsNamesFixture) handshake(peer string, name string) *tls.Certificate {
	config := &tls.Config{GetCertificate: f.pick}
	f.rule.wrapConfig(config)
	hello := &tls.ClientHelloInfo{ServerName: name, Conn: helloConn{remote: tcpAddr(peer), local: tcpAddr("192.168.1.10:2053")}}
	chosen, err := config.GetConfigForClient(hello)
	if err != nil {
		panic(err)
	}
	if chosen == refuseTLS {
		return nil
	}
	if chosen == nil {
		chosen = config
	}
	if chosen.GetCertificate == nil {
		return nil
	}
	cert, _ := chosen.GetCertificate(hello)
	return cert
}

func names(cert *tls.Certificate) string {
	if cert == nil {
		return "(refused)"
	}
	leaf := leafOf(cert)
	out := append([]string{}, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		out = append(out, ip.String())
	}
	return strings.Join(out, ",")
}

// The table of fork_tls_names.go, peer by peer, name by name. Without the
// rule every row is "as before": the stranger columns fail.
func TestTLSNamesTable(t *testing.T) {
	f := newTLSNamesFixture(t, tlsNamesConfig())
	const refused, minted = "refused", "minted"
	type want struct{ local, cloudflare, other string } // a certificate's role, "refused" or "minted"
	cases := []struct {
		name string
		want want
	}{
		{"", want{"self", refused, refused}},
		{"192.168.1.10", want{"self", refused, refused}},
		{"nothere.example.org", want{"zone", refused, refused}},
		{"foo.test", want{"primary", refused, refused}},
		{"nas.local", want{"self", refused, refused}},
		{"localhost", want{"primary", refused, refused}},
		{"jellyfin.home.example.org", want{"zone", refused, refused}},
		{"anything.home.example.org", want{"zone", refused, refused}},
		{"radarr.example.org", want{"zone", "zone", refused}},
		{"RADARR.example.org.", want{"zone", "zone", refused}},
		{"jellyfin.example.org", want{"zone", "zone", "zone"}},
		{"cosmos.example.org", want{"zone", "zone", "zone"}},
		{"selfonly.example.net", want{"self", "self", minted}},
		// a publicly trusted certificate naming other names is served as it
		// is (its names are public in the CT logs; nothing else would be
		// trusted): the documented exception
		{"deep.sub.example.org", want{"deep", "deep", "deep"}},
	}
	roles := map[*tls.Certificate]string{f.self: "self", f.primary: "primary", f.zone: "zone", f.deep: "deep"}
	for _, c := range cases {
		for _, peer := range []struct{ address, want string }{{tnLocal, c.want.local}, {tnCloudflare, c.want.cloudflare}, {tnOther, c.want.other}} {
			cert := f.handshake(peer.address, c.name)
			got := refused
			if cert != nil {
				got = roles[cert]
				if got == "" {
					got = minted
				}
			}
			if got != peer.want {
				t.Errorf("%q from %s: got %s (%s), want %s", c.name, peer.address, got, names(cert), peer.want)
				continue
			}
			if got == minted {
				leaf := leafOf(cert)
				if names(cert) != strings.ToLower(c.name) || leaf.CheckSignatureFrom(f.cosmos.cert) != nil || leaf.Subject.CommonName != c.name {
					t.Errorf("%q from %s: made a certificate naming %s (CN %q), want exactly the name, signed by Cosmos' authority", c.name, peer.address, names(cert), leaf.Subject.CommonName)
				}
			}
			if peer.address == tnOther && cert != nil && cert != f.deep && !namesOnly(leafOf(cert), strings.ToLower(strings.TrimSuffix(c.name, "."))) {
				t.Errorf("%q from a stranger: the certificate names more: %s", c.name, names(cert))
			}
		}
	}
	// the certificate made for a name is made once
	if a, b := f.handshake(tnOther, "selfonly.example.net"), f.handshake(tnOther, "selfonly.example.net"); a != b {
		t.Error("a certificate is made at every handshake")
	}
}

// A local peer without a name gets the self-signed certificate with all its
// names, as before: the owner's devices at home, by address.
func TestTLSNamesLocalPeerWithoutNameUnchanged(t *testing.T) {
	f := newTLSNamesFixture(t, tlsNamesConfig())
	for _, peer := range []string{tnLocal, "127.0.0.1:40000", "[fe80::1]:40000", "100.100.1.2:40000"} {
		f.peers[peer] = tlsPeerLocal
		if cert := f.handshake(peer, ""); cert != f.self {
			t.Errorf("%s without a name: got %s, want the self-signed certificate", peer, names(cert))
		}
	}
	// and the real classifier calls these local
	for _, peer := range []string{tnLocal, "127.0.0.1:40000", "[fe80::1]:40000", "100.100.1.2:40000", "10.0.0.5:1"} {
		if got := classifyTLSPeer(peer); got != tlsPeerLocal {
			t.Errorf("classifyTLSPeer(%s) = %v, want local", peer, got)
		}
	}
	if classifyTLSPeer(tnCloudflare) != tlsPeerCloudflare || classifyTLSPeer(tnOther) != tlsPeerOther {
		t.Error("Cloudflare's or a stranger's address classified wrongly")
	}
}

// Servers meant to be reached by address: nobody is locked out.
func TestTLSNamesByAddress(t *testing.T) {
	// a panel at a public address: a stranger without a name gets a
	// certificate for that address only
	config := tlsNamesConfig()
	config.HTTPConfig.Hostname = "203.0.113.5"
	f := newTLSNamesFixture(t, config)
	cert := f.handshake(tnOther, "")
	if cert == nil || names(cert) != "203.0.113.5" || leafOf(cert).CheckSignatureFrom(f.cosmos.cert) != nil {
		t.Errorf("panel at an address, stranger without a name: got %s, want a certificate of Cosmos' authority for 203.0.113.5 alone", names(cert))
	}
	if cert := f.handshake(tnOther, "nothere.example.org"); cert != nil {
		t.Errorf("panel at an address, unknown name: got %s, want refused", names(cert))
	}

	// a new install answers everyone as before
	config = tlsNamesConfig()
	config.NewInstall = true
	f = newTLSNamesFixture(t, config)
	if cert := f.handshake(tnOther, ""); cert != f.self {
		t.Errorf("new install, stranger without a name: got %s, want as before", names(cert))
	}

	// without Cloudflare at all: direct names still work, nothing else
	config = tlsNamesConfig()
	config.HTTPConfig.Cloudflare = utils.ForkCloudflareConfig{}
	f = newTLSNamesFixture(t, config)
	if cert := f.handshake(tnOther, "radarr.example.org"); cert != f.zone {
		t.Errorf("no Cloudflare: radarr is served directly, got %s", names(cert))
	}
	if cert := f.handshake(tnOther, ""); cert != nil {
		t.Errorf("no Cloudflare: no name from a stranger got %s", names(cert))
	}
}

func TestTLSNameIndex(t *testing.T) {
	config := tlsNamesConfig()
	config.HTTPConfig.ProxyConfig.Routes = append(config.HTTPConfig.ProxyConfig.Routes,
		utils.ProxyRouteConfig{Name: "any", UseHost: true, Host: ":8443", Target: "http://x:1", Mode: "SERVAPP"},
		utils.ProxyRouteConfig{Name: "vps", UseHost: true, Host: "203.0.113.7:9000", Target: "http://x:1", Mode: "SERVAPP"})
	index := utils.BuildTLSNameIndex(config, []string{"cosmos.example.org:2053", "jellyfin.example.org", ":8443", "0.0.0.0:9443", "203.0.113.7:9000", "192.168.1.10:8080", "nas.local"})
	if !index.AnyHostPorts["8443"] || !index.AnyHostPorts["9443"] || len(index.AnyHostPorts) != 2 {
		t.Errorf("ports answering any host: %v", index.AnyHostPorts)
	}
	if len(index.PublicIPs) != 1 || index.PublicIPs[0].String() != "203.0.113.7" {
		t.Errorf("public addresses: %v (a private one is not one)", index.PublicIPs)
	}
	for name, want := range map[string]utils.TLSNameClass{
		"cosmos.example.org": utils.TLSNameDirect, "Cosmos.Example.Org.:2053": utils.TLSNameDirect,
		"radarr.example.org": utils.TLSNameShielded, "nas.local": utils.TLSNameLocalOnly, "x.home.example.org": utils.TLSNameLocalOnly,
		"": utils.TLSNameNone, "[2001:db8::1]:443": utils.TLSNameNone, "nothere.example.org": utils.TLSNameUnknown,
	} {
		if got := index.Class(name); got != want {
			t.Errorf("Class(%q) = %v, want %v", name, got, want)
		}
	}
	config.HTTPConfig.AcceptAllInsecureHostname = true
	if utils.BuildTLSNameIndex(config, nil).Class("nothere.example.org") != utils.TLSNameDirect {
		t.Error("with every hostname accepted, an unknown name is served")
	}
}

// Real handshakes on loopback through an http.Server configured as the
// HTTPS server is (origin pulls inside, the rule around), the peer given by
// the listener.
func TestTLSNamesRealHandshakes(t *testing.T) {
	config := tlsNamesConfig()
	config.HTTPConfig.Cloudflare.OriginPulls = utils.ForkOriginPullsConfig{Mode: utils.OriginPullTest, Kind: utils.OriginPullShared}
	f := newTLSNamesFixture(t, config)
	t.Cleanup(utils.ResetOriginPullCounts)

	server := &http.Server{
		Handler:   http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "app") }),
		TLSConfig: originPullTLS(&tls.Config{GetCertificate: f.pick}),
	}
	forkTLSNamesWith(f.rule, server)
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	from := &atomic.Value{}
	from.Store(tnOther)
	go server.ServeTLS(&fromListener{Listener: inner, from: from}, "", "")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	})

	dial := func(peer, name string) (*tls.ConnectionState, bool, error) {
		from.Store(peer)
		asked := false
		conn, err := tls.Dial("tcp", inner.Addr().String(), &tls.Config{
			ServerName: name, InsecureSkipVerify: true, NextProtos: []string{"h2", "http/1.1"},
			GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
				asked = true
				return &tls.Certificate{}, nil
			},
		})
		if err != nil {
			return nil, asked, err
		}
		defer conn.Close()
		state := conn.ConnectionState()
		return &state, asked, nil
	}

	for _, name := range []string{"", "nothere.example.org", "radarr.example.org", "nas.local", "jellyfin.home.example.org"} {
		if _, _, err := dial(tnOther, name); err == nil || !strings.Contains(err.Error(), "unrecognized name") {
			t.Errorf("stranger, %q: %v, want the alert unrecognized_name", name, err)
		}
	}
	if f.rule.refused[tlsPeerOther][tlsShielded].Load() != 1 || f.rule.refused[tlsPeerOther][tlsNoName].Load() != 1 {
		t.Errorf("refusals not counted: %v", f.rule.refused[tlsPeerOther])
	}
	if line := f.rule.summary(); !strings.Contains(line, "1 from other (shielded name)") || strings.Contains(line, "radarr") {
		t.Errorf("summary %q", line)
	}

	state, asked, err := dial(tnOther, "jellyfin.example.org")
	if err != nil || state.NegotiatedProtocol != "h2" || asked || names(&tls.Certificate{Certificate: [][]byte{state.PeerCertificates[0].Raw}}) != "example.org,*.example.org,*.home.example.org" {
		t.Errorf("stranger, direct name: %v %+v asked=%v", err, state, asked)
	}
	state, asked, err = dial(tnCloudflare, "radarr.example.org")
	if err != nil || state.NegotiatedProtocol != "h2" || !asked {
		t.Errorf("Cloudflare, shielded name with origin pulls testing: err=%v asked=%v, want served over h2 and asked for a client certificate", err, asked)
	}
	if _, _, err := dial(tnCloudflare, ""); err == nil {
		t.Error("Cloudflare without a name was served")
	}
	state, asked, err = dial(tnLocal, "")
	if err != nil || asked || !state.PeerCertificates[0].Equal(f.self.Leaf) {
		t.Errorf("local without a name: %v asked=%v, want the self-signed certificate", err, asked)
	}

	// the handshake error line of a refusal is not logged; other lines are
	var out strings.Builder
	logger := tlsRefusedLog{out: func() io.Writer { return &out }}
	logger.Write([]byte("http: TLS handshake error from 198.51.100.66:5: tls: no certificates configured\n"))
	logger.Write([]byte("http: TLS handshake error from 198.51.100.66:5: EOF\n"))
	if out.String() != "http: TLS handshake error from 198.51.100.66:5: EOF\n" {
		t.Errorf("log: %q", out.String())
	}
}

// The Host of a request over TLS: a stranger gets one and the same answer
// for every name it could not have opened, the records of the guards
// behind are written, local and Cloudflare's peers pass as before.
func TestTLSNamesHTTPLevel(t *testing.T) {
	f := newTLSNamesFixture(t, tlsNamesConfig())
	var recorded []string
	utils.SetSecurityRecorder(func(event utils.SecurityEvent) { recorded = append(recorded, event.Kind+" "+event.Details["reason"]) }, func() {})
	t.Cleanup(func() { utils.SetSecurityRecorder(nil, nil) })

	handler := f.rule.handler(proxy.HomeNameGuard(proxy.CloudflareOriginGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "app")
	}))))
	get := func(peer, host string, overTLS bool) (int, string) {
		r := httptest.NewRequest("GET", "https://"+host+"/", nil)
		r.Host, r.RemoteAddr = host, peer
		if !overTLS {
			r.TLS = nil
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}

	var body421 string
	for _, host := range []string{"radarr.example.org", "nas.local", "x.home.example.org", "nothere.example.org", "192.168.1.10", "203.0.113.5", ""} {
		recorded = nil
		code, body := get(tnOther, host, true)
		if code != http.StatusMisdirectedRequest || (body421 != "" && body != body421) || strings.Contains(body, "example") {
			t.Errorf("stranger, Host %q: %d %q, want 421 with the one body", host, code, body)
		}
		body421 = body
		want := map[string]string{"radarr.example.org": "access.origin.denied not_cloudflare", "nas.local": "access.local.denied local_name", "x.home.example.org": "access.local.denied home_name"}[host]
		if want == "" {
			want = "shield.hostname.unknown "
		}
		if len(recorded) != 1 || recorded[0] != want {
			t.Errorf("stranger, Host %q: recorded %v, want %q", host, recorded, want)
		}
	}
	for _, c := range []struct {
		peer, host string
		code       int
	}{
		{tnOther, "jellyfin.example.org", 200},
		{tnOther, "cosmos.example.org:2053", 200},
		{tnCloudflare, "radarr.example.org", 200},
		{tnCloudflare, "nothere.example.org", 421},
		{tnLocal, "nas.local", 200},
		{tnLocal, "192.168.1.10", 200},
		{tnLocal, "nothere.example.org", 200}, // as before: the router answers it
	} {
		if code, _ := get(c.peer, c.host, true); code != c.code {
			t.Errorf("%s, Host %q: %d, want %d", c.peer, c.host, code, c.code)
		}
	}
	// over plain HTTP (connections handed over without TLS) nothing changes here
	if code, _ := get(tnOther, "nothere.example.org", false); code != 200 {
		t.Errorf("plain HTTP is not this guard's: %d", code)
	}
}

// The plain-HTTP port redirects a stranger only for a name it could open
// over HTTPS; a shielded or home name answers like an unknown one.
func TestTLSNamesPlainHTTPRedirect(t *testing.T) {
	f := newTLSNamesFixture(t, tlsNamesConfig())
	saved := forkTLS
	forkTLS = f.rule
	t.Cleanup(func() { forkTLS = saved })
	for _, c := range []struct {
		peer, host string
		redirect   bool
	}{
		{tnOther, "jellyfin.example.org", true},
		{tnOther, "radarr.example.org", false},
		{tnOther, "x.home.example.org", false},
		{tnCloudflare, "radarr.example.org", true},
		{tnLocal, "radarr.example.org", true},
	} {
		r := httptest.NewRequest("GET", "http://"+c.host+"/", nil)
		r.Host, r.RemoteAddr = c.host, c.peer
		w := httptest.NewRecorder()
		if got := redirectPlainHTTPToHTTPS(w, r); got != c.redirect {
			t.Errorf("%s, %q on the plain-HTTP port: redirect %v, want %v", c.peer, c.host, got, c.redirect)
		}
	}
}
