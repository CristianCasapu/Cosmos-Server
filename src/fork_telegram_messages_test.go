package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestTelegramMessageRoutesAreHookedAndProtected(t *testing.T) {
	content, _ := os.ReadFile("httpServer.go")
	if !strings.Contains(string(content), "registerTelegramMessageRoutes(srapiAdmin)") {
		t.Fatal("httpServer.go: registerTelegramMessageRoutes(srapiAdmin) is gone")
	}

	router := mux.NewRouter()
	api := router.PathPrefix("/cosmos").Subrouter()
	registerTelegramMessageRoutes(api)

	for _, request := range [][2]string{{"GET", "/cosmos/api/telegram/kinds"}, {"POST", "/cosmos/api/telegram/sample"}} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(request[0], "http://cosmos.example"+request[1], strings.NewReader(`{"kind":"auth.login.failed"}`)))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d, want 401", request[0], request[1], w.Code)
		}
	}

	// a reader of the configuration sees the samples, but cannot send one
	reader := &utils.AuthContext{Nickname: "alice", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ}}
	req := httptest.NewRequest("GET", "http://cosmos.example/cosmos/api/telegram/kinds", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, reader)))
	var answer struct {
		Data struct {
			Kinds []struct {
				Kind    string `json:"kind"`
				Preview string `json:"preview"`
			} `json:"kinds"`
		} `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &answer) != nil || len(answer.Data.Kinds) < 20 {
		t.Fatalf("GET the kinds as a reader of the configuration: %d %.300s", w.Code, w.Body.String())
	}
	req = httptest.NewRequest("POST", "http://cosmos.example/cosmos/api/telegram/sample", strings.NewReader(`{"kind":"auth.login.failed"}`))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, reader)))
	if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Errorf("POST a sample as a reader: %d, want a refusal", w.Code)
	}
	// GET is not a way to send
	req = httptest.NewRequest("GET", "http://cosmos.example/cosmos/api/telegram/sample", nil)
	w = httptest.NewRecorder()
	admin := &utils.AuthContext{Nickname: "alice", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN}, IsSudoed: true}
	router.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, admin)))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET a sample: %d, want 405", w.Code)
	}
}
