package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/gorilla/mux"
)

// Fork addition: tests of fork_pwa.go.

const pwaTestWorker = "// the panel's worker\nself.addEventListener('fetch', () => {});\n"

// pwaStatic builds a static folder like the UI build's, with or without the
// worker and the manifest, and returns the handler as the server mounts it.
func pwaStatic(t *testing.T, withPWA bool, cfg utils.Config) http.Handler {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"index.html":               "<!DOCTYPE html><title>Cosmos</title>",
		"offline.html":             "<!DOCTYPE html><title>Offline</title>",
		"assets/index-1a2b3c4d.js": "console.log(1)",
		"skin/theme.css":           "body{}",
	}
	if withPWA {
		files["sw.js"] = pwaTestWorker
		files["manifest.webmanifest"] = `{"name":"Cosmos"}`
	}
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	prev := utils.GetBaseMainConfig()
	t.Cleanup(func() { utils.LoadBaseMainConfig(prev) })
	utils.LoadBaseMainConfig(cfg)

	return http.StripPrefix("/cosmos-ui", forkPWAStatic(utils.SPAHandler(dir), dir))
}

func pwaConfig(hostname string, off bool) utils.Config {
	return utils.Config{
		DisableWebApp: off,
		HTTPConfig:    utils.HTTPConfig{Hostname: hostname},
	}
}

func pwaAsk(h http.Handler, method, url string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, url, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestPWAWorkerHeaders(t *testing.T) {
	h := pwaStatic(t, true, pwaConfig("cosmos.example.com", false))
	// the port is not part of the comparison: the panel runs on 2053 here
	w := pwaAsk(h, "GET", "https://cosmos.example.com:2053/cosmos-ui/sw.js")
	if w.Code != 200 || w.Body.String() != pwaTestWorker {
		t.Fatalf("worker: %d %q", w.Code, w.Body.String())
	}
	for header, want := range map[string]string{
		"Cache-Control":          "no-cache",
		"Service-Worker-Allowed": "/cosmos-ui/",
		"Content-Type":           "text/javascript; charset=utf-8",
	} {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s: %q, want %q", header, got, want)
		}
	}
}

