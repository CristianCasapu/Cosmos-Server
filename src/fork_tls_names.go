package main

// Fork addition: a handshake tells a stranger nothing.
//
// The HTTPS port answers everyone: devices at home, Cloudflare's proxy for
// the names served through it, visitors of names served directly. Before
// this, a client that connected without a server name (SNI), or with a name
// this server does not serve, was handed the self-signed certificate, which
// lists the panel, every app and every .local name: internet-wide scanners
// index certificates by address, so that tied the address to the owner's
// domain and published the list of apps. A client that sent a name served
// through Cloudflare got its certificate and then a 403, which confirmed
// the origin to anyone who suspected it.
//
// The rule, decided in GetConfigForClient before any cryptography, by the
// peer of the connection (never by anything the client says about itself)
// and the name it asks for (utils.TLSNameIndex):
//
//	name          local peer   Cloudflare's ranges   any other peer
//	none / an IP  as before    refused               refused (*)
//	unknown       as before    refused               refused (*)
//	local only    as before    refused               refused
//	shielded      as before    as before             refused
//	direct        as before    as before             that name's certificate, naming nothing else
//
// (*) except where the server is meant to be reached by address: a new
// install or a panel at 0.0.0.0 answers as before; a public address among
// the served names, or a port route that answers any host, gets a
// certificate naming only that address.
//
// "Refused" is the TLS alert unrecognized_name (RFC 6066), what nginx's
// ssl_reject_handshake or Traefik's sniStrict answer: nothing is signed and
// no certificate is sent. The same rule is applied to every request's Host
// over TLS (a connection opened for one name could otherwise carry a
// request for another, HTTP/2 or not): a stranger gets 421 Misdirected
// Request with the same body whichever name it tried, and what the guards
// behind would have recorded is recorded. On the plain-HTTP port a stranger
// is redirected to HTTPS only for a name it could open there.
//
// Local peers take exactly the path they always took: the owner is never
// locked out at home, with or without the internet or Cloudflare.

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/azukaar/cosmos-server/src/proxy"
	"github.com/azukaar/cosmos-server/src/utils"
)

type tlsPeer int

const (
	tlsPeerLocal tlsPeer = iota
	tlsPeerCloudflare
	tlsPeerOther
	tlsPeerClasses
)

var tlsPeerNames = [tlsPeerClasses]string{"local", "cloudflare", "other"}

// why a handshake or a request was refused
type tlsRefusal int

const (
	tlsAllowed tlsRefusal = iota
	tlsNoName
	tlsUnknownName
	tlsLocalOnly
	tlsShielded
	tlsRefusals
)

var tlsRefusalNames = [tlsRefusals]string{"", "no name", "unknown name", "local name", "shielded name"}

// what is presented when a handshake is not refused
type tlsServe int

const (
	tlsServeAsBefore tlsServe = iota // what GetCertificate picks
	tlsServeName                     // that, unless it names more than the name asked for
	tlsServeAddress                  // a certificate naming only the server's address
)

// the key holds the certificate itself, so its address is never reused for
// another while the verdict is kept
type tlsVerdictKey struct {
	cert *tls.Certificate
	name string
}

// tlsNames holds the rule and what it depends on; every dependency is a
// field so that a test can give its own.
type tlsNames struct {
	// classify tells the class of the peer of a connection ("address:port")
	classify func(remote string) tlsPeer
	names    func() *utils.TLSNameIndex
	ca       func() (*x509.Certificate, crypto.Signer)
	// own reports whether a certificate is the self-signed one
	own func(cert *tls.Certificate) bool

	refused [tlsPeerClasses][tlsRefusals]atomic.Uint64

	mintedMu sync.Mutex
	minted   map[string]*tls.Certificate
	verdictsMu sync.Mutex
	verdicts   map[tlsVerdictKey]bool // true: the certificate names only that name
}

var forkTLS = &tlsNames{
	classify: classifyTLSPeer,
	names:    utils.CurrentTLSNames,
	ca:       utils.ForkCosmosCA,
	own:      func(cert *tls.Certificate) bool { return cert != nil && cert == secondaryCert },
}

