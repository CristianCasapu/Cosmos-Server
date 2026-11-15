package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: tests of fork_token_owner.go. The set-up is the one of
// fork_session_paths_test.go: the token "script" of alice holds the
// permission to read the logs.

func TestATokenHoldsAtMostWhatItsOwnerHoldsNow(t *testing.T) {
	sessionPathsSetup(t)
	token := map[string]string{"Authorization": "Bearer cosmos_test_token_of_alice"}

	var permissions []utils.Permission
	admin := false
	keep := func(w http.ResponseWriter, r *http.Request) {
		permissions = utils.GetAuthContext(r).APIToken.Permissions
		admin = utils.CheckPermissions(httptest.NewRecorder(), r, utils.PERM_ADMIN_READ) == nil
	}
	outcome, _ := throughTokenMiddleware(pathsRequest("GET", "/cosmos/api/logs", nil, token), keep)
	if !outcome.reached || !admin {
		t.Fatalf("the token of an administrator: %+v", outcome)
	}

	// alice is made a plain user: her token keeps none of the admin's permissions
	if err := utils.UpdateUser("alice", map[string]interface{}{"Role": utils.USER}); err != nil {
		t.Fatal(err)
	}
	outcome, _ = throughTokenMiddleware(pathsRequest("GET", "/cosmos/api/logs", nil, token), keep)
	if !outcome.reached || admin || len(permissions) != 0 {
		t.Errorf("the token after its owner was made a plain user: %+v, permissions %v", outcome, permissions)
	}

	// and gets them back with the role
	if err := utils.UpdateUser("alice", map[string]interface{}{"Role": utils.ADMIN}); err != nil {
		t.Fatal(err)
	}
	if outcome, _ = throughTokenMiddleware(pathsRequest("GET", "/cosmos/api/logs", nil, token), keep); !admin {
		t.Errorf("the token after its owner is an administrator again: %+v", outcome)
	}

	// alice is deleted: her token is refused like an invalid one
	if err := utils.DeleteUser("alice"); err != nil {
		t.Fatal(err)
	}
	outcome, answer := throughTokenMiddleware(pathsRequest("GET", "/cosmos/api/logs", nil, token), keep)
	if outcome.reached || answer.Code != http.StatusUnauthorized {
		t.Errorf("the token of a deleted owner: reached=%v %d", outcome.reached, answer.Code)
	}
}

func TestOwnerHoldsKeepsOnlyTheSharedPermissions(t *testing.T) {
	got := ownerHolds([]utils.Permission{utils.PERM_ADMIN, utils.PERM_CONFIGURATION, utils.PERM_USERS}, []utils.Permission{utils.PERM_LOGIN, utils.PERM_CONFIGURATION})
	if len(got) != 1 || got[0] != utils.PERM_CONFIGURATION {
		t.Errorf("ownerHolds: %v", got)
	}
}
