//go:build ignore

// Fork addition: a stand-in for GitHub's release downloads and release
// list, for the install scripts' test bed (scripts/fork-install-e2e.sh) and
// for an update of an installed test server through its own updater.
// Nothing is ever fetched from the real GitHub in those tests.
//
// It answers HTTPS on :443 as github.com, api.github.com and
// release-assets.githubusercontent.com, with a certificate from an
// authority it makes at start and writes to <shared>/ca.pem (the test
// machines add it to their system's trusted authorities). The releases are
// folders <releases>/<tag>/ holding the assets (zips, .md5, .sig, scripts):
//
//	GET https://github.com/<owner>/<repo>/releases/latest/download/<asset>
//	    302 to .../releases/download/<latest tag>/<asset>, as GitHub does
//	GET https://github.com/<owner>/<repo>/releases/download/<tag>/<asset>
//	    302 to https://release-assets.githubusercontent.com/<owner>/<repo>/<tag>/<asset>
//	GET https://release-assets.githubusercontent.com/<owner>/<repo>/<tag>/<asset>
//	    the file
//	GET https://api.github.com/repos/<owner>/<repo>/releases
//	    the list of releases (tag_name, prerelease, assets with
//	    browser_download_url), the newest first
//
// The latest release is the tag with the highest fork build number, unless
// set on the plain control port (:8080):
//
//	GET /latest?tag=<tag>        which tag "latest" is ("" = the highest)
//	GET /cut?asset=<part>&after=<bytes>
//	                             a download of an asset whose name contains
//	                             <part> ends after that many bytes: the
//	                             connection is closed, as a network that
//	                             goes down does (asset= empty: no cut)
//	GET /redirect?to=<url>       a download is sent to <url>/<asset> instead of
//	                             release-assets.githubusercontent.com (an
//	                             address outside GitHub's, to see the script
//	                             refuse it; to= empty: as GitHub)
//	GET /down?set=1              every HTTPS request is answered by closing
//	                             the connection (0: answer again)
//	GET /calls                   the requests received, in order (?clear=1)
//	GET /reload                  read the release folders again
//
// Usage: go run scripts/fork-fake-github.go -releases <dir> -shared <dir>
// in a container with the network aliases github.com, api.github.com and
// release-assets.githubusercontent.com. It is not part of any build.
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
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	releasesDir = flag.String("releases", "/releases", "folder of <tag>/ folders")
	sharedDir   = flag.String("shared", "/shared", "where ca.pem is written")

	mu        sync.Mutex
	latestTag string
	cutAsset  string
	cutAfter  int64
	down      bool
	redirect  string
	calls     []string

	tagPattern  = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+-fork\.([0-9]+)$`)
	namePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,200}$`)
)

func note(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if len(calls) < 10000 {
		calls = append(calls, fmt.Sprintf(format, args...))
	}
}