// classifyTLSPeer: Cloudflare's ranges first (an address of Cloudflare's is
// never local), then the fork's one definition of local.
func classifyTLSPeer(remote string) tlsPeer {
	if utils.IsCloudflareIP(remote) {
		return tlsPeerCloudflare
	}
	if proxy.LocalNetworkPeer(remote) {
		return tlsPeerLocal
	}
	return tlsPeerOther
}

// decide is the table above. anyHostPort: the connection came in through a
// port route that answers any host.
func (p *tlsNames) decide(peer tlsPeer, class utils.TLSNameClass, index *utils.TLSNameIndex, anyHostPort bool) (tlsRefusal, tlsServe) {
	if peer == tlsPeerLocal || index.AnyName {
		return tlsAllowed, tlsServeAsBefore
	}
	switch class {
	case utils.TLSNameDirect:
		if peer == tlsPeerCloudflare {
			return tlsAllowed, tlsServeAsBefore
		}
		return tlsAllowed, tlsServeName
	case utils.TLSNameShielded:
		if peer == tlsPeerCloudflare {
			return tlsAllowed, tlsServeAsBefore
		}
		return tlsShielded, 0
	case utils.TLSNameLocalOnly:
		return tlsLocalOnly, 0
	case utils.TLSNameNone:
		if anyHostPort || len(index.PublicIPs) > 0 {
			return tlsAllowed, tlsServeAddress
		}
		return tlsNoName, 0
	}
	if anyHostPort {
		return tlsAllowed, tlsServeAddress
	}
	return tlsUnknownName, 0
}

func (p *tlsNames) decideHello(hello *tls.ClientHelloInfo) (tlsPeer, tlsRefusal, tlsServe) {
	peer := p.classify(hello.Conn.RemoteAddr().String())
	if peer == tlsPeerLocal {
		return peer, tlsAllowed, tlsServeAsBefore
	}
	port, handedOver := proxy.HandedOverPort(hello.Conn)
	index := p.names()
	refusal, serve := p.decide(peer, index.Class(hello.ServerName), index, handedOver && index.AnyHostPorts[port])
	return peer, refusal, serve
}

// refuseTLS has no certificate: a handshake given it ends in the alert
// unrecognized_name, before anything is signed.
var refuseTLS = &tls.Config{}

// wrapConfig adds the rule to the HTTPS server's configuration, around
// what it already chooses per connection (Cloudflare's client
// certificates, fork_origin_pulls.go) and per name (GetCertificate).
func (p *tlsNames) wrapConfig(base *tls.Config) {
	inner := base.GetConfigForClient
	pick := base.GetCertificate

	base.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		if hello != nil && hello.Conn != nil {
			peer, refusal, _ := p.decideHello(hello)
			if refusal != tlsAllowed {
				p.refused[peer][refusal].Add(1)
				return refuseTLS, nil
			}
		}
		if inner != nil {
			return inner(hello)
		}
		return nil, nil
	}

	base.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if hello == nil || hello.Conn == nil || pick == nil {
			if pick == nil {
				return nil, nil
			}
			return pick(hello)
		}
		_, refusal, serve := p.decideHello(hello)
		switch {
		case refusal != tlsAllowed:
			return nil, nil // not reached: refused above
		case serve == tlsServeAddress:
			return p.addressCertificate(hello), nil
		case serve == tlsServeName:
			cert, err := pick(hello)
			if err != nil || cert == nil {
				return nil, nil
			}
			return p.narrow(cert, strings.ToLower(strings.TrimSuffix(hello.ServerName, "."))), nil
		}
		return pick(hello)
	}
}

// namesOnly reports whether a certificate names nothing but the name asked
// for: each of its names is that name, a wildcard, or a domain the name is
// under (all three tell nothing the name does not), and no address.
func namesOnly(leaf *x509.Certificate, name string) bool {
	if len(leaf.IPAddresses) > 0 || len(leaf.EmailAddresses) > 0 || len(leaf.URIs) > 0 {
		return false
	}
	fits := func(d string) bool {
		d = strings.ToLower(strings.TrimSuffix(d, "."))
		return d == "" || d == name || strings.HasPrefix(d, "*.") || strings.HasSuffix(name, "."+d)
	}
	if !fits(leaf.Subject.CommonName) {
		return false
	}
	for _, d := range leaf.DNSNames {
		if !fits(d) {
			return false
		}
	}
	return true
}

