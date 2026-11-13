package user

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: tests of the rule that GET /cosmos/api/mfa never replaces a
// verified secret (fork_mfa_keep.go). The helpers are in fork_mfa_limit_test.go.

// a session with the code given
func fullyLoggedIn(nickname string) utils.AuthContext {
	return utils.AuthContext{Nickname: nickname, Role: utils.USER, UserRole: utils.USER,
		Permissions: utils.GetRolePermissions(utils.USER), MFAState: 0}
}

func getNewSecret(ctx utils.AuthContext) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	New2FA(w, mfaRequest("GET", "", ctx))
	return w
}

func TestNew2FAKeepsAVerifiedSecret(t *testing.T) {
	mfaTestSetup(t)
	secret := mfaTestUser(t, "alice", true)

	// fully logged in: this used to replace the secret and switch the second factor off
	w := getNewSecret(fullyLoggedIn("alice"))
	if w.Code != http.StatusConflict || errorCode(t, w) != "2FA007" {
		t.Fatalf("GET with a verified secret: %d %s, want 409 2FA007", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "otpauth://") || strings.Contains(w.Body.String(), secret) {
		t.Fatalf("the refusal carries a secret: %s", w.Body.String())
	}

	// password only: refused as before, by the existing check
	w = getNewSecret(halfLoggedIn("alice"))
	if w.Code != http.StatusUnauthorized || errorCode(t, w) != "HTTP006" {
		t.Fatalf("GET before the code was given: %d %s, want 401 HTTP006", w.Code, w.Body.String())
	}

	after, err := utils.GetUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	if after.MFAKey != secret || !after.Was2FAVerified {
		t.Fatalf("the verified secret was touched: same key %v, verified %v", after.MFAKey == secret, after.Was2FAVerified)
	}

	// the authenticator the user already has keeps working
	if w := postCode("alice", rightCode(t, secret)); w.Code != http.StatusOK {
		t.Fatalf("code of the kept secret: %d %s, want 200", w.Code, w.Body.String())
	}
}

func TestNew2FAStillEnrolsAndFollowsAnAdminReset(t *testing.T) {
	mfaTestSetup(t)
	pending := mfaTestUser(t, "alice", false)

	// a secret that was never confirmed is replaced on every visit of the setup page, as before
	w := getNewSecret(halfLoggedIn("alice"))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "otpauth://") {
		t.Fatalf("GET during enrolment: %d %s, want 200 with the provisioning URL", w.Code, w.Body.String())
	}
	user, _ := utils.GetUser("alice")
	if user.MFAKey == "" || user.MFAKey == pending || user.Was2FAVerified {
		t.Fatalf("enrolment did not store a new unverified secret (verified %v)", user.Was2FAVerified)
	}

	// confirming it makes it the verified one
	enrolled := user.MFAKey
	if w := postCode("alice", rightCode(t, enrolled)); w.Code != http.StatusOK {
		t.Fatalf("confirming the new secret: %d %s", w.Code, w.Body.String())
	}
	if w := getNewSecret(fullyLoggedIn("alice")); w.Code != http.StatusConflict {
		t.Fatalf("GET right after enrolment: %d, want 409", w.Code)
	}

	// the reset of the Users page (DELETE, needs the users permission and a confirmed password)
	admin := utils.AuthContext{Nickname: "root", Role: utils.ADMIN, UserRole: utils.ADMIN,
		Permissions: utils.GetRolePermissions(utils.ADMIN), IsSudoed: true}
	w = httptest.NewRecorder()
	Delete2FA(w, mfaRequest("DELETE", `{"nickname":"alice"}`, admin))
	if w.Code != http.StatusOK {
		t.Fatalf("admin reset: %d %s", w.Code, w.Body.String())
	}

	// after it a new secret is issued again
	w = getNewSecret(fullyLoggedIn("alice"))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "otpauth://") {
		t.Fatalf("GET after the admin reset: %d %s, want 200 with the provisioning URL", w.Code, w.Body.String())
	}
	user, _ = utils.GetUser("alice")
	if user.MFAKey == "" || user.MFAKey == enrolled || user.Was2FAVerified {
		t.Fatal("the reset did not lead to a new unverified secret")
	}
}
