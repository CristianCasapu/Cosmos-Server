package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/gorilla/mux"
)

// Fork addition: tests of fork_api_inputs.go.

func TestTokenInTheAddressIsForWebsocketsOnly(t *testing.T) {
	const token = "cosmos_0123456789abcdef0123456789abcdef"
	sum := sha256.Sum256([]byte(token))
	prev := utils.GetBaseMainConfig()
	config := utils.Config{}
	config.HTTPConfig.Hostname = "cosmos.example.com"
	config.APITokens = map[string]utils.APITokenConfig{
		"ci": {Name: "ci", Owner: "admin", TokenHash: hex.EncodeToString(sum[:]), Permissions: []utils.Permission{utils.PERM_ADMIN}},
	}
	utils.LoadBaseMainConfig(config)
	t.Cleanup(func() { utils.LoadBaseMainConfig(prev) })

	as := func(target string, headers map[string]string) string {
		who := "nobody"
		handler := tokenMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if auth := utils.GetAuthContext(r); auth.APIToken != nil {
				who = "token " + auth.APIToken.Name
			}
		}))
		req := httptest.NewRequest("GET", "https://cosmos.example.com"+target, nil)
		req.Host = "cosmos.example.com"
		req.RemoteAddr = "192.168.1.20:50000"
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		handler.ServeHTTP(httptest.NewRecorder(), req)
		return who
	}

	if who := as("/cosmos/api/terminal/bash?token="+token, map[string]string{"Upgrade": "websocket", "Connection": "Upgrade"}); who != "token ci" {
		t.Errorf("a websocket with the token in its address is %s, want the token", who)
	}
	if who := as("/cosmos/api/config", map[string]string{"Authorization": "Bearer " + token}); who != "token ci" {
		t.Errorf("the token in the Authorization header is %s, want the token", who)
	}
	if who := as("/cosmos/api/config?token="+token, nil); who != "nobody" {
		t.Errorf("a plain request with the token in its address is %s, want nobody", who)
	}
	if who := as("/cosmos/api/restart?token="+token, map[string]string{"Sec-Fetch-Mode": "navigate"}); who != "nobody" {
		t.Errorf("a link with the token in its address is %s, want nobody", who)
	}
}

func TestSignInRoutesReadABoundedBody(t *testing.T) {
	router := mux.NewRouter()
	strict := router.PathPrefix("/cosmos").Subrouter()
	limitRequestBodies(1024, strict)
	read, failed := 0, false
	strict.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		read, failed = len(data), err != nil
	})

	post := func(size int) {
		req := httptest.NewRequest("POST", "https://cosmos.example.com/cosmos/api/login", strings.NewReader(strings.Repeat("a", size)))
		router.ServeHTTP(httptest.NewRecorder(), req)
	}
	post(900)
	if read != 900 || failed {
		t.Errorf("a body within the limit: %d bytes read, failed %v", read, failed)
	}
	post(50000)
	if read > 1024 || !failed {
		t.Errorf("a body over the limit: %d bytes read, failed %v", read, failed)
	}
}
