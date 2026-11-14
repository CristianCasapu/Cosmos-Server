package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func gateReq(host string, ctx *utils.AuthContext) *http.Request {
	r := httptest.NewRequest("GET", "http://"+host+"/app/page", nil)
	return r.WithContext(context.WithValue(r.Context(), utils.AuthCtxKey, ctx))
}

func TestAppGateBlocksPendingMFA(t *testing.T) {
	cfg := utils.Config{}
	cfg.HTTPConfig.Hostname = "cosmos.example"
	utils.LoadBaseMainConfig(cfg)
	route := utils.ProxyRouteConfig{Name: "app", Host: "app.example", AuthEnabled: true}
	admin := []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN_READ}

	cases := []struct {
		name  string
		gate  func(http.ResponseWriter, *http.Request, utils.ProxyRouteConfig) error
		state int
		host  string
		page  string
		back  string
	}{
		{"user pending totp", LoggedInOnlyWithRedirect, 1, "app.example", "loginmfa", "%2Fcosmos-ui"},
		{"user needs enrol", LoggedInOnlyWithRedirect, 2, "app.example", "newmfa", "%2Fcosmos-ui"},
		{"admin pending totp", AdminOnlyWithRedirect, 1, "app.example", "loginmfa", "%2Fcosmos-ui"},
		{"same host keeps path", LoggedInOnlyWithRedirect, 1, "cosmos.example", "loginmfa", "%2Fapp%2Fpage"},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		err := c.gate(w, gateReq(c.host, &utils.AuthContext{Nickname: "u", Permissions: admin, MFAState: c.state}), route)
		if err == nil {
			t.Errorf("%s: gate admitted the session", c.name)
			continue
		}
		loc := w.Header().Get("Location")
		if w.Code != http.StatusTemporaryRedirect || !strings.HasPrefix(loc, "http://cosmos.example/cosmos-ui/"+c.page+"?") || !strings.HasSuffix(loc, "redirect="+c.back) {
			t.Errorf("%s: got %d %q", c.name, w.Code, loc)
		}
	}

	// fully authenticated sessions still pass both gates
	for _, gate := range []func(http.ResponseWriter, *http.Request, utils.ProxyRouteConfig) error{LoggedInOnlyWithRedirect, AdminOnlyWithRedirect} {
		w := httptest.NewRecorder()
		if err := gate(w, gateReq("app.example", &utils.AuthContext{Nickname: "u", Permissions: admin, MFAState: 0}), route); err != nil {
			t.Errorf("MFA-complete session rejected: %v", err)
		}
	}
}

// Fork addition: the sign-in and the two-factor page are on the port the
// visitor is using, not on 443 (utils/fork_panel_origin.go).
func TestAppGateSendsTheVisitorToThePanelOnTheirPort(t *testing.T) {
	realHTTPS := utils.IsHTTPS
	defer func() { utils.IsHTTPS = realHTTPS }()
	utils.IsHTTPS = true

	cfg := utils.Config{}
	cfg.HTTPConfig.Hostname = "cosmos.example"
	cfg.HTTPConfig.HTTPSPort = "2053"
	cfg.HTTPConfig.AuthPrivateKey = strings.Repeat("k", 80)
	utils.LoadBaseMainConfig(cfg)
	route := utils.ProxyRouteConfig{Name: "app", Host: "app.example", AuthEnabled: true}

	for host, origin := range map[string]string{
		"app.example:2053": "https://cosmos.example:2053",
		"app.example:8443": "https://cosmos.example:8443",
		"app.example":      "https://cosmos.example",
	} {
		// not signed in: to the sign-in page
		w := httptest.NewRecorder()
		if err := LoggedInOnlyWithRedirect(w, gateReq(host, &utils.AuthContext{}), route); err == nil {
			t.Errorf("%s: the gate admitted a visitor who is not signed in", host)
		}
		if loc := w.Header().Get("Location"); w.Code != http.StatusFound || !strings.HasPrefix(loc, origin+"/cosmos-ui/openid?") {
			t.Errorf("%s: sign-in redirect %d %q, want it on %s", host, w.Code, loc, origin)
		}

		// signed in, second factor pending: to the two-factor page
		w = httptest.NewRecorder()
		LoggedInOnlyWithRedirect(w, gateReq(host, &utils.AuthContext{Nickname: "u", MFAState: 1}), route)
		if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, origin+"/cosmos-ui/loginmfa?") {
			t.Errorf("%s: two-factor redirect %q, want it on %s", host, loc, origin)
		}
	}

	// on the panel's own name the visitor comes back to the page asked for, port or not
	w := httptest.NewRecorder()
	LoggedInOnlyWithRedirect(w, gateReq("cosmos.example:2053", &utils.AuthContext{Nickname: "u", MFAState: 1}), route)
	if loc := w.Header().Get("Location"); !strings.HasSuffix(loc, "redirect=%2Fapp%2Fpage") {
		t.Errorf("same name with a port: %q", loc)
	}
}
