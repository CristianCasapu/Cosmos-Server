package user

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/gorilla/mux"
)

// Fork addition: tests of fork_accounts.go. The store and key helpers are in
// fork_mfa_limit_test.go.

func sessionOf(nickname string, role utils.Role, sudo bool) utils.AuthContext {
	return utils.AuthContext{Nickname: nickname, Role: role, UserRole: role, Permissions: utils.GetRolePermissions(role), IsSudoed: sudo}
}

func userRequest(method string, nickname string, body string, ctx utils.AuthContext) *http.Request {
	req := httptest.NewRequest(method, "https://cosmos.example.com/cosmos/api/users/"+nickname, strings.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"nickname": nickname})
	return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &ctx))
}

func TestLoginAnswersAPendingAccountLikeAnUnknownOne(t *testing.T) {
	mfaTestSetup(t)
	if err := utils.CreateUser(utils.User{Nickname: "invited", Role: utils.USER, RegisterKey: "pending"}); err != nil {
		t.Fatal(err)
	}

	answer := func(nickname string) (int, string) {
		req := httptest.NewRequest("POST", "https://localhost/cosmos/api/login", strings.NewReader(`{"nickname":"`+nickname+`","password":"Some-Passw0rd!"}`))
		req.Host = "localhost"
		w := httptest.NewRecorder()
		UserLogin(w, req)
		return w.Code, w.Body.String()
	}
	unknownCode, unknownBody := answer("nobody")
	pendingCode, pendingBody := answer("invited")
	if pendingCode != unknownCode || pendingBody != unknownBody {
		t.Errorf("a pending account answers %d %s, an unknown one %d %s", pendingCode, pendingBody, unknownCode, unknownBody)
	}
}

func TestAnAdministratorDoesNotDeleteOrDemoteTheOwnAccount(t *testing.T) {
	mfaTestSetup(t)
	for nickname, role := range map[string]utils.Role{"boss": utils.ADMIN, "second": utils.ADMIN, "child": utils.USER} {
		if err := utils.CreateUser(utils.User{Nickname: nickname, Password: "x", Role: role}); err != nil {
			t.Fatal(err)
		}
	}
	exists := func(nickname string) bool { _, err := utils.GetUser(nickname); return err == nil }
	// the handlers ask whether this server is an agent; as upstream's tests, an object that reaches nothing
	prevFBL := utils.FBL
	utils.FBL = utils.NewFirebaseApiSdk("http://127.0.0.1:9")
	t.Cleanup(func() { utils.FBL = prevFBL })

	// the own account, with or without the password confirmed
	for _, sudo := range []bool{false, true} {
		w := httptest.NewRecorder()
		UserDelete(w, userRequest("DELETE", "boss", "", sessionOf("boss", utils.ADMIN, sudo)))
		if w.Code != http.StatusConflict || !exists("boss") {
			t.Errorf("an administrator deleting the own account (password confirmed: %v): %d, still there: %v", sudo, w.Code, exists("boss"))
		}
	}
	w := httptest.NewRecorder()
	UserEdit(w, userRequest("PATCH", "boss", `{"role":1}`, sessionOf("boss", utils.ADMIN, true)))
	if after, _ := utils.GetUser("boss"); w.Code != http.StatusConflict || after.Role != utils.ADMIN {
		t.Errorf("an administrator demoting the own account: %d, role now %d", w.Code, after.Role)
	}

	// another administrator can do both, and a plain user may still leave
	w = httptest.NewRecorder()
	UserEdit(w, userRequest("PATCH", "second", `{"role":1}`, sessionOf("boss", utils.ADMIN, true)))
	if after, _ := utils.GetUser("second"); w.Code != http.StatusOK || after.Role != utils.USER {
		t.Errorf("an administrator demoting another account: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	UserDelete(w, userRequest("DELETE", "second", "", sessionOf("boss", utils.ADMIN, true)))
	if w.Code != http.StatusOK || exists("second") {
		t.Errorf("an administrator deleting another account: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	UserDelete(w, userRequest("DELETE", "child", "", sessionOf("child", utils.USER, false)))
	if w.Code != http.StatusOK || exists("child") {
		t.Errorf("a plain user deleting the own account: %d %s", w.Code, w.Body.String())
	}
}