func TestPWAManifestHeaders(t *testing.T) {
	h := pwaStatic(t, true, pwaConfig("cosmos.example.com", false))
	w := pwaAsk(h, "GET", "https://cosmos.example.com/cosmos-ui/manifest.webmanifest")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Cosmos") {
		t.Fatalf("manifest: %d %q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/manifest+json; charset=utf-8" {
		t.Errorf("Content-Type: %q", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control: %q", got)
	}
}

// On the hostname of an application (which also serves /cosmos-ui/ for the
// sign-in), the panel cannot be installed and no worker of the panel runs.
func TestPWANotOnAnotherHostname(t *testing.T) {
	h := pwaStatic(t, true, pwaConfig("cosmos.example.com", false))

	w := pwaAsk(h, "GET", "https://jellyfin.example.com/cosmos-ui/manifest.webmanifest")
	if w.Code != 404 {
		t.Fatalf("manifest on an app's hostname: %d, want 404", w.Code)
	}
	if strings.Contains(w.Body.String(), "Cosmos") {
		t.Errorf("the manifest's content was served: %q", w.Body.String())
	}

	w = pwaAsk(h, "GET", "https://jellyfin.example.com/cosmos-ui/sw.js")
	if w.Code != 200 || w.Body.String() != forkPWAOffWorker {
		t.Fatalf("worker on an app's hostname: %d %q, want the removing worker", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Service-Worker-Allowed"); got != "/cosmos-ui/" {
		t.Errorf("Service-Worker-Allowed: %q", got)
	}
}

// The kill switch: the manifest disappears, the worker's address removes
// whatever worker a browser has.
func TestPWASwitchedOff(t *testing.T) {
	h := pwaStatic(t, true, pwaConfig("cosmos.example.com", true))

	if w := pwaAsk(h, "GET", "https://cosmos.example.com/cosmos-ui/manifest.webmanifest"); w.Code != 404 {
		t.Fatalf("manifest when off: %d", w.Code)
	}
	w := pwaAsk(h, "GET", "https://cosmos.example.com/cosmos-ui/sw.js")
	if w.Code != 200 || w.Body.String() != forkPWAOffWorker {
		t.Fatalf("worker when off: %d %q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control: %q", got)
	}
	// HEAD: the headers, no body
	if w := pwaAsk(h, "HEAD", "https://cosmos.example.com/cosmos-ui/sw.js"); w.Code != 200 || w.Body.Len() != 0 {
		t.Errorf("HEAD: %d with %d bytes", w.Code, w.Body.Len())
	}
}

// The removing worker has no fetch handler and only deletes the panel's caches.
func TestPWAOffWorkerOnlyRemoves(t *testing.T) {
	for _, forbidden := range []string{"'fetch'", "respondWith", "importScripts", "http"} {
		if strings.Contains(forkPWAOffWorker, forbidden) {
			t.Errorf("the removing worker contains %q", forbidden)
		}
	}
	for _, wanted := range []string{"unregister()", "'cosmos-ui-'", "caches.delete"} {
		if !strings.Contains(forkPWAOffWorker, wanted) {
			t.Errorf("the removing worker lacks %q", wanted)
		}
	}
}

// A build without the worker never answers the worker's address with the
// panel's page (which a browser would refuse, keeping an old worker).
func TestPWAMissingFromBuild(t *testing.T) {
	h := pwaStatic(t, false, pwaConfig("cosmos.example.com", false))
	w := pwaAsk(h, "GET", "https://cosmos.example.com/cosmos-ui/sw.js")
	if w.Code != 200 || w.Body.String() != forkPWAOffWorker {
		t.Fatalf("worker missing from the build: %d %q", w.Code, w.Body.String())
	}
	w = pwaAsk(h, "GET", "https://cosmos.example.com/cosmos-ui/manifest.webmanifest")
	if w.Code != 404 || strings.Contains(w.Body.String(), "<title>") {
		t.Fatalf("manifest missing from the build: %d %q", w.Code, w.Body.String())
	}
}

func TestPWAMethods(t *testing.T) {
	h := pwaStatic(t, true, pwaConfig("cosmos.example.com", false))
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		for _, name := range []string{"sw.js", "manifest.webmanifest"} {
			w := pwaAsk(h, method, "https://cosmos.example.com/cosmos-ui/"+name)
			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: %d", method, name, w.Code)
			}
		}
	}
}

func TestPWAOtherFilesCacheHeaders(t *testing.T) {
	h := pwaStatic(t, true, pwaConfig("cosmos.example.com", false))
	cases := map[string]string{
		"/cosmos-ui/assets/index-1a2b3c4d.js":   "public, max-age=31536000, immutable",
		"/cosmos-ui/":                           "no-store",
		"/cosmos-ui/servapps":                   "no-store", // a page of the panel: index.html
		"/cosmos-ui/offline.html":               "no-cache",
		"/cosmos-ui/assets/missing-00000000.js": "no-store", // answered with index.html, never immutable
	}
	for url, want := range cases {
		w := pwaAsk(h, "GET", "https://cosmos.example.com"+url)
		if got := w.Header().Get("Cache-Control"); got != want {
			t.Errorf("%s: Cache-Control %q, want %q (status %d)", url, got, want, w.Code)
		}
	}
}

func TestPWAOwnHost(t *testing.T) {
	prev := utils.GetBaseMainConfig()
	t.Cleanup(func() { utils.LoadBaseMainConfig(prev) })

	cases := []struct {
		hostname string
		local    bool
		host     string
		want     bool
	}{
		{"cosmos.example.com", false, "cosmos.example.com", true},
		{"cosmos.example.com", false, "cosmos.example.com:2053", true},
		{"cosmos.example.com", false, "app.example.com", false},
		{"cosmos.example.com", false, "cosmos.example.com.evil.test", false},
		{"cosmos.example.com", false, "192.168.1.10", false},
		{"cosmos.example.com", true, "192.168.1.10:2053", true},
		{"cosmos.example.com", true, "[fd00::10]:2053", true},
		{"cosmos.example.com", true, "8.8.8.8", false},
		{"0.0.0.0", false, "anything.test", true},
	}
	for _, c := range cases {
		utils.LoadBaseMainConfig(utils.Config{HTTPConfig: utils.HTTPConfig{Hostname: c.hostname, AllowHTTPLocalIPAccess: c.local}})
		req := httptest.NewRequest("GET", "/cosmos-ui/sw.js", nil)
		req.Host = c.host
		if got := forkPWAOwnHost(req); got != c.want {
			t.Errorf("hostname %s, local %v, Host %s: %v, want %v", c.hostname, c.local, c.host, got, c.want)
		}
	}
}

// Every answer of the API says no-store, unless the handler says otherwise.
func TestAPINoStore(t *testing.T) {
	router := mux.NewRouter()
	api := router.PathPrefix("/cosmos").Subrouter()
	forkAPINoStore(api)
	api.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) })
	api.HandleFunc("/api/icon", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "max-age=5")
		w.Write([]byte("png"))
	})

	if got := pwaAsk(router, "GET", "/cosmos/api/status").Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("status: Cache-Control %q", got)
	}
	if got := pwaAsk(router, "GET", "/cosmos/api/icon").Header().Get("Cache-Control"); got != "max-age=5" {
		t.Errorf("a handler's own: Cache-Control %q", got)
	}
}
