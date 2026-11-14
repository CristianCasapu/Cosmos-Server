package user

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v4"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: tests of fork_session_life.go. The store and key helpers are
// in fork_mfa_limit_test.go, the account helpers in fork_security_test.go.

const lifeHost = "cosmos.example.com"

// lifeSetup is a server named cosmos.example.com with an empty user table and
// a clock the test moves.
func lifeSetup(t *testing.T) *fakeClock {
	t.Helper()
	mfaTestSetup(t)
	config := utils.GetBaseMainConfig()
	config.HTTPConfig.Hostname = lifeHost
	config.HTTPConfig.HTTPSCertificateMode = "SELFSIGNED"
	utils.LoadBaseMainConfig(config)

	clock := &fakeClock{t: time.Now().Truncate(time.Second)}
	previous := sessionClock
	sessionClock = clock.now
	t.Cleanup(func() { sessionClock = previous })
	return clock
}

func lifeRequest(method string, path string, cookie *http.Cookie) *http.Request {
	req := httptest.NewRequest(method, "https://"+lifeHost+path, nil)
	req.Host = lifeHost
	if cookie != nil {
		req.AddCookie(cookie)
	}
	return req
}

func sessionCookieOf(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "jwttoken" && cookie.Value != "" {
			return cookie
		}
	}
	return nil
}

// loginAs is what a login does once the password was right.
func loginAs(t *testing.T, nickname string) *http.Cookie {
	t.Helper()
	account, err := utils.GetUser(nickname)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	SendUserToken(w, NewSession(lifeRequest("POST", "/cosmos/api/login", nil)), account, true, false)
	cookie := sessionCookieOf(t, w)
	if cookie == nil {
		t.Fatal("no session cookie was issued")
	}
	return cookie
}

// refresh is one request with a session cookie: who it is, whether the
// password counts as confirmed, the error, and the cookie a re-issue sent.
func refresh(t *testing.T, cookie *http.Cookie) (utils.User, bool, error, *http.Cookie) {
	t.Helper()
	w := httptest.NewRecorder()
	_, sudo, account, err := RefreshUserToken(w, lifeRequest("GET", "/cosmos/api/me", cookie))
	return account, sudo, err, sessionCookieOf(t, w)
}

func claimsOf(t *testing.T, cookie *http.Cookie) jwt.MapClaims {
	t.Helper()
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(cookie.Value, claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

// signedSession is a token of this server with the claims the test wants.
func signedSession(t *testing.T, account utils.User, change func(jwt.MapClaims)) *http.Cookie {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"exp": now.Add(24 * time.Hour).Unix(), "iat": now.Unix(), "nbf": now.Unix(),
		"nickname": account.Nickname, "passwordCycle": account.PasswordCycle,
		"mfaDone": true, "forDomain": lifeHost, "permissions": []interface{}{},
		sessionStartClaim: sessionClock().Unix(),
	}
	perms := []interface{}{}
	for _, p := range utils.GetRolePermissions(account.Role) {
		perms = append(perms, int(p))
	}
	claims["permissions"] = perms
	change(claims)
	key, err := jwt.ParseEdPrivateKeyFromPEM([]byte(utils.GetPrivateAuthKey()))
	if err != nil {
		t.Fatal(err)
	}
	value, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: "jwttoken", Value: value}
}

func logoutWith(cookie *http.Cookie) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	UserLogout(w, lifeRequest("GET", "/cosmos/api/logout", cookie))
	return w
}

func mustWork(t *testing.T, what string, cookie *http.Cookie, nickname string) {
	t.Helper()
	if account, _, err, _ := refresh(t, cookie); err != nil || account.Nickname != nickname {
		t.Fatalf("%s: the session is refused: %v", what, err)
	}
}

func mustBeRefused(t *testing.T, what string, cookie *http.Cookie, reason string) {
	t.Helper()
	account, _, err, _ := refresh(t, cookie)
	if err == nil || account.Nickname != "" {
		t.Fatalf("%s: the session still works (user %q)", what, account.Nickname)
	}
	if !strings.Contains(err.Error(), reason) {
		t.Fatalf("%s: refused for %q, want %q", what, err.Error(), reason)
	}
}

