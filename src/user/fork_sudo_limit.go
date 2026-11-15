package user

// Fork addition: wrong passwords at the password confirmation (sudo) are
// limited per account, like wrong two-factor codes (fork_mfa_limit.go, same
// limiter and numbers: 5 wrong passwords within 10 minutes close the
// confirmation of that account for 15 minutes; the right password before
// that clears the count). Upstream had no limit there, and the login
// shield exempts an administrator's session.
//
// The account is the one of the session, so only an existing account that
// is already logged in ever gets a counter.

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

var sudoAttempts = newMFAAttemptLimiter(time.Now)

// sudoRetryMessage is what a locked out user is told, in whole minutes rounded up
func sudoRetryMessage(wait time.Duration) string {
	minutes := int((wait + time.Minute - 1) / time.Minute)
	if minutes <= 1 {
		return "Too many wrong passwords. Try again in 1 minute."
	}
	return fmt.Sprintf("Too many wrong passwords. Try again in %d minutes.", minutes)
}

// sudoAttemptRefused is the hook of UserSudo, before the password is
// compared. It returns true, with the answer written (429), when the account
// gave too many wrong passwords.
func sudoAttemptRefused(w http.ResponseWriter, nickname string) bool {
	allowed, wait := sudoAttempts.begin(nickname)
	if allowed {
		return false
	}
	seconds := int((wait + time.Second - 1) / time.Second)
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	utils.Error("UserSudo: User "+nickname+" gave too many wrong passwords, the confirmation is closed for this account", nil)
	utils.HTTPError(w, sudoRetryMessage(wait), http.StatusTooManyRequests, "UL010")
	return true
}

// sudoSucceeded is the hook of UserSudo once the password was right
func sudoSucceeded(nickname string) {
	sudoAttempts.succeeded(nickname)
}
