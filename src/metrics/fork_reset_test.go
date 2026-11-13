package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: tests of fork_reset.go.

func resetRequest(method string) *http.Request {
	req := httptest.NewRequest(method, "https://cosmos.example.com/cosmos/api/reset-metrics", nil)
	ctx := context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{
		Nickname:    "admin",
		Role:        utils.ADMIN,
		UserRole:    utils.ADMIN,
		Permissions: utils.GetRolePermissions(utils.ADMIN),
		IsSudoed:    true,
	})
	return req.WithContext(ctx)
}

func TestResetMetricsLooksAtTheMethodBeforeDeleting(t *testing.T) {
	lock <- true
	prevBuffer, prevInserted := dataBuffer, lastInserted
	dataBuffer = map[string]DataPush{"cosmos.system.cpu.0": {Key: "cosmos.system.cpu.0"}}
	lastInserted = map[string]int{"cosmos.system.cpu.0": 7}
	<-lock
	t.Cleanup(func() {
		lock <- true
		dataBuffer, lastInserted = prevBuffer, prevInserted
		<-lock
	})

	for _, method := range []string{"POST", "PUT", "DELETE", "HEAD", "OPTIONS"} {
		w := httptest.NewRecorder()
		API_ResetMetrics(w, resetRequest(method))
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: got %d, want 405", method, w.Code)
		}
		lock <- true
		kept := len(dataBuffer) == 1 && lastInserted["cosmos.system.cpu.0"] == 7
		<-lock
		if !kept {
			t.Fatalf("%s was refused, but the metrics waiting to be saved were dropped first", method)
		}
	}

	// without the permission nothing is touched either, whatever the method
	w := httptest.NewRecorder()
	API_ResetMetrics(w, httptest.NewRequest("GET", "https://cosmos.example.com/cosmos/api/reset-metrics", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("without a session: got %d, want 401", w.Code)
	}
	lock <- true
	kept := len(dataBuffer) == 1
	<-lock
	if !kept {
		t.Error("a request without a session dropped the metrics waiting to be saved")
	}
}
