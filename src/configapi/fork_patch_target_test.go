package configapi

// Fork addition: tests of fork_patch_target.go.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestPatchingAServAppRouteWithAnOddTargetAnswers(t *testing.T) {
	for _, target := range []string{"nocolon", "a:", "http://", "http://app:8080", ""} {
		hostPowersServer(t)
		route := utils.ProxyRouteConfig{Name: "odd", UseHost: true, Host: "odd.example.com", Mode: "SERVAPP", Target: target}
		body, _ := json.Marshal(UpdateRouteRequest{Operation: "add", NewRoute: &route})
		w := httptest.NewRecorder()
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("target %q: the handler panicked: %v", target, r)
				}
			}()
			ConfigApiPatch(w, asAdmin(httptest.NewRequest("PATCH", "https://cosmos.example.com/cosmos/api/config", bytes.NewReader(body))))
		}()
		if w.Code != http.StatusOK {
			t.Errorf("target %q: %d", target, w.Code)
		}
	}
	if got := servAppTargetName("http://app:8080"); got != "app" {
		t.Errorf("servAppTargetName: %q", got)
	}
}
