package user

import (
	"net/http"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: GET /cosmos/api/mfa never replaces a verified secret.
//
// Upstream's New2FA stores a new secret, marked as not verified, on every GET
// of a fully logged in user. For an account whose second factor was in use
// that silently switches it off: the old authenticator entry is dead, and the
// next login is asked for no code (or, with "force MFA", lets whoever has the
// password enrol a device of their own). A GET needs no intent: the Back
// button onto the setup page, a link, a prefetch or a page of another site
// sending the browser there is enough.
//
// So once a secret is verified, this GET changes nothing and says so. A new
// secret is issued again after the reset of the Users page (DELETE
// /cosmos/api/mfa: needs the users permission and a confirmed password, and
// ends the sessions of the account). Enrolment is untouched: without a
// verified secret the GET answers as before.

// refuseVerified2FAReplacement is the hook of New2FA, called for an account
// with a verified secret once the caller passed the existing checks.
func refuseVerified2FAReplacement(w http.ResponseWriter, nickname string) {
	utils.Warn("2FA: User " + nickname + " already has a verified key, it was not replaced")
	utils.HTTPError(w, "Two-factor authentication is already set up for this account. An administrator has to reset it before a new one can be set up.", http.StatusConflict, "2FA007")
}
