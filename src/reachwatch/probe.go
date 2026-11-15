package reachwatch

// One test of one name: a small request that goes out to Cloudflare and,
// if the path works, comes back in to this server.
//
//   - The name is looked up through the system resolver. The connection is
//     made only to an address in Cloudflare's ranges that the lookup gave:
//     whatever DNS says, the watch never connects to the house, the local
//     network or any other host.
//   - TLS is verified for the name; no proxy from the environment, no
//     keep-alive, no cookie jar, no credentials; redirects are not followed.
//   - The token in X-Cosmos-Watch is the proof (utils.NoteWatchProbe): only
//     when it arrived did the request travel the whole way.

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// UserAgent marks the watch's requests in the server's request log.
const UserAgent = "Cosmos-Reachability-Watch/1"

// Path is what is asked for: upstream's /_health, which answers every host.
const Path = "/_health"

const maxBody = 2048

// Bounds of one test. Variables for the tests.
var (
	lookupTimeout  = 5 * time.Second
	dialTimeout    = 10 * time.Second
	tlsTimeout     = 10 * time.Second
	answerTimeout  = 30 * time.Second // Cloudflare gives up connecting after 19 s and answers 522
	requestTimeout = 40 * time.Second
	maxAddresses   = 2
)

// Replaced by the tests: nothing here may reach a real resolver or Cloudflare.
var (
	resolve = func(ctx context.Context, host string) ([]net.IP, error) {
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		ips := make([]net.IP, 0, len(addrs))
		for _, a := range addrs {
			ips = append(ips, a.IP)
		}
		return ips, err
	}
	isCloudflare = func(ip net.IP) bool { return utils.IsCloudflareIP(ip.String()) }
	rootCAs      *x509.CertPool // nil: the system's
	newToken     = utils.NewWatchToken
	tokenArrived = utils.WatchTokenArrived
	randomHex    = func() string {
		b := make([]byte, 4)
		rand.Read(b)
		return hex.EncodeToString(b)
	}
	// dialAddress replaces the address dialled, for the tests (an
	// httptest server stands in for Cloudflare's edge)
	dialAddress = func(ip net.IP, port int) string { return net.JoinHostPort(ip.String(), strconv.Itoa(port)) }
)

// Result is an outcome with what is shown about it.
type Result struct {
	Outcome Outcome
	Verdict Verdict
	At      time.Time
	// Visitor: the address Cloudflare reported for the request, which is
	// this server's public address as the internet sees it ("" unless it
	// arrived).
	Visitor string
	// Raw: what happened, in one technical line
	Raw string
}

// testName tests one name on the public HTTPS port.
func testName(ctx context.Context, name string, port int) Result {
	o, raw, visitor := probe(ctx, name, port)
	return Result{Outcome: o, Verdict: Classify(o), Raw: raw, Visitor: visitor}
}

func target(name string, port int) string {
	address := "https://" + name
	if port != 443 {
		address += ":" + strconv.Itoa(port)
	}
	return address + Path
}

func probe(ctx context.Context, name string, port int) (Outcome, string, string) {
	o := Outcome{}
	where := "GET " + target(name, port)

	lookupCtx, cancel := context.WithTimeout(ctx, lookupTimeout)
	ips, err := resolve(lookupCtx, name)
	cancel()
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			o.NotFound = true
			return o, where + ": the name does not exist", ""
		}
		o.LookupFailed = true
		return o, where + ": the name could not be looked up", ""
	}
	if len(ips) == 0 {
		o.NotFound = true
		return o, where + ": the name has no address", ""
	}
	edge, public := []net.IP{}, 0
	for _, ip := range ips {
		switch {
		case isCloudflare(ip):
			edge = append(edge, ip)
		case ip.IsGlobalUnicast() && !ip.IsPrivate():
			public++
		}
	}
	if len(edge) == 0 {
		if public > 0 {
			o.NoCloudflare = true
			return o, where + ": the name does not resolve to Cloudflare", ""
		}
		o.OnlyPrivate = true
		return o, where + ": the name resolves to local addresses only", ""
	}
	// IPv4 first: the house's IPv6 is not what is tested
	ordered := []net.IP{}
	for _, ip := range edge {
		if ip.To4() != nil {
			ordered = append(ordered, ip)
		}
	}
	for _, ip := range edge {
		if ip.To4() == nil {
			ordered = append(ordered, ip)
		}
	}
	if len(ordered) > maxAddresses {
		ordered = ordered[:maxAddresses]
	}

	for i, ip := range ordered {
		last := i == len(ordered)-1
		out, raw, visitor, dialFailed := request(ctx, name, port, ip)
		if dialFailed && !last {
			continue
		}
		return out, where + " (" + ip.String() + "): " + raw, visitor
	}
	return o, where, "" // not reached
}

