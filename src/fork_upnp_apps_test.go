package main

// Fork addition: tests of the server's side of the router port forwarding
// for apps (fork_upnp_apps.go) and of its hooks in upstream's files: a merge
// that drops one fails here.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/upnp"
	"github.com/azukaar/cosmos-server/src/utils"
)

func TestUPnPHooksAreInPlace(t *testing.T) {
	for file, hook := range map[string]string{
		// on the admin router: behind the token check, the SmartShield and
		// the admin address restriction
		"httpServer.go":    "registerUPnPAppRoutes(srapiAdmin)",
		"httpServer.go ":   `srapiAdmin.HandleFunc("/api/upnp", upnp.StatusRoute)`,
		"configapi/set.go": "keepUPnPAppPorts(&request, config)",
	} {
		source, err := os.ReadFile(strings.TrimSpace(file))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(source), hook) {
			t.Errorf("%s: %q is gone: a hook of the router port forwarding is missing", file, hook)
		}
	}
	// the save must keep the list after it re-read the stored configuration, under the lock
	source, _ := os.ReadFile("configapi/set.go")
	text := string(source)
	lock, keep, write := strings.Index(text, "utils.ConfigLock.Lock()"), strings.Index(text, "keepUPnPAppPorts(&request, config)"), strings.LastIndex(text, "utils.SetBaseMainConfig(request)")
	if !(lock < keep && keep < write) {
		t.Error("configapi/set.go: the app ports must be put back between taking the lock and writing the configuration")
	}
}

func TestUPnPRoutesAreRegisteredAndProtected(t *testing.T) {
	router := mux.NewRouter()
	api := router.PathPrefix("/cosmos").Subrouter()
	api.HandleFunc("/api/upnp", upnp.StatusRoute) // as httpServer.go does
	registerUPnPAppRoutes(api)

	serve := func(method string, path string, auth *utils.AuthContext) int {
		req := httptest.NewRequest(method, "http://cosmos.example"+path, strings.NewReader("{}"))
		if auth != nil {
			req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}

	// may read the settings, the logs, change containers and settings, password confirmed: still not the admin permission
	almost := &utils.AuthContext{Nickname: "alice", IsSudoed: true, Permissions: []utils.Permission{
		utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ, utils.PERM_ADMIN_READ, utils.PERM_RESOURCES, utils.PERM_CONFIGURATION,
	}}
	for _, path := range []string{"/cosmos/api/upnp", "/cosmos/api/upnp/apps", "/cosmos/api/upnp/close"} {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			if code := serve(method, path, nil); code != http.StatusUnauthorized {
				t.Errorf("%s %s without a session: %d, want 401", method, path, code)
			}
		}
		if code := serve("POST", path, almost); code != http.StatusUnauthorized {
			t.Errorf("POST %s without the admin permission: %d, want 401", path, code)
		}
	}
	if code := serve("GET", "/cosmos/api/upnp", almost); code != http.StatusOK {
		t.Errorf("the status for a reader of the settings: %d", code)
	}
	// nothing else is served below /api/upnp
	for _, path := range []string{"/cosmos/api/upnp/", "/cosmos/api/upnp/apps/jellyfin", "/cosmos/api/upnp/open", "/cosmos/api/upnp/foreign"} {
		if code := serve("POST", path, almost); code != http.StatusNotFound {
			t.Errorf("POST %s: %d, want 404", path, code)
		}
	}
}
