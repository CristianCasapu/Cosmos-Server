package user

// Fork addition: tests of what the login, account and session code reports
// to the security record (fork_security.go and the hooks in upstream's
// handlers).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/crypto/bcrypt"

	"github.com/azukaar/cosmos-server/src/utils"
)

const securityTestPassword = "Correct-horse-9"

// captureSecurity collects what is recorded during a test.
func captureSecurity(t *testing.T) func() []utils.SecurityEvent {
	t.Helper()
	var mu sync.Mutex
	events := []utils.SecurityEvent{}
	utils.SetSecurityRecorder(func(event utils.SecurityEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}, func() {})
	t.Cleanup(func() { utils.SetSecurityRecorder(nil, nil) })
	return func() []utils.SecurityEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]utils.SecurityEvent(nil), events...)
	}
}

// securityTestUser creates an account that can log in.
func securityTestUser(t *testing.T, nickname string, role utils.Role) utils.User {
	t.Helper()
	// a cheap hash: the handlers compare with whatever cost the hash names
	hash, err := bcrypt.GenerateFromPassword([]byte(securityTestPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	user := utils.User{Nickname: nickname, Password: string(hash), Role: role, Email: nickname + "@example.com"}
	if err := utils.CreateUser(user); err != nil {
		t.Fatal("CreateUser:", err)
	}
	return user
}

func securityPost(path string, body string, auth *utils.AuthContext) *http.Request {
	req := httptest.NewRequest("POST", "http://localhost"+path, strings.NewReader(body))
	req.RemoteAddr = "203.0.113.7:40000"
	if auth != nil {
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
	}
	return req
}

func adminSession(nickname string) *utils.AuthContext {
	return &utils.AuthContext{Nickname: nickname, Role: utils.ADMIN, UserRole: utils.ADMIN,
		Permissions: utils.GetRolePermissions(utils.ADMIN), IsSudoed: true}
}

func login(nickname string, password string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	UserLogin(w, securityPost("/cosmos/api/login", `{"nickname":"`+nickname+`","password":"`+password+`"}`, nil))
	return w
}

// An unknown user and a wrong password get the same answer; the record
// tells them apart.
func TestSecurityLoginRecordsWhoAndWhy(t *testing.T) {
	mfaTestSetup(t)
	recorded := captureSecurity(t)
	securityTestUser(t, "alice", utils.ADMIN)

	unknown := login("mallory", securityTestPassword)
	wrong := login("alice", "Wrong-password-1")
	mistyped := login(securityTestPassword, "alice") // the password in the name field
	right := login("Alice", securityTestPassword)

	if unknown.Code != wrong.Code || unknown.Body.String() != wrong.Body.String() {
		t.Fatalf("an unknown user is told %d %s, a wrong password %d %s: the two must not differ",
			unknown.Code, unknown.Body.String(), wrong.Code, wrong.Body.String())
	}
	if unknown.Code != http.StatusInternalServerError || !strings.Contains(unknown.Body.String(), "UL001") {
		t.Fatalf("the answer to a failed login changed: %d %s", unknown.Code, unknown.Body.String())
	}
	if mistyped.Code != unknown.Code {
		t.Fatalf("a password in the name field is told %d", mistyped.Code)
	}
	if right.Code != http.StatusOK {
		t.Fatalf("the right password was refused: %d %s", right.Code, right.Body.String())
	}

	events := recorded()
	if len(events) != 4 {
		t.Fatalf("%d events, want 4: %+v", len(events), events)
	}
	for i, want := range []struct{ kind, target, reason, actor string }{
		{utils.SecLoginFailed, "mallory", "unknown_user", ""},
		{utils.SecLoginFailed, "alice", "wrong_password", ""},
		{utils.SecLoginFailed, "(not a nickname)", "unknown_user", ""},
		{utils.SecLoginSuccess, "", "", "alice"},
	} {
		event := events[i]
		if event.Kind != want.kind || event.Target != want.target || event.Details["reason"] != want.reason ||
			event.Actor != want.actor || event.IP != "203.0.113.7" || event.Path != "/cosmos/api/login" {
			t.Errorf("event %d = %+v, want %+v", i, event, want)
		}
	}
	if events[3].Details["mfa"] != "none" {
		t.Errorf("mfa = %q for an account without a second factor", events[3].Details["mfa"])
	}
	for _, event := range events {
		for _, text := range []string{event.Target, event.Actor, event.Details["reason"]} {
			if strings.Contains(strings.ToLower(text), strings.ToLower(securityTestPassword)) || strings.Contains(text, "Wrong-password-1") {
				t.Fatalf("a password reached the record: %+v", event)
			}
		}
	}
}

func TestSecurityLoginOfAnInvitedOrMalformedRequest(t *testing.T) {
	mfaTestSetup(t)
	recorded := captureSecurity(t)
	if err := utils.CreateUser(utils.User{Nickname: "invited", Role: utils.USER, RegisterKey: "key"}); err != nil {
		t.Fatal(err)
	}

	login("invited", securityTestPassword)
	w := httptest.NewRecorder()
	UserLogin(w, securityPost("/cosmos/api/login", `not json`, nil))

	events := recorded()
	if len(events) != 2 || events[0].Details["reason"] != "not_registered" || events[0].Target != "invited" ||
		events[1].Details["reason"] != "bad_request" || events[1].Target != "" {
		t.Fatalf("events = %+v", events)
	}
}

func TestSecuritySudoRecordsBothOutcomes(t *testing.T) {
	mfaTestSetup(t)
	recorded := captureSecurity(t)
	securityTestUser(t, "alice", utils.ADMIN)
	session := &utils.AuthContext{Nickname: "alice", Role: utils.USER, UserRole: utils.ADMIN, Permissions: utils.GetRolePermissions(utils.ADMIN)}

	wrong := httptest.NewRecorder()
	UserSudo(wrong, securityPost("/cosmos/api/sudo", `{"password":"Wrong-password-1"}`, session))
	right := httptest.NewRecorder()
	UserSudo(right, securityPost("/cosmos/api/sudo", `{"password":"`+securityTestPassword+`"}`, session))

	if wrong.Code != http.StatusInternalServerError || right.Code != http.StatusOK {
		t.Fatalf("answers %d and %d", wrong.Code, right.Code)
	}
	events := recorded()
	if len(events) != 2 || events[0].Kind != utils.SecSudoFailed || events[1].Kind != utils.SecSudoSuccess ||
		events[0].Actor != "alice" || events[1].Actor != "alice" {
		t.Fatalf("events = %+v", events)
	}
}

// Five wrong codes: five failures and, with the fifth, the lock-out, once.
func TestSecurityTwoFactorFailuresAndLockOut(t *testing.T) {
	mfaTestSetup(t)
	recorded := captureSecurity(t)
	secret := mfaTestUser(t, "alice", true)
	wrong := wrongCode(t, secret)

	for i := 0; i < mfaMaxFailures; i++ {
		postCode("alice", wrong)
	}
	refused := postCode("alice", wrong)
	if refused.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt after the lock: %d", refused.Code)
	}
	// even the right code is refused now, and recorded as such
	postCode("alice", rightCode(t, secret))

	kinds := []string{}
	for _, event := range recorded() {
		kinds = append(kinds, event.Kind+"/"+event.Details["reason"])
		if event.Actor != "alice" && event.Target != "alice" {
			t.Errorf("event without the account: %+v", event)
		}
	}
	want := strings.Repeat(utils.SecMFAFailed+"/wrong_code ", mfaMaxFailures) + utils.SecMFALocked + "/ " +
		utils.SecMFAFailed + "/locked " + utils.SecMFAFailed + "/locked"
	if got := strings.Join(kinds, " "); got != want {
		t.Fatalf("recorded:\n%s\nwant:\n%s", got, want)
	}
}

func TestSecurityTwoFactorSuccess(t *testing.T) {
	mfaTestSetup(t)
	recorded := captureSecurity(t)
	secret := mfaTestUser(t, "alice", true)

	if w := postCode("alice", rightCode(t, secret)); w.Code != http.StatusOK {
		t.Fatalf("the right code was refused: %d %s", w.Code, w.Body.String())
	}
	events := recorded()
	if len(events) != 1 || events[0].Kind != utils.SecMFASuccess || events[0].Actor != "alice" {
		t.Fatalf("events = %+v", events)
	}
}

// The logout route is outside the token check: the record reads the session
// itself, and a logout without one is not an event.
func TestSecurityLogoutNamesTheSession(t *testing.T) {
	mfaTestSetup(t)
	recorded := captureSecurity(t)
	user := securityTestUser(t, "alice", utils.ADMIN)

	issued := httptest.NewRecorder()
	SendUserToken(issued, httptest.NewRequest("GET", "http://localhost/", nil), user, true, false)
	var session *http.Cookie
	for _, cookie := range issued.Result().Cookies() {
		if cookie.Name == "jwttoken" {
			session = cookie
		}
	}
	if session == nil || session.Value == "" {
		t.Fatal("no session cookie was issued")
	}

	logout := func(cookie *http.Cookie) int {
		req := httptest.NewRequest("GET", "http://localhost/cosmos/api/logout", nil)
		req.RemoteAddr = "203.0.113.7:40000"
		if cookie != nil {
			req.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		UserLogout(w, req)
		return w.Code
	}

	tampered := *session
	parts := strings.Split(session.Value, ".")
	tampered.Value = parts[0] + "." + parts[1] + "." + strings.Repeat("A", len(parts[2]))
	for name, cookie := range map[string]*http.Cookie{
		"none":      nil,
		"garbage":   {Name: "jwttoken", Value: "garbage"},
		"two parts": {Name: "jwttoken", Value: "a.b"},
		"tampered":  &tampered,
	} {
		if code := logout(cookie); code != http.StatusOK {
			t.Errorf("logout with %s cookie: %d, want 200 as before", name, code)
		}
	}
	if events := recorded(); len(events) != 0 {
		t.Fatalf("a logout without a session of this server was recorded: %+v", events)
	}

	if code := logout(session); code != http.StatusOK {
		t.Fatalf("logout: %d", code)
	}
	events := recorded()
	if len(events) != 1 || events[0].Kind != utils.SecLogout || events[0].Actor != "alice" || events[0].IP != "203.0.113.7" {
		t.Fatalf("events = %+v", events)
	}
}

func TestSecurityRegistrationRecordsInviteResetAndBadKeys(t *testing.T) {
	mfaTestSetup(t)
	recorded := captureSecurity(t)
	soon := time.Now().Add(time.Hour)
	if err := utils.CreateUser(utils.User{Nickname: "invited", Role: utils.USER, RegisterKey: "invitekey", RegisterKeyExp: soon}); err != nil {
		t.Fatal(err)
	}
	if err := utils.CreateUser(utils.User{Nickname: "forgetful", Role: utils.USER, Password: "old-hash", RegisterKey: "resetkey", RegisterKeyExp: soon}); err != nil {
		t.Fatal(err)
	}
	if err := utils.CreateUser(utils.User{Nickname: "late", Role: utils.USER, RegisterKey: "latekey", RegisterKeyExp: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}

	register := func(nickname string, key string) int {
		w := httptest.NewRecorder()
		UserRegister(w, securityPost("/cosmos/api/register", `{"nickname":"`+nickname+`","password":"`+securityTestPassword+`","registerKey":"`+key+`"}`, nil))
		return w.Code
	}
	codes := []int{
		register("invited", "wrongkey"),
		register("nobody", "invitekey"),
		register("late", "latekey"),
		register("invited", "invitekey"),
		register("forgetful", "resetkey"),
	}
	for i, want := range []int{500, 500, 500, 200, 200} {
		if codes[i] != want {
			t.Fatalf("answers = %v, want 500 500 500 200 200", codes)
		}
	}

	events := recorded()
	if len(events) != 5 {
		t.Fatalf("%d events, want 5: %+v", len(events), events)
	}
	for i, want := range []struct{ kind, target, actor, reason string }{
		{utils.SecRegisterFailed, "invited", "", "bad_key"},
		{utils.SecRegisterFailed, "nobody", "", "bad_key"},
		{utils.SecRegisterFailed, "late", "", "expired"},
		{utils.SecUserInviteUsed, "", "invited", ""},
		{utils.SecPasswordChanged, "", "forgetful", ""},
	} {
		event := events[i]
		if event.Kind != want.kind || event.Target != want.target || event.Actor != want.actor || event.Details["reason"] != want.reason {
			t.Errorf("event %d = %+v, want %+v", i, event, want)
		}
		for _, text := range []string{event.Target, event.Actor, event.Path} {
			if strings.Contains(text, "key") && text != "/cosmos/api/register" {
				t.Errorf("a register key reached the record: %+v", event)
			}
		}
	}
}

func TestSecurityPasswordResetRequests(t *testing.T) {
	recorded := captureSecurity(t)
	req := securityPost("/cosmos/api/password-reset", `{}`, nil)

	recordPasswordResetRequest(req, "alice", utils.User{Nickname: "alice", Email: "alice@example.com"}, nil)
	recordPasswordResetRequest(req, "mallory", utils.User{}, utils.ErrNotFound)
	recordPasswordResetRequest(req, "p4ss word!", utils.User{}, utils.ErrNotFound)
	recordPasswordResetRequest(req, "nomail", utils.User{Nickname: "nomail"}, nil)

	events := recorded()
	if len(events) != 4 {
		t.Fatalf("%d events, want 4: %+v", len(events), events)
	}
	for i, want := range [][2]string{
		{utils.SecResetRequested, "alice"},
		{utils.SecResetRejected, "mallory"},
		{utils.SecResetRejected, "(not a nickname)"},
		{utils.SecResetRejected, "nomail"},
	} {
		if events[i].Kind != want[0] || events[i].Target != want[1] || events[i].Actor != "" {
			t.Errorf("event %d = %+v, want %v", i, events[i], want)
		}
	}
}

func TestSecurityAccountChangesAreAudited(t *testing.T) {
	mfaTestSetup(t)
	recorded := captureSecurity(t)
	securityTestUser(t, "alice", utils.ADMIN)
	securityTestUser(t, "bob", utils.USER)
	// the handlers read this object; as in upstream's own tests it points nowhere
	previousFBL := utils.FBL
	utils.FBL = utils.NewFirebaseApiSdk("http://127.0.0.1:9")
	t.Cleanup(func() { utils.FBL = previousFBL })

	admin := adminSession("alice")
	// may manage users, but holds nothing else: cannot hand out the admin role
	manager := &utils.AuthContext{Nickname: "bob", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_USERS}, IsSudoed: true}

	withName := func(req *http.Request, nickname string) *http.Request {
		return mux.SetURLVars(req, map[string]string{"nickname": nickname})
	}
	run := func(handler http.HandlerFunc, req *http.Request) int {
		w := httptest.NewRecorder()
		handler(w, req)
		return w.Code
	}

	codes := []int{
		run(UserCreate, securityPost("/cosmos/api/users", `{"nickname":"carol","role":1}`, admin)),
		run(UserCreate, securityPost("/cosmos/api/users", `{"nickname":"dave","role":2}`, manager)),
		run(UserResendInviteLink, securityPost("/cosmos/api/invite", `{"nickname":"carol","formType":"2"}`, admin)),
		run(UserResendInviteLink, securityPost("/cosmos/api/invite", `{"nickname":"bob","formType":"1"}`, admin)),
	}
	patch := withName(securityPost("/cosmos/api/users/bob", `{"role":2}`, admin), "bob")
	patch.Method = "PATCH"
	codes = append(codes, run(UserEdit, patch))
	escalate := withName(securityPost("/cosmos/api/users/bob", `{"role":2}`, manager), "bob")
	escalate.Method = "PATCH"
	codes = append(codes, run(UserEdit, escalate))
	reset := securityPost("/cosmos/api/mfa", `{"nickname":"bob"}`, admin)
	reset.Method = "DELETE"
	codes = append(codes, run(Delete2FA, reset))
	remove := withName(securityPost("/cosmos/api/users/carol", ``, admin), "carol")
	remove.Method = "DELETE"
	codes = append(codes, run(UserDelete, remove))

	wantCodes := []int{200, 403, 200, 200, 200, 403, 200, 200}
	for i := range wantCodes {
		if codes[i] != wantCodes[i] {
			t.Fatalf("answers = %v, want %v", codes, wantCodes)
		}
	}

	got := []string{}
	for _, event := range recorded() {
		got = append(got, event.Kind+" "+event.Actor+">"+event.Target+" "+event.Details["role"]+event.Details["self"])
	}
	want := []string{
		utils.SecUserCreated + " alice>carol User",
		utils.SecAccessGrantDenied + " bob>dave ",
		utils.SecUserInviteCreated + " alice>carol ",
		utils.SecUserResetCreated + " alice>bob ",
		utils.SecUserRoleChanged + " alice>bob Admin",
		utils.SecAccessGrantDenied + " bob>bob ",
		utils.SecUserMFAReset + " alice>bob ",
		utils.SecUserDeleted + " alice>carol no",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("recorded:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
