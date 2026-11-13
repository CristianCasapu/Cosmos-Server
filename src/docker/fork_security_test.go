package docker

// Fork addition: tests of what the container code reports to the security
// record (fork_security.go).

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestSecurityContainerActionsAndTerminals(t *testing.T) {
	var mu sync.Mutex
	got := []string{}
	utils.SetSecurityRecorder(func(event utils.SecurityEvent) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, event.Kind+" "+event.Actor+">"+event.Target+" "+event.Details["action"]+event.Details["mode"])
	}, func() {})
	t.Cleanup(func() { utils.SetSecurityRecorder(nil, nil) })

	req := httptest.NewRequest("GET", "http://cosmos.example/cosmos/api/servapps/jellyfin/manage/stop", nil)
	req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{Nickname: "alice"}))

	for _, action := range []string{"stop", "start", "restart", "kill", "pause", "unpause", "recreate", "update", "remove", "explode", ""} {
		auditContainerAction(req, "jellyfin", action)
	}
	recordTerminal(req, "jellyfin", "new", false)
	recordTerminal(req, "jellyfin", "attach", false)
	recordTerminal(req, "jellyfin", "attach", true)

	want := []string{}
	for _, action := range []string{"stop", "start", "restart", "kill", "pause", "unpause", "recreate", "update"} {
		want = append(want, utils.SecContainerAction+" alice>jellyfin "+action)
	}
	want = append(want,
		utils.SecContainerDeleted+" alice>jellyfin ",
		utils.SecContainerTerminal+" alice>jellyfin new",
		utils.SecContainerTerminal+" alice>jellyfin attach",
		utils.SecContainerTerminal+" alice>jellyfin attach_readonly",
	)
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("recorded:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
