package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azukaar/cosmos-server/src/user"
	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: the rules of user/fork_session_life.go hold on every way a
// session reaches the API: a plain call, a websocket, the OpenID provider
// and the check for a confirmed password. They do not touch API tokens.

const pathsHost = "cosmos.example.com"

func sessionPathsSetup(t *testing.T) {
	t.Helper()
	prevFolder := utils.CONFIGFOLDER
	utils.CONFIGFOLDER = t.TempDir() + "/"
	if err := utils.InitStore(); err != nil {
		t.Fatal("InitStore:", err)
	}
	prevConfig := utils.GetBaseMainConfig()
	pub, priv := utils.GenerateEd25519Certificates()
	config := utils.Config{}
	config.HTTPConfig.Hostname = pathsHost
	config.HTTPConfig.HTTPSCertificateMode = "SELFSIGNED"
	config.HTTPConfig.AuthPublicKey = pub
	config.HTTPConfig.AuthPrivateKey = priv
	sum := sha256.Sum256([]byte("cosmos_test_token_of_alice"))
	config.APITokens = map[string]utils.APITokenConfig{
		"script": {Name: "script", Owner: "alice", TokenHash: hex.EncodeToString(sum[:]), Permissions: []utils.Permission{utils.PERM_ADMIN_READ}},
	}
	utils.LoadBaseMainConfig(config)
	t.Cleanup(func() {
		utils.LoadBaseMainConfig(prevConfig)
		utils.CloseStore()
		utils.CONFIGFOLDER = prevFolder
	})
	for _, account := range []utils.User{
		{Nickname: "alice", Password: "x", Role: utils.ADMIN},
		{Nickname: "bob", Password: "x", Role: utils.ADMIN},
	} {
		if err := utils.CreateUser(account); err != nil {
			t.Fatal(err)
		}
	}
}

func pathsRequest(method string, path string, cookie *http.Cookie, headers map[string]string) *http.Request {
	req := httptest.NewRequest(method, "https://"+pathsHost+path, nil)
	req.Host = pathsHost
	req.RemoteAddr = "203.0.113.7:40000"
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	return req
}

func pathsCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "jwttoken" && cookie.Value != "" {
			return cookie
		}
	}
	t.Fatal("no session cookie was issued")
	return nil
}

// pathsSudoSession logs an administrator in and confirms the password, as
// the two handlers do once the password was right.
func pathsSudoSession(t *testing.T, nickname string) *http.Cookie {
	t.Helper()
	account, err := utils.GetUser(nickname)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	user.SendUserToken(w, user.NewSession(pathsRequest("POST", "/cosmos/api/login", nil, nil)), account, true, false)
	return pathsCookie(t, w)
}

type pathOutcome struct {
	reached  bool
	nickname string
	apiToken bool
	admin    bool // utils.CheckPermissions(PERM_ADMIN_READ) passed in the handler
}

// throughTokenMiddleware sends one request through the API's token check.
func throughTokenMiddleware(req *http.Request, handler http.HandlerFunc) (pathOutcome, *httptest.ResponseRecorder) {
	outcome := pathOutcome{}
	w := httptest.NewRecorder()
	tokenMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := utils.GetAuthContext(r)
		outcome.reached = true
		outcome.nickname = ctx.Nickname
		outcome.apiToken = ctx.APIToken != nil
		if handler != nil {
			handler(w, r)
			return
		}
		outcome.admin = utils.CheckPermissions(httptest.NewRecorder(), r, utils.PERM_ADMIN_READ) == nil
	})).ServeHTTP(w, req)
	return outcome, w
}

