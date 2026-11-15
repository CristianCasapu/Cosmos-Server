package user

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: tests of fork_manage_target.go.

const usersOnlyRole utils.Role = 5

func manageTargetSetup(t *testing.T) {
	t.Helper()
	mfaTestSetup(t)
	config := utils.GetBaseMainConfig()
	config.Roles = map[utils.Role]utils.RoleConfig{
		usersOnlyRole: {Name: "Account keeper", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_USERS_READ, utils.PERM_USERS}},
	}
	utils.LoadBaseMainConfig(config)

	for nickname, role := range map[string]utils.Role{"boss": utils.ADMIN, "other": utils.ADMIN, "child": utils.USER, "keeper": usersOnlyRole} {
		if err := utils.CreateUser(utils.User{Nickname: nickname, Password: "x", Role: role, MFAKey: "SECRET" + nickname, Was2FAVerified: true, Email: nickname + "@example.com"}); err != nil {
			t.Fatal(err)
		}
	}
	prevFBL := utils.FBL
	utils.FBL = utils.NewFirebaseApiSdk("http://127.0.0.1:9")
	t.Cleanup(func() { utils.FBL = prevFBL })
}

func usersOnlyToken() utils.AuthContext {
	return utils.AuthContext{Nickname: "boss", APIToken: &utils.APITokenContext{Name: "users", Owner: "boss", Permissions: []utils.Permission{utils.PERM_USERS_READ, utils.PERM_USERS}}}
}

// what the users permission does to an account, each as one request
func manageActions() map[string]func(target string, ctx utils.AuthContext) int {
	run := func(handler http.HandlerFunc, req *http.Request) int {
		w := httptest.NewRecorder()
		handler(w, req)
		return w.Code
	}
	return map[string]func(string, utils.AuthContext) int{
		"invitation link": func(target string, ctx utils.AuthContext) int {
			return run(UserResendInviteLink, userRequest("POST", target, `{"nickname":"`+target+`","formType":"1"}`, ctx))
		},
		"two-factor reset": func(target string, ctx utils.AuthContext) int {
			return run(Delete2FA, userRequest("DELETE", target, `{"nickname":"`+target+`"}`, ctx))
		},
		"role change": func(target string, ctx utils.AuthContext) int {
			return run(UserEdit, userRequest("PATCH", target, `{"role":1}`, ctx))
		},
		"email change": func(target string, ctx utils.AuthContext) int {
			return run(UserEdit, userRequest("PATCH", target, `{"Email":"new@example.com"}`, ctx))
		},
		"delete": func(target string, ctx utils.AuthContext) int {
			return run(UserDelete, userRequest("DELETE", target, "", ctx))
		},
	}
}

func TestUsersPermissionDoesNotReachAStrongerAccount(t *testing.T) {
	callers := map[string]func() utils.AuthContext{
		"a token with the users permission only": usersOnlyToken,
		"a role with the users permission only":  func() utils.AuthContext { return sessionOf("keeper", usersOnlyRole, true) },
	}
	for callerName, callerOf := range callers {
		for action, do := range manageActions() {
			manageTargetSetup(t)
			caller := callerOf()
			before, _ := utils.GetUser("other")
			if code := do("other", caller); code != http.StatusForbidden {
				t.Errorf("%s, %s of an administrator: %d, want 403", callerName, action, code)
			}
			after, err := utils.GetUser("other")
			if err != nil || after.Role != before.Role || after.Email != before.Email || after.MFAKey != before.MFAKey || after.RegisterKey != before.RegisterKey {
				t.Errorf("%s, %s of an administrator changed the account (err %v)", callerName, action, err)
			}

			// an account it holds every permission of is still its to manage
			if code := do("child", caller); code != http.StatusOK {
				t.Errorf("%s, %s of a plain user: %d, want 200", callerName, action, code)
			}
		}
	}
}

func TestAnAdministratorStillManagesAnotherAdministrator(t *testing.T) {
	for action, do := range manageActions() {
		manageTargetSetup(t)
		if code := do("other", sessionOf("boss", utils.ADMIN, true)); code != http.StatusOK {
			t.Errorf("an administrator, %s of another administrator: %d, want 200", action, code)
		}
	}
}
