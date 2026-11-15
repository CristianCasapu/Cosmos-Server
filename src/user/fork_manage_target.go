package user

// Fork addition: the users permission reaches only the accounts it could
// have created.
//
// Upstream checks what a caller may grant only for the role being given
// (creating an account, changing a role). The other things the users
// permission does to an account (a new invitation or password link, removing
// its two-factor key, deleting it, changing its role or its email address)
// were allowed on any account, also on one whose role holds more than the
// caller: an API token or a custom role with the users permission alone could
// take over an administrator. Now such a caller acts only on accounts whose
// role's permissions it holds itself (the same rule as utils.CanGrant); the
// others are refused with 403 and recorded. A full administrator holds every
// permission, so nothing changes for one.

import (
	"errors"
	"net/http"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

// targetBeyondCaller reports the permissions of the target's role the caller
// does not hold, nil when it may act on the account.
func targetBeyondCaller(req *http.Request, target utils.User) []utils.Permission {
	return utils.MissingGrants(req, utils.GetRolePermissions(target.Role))
}

// refuseStrongerAccount is the hook of the handlers that act on an account
// they already loaded. It answers 403 and returns true when the account's
// role holds a permission the caller does not.
func refuseStrongerAccount(w http.ResponseWriter, req *http.Request, target utils.User) bool {
	missing := targetBeyondCaller(req, target)
	if len(missing) == 0 {
		return false
	}
	labels := make([]string, 0, len(missing))
	for _, p := range missing {
		labels = append(labels, utils.PermissionLabel(p))
	}
	utils.RecordSecurity(req, utils.SecAccessDenied, target.Nickname, "permission", strings.Join(labels, ", "))
	utils.Error("User: the caller may not manage an account whose role holds more than it does", nil)
	utils.HTTPError(w, "This account has permissions you do not hold, so you cannot manage it. An administrator with all of its permissions can.", http.StatusForbidden, "UE004")
	return true
}

// refuseStrongerAccountByName is refuseStrongerAccount for the handlers that
// have only the nickname. An account that cannot be read is left to the
// handler, which answers as it did.
func refuseStrongerAccountByName(w http.ResponseWriter, req *http.Request, nickname string) bool {
	target, err := utils.GetUser(nickname)
	if err != nil {
		if !errors.Is(err, utils.ErrNotFound) {
			utils.Error("User: could not read the account to check the caller's permissions", err)
			utils.HTTPError(w, "User Error", http.StatusInternalServerError, "UE005")
			return true
		}
		return false
	}
	return refuseStrongerAccount(w, req, target)
}
