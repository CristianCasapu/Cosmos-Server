package user

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: tests of fork_session.go. The store and key helpers are in
// fork_mfa_limit_test.go.

// sessionTestSetup is a server named cosmos.example.com with one user, and
// returns the Set-Cookie headers of a login of that user on the given host.
func sessionTestSetup(t *testing.T, loginHost string) []*http.Cookie {
	t.Helper()
	mfaTestSetup(t)
	mfaTestUser(t, "alice", false)

	config := utils.GetBaseMainConfig()
	config.HTTPConfig.Hostname = loginHost
	config.HTTPConfig.HTTPSCertificateMode = "SELFSIGNED"
	utils.LoadBaseMainConfig(config)

	alice, err := utils.GetUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "https://"+loginHost+"/cosmos/api/login", nil)
	req.Host = loginHost
	w := httptest.NewRecorder()
	SendUserToken(w, req, alice, true, false)
	cookies := w.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("expected the two session cookies, got %d: %v", len(cookies), w.Header())
	}
	return cookies
}

func refreshWith(host string, cookie *http.Cookie) (utils.User, error, *httptest.ResponseRecorder) {
	req := httptest.NewRequest("GET", "https://"+host+"/cosmos/api/me", nil)
	req.Host = host
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	_, _, u, err := RefreshUserToken(w, req)
	return u, err, w
}

func TestSessionCookiesAreSameSiteLax(t *testing.T) {
	cookies := sessionTestSetup(t, "cosmos.example.com")
	for _, cookie := range cookies {
		if cookie.SameSite != http.SameSiteLaxMode {
			t.Errorf("cookie %s is sent without SameSite=Lax (%v)", cookie.Name, cookie.SameSite)
		}
	}

	// the cookies that end a session must match the ones that started it
	req := httptest.NewRequest("GET", "https://cosmos.example.com/cosmos/api/logout", nil)
	req.Host = "cosmos.example.com"
	w := httptest.NewRecorder()
	logOutUser(w, req)
	cleared := w.Result().Cookies()
	if len(cleared) != 2 {
		t.Fatalf("logout should clear two cookies, got %d", len(cleared))
	}
	for _, cookie := range cleared {
		if cookie.SameSite != http.SameSiteLaxMode {
			t.Errorf("logout clears %s without SameSite=Lax", cookie.Name)
		}
	}
}

func TestMalformedSessionCookieIsRefused(t *testing.T) {
	sessionTestSetup(t, "cosmos.example.com")

	for _, value := range []string{"a", "a.b", "a.b.c.d", ".", "..", "a..c", "eyJhbGciOiJFZERTQSJ9"} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("cookie %q ended the request in a panic: %v", value, r)
				}
			}()
			u, err, w := refreshWith("cosmos.example.com", &http.Cookie{Name: "jwttoken", Value: value})
			if err == nil || u.Nickname != "" {
				t.Errorf("cookie %q was not refused (user %q, err %v)", value, u.Nickname, err)
			}
			if w.Code != http.StatusTemporaryRedirect || !strings.HasPrefix(w.Header().Get("Location"), "/cosmos-ui/login") {
				t.Errorf("cookie %q: expected the redirect to the login page, got %d %q", value, w.Code, w.Header().Get("Location"))
			}
		}()
	}

	// no cookie at all is simply "not logged in", as before
	if u, err, _ := refreshWith("cosmos.example.com", nil); err != nil || u.Nickname != "" {
		t.Errorf("no cookie: user %q, err %v", u.Nickname, err)
	}
}
