package main

// Fork addition: tests of fork_admin_routes.go.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"

	"github.com/gorilla/mux"
)

func TestAdminRestrictionReachesTerminalAndRemoteStorage(t *testing.T) {
	router := mux.NewRouter()
	api := router.PathPrefix("/cosmos").Subrouter()
	ok := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }
	for _, path := range []string{"/api/terminal/{route}", "/rclone/config/dump", "/rclone/core/stats", "/api/status", "/api/me"} {
		api.HandleFunc(path, ok)
	}
	refuse := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })
	}
	if n := restrictAdminElsewhere(api, refuse); n != 3 {
		t.Errorf("restricted %d routes, want 3", n)
	}
	for path, want := range map[string]int{
		"/cosmos/api/terminal/bash":  http.StatusForbidden,
		"/cosmos/rclone/config/dump": http.StatusForbidden,
		"/cosmos/rclone/core/stats":  http.StatusForbidden,
		"/cosmos/api/status":         http.StatusTeapot,
		"/cosmos/api/me":             http.StatusTeapot,
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
	}
	source, err := os.ReadFile("httpServer.go")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^\s*restrictAdminElsewhere\(srapi, utils\.Restrictions\(`).Match(source) {
		t.Error("the hook of fork_admin_routes.go is missing from InitServer")
	}
	for _, path := range []string{`"/api/terminal/{route}"`, `"/rclone/config/dump"`} {
		if !regexp.MustCompile(`srapi\.HandleFunc\(` + regexp.QuoteMeta(path)).Match(source) {
			t.Errorf("%s is no longer on srapi: check that it is still behind the admin restriction", path)
		}
	}
}
