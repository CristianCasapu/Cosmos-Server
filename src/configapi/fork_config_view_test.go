package configapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: tests of fork_config_view.go, through the real handler.

func asPlainUser(req *http.Request) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{
		Nickname:    "child",
		Role:        utils.USER,
		UserRole:    utils.USER,
		Permissions: utils.GetRolePermissions(utils.USER),
	}))
}

func asAdminWithoutPassword(req *http.Request) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{
		Nickname:    "admin",
		Role:        utils.USER, // the effective role of an admin who has not confirmed the password
		UserRole:    utils.ADMIN,
		Permissions: utils.GetRolePermissions(utils.ADMIN),
		IsSudoed:    false,
	}))
}

func TestPlainUserGetsTheConfigurationWithoutItsSensitiveParts(t *testing.T) {
	settingsTestServer(t)

	config := utils.DefaultConfig
	config.NewInstall = false
	config.HTTPConfig.Hostname = "cosmos.example.com"
	config.HTTPConfig.TrustedProxies = []string{"10.9.9.9"}
	config.AdminWhitelistIPs = []string{"203.0.113.77"}
	config.APITokens = map[string]utils.APITokenConfig{"ci": {Name: "ci", TokenHash: "HASH-OF-THE-TOKEN", TokenSuffix: "abcd", Permissions: []utils.Permission{utils.PERM_ADMIN}}}
	config.OpenIDClients = []utils.OpenIDClient{{ID: "grafana", Secret: "BCRYPT-OF-THE-SECRET", Redirect: "https://grafana.example.com/cb"}}
	config.CRON = map[string]utils.CRONConfig{"sync": {Name: "sync", Enabled: false, Crontab: "0 0 3 * * *", Command: "rsync --password=CRON-SECRET /a /b"}}
	config.BackupOutputDir = "/storage/PRIVATE-BACKUP-PATH"
	config.HTTPConfig.ProxyConfig.Routes = []utils.ProxyRouteConfig{
		{Name: "family", UseHost: true, Host: "photos.example.com", Mode: "PROXY", Target: "http://127.0.0.1:2342",
			ExtraHeaders: map[string]string{"Authorization": "Bearer UPSTREAM-KEY"}, WhitelistInboundIPs: []string{"198.51.100.4"}},
		{Name: "ops", UseHost: true, Host: "ops.example.com", Mode: "PROXY", Target: "http://127.0.0.1:9000", AdminOnly: true},
	}
	utils.SaveConfigTofile(config)
	utils.LoadBaseMainConfig(config)

	get := func(as func(*http.Request) *http.Request) string {
		w := httptest.NewRecorder()
		ConfigApiGet(w, as(httptest.NewRequest("GET", "https://cosmos.example.com/cosmos/api/config", nil)))
		if w.Code != http.StatusOK {
			t.Fatalf("GET /api/config: %d %s", w.Code, w.Body.String())
		}
		return w.Body.String()
	}

	secrets := []string{"HASH-OF-THE-TOKEN", "BCRYPT-OF-THE-SECRET", "CRON-SECRET", "UPSTREAM-KEY", "203.0.113.77", "198.51.100.4", "10.9.9.9", "PRIVATE-BACKUP-PATH"}

	plain := get(asPlainUser)
	for _, secret := range secrets {
		if strings.Contains(plain, secret) {
			t.Errorf("a plain user is sent %q", secret)
		}
	}
	// what the home page of a plain user is built from is still there
	for _, needed := range []string{`"photos.example.com"`, `"family"`, `"cosmos.example.com"`} {
		if !strings.Contains(plain, needed) {
			t.Errorf("a plain user no longer gets %s", needed)
		}
	}
	if strings.Contains(plain, "ops.example.com") {
		t.Error("a plain user is sent a route for administrators")
	}

	// an administrator reads the configuration as before, also before confirming the password
	for name, as := range map[string]func(*http.Request) *http.Request{"an admin": asAdmin, "an admin who has not confirmed the password": asAdminWithoutPassword} {
		full := get(as)
		for _, secret := range secrets {
			if !strings.Contains(full, secret) {
				t.Errorf("%s no longer gets %q", name, secret)
			}
		}
	}

	// the stored configuration is untouched
	if stored := utils.ReadConfigFromFile(); stored.APITokens["ci"].TokenHash != "HASH-OF-THE-TOKEN" || stored.CRON["sync"].Command == "" {
		t.Error("reading the configuration changed what is stored")
	}
}
