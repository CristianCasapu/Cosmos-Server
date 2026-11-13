package user

// Fork addition: two rules about accounts.
//
//   - Logging in to an account that was invited but has not chosen a password
//     yet is answered like an unknown account or a wrong password, after the
//     same work (upstream answered "User not registered" at once, which told
//     anyone that the name exists).
//   - An administrator does not delete or demote the own account: another
//     administrator does that. However the accounts are changed, the one who
//     changes them stays an administrator, so the server cannot be left
//     without one (upstream let any account delete itself, without the
//     password being confirmed, and an administrator give up the own role).

import (
	"net/http"

	"github.com/azukaar/cosmos-server/src/utils"
	"golang.org/x/crypto/bcrypt"
)

// answerPendingLikeUnknown is the hook of UserLogin for an account without a
// password.
func answerPendingLikeUnknown(w http.ResponseWriter) {
	bcrypt.CompareHashAndPassword([]byte("$2a$14$4nzsVwEnR3.jEbMTME7kqeCo4gMgR/Tuk7ivNExvXjr73nKvLgHka"), []byte("dummyPassword"))
	utils.Error("UserLogin: User not registered", nil)
	utils.HTTPError(w, "User Logging Error", http.StatusInternalServerError, "UL001")
}

// isOwnAdminAccount reports whether the request comes from a session of an
// administrator (a role with permissions that need the password confirmed)
// and is about that very account.
func isOwnAdminAccount(req *http.Request, nickname string) bool {
	auth := utils.GetAuthContext(req)
	return auth.APIToken == nil && auth.Nickname != "" && auth.Nickname == nickname && utils.RoleHasSudoPermissions(auth.UserRole)
}

// refuseOwnAdminAccount is the hook of UserDelete and of the role change in
// UserEdit. It answers and returns true when the request must stop.
func refuseOwnAdminAccount(w http.ResponseWriter, req *http.Request, nickname string) bool {
	if !isOwnAdminAccount(req, nickname) {
		return false
	}
	utils.Error("User: an administrator cannot delete or demote the own account", nil)
	utils.HTTPError(w, "An administrator cannot delete or demote their own account. Another administrator can do it.", http.StatusConflict, "UD003")
	return true
}
