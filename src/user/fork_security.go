package user

// Fork addition: what the login, account and session code reports to the
// security record (utils/fork_security.go, package security). The hooks in
// upstream's files are one line each; whatever needs more than a line is
// here.

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/golang-jwt/jwt"

	"github.com/azukaar/cosmos-server/src/utils"
)

// recordLoginFailure records a refused login. The caller is told the same
// thing whatever the reason; the record keeps the reason, and the name that
// was tried if it looks like one.
func recordLoginFailure(req *http.Request, nickname string, reason string) {
	target := ""
	if nickname != "" {
		target = utils.SecurityAttemptedName(nickname)
	}
	utils.RecordSecurity(req, utils.SecLoginFailed, target, "reason", reason)
}

// recordLogin records a login with the right password, and whether a
// two-factor code is still to come.
func recordLogin(req *http.Request, user utils.User) {
	mfa := "none"
	if user.MFAKey != "" && user.Was2FAVerified {
		mfa = "pending"
	} else if utils.GetMainConfig().RequireMFA {
		mfa = "setup"
	}
	utils.RecordSecurityAs(req, user.Nickname, utils.SecLoginSuccess, "", "mfa", mfa)
}

// recordMFAFailure records a wrong two-factor code, and the lock-out when
// this was the code that closed the account's code check.
func recordMFAFailure(req *http.Request, nickname string) {
	utils.RecordSecurity(req, utils.SecMFAFailed, "", "reason", "wrong_code")
	if mfaAttempts.locked(nickname) {
		utils.RecordSecurity(req, utils.SecMFALocked, nickname)
	}
}

// locked reports whether the code check of an account is closed right now.
func (l *mfaAttemptLimiter) locked(nickname string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	account := l.accounts[nickname]
	return account != nil && l.now().Before(account.lockedUntil)
}

// sessionNickname reads whose session a request carries, without changing
// anything: the cookie must be signed by this server and not expired. The
// logout route is not behind the token check (it has to work whatever the
// state of the cookie), so nobody else has looked at the cookie there.
func sessionNickname(req *http.Request) string {
	cookie, err := req.Cookie("jwttoken")
	if err != nil || cookie.Value == "" {
		return ""
	}
	key, err := jwt.ParseEdPublicKeyFromPEM([]byte(utils.GetPublicAuthKey()))
	if err != nil {
		return ""
	}
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(cookie.Value, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return key, nil
	})
	if err != nil || !token.Valid {
		return ""
	}
	nickname, _ := claims["nickname"].(string)
	return nickname
}

// recordLogout records a logout, when the request carries a session of this
// server: a logout without one is not an event.
func recordLogout(req *http.Request) {
	defer func() { recover() }() // a logout must work whatever happens here
	if nickname := sessionNickname(req); nickname != "" {
		utils.RecordSecurityAs(req, nickname, utils.SecLogout, "")
	}
}

// recordRegistration records what the registration form did for an account:
// an invited user set a first password, or a password was replaced through a
// reset link.
func recordRegistration(req *http.Request, nickname string, hadPassword bool) {
	if hadPassword {
		utils.RecordSecurityAs(req, nickname, utils.SecPasswordChanged, "", "via", "reset_link")
		return
	}
	utils.RecordSecurityAs(req, nickname, utils.SecUserInviteUsed, "")
}

// recordPasswordResetRequest records that the "forgot password" form was
// sent: for an account (a link is mailed) or for a name and email that match
// none. The form is open to anyone, so both are incidents.
func recordPasswordResetRequest(req *http.Request, nickname string, user utils.User, lookupErr error) {
	switch {
	case lookupErr == nil && user.Email != "":
		utils.RecordSecurity(req, utils.SecResetRequested, user.Nickname)
	case lookupErr == nil || errors.Is(lookupErr, utils.ErrNotFound):
		utils.RecordSecurity(req, utils.SecResetRejected, utils.SecurityAttemptedName(nickname))
	}
}

// auditLink records the link an administrator made for an account: an
// invitation (form type 2) or a password reset.
func auditLink(req *http.Request, nickname string, formType string) {
	if formType == "2" {
		utils.AuditSecurity(req, utils.SecUserInviteCreated, nickname)
		return
	}
	utils.AuditSecurity(req, utils.SecUserResetCreated, nickname)
}

// auditUserEdit records a change to an account: a new role, else a new
// email. Neither the old nor the new email is kept.
func auditUserEdit(req *http.Request, nickname string, toSet map[string]interface{}) {
	if role, changed := toSet["Role"].(utils.Role); changed {
		utils.AuditSecurity(req, utils.SecUserRoleChanged, nickname, "role", roleLabel(role))
		return
	}
	if _, changed := toSet["Email"]; changed {
		utils.AuditSecurity(req, utils.SecUserEdited, nickname, "changed", "email")
	}
}

// auditUserDeletion records the deletion of an account, and whether the user
// deleted the own one.
func auditUserDeletion(req *http.Request, nickname string) {
	self := "no"
	if nickname != "" && nickname == utils.GetAuthContext(req).Nickname {
		self = "yes"
	}
	utils.AuditSecurity(req, utils.SecUserDeleted, nickname, "self", self)
}

// roleLabel names a role for the record: its name from the settings, else
// its number.
func roleLabel(role utils.Role) string {
	if config, ok := utils.GetRoles()[role]; ok && config.Name != "" {
		return config.Name
	}
	return strconv.Itoa(int(role))
}
