package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition (plan 49): /api/status still needs a session, and its
// "visitor" says where the visitor is and nothing else.
func TestStatusVisitor(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///nonexistent/docker.sock")
	prev := utils.GetBaseMainConfig()
	config := utils.Config{}
	config.NewInstall = false
	utils.LoadBaseMainConfig(config)
	t.Cleanup(func() { utils.LoadBaseMainConfig(prev) })

	call := func(auth *utils.AuthContext) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "https://cosmos.example.org/cosmos/api/status", nil)
		req.RemoteAddr = "198.51.100.20:4000"
		if auth != nil {
			req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
		}
		w := httptest.NewRecorder()
		utils.ClientRealIP(http.HandlerFunc(StatusRoute)).ServeHTTP(w, req)
		return w
	}
	if w := call(nil); w.Code != http.StatusUnauthorized {
		t.Errorf("without a session /api/status answers %d, want 401", w.Code)
	}
	w := call(&utils.AuthContext{Nickname: "alice", Role: utils.USER, UserRole: utils.USER, Permissions: utils.GetRolePermissions(utils.USER)})
	if w.Code != http.StatusOK {
		t.Fatalf("with a session: %d %s", w.Code, w.Body.String())
	}
	var answer struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	var visitor map[string]interface{}
	if err := json.Unmarshal(answer.Data["visitor"], &visitor); err != nil {
		t.Fatalf("no visitor: %s", w.Body.String())
	}
	if len(visitor) != 2 || visitor["where"] != "away" || visitor["sure"] != true {
		t.Errorf("visitor = %v, want {where: away, sure: true} and nothing else", visitor)
	}
}
