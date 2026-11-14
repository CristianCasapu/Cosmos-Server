package main

// Fork addition: tests of the server's side of the Fail2ban integration
// (fork_fail2ban.go) and of its two hooks in upstream's files: a merge that
// drops one fails here.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/cloudflare"
	"github.com/azukaar/cosmos-server/src/fail2ban"
	"github.com/azukaar/cosmos-server/src/utils"
)

func TestFail2banHooksAreInPlace(t *testing.T) {
	for file, hook := range map[string]string{
		// on the admin router: behind the token check, the SmartShield and
		// the admin address restriction
		"httpServer.go": "registerFail2banRoutes(srapiAdmin)",
		"index.go":      "startFail2ban()",
	} {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(source), hook) {
			t.Errorf("%s: %q is gone: a hook of the Fail2ban integration is missing", file, hook)
		}
	}
}

func TestFail2banRoutesAreRegisteredAndProtected(t *testing.T) {
	router := mux.NewRouter()
	api := router.PathPrefix("/cosmos").Subrouter()
	registerFail2banRoutes(api)

	serve := func(method string, path string, auth *utils.AuthContext) int {
		req := httptest.NewRequest(method, "http://cosmos.example"+path, strings.NewReader("{}"))
		if auth != nil {
			req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}

	routes := map[string]string{
		"/cosmos/api/fail2ban":          "GET",
		"/cosmos/api/fail2ban/unban":    "POST",
		"/cosmos/api/fail2ban/settings": "POST",
	}
	reader := &utils.AuthContext{Nickname: "alice", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN_READ}}
	for path, method := range routes {
		for _, any := range []string{"GET", "POST", "PUT", "DELETE"} {
			if code := serve(any, path, nil); code != http.StatusUnauthorized {
				t.Errorf("%s %s without a session: %d, want 401", any, path, code)
			}
		}
		// someone who may read the logs may read the status and nothing else
		want := http.StatusUnauthorized
		if method == "GET" {
			want = http.StatusOK
		}
		if code := serve(method, path, reader); code != want {
			t.Errorf("%s %s as a reader of the logs: %d, want %d", method, path, code, want)
		}
	}
	// nothing else is served below /api/fail2ban
	for _, path := range []string{"/cosmos/api/fail2ban/", "/cosmos/api/fail2ban/ban", "/cosmos/api/fail2ban/reload", "/cosmos/api/fail2ban/unban/203.0.113.7"} {
		if code := serve("POST", path, reader); code != http.StatusNotFound {
			t.Errorf("POST %s: %d, want 404", path, code)
		}
	}
}

// The never-ban list of Fail2ban follows what is served through Cloudflare:
// the Cloudflare routes tell the Fail2ban integration when that changes.
func TestFail2banIsToldWhenCloudflareChanges(t *testing.T) {
	if reflect.ValueOf(cloudflare.AfterChange).Pointer() != reflect.ValueOf(fail2ban.CloudflareChanged).Pointer() {
		t.Error("a change of the hostnames served through Cloudflare, or of its ranges, does not reach Fail2ban's never-ban list")
	}
}
