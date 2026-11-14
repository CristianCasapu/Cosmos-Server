package main

// Fork addition: real handshakes against the TLS configuration of the HTTPS
// server (originPullTLS), with the guard and the visitor's address behind it
// as on the server. The peer's address is what the test says it is: the
// listener reports it, the way a connection from that address would.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/cloudflare/cffake"
	"github.com/azukaar/cosmos-server/src/proxy"
	"github.com/azukaar/cosmos-server/src/utils"
)

// a listener whose connections claim to come from a chosen address
type fromListener struct {
	net.Listener
	from *atomic.Value // of string, "address:port"
}

type fromConn struct {
	net.Conn
	remote net.Addr
}

func (c *fromConn) RemoteAddr() net.Addr { return c.remote }

func (l *fromListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	host, port, _ := net.SplitHostPort(l.from.Load().(string))
	number, _ := net.LookupPort("tcp", port)
	return &fromConn{Conn: conn, remote: &net.TCPAddr{IP: net.ParseIP(host), Port: number}}, nil
}

type originPullServer struct {
	t       *testing.T
	address string
	from    *atomic.Value
	roots   *x509.CertPool
}

// startOriginPullServer serves "radarr.example.org" (shielded) and
// "jellyfin.example.org" (direct) the way the HTTPS server does: its TLS
// configuration through originPullTLS, the visitor's address worked out, the
// guard in front. The answer names the visitor the server worked with.
func startOriginPullServer(t *testing.T) *originPullServer {
	t.Helper()
	web := cffake.NewAuthority("the web's authority")
	serverCert := web.Issue(cffake.Leaf{Names: []string{"radarr.example.org", "jellyfin.example.org"}})

	handler := utils.ClientRealIP(proxy.CloudflareOriginGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		visitor, _ := r.Context().Value("ClientID").(string)
		io.WriteString(w, visitor)
	})))
	server := &http.Server{
		Handler:   handler,
		TLSConfig: originPullTLS(&tls.Config{Certificates: []tls.Certificate{serverCert}}),
	}
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	from := &atomic.Value{}
	from.Store("198.51.100.66:50000")
	go server.ServeTLS(&fromListener{Listener: inner, from: from}, "", "")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	})

	roots := x509.NewCertPool()
	roots.AddCert(web.Cert)
	return &originPullServer{t: t, address: inner.Addr().String(), from: from, roots: roots}
}

type originPullAnswer struct {
	status  int
	visitor string
	// asked: the server asked for a client certificate; issuers are the
	// names it said it accepts
	asked   bool
	issuers [][]byte
	proto   int
	resumed bool
}

