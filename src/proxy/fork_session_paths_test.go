package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azukaar/cosmos-server/src/user"
	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: the rules of user/fork_session_life.go hold for the
// applications behind routes too: their token check is the same function.

func TestALoggedOutAccountIsRefusedByAnApplicationRoute(t *testing.T) {
	const host = "cosmos.example.com"
	prevFolder := utils.CONFIGFOLDER
	utils.CONFIGFOLDER = t.TempDir() + "/"
	if err := utils.InitStore(); err != nil {
		t.Fatal("InitStore:", err)
	}
	prevConfig := utils.GetBaseMainConfig()
	pub, priv := utils.GenerateEd25519Certificates()
	config := utils.Config{}
	config.HTTPConfig.Hostname = host
	config.HTTPConfig.HTTPSCertificateMode = "SELFSIGNED"
	config.HTTPConfig.AuthPublicKey = pub
	config.HTTPConfig.AuthPrivateKey = priv
	utils.LoadBaseMainConfig(config)
	t.Cleanup(func() {
		utils.LoadBaseMainConfig(prevConfig)
		utils.CloseStore()
		utils.CONFIGFOLDER = prevFolder
	})

	request := func(path string, cookie *http.Cookie) *http.Request {
		req := httptest.NewRequest("GET", "https://app."+host+path, nil)
		req.Host = "app." + host
		req.RemoteAddr = "203.0.113.7:40000"
		if cookie != nil {
			req.AddCookie(cookie)
		}
		return req
	}
	login := func(nickname string) *http.Cookie {
		if err := utils.CreateUser(utils.User{Nickname: nickname, Password: "x", Role: utils.USER}); err != nil {
			t.Fatal(err)
		}
		account, _ := utils.GetUser(nickname)
		w := httptest.NewRecorder()
		// the session is made on the panel's hostname and is good for names under it
		req := httptest.NewRequest("POST", "https://"+host+"/cosmos/api/login", nil)
		req.Host = host
		user.SendUserToken(w, user.NewSession(req), account, true, false)
		for _, cookie := range w.Result().Cookies() {
			if cookie.Name == "jwttoken" {
				return cookie
			}
		}
		t.Fatal("no session cookie")
		return nil
	}
	alice, aliceElsewhere, bob := login("alice"), (*http.Cookie)(nil), login("bob")
	account, _ := utils.GetUser("alice")
	again := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "https://"+host+"/cosmos/api/login", nil)
	req.Host = host
	user.SendUserToken(again, user.NewSession(req), account, true, false)
	for _, cookie := range again.Result().Cookies() {
		if cookie.Name == "jwttoken" {
			aliceElsewhere = cookie
		}
	}

	route := utils.ProxyRouteConfig{Name: "app", Host: "app." + host, AuthEnabled: true}
	through := func(cookie *http.Cookie) (string, int) {
		seen := ""
		w := httptest.NewRecorder()
		tokenMiddleware(route)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = r.Header.Get("x-cosmos-user")
			if seen == "" {
				seen = "(nobody)"
			}
		})).ServeHTTP(w, request("/page", cookie))
		return seen, w.Code
	}

	for who, cookie := range map[string]*http.Cookie{"alice": alice, "bob": bob} {
		if seen, code := through(cookie); seen != who {
			t.Fatalf("before the logout the application sees %q for %s (%d)", seen, who, code)
		}
	}

	w := httptest.NewRecorder()
	logout := httptest.NewRequest("GET", "https://"+host+"/cosmos/api/logout", nil)
	logout.Host = host
	logout.AddCookie(alice)
	user.UserLogout(w, logout)
	if w.Code != http.StatusOK {
		t.Fatalf("logout: %d", w.Code)
	}

	for device, cookie := range map[string]*http.Cookie{"the device that logged out": alice, "her other device": aliceElsewhere} {
		if seen, code := through(cookie); seen != "" || code != http.StatusTemporaryRedirect {
			t.Errorf("after the logout, from %s: the application was reached as %q (%d)", device, seen, code)
		}
	}
	if seen, _ := through(bob); seen != "bob" {
		t.Errorf("after alice's logout the application sees %q for bob", seen)
	}
	if seen, code := through(nil); seen != "" || code != http.StatusFound && code != http.StatusTemporaryRedirect {
		t.Errorf("without a session the application was reached as %q (%d)", seen, code)
	}
}
