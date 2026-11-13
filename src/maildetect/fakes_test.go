package maildetect

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- a resolver that only knows what the test tells it ----------------------

type fakeResolver struct {
	mu    sync.Mutex
	mx    map[string][]*net.MX
	srv   map[string][]*net.SRV // "_submission._tcp.example.org"
	ip    map[string][]string
	later map[string][]string // the answer from the second question on
	asked []string            // every question, in order
	delay time.Duration
}

func (f *fakeResolver) note(question string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, q := range f.asked {
		if q == question {
			count++
		}
	}
	f.asked = append(f.asked, question)
	return count
}

func (f *fakeResolver) wait(ctx context.Context) error {
	if f.delay == 0 {
		return nil
	}
	select {
	case <-time.After(f.delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func notFound(name string) error {
	return &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

// the detector asks for absolute names: a question without the final dot
// would be tried below the server's search domains
func relative(t *testing.T, name string) string {
	if !strings.HasSuffix(name, ".") {
		panic("asked for a name without the final dot: " + name)
	}
	return strings.TrimSuffix(name, ".")
}

func (f *fakeResolver) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	name = relative(nil, name)
	f.note("MX " + name)
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	if records, ok := f.mx[name]; ok {
		return records, nil
	}
	return nil, notFound(name)
}

func (f *fakeResolver) LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error) {
	name = "_" + service + "._" + proto + "." + relative(nil, name)
	f.note("SRV " + name)
	if err := f.wait(ctx); err != nil {
		return "", nil, err
	}
	if records, ok := f.srv[name]; ok {
		return name, records, nil
	}
	return "", nil, notFound(name)
}

func (f *fakeResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	host = relative(nil, host)
	before := f.note("IP " + host)
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	answers, ok := f.ip[host]
	if later, changed := f.later[host]; changed && before > 0 {
		answers, ok = later, true
	}
	if !ok {
		return nil, notFound(host)
	}
	out := []netip.Addr{}
	for _, text := range answers {
		out = append(out, netip.MustParseAddr(text))
	}
	return out, nil
}

func (f *fakeResolver) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, q := range f.asked {
		if strings.HasPrefix(q, prefix) {
			n++
		}
	}
	return n
}

// ---- a certificate for the fake servers --------------------------------------

func newCert(t *testing.T, notAfter time.Time, names ...string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: names[0]},
		DNSNames:              names,
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

// ---- a mail server that writes down everything it is told --------------------

const secretBanner = "banner-text-that-must-not-leave-the-server"

type fakeServer struct {
	port string
	mu   sync.Mutex
	got  []string // every line received, also inside TLS
	conn int
}

func (s *fakeServer) heard() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.got...)
}

func (s *fakeServer) connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn
}

// startFake starts a server on loopback. mode: "tls" (TLS from the start),
// "starttls", "plain" (SMTP without STARTTLS), "garbage" (not a mail
// server), "silent" (accepts and says nothing), "flood" (never stops
// talking).
func startFake(t *testing.T, mode string, cert *tls.Certificate) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	s := &fakeServer{port: port}
	var config *tls.Config
	if cert != nil {
		config = &tls.Config{Certificates: []tls.Certificate{*cert}}
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conn++
			s.mu.Unlock()
			go s.serve(conn, mode, config)
		}
	}()
	return s
}

func (s *fakeServer) serve(conn net.Conn, mode string, config *tls.Config) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	switch mode {
	case "silent":
		io.Copy(io.Discard, conn)
		return
	case "garbage":
		conn.Write([]byte("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n"))
		io.Copy(io.Discard, conn)
		return
	case "flood":
		line := []byte("220-" + strings.Repeat("x", 500) + "\r\n")
		for i := 0; i < 4000; i++ {
			if _, err := conn.Write(line); err != nil {
				return
			}
		}
		return
	case "tls":
		secure := tls.Server(conn, config)
		if secure.Handshake() != nil {
			return
		}
		s.talk(secure, nil)
		return
	case "starttls":
		s.talk(conn, config)
	default:
		s.talk(conn, nil)
	}
}

// talk speaks SMTP. With a TLS configuration it offers STARTTLS, and the
// sign-in methods only once the connection is encrypted.
func (s *fakeServer) talk(conn net.Conn, upgrade *tls.Config) {
	reader := bufio.NewReader(conn)
	say := func(line string) { conn.Write([]byte(line + "\r\n")) }
	say("220 fake.test ESMTP " + secretBanner)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		s.mu.Lock()
		s.got = append(s.got, line)
		s.mu.Unlock()
		command := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(command, "EHLO"):
			say("250-fake.test " + secretBanner)
			if upgrade != nil {
				say("250 STARTTLS")
			} else {
				say("250-SIZE 1000000")
				say("250 AUTH PLAIN login X-SECRET-METHOD XOAUTH2")
			}
		case command == "STARTTLS" && upgrade != nil:
			say("220 go ahead")
			secure := tls.Server(conn, upgrade)
			if secure.Handshake() != nil {
				return
			}
			s.talk2(secure)
			return
		case command == "QUIT":
			say("221 bye")
			return
		default:
			say("502 not implemented")
		}
	}
}

// talk2 is the session after STARTTLS: no greeting, EHLO again.
func (s *fakeServer) talk2(conn net.Conn) {
	reader := bufio.NewReader(conn)
	say := func(line string) { conn.Write([]byte(line + "\r\n")) }
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		s.mu.Lock()
		s.got = append(s.got, line)
		s.mu.Unlock()
		command := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(command, "EHLO"):
			say("250-fake.test")
			say("250 AUTH PLAIN LOGIN")
		case command == "QUIT":
			say("221 bye")
			return
		default:
			say("502 not implemented")
		}
	}
}

// ---- a detector wired to the fakes -------------------------------------------

type dialLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *dialLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.calls...)
}

// testDetector connects "<any address>:<port>" to the fake server given for
// that port ("host:port" keys win over plain ports); a port without a
// server refuses the connection.
func testDetector(resolver *fakeResolver, servers map[string]*fakeServer, roots *x509.CertPool) (*Detector, *dialLog) {
	log := &dialLog{}
	d := NewDetector()
	d.Resolver = resolver
	d.OwnAddrs = func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("192.168.1.10")} }
	d.Roots = roots
	if roots == nil {
		d.Roots = x509.NewCertPool() // never the system's: the tests trust nothing by default
	}
	d.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		log.mu.Lock()
		log.calls = append(log.calls, address)
		log.mu.Unlock()
		_, port, _ := net.SplitHostPort(address)
		server := servers[address]
		if server == nil {
			server = servers[port]
		}
		if server == nil {
			return nil, errors.New("connection refused")
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", "127.0.0.1:"+server.port)
	}
	return d, log
}

func find(t *testing.T, result Result, host string, port int) Candidate {
	t.Helper()
	for _, c := range result.Candidates {
		if c.Host == host && c.Port == port {
			return c
		}
	}
	t.Fatalf("no candidate %s:%d in %+v", host, port, result.Candidates)
	return Candidate{}
}

func has(result Result, host string, port int) bool {
	for _, c := range result.Candidates {
		if c.Host == host && c.Port == port {
			return true
		}
	}
	return false
}

func list(result Result) string {
	parts := []string{}
	for _, c := range result.Candidates {
		parts = append(parts, c.Source+" "+c.Host+":"+strconv.Itoa(c.Port)+" "+c.Check)
	}
	return strings.Join(parts, " | ")
}
