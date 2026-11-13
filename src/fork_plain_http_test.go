package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: tests of fork_plain_http.go, what the plain-HTTP port of a
// server that serves HTTPS answers.

// plainHTTPTestConfig is a server reached by its IP on 2052 (HTTP) and 2053
// (HTTPS), with one hostname of an application and one HTTP-only domain
func plainHTTPTestConfig(t *testing.T, change func(*utils.Config)) {
	t.Helper()

	config := utils.Config{}
	config.HTTPConfig.Hostname = "172.16.0.14"
	config.HTTPConfig.HTTPPort = "2052"
	config.HTTPConfig.HTTPSPort = "2053"
	config.HTTPConfig.HTTPSCertificateMode = "LETSENCRYPT"
	config.HTTPConfig.DNSZones = []utils.DNSZoneConfig{
		{Zone: "plain.net", HTTPSCertificateMode: "DISABLED"},
	}
	config.HTTPConfig.ProxyConfig.Routes = []utils.ProxyRouteConfig{
		{Name: "media", UseHost: true, Host: "media.home.lan", Target: "http://127.0.0.1:8096"},
		{Name: "plain", UseHost: true, Host: "app.plain.net", Target: "http://127.0.0.1:8097"},
	}
	if change != nil {
		change(&config)
	}

	prevHTTP, prevHTTPS := serverPortHTTP, serverPortHTTPS
	serverPortHTTP, serverPortHTTPS = config.HTTPConfig.HTTPPort, config.HTTPConfig.HTTPSPort
	utils.LoadBaseMainConfig(config)
	t.Cleanup(func() {
		serverPortHTTP, serverPortHTTPS = prevHTTP, prevHTTPS
		utils.LoadBaseMainConfig(utils.Config{})
	})
}

func redirectTarget(t *testing.T, host string, requestURI string, httpsPort string) (string, bool) {
	t.Helper()
	u, err := url.ParseRequestURI(requestURI)
	if err != nil {
		t.Fatal(err)
	}
	return plainHTTPRedirectTarget(host, u, httpsPort)
}

func TestPlainHTTPRedirectTarget(t *testing.T) {
	plainHTTPTestConfig(t, nil)

	for _, c := range []struct {
		host, uri, httpsPort, want string
	}{
		// the server's own hostname, with and without the port it was asked on
		{"172.16.0.14:2052", "/cosmos-ui/", "2053", "https://172.16.0.14:2053/cosmos-ui/"},
		{"172.16.0.14", "/", "2053", "https://172.16.0.14:2053/"},
		// the hostname of a route, path and query untouched
		{"media.home.lan:2052", "/web/index.html?a=1&b=%2Fx%20y", "2053", "https://media.home.lan:2053/web/index.html?a=1&b=%2Fx%20y"},
		{"media.home.lan:2052", "/a%2Fb/c", "2053", "https://media.home.lan:2053/a%2Fb/c"},
		// the standard port is left out
		{"media.home.lan", "/", "443", "https://media.home.lan/"},
		{"media.home.lan:80", "/x", "443", "https://media.home.lan/x"},
		// a path cannot move the redirect to another host
		{"media.home.lan:2052", "//evil.example/x", "2053", "https://media.home.lan:2053//evil.example/x"},
		{"media.home.lan:2052", "/\\evil.example", "2053", "https://media.home.lan:2053/%5Cevil.example"},
	} {
		got, ok := redirectTarget(t, c.host, c.uri, c.httpsPort)
		if !ok || got != c.want {
			t.Errorf("http://%s%s -> %q (%v), want %q", c.host, c.uri, got, ok, c.want)
		}
	}

	for _, host := range []string{
		// not a hostname of this server: 404 as before
		"other.net", "other.net:2052", "172.16.0.15:2052", "evil.example",
		// a local IP is no hostname of this server unless local IP access is allowed
		"192.168.1.10:2052", "127.0.0.1:2052",
		// served over plain HTTP by its zone: never redirected
		"app.plain.net", "app.plain.net:2052",
		"",
	} {
		if got, ok := redirectTarget(t, host, "/", "2053"); ok {
			t.Errorf("http://%s/ is redirected to %q, want no redirect", host, got)
		}
	}
}

