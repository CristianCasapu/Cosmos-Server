//go:build ignore

// Fork addition: a stand-in for Cloudflare's API, for trying the Cloudflare
// integration (src/cloudflare) in a browser without sending anything to
// Cloudflare. Used with scripts/fork-ui-check-cloudflare.mjs. The stand-in
// itself is the package the Go tests use (src/cloudflare/cffake).
//
// It answers HTTPS on :443 as api.cloudflare.com, with a certificate from an
// authority it makes at start and writes to /shared/ca.pem, and has a plain
// control port (:8080): GET /records lists the records of example.org, GET
// /calls the requests received (?clear=1 empties the list), GET
// /proxied?name=<record>&on=<0|1> flips a record's flag the way an edit in
// Cloudflare's dashboard would.
//
// What it holds at start is the layout this was written for: the domain
// example.org whose own name is proxied and points at the address the test
// server advertises (203.0.113.10), a website alias, a mail record; one
// token limited to that domain, one that may only read, one that sees a
// second domain too.
//
// How it is used: on a Docker network made with --internal (no way out), run
// it in a container with the network alias api.cloudflare.com and a folder
// mounted at /shared; start the throwaway Cosmos on the same network with
// that folder mounted and SSL_CERT_FILE=/shared/ca.pem. The server's own code
// then talks to this program, unchanged: fixed host, certificate verified.
// It is not part of any build (go run scripts/fork-fake-cloudflare.go).
//
// Authenticated Origin Pulls. The stand-in also takes the client certificate
// Cosmos uploads for a domain (with the token SSLToken, which may do that
// and nothing else; the "Edit zone DNS" tokens above may not) and lists what
// it holds at GET /aop on the control port. A second role stands in for
// Cloudflare's proxy:
//
//	fork-fake-cloudflare edge <cosmos address:port> <address to come from> <control URL of the API stand-in>
//
// It has to run inside the test Cosmos' own network namespace, where
// <address to come from> (one of Cloudflare's published addresses, such as
// 173.245.48.5) was added to the loopback interface: the requests then reach
// Cosmos from that address without any route on the host or the test network
// being touched. Its control port (:8081) makes it visit:
//
//	GET /visit?host=<name>&cert=uploaded|wrong|none&n=<count>&visitor=<address>&path=<path>
//
// "uploaded" presents the certificate Cosmos gave to the API stand-in, the
// way Cloudflare presents what was uploaded for a zone; "wrong" one from an
// authority of its own; "none" nothing. The answer lists the status of each
// request and whether Cosmos asked for a certificate.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/cloudflare/cffake"
)

const (
	GoodToken  = "cfut_dGVzdC10b2tlbi1mb3ItdGhlLWJyb3dzZXItY2hlY2s0123456789"
	ReadToken  = "readonly0123456789abcdefghijklmnopqrstuvwxYZ"
	BroadToken = "allzones0123456789abcdefghijklmnopqrstuvwxYZ"
	// SSLToken may upload a client certificate for example.org ("SSL and
	// Certificates: Edit") and nothing else: it does not even list the domain
	SSLToken = "sslcerts0123456789abcdefghijklmnopqrstuvwxYZ"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "edge" {
		if len(os.Args) != 5 {
			log.Fatal("usage: edge <cosmos address:port> <address to come from> <control URL of the API stand-in>")
		}
		edge(os.Args[2], os.Args[3], os.Args[4])
		return
	}

	fake := cffake.New()
	fake.AddZone("example.org",
		cffake.Record{Type: "A", Name: "example.org", Content: "203.0.113.10", Proxied: true},
		cffake.Record{Type: "CNAME", Name: "www.example.org", Content: "example.org", Proxied: true},
		cffake.Record{Type: "MX", Name: "example.org", Content: "mail.example.org"},
		cffake.Record{Type: "A", Name: "mail.example.org", Content: "203.0.113.10"},
		cffake.Record{Type: "TXT", Name: "example.org", Content: `"v=spf1 mx -all"`},
	)
	fake.AddZone("example.net")
	fake.AddToken(GoodToken, cffake.Token{Zones: []string{"example.org"}, ReadDNS: true, EditDNS: true})
	fake.AddToken(ReadToken, cffake.Token{Zones: []string{"example.org"}, ReadDNS: true})
	fake.AddToken(BroadToken, cffake.Token{ReadDNS: true, EditDNS: true, ReadSettings: true})
	fake.AddToken(SSLToken, cffake.Token{Zones: []string{"example.org"}, NoZoneRead: true, EditSSL: true})

	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "cosmos-cloudflare test authority"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		IsCA: true, KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "api.cloudflare.com"},
		DNSNames:  []string{"api.cloudflare.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, _ := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)

	if err := os.WriteFile("/shared/ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0644); err != nil {
		log.Fatal(err)
	}

	go func() {
		server := &http.Server{Addr: ":443", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Printf("%s %s", r.Method, r.URL.Path)
			fake.ServeHTTP(w, r)
		}), TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		}}
		log.Fatal(server.ListenAndServeTLS("", ""))
	}()

	control := http.NewServeMux()
	control.HandleFunc("/records", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(fake.Records("example.org"))
	})
	control.HandleFunc("/calls", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(fake.Calls(r.URL.Query().Get("clear") != ""))
	})
	control.HandleFunc("/proxied", func(w http.ResponseWriter, r *http.Request) {
		fake.SetProxied("example.org", r.URL.Query().Get("name"), "A", r.URL.Query().Get("on") == "1")
		io.WriteString(w, "ok\n")
	})
	control.HandleFunc("/aop", func(w http.ResponseWriter, r *http.Request) {
		certs, enabled := fake.OriginPull("example.org")
		json.NewEncoder(w).Encode(map[string]interface{}{"enabled": enabled, "certificates": certs})
	})
	log.Println("fake Cloudflare ready")
	log.Fatal(http.ListenAndServe(":8080", control))
}

