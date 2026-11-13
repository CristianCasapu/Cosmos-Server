package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

// Fork addition: tests of fork_same_origin.go, which requests the API accepts
// by where they come from.

// sameOriginTestRouter is an API router as InitServer builds it: a sub-router
// under /cosmos with the check in front, and a handler that counts its calls.
func sameOriginTestRouter(reached *int) *mux.Router {
	router := mux.NewRouter()
	api := router.PathPrefix("/cosmos").Subrouter()
	requireSameOrigin(api)
	hit := func(w http.ResponseWriter, r *http.Request) { *reached++; w.Write([]byte("ok")) }
	api.HandleFunc("/api/restart", hit)
	api.HandleFunc("/api/servapps/{containerId}/manage/{action}", hit)
	api.HandleFunc("/api/docker-service", hit)
	api.HandleFunc("/api/terminal/{route}", hit)
	api.HandleFunc("/rclone/config/create", hit)
	api.HandleFunc("/oauth2/detect-callback", hit)
	return router
}

type originCase struct {
	name    string
	method  string
	target  string
	headers map[string]string
	allowed bool
}

func TestAPIRefusesRequestsOfOtherSites(t *testing.T) {
	const host = "172.16.0.14:2053"
	cases := []originCase{
		// the panel itself
		{"panel call", "GET", "/cosmos/api/restart", map[string]string{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "cors"}, true},
		{"panel post", "POST", "/cosmos/api/docker-service", map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "https://" + host}, true},
		{"panel download in a new tab", "GET", "/cosmos/api/restart", map[string]string{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "navigate"}, true},
		{"panel terminal, websocket without fetch metadata", "GET", "/cosmos/api/terminal/bash", map[string]string{"Origin": "https://" + host, "Upgrade": "websocket"}, true},
		{"panel on plain HTTP: no fetch metadata, Origin of this host", "POST", "/cosmos/api/docker-service", map[string]string{"Origin": "http://" + host}, true},
		{"panel behind a proxy that rewrites Host", "POST", "/cosmos/api/docker-service", map[string]string{"Origin": "https://cosmos.example.com", "X-Forwarded-Host": "cosmos.example.com"}, true},

		// not a browser
		{"curl or a script", "GET", "/cosmos/api/restart", nil, true},
		{"script posting", "POST", "/cosmos/api/docker-service", map[string]string{"Content-Type": "application/json"}, true},
		{"API token in the header, whatever the site", "POST", "/cosmos/api/docker-service", map[string]string{"Authorization": "Bearer cosmos_abc", "Sec-Fetch-Site": "cross-site", "Origin": "https://dashboard.example.net"}, true},
		{"API token in the query (websocket)", "GET", "/cosmos/api/terminal/bash?token=cosmos_abc", map[string]string{"Origin": "https://dashboard.example.net"}, true},

		// the sign-in callback of an application arrives from the panel's hostname
		{"application sign-in callback", "GET", "/cosmos/oauth2/detect-callback?code=x&state=y", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate"}, true},

		// another site
		{"link or redirect of another site", "GET", "/cosmos/api/restart", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate"}, false},
		{"image tag of another site", "GET", "/cosmos/api/servapps/jellyfin/manage/stop", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "image"}, false},
		{"form of another site", "POST", "/cosmos/api/docker-service", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example", "Content-Type": "text/plain"}, false},
		{"form of another site, any method route", "POST", "/cosmos/rclone/config/create", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}, false},
		{"page of an application on the same domain", "POST", "/cosmos/api/docker-service", map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "https://app.example.com"}, false},
		{"link opened from a mail program or the address bar", "GET", "/cosmos/api/restart", map[string]string{"Sec-Fetch-Site": "none", "Sec-Fetch-Mode": "navigate"}, false},
		{"old browser: form of another site", "POST", "/cosmos/api/docker-service", map[string]string{"Origin": "https://evil.example"}, false},
		{"old browser: sandboxed frame", "POST", "/cosmos/api/docker-service", map[string]string{"Origin": "null"}, false},
		{"Origin of the same name on another port", "POST", "/cosmos/api/docker-service", map[string]string{"Origin": "https://172.16.0.14:8096"}, false},
		{"Origin that only starts like this host", "POST", "/cosmos/api/docker-service", map[string]string{"Origin": "https://172.16.0.14:2053.evil.example"}, false},
		{"websocket of another site", "GET", "/cosmos/api/terminal/bash", map[string]string{"Origin": "https://evil.example", "Upgrade": "websocket"}, false},
		{"a forged X-Forwarded-Host does not help a marked request", "POST", "/cosmos/api/docker-service", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example", "X-Forwarded-Host": "evil.example"}, false},
		{"a token-like query that is not a Cosmos token", "GET", "/cosmos/api/restart?token=abc", map[string]string{"Sec-Fetch-Site": "cross-site"}, false},
		{"unknown value", "GET", "/cosmos/api/restart", map[string]string{"Sec-Fetch-Site": "whatever"}, false},
	}

	for _, c := range cases {
		reached := 0
		router := sameOriginTestRouter(&reached)

		req := httptest.NewRequest(c.method, "https://"+host+c.target, nil)
		req.Host = host
		for name, value := range c.headers {
			req.Header.Set(name, value)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if c.allowed {
			if rec.Code != http.StatusOK || reached != 1 {
				t.Errorf("%s: expected to pass, got %d (handler reached %d times)", c.name, rec.Code, reached)
			}
			continue
		}
		if rec.Code != http.StatusForbidden || reached != 0 {
			t.Errorf("%s: expected 403 without reaching the handler, got %d (handler reached %d times)", c.name, rec.Code, reached)
		}
		if !strings.Contains(rec.Body.String(), `"code":"HTTP009"`) {
			t.Errorf("%s: the refusal should carry its own code, got %q", c.name, rec.Body.String())
		}
	}
}

func TestOriginNamesHost(t *testing.T) {
	match := [][2]string{
		{"https://cosmos.example.com", "cosmos.example.com"},
		{"https://cosmos.example.com:443", "cosmos.example.com"},
		{"https://cosmos.example.com", "cosmos.example.com:443"},
		{"http://cosmos.lan", "cosmos.lan:80"},
		{"https://Cosmos.Example.com:2053", "cosmos.example.com:2053"},
		{"https://[fd00::1]:2053", "[fd00::1]:2053"},
		{"https://[fd00::1]", "[fd00::1]:443"},
	}
	for _, m := range match {
		req := httptest.NewRequest("POST", "https://x/", nil)
		req.Host = m[1]
		if !originNamesHost(m[0], req) {
			t.Errorf("Origin %q should name Host %q", m[0], m[1])
		}
	}
	differ := [][2]string{
		{"https://evil.example", "cosmos.example.com"},
		{"https://cosmos.example.com:8443", "cosmos.example.com"},
		{"https://cosmos.example.com.evil.example", "cosmos.example.com"},
		{"https://evilcosmos.example.com", "cosmos.example.com"},
		{"null", "cosmos.example.com"},
		{"", "cosmos.example.com"},
		{"cosmos.example.com", "cosmos.example.com"},
		{"file://cosmos.example.com", "cosmos.example.com"},
		{"https://cosmos.example.com:80", "cosmos.example.com"},
	}
	for _, d := range differ {
		req := httptest.NewRequest("POST", "https://x/", nil)
		req.Host = d[1]
		if originNamesHost(d[0], req) {
			t.Errorf("Origin %q must not name Host %q", d[0], d[1])
		}
	}
}

func TestPrintableStripsControlCharacters(t *testing.T) {
	if got := printable("/cosmos/api/x\r\nINJECTED \x1b[31m", 200); strings.ContainsAny(got, "\r\n\x1b") {
		t.Errorf("control characters survived: %q", got)
	}
	if got := printable(strings.Repeat("a", 500), 120); len(got) != 120 {
		t.Errorf("expected 120 bytes, got %d", len(got))
	}
}
