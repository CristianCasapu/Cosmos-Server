package main

// Fork addition (plan 44): the URL check's routes at the HTTP layer: the
// same-origin check in front, the administrator permission, POST for every
// command, and their registration in InitServer.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/behind"
	"github.com/azukaar/cosmos-server/src/utils"
)

func TestURLCheckRoutesAreGuarded(t *testing.T) {
	router := mux.NewRouter()
	api := router.PathPrefix("/cosmos").Subrouter()
	requireSameOrigin(api)
	api.HandleFunc("/api/url-check", behind.CheckRoute)
	api.HandleFunc("/api/url-checks", behind.ChecksRoute)
	api.HandleFunc("/api/url-check/apply", behind.ApplyRoute)
	api.HandleFunc("/api/known-apps", behind.KnownAppsRoute)
	utils.LoadBaseMainConfig(utils.Config{})

	as := func(req *http.Request, perms ...utils.Permission) *http.Request {
		if perms == nil {
			return req
		}
		return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{Nickname: "u", Permissions: perms, IsSudoed: true}))
	}
	admin := []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN}
	reader := []utils.Permission{utils.PERM_LOGIN, utils.PERM_RESOURCES_READ}
	for _, c := range []struct {
		name, method, path, body string
		headers                  map[string]string
		perms                    []utils.Permission
		want                     int
	}{
		{"a foreign page", "POST", "/cosmos/api/url-check", `{"route":"x"}`, map[string]string{"Origin": "https://evil.example.net", "Sec-Fetch-Site": "cross-site"}, admin, http.StatusForbidden},
		{"no session", "POST", "/cosmos/api/url-check", `{"route":"x"}`, nil, nil, http.StatusUnauthorized},
		{"a reader may not check", "POST", "/cosmos/api/url-check", `{"route":"x"}`, nil, reader, http.StatusUnauthorized},
		{"a reader may not apply", "POST", "/cosmos/api/url-check/apply", `{"route":"x","appOwnHost":true}`, nil, reader, http.StatusUnauthorized},
		{"check by GET", "GET", "/cosmos/api/url-check", "", nil, admin, http.StatusMethodNotAllowed},
		{"apply by GET", "GET", "/cosmos/api/url-check/apply", "", nil, admin, http.StatusMethodNotAllowed},
		{"checks by GET", "GET", "/cosmos/api/url-checks", "", nil, admin, http.StatusMethodNotAllowed},
		{"an unknown URL", "POST", "/cosmos/api/url-check", `{"route":"nosuch"}`, nil, admin, http.StatusNotFound},
		{"a body too long", "POST", "/cosmos/api/url-check", `{"route":"` + strings.Repeat("a", 5000) + `"}`, nil, admin, http.StatusBadRequest},
		{"the known apps to a reader", "GET", "/cosmos/api/known-apps", "", nil, reader, http.StatusOK},
	} {
		req := httptest.NewRequest(c.method, "https://server.example.org:2053"+c.path, strings.NewReader(c.body))
		for k, v := range c.headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, as(req, c.perms...))
		if rec.Code != c.want && !(c.want == http.StatusUnauthorized && rec.Code == http.StatusForbidden) {
			t.Errorf("%s: %d, want %d (%s)", c.name, rec.Code, c.want, strings.TrimSpace(rec.Body.String()))
		}
	}
	source, err := os.ReadFile("httpServer.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"url-check", "url-checks", "url-check/apply", "known-apps"} {
		if !regexp.MustCompile(`srapiAdmin\.HandleFunc\("/api/` + regexp.QuoteMeta(path) + `", behind\.`).Match(source) {
			t.Errorf("/api/%s is not registered on the router the same-origin check guards", path)
		}
	}
}
