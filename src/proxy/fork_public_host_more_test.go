package proxy

// Fork addition (plan 44): what an app is told through Cloudflare, over
// HTTP/2 and to an h2c backend.

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func TestThroughCloudflareTheAppIsToldTheVisitorAndTheAddress(t *testing.T) {
	onServer(t, func(c *utils.Config) {
		c.HTTPConfig.Cloudflare.Shielded = []string{"qbittorrent.example.org"}
		c.HTTPConfig.Cloudflare.TrustRanges = true
	})
	seen := make(chan *http.Request, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen <- r.Clone(r.Context()) }))
	defer backend.Close()
	for _, own := range []bool{false, true} {
		route := utils.ProxyRouteConfig{Name: "qbt", Mode: "PROXY", UseHost: true, Host: "qbittorrent.example.org", Target: backend.URL, ForkAppOwnHost: own}
		p, _ := NewProxy(backend.URL, false, false, route)
		r := httptest.NewRequest("GET", "http://placeholder/", nil)
		r.Host = "qbittorrent.example.org:2053"
		r.RemoteAddr = "173.245.48.9:40000" // one of Cloudflare's
		r.Header.Set("CF-Connecting-IP", "203.0.113.9")
		r.Header.Set("X-Forwarded-For", "203.0.113.9")
		r.Header.Set("X-Forwarded-Host", "evil.example.net")
		p.ServeHTTP(httptest.NewRecorder(), r)
		got := <-seen
		wantHost := "qbittorrent.example.org:2053"
		if own {
			wantHost = strings.TrimPrefix(backend.URL, "http://")
		}
		if got.Host != wantHost || got.Header.Get("X-Forwarded-Host") != "qbittorrent.example.org:2053" || got.Header.Get("X-Forwarded-Port") != "2053" {
			t.Errorf("own=%v: Host %q, X-Forwarded-Host %q, -Port %q", own, got.Host, got.Header.Get("X-Forwarded-Host"), got.Header.Get("X-Forwarded-Port"))
		}
		if got.Header.Get("CF-Connecting-IP") != "203.0.113.9" || !strings.HasPrefix(got.Header.Get("X-Forwarded-For"), "203.0.113.9") || got.Header.Get("X-Real-Ip") != "203.0.113.9" {
			t.Errorf("own=%v: the visitor Cloudflare named is not what the app got: %v", own, got.Header)
		}
	}
}

func TestOverHTTP2AndToAnH2CBackend(t *testing.T) {
	onServer(t, nil)
	seen := make(chan string, 2)
	backend := httptest.NewServer(h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Proto + " " + r.Host + " " + r.Header.Get("X-Forwarded-Host") + " " + r.Header.Get("X-Forwarded-Port")
	}), &http2.Server{}))
	defer backend.Close()
	for _, own := range []bool{false, true} {
		route := utils.ProxyRouteConfig{Name: "h2", Mode: "PROXY", UseHost: true, Host: "app.example.org", Target: backend.URL, UseH2C: true, ForkAppOwnHost: own}
		p, _ := NewProxy(backend.URL, false, false, route)
		front := httptest.NewUnstartedServer(p)
		front.EnableHTTP2 = true
		front.StartTLS()
		client := front.Client()
		client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}}
		req, _ := http.NewRequest("GET", front.URL+"/", nil)
		req.Host = "app.example.org:2053"
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.ProtoMajor != 2 {
			t.Errorf("the visitor's side was %s, not HTTP/2", resp.Proto)
		}
		host := "app.example.org:2053"
		if own {
			host = strings.TrimPrefix(backend.URL, "http://")
		}
		if got, want := <-seen, "HTTP/2.0 "+host+" app.example.org:2053 2053"; got != want {
			t.Errorf("own=%v: the h2c backend got %q, want %q", own, got, want)
		}
		front.Close()
	}
}