func TestALoggedOutAccountIsRefusedOnEveryPathOfTheAPI(t *testing.T) {
	sessionPathsSetup(t)
	alice := pathsSudoSession(t, "alice")
	aliceElsewhere := pathsSudoSession(t, "alice")
	bob := pathsSudoSession(t, "bob")

	websocket := map[string]string{"Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13", "Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ=="}
	paths := []struct {
		name    string
		method  string
		path    string
		headers map[string]string
	}{
		{"a plain API call", "GET", "/cosmos/api/me", nil},
		{"a command", "POST", "/cosmos/api/restart", nil},
		{"the host terminal's websocket", "GET", "/cosmos/api/terminal/bash", websocket},
		{"a container terminal's websocket", "GET", "/cosmos/api/servapps/app/terminal/attach", websocket},
		{"the OpenID provider's sign-in", "GET", "/oauth2/auth", nil},
		{"the logs", "GET", "/cosmos/_logs", nil},
	}

	for _, p := range paths {
		for who, cookie := range map[string]*http.Cookie{"alice": alice, "bob": bob} {
			outcome, _ := throughTokenMiddleware(pathsRequest(p.method, p.path, cookie, p.headers), nil)
			if !outcome.reached || outcome.nickname != who || !outcome.admin {
				t.Fatalf("%s before the logout, as %s: %+v", p.name, who, outcome)
			}
		}
	}

	// alice logs out on one device
	w := httptest.NewRecorder()
	user.UserLogout(w, pathsRequest("GET", "/cosmos/api/logout", alice, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("logout: %d %s", w.Code, w.Body.String())
	}

	for _, p := range paths {
		for device, cookie := range map[string]*http.Cookie{"the device that logged out": alice, "her other device": aliceElsewhere} {
			outcome, answer := throughTokenMiddleware(pathsRequest(p.method, p.path, cookie, p.headers), nil)
			if outcome.reached {
				t.Errorf("%s after the logout, from %s: the request was let through as %q", p.name, device, outcome.nickname)
			}
			if answer.Code != http.StatusTemporaryRedirect {
				t.Errorf("%s after the logout, from %s: answered %d, want the redirect to the login", p.name, device, answer.Code)
			}
		}
		outcome, _ := throughTokenMiddleware(pathsRequest(p.method, p.path, bob, p.headers), nil)
		if !outcome.reached || outcome.nickname != "bob" || !outcome.admin {
			t.Errorf("%s after alice's logout, as bob: %+v", p.name, outcome)
		}
	}

	// the OpenID provider's own handler: no authorization for the logged-out session
	reachedProvider := false
	outcome, _ := throughTokenMiddleware(pathsRequest("GET", "/oauth2/auth?client_id=x&response_type=code", aliceElsewhere, nil),
		func(w http.ResponseWriter, r *http.Request) { reachedProvider = true })
	if outcome.reached || reachedProvider {
		t.Error("the OpenID provider was reached with the session of a logged-out account")
	}
}

func TestAnAPITokenIsNotASession(t *testing.T) {
	sessionPathsSetup(t)
	alice := pathsSudoSession(t, "alice")
	token := map[string]string{"Authorization": "Bearer cosmos_test_token_of_alice"}

	check := func(when string) {
		t.Helper()
		outcome, answer := throughTokenMiddleware(pathsRequest("GET", "/cosmos/api/me", nil, token), nil)
		if !outcome.reached || !outcome.apiToken || outcome.nickname != "alice" || !outcome.admin {
			t.Fatalf("%s: the API token is not accepted as before: %+v (%d)", when, outcome, answer.Code)
		}
		if len(answer.Result().Cookies()) != 0 {
			t.Errorf("%s: a request with an API token was given cookies", when)
		}
	}
	check("before the owner logs out")

	w := httptest.NewRecorder()
	user.UserLogout(w, pathsRequest("GET", "/cosmos/api/logout", alice, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("logout: %d", w.Code)
	}
	if outcome, _ := throughTokenMiddleware(pathsRequest("GET", "/cosmos/api/me", alice, nil), nil); outcome.reached {
		t.Fatal("the session still works after the logout")
	}
	check("after the owner logged out")

	// a token cannot confirm a password, and a wrong token is refused as before
	sudo := httptest.NewRecorder()
	outcome, _ := throughTokenMiddleware(pathsRequest("POST", "/cosmos/api/sudo", nil, token), func(w http.ResponseWriter, r *http.Request) {
		user.UserSudo(sudo, r)
	})
	if !outcome.reached || sudo.Code != http.StatusUnauthorized || len(sudo.Result().Cookies()) != 0 {
		t.Errorf("sudo with an API token: %d, cookies %v", sudo.Code, sudo.Result().Cookies())
	}
	outcome, answer := throughTokenMiddleware(pathsRequest("GET", "/cosmos/api/me", nil, map[string]string{"Authorization": "Bearer cosmos_wrong"}), nil)
	if outcome.reached || answer.Code != http.StatusUnauthorized {
		t.Errorf("a wrong API token: reached=%v %d", outcome.reached, answer.Code)
	}
}

func TestSudoPermissionsNeedTheConfirmationOnTheAPI(t *testing.T) {
	sessionPathsSetup(t)
	session := pathsSudoSession(t, "alice")

	needsSudo := func(cookie *http.Cookie) (reached bool, code int) {
		w := httptest.NewRecorder()
		outcome, _ := throughTokenMiddleware(pathsRequest("POST", "/cosmos/api/restart", cookie, nil), func(_ http.ResponseWriter, r *http.Request) {
			if utils.CheckPermissions(w, r, utils.PERM_ADMIN) == nil {
				w.WriteHeader(http.StatusOK)
			}
		})
		return outcome.reached, w.Code
	}

	if reached, code := needsSudo(session); !reached || code != http.StatusUnauthorized {
		t.Fatalf("an admin action right after the login: reached=%v %d, want 401 (password not confirmed)", reached, code)
	}

	// a re-issue by a caller that did not ask for the password confirms nothing
	account, _ := utils.GetUser("alice")
	w := httptest.NewRecorder()
	user.SendUserToken(w, pathsRequest("POST", "/cosmos/api/mfa", session, nil), account, true, true)
	if reached, code := needsSudo(pathsCookie(t, w)); !reached || code != http.StatusUnauthorized {
		t.Fatalf("a token re-issued by a caller that did not ask for the password is confirmed: reached=%v %d", reached, code)
	}
}
