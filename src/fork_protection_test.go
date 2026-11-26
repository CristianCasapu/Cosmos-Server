package main

// Fork addition: tests of the server's side of the protection cards
// (fork_protection.go) and of its hook in httpServer.go: a merge that drops
// it fails here.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestProtectionHookIsInPlace(t *testing.T) {
	source, err := os.ReadFile("httpServer.go")
	if err != nil {
		t.Fatal(err)
	}
	// on the admin router: behind the token check, the SmartShield and the
	// admin address restriction
	if !strings.Contains(string(source), "registerProtectionRoutes(srapiAdmin)") {
		t.Error("httpServer.go: registerProtectionRoutes(srapiAdmin) is gone: the protection cards have no route")
	}
}

func TestProtectionRouteIsRegisteredAndProtected(t *testing.T) {
	router := mux.NewRouter()
	api := router.PathPrefix("/cosmos").Subrouter()
	registerProtectionRoutes(api)

	serve := func(method string, path string, auth *utils.AuthContext) int {
		req := httptest.NewRequest(method, "http://cosmos.example"+path, nil)
		if auth != nil {
			req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}

	reader := &utils.AuthContext{Nickname: "alice", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN_READ}}
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		if code := serve(method, "/cosmos/api/protection", nil); code != http.StatusUnauthorized {
			t.Errorf("%s without a session: %d, want 401", method, code)
		}
	}
	// someone who may read the logs may read it, with GET only
	if code := serve("GET", "/cosmos/api/protection", reader); code != http.StatusOK {
		t.Errorf("GET as a reader of the logs: %d, want 200", code)
	}
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		if code := serve(method, "/cosmos/api/protection", reader); code != http.StatusMethodNotAllowed {
			t.Errorf("%s as a reader of the logs: %d, want 405", method, code)
		}
	}
	// nothing else is served below /api/protection
	for _, path := range []string{"/cosmos/api/protection/", "/cosmos/api/protection/install", "/cosmos/api/protection/nftables", "/cosmos/api/protection/run"} {
		for _, method := range []string{"GET", "POST"} {
			if code := serve(method, path, reader); code != http.StatusNotFound {
				t.Errorf("%s %s: %d, want 404", method, path, code)
			}
		}
	}
}