// tags returns the release folders, the newest (highest build) first.
func tags() []string {
	entries, _ := os.ReadDir(*releasesDir)
	var out []string
	for _, e := range entries {
		if e.IsDir() && tagPattern.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	build := func(tag string) int {
		n, _ := strconv.Atoi(tagPattern.FindStringSubmatch(tag)[1])
		return n
	}
	sort.Slice(out, func(i, j int) bool { return build(out[i]) > build(out[j]) })
	return out
}

func latest() string {
	mu.Lock()
	tag := latestTag
	mu.Unlock()
	if tag != "" {
		return tag
	}
	if t := tags(); len(t) > 0 {
		return t[0]
	}
	return ""
}

func closeConnection(w http.ResponseWriter) {
	if h, ok := w.(http.Hijacker); ok {
		if conn, _, err := h.Hijack(); err == nil {
			conn.Close()
		}
	}
}

func serveGitHub(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(strings.Split(r.Host, ":")[0])
	note("%s %s%s", r.Method, host, r.URL.Path)
	mu.Lock()
	isDown := down
	mu.Unlock()
	if isDown {
		closeConnection(w)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch host {
	case "github.com":
		// <owner>/<repo>/releases/latest/download/<asset>
		// <owner>/<repo>/releases/download/<tag>/<asset>
		if len(parts) == 6 && parts[2] == "releases" && parts[3] == "latest" && parts[4] == "download" {
			tag := latest()
			if tag == "" {
				http.NotFound(w, r)
				return
			}
			http.Redirect(w, r, "https://github.com/"+parts[0]+"/"+parts[1]+"/releases/download/"+tag+"/"+parts[5], http.StatusFound)
			return
		}
		if len(parts) == 6 && parts[2] == "releases" && parts[3] == "download" && namePattern.MatchString(parts[4]) && namePattern.MatchString(parts[5]) {
			if _, err := os.Stat(filepath.Join(*releasesDir, parts[4], parts[5])); err != nil {
				http.NotFound(w, r)
				return
			}
			mu.Lock()
			to := redirect
			mu.Unlock()
			if to != "" {
				w.Header().Set("Location", to+"/"+parts[5])
				w.WriteHeader(http.StatusFound)
				return
			}
			http.Redirect(w, r, "https://release-assets.githubusercontent.com/"+parts[0]+"/"+parts[1]+"/"+parts[4]+"/"+parts[5], http.StatusFound)
			return
		}
	case "release-assets.githubusercontent.com":
		if len(parts) == 4 && namePattern.MatchString(parts[2]) && namePattern.MatchString(parts[3]) {
			serveFile(w, r, parts[2], parts[3])
			return
		}
	case "api.github.com":
		if len(parts) == 4 && parts[0] == "repos" && parts[3] == "releases" {
			serveList(w, parts[1], parts[2])
			return
		}
	}
	http.NotFound(w, r)
}

func serveFile(w http.ResponseWriter, r *http.Request, tag, name string) {
	f, err := os.Open(filepath.Join(*releasesDir, tag, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, _ := f.Stat()
	mu.Lock()
	cut, after := cutAsset != "" && strings.Contains(name, cutAsset), cutAfter
	mu.Unlock()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	if !cut {
		io.Copy(w, f)
		return
	}
	io.CopyN(w, f, after)
	if fl, ok := w.(http.Flusher); ok {
		fl.Flush()
	}
	time.Sleep(200 * time.Millisecond)
	closeConnection(w)
}

type asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type release struct {
	TagName    string  `json:"tag_name"`
	Name       string  `json:"name"`
	PreRelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
	Assets     []asset `json:"assets"`
}

func serveList(w http.ResponseWriter, owner, repo string) {
	list := []release{}
	for _, tag := range tags() {
		entries, _ := os.ReadDir(filepath.Join(*releasesDir, tag))
		rel := release{TagName: tag, Name: strings.TrimPrefix(tag, "v"), Assets: []asset{}}
		for _, e := range entries {
			if e.Type().IsRegular() && namePattern.MatchString(e.Name()) {
				info, _ := e.Info()
				rel.Assets = append(rel.Assets, asset{Name: e.Name(), Size: info.Size(),
					URL: "https://github.com/" + owner + "/" + repo + "/releases/download/" + tag + "/" + e.Name()})
			}
		}
		list = append(list, rel)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func control(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	mu.Lock()
	defer mu.Unlock()
	switch r.URL.Path {
	case "/latest":
		latestTag = q.Get("tag")
	case "/cut":
		cutAsset = q.Get("asset")
		cutAfter, _ = strconv.ParseInt(q.Get("after"), 10, 64)
	case "/redirect":
		redirect = q.Get("to")
	case "/down":
		down = q.Get("set") == "1"
	case "/calls":
		json.NewEncoder(w).Encode(calls)
		if q.Get("clear") == "1" {
			calls = nil
		}
		return
	case "/reload":
	default:
		http.NotFound(w, r)
		return
	}
	fmt.Fprintln(w, "ok")
}

func main() {
	flag.Parse()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Cosmos fork test bed CA (not trusted anywhere else)"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(7 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		log.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "github.com"},
		DNSNames:     []string{"github.com", "api.github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(7 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(*sharedDir, "ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o644); err != nil {
		log.Fatal(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der, caDER}, PrivateKey: key}

	go func() {
		log.Fatal(http.ListenAndServe(":8080", http.HandlerFunc(control)))
	}()
	ln, err := net.Listen("tcp", ":443")
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(serveGitHub), TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}
	log.Printf("GitHub stand-in: releases in %s, CA in %s/ca.pem", *releasesDir, *sharedDir)
	log.Fatal(server.Serve(tls.NewListener(ln, server.TLSConfig)))
}
