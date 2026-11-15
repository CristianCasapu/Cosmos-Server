package main

// Fork addition: the blocks at Cloudflare's edge are wired in
// (fork_edgeblock.go): the routes on the admin router, and the note of the
// addresses administrators use.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/edgeblock"
	"github.com/azukaar/cosmos-server/src/utils"
)

func TestEdgeBlocksAreWired(t *testing.T) {
	raw, err := os.ReadFile("httpServer.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "registerEdgeBlockRoutes(srapiAdmin)") {
		t.Error("the routes are not on the admin router")
	}
}

func TestOnlyAnAdministratorsFullSessionMarksAnAddress(t *testing.T) {
	handler := securityAudit(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {}))
	cases := []struct {
		ip   string
		auth *utils.AuthContext
		want bool
	}{
		{"198.51.100.31", &utils.AuthContext{Nickname: "owner", UserRole: utils.ADMIN}, true},
		{"198.51.100.32", &utils.AuthContext{Nickname: "owner", UserRole: utils.ADMIN, MFAState: 1}, false}, // second factor not done
		{"198.51.100.33", &utils.AuthContext{Nickname: "bob", UserRole: utils.USER}, false},
		{"198.51.100.34", &utils.AuthContext{Nickname: "owner", UserRole: utils.ADMIN, APIToken: &utils.APITokenContext{Name: "t"}}, false},
		{"198.51.100.35", &utils.AuthContext{}, false},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/cosmos/api/status", nil)
		req.RemoteAddr = c.ip + ":5000"
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, c.auth))
		handler.ServeHTTP(httptest.NewRecorder(), req)
		if got := edgeblock.IsAdminAddress(c.ip); got != c.want {
			t.Errorf("%s %+v: noted %v", c.ip, c.auth, got)
		}
	}
}
