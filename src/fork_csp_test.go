package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/proxy"
	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/gorilla/mux"
)

// Fork addition: tests of fork_csp.go.

// cspRouter is the part of InitServer these headers live in: the panel's
// router, an API router and, with routes, the proxy's routes of applications
// registered after them, each with upstream's SetSecurityHeaders and,
// when fork is set, the fork's hooks exactly as InitServer adds them.
func cspRouter(t *testing.T, fork bool, routes ...utils.ProxyRouteConfig) *mux.Router {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"index.html":           "<!DOCTYPE html><title>Cosmos</title>",
		"offline.html":         "<!DOCTYPE html><title>Offline</title>",
		"assets/main-1a2b.js":  "console.log(1)",
		"sw.js":                "self.addEventListener('fetch', () => {});",
		"manifest.webmanifest": `{"name":"Cosmos"}`,
	} {
		full := dir + "/" + name
		if err := os.MkdirAll(full[:strings.LastIndex(full, "/")], 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	router := mux.NewRouter().StrictSlash(true).SkipClean(true)
	router.Use(utils.ClientRealIP)

	srapi := router.PathPrefix("/cosmos").Subrouter()
	srapi.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"status":"OK"}`)) })
	srapi.Use(utils.SetSecurityHeaders)
	if fork {
		forkAPIHeaders(srapi)
	}

	uirouter := router.PathPrefix("/cosmos-ui").Subrouter()
	uirouter.Use(utils.SetSecurityHeaders)
	if fork {
		uirouter.Use(forkPanelHeaders)
	}
	uirouter.PathPrefix("/").Handler(http.StripPrefix("/cosmos-ui", forkPWAStatic(utils.SPAHandler(dir), dir)))

	for _, route := range routes {
		if proxy.RouterGen(route, router, proxy.RouteTo(route)) == nil {
			t.Fatalf("RouterGen refused %s", route.Name)
		}
	}
	return router
}

func cspConfig(t *testing.T, cfg utils.Config) {
	t.Helper()
	prev := utils.GetBaseMainConfig()
	prevListening := utils.HTTPSListening
	t.Cleanup(func() {
		utils.LoadBaseMainConfig(prev)
		utils.HTTPSListening = prevListening
	})
	if cfg.HTTPConfig.Hostname == "" {
		cfg.HTTPConfig.Hostname = "cosmos.example.com"
	}
	cfg.MonitoringDisabled = true
	cfg.HTTPConfig.AcceptAllInsecureHostname = true
	cfg.HTTPConfig.AuthPrivateKey = strings.Repeat("k", 128)
	utils.LoadBaseMainConfig(cfg)
}

func cspAsk(router http.Handler, url string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestPanelPolicyText(t *testing.T) {
	got := forkPanelPolicy([]string{"https://app.example.com", "com.example.app:"})
	want := "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; font-src 'self'; " +
		"connect-src 'self' https: http: wss: ws:; media-src 'self'; worker-src 'self'; manifest-src 'self'; frame-src 'none'; " +
		"object-src 'none'; base-uri 'none'; form-action 'self' https://app.example.com com.example.app:; frame-ancestors 'none'"
	if got != want {
		t.Fatalf("policy:\n%s\nwant:\n%s", got, want)
	}
	for _, forbidden := range []string{"'unsafe-eval'", "script-src 'self' 'unsafe-inline'", "*", "report-uri", "report-to"} {
		if strings.Contains(forkPanelPolicy(nil), forbidden) {
			t.Errorf("the policy must not hold %q", forbidden)
		}
	}
}

// every file of the panel: the page (and what a missing file is answered
// with), the offline page, the build's files, the worker and the manifest
func TestPanelHeadersOnEveryPanelPath(t *testing.T) {
	cspConfig(t, utils.Config{})
	prevOrigins := forkReturnOrigins
	forkReturnOrigins = func() []string { return []string{"https://app.example.com"} }
	t.Cleanup(func() { forkReturnOrigins = prevOrigins })

	router := cspRouter(t, true)
	policy := forkPanelPolicy([]string{"https://app.example.com"})
	for _, path := range []string{"/cosmos-ui/", "/cosmos-ui/login", "/cosmos-ui/servapps/containers/x", "/cosmos-ui/offline.html",
		"/cosmos-ui/assets/main-1a2b.js", "/cosmos-ui/sw.js", "/cosmos-ui/manifest.webmanifest"} {
		req := httptest.NewRequest("GET", "https://cosmos.example.com"+path, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if plain := cspAsk(router, "http://cosmos.example.com"+path); plain.Header().Get("Cross-Origin-Opener-Policy") != "" {
			t.Errorf("%s: Cross-Origin-Opener-Policy over plain HTTP, where browsers refuse it", path)
		}
		if w.Code != http.StatusOK {
			t.Errorf("%s: %d", path, w.Code)
		}
		h := w.Header()
		for name, want := range map[string]string{
			"Content-Security-Policy":    policy,
			"X-Content-Type-Options":     "nosniff",
			"Referrer-Policy":            "same-origin",
			"Permissions-Policy":         forkPermissionsPolicy,
			"Cross-Origin-Opener-Policy": "same-origin",
		} {
			if got := h.Values(name); len(got) != 1 || got[0] != want {
				t.Errorf("%s %s: %q, want %q", path, name, got, want)
			}
		}
		if h.Get("Content-Security-Policy-Report-Only") != "" {
			t.Errorf("%s: a report-only policy next to the enforced one", path)
		}
	}
}

func TestPanelPolicyReportOnly(t *testing.T) {
	cspConfig(t, utils.Config{})
	prev := forkCSPEnforced
	forkCSPEnforced = false
	t.Cleanup(func() { forkCSPEnforced = prev })

	w := cspAsk(cspRouter(t, true), "http://cosmos.example.com/cosmos-ui/")
	if got := w.Header().Get("Content-Security-Policy-Report-Only"); got != forkPanelPolicy(forkReturnOrigins()) {
		t.Errorf("report-only: %q", got)
	}
	// upstream's own policy stays enforced meanwhile
	if got := w.Header().Get("Content-Security-Policy"); got != "frame-ancestors 'self'" {
		t.Errorf("enforced policy while reporting: %q", got)
	}
}

func TestAPIHeaders(t *testing.T) {
	cspConfig(t, utils.Config{})
	w := cspAsk(cspRouter(t, true), "http://cosmos.example.com/cosmos/api/status")
	h := w.Header()
	if got := h.Values("Content-Security-Policy"); len(got) != 1 || got[0] != forkAPIPolicy {
		t.Errorf("API policy: %q", got)
	}
	if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "same-origin" {
		t.Errorf("API headers: %v", h)
	}
}

// the answers of an application, on its own name and under a path of the
// panel's name, are exactly what they are without the fork's hooks: its own
// headers, and upstream's header hardening when the route has it
func TestProxiedApplicationsAreUntouched(t *testing.T) {
	cspConfig(t, utils.Config{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "script-src 'unsafe-inline' 'unsafe-eval' *")
		w.Header().Set("Referrer-Policy", "unsafe-url")
		w.Header().Set("X-App", "1")
		w.Write([]byte("<script>alert(1)</script>"))
	}))
	defer backend.Close()

	routes := []utils.ProxyRouteConfig{
		{Name: "app", Mode: "PROXY", UseHost: true, Host: "app.example.com", Target: backend.URL},
		{Name: "plain", Mode: "PROXY", UseHost: true, Host: "plain.example.com", Target: backend.URL, DisableHeaderHardening: true},
		{Name: "under", Mode: "PROXY", UseHost: true, Host: "cosmos.example.com", UsePathPrefix: true, PathPrefix: "/app", Target: backend.URL},
	}
	with := cspRouter(t, true, routes...)
	without := cspRouter(t, false, routes...)

	for _, url := range []string{"http://app.example.com/", "http://plain.example.com/page", "http://cosmos.example.com/app/x"} {
		a := cspAsk(with, url)
		b := cspAsk(without, url)
		if a.Code != http.StatusOK || a.Header().Get("X-App") != "1" {
			t.Fatalf("%s did not reach the application: %d %v %q", url, a.Code, a.Header(), a.Body.String())
		}
		strip := func(h http.Header) http.Header {
			c := h.Clone()
			c.Del("Date")
			return c
		}
		if !reflect.DeepEqual(strip(a.Header()), strip(b.Header())) {
			t.Errorf("%s: headers changed by the fork's hooks:\n with  %v\n without %v", url, a.Header(), b.Header())
		}
		for _, name := range []string{"Permissions-Policy", "Cross-Origin-Opener-Policy"} {
			if a.Header().Get(name) != "" {
				t.Errorf("%s: %s set on an application", url, name)
			}
		}
		for _, value := range a.Header().Values("Content-Security-Policy") {
			if value == forkPanelPolicy(forkReturnOrigins()) || value == forkAPIPolicy {
				t.Errorf("%s: the panel's policy on an application", url)
			}
		}
		if got := a.Header().Values("Referrer-Policy"); len(got) != 1 || got[0] != "unsafe-url" {
			t.Errorf("%s: the application's Referrer-Policy: %q", url, got)
		}
	}
	// the route without header hardening carries its own policy only
	if got := cspAsk(with, "http://plain.example.com/").Header().Values("Content-Security-Policy"); len(got) != 1 || got[0] != "script-src 'unsafe-inline' 'unsafe-eval' *" {
		t.Errorf("the application's own policy: %q", got)
	}
	// and the panel on the same name still has its own
	if got := cspAsk(with, "http://app.example.com/cosmos-ui/login").Header().Get("Content-Security-Policy"); got != forkPanelPolicy(forkReturnOrigins()) {
		t.Errorf("the panel's sign-in on the application's name: %q", got)
	}
}

// InitServer adds the hooks after upstream's headers, so that they win
func TestCSPHooksInInitServer(t *testing.T) {
	source, err := os.ReadFile("httpServer.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	order := func(before, after string) {
		i, j := strings.Index(text, before), strings.Index(text, after)
		if i < 0 || j < 0 || j < i {
			t.Errorf("%q must come after %q in httpServer.go", after, before)
		}
	}
	order("srapiAdmin.Use(utils.SetSecurityHeaders)", "forkAPIHeaders(srapi, srapiStrict, srapiAdmin)")
	order("uirouter.Use(utils.SetSecurityHeaders)", "uirouter.Use(forkPanelHeaders)")
	order("uirouter.Use(forkPanelHeaders)", "router = proxy.BuildFromConfig(router")
}

// --- HSTS

type hstsCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newHSTSCA(t *testing.T) hstsCA {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test public authority"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return hstsCA{cert, key}
}

func (ca hstsCA) leaf(t *testing.T, name string, notAfter time.Time) string {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestHSTS(t *testing.T) {
	ca := newHSTSCA(t)
	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)
	prevRoots := forkHSTSRoots
	forkHSTSRoots = roots
	t.Cleanup(func() {
		forkHSTSRoots = prevRoots
		forkTrustCache = map[[32]byte]forkTrustEntry{}
	})
	other := newHSTSCA(t)

	cases := []struct {
		name      string
		mode      string
		cert      string
		listening bool
		host      string
		want      []string
	}{
		{"a name with a public certificate", "PROVIDED", ca.leaf(t, "cosmos.example.com", time.Now().Add(20*time.Hour)), true, "cosmos.example.com", []string{"max-age=31536000"}},
		{"the same with the port", "PROVIDED", ca.leaf(t, "cosmos.example.com", time.Now().Add(20*time.Hour)), true, "cosmos.example.com:2053", []string{"max-age=31536000"}},
		{"a certificate of another authority", "PROVIDED", other.leaf(t, "cosmos.example.com", time.Now().Add(20*time.Hour)), true, "cosmos.example.com", []string{"max-age=0"}},
		{"a certificate for another name", "PROVIDED", ca.leaf(t, "other.example.com", time.Now().Add(20*time.Hour)), true, "cosmos.example.com", []string{"max-age=0"}},
		{"an expired certificate", "PROVIDED", ca.leaf(t, "cosmos.example.com", time.Now().Add(-time.Minute)), true, "cosmos.example.com", []string{"max-age=0"}},
		{"Cosmos' own certificate", "SELFSIGNED", "", true, "cosmos.example.com", []string{"max-age=0"}},
		{"a .local name", "SELFSIGNED", "", true, "dpe.local", []string{"max-age=0"}},
		{"an IP address", "SELFSIGNED", "", true, "192.168.1.10:2053", nil},
		{"an IPv6 address", "PROVIDED", ca.leaf(t, "cosmos.example.com", time.Now().Add(20*time.Hour)), true, "[fd00::10]:2053", nil},
		{"no HTTPS on this server", "DISABLED", "", false, "cosmos.example.com", nil},
		{"HTTPS not listening", "PROVIDED", ca.leaf(t, "cosmos.example.com", time.Now().Add(20*time.Hour)), false, "cosmos.example.com", nil},
	}
	for _, c := range cases {
		forkTrustCache = map[[32]byte]forkTrustEntry{}
		cfg := utils.Config{}
		cfg.HTTPConfig.HTTPSCertificateMode = c.mode
		cfg.HTTPConfig.TLSCert = c.cert
		cfg.HTTPConfig.SelfTLSCert = "self-signed"
		cspConfig(t, cfg)
		utils.HTTPSListening = c.listening
		router := cspRouter(t, true)
		for _, path := range []string{"/cosmos-ui/", "/cosmos/api/status"} {
			req := httptest.NewRequest("GET", "https://"+c.host+path, nil)
			req.Host = c.host
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if got := w.Header().Values("Strict-Transport-Security"); !reflect.DeepEqual(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
				t.Errorf("%s, %s: %q, want %q", c.name, path, got, c.want)
			}
			for _, v := range w.Header().Values("Strict-Transport-Security") {
				if strings.Contains(v, "includeSubDomains") || strings.Contains(v, "preload") {
					t.Errorf("%s, %s: %q", c.name, path, v)
				}
			}
		}
	}
}

// names come from requests: the cache is per certificate and bounded
func TestHSTSTrustCacheIsBounded(t *testing.T) {
	cfg := utils.Config{}
	cfg.HTTPConfig.HTTPSCertificateMode = "PROVIDED"
	ca := newHSTSCA(t)
	cfg.HTTPConfig.TLSCert = ca.leaf(t, "cosmos.example.com", time.Now().Add(time.Hour))
	cspConfig(t, cfg)
	forkTrustCache = map[[32]byte]forkTrustEntry{}
	for i := 0; i < 500; i++ {
		forkPubliclyTrusted("n" + strings.Repeat("x", i%40) + ".example.com")
	}
	if len(forkTrustCache) != 1 {
		t.Errorf("%d entries for one certificate", len(forkTrustCache))
	}
}
