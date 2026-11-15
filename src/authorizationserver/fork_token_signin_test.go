package authorizationserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ory/fosite"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: tests of fork_token_signin.go.

func TestAnAPITokenNeverSignsInToAnApplication(t *testing.T) {
	for name, perms := range map[string][]utils.Permission{
		"a token that reads the logs": {utils.PERM_ADMIN_READ},
		"an administrator's token":    utils.DefaultAdminTokenPermissions,
	} {
		req := httptest.NewRequest("GET", "https://cosmos.example.com/cosmos/api/oauth2/auth?client_id=app&response_type=code&scope=openid", nil)
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{
			Nickname: "admin",
			APIToken: &utils.APITokenContext{Name: "t", Owner: "admin", Permissions: perms},
		}))
		w := httptest.NewRecorder()
		authEndpoint(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s at the authorization endpoint: %d, want 403", name, w.Code)
		}
		if loc := w.Header().Get("Location"); loc != "" {
			t.Errorf("%s was sent on to %s", name, loc)
		}
	}
}

func TestTheRoleClaimSaysAdminOnlyForTheAdminPermission(t *testing.T) {
	prevFolder := utils.CONFIGFOLDER
	utils.CONFIGFOLDER = t.TempDir() + "/"
	if err := utils.InitStore(); err != nil {
		t.Fatal(err)
	}
	previous := utils.GetBaseMainConfig()
	config := utils.Config{}
	config.Roles = map[utils.Role]utils.RoleConfig{
		7: {Name: "Viewer", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN_READ}},
		8: {Name: "Operator", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN}},
	}
	utils.LoadBaseMainConfig(config)
	t.Cleanup(func() {
		utils.LoadBaseMainConfig(previous)
		utils.CloseStore()
		utils.CONFIGFOLDER = prevFolder
	})

	want := map[string]struct {
		role  utils.Role
		claim string
	}{
		"admin":    {utils.ADMIN, "admin"},
		"plain":    {utils.USER, "user"},
		"viewer":   {7, "user"},
		"operator": {8, "admin"},
	}
	for nickname, w := range want {
		if err := utils.CreateUser(utils.User{Nickname: nickname, Password: "x", Role: w.role}); err != nil {
			t.Fatal(err)
		}
		claims, err := getUserClaims(nickname, fosite.Arguments{"openid"})
		if err != nil {
			t.Fatal(err)
		}
		if claims["role"] != w.claim {
			t.Errorf("%s (role %d): role claim %v, want %s", nickname, w.role, claims["role"], w.claim)
		}
	}
}