func leafOf(cert *tls.Certificate) *x509.Certificate {
	if cert.Leaf != nil {
		return cert.Leaf
	}
	if len(cert.Certificate) == 0 {
		return nil
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil
	}
	return leaf
}

// narrow returns what a stranger asking for name gets instead of cert: cert
// itself when it names nothing else; a certificate of Cosmos' authority for
// exactly that name when cert is one of Cosmos' own (the self-signed one or
// another signed by its authority: a device that trusts the authority
// trusts the new one too); a publicly trusted certificate as it is (its
// names are in the public Certificate Transparency logs already, and none
// other would be trusted by the visitor's browser). nil refuses.
func (p *tlsNames) narrow(cert *tls.Certificate, name string) *tls.Certificate {
	key := tlsVerdictKey{cert, name}
	p.verdictsMu.Lock()
	fine := p.verdicts[key]
	p.verdictsMu.Unlock()
	if fine {
		return cert
	}
	leaf := leafOf(cert)
	if leaf == nil {
		return nil
	}
	if namesOnly(leaf, name) {
		p.verdictsMu.Lock()
		if p.verdicts == nil || len(p.verdicts) >= tlsMintedMax {
			p.verdicts = map[tlsVerdictKey]bool{} // certificates replaced since are let go
		}
		p.verdicts[key] = true
		p.verdictsMu.Unlock()
		return cert
	}
	caCert, _ := p.ca()
	ours := p.own(cert) || (caCert != nil && bytes.Equal(leaf.RawIssuer, caCert.RawSubject) && leaf.CheckSignatureFrom(caCert) == nil)
	if !ours {
		return cert
	}
	return p.mint(name, nil)
}

// addressCertificate is the certificate for a connection without a name
// where the server is meant to be reached by address: the address the
// connection came in on when it is one of the served ones or came through a
// port route that answers any host, else the served public addresses.
func (p *tlsNames) addressCertificate(hello *tls.ClientHelloInfo) *tls.Certificate {
	var local net.IP
	if addr, ok := hello.Conn.LocalAddr().(*net.TCPAddr); ok {
		local = addr.IP
	}
	index := p.names()
	ips := index.PublicIPs
	for _, ip := range index.PublicIPs {
		if ip.Equal(local) {
			ips = []net.IP{ip}
			break
		}
	}
	if port, ok := proxy.HandedOverPort(hello.Conn); ok && index.AnyHostPorts[port] && local != nil {
		ips = []net.IP{local}
	}
	if len(ips) == 0 {
		return nil
	}
	return p.mint("", ips)
}

// how many certificates made here are kept, and for how long one is made
const (
	tlsMintedMax  = 512
	tlsMintedLife = 90 * 24 * time.Hour
)

// mint makes (once) a certificate for exactly one name, or for the given
// addresses, signed by Cosmos' authority. nil when there is no authority.
// The names come from the configuration or from the server's own addresses,
// never from a client alone.
func (p *tlsNames) mint(name string, ips []net.IP) *tls.Certificate {
	caCert, caKey := p.ca()
	if caCert == nil || caKey == nil {
		return nil
	}
	sum := sha256.Sum256(caCert.Raw)
	key := fmt.Sprintf("%x|%s", sum[:8], name)
	for _, ip := range ips {
		key += "|" + ip.String()
	}

	p.mintedMu.Lock()
	defer p.mintedMu.Unlock()
	if cert := p.minted[key]; cert != nil {
		// made again once three quarters of its life are gone
		life := cert.Leaf.NotAfter.Sub(cert.Leaf.NotBefore)
		if time.Now().Before(cert.Leaf.NotBefore.Add(life * 3 / 4)) {
			return cert
		}
	}

	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil
	}
	notAfter := time.Now().Add(tlsMintedLife)
	if caCert.NotAfter.Before(notAfter) {
		notAfter = caCert.NotAfter
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           ips,
		AuthorityKeyId:        caCert.SubjectKeyId,
	}
	if name != "" {
		template.DNSNames = []string{name}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &private.PublicKey, caKey)
	if err != nil {
		utils.Error("TLS: cannot make a certificate for one name", err)
		return nil
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil
	}
	cert := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: private, Leaf: leaf}
	if p.minted == nil || len(p.minted) >= tlsMintedMax {
		p.minted = map[string]*tls.Certificate{}
	}
	p.minted[key] = cert
	return cert
}