func TestLogoutEndsEverySessionOfTheAccountAndNoOther(t *testing.T) {
	clock := lifeSetup(t)
	securityTestUser(t, "alice", utils.USER)
	securityTestUser(t, "bob", utils.USER)

	laptop := loginAs(t, "alice")
	phone := loginAs(t, "alice")
	bob := loginAs(t, "bob")

	// the laptop's token is re-issued; somebody kept the one from before
	alice, _ := utils.GetUser("alice")
	clock.advance(36 * time.Hour)
	old := signedSession(t, alice, func(c jwt.MapClaims) {
		c["iat"] = time.Now().Add(-25 * time.Hour).Unix()
		c[sessionStartClaim] = claimsOf(t, laptop)[sessionStartClaim]
	})
	_, _, err, renewed := refresh(t, old)
	if err != nil || renewed == nil {
		t.Fatalf("a day-old token was not re-issued: %v", err)
	}
	for name, cookie := range map[string]*http.Cookie{"laptop": laptop, "kept copy": old, "re-issued": renewed, "phone": phone} {
		mustWork(t, name+" before logout", cookie, "alice")
	}

	if w := logoutWith(renewed); w.Code != http.StatusOK {
		t.Fatalf("logout: %d %s", w.Code, w.Body.String())
	}

	for name, cookie := range map[string]*http.Cookie{"laptop": laptop, "kept copy": old, "re-issued": renewed, "phone": phone} {
		mustBeRefused(t, name+" after logout", cookie, "Password cycle changed")
	}
	mustWork(t, "another account after alice's logout", bob, "bob")

	// across a restart: the rule is in the database
	utils.CloseStore()
	if err := utils.InitStore(); err != nil {
		t.Fatal(err)
	}
	mustBeRefused(t, "phone after logout and restart", phone, "Password cycle changed")
	mustWork(t, "another account after the restart", bob, "bob")

	// a new login is a new session
	mustWork(t, "a login after the logout", loginAs(t, "alice"), "alice")
}

func TestLogoutNeedsASessionThatIsValidNow(t *testing.T) {
	lifeSetup(t)
	securityTestUser(t, "alice", utils.USER)
	session := loginAs(t, "alice")
	alice, _ := utils.GetUser("alice")

	tampered := *session
	parts := strings.Split(session.Value, ".")
	tampered.Value = parts[0] + "." + parts[1] + "." + strings.Repeat("A", len(parts[2]))
	stale := signedSession(t, alice, func(c jwt.MapClaims) { c["passwordCycle"] = alice.PasswordCycle - 1 })
	expired := signedSession(t, alice, func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Minute).Unix() })
	tooOld := signedSession(t, alice, func(c jwt.MapClaims) {
		c[sessionStartClaim] = sessionClock().Add(-MaxSessionAge - time.Hour).Unix()
	})
	legacy := signedSession(t, alice, func(c jwt.MapClaims) { delete(c, sessionStartClaim) })

	for name, cookie := range map[string]*http.Cookie{
		"none": nil, "garbage": {Name: "jwttoken", Value: "a.b.c"}, "tampered": &tampered,
		"of an ended session": stale, "expired": expired, "too old": tooOld, "without a start": legacy,
	} {
		if w := logoutWith(cookie); w.Code != http.StatusOK {
			t.Errorf("logout with a cookie that is %s: %d, want 200", name, w.Code)
		}
		if after, _ := utils.GetUser("alice"); after.PasswordCycle != alice.PasswordCycle {
			t.Fatalf("a logout with a cookie that is %s ended alice's sessions", name)
		}
	}
	mustWork(t, "after the logouts that were not hers", session, "alice")
}

func TestLogoutSaysSoWhenTheSessionsCannotBeEnded(t *testing.T) {
	lifeSetup(t)
	securityTestUser(t, "alice", utils.USER)
	session := loginAs(t, "alice")

	utils.CloseStore() // the database does not answer
	w := logoutWith(session)
	if err := utils.InitStore(); err != nil {
		t.Fatal(err)
	}

	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "UL003") {
		t.Fatalf("logout while the sessions cannot be ended: %d %s, want 500 UL003", w.Code, w.Body.String())
	}
	cleared := 0
	for _, cookie := range w.Result().Cookies() {
		if cookie.Value == "" || cookie.Value == "{}" {
			cleared++
		}
	}
	if cleared != 2 {
		t.Errorf("the browser's cookies are not cleared: %v", w.Result().Cookies())
	}
}

