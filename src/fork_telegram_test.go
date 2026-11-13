package main

// Fork addition: tests of the server's side of the Telegram notifications
// (fork_telegram.go), and of the hooks in files that are not the fork's
// own: a merge that drops one fails here.

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

// telegramHooks are the hooks of the Telegram notifications: where, and
// what the line contains. When upstream moves the code around a hook,
// re-apply the hook and adjust the entry; do not delete the entry.
var telegramHooks = []struct {
	file string
	text string
}{
	{"index.go", "startTelegram()"},
	{"httpServer.go", "registerTelegramRoutes(srapiAdmin)"},
	{"fork_updates.go", "announceServerUpdate(status)"},
	{"CRON.go", "utils.ForkNotice(utils.NoticeExitUpdate, \"version\", updates.Version)"},
	{"CRON.go", "utils.ForkNotice(utils.NoticeUpdateFailed, "},
	{"utils/utils.go", "ForkNotice(NoticeExitRestart)"},
	{"utils/notifications.go", "forwardNotification(notification)"},
	{"configapi/get.go", "maskTelegram(&config, canReadCredentials)"},
	{"configapi/set.go", "keepTelegramSecrets(&request, config)"},
	{"configapi/set.go", "announceTelegramChange(req, config, request)"},
	{"docker/events.go", "noteContainerEvent((string)(msg.Action), msg.Actor.Attributes)"},
}

func TestTelegramHooksAreInPlace(t *testing.T) {
	for _, hook := range telegramHooks {
		content, err := os.ReadFile(hook.file)
		if err != nil {
			t.Errorf("%s: %v", hook.file, err)
			continue
		}
		if !strings.Contains(string(content), hook.text) {
			t.Errorf("%s: %q is gone: a hook of the Telegram notifications is missing", hook.file, hook.text)
		}
	}

	// the notifier is started after the settings are loaded: before, there is nothing to send with
	index, _ := os.ReadFile("index.go")
	load, start := strings.Index(string(index), "\tLoadConfig()\n"), strings.Index(string(index), "startTelegram()")
	if load < 0 || start < load {
		t.Error("startTelegram() does not come after LoadConfig()")
	}

	// the reason is handed over before the process ends
	cron, _ := os.ReadFile("CRON.go")
	apply := string(cron)[strings.Index(string(cron), "func applyServerUpdate("):]
	notice, exit := strings.Index(apply, "utils.NoticeExitUpdate"), strings.Index(apply, "os.Exit(0)")
	if notice < 0 || exit < notice {
		t.Error("applyServerUpdate does not say that it ends for an update before it exits")
	}

	// the mask is applied before the answer is written, whoever asks
	get, _ := os.ReadFile("configapi/get.go")
	mask, answer := strings.Index(string(get), "maskTelegram("), strings.Index(string(get), "json.NewEncoder(w).Encode(map[string]interface{}{")
	if mask < 0 || answer < mask {
		t.Error("the Telegram token is not masked before the configuration is sent")
	}
}

func TestTelegramRoutesAreRegisteredAndProtected(t *testing.T) {
	router := mux.NewRouter()
	api := router.PathPrefix("/cosmos").Subrouter()
	registerTelegramRoutes(api)

	for _, request := range [][2]string{{"GET", "/cosmos/api/telegram"}, {"POST", "/cosmos/api/telegram/test"}} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(request[0], "http://cosmos.example"+request[1], strings.NewReader("{}")))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d, want 401", request[0], request[1], w.Code)
		}
	}

	reader := &utils.AuthContext{Nickname: "alice", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ}}
	req := httptest.NewRequest("GET", "http://cosmos.example/cosmos/api/telegram", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, reader)))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("GET the status as a reader of the configuration: %d %s", w.Code, w.Body.String())
	}

	// an admin who did not confirm the password cannot send
	admin := &utils.AuthContext{Nickname: "alice", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN}}
	req = httptest.NewRequest("POST", "http://cosmos.example/cosmos/api/telegram/test", strings.NewReader("{}"))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, admin)))
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "HTTP008") {
		t.Fatalf("POST the test without the password confirmation: %d %s", w.Code, w.Body.String())
	}
}

func TestTelegramUpdateAnnouncement(t *testing.T) {
	type seen struct {
		kind   string
		values map[string]string
	}
	got := []seen{}
	utils.SetForkNoticeHandler(func(kind string, values map[string]string) { got = append(got, seen{kind, values}) })
	defer utils.SetForkNoticeHandler(nil)

	previous := utils.GetBaseMainConfig()
	defer utils.LoadBaseMainConfig(previous)

	// nothing newer, or nothing known: nothing to say
	announceServerUpdate(ServerUpdateStatus{CurrentVersion: "0.24.0-fork.8", LatestVersion: "0.24.0-fork.8"})
	announceServerUpdate(ServerUpdateStatus{CurrentVersion: "0.24.0-fork.8", UpdateAvailable: true})
	if len(got) != 0 {
		t.Fatalf("announced %+v", got)
	}

	config := utils.Config{AutoUpdate: true, MaintenanceTime: "03:30"}
	utils.LoadBaseMainConfig(config)
	announceServerUpdate(ServerUpdateStatus{CurrentVersion: "0.24.0-fork.8", LatestVersion: "0.24.0-fork.9", UpdateAvailable: true})
	config.AutoUpdate = false
	utils.LoadBaseMainConfig(config)
	announceServerUpdate(ServerUpdateStatus{CurrentVersion: "0.24.0-fork.8", LatestVersion: "0.24.0-fork.9", UpdateAvailable: true})

	if len(got) != 2 || got[0].kind != utils.NoticeUpdateAvailable {
		t.Fatalf("announced %+v", got)
	}
	if v := got[0].values; v["version"] != "0.24.0-fork.9" || v["current"] != "0.24.0-fork.8" || v["at"] != "03:30" {
		t.Errorf("with automatic updates: %+v", v)
	}
	if got[1].values["at"] != "" {
		t.Errorf("without automatic updates a time is given: %+v", got[1].values)
	}
}
