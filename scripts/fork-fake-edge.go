//go:build ignore

// Fork addition: a stand-in for Cloudflare's proxy in front of a test
// server, for trying the reachability watch (src/reachwatch) for real
// without the real Cloudflare: the path name -> edge -> router -> server.
// Used with scripts/fork-reach-watch-e2e.sh.
//
//	go run scripts/fork-fake-edge.go <origin address:port> <address to come from> [name=address ...]
//
// It is, at once:
//
//   - the name server of the test (UDP :53): example.org and every name
//     under it resolve to <address to come from> (one of Cloudflare's
//     published addresses, such as 173.245.48.5, added to this container's
//     interface), as a name proxied by Cloudflare does; name=address
//     arguments add other names (api.telegram.org for the Telegram
//     stand-in). Anything else does not exist.
//   - Cloudflare's edge (HTTPS :2053 and :443) with a certificate for
//     example.org and *.example.org from an authority it makes at start and
//     writes to /shared/ca.pem. Every request is passed on to the origin
//     address the way Cloudflare's proxy does: a new connection from
//     <address to come from>, TLS with the visitor's name, CF-Connecting-IP
//     set to the visitor's address, X-Forwarded-For appended, CF-Ray. When
//     the leg behind it is broken it answers as Cloudflare does, with its own
//     page: 522 when the connection is not answered (6 s here, 19 s at
//     Cloudflare), 521 when it is refused, 523 when there is no route, 525
//     when TLS fails, 526 when the certificate does not verify in "strict",
//     524 when the origin does not answer in time, 520 when the answer is
//     broken.
//
// Control port (:8082, plain):
//
//	GET /dns?mode=proxied|direct|nx   names lead to the edge (proxied), to the
//	                                  origin's address (the proxy switched off),
//	                                  or do not exist
//	GET /ssl?mode=full|strict         "Full" accepts any certificate of the
//	                                  origin, "Full (strict)" verifies it
//	GET /calls                        what was passed on and how it ended
//
// How it is used: on Docker networks made with --internal (no way out); the
// test server's resolver is this program. Nothing is ever sent to the real
// Cloudflare. It is not part of any build.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	mu      sync.Mutex
	dnsMode = "proxied"
	strict  = false
	calls   = []string{}
	origin  string
	from    string
	extra   = map[string]string{}
)

func note(line string) {
	mu.Lock()
	defer mu.Unlock()
	calls = append(calls, time.Now().Format("15:04:05")+" "+line)
	if len(calls) > 500 {
		calls = calls[len(calls)-500:]
	}
	log.Println(line)
}

func main() {
	if len(os.Args) < 3 {
		log.Fatal("usage: fork-fake-edge <origin address:port> <address to come from> [name=address ...]")
	}
	origin, from = os.Args[1], os.Args[2]
	for _, pair := range os.Args[3:] {
		if name, address, ok := strings.Cut(pair, "="); ok {
			extra[strings.ToLower(name)] = address
		}
	}
	cert := makeCertificate()
	go dnsServer()
	for _, port := range []string{":2053", ":443"} {
		server := &http.Server{Addr: port, Handler: http.HandlerFunc(edge), TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
			ReadHeaderTimeout: 10 * time.Second}
		go func() { log.Fatal(server.ListenAndServeTLS("", "")) }()
	}
	control := http.NewServeMux()
	control.HandleFunc("/dns", func(w http.ResponseWriter, r *http.Request) {
		mode := r.URL.Query().Get("mode")
		if mode != "proxied" && mode != "direct" && mode != "nx" {
			http.Error(w, "mode", 400)
			return
		}
		mu.Lock()
		dnsMode = mode
		mu.Unlock()
		note("control: dns " + mode)
		w.Write([]byte(mode + "\n"))
	})
	control.HandleFunc("/ssl", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		strict = r.URL.Query().Get("mode") == "strict"
		mu.Unlock()
		note("control: ssl " + r.URL.Query().Get("mode"))
		w.Write([]byte("ok\n"))
	})
	control.HandleFunc("/calls", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		json.NewEncoder(w).Encode(calls)
	})
	log.Println("fake Cloudflare edge ready: origin " + origin + ", coming from " + from)
	log.Fatal(http.ListenAndServe(":8082", control))
}

// --- the certificate of the edge

func makeCertificate() tls.Certificate {
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test edge authority (not Cloudflare)"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		log.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "example.org"},
		DNSNames: []string{"example.org", "*.example.org"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, caCert, &key.PublicKey, caKey)
	if err != nil {
		log.Fatal(err)
	}
	os.MkdirAll("/shared", 0755)
	os.WriteFile("/shared/ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0644)
	return tls.Certificate{Certificate: [][]byte{der, caDER}, PrivateKey: key}
}

// --- the proxy

func ray() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b) + "-TST"
}

var titles = map[int]string{520: "Web server is returning an unknown error", 521: "Web server is down", 522: "Connection timed out",
	523: "Origin is unreachable", 524: "A timeout occurred", 525: "SSL handshake failed", 526: "Invalid SSL certificate"}

func errorPage(w http.ResponseWriter, host string, code int, id string) {
	w.Header().Set("Server", "cloudflare")
	w.Header().Set("CF-Ray", id)
	w.Header().Set("Content-Type", "text/html; charset=UTF-8")
	w.WriteHeader(code)
	io.WriteString(w, "<!DOCTYPE html><html><head><title>"+host+" | "+itoa(code)+": "+titles[code]+"</title></head><body>"+
		"<h1>Error "+itoa(code)+"</h1><p>"+titles[code]+"</p><p>Cloudflare Ray ID: "+id+" (test stand-in, not Cloudflare)</p></body></html>")
}

