package authorizationserver

// Fork addition: two rules of the sign-in to applications (OpenID).
//
//   - An API token never signs in to an application. Upstream let any token,
//     whatever its permissions, through the authorization endpoint as its
//     owner: a token meant to read the logs got its owner's session in every
//     application set up for OpenID. The endpoint now refuses a request
//     authenticated by a token, with 403, and records it.
//   - The "role" claim says "admin" only for a role that holds the admin
//     permission. Upstream said "admin" for every role numbered from Admin
//     up, which a custom role with few permissions can be.

import (
	"net/http"

	"github.com/azukaar/cosmos-server/src/utils"
)

// refuseTokenSignIn is the hook of authEndpoint. It answers and returns true
// when the request comes with an API token.
func refuseTokenSignIn(rw http.ResponseWriter, req *http.Request) bool {
	if utils.GetAuthContext(req).APIToken == nil {
		return false
	}
	utils.RecordSecurity(req, utils.SecAccessDenied, "", "permission", utils.PermissionLabel(utils.PERM_LOGIN), "what", "application sign-in with an API token")
	utils.Error("OpenID: an API token cannot sign in to an application", nil)
	utils.HTTPError(rw, "An API token cannot be used to sign in to an application. Sign in with your account.", http.StatusForbidden, "OA010")
	return true
}

// roleClaim is the "role" claim of an account: admin only when its role
// holds the admin permission.
func roleClaim(role utils.Role) string {
	for _, p := range utils.GetRolePermissions(role) {
		if p == utils.PERM_ADMIN {
			return "admin"
		}
	}
	return "user"
}