// edge stands in for Cloudflare's proxy: it makes requests to Cosmos from an
// address inside Cloudflare's ranges, presenting what it is told to.
func edge(target string, from string, api string) {
	source := net.ParseIP(from)
	if source == nil {
		log.Fatal("not an address: " + from)
	}
	wrongAuthority := cffake.NewAuthority("an authority Cosmos does not know")
	wrong := wrongAuthority.Issue(cffake.Leaf{})

	uploaded := func() (*tls.Certificate, error) {
		resp, err := http.Get(api + "/aop")
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var held struct {
			Enabled      bool                    `json:"enabled"`
			Certificates []cffake.OriginPullCert `json:"certificates"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&held); err != nil {
			return nil, err
		}
		if !held.Enabled || len(held.Certificates) == 0 {
			// nothing uploaded, or not switched on: Cloudflare presents nothing
			return nil, nil
		}
		// the most recently uploaded one is the one in use
		last := held.Certificates[len(held.Certificates)-1]
		pair, err := tls.X509KeyPair([]byte(last.Certificate), []byte(last.PrivateKey))
		if err != nil {
			return nil, err
		}
		return &pair, nil
	}

	control := http.NewServeMux()
	control.HandleFunc("/visit", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		host, visitor, path := query.Get("host"), query.Get("visitor"), query.Get("path")
		if path == "" {
			path = "/"
		}
		if visitor == "" {
			visitor = "198.51.100.77"
		}
		count, _ := strconv.Atoi(query.Get("n"))
		if count < 1 || count > 50 {
			count = 1
		}
		var present *tls.Certificate
		switch query.Get("cert") {
		case "uploaded":
			var err error
			if present, err = uploaded(); err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
		case "wrong":
			present = &wrong
		}

		var mu sync.Mutex
		asked := false
		statuses := []int{}
		problem := ""
		for i := 0; i < count; i++ {
			transport := &http.Transport{
				DisableKeepAlives: true,
				DialContext:       (&net.Dialer{Timeout: 5 * time.Second, LocalAddr: &net.TCPAddr{IP: source}}).DialContext,
				TLSClientConfig: &tls.Config{
					ServerName: host,
					// the test Cosmos serves a certificate of its own making
					InsecureSkipVerify: true,
					GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
						mu.Lock()
						asked = true
						mu.Unlock()
						if present == nil {
							return &tls.Certificate{}, nil
						}
						return present, nil
					},
				},
			}
			req, _ := http.NewRequest("GET", "https://"+target+path, nil)
			req.Host = host
			req.Header.Set("CF-Connecting-IP", visitor)
			req.Header.Set("X-Forwarded-For", visitor)
			client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			resp, err := client.Do(req)
			if err != nil {
				statuses = append(statuses, 0)
				problem = err.Error()
				continue
			}
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			statuses = append(statuses, resp.StatusCode)
		}
		mu.Lock()
		defer mu.Unlock()
		json.NewEncoder(w).Encode(map[string]interface{}{"statuses": statuses, "asked": asked, "presented": present != nil, "problem": problem})
	})
	log.Println("fake Cloudflare edge ready, coming from " + from)
	log.Fatal(http.ListenAndServe(":8081", control))
}
