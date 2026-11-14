//go:build ignore

// Fork addition: a stand-in for Telegram's Bot API, for trying the Telegram
// notifications (src/telegram) without sending anything to Telegram. Used
// with scripts/fork-ui-check-telegram.mjs.
//
// It answers HTTPS on :443 as api.telegram.org, with a certificate from an
// authority it makes at start and writes to /shared/ca.pem, and has a plain
// control port (:8080): GET /mode?set=<ok|wrong_token|chat_not_found|
// not_started|blocked|not_member|topic|flood|down> chooses how it answers,
// GET /received lists what it got (?clear=1 empties the list).
//
// How it is used: on a Docker network made with --internal (no way out),
// run it in a container with the network alias api.telegram.org and a
// folder mounted at /shared; start the throwaway Cosmos on the same network
// with that folder mounted and SSL_CERT_FILE=/shared/ca.pem. The server's
// own code then talks to this program, unchanged: fixed host, certificate
// verified. It is not part of any build (go run scripts/fork-fake-telegram.go).
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
	"strings"
	"sync"
	"time"
)

type received struct {
	At     time.Time              `json:"at"`
	Method string                 `json:"method"`
	Path   string                 `json:"path"`
	Body   map[string]interface{} `json:"body"`
}

var (
	mu   sync.Mutex
	mode = "ok"
	got  []received
)

var answers = map[string]struct {
	status int
	body   string
}{
	"ok":             {200, `{"ok":true,"result":{"message_id":1}}`},
	"wrong_token":    {401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`},
	"chat_not_found": {400, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`},
	"not_started":    {403, `{"ok":false,"error_code":403,"description":"Forbidden: bot can't initiate conversation with a user"}`},
	"blocked":        {403, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`},
	"not_member":     {403, `{"ok":false,"error_code":403,"description":"Forbidden: bot is not a member of the supergroup chat"}`},
	"topic":          {400, `{"ok":false,"error_code":400,"description":"Bad Request: message thread not found"}`},
	"flood":          {429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 3","parameters":{"retry_after":3}}`},
	"down":           {502, `<html>Bad Gateway</html>`},
}

func main() {
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "cosmos-telegram test authority"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		IsCA: true, KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "api.telegram.org"},
		DNSNames:  []string{"api.telegram.org"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, _ := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)

	if err := os.WriteFile("/shared/ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0644); err != nil {
		log.Fatal(err)
	}

	api := http.NewServeMux()
	api.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		entry := received{At: time.Now(), Method: r.Method, Path: r.URL.Path}
		json.Unmarshal(raw, &entry.Body)
		mu.Lock()
		got = append(got, entry)
		answer := answers[mode]
		mu.Unlock()
		log.Printf("%s %s -> %d", r.Method, strings.Split(r.URL.Path, ":")[0]+":…", answer.status)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(answer.status)
		io.WriteString(w, answer.body)
	})
	go func() {
		server := &http.Server{Addr: ":443", Handler: api, TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		}}
		log.Fatal(server.ListenAndServeTLS("", ""))
	}()

	control := http.NewServeMux()
	control.HandleFunc("/mode", func(w http.ResponseWriter, r *http.Request) {
		wanted := r.URL.Query().Get("set")
		mu.Lock()
		defer mu.Unlock()
		if _, known := answers[wanted]; known {
			mode = wanted
		}
		io.WriteString(w, mode+"\n")
	})
	control.HandleFunc("/received", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Query().Get("clear") != "" {
			got = nil
		}
		json.NewEncoder(w).Encode(got)
	})
	log.Println("fake Telegram ready")
	log.Fatal(http.ListenAndServe(":8080", control))
}
