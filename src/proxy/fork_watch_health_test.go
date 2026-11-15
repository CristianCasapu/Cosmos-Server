package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/utils"
)

// TestHealthAnswersEveryoneTheSameAndTellsTheWatch: /_health answers "OK"
// whatever the request carries (a stranger learns nothing from the header
// of the reachability watch), comes before every URL (so its requests never
// reach SmartShield, the bot filter or the sampled events of an app), and
// marks the watch's own token arrived when it comes from Cloudflare.
func TestHealthAnswersEveryoneTheSameAndTellsTheWatch(t *testing.T) {
	previous := utils.GetMainConfig()
	cfg := previous
	cfg.HTTPConfig.ProxyConfig.Routes = []utils.ProxyRouteConfig{{
		Name: "app", Mode: "SERVAPP", UseHost: true, Host: "app.example.org", Target: "http://192.0.2.50:8080",
		SmartShield:     utils.SmartShieldPolicy{Enabled: true},
		BlockCommonBots: true,
	}}
	utils.LoadBaseMainConfig(cfg)
	defer utils.LoadBaseMainConfig(previous)

	router := mux.NewRouter()
	BuildFromConfig(router, cfg.HTTPConfig.ProxyConfig)

	for _, host := range []string{"app.example.org:2053", "example.org", "anything.example.net"} {
		req := httptest.NewRequest("GET", "https://"+host+"/_health", nil)
		req.Host = host
		var match mux.RouteMatch
		if !router.Match(req, &match) {
			t.Fatalf("%s: no route", host)
		}
		if template, _ := match.Route.GetPathTemplate(); template != "/_health" {
			t.Fatalf("%s: /_health answered by %q", host, template)
		}
	}

	answer := func(header, peer string) (int, string, http.Header) {
		req := httptest.NewRequest("GET", "https://app.example.org:2053/_health?cosmos-watch=0a1b2c3d", nil)
		req.Host = "app.example.org:2053"
		req.RemoteAddr = peer
		if header != "" {
			req.Header.Set(utils.ReachWatchHeader, header)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String(), rec.Header()
	}
	code, body, headers := answer("", "198.51.100.20:40000")
	if code != 200 || body != "OK" {
		t.Fatalf("/_health: %d %q", code, body)
	}
	token := utils.NewWatchToken("app.example.org")
	for _, c := range []struct{ header, peer string }{
		{strings.Repeat("f", 32), "173.245.48.9:40000"},
		{"not a token", "173.245.48.9:40000"},
		{token, "198.51.100.20:40000"},
		{token, "173.245.48.9:40000"},
	} {
		gotCode, gotBody, gotHeaders := answer(c.header, c.peer)
		if gotCode != code || gotBody != body || len(gotHeaders) != len(headers) {
			t.Errorf("header %q from %s: %d %q %v, want what everyone gets", c.header, c.peer, gotCode, gotBody, gotHeaders)
		}
	}
	if arrived, _ := utils.WatchTokenArrived(token); !arrived {
		t.Fatal("the watch's own request from Cloudflare did not arrive")
	}
}
