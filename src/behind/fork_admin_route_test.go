package behind

// Plan 45: a web manager Cosmos created (pgAdmin, phpMyAdmin, Adminer, Redis
// Insight) has a URL for administrators behind Cosmos' sign-in. The one-click
// repair of the URL check changes only ForkAppOwnHost or ForkNoVisitorAddress:
// it must never take that protection, or anything else of the URL, away.

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestTheRepairNeverTakesAManagersSignInAway(t *testing.T) {
	manager := urlTo("pgadmin", "db-pgadmin.server.local", false)
	manager.AuthEnabled = true
	manager.AdminOnly = true
	manager.Target = "http://db-pgadmin:80"
	withApp(t, "dpage/pgadmin4:9", nil, nil, []utils.ProxyRouteConfig{manager}, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<!DOCTYPE html><html><head><title>pgAdmin 4</title></head></html>"))
	})
	var saved []utils.ProxyRouteConfig
	oldUpdate, oldReload := updateRoute, reloadRoutes
	updateRoute = func(name string, change func(*utils.ProxyRouteConfig) error) error {
		c := utils.GetMainConfig()
		for i := range c.HTTPConfig.ProxyConfig.Routes {
			if c.HTTPConfig.ProxyConfig.Routes[i].Name == name {
				if err := change(&c.HTTPConfig.ProxyConfig.Routes[i]); err != nil {
					return err
				}
				saved = append([]utils.ProxyRouteConfig{}, c.HTTPConfig.ProxyConfig.Routes...)
				utils.LoadBaseMainConfig(c)
				return nil
			}
		}
		return errNoRoute
	}
	reloadRoutes = func(bool) {}
	defer func() { updateRoute, reloadRoutes = oldUpdate, oldReload }()

	admin := &utils.AuthContext{Nickname: "admin", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN}, IsSudoed: true, UserRole: utils.ADMIN}
	for _, body := range []string{
		`{"route":"pgadmin","appOwnHost":true}`, `{"route":"pgadmin","noVisitorAddress":true}`,
		`{"route":"pgadmin","appOwnHost":false}`, `{"route":"pgadmin","noVisitorAddress":false}`,
		// fields the route does not take are ignored, never applied
		`{"route":"pgadmin","appOwnHost":true,"AuthEnabled":false,"AdminOnly":false,"authEnabled":false,"adminOnly":false}`,
	} {
		req := httptest.NewRequest("POST", "/cosmos/api/url-check/apply", bytes.NewReader([]byte(body)))
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, admin))
		rec := httptest.NewRecorder()
		ApplyRoute(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
		if len(saved) != 1 {
			t.Fatalf("%s: routes %v", body, saved)
		}
		got := saved[0]
		if !got.AuthEnabled || !got.AdminOnly {
			t.Fatalf("%s took the sign-in away: AuthEnabled %v AdminOnly %v", body, got.AuthEnabled, got.AdminOnly)
		}
		// nothing but the two settings of the repair differs
		want := manager
		want.ForkAppOwnHost, want.ForkNoVisitorAddress = got.ForkAppOwnHost, got.ForkNoVisitorAddress
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s changed more than its setting:\n%+v\n%+v", body, got, want)
		}
	}
}
