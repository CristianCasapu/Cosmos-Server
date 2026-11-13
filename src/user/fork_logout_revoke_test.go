package user

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: tests of fork_logout_revoke.go. The session helpers are in
// fork_session_test.go.

func TestALoggedOutSessionStaysLoggedOut(t *testing.T) {
	cookies := sessionTestSetup(t, "cosmos.example.com")
	var session *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name == "jwttoken" {
			session = cookie
		}
	}
	if u, err, _ := refreshWith("cosmos.example.com", session); err != nil || u.Nickname != "alice" {
		t.Fatalf("the session does not work before logout: %v", err)
	}

	// a logout that presents something else remembers nothing
	junk := httptest.NewRequest("GET", "https://cosmos.example.com/cosmos/api/logout", nil)
	junk.AddCookie(&http.Cookie{Name: "jwttoken", Value: "a.b.c"})
	UserLogout(httptest.NewRecorder(), junk)
	if u, err, _ := refreshWith("cosmos.example.com", session); err != nil || u.Nickname != "alice" {
		t.Fatalf("a foreign logout ended the session: %v", err)
	}

	logout := httptest.NewRequest("GET", "https://cosmos.example.com/cosmos/api/logout", nil)
	logout.Host = "cosmos.example.com"
	logout.AddCookie(session)
	w := httptest.NewRecorder()
	UserLogout(w, logout)
	if w.Code != http.StatusOK {
		t.Fatalf("logout: %d", w.Code)
	}

	// the cookie somebody kept
	if u, err, _ := refreshWith("cosmos.example.com", session); err == nil || u.Nickname != "" {
		t.Errorf("the session still works after logout (user %q)", u.Nickname)
	}
	// also after a restart: the list is read from the file again
	revokedTokens.Lock()
	revokedTokens.entries = nil
	revokedTokens.Unlock()
	if u, err, _ := refreshWith("cosmos.example.com", session); err == nil || u.Nickname != "" {
		t.Errorf("the session works again after a restart (user %q)", u.Nickname)
	}

	// a new login is a new session
	alice, _ := utils.GetUser("alice")
	login := httptest.NewRequest("POST", "https://cosmos.example.com/cosmos/api/login", nil)
	login.Host = "cosmos.example.com"
	rec := httptest.NewRecorder()
	// the token carries its time of issue in seconds: let it differ
	waitForNextSecond()
	SendUserToken(rec, login, alice, true, false)
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "jwttoken" {
			if u, err, _ := refreshWith("cosmos.example.com", cookie); err != nil || u.Nickname != "alice" {
				t.Errorf("a new login after logout does not work: %v", err)
			}
		}
	}
}

// waitForNextSecond lets two tokens of the same account differ (the time of
// issue is in seconds).
func waitForNextSecond() {
	start := time.Now().Unix()
	for time.Now().Unix() == start {
		time.Sleep(20 * time.Millisecond)
	}
}
