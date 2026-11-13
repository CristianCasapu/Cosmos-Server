package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: tests of fork_icons.go.

// a one-pixel PNG
var tinyPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\xff\xff?\x00\x05\xfe\x02\xfe\xa75\x81\x84\x00\x00\x00\x00IEND\xaeB`\x82")

// askFavicon serves one application with the given icon answer behind a
// route named "app" and returns what /cosmos/api/favicon?route=app answers.
func askFavicon(t *testing.T, iconType string, iconBody []byte, page string) *httptest.ResponseRecorder {
	t.Helper()
	if page == "" {
		page = `<html><head><link rel="icon" href="/the-icon.png"></head><body>app</body></html>`
	}
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(page))
		case "/the-icon.png":
			w.Header().Set("Content-Type", iconType)
			w.Write(iconBody)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(app.Close)

	prev := utils.GetBaseMainConfig()
	prevCache := IconCache
	IconCache = map[string]CachedImage{}
	t.Cleanup(func() {
		utils.LoadBaseMainConfig(prev)
		IconCache = prevCache
	})
	utils.LoadBaseMainConfig(utils.Config{
		HTTPConfig: utils.HTTPConfig{
			Hostname: "cosmos.example.com",
			ProxyConfig: utils.ProxyConfig{
				Routes: []utils.ProxyRouteConfig{{Name: "app", Mode: "PROXY", Target: app.URL}},
			},
		},
	})

	req := httptest.NewRequest("GET", "https://cosmos.example.com/cosmos/api/favicon?route=app", nil)
	ctx := context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{
		Nickname: "alice", Role: utils.USER, UserRole: utils.USER, Permissions: utils.GetRolePermissions(utils.USER),
	})
	w := httptest.NewRecorder()
	GetFavicon(w, req.WithContext(ctx))
	return w
}

func TestIconIsPassedOnAsAPictureOnly(t *testing.T) {
	// a real picture: passed on, under the type its bytes have
	w := askFavicon(t, "image/png", tinyPNG, "")
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/png" || w.Body.Len() != len(tinyPNG) {
		t.Fatalf("a PNG icon: %d %q, %d bytes", w.Code, w.Header().Get("Content-Type"), w.Body.Len())
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("an icon is sent without the policy that makes it a picture only: %q", csp)
	}

	// a picture the application calls something else is still a picture
	w = askFavicon(t, "application/octet-stream", tinyPNG, "")
	if w.Header().Get("Content-Type") != "image/png" {
		t.Errorf("a PNG served as octet-stream is passed on as %q", w.Header().Get("Content-Type"))
	}

	// a page that the application calls an image
	script := []byte(`<html><body><script>fetch('/cosmos/api/config')</script></body></html>`)
	for _, claimed := range []string{"text/html; x=image", "text/html;image/png", "image/png", "application/octet-stream", "image/svg+xml"} {
		w = askFavicon(t, claimed, script, "")
		if strings.Contains(w.Body.String(), "<script>") {
			t.Errorf("a page claimed as %q was passed on from the panel's address as %q", claimed, w.Header().Get("Content-Type"))
		}
		if strings.Contains(w.Header().Get("Content-Type"), "html") {
			t.Errorf("claimed %q: the answer has the type %q", claimed, w.Header().Get("Content-Type"))
		}
	}

	// an SVG drawing is passed on as a drawing, and cannot act when opened by itself
	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><script>alert(2)</script><circle r="4"/></svg>`)
	w = askFavicon(t, "image/svg+xml", svg, "")
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("an SVG icon: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("an SVG icon is sent without the policy that stops its scripts: %q", csp)
	}
	// an SVG the application does not call SVG is not passed on at all
	w = askFavicon(t, "image/png", svg, "")
	if strings.Contains(w.Body.String(), "<svg") {
		t.Errorf("an SVG claimed as PNG was passed on as %q", w.Header().Get("Content-Type"))
	}
}

func TestIconLargerThanAnIconIsNotKept(t *testing.T) {
	big := append([]byte{}, tinyPNG...)
	big = append(big, make([]byte, maxIconSize+10)...)
	w := askFavicon(t, "image/png", big, "")
	if w.Body.Len() > maxIconSize {
		t.Errorf("an icon of %d bytes was passed on", w.Body.Len())
	}
	if len(IconCache) != 0 {
		t.Errorf("an oversized icon was kept: %d entries", len(IconCache))
	}
}

func TestIconSearchDoesNotLeaveForTheLocalNetwork(t *testing.T) {
	// what the application must not be able to reach through the server
	var internalHits atomic.Int32
	internal := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		internalHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Write(tinyPNG)
	}))
	// the same machine under another address than the application's own host
	listener, err := net.Listen("tcp", "127.0.0.2:0")
	if err != nil {
		t.Skip("no second loopback address here: " + err.Error())
	}
	internal.Listener.Close()
	internal.Listener = listener
	internal.Start()
	t.Cleanup(internal.Close)

	// an icon link that points there
	w := askFavicon(t, "image/png", tinyPNG, `<html><head><link rel="icon" href="`+internal.URL+`/secret.png"></head></html>`)
	_ = w
	if internalHits.Load() != 0 {
		t.Errorf("the server fetched an icon link to another local address (%d requests)", internalHits.Load())
	}
}

func TestIconURLAllowed(t *testing.T) {
	prev := lookupIconHost
	lookupIconHost = func(host string) ([]net.IP, error) {
		switch host {
		case "cdn.example.com":
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		case "rebind.example.com":
			return []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("127.0.0.1")}, nil
		case "nas.lan":
			return []net.IP{net.ParseIP("192.168.1.10")}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host}
	}
	t.Cleanup(func() { lookupIconHost = prev })

	base, _ := url.Parse("http://172.18.0.5:8096/web/")
	allowed := []string{
		"http://172.18.0.5:8096/favicon.ico", // the application itself
		"http://172.18.0.5:9000/icon.png",    // another port of its own host
		"https://cdn.example.com/icon.png",   // the internet
		"https://93.184.216.34/icon.png",
	}
	for _, raw := range allowed {
		target, _ := url.Parse(raw)
		if !iconURLAllowed(target, base) {
			t.Errorf("%s should be allowed for the application at %s", raw, base)
		}
	}
	refused := []string{
		"http://127.0.0.1:2052/cosmos/api/status",
		"http://localhost/",
		"http://[::1]/",
		"http://172.18.0.6:8080/icon.png", // another container
		"http://192.168.1.1/admin.png",
		"http://169.254.169.254/latest/meta-data/",
		"http://100.100.100.100/",
		"http://nas.lan/icon.png",
		"http://rebind.example.com/icon.png",
		"http://unknown.invalid/icon.png",
		"file:///etc/passwd",
		"ftp://cdn.example.com/icon.png",
		"http:///icon.png",
	}
	for _, raw := range refused {
		target, _ := url.Parse(raw)
		if iconURLAllowed(target, base) {
			t.Errorf("%s must not be fetched for the application at %s", raw, base)
		}
	}
}
