package reachwatch

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// edgeRig stands an httptest TLS server in for Cloudflare's edge. The names
// resolve to what resolveTo says; every connection goes to the stand-in.
type edgeRig struct {
	mu       sync.Mutex
	server   *httptest.Server
	requests []*http.Request
	handler  func(w http.ResponseWriter, r *http.Request)
	dialled  []string
}

func newEdge(t *testing.T, resolveTo ...string) *edgeRig {
	t.Helper()
	e := &edgeRig{}
	e.server = httptest.NewUnstartedServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.requests = append(e.requests, r.Clone(context.Background()))
		handler := e.handler
		e.mu.Unlock()
		handler(rw, r)
	}))
	cert, leaf := edgeCertificate(t)
	e.server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	e.server.StartTLS()
	saved := []interface{}{resolve, rootCAs, dialAddress}
	resolve = func(ctx context.Context, host string) ([]net.IP, error) {
		ips := []net.IP{}
		for _, text := range resolveTo {
			ips = append(ips, net.ParseIP(text))
		}
		return ips, nil
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	rootCAs = pool
	_, port, _ := net.SplitHostPort(e.server.Listener.Addr().String())
	dialAddress = func(ip net.IP, _ int) string {
		e.mu.Lock()
		e.dialled = append(e.dialled, ip.String())
		e.mu.Unlock()
		return "127.0.0.1:" + port
	}
	t.Cleanup(func() {
		e.server.Close()
		resolve = saved[0].(func(context.Context, string) ([]net.IP, error))
		rootCAs = saved[1].(*x509.CertPool)
		dialAddress = saved[2].(func(net.IP, int) string)
	})
	return e
}

// asOrigin is what this server's /_health does when the request comes in
// from Cloudflare's address.
func asOrigin(rw http.ResponseWriter, r *http.Request) {
	r.RemoteAddr = "173.245.48.9:40000"
	utils.NoteWatchProbe(r)
	rw.Header().Set("Server", "cloudflare")
	rw.Header().Set("CF-Ray", "8a1b2c3d4e5f6a7b-AMS")
	rw.Write([]byte("OK"))
}

func TestARequestThatComesBackInIsSeen(t *testing.T) {
	e := newEdge(t, "173.245.48.5")
	e.handler = asOrigin
	r := testName(context.Background(), "example.org", 2053)
	if r.Verdict.Cause != "seen" || !r.Outcome.Arrived {
		t.Fatalf("%+v", r)
	}
	req := e.requests[0]
	if req.Header.Get("User-Agent") != UserAgent || len(req.Header.Get(utils.ReachWatchHeader)) != 32 {
		t.Errorf("headers %v", req.Header)
	}
	if req.Header.Get("Cookie") != "" || req.Header.Get("Authorization") != "" {
		t.Error("a credential was sent")
	}
	if req.URL.Path != Path || !strings.HasPrefix(req.URL.RawQuery, "cosmos-watch=") || req.Host != "example.org:2053" {
		t.Errorf("request %s %s %s", req.Host, req.URL.Path, req.URL.RawQuery)
	}
	if strings.Contains(r.Raw, req.Header.Get(utils.ReachWatchHeader)) {
		t.Error("the token is in the technical text")
	}
}

func TestAnAnswerWithoutTheTokenIsForeign(t *testing.T) {
	e := newEdge(t, "173.245.48.5")
	e.handler = func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("CF-Ray", "8a1b")
		rw.Write([]byte("OK"))
	}
	if r := testName(context.Background(), "example.org", 2053); r.Verdict.Cause != "foreign" {
		t.Fatalf("%+v", r.Verdict)
	}
}

