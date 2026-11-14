package user

// Fork addition: how long a session and a password confirmation last, and
// what ends every session of an account.
//
//   - A session has a start: the moment the password was given. The token
//     carries it (claim "sessionStart") and every re-issue copies it, so
//     renewing a token never makes a session younger. A session older than
//     MaxSessionAge is refused; so is a token without a start (one issued
//     before this rule existed) and one whose session began before the account
//     was created (the token of a deleted account under a name used again).
//   - The password confirmation ("sudo") ends SudoDuration after the password
//     was confirmed. Upstream re-issued the token in the second hour with two
//     new hours, so one request an hour kept it for ever; now a re-issue
//     copies the end unchanged.
//   - Ending the sessions of an account is upstream's own mechanism: the
//     account's password cycle is raised, and RefreshUserToken refuses every
//     token that carries another one. Upstream does that when a password is
//     set through a link and when the second factor is reset; here it also
//     happens at logout (the session of every device ends, not only the cookie
//     of this browser) and when an administrator makes a password-reset link.
//     The cycle is in the database, so it holds across a restart.
//
// RefreshUserToken is the only function that accepts a session (the API, the
// websockets, the OpenID provider and the routes of applications all go
// through it), so these rules are applied there and nowhere else.

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sync"
	"time"

	jwt "github.com/golang-jwt/jwt/v4"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	// MaxSessionAge is the longest a session lasts, counted from the login
	// (the password), whatever its use and however often its token was
	// re-issued. After it the next request is sent to the login page.
	MaxSessionAge = 30 * 24 * time.Hour

	// SudoDuration is how long a confirmed password counts, counted from the
	// confirmation and not extended by anything.
	SudoDuration = 2 * time.Hour

	// sessionIdleLimit is upstream's lifetime of one token: a session that
	// sends no request for this long ends, because nothing re-issued it.
	sessionIdleLimit = 14 * 24 * time.Hour

	sessionStartClaim = "sessionStart"
	sudoUntilClaim    = "sudo-until"
)

// sessionClock is the clock of these rules; tests set it.
var sessionClock = time.Now

// What RefreshUserToken answers for a session these rules refuse. The texts
// are matched by utils.RecordSecuritySessionError.
var (
	errSessionNoStart       = errors.New("Session has no start, token is too old")
	errSessionTooOld        = errors.New("Session is older than the maximum session age")
	errSessionBeforeAccount = errors.New("Session started before the account was created")
)

// sessionLife is what a token says about its session.
type sessionLife struct {
	start     int64 // when the password was given (unix seconds)
	sudoUntil int64 // until when the password counts as confirmed, 0: not confirmed
}

// sudoActive reports whether the password counts as confirmed right now. An
// end further away than SudoDuration cannot have been issued by these rules
// (the clock was set back since): it does not count.
func (l sessionLife) sudoActive() bool {
	now := sessionClock().Unix()
	return l.sudoUntil > now && l.sudoUntil <= now+int64(SudoDuration/time.Second)
}

// cookieExpiry is when the token and its cookie end: the idle limit from now,
// but never past the maximum age of the session.
func (l sessionLife) cookieExpiry() time.Time {
	idle := sessionClock().Add(sessionIdleLimit)
	oldest := time.Unix(l.start, 0).Add(MaxSessionAge)
	if oldest.Before(idle) {
		return oldest
	}
	return idle
}

// sessionLifeOf is the hook of RefreshUserToken, asked once the signature, the
// account and its password cycle have been checked: the session of the token,
// or why it is refused.
func sessionLifeOf(claims jwt.MapClaims, account utils.User) (sessionLife, error) {
	retireRevokedSessionsFile()

	startClaim, ok := claims[sessionStartClaim].(float64)
	if !ok || startClaim <= 0 {
		return sessionLife{}, errSessionNoStart
	}
	life := sessionLife{start: int64(startClaim)}

	if sessionClock().Unix()-life.start >= int64(MaxSessionAge/time.Second) {
		return sessionLife{}, errSessionTooOld
	}
	if !account.CreatedAt.IsZero() && life.start < account.CreatedAt.Unix() {
		return sessionLife{}, errSessionBeforeAccount
	}

	if sudoUntil, ok := claims[sudoUntilClaim].(float64); ok && sudoUntil > 0 {
		life.sudoUntil = int64(sudoUntil)
	}
	return life, nil
}

// ---- issuing ---------------------------------------------------------------

type sessionIntent int

const (
	sessionAsBefore          sessionIntent = iota // no hook: carry what the request's own session says
	sessionNew                                    // the password was just given: a new session
	sessionPasswordConfirmed                      // the password was just confirmed inside a session
	sessionCarried                                // a re-issue: the checked token's values, unchanged
)

type sessionMark struct {
	intent sessionIntent
	life   sessionLife
}

type sessionMarkKey struct{}

func markSession(req *http.Request, mark sessionMark) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), sessionMarkKey{}, mark))
}

// NewSession is the hook of a login (and of the sign-in on an application's
// hostname): the token SendUserToken writes for this request starts a session.
func NewSession(req *http.Request) *http.Request {
	return markSession(req, sessionMark{intent: sessionNew})
}

// passwordConfirmed is the hook of UserSudo: the token SendUserToken writes
// for this request keeps the session and confirms the password from now.
func passwordConfirmed(req *http.Request) *http.Request {
	return markSession(req, sessionMark{intent: sessionPasswordConfirmed})
}