// misdirected answers a request whose Host the peer could not have opened
// a handshake for. One body for every such Host.
func misdirected(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusMisdirectedRequest)
	io.WriteString(w, "Misdirected Request\n")
}

// handler applies the rule to the Host of every request over TLS, before
// every other guard (whose records it writes in their place).
func (p *tlsNames) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			next.ServeHTTP(w, r)
			return
		}
		peer := p.classify(r.RemoteAddr)
		if peer == tlsPeerLocal {
			next.ServeHTTP(w, r)
			return
		}
		index := p.names()
		port, handedOver := proxy.HandoffPort(r)
		refusal, _ := p.decide(peer, index.Class(r.Host), index, handedOver && index.AnyHostPorts[port])
		if refusal == tlsAllowed {
			next.ServeHTTP(w, r)
			return
		}
		switch refusal {
		case tlsShielded:
			utils.RecordSecurity(r, utils.SecAccessOriginDenied, "", "reason", "not_cloudflare")
		case tlsLocalOnly:
			reason := "local_name"
			if index.IsHome(r.Host) {
				reason = "home_name"
			}
			utils.RecordSecurity(r, utils.SecAccessLocalDenied, "", "reason", reason)
		default:
			utils.RecordSecurity(r, utils.SecShieldHostname, "")
			if peer == tlsPeerOther {
				if ip, _ := utils.SplitIP(r.RemoteAddr); ip != "" {
					utils.IncrementIPAbuseCounter(ip)
				}
			}
		}
		misdirected(w)
	})
}

// plainRedirectAllowed: on the plain-HTTP port, the redirect to HTTPS is
// for a name the peer could open there; anything else gets the 404 an
// unknown name gets.
func (p *tlsNames) plainRedirectAllowed(r *http.Request) bool {
	peer := p.classify(r.RemoteAddr)
	if peer == tlsPeerLocal {
		return true
	}
	index := p.names()
	refusal, _ := p.decide(peer, index.Class(r.Host), index, false)
	return refusal == tlsAllowed
}

// tlsRefusedLog is net/http's log of failed handshakes without the lines of
// handshakes this rule refused (one per scanner's probe): every other line
// is passed on as it was.
type tlsRefusedLog struct{ out func() io.Writer }

func (l tlsRefusedLog) Write(line []byte) (int, error) {
	if bytes.Contains(line, []byte("TLS handshake error")) && bytes.Contains(line, []byte("tls: no certificates configured")) {
		return len(line), nil
	}
	return l.out().Write(line)
}

var tlsSummaryOnce sync.Once

// summary logs, once an hour when there were any, how many handshakes were
// refused, by peer and reason. Counts only: nothing a client sent.
func (p *tlsNames) summary() string {
	parts := []string{}
	for peer := tlsPeer(0); peer < tlsPeerClasses; peer++ {
		for reason := tlsNoName; reason < tlsRefusals; reason++ {
			if n := p.refused[peer][reason].Swap(0); n > 0 {
				parts = append(parts, fmt.Sprintf("%d from %s (%s)", n, tlsPeerNames[peer], tlsRefusalNames[reason]))
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "TLS: refused handshakes in the last hour: " + strings.Join(parts, ", ")
}

func forkTLSNamesWith(rule *tlsNames, server *http.Server) {
	rule.wrapConfig(server.TLSConfig)
	server.Handler = rule.handler(server.Handler)
	server.ErrorLog = log.New(tlsRefusedLog{out: func() io.Writer { return log.Writer() }}, "", log.LstdFlags)
}

// forkTLSNames is the hook of startHTTPSServer: the rule on the HTTPS
// server's handshakes, requests and handshake log.
func forkTLSNames(server *http.Server) {
	forkTLSNamesWith(forkTLS, server)
	tlsSummaryOnce.Do(func() {
		go func() {
			for range time.Tick(time.Hour) {
				if line := forkTLS.summary(); line != "" {
					utils.Log(line)
				}
			}
		}()
	})
}