func TestPlainHTTPRedirectFollowsWhatHTTPSAccepts(t *testing.T) {
	// with local IP access allowed, HTTPS answers on any local IP: so does the redirect
	plainHTTPTestConfig(t, func(config *utils.Config) {
		config.HTTPConfig.AllowHTTPLocalIPAccess = true
	})
	for host, want := range map[string]string{
		"192.168.1.10:2052": "https://192.168.1.10:2053/cosmos-ui/",
		"127.0.0.1:2052":    "https://127.0.0.1:2053/cosmos-ui/",
		"172.16.0.14:2052":  "https://172.16.0.14:2053/cosmos-ui/",
	} {
		if got, ok := redirectTarget(t, host, "/cosmos-ui/", "2053"); !ok || got != want {
			t.Errorf("local IP access allowed: http://%s -> %q (%v), want %q", host, got, ok, want)
		}
	}
	for _, host := range []string{"8.8.8.8:2052", "other.net:2052"} {
		if got, ok := redirectTarget(t, host, "/", "2053"); ok {
			t.Errorf("local IP access allowed: http://%s is redirected to %q, want no redirect", host, got)
		}
	}

	// a server that accepts every hostname redirects every well-formed one, and only those
	plainHTTPTestConfig(t, func(config *utils.Config) {
		config.HTTPConfig.AcceptAllInsecureHostname = true
	})
	for host, want := range map[string]string{
		"anything.example:2052": "https://anything.example:2053/",
		"[::1]:2052":            "https://[::1]:2053/",
		"[fd00::14]":            "https://[fd00::14]:2053/",
		"Some_Host-1:2052":      "https://Some_Host-1:2053/",
	} {
		if got, ok := redirectTarget(t, host, "/", "2053"); !ok || got != want {
			t.Errorf("every hostname accepted: http://%s -> %q (%v), want %q", host, got, ok, want)
		}
	}
	for _, host := range []string{"user@evil.example", "a b", "evil.example/x", "evil.example?x", "evil.example#x", "x\\y", "%65vil.example", "app.plain.net"} {
		if got, ok := redirectTarget(t, host, "/", "2053"); ok {
			t.Errorf("every hostname accepted: Host %q is redirected to %q, want no redirect", host, got)
		}
	}
}

func TestPlainHTTPHandlerRedirectsAndServesNothingMore(t *testing.T) {
	served := 0
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served++
		w.WriteHeader(http.StatusTeapot)
	})
	ask := func(method, host, path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "http://"+host+path, nil)
		request.RemoteAddr = "172.16.0.50:51000" // a client of the local network
		response := httptest.NewRecorder()
		plainHTTPHandler(router).ServeHTTP(response, request)
		return response
	}

	for _, allowLocal := range []bool{false, true} {
		plainHTTPTestConfig(t, func(config *utils.Config) {
			config.HTTPConfig.AllowHTTPLocalIPAccess = allowLocal
		})
		served = 0

		// the report: the UI by IP, and an application by its hostname, used to answer 404
		for host, want := range map[string]string{
			"172.16.0.14:2052":    "https://172.16.0.14:2053/cosmos-ui/?x=1",
			"media.home.lan:2052": "https://media.home.lan:2053/cosmos-ui/?x=1",
		} {
			for _, method := range []string{"GET", "HEAD", "POST"} {
				response := ask(method, host, "/cosmos-ui/?x=1")
				if response.Code != http.StatusTemporaryRedirect || response.Header().Get("Location") != want {
					t.Errorf("local IP access %v: %s http://%s answered %d to %q, want 307 to %q",
						allowLocal, method, host, response.Code, response.Header().Get("Location"), want)
				}
			}
		}
		// nothing of it was handed to the router: the port serves no page and no API of an HTTPS hostname
		if served != 0 {
			t.Errorf("local IP access %v: %d request(s) for HTTPS hostnames were served over plain HTTP", allowLocal, served)
		}

		// unknown hostnames: as before
		for _, host := range []string{"other.net:2052", "8.8.8.8:2052"} {
			if response := ask("GET", host, "/"); response.Code != http.StatusNotFound || response.Header().Get("Location") != "" {
				t.Errorf("local IP access %v: http://%s answered %d, want 404", allowLocal, host, response.Code)
			}
		}
		if served != 0 {
			t.Errorf("local IP access %v: an unknown hostname was served", allowLocal)
		}

		// the HTTP-only domain: served by the router, as before
		if response := ask("GET", "app.plain.net:2052", "/"); response.Code != http.StatusTeapot || served != 1 {
			t.Errorf("local IP access %v: http://app.plain.net answered %d (served %d), want the router", allowLocal, response.Code, served)
		}
	}

	// another local IP than the server's own name: redirected only when HTTPS accepts it, never served
	plainHTTPTestConfig(t, nil)
	served = 0
	if response := ask("GET", "192.168.1.10:2052", "/cosmos-ui/"); response.Code != http.StatusNotFound {
		t.Errorf("local IP access off: http://192.168.1.10 answered %d, want 404", response.Code)
	}
	plainHTTPTestConfig(t, func(config *utils.Config) { config.HTTPConfig.AllowHTTPLocalIPAccess = true })
	response := ask("GET", "192.168.1.10:2052", "/cosmos-ui/")
	if response.Code != http.StatusTemporaryRedirect || response.Header().Get("Location") != "https://192.168.1.10:2053/cosmos-ui/" {
		t.Errorf("local IP access on: http://192.168.1.10 answered %d to %q, want 307 to HTTPS", response.Code, response.Header().Get("Location"))
	}
	if served != 0 {
		t.Error("a local IP was served over plain HTTP")
	}
	// the answer is the redirect and nothing else
	if body := response.Body.String(); !strings.Contains(body, "https://192.168.1.10:2053/cosmos-ui/") || len(body) > 200 {
		t.Errorf("unexpected body in the redirect: %q", body)
	}
}