// carriedSession is the hook of the re-issue in RefreshUserToken: the token
// SendUserToken writes for this request continues the checked one.
func carriedSession(req *http.Request, life sessionLife) *http.Request {
	return markSession(req, sessionMark{intent: sessionCarried, life: life})
}

// sessionLifeFor is the hook of SendUserToken: the start and the end of the
// password confirmation the new token carries.
//
// A caller without a hook (the second factor; anything upstream adds later)
// continues the session of the request's own cookie when that is a valid
// token of the same account, and starts a new one otherwise. Such a caller
// can keep a confirmation that is still running, never start or extend one.
func sessionLifeFor(req *http.Request, account utils.User, sudoed bool) sessionLife {
	now := sessionClock().Unix()
	mark, _ := req.Context().Value(sessionMarkKey{}).(sessionMark)

	var life sessionLife
	switch mark.intent {
	case sessionNew:
		return sessionLife{start: now}
	case sessionCarried:
		life = mark.life
	default:
		presented, ok := presentedSession(req)
		if ok && presented.nickname == account.Nickname && presented.cycle == account.PasswordCycle {
			life = presented.life
		} else {
			life = sessionLife{start: now}
		}
	}

	switch {
	case mark.intent == sessionPasswordConfirmed:
		life.sudoUntil = now + int64(SudoDuration/time.Second)
	case !sudoed || !life.sudoActive():
		life.sudoUntil = 0
	}
	return life
}

// presented is a session cookie this server signed and that has not expired.
type presented struct {
	nickname string
	cycle    int
	life     sessionLife
}

// presentedSession reads the session cookie of a request without accepting
// anything: the signature and the expiry are checked, the account is not.
func presentedSession(req *http.Request) (presented, bool) {
	cookie, err := req.Cookie("jwttoken")
	if err != nil || cookie.Value == "" || len(cookie.Value) > 8192 {
		return presented{}, false
	}
	key, err := jwt.ParseEdPublicKeyFromPEM([]byte(utils.GetPublicAuthKey()))
	if err != nil {
		return presented{}, false
	}
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(cookie.Value, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return key, nil
	})
	if err != nil || !token.Valid {
		return presented{}, false
	}

	nickname, okName := claims["nickname"].(string)
	cycle, okCycle := claims["passwordCycle"].(float64)
	start, okStart := claims[sessionStartClaim].(float64)
	if !okName || nickname == "" || !okCycle || !okStart || start <= 0 {
		return presented{}, false
	}
	found := presented{nickname: nickname, cycle: int(cycle), life: sessionLife{start: int64(start)}}
	if sudoUntil, ok := claims[sudoUntilClaim].(float64); ok && sudoUntil > 0 {
		found.life.sudoUntil = int64(sudoUntil)
	}
	return found, true
}

// ---- ending the sessions of an account ---------------------------------------

// endSessionsOf refuses, from now on, every token of the account issued so far.
func endSessionsOf(account utils.User) error {
	return utils.UpdateUser(account.Nickname, map[string]interface{}{
		"PasswordCycle": account.PasswordCycle + 1,
	})
}

// endSessionsAtLogout is the hook of UserLogout. The request must carry a
// session that is valid right now: a cookie that was made up, has expired or
// belongs to a session already ended changes nothing, so a stale cookie
// cannot be used to log an account out. False means the answer has been
// written: the sessions could not be ended.
func endSessionsAtLogout(w http.ResponseWriter, req *http.Request) bool {
	session, ok := presentedSession(req)
	if !ok {
		return true
	}
	account, err := utils.GetUser(session.nickname)
	if errors.Is(err, utils.ErrNotFound) {
		return true
	}
	if err == nil {
		if account.PasswordCycle != session.cycle {
			return true
		}
		if !account.CreatedAt.IsZero() && session.life.start < account.CreatedAt.Unix() {
			return true
		}
		if sessionClock().Unix()-session.life.start >= int64(MaxSessionAge/time.Second) {
			return true
		}
		err = endSessionsOf(account)
	}

	if err != nil {
		utils.Error("UserLogout: the sessions of the account could not be ended", err)
		logOutUser(w, req)
		utils.HTTPError(w, "Logged out in this browser, but the sessions on other devices could not be ended", http.StatusInternalServerError, "UL003")
		return false
	}
	return true
}

// withSessionsEnded is the hook of UserResendInviteLink: when an administrator
// makes a password-reset link for an account, the same update that stores the
// link ends the account's sessions. An invitation (form type 2) is for an
// account that has none.
func withSessionsEnded(account utils.User, formType string, fields map[string]interface{}) map[string]interface{} {
	if formType != "2" {
		fields["PasswordCycle"] = account.PasswordCycle + 1
	}
	return fields
}

// ---- the list this replaces ----------------------------------------------------

var retireRevokedSessions sync.Once

// retireRevokedSessionsFile removes revoked-sessions.json, the list of single
// logged-out tokens an earlier fork release kept next to the configuration.
// Logging out now ends every token of the account, so nothing reads it.
func retireRevokedSessionsFile() {
	retireRevokedSessions.Do(func() {
		if utils.CONFIGFOLDER != "" {
			os.Remove(utils.CONFIGFOLDER + "revoked-sessions.json")
		}
	})
}
