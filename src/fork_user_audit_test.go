package main

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

// The routes of the user audit panel: registered, the admin ones on the
// admin router and the own-activity one on the router of logged-in users,
// all refusing a request without a session.
func TestUserAuditRoutesAreRegisteredAndProtected(t *testing.T) {
	router := mux.NewRouter()
	admin := router.PathPrefix("/cosmos").Subrouter()
	user := router.PathPrefix("/cosmos").Subrouter()
	registerUserAuditRoutes(admin, user)

	for _, path := range []string{"/cosmos/api/security/audit", "/cosmos/api/security/user-summary?user=x", "/cosmos/api/security/my-activity"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "http://cosmos.example"+path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a session: %d, want 401", path, w.Code)
		}
	}

	// a plain user is refused the trail
	plain := &utils.AuthContext{Nickname: "maria", Permissions: []utils.Permission{utils.PERM_LOGIN}}
	req := httptest.NewRequest("GET", "http://cosmos.example/cosmos/api/security/audit", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, plain)))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("GET audit as a plain user: %d", w.Code)
	}

	source, err := os.ReadFile("httpServer.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "registerUserAuditRoutes(srapiAdmin, srapi)") {
		t.Fatal("the routes are not registered on the admin router and the logged-in router")
	}
}
