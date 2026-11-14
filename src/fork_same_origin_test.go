package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/utils"
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
		{"API token in the query of a websocket", "GET", "/cosmos/api/terminal/bash?token=cosmos_abc", map[string]string{"Origin": "https://dashboard.example.net", "Upgrade": "websocket", "Connection": "Upgrade"}, true},
		{"API token in the query of anything else counts for nothing", "GET", "/cosmos/api/restart?token=cosmos_abc", map[string]string{"Sec-Fetch-Site": "cross-site"}, false},

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

// A route that gives an application its own port is served by this server too:
// the API must not answer there, where the application's pages are at home.
func TestAPIDoesNotAnswerOnThePortOfAnApplication(t *testing.T) {
	prev := routePortOf
	routePortOf = func(r *http.Request) (string, bool) {
		if strings.HasSuffix(r.Host, ":7200") {
			return "7200", true
		}
		return "", false
	}
	t.Cleanup(func() { routePortOf = prev })

	try := func(host string, target string, headers map[string]string) (int, int) {
		reached := 0
		router := sameOriginTestRouter(&reached)
		req := httptest.NewRequest("GET", "https://"+host+target, nil)
		req.Host = host
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code, reached
	}

	sameOrigin := map[string]string{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "cors"}

	// a page of the application calls the API on its own origin
	if code, reached := try("172.16.0.14:7200", "/cosmos/api/restart", sameOrigin); code != http.StatusForbidden || reached != 0 {
		t.Errorf("the API answered on the port of an application: %d (handler reached %d times)", code, reached)
	}
	if code, reached := try("172.16.0.14:7200", "/cosmos/rclone/config/create", sameOrigin); code != http.StatusForbidden || reached != 0 {
		t.Errorf("the rclone API answered on the port of an application: %d (handler reached %d times)", code, reached)
	}
	// neither a script nor a token changes where the API lives
	if code, reached := try("172.16.0.14:7200", "/cosmos/api/restart", nil); code != http.StatusForbidden || reached != 0 {
		t.Errorf("a request without browser headers was answered on the port of an application: %d", code)
	}
	if code, reached := try("172.16.0.14:7200", "/cosmos/api/restart", map[string]string{"Authorization": "Bearer cosmos_abc"}); code != http.StatusForbidden || reached != 0 {
		t.Errorf("a request with a token was answered on the port of an application: %d", code)
	}
	// the sign-in of the application ends on its own address
	if code, reached := try("172.16.0.14:7200", "/cosmos/oauth2/detect-callback?code=x&state=y", map[string]string{"Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "navigate"}); code != http.StatusOK || reached != 1 {
		t.Errorf("the sign-in callback of an application on its port: %d (handler reached %d times)", code, reached)
	}
	// the server's own port is unchanged
	if code, reached := try("172.16.0.14:2053", "/cosmos/api/restart", sameOrigin); code != http.StatusOK || reached != 1 {
		t.Errorf("the API on the server's own port: %d (handler reached %d times)", code, reached)
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

// A refused request is an incident in the security record, with which rule
// refused it as one of a fixed list of words: nothing a request says about
// itself is recorded as the reason. What passes leaves no such entry.
func TestRefusedForeignRequestsAreRecorded(t *testing.T) {
	const host = "172.16.0.14:2053"
	events := captureSecurity(t)
	reached := 0
	router := sameOriginTestRouter(&reached)

	ask := func(method string, target string, headers map[string]string) int {
		req := httptest.NewRequest(method, "https://"+host+target, nil)
		req.Host = host
		req.RemoteAddr = "203.0.113.7:50000"
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder.Code
	}

	hostile := "cross-site\r\n2026-11-13T10:00:00+0000 cosmos-security kind=auth.login.failed ip=198.51.100.1"
	refused := []struct {
		name    string
		method  string
		target  string
		headers map[string]string
		reason  string
	}{
		{"another website", "POST", "/cosmos/api/restart", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example.net"}, "another_site"},
		{"another application of the same domain", "POST", "/cosmos/api/docker-service", map[string]string{"Sec-Fetch-Site": "same-site"}, "same_domain"},
		{"opened from the address bar", "GET", "/cosmos/api/restart", map[string]string{"Sec-Fetch-Site": "none", "Sec-Fetch-Mode": "navigate"}, "opened_by_hand"},
		{"an old browser, Origin of another site", "POST", "/cosmos/api/restart", map[string]string{"Origin": "https://evil.example.net"}, "another_origin"},
		{"a mark that is not one", "GET", "/cosmos/api/restart", map[string]string{"Sec-Fetch-Site": hostile}, "not_understood"},
	}
	for _, c := range refused {
		if code := ask(c.method, c.target, c.headers); code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", c.name, code)
		}
	}
	// what the panel itself and programs send: no entry
	if code := ask("POST", "/cosmos/api/restart", map[string]string{"Sec-Fetch-Site": "same-origin"}); code != http.StatusOK {
		t.Errorf("the panel's own call: status %d", code)
	}
	if code := ask("GET", "/cosmos/api/restart", nil); code != http.StatusOK {
		t.Errorf("a script's call: status %d", code)
	}

	got := events()
	if len(got) != len(refused) {
		t.Fatalf("%d events for %d refusals and two requests that passed: %+v", len(got), len(refused), got)
	}
	for i, c := range refused {
		event := got[i]
		if event.Kind != utils.SecAccessForeignDenied || event.IP != "203.0.113.7" || event.Path != strings.SplitN(c.target, "?", 2)[0] || event.Method != c.method {
			t.Errorf("%s: recorded %+v", c.name, event)
		}
		if event.Details["reason"] != c.reason || len(event.Details) != 1 {
			t.Errorf("%s: reason %q (details %v), want %q", c.name, event.Details["reason"], event.Details, c.reason)
		}
	}
	if reached != 2 {
		t.Errorf("%d requests reached a handler, want the two that may", reached)
	}

	// the port of an application
	previous := routePortOf
	routePortOf = func(*http.Request) (string, bool) { return "7200", true }
	defer func() { routePortOf = previous }()
	if code := ask("GET", "/cosmos/api/restart", map[string]string{"Sec-Fetch-Site": "same-origin"}); code != http.StatusForbidden {
		t.Errorf("on the port of an application: status %d", code)
	}
	if got = events(); got[len(got)-1].Details["reason"] != "application_port" {
		t.Errorf("on the port of an application: %+v", got[len(got)-1])
	}
}