func TestWhatElseEndsTheSessionsOfAnAccount(t *testing.T) {
	lifeSetup(t)
	securityTestUser(t, "root", utils.ADMIN)
	admin := adminSession("root")

	t.Run("an administrator's password reset link", func(t *testing.T) {
		securityTestUser(t, "carol", utils.USER)
		session := loginAs(t, "carol")
		w := httptest.NewRecorder()
		UserResendInviteLink(w, securityPost("/cosmos/api/invite", `{"nickname":"carol","formType":"1"}`, admin))
		if w.Code != http.StatusOK {
			t.Fatalf("reset link: %d %s", w.Code, w.Body.String())
		}
		mustBeRefused(t, "after the reset link", session, "Password cycle changed")
		if strings.Contains(w.Body.String(), "PasswordCycle") {
			t.Error("the answer names the password cycle")
		}
	})

	t.Run("not an invitation", func(t *testing.T) {
		securityTestUser(t, "dave", utils.USER)
		session := loginAs(t, "dave")
		w := httptest.NewRecorder()
		UserResendInviteLink(w, securityPost("/cosmos/api/invite", `{"nickname":"dave","formType":"2"}`, admin))
		if w.Code != http.StatusOK {
			t.Fatalf("invitation: %d", w.Code)
		}
		mustWork(t, "after an invitation link", session, "dave")
	})

	t.Run("a second-factor reset", func(t *testing.T) {
		securityTestUser(t, "erin", utils.USER)
		session := loginAs(t, "erin")
		req := httptest.NewRequest("DELETE", "http://localhost/cosmos/api/mfa", strings.NewReader(`{"nickname":"erin"}`))
		w := httptest.NewRecorder()
		Delete2FA(w, req.WithContext(securityPost("/", "", admin).Context()))
		if w.Code != http.StatusOK {
			t.Fatalf("2FA reset: %d %s", w.Code, w.Body.String())
		}
		mustBeRefused(t, "after the second-factor reset", session, "Password cycle changed")
	})

	t.Run("a password set through a link", func(t *testing.T) {
		securityTestUser(t, "frank", utils.USER)
		session := loginAs(t, "frank")
		if err := utils.UpdateUser("frank", map[string]interface{}{
			"RegisterKey": "abcdefghijklmnopqrstuvwx", "RegisterKeyExp": time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		UserRegister(w, securityPost("/cosmos/api/register",
			`{"nickname":"frank","password":"New-password-123!","registerKey":"abcdefghijklmnopqrstuvwx"}`, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("register: %d %s", w.Code, w.Body.String())
		}
		mustBeRefused(t, "after the password change", session, "Password cycle changed")
	})

	t.Run("deleting the account, also when the name is used again", func(t *testing.T) {
		clock := &fakeClock{t: sessionClock()}
		sessionClock = clock.now // restored by lifeSetup's cleanup
		if err := utils.CreateUser(utils.User{Nickname: "gina", Password: "x", Role: utils.USER, PasswordCycle: 1, CreatedAt: clock.now().Add(-time.Hour)}); err != nil {
			t.Fatal(err)
		}
		session := loginAs(t, "gina")
		mustWork(t, "before the deletion", session, "gina")
		if err := utils.DeleteUser("gina"); err != nil {
			t.Fatal(err)
		}
		mustBeRefused(t, "after the deletion", session, "User not found")

		// the same name, the same cycle number, created later
		clock.advance(time.Minute)
		if err := utils.CreateUser(utils.User{Nickname: "gina", Password: "x", Role: utils.USER, PasswordCycle: 1, CreatedAt: clock.now()}); err != nil {
			t.Fatal(err)
		}
		mustBeRefused(t, "of the deleted account, the name used again", session, "before the account was created")
		mustWork(t, "a login to the new account", loginAs(t, "gina"), "gina")
	})
}

func TestTheMaximumSessionAgeIsAbsolute(t *testing.T) {
	clock := lifeSetup(t)
	securityTestUser(t, "alice", utils.USER)
	alice, _ := utils.GetUser("alice")
	session := loginAs(t, "alice")
	start := int64(claimsOf(t, session)[sessionStartClaim].(float64))
	if start != clock.now().Unix() {
		t.Fatalf("the session starts at %d, the login was at %d", start, clock.now().Unix())
	}
	if !session.Expires.Equal(clock.now().Add(sessionIdleLimit)) {
		t.Errorf("a new cookie ends %s, want the idle limit %s", session.Expires, clock.now().Add(sessionIdleLimit))
	}

	// used every few days: each re-issue continues the same session
	current := session
	for day := 5; day <= 25; day += 5 {
		clock.advance(5 * 24 * time.Hour)
		due := signedSession(t, alice, func(c jwt.MapClaims) {
			c["iat"] = time.Now().Add(-25 * time.Hour).Unix()
			c[sessionStartClaim] = claimsOf(t, current)[sessionStartClaim]
		})
		account, _, err, renewed := refresh(t, due)
		if err != nil || account.Nickname != "alice" || renewed == nil {
			t.Fatalf("day %d: not accepted and re-issued: %v", day, err)
		}
		if got := int64(claimsOf(t, renewed)[sessionStartClaim].(float64)); got != start {
			t.Fatalf("day %d: the re-issued token starts at %d, the session started at %d", day, got, start)
		}
		current = renewed
	}

	// day 25: the cookie ends with the session, not 14 days later
	limit := time.Unix(start, 0).Add(MaxSessionAge)
	if !current.Expires.Equal(limit) {
		t.Errorf("the cookie re-issued on day 25 ends %s, want the end of the session %s", current.Expires, limit)
	}
	if exp := int64(claimsOf(t, current)["exp"].(float64)); exp != limit.Unix() {
		t.Errorf("the token re-issued on day 25 ends at %d, want %d", exp, limit.Unix())
	}

	clock.advance(5*24*time.Hour - time.Second)
	fresh := signedSession(t, alice, func(c jwt.MapClaims) { c[sessionStartClaim] = start })
	mustWork(t, "one second before the maximum age", fresh, "alice")

	clock.advance(time.Second)
	mustBeRefused(t, "at the maximum age, with a token issued a moment ago", fresh, "older than the maximum session age")
	w := httptest.NewRecorder()
	RefreshUserToken(w, lifeRequest("GET", "/cosmos/api/me", fresh))
	if cleared := w.Result().Cookies(); len(cleared) != 2 || cleared[0].Value != "" {
		t.Errorf("the too old session's cookies are not cleared: %v", cleared)
	}

	mustWork(t, "a new login after the maximum age", loginAs(t, "alice"), "alice")
}

func TestATokenWithoutASessionStartIsRefused(t *testing.T) {
	lifeSetup(t)
	securityTestUser(t, "alice", utils.USER)
	alice, _ := utils.GetUser("alice")
	for name, change := range map[string]func(jwt.MapClaims){
		"none":         func(c jwt.MapClaims) { delete(c, sessionStartClaim) },
		"zero":         func(c jwt.MapClaims) { c[sessionStartClaim] = 0 },
		"not a number": func(c jwt.MapClaims) { c[sessionStartClaim] = "1700000000" },
	} {
		mustBeRefused(t, "session start "+name, signedSession(t, alice, change), "Session has no start")
	}
}

func TestThePasswordConfirmationLastsTwoHoursAndIsNotRenewed(t *testing.T) {
	clock := lifeSetup(t)
	securityTestUser(t, "root", utils.ADMIN)
	root, _ := utils.GetUser("root")
	session := loginAs(t, "root")
	if _, sudo, _, _ := refresh(t, session); sudo {
		t.Fatal("a login confirms the password for admin actions")
	}

	// the password is confirmed 10 minutes into the session
	clock.advance(10 * time.Minute)
	confirmedAt := clock.now()
	req := httptest.NewRequest("POST", "https://"+lifeHost+"/cosmos/api/sudo", strings.NewReader(`{"password":"`+securityTestPassword+`"}`))
	req.Host = lifeHost
	req.AddCookie(session)
	auth := &utils.AuthContext{Nickname: "root", Role: utils.USER, UserRole: utils.ADMIN, Permissions: utils.GetRolePermissions(utils.ADMIN)}
	req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
	w := httptest.NewRecorder()
	UserSudo(w, req)
	confirmed := sessionCookieOf(t, w)
	if w.Code != http.StatusOK || confirmed == nil {
		t.Fatalf("sudo: %d %s", w.Code, w.Body.String())
	}
	claims := claimsOf(t, confirmed)
	until := confirmedAt.Add(SudoDuration).Unix()
	if got := int64(claims[sudoUntilClaim].(float64)); got != until {
		t.Fatalf("confirmed until %d, want two hours after the password: %d", got, until)
	}
	if got, want := claims[sessionStartClaim], claimsOf(t, session)[sessionStartClaim]; got != want {
		t.Fatalf("confirming the password moved the start of the session from %v to %v", want, got)
	}
	var clientInfos string
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "client-infos" {
			clientInfos = cookie.Value
		}
	}
	if !strings.HasSuffix(clientInfos, ","+jwtNumber(until)) {
		t.Errorf("the page is told %q, want the end %d", clientInfos, until)
	}

	// used all the time: confirmed, and never re-issued for it
	for _, at := range []time.Duration{30 * time.Minute, 61 * time.Minute, 90 * time.Minute, 119 * time.Minute, 2*time.Hour - time.Second} {
		clock.t = confirmedAt.Add(at)
		_, sudo, err, reissued := refresh(t, confirmed)
		if err != nil || !sudo {
			t.Fatalf("%s after the password: not confirmed (%v)", at, err)
		}
		if reissued != nil {
			t.Fatalf("%s after the password: the token was re-issued", at)
		}
	}

	// a re-issue for another reason copies the end, it does not move it
	clock.t = confirmedAt.Add(90 * time.Minute)
	due := signedSession(t, root, func(c jwt.MapClaims) {
		c["iat"] = time.Now().Add(-25 * time.Hour).Unix()
		c[sessionStartClaim] = claims[sessionStartClaim]
		c[sudoUntilClaim] = until
	})
	_, sudo, err, reissued := refresh(t, due)
	if err != nil || !sudo || reissued == nil {
		t.Fatalf("a day-old token in sudo was not accepted and re-issued: %v", err)
	}
	if got := int64(claimsOf(t, reissued)[sudoUntilClaim].(float64)); got != until {
		t.Fatalf("the re-issued token is confirmed until %d, the confirmation ends at %d", got, until)
	}

	// two hours after the password: over, for the original and the re-issued token
	clock.t = confirmedAt.Add(SudoDuration)
	for name, cookie := range map[string]*http.Cookie{"confirmed": confirmed, "re-issued": reissued} {
		account, sudo, err, _ := refresh(t, cookie)
		if err != nil || account.Nickname != "root" {
			t.Fatalf("%s token, two hours after the password: the session itself ended: %v", name, err)
		}
		if sudo {
			t.Fatalf("%s token: still confirmed two hours after the password", name)
		}
	}

	// a token re-issued after the end carries no confirmation
	late := signedSession(t, root, func(c jwt.MapClaims) {
		c["iat"] = time.Now().Add(-25 * time.Hour).Unix()
		c[sudoUntilClaim] = until
	})
	_, sudo, _, reissued = refresh(t, late)
	if sudo || reissued == nil {
		t.Fatal("a day-old token after the end of the confirmation: confirmed, or not re-issued")
	}
	if _, has := claimsOf(t, reissued)[sudoUntilClaim]; has {
		t.Error("a token re-issued after the end of the confirmation carries one")
	}

	// an end further away than two hours was never issued by this server's clock
	clock.t = confirmedAt
	far := signedSession(t, root, func(c jwt.MapClaims) { c[sudoUntilClaim] = confirmedAt.Add(SudoDuration + time.Minute).Unix() })
	if _, sudo, _, _ := refresh(t, far); sudo {
		t.Error("a confirmation that ends in more than two hours counts")
	}

	// an account whose role has no admin permission is never confirmed
	securityTestUser(t, "alice", utils.USER)
	alice, _ := utils.GetUser("alice")
	plain := signedSession(t, alice, func(c jwt.MapClaims) { c[sudoUntilClaim] = confirmedAt.Add(time.Hour).Unix() })
	if _, sudo, err, _ := refresh(t, plain); err != nil || sudo {
		t.Errorf("a plain account with a confirmation claim: sudo=%v err=%v", sudo, err)
	}
}

func jwtNumber(n int64) string {
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func TestACallerWithoutAHookContinuesTheSessionAndCannotConfirm(t *testing.T) {
	clock := lifeSetup(t)
	securityTestUser(t, "root", utils.ADMIN)
	securityTestUser(t, "other", utils.ADMIN)
	root, _ := utils.GetUser("root")
	session := loginAs(t, "root")
	start := claimsOf(t, session)[sessionStartClaim]
	clock.advance(3 * time.Hour)

	issue := func(cookie *http.Cookie, account utils.User, sudoed bool) jwt.MapClaims {
		w := httptest.NewRecorder()
		SendUserToken(w, lifeRequest("POST", "/cosmos/api/mfa", cookie), account, true, sudoed)
		return claimsOf(t, sessionCookieOf(t, w))
	}

	// the second factor, minutes or hours after the password: the same session
	claims := issue(session, root, false)
	if claims[sessionStartClaim] != start {
		t.Errorf("the second factor moved the start of the session from %v to %v", start, claims[sessionStartClaim])
	}

	// "sudoed" from a caller that did not check the password confirms nothing
	if _, has := issue(session, root, true)[sudoUntilClaim]; has {
		t.Error("a caller without the sudo hook confirmed the password")
	}

	// a confirmation that is running is kept, with its end
	until := clock.now().Add(30 * time.Minute).Unix()
	running := signedSession(t, root, func(c jwt.MapClaims) { c[sudoUntilClaim] = until })
	if got, _ := issue(running, root, true)[sudoUntilClaim].(float64); int64(got) != until {
		t.Errorf("a running confirmation became %v, want %d", got, until)
	}
	if _, has := issue(running, root, false)[sudoUntilClaim]; has {
		t.Error("a caller that says 'not confirmed' kept the confirmation")
	}

	// somebody else's cookie, or one of an ended session, is not continued
	other, _ := utils.GetUser("other")
	otherOld := signedSession(t, other, func(c jwt.MapClaims) {
		c[sessionStartClaim] = clock.now().Add(-20 * 24 * time.Hour).Unix()
		c[sudoUntilClaim] = until
	})
	claims = issue(otherOld, root, true)
	if int64(claims[sessionStartClaim].(float64)) != clock.now().Unix() {
		t.Error("the session of another account's cookie was continued")
	}
	if _, has := claims[sudoUntilClaim]; has {
		t.Error("the confirmation of another account's cookie was taken over")
	}
	ended := signedSession(t, root, func(c jwt.MapClaims) {
		c["passwordCycle"] = root.PasswordCycle + 7
		c[sudoUntilClaim] = until
	})
	if _, has := issue(ended, root, true)[sudoUntilClaim]; has {
		t.Error("the confirmation of an ended session was taken over")
	}

	// a login never continues anything
	w := httptest.NewRecorder()
	SendUserToken(w, NewSession(lifeRequest("POST", "/cosmos/api/login", running)), root, false, true)
	claims = claimsOf(t, sessionCookieOf(t, w))
	if _, has := claims[sudoUntilClaim]; has || int64(claims[sessionStartClaim].(float64)) != clock.now().Unix() {
		t.Errorf("a login continued the cookie it came with: %v", claims)
	}
}

func TestRefusedSessionsAreNamedInTheSecurityRecord(t *testing.T) {
	lifeSetup(t)
	for err, reason := range map[error]string{
		errSessionTooOld:        "session_too_old",
		errSessionNoStart:       "session_unknown_start",
		errSessionBeforeAccount: "account_recreated",
	} {
		recorded := captureSecurity(t)
		utils.RecordSecuritySessionError(lifeRequest("GET", "/cosmos/api/me", nil), err)
		events := recorded()
		if len(events) != 1 || events[0].Kind != utils.SecSessionRejected || events[0].Details["reason"] != reason {
			t.Errorf("%v is recorded as %+v, want rejected with reason %s", err, events, reason)
		}
	}
}

func TestTheOldListOfLoggedOutTokensIsRemoved(t *testing.T) {
	lifeSetup(t)
	securityTestUser(t, "alice", utils.USER)
	path := utils.CONFIGFOLDER + "revoked-sessions.json"
	if err := os.WriteFile(path, []byte(`{"abc":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	retireRevokedSessions = sync.Once{}
	mustWork(t, "a session", loginAs(t, "alice"), "alice")
	if _, err := os.Stat(path); err == nil {
		t.Error("revoked-sessions.json is still there after the first request with a session")
	}
}