// get makes one request on a connection of its own, from the given address,
// presenting cert when the server asks for one (nil: nothing to present).
func (s *originPullServer) get(peer string, host string, cert *tls.Certificate, cache tls.ClientSessionCache) originPullAnswer {
	s.t.Helper()
	s.from.Store(peer)
	answer := originPullAnswer{}
	var mu sync.Mutex
	transport := &http.Transport{
		ForceAttemptHTTP2: true,
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network string, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, s.address)
		},
		TLSClientConfig: &tls.Config{
			RootCAs:            s.roots,
			ClientSessionCache: cache,
			GetClientCertificate: func(info *tls.CertificateRequestInfo) (*tls.Certificate, error) {
				mu.Lock()
				answer.asked, answer.issuers = true, info.AcceptableCAs
				mu.Unlock()
				if cert == nil {
					return &tls.Certificate{}, nil
				}
				return cert, nil
			},
		},
	}
	defer transport.CloseIdleConnections()
	req, _ := http.NewRequest("GET", "https://"+host+":2053/", nil)
	req.Header.Set("CF-Connecting-IP", "86.120.1.1")
	resp, err := (&http.Client{Transport: transport, Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		s.t.Fatalf("from %s for %s: the request failed (a handshake must never fail over a client certificate): %v", peer, host, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	mu.Lock()
	defer mu.Unlock()
	answer.status, answer.visitor, answer.proto = resp.StatusCode, string(body), resp.ProtoMajor
	answer.resumed = resp.TLS != nil && resp.TLS.DidResume
	return answer
}

func setOriginPulls(t *testing.T, pulls utils.ForkOriginPullsConfig) {
	t.Helper()
	config := utils.Config{}
	config.HTTPConfig.Cloudflare = utils.ForkCloudflareConfig{Shielded: []string{"radarr.example.org"}, TrustRanges: true, OriginPulls: pulls}
	utils.LoadBaseMainConfig(config)
}

const (
	pullCloudflare = "173.245.48.9:40000"
	pullStranger   = "198.51.100.66:50000"
	pullHome       = "192.168.1.23:51000"
)

// With the option off (how every server starts, and stays until the admin
// switches it on) the listener asks nobody for a certificate. This fails if
// the listener ever starts asking, or requiring, by default.
func TestNobodyIsAskedForACertificateByDefault(t *testing.T) {
	previous := utils.GetBaseMainConfig()
	t.Cleanup(func() { utils.LoadBaseMainConfig(previous) })
	server := startOriginPullServer(t)
	mine := cffake.NewAuthority("this server's authority")
	cert := mine.Issue(cffake.Leaf{})

	for name, pulls := range map[string]utils.ForkOriginPullsConfig{
		"nothing configured":                              {},
		"a certificate made, the option off":              {Kind: utils.OriginPullOwn, Own: []utils.ForkOriginPullCert{{CA: mine.PEM}}},
		"Cloudflare's certificate chosen, the option off": {Kind: utils.OriginPullShared},
	} {
		setOriginPulls(t, pulls)
		for _, peer := range []string{pullCloudflare, pullHome} {
			for _, host := range []string{"radarr.example.org", "jellyfin.example.org"} {
				answer := server.get(peer, host, &cert, nil)
				if answer.asked {
					t.Errorf("%s: %s asking for %s was asked for a client certificate", name, peer, host)
				}
				if answer.status != 200 {
					t.Errorf("%s: %s asking for %s: status %d", name, peer, host, answer.status)
				}
			}
		}
		// and a visitor without any certificate gets through the handshake
		if answer := server.get(pullStranger, "jellyfin.example.org", nil, nil); answer.asked || answer.status != 200 {
			t.Errorf("%s: a visitor of a direct name: %+v", name, answer)
		}
	}
	base := &tls.Config{}
	if originPullTLS(base); base.ClientAuth != tls.NoClientCert || base.ClientCAs != nil {
		t.Errorf("the listener's own configuration asks for certificates: %v", base.ClientAuth)
	}
}

// Switched on, only Cloudflare's peers are asked, told exactly which issuer
// is expected, and whatever they present the handshake completes.
func TestOnlyCloudflareIsAskedAndTheHandshakeNeverFails(t *testing.T) {
	previous := utils.GetBaseMainConfig()
	t.Cleanup(func() { utils.LoadBaseMainConfig(previous) })
	t.Cleanup(utils.ResetOriginPullCounts)
	server := startOriginPullServer(t)
	mine := cffake.NewAuthority("this server's authority")
	other := cffake.NewAuthority("somebody else's authority")
	good, wrong, expired := mine.Issue(cffake.Leaf{}), other.Issue(cffake.Leaf{}), mine.Issue(cffake.Leaf{Expired: true})
	own := []utils.ForkOriginPullCert{{CA: mine.PEM}}

	// testing: everything is answered, Cloudflare's peers are asked
	setOriginPulls(t, utils.ForkOriginPullsConfig{Mode: utils.OriginPullTest, Kind: utils.OriginPullOwn, Own: own})
	utils.ResetOriginPullCounts()
	for name, cert := range map[string]*tls.Certificate{"the right certificate": &good, "a wrong one": &wrong, "an expired one": &expired, "none": nil} {
		answer := server.get(pullCloudflare, "radarr.example.org", cert, nil)
		if !answer.asked || answer.status != 200 || answer.visitor != "86.120.1.1" {
			t.Errorf("testing, Cloudflare with %s: %+v", name, answer)
		}
		if len(answer.issuers) != 1 || string(answer.issuers[0]) != string(mine.Cert.RawSubject) {
			t.Errorf("testing: the server names %d issuers, want this server's authority alone", len(answer.issuers))
		}
		if everyoneElse := server.get(pullHome, "radarr.example.org", nil, nil); answer.proto != everyoneElse.proto || answer.proto != 2 {
			t.Errorf("testing, Cloudflare with %s: HTTP/%d, everyone else gets HTTP/%d (want HTTP/2 for both)", name, answer.proto, everyoneElse.proto)
		}
	}
	if counts := utils.OriginPullSnapshot(); counts.Verified != 1 || counts.Failed != 3 ||
		counts.Problems[utils.OriginPullNoCert] != 1 || counts.Problems[utils.OriginPullWrongCert] != 1 || counts.Problems[utils.OriginPullExpired] != 1 {
		t.Errorf("testing counted %+v", counts)
	}
	for _, peer := range []string{pullHome, pullStranger, "127.0.0.1:40000", "[2001:db8::5]:40000"} {
		for _, host := range []string{"radarr.example.org", "jellyfin.example.org"} {
			if answer := server.get(peer, host, &good, nil); answer.asked {
				t.Errorf("testing: %s asking for %s was asked for a client certificate", peer, host)
			}
		}
	}

	// enforcing
	setOriginPulls(t, utils.ForkOriginPullsConfig{Mode: utils.OriginPullEnforce, Kind: utils.OriginPullOwn, Confirmed: true, Own: own})
	cases := []struct {
		name    string
		peer    string
		host    string
		cert    *tls.Certificate
		status  int
		visitor string
		asked   bool
	}{
		{"Cloudflare with the right certificate", pullCloudflare, "radarr.example.org", &good, 200, "86.120.1.1", true},
		{"Cloudflare with another authority's certificate", pullCloudflare, "radarr.example.org", &wrong, 403, "", true},
		{"Cloudflare with an expired certificate", pullCloudflare, "radarr.example.org", &expired, 403, "", true},
		{"Cloudflare without a certificate", pullCloudflare, "radarr.example.org", nil, 403, "", true},
		{"Cloudflare over IPv6 with the right certificate", "[2606:4700::9]:40000", "radarr.example.org", &good, 200, "86.120.1.1", true},
		// a name that is served directly: asked (the peer is Cloudflare's), nothing depends on it, nothing it says is believed
		{"Cloudflare for a direct name, no certificate", pullCloudflare, "jellyfin.example.org", nil, 200, "173.245.48.9", true},
		{"home, no certificate", pullHome, "radarr.example.org", nil, 200, "192.168.1.23", false},
		{"home, direct name", pullHome, "jellyfin.example.org", nil, 200, "192.168.1.23", false},
		{"a stranger with the right certificate", pullStranger, "radarr.example.org", &good, 403, "", false},
		{"a stranger, direct name", pullStranger, "jellyfin.example.org", nil, 200, "198.51.100.66", false},
	}
	for _, c := range cases {
		answer := server.get(c.peer, c.host, c.cert, nil)
		if answer.status != c.status || answer.asked != c.asked || (c.status == 200 && answer.visitor != c.visitor) {
			t.Errorf("enforcing, %s: status %d visitor %q asked %v, want %d %q %v", c.name, answer.status, answer.visitor, answer.asked, c.status, c.visitor, c.asked)
		}
	}

	// Cloudflare's own certificate expected: a certificate of anyone else's making does not pass
	setOriginPulls(t, utils.ForkOriginPullsConfig{Mode: utils.OriginPullEnforce, Kind: utils.OriginPullShared, Confirmed: true})
	answer := server.get(pullCloudflare, "radarr.example.org", &good, nil)
	builtin, _ := utils.CloudflareOriginPullCA()
	if answer.status != 403 || !answer.asked || len(answer.issuers) != 1 || string(answer.issuers[0]) != string(builtin.RawSubject) {
		t.Errorf("Cloudflare's certificate expected, another presented: %+v", answer)
	}
}

// A connection that is asked for a certificate is never the continuation of
// a session that was not: otherwise what Cloudflare opened before the option
// was switched on would go on without a certificate.
func TestSessionsFromBeforeTheOptionAreNotResumed(t *testing.T) {
	previous := utils.GetBaseMainConfig()
	t.Cleanup(func() { utils.LoadBaseMainConfig(previous) })
	server := startOriginPullServer(t)
	mine := cffake.NewAuthority("this server's authority")
	good := mine.Issue(cffake.Leaf{})
	cache := tls.NewLRUClientSessionCache(8)

	setOriginPulls(t, utils.ForkOriginPullsConfig{})
	first := server.get(pullCloudflare, "radarr.example.org", &good, cache)
	second := server.get(pullCloudflare, "radarr.example.org", &good, cache)
	if first.resumed || !second.resumed {
		t.Skipf("sessions are not resumed here at all (first %v, second %v): nothing to show", first.resumed, second.resumed)
	}

	setOriginPulls(t, utils.ForkOriginPullsConfig{Mode: utils.OriginPullEnforce, Kind: utils.OriginPullOwn, Confirmed: true, Own: []utils.ForkOriginPullCert{{CA: mine.PEM}}})
	for i := 0; i < 3; i++ {
		answer := server.get(pullCloudflare, "radarr.example.org", &good, cache)
		if answer.resumed || !answer.asked || answer.status != 200 {
			t.Errorf("connection %d after the option was switched on: resumed %v, asked %v, status %d", i+1, answer.resumed, answer.asked, answer.status)
		}
	}
	// everyone else keeps their sessions
	setOriginPulls(t, utils.ForkOriginPullsConfig{Mode: utils.OriginPullEnforce, Kind: utils.OriginPullOwn, Confirmed: true, Own: []utils.ForkOriginPullCert{{CA: mine.PEM}}})
	homeCache := tls.NewLRUClientSessionCache(8)
	server.get(pullHome, "jellyfin.example.org", nil, homeCache)
	if answer := server.get(pullHome, "jellyfin.example.org", nil, homeCache); !answer.resumed {
		t.Error("a visitor from home no longer resumes a session while the option is on")
	}
}