func TestCloudflaresErrorIsItsCause(t *testing.T) {
	e := newEdge(t, "173.245.48.5")
	e.handler = func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Server", "cloudflare")
		rw.WriteHeader(522)
		rw.Write([]byte(cloudflarePage + strings.Repeat("x", 10000)))
	}
	r := testName(context.Background(), "example.org", 2053)
	if r.Verdict.Cause != "connect" || r.Verdict.Code != "522" {
		t.Fatalf("%+v", r.Verdict)
	}
	if len(r.Outcome.Body) > maxBody {
		t.Fatalf("read %d bytes", len(r.Outcome.Body))
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	e := newEdge(t, "173.245.48.5")
	e.handler = func(rw http.ResponseWriter, r *http.Request) {
		http.SetCookie(rw, &http.Cookie{Name: "jar", Value: "x"})
		http.Redirect(rw, r, "https://elsewhere.example.net/", http.StatusFound)
	}
	r := testName(context.Background(), "example.org", 2053)
	if len(e.requests) != 1 || r.Verdict.Cause != "foreign" || r.Outcome.Status != 302 {
		t.Fatalf("%d requests, %+v", len(e.requests), r.Verdict)
	}
}

func TestOnlyCloudflaresAddressesAreConnectedTo(t *testing.T) {
	e := newEdge(t, "203.0.113.7", "192.168.1.10")
	e.handler = asOrigin
	r := testName(context.Background(), "example.org", 2053)
	if r.Verdict.Cause != "dnsNotCloudflare" || len(e.dialled) != 0 {
		t.Fatalf("%+v, dialled %v", r.Verdict, e.dialled)
	}
	e2 := newEdge(t, "192.168.1.10", "2606:4700::1111", "fd00::1")
	e2.handler = asOrigin
	r = testName(context.Background(), "example.org", 2053)
	if r.Verdict.Cause != "seen" || len(e2.dialled) != 1 || e2.dialled[0] != "2606:4700::1111" {
		t.Fatalf("%+v, dialled %v", r.Verdict, e2.dialled)
	}
	e3 := newEdge(t, "192.168.1.10")
	if r := testName(context.Background(), "example.org", 2053); r.Verdict.Cause != "dnsLocal" || len(e3.dialled) != 0 {
		t.Fatalf("%+v", r.Verdict)
	}
}

func TestTheEdgesCertificateIsVerified(t *testing.T) {
	e := newEdge(t, "173.245.48.5")
	e.handler = asOrigin
	rootCAs = x509.NewCertPool() // trusts nothing
	r := testName(context.Background(), "example.org", 2053)
	if r.Verdict.Cause != "edgeCert" || len(e.requests) != 0 {
		t.Fatalf("%+v", r.Verdict)
	}
}

func TestNoWayOutIsUnknown(t *testing.T) {
	e := newEdge(t, "173.245.48.5", "173.245.48.6")
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	_, port, _ := net.SplitHostPort(closed.Addr().String())
	closed.Close()
	dialAddress = func(ip net.IP, _ int) string { e.dialled = append(e.dialled, ip.String()); return "127.0.0.1:" + port }
	r := testName(context.Background(), "example.org", 2053)
	if r.Verdict.Status != StatusUnknown || r.Verdict.Cause != "offline" || len(e.dialled) != 2 {
		t.Fatalf("%+v, dialled %v", r.Verdict, e.dialled)
	}
	resolve = func(context.Context, string) ([]net.IP, error) { return nil, errors.New("no answer") }
	if r := testName(context.Background(), "example.org", 2053); r.Verdict.Cause != "offline" {
		t.Fatalf("%+v", r.Verdict)
	}
	resolve = func(context.Context, string) ([]net.IP, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "example.org", IsNotFound: true}
	}
	if r := testName(context.Background(), "example.org", 2053); r.Verdict.Cause != "dnsNotFound" {
		t.Fatalf("%+v", r.Verdict)
	}
}

func TestTheAddressCarriesThePort(t *testing.T) {
	if target("example.org", 443) != "https://example.org/_health" || target("example.org", 2053) != "https://example.org:"+strconv.Itoa(2053)+"/_health" {
		t.Error(target("example.org", 443), target("example.org", 2053))
	}
}

// edgeCertificate is a certificate for example.org and its names, as
// Cloudflare's edge has one.
func edgeCertificate(t *testing.T) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"example.org", "*.example.org"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, leaf
}
