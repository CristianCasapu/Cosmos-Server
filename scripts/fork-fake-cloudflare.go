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
	"net/http"
	"os"
	"time"

	"github.com/azukaar/cosmos-server/src/cloudflare/cffake"
)

const (
	GoodToken  = "cfut_dGVzdC10b2tlbi1mb3ItdGhlLWJyb3dzZXItY2hlY2s0123456789"
	ReadToken  = "readonly0123456789abcdefghijklmnopqrstuvwxYZ"
	BroadToken = "allzones0123456789abcdefghijklmnopqrstuvwxYZ"
)

func main() {
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
	log.Println("fake Cloudflare ready")
	log.Fatal(http.ListenAndServe(":8080", control))
}
