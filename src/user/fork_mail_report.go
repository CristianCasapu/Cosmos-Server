package user

// Fork addition: a mail that could not be sent is reported as not sent.
//
// Two places sent a mail and went on as if it had arrived:
//
//   - the invitation and the administrator's password reset link
//     (resend.go): the answer said "emailWasSent" whatever the mail server
//     had answered, so the dialog told the administrator the user had been
//     written to. It now says the mail was not sent, and why.
//
//   - the "forgot password" form (password_reset.go), which anyone can call:
//     it answered an error for an unknown account, another one when the mail
//     failed and OK otherwise, which told a stranger whether a nickname and
//     an email belong to an account. It now gives one answer in all three
//     cases and does the work after answering, so the time it takes says
//     nothing either. A mail that fails is reported to the administrators
//     (log and notification, at most one per account and hour), not to the
//     caller. An account is sent at most one link a minute.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// maxMailReason bounds the reason shown for a mail that was not sent.
const maxMailReason = 300

// resetFailureNoticeEvery is how often the administrators are told that the
// reset mail of one account cannot be sent: the form is open to anyone, and
// must not be a way to fill their notifications.
const resetFailureNoticeEvery = time.Hour

// resetMailEvery is how often one account is sent a reset link: the work is
// done after the answer, so nothing else holds back a caller who repeats the
// request, and each link replaces the one before.
const resetMailEvery = time.Minute

// What the reset needs from the rest of the server; the tests replace these.
var (
	sendResetMail    = SendPasswordEmail
	saveResetKey     = utils.UpdateUser
	reportToAdmins   = utils.MajorError
	recordResetEvent = utils.TriggerEvent
	resetNow         = time.Now
	inBackground     = func(work func()) {
		go func() {
			// nothing recovers a panic outside the request's own goroutine: it would end the server
			defer func() {
				if failure := recover(); failure != nil {
					utils.Error("PasswordReset: sending the reset link failed unexpectedly", fmt.Errorf("%v", failure))
				}
			}()
			work()
		}()
	}
)

var (
	resetLock          sync.Mutex
	resetFailureNotice = map[string]time.Time{}
	resetMailSent      = map[string]time.Time{}
)

// resetDue reports whether a reset mail may go to this account now, and
// counts it when it may: one per account and resetMailEvery.
func resetDue(nickname string) bool {
	resetLock.Lock()
	defer resetLock.Unlock()
	now := resetNow()
	if last, sent := resetMailSent[nickname]; sent && now.Sub(last) < resetMailEvery {
		return false
	}
	resetMailSent[nickname] = now
	return true
}

// mailOutcome turns the result of sending a mail into what the caller is
// told: whether it was sent, and if not, why (one line, bounded).
func mailOutcome(err error) (bool, string) {
	if err == nil {
		return true, ""
	}
	reason := strings.Join(strings.Fields(err.Error()), " ")
	if len(reason) > maxMailReason {
		reason = strings.ToValidUTF8(reason[:maxMailReason], "") + "…"
	}
	if reason == "" {
		reason = "unknown error"
	}
	return false, reason
}

// answerPasswordReset answers the "forgot password" form: always the same,
// whether the account exists or not and whether the mail can be sent or not.
// The mail is sent after the answer. It reports true: the request is done.
func answerPasswordReset(w http.ResponseWriter, nickname string, user utils.User, lookupErr error) bool {
	switch {
	case lookupErr == nil && user.Email != "":
		if resetDue(user.Nickname) {
			inBackground(func() { deliverPasswordReset(nickname, user) })
		}
	case lookupErr != nil && !errors.Is(lookupErr, utils.ErrNotFound):
		utils.Error("PasswordReset: Error while finding user", lookupErr)
	default:
		utils.Debug("PasswordReset: no account with this nickname and email")
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
	})
	return true
}

// deliverPasswordReset makes a reset link for the account and mails it. A
// failure goes to the administrators.
func deliverPasswordReset(nickname string, user utils.User) {
	registerKey := utils.GenerateRandomString(48)

	err := saveResetKey(nickname, map[string]interface{}{
		"RegisterKeyExp": resetNow().Add(time.Hour * 24 * 7),
		"RegisterKey":    registerKey,
	})
	if err != nil {
		reportResetFailure(user.Nickname, "The password reset link for "+user.Nickname+" could not be saved", err)
		return
	}

	url := utils.GetServerURL("") + ("cosmos-ui/register?t=1&nickname=" + user.Nickname + "&key=" + registerKey)

	if err := sendResetMail(user.Nickname, user.Email, url); err != nil {
		_, reason := mailOutcome(err)
		reportResetFailure(user.Nickname, "The password reset email for "+user.Nickname+" could not be sent", errors.New(reason))
		return
	}

	recordResetEvent(
		"cosmos.user.passwordreset",
		"Password reset sent",
		"success",
		"",
		map[string]interface{}{
			"nickname": user.Nickname,
		})
}

// reportResetFailure tells the administrators, once per account and hour;
// the log gets every failure.
func reportResetFailure(nickname string, message string, err error) {
	resetLock.Lock()
	last, told := resetFailureNotice[nickname]
	now := resetNow()
	quiet := told && now.Sub(last) < resetFailureNoticeEvery
	if !quiet {
		resetFailureNotice[nickname] = now
	}
	resetLock.Unlock()

	if quiet {
		utils.Error("PasswordReset: "+message, err)
		return
	}
	reportToAdmins(message, err)
}