// request makes the one request through the address ip.
func request(ctx context.Context, name string, port int, ip net.IP) (o Outcome, raw string, visitor string, dialFailed bool) {
	token := newToken(name)
	if token == "" {
		o.NoAnswer = true
		return o, "too many tests at once", "", false
	}
	var dialErr atomic.Bool
	dialer := &net.Dialer{Timeout: dialTimeout}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, "tcp", dialAddress(ip, port))
			if err != nil {
				dialErr.Store(true)
			}
			return conn, err
		},
		TLSClientConfig:        &tls.Config{ServerName: name, MinVersion: tls.VersionTLS12, RootCAs: rootCAs},
		TLSHandshakeTimeout:    tlsTimeout,
		ResponseHeaderTimeout:  answerTimeout,
		DisableKeepAlives:      true,
		MaxResponseHeaderBytes: 64 << 10,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport:     transport,
		Timeout:       requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, "GET", target(name, port)+"?cosmos-watch="+randomHex(), nil)
	if err != nil {
		tokenArrived(token)
		o.NoAnswer = true
		return o, "no request could be made", "", false
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set(utils.ReachWatchHeader, token)
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := client.Do(req)
	if err != nil {
		tokenArrived(token)
		var unknownAuthority x509.UnknownAuthorityError
		var hostname x509.HostnameError
		var invalid x509.CertificateInvalidError
		var verification *tls.CertificateVerificationError
		var netErr net.Error
		switch {
		case dialErr.Load():
			o.DialFailed = true
			return o, "no connection to Cloudflare", "", true
		case errors.As(err, &unknownAuthority), errors.As(err, &hostname), errors.As(err, &invalid), errors.As(err, &verification):
			o.EdgeCertBad = true
			return o, "Cloudflare's certificate does not verify: " + oneLine(err.Error()), "", false
		case errors.As(err, &netErr) && netErr.Timeout():
			o.NoAnswer = true
			return o, "no answer in time", "", false
		default:
			o.TLSFailed = true
			return o, "the connection broke: " + oneLine(err.Error()), "", false
		}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	resp.Body.Close()
	o.Status = resp.StatusCode
	o.CloudflareMark = resp.Header.Get("CF-Ray") != "" || strings.EqualFold(resp.Header.Get("Server"), "cloudflare")
	o.Challenge = strings.EqualFold(resp.Header.Get("cf-mitigated"), "challenge")
	o.Body = string(body)
	o.Arrived, visitor = tokenArrived(token)
	raw = "answer " + strconv.Itoa(resp.StatusCode)
	if ray := resp.Header.Get("CF-Ray"); ray != "" && len(ray) <= 64 {
		raw += " (CF-Ray " + oneLine(ray) + ")"
	}
	if o.Arrived {
		raw += ", the request arrived at this server"
	} else {
		raw += ", the request did not arrive at this server"
	}
	return o, raw, visitor, false
}

// oneLine keeps a text to one line of printable characters, at most 300.
func oneLine(text string) string {
	out := []rune{}
	for _, r := range text {
		if r < 0x20 || r == 0x7f {
			r = ' '
		}
		out = append(out, r)
		if len(out) >= 300 {
			break
		}
	}
	return string(out)
}
