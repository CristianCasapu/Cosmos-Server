package main

// Fork addition: an API token holds at most what its owner may do now.
//
// Upstream kept a token's permissions as they were when it was made: a token
// of an administrator who was later made a plain user, or whose account was
// deleted, kept working with all of them. Now each request with a token
// looks its owner up: a token whose owner no longer exists is refused like
// an invalid token, and the token's permissions are cut to those the owner's
// role holds at that moment (they come back if the role gets them back).

import (
	"errors"
	"net/http"

	"github.com/azukaar/cosmos-server/src/utils"
)

// tokenPermissionsNow is the hook of tokenMiddleware, once the token is
// known to be valid. It returns the permissions the request has, or answers
// and returns false.
func tokenPermissionsNow(w http.ResponseWriter, r *http.Request, name string, token *utils.APITokenConfig) ([]utils.Permission, bool) {
	owner, err := utils.GetUser(token.Owner)
	if err != nil {
		if errors.Is(err, utils.ErrNotFound) {
			utils.RecordSecurity(r, utils.SecTokenInvalid, name, "what", "owner no longer exists")
			utils.Error("API Token: the owner of token "+name+" no longer exists", nil)
			utils.HTTPError(w, "Invalid API token", http.StatusUnauthorized, "AT001")
			return nil, false
		}
		utils.Error("API Token: could not read the owner of token "+name, err)
		utils.HTTPError(w, "API token owner could not be checked", http.StatusInternalServerError, "AT005")
		return nil, false
	}
	return ownerHolds(token.Permissions, utils.GetRolePermissions(owner.Role)), true
}

// ownerHolds keeps the permissions of the token that are also the owner's
func ownerHolds(tokenPermissions []utils.Permission, ownerPermissions []utils.Permission) []utils.Permission {
	held := map[utils.Permission]bool{}
	for _, p := range ownerPermissions {
		held[p] = true
	}
	kept := []utils.Permission{}
	for _, p := range tokenPermissions {
		if held[p] {
			kept = append(kept, p)
		}
	}
	return kept
}