func itoa(n int) string { return strconv.Itoa(n) }

func edge(w http.ResponseWriter, r *http.Request) {
	id := ray()
	host := strings.ToLower(r.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	visitor, _, _ := net.SplitHostPort(r.RemoteAddr)
	line := r.Method + " " + r.Host + r.URL.Path + " from " + visitor + ": "

	dialer := &net.Dialer{Timeout: 6 * time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(from)}}
	raw, err := dialer.DialContext(r.Context(), "tcp", origin)
	if err != nil {
		code := 522
		var errno syscall.Errno
		switch {
		case errors.As(err, &errno) && errno == syscall.ECONNREFUSED:
			code = 521
		case errors.As(err, &errno) && (errno == syscall.ENETUNREACH || errno == syscall.EHOSTUNREACH):
			code = 523
		}
		note(line + itoa(code) + " (" + err.Error() + ")")
		errorPage(w, host, code, id)
		return
	}
	mu.Lock()
	verify := strict
	mu.Unlock()
	conn := tls.Client(raw, &tls.Config{ServerName: host, InsecureSkipVerify: !verify, NextProtos: []string{"http/1.1"}})
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := conn.HandshakeContext(r.Context()); err != nil {
		raw.Close()
		var verification *tls.CertificateVerificationError
		code := 525
		if errors.As(err, &verification) {
			code = 526
		}
		note(line + itoa(code) + " (" + err.Error() + ")")
		errorPage(w, host, code, id)
		return
	}
	conn.SetDeadline(time.Time{})
	used := false
	transport := &http.Transport{
		DialTLSContext: func(context.Context, string, string) (net.Conn, error) {
			if used {
				return nil, errors.New("one connection per request")
			}
			used = true
			return conn, nil
		},
		ResponseHeaderTimeout: 20 * time.Second,
		DisableKeepAlives:     true,
	}
	defer transport.CloseIdleConnections()
	out, _ := http.NewRequestWithContext(r.Context(), r.Method, "https://"+r.Host+r.URL.RequestURI(), r.Body)
	out.Header = r.Header.Clone()
	out.Header.Set("CF-Connecting-IP", visitor)
	out.Header.Set("CF-Ray", id)
	out.Header.Set("X-Forwarded-For", strings.TrimPrefix(r.Header.Get("X-Forwarded-For")+", "+visitor, ", "))
	out.Header.Set("X-Forwarded-Proto", "https")
	out.Host = r.Host
	resp, err := transport.RoundTrip(out)
	if err != nil {
		code := 520
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			code = 524
		}
		note(line + itoa(code) + " (" + err.Error() + ")")
		errorPage(w, host, code, id)
		return
	}
	defer resp.Body.Close()
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.Header().Set("Server", "cloudflare")
	w.Header().Set("CF-Ray", id)
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
	note(line + "origin answered " + itoa(resp.StatusCode))
}

// --- the name server (UDP, A records, enough for Go's resolver)

func dnsServer() {
	// bound to that address: an answer must come from the address asked
	conn, err := net.ListenPacket("udp", net.JoinHostPort(from, "53"))
	if err != nil {
		log.Fatal(err)
	}
	buf := make([]byte, 1500)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil || n < 12 {
			continue
		}
		if answer := dnsAnswer(buf[:n]); answer != nil {
			conn.WriteTo(answer, addr)
		}
	}
}

func dnsAnswer(q []byte) []byte {
	// the question: name labels, then type and class
	i := 12
	labels := []string{}
	for i < len(q) && q[i] != 0 {
		l := int(q[i])
		if l > 63 || i+1+l > len(q) {
			return nil
		}
		labels = append(labels, string(q[i+1:i+1+l]))
		i += 1 + l
	}
	if i+5 > len(q) {
		return nil
	}
	end := i + 5
	qtype := binary.BigEndian.Uint16(q[i+1 : i+3])
	name := strings.ToLower(strings.Join(labels, "."))

	mu.Lock()
	mode := dnsMode
	mu.Unlock()
	address, known := extra[name]
	inZone := name == "example.org" || strings.HasSuffix(name, ".example.org")
	if !known && inZone {
		switch mode {
		case "proxied":
			address, known = from, true
		case "direct":
			host, _, _ := net.SplitHostPort(origin)
			address, known = host, true
		}
	}
	out := append([]byte{}, q[:end]...)
	out[2] = 0x84 | (q[2] & 0x01) // answer, authoritative, recursion desired copied
	out[3] = 0x80                 // recursion available
	binary.BigEndian.PutUint16(out[6:8], 0)
	binary.BigEndian.PutUint16(out[8:10], 0)
	binary.BigEndian.PutUint16(out[10:12], 0)
	if !known {
		out[3] |= 3 // NXDOMAIN
		return out
	}
	ip := net.ParseIP(address).To4()
	if qtype != 1 || ip == nil {
		return out // the name exists, no record of that type
	}
	binary.BigEndian.PutUint16(out[6:8], 1)
	out = append(out, 0xc0, 12, 0, 1, 0, 1, 0, 0, 0, 30, 0, 4)
	return append(out, ip...)
}
