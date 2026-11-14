package configapi

// Fork addition: tests of fork_host_powers.go, through the real handlers.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/utils"
)

const storedBackupPassword = "STORED-backup-password"

func hostPowersConfig() utils.Config {
	config := utils.DefaultConfig
	config.NewInstall = false
	config.HTTPConfig.Hostname = "cosmos.example.com"
	config.HTTPConfig.HTTPPort = "2052"
	config.HTTPConfig.HTTPSPort = "2053"
	config.CRON = map[string]utils.CRONConfig{"tidy": {Name: "tidy", Enabled: true, Crontab: "0 0 3 * * *", Command: "echo tidy"}}
	config.Storage.SnapRAIDs = []utils.SnapRAIDConfig{{Name: "pool", Data: map[string]string{"d1": "/mnt/d1"}, Parity: []string{"/mnt/p1"}, SyncCrontab: "0 0 2 * * *"}}
	config.Backup.Backups = map[string]utils.SingleBackupConfig{"photos": {Name: "photos", Repository: "/mnt/backup/photos", Password: storedBackupPassword, Source: "/srv/photos", Crontab: "0 0 4 * * *"}}
	config.RemoteStorage.Shares = []utils.LocationRemoteStorageConfig{{Name: "docs", Protocol: "webdav", Source: "/srv/docs"}}
	config.Snapshots = utils.SnapshotsConfig{Enabled: false, Sources: []string{"/srv/photos"}}
	config.HTTPConfig.ProxyConfig.Routes = []utils.ProxyRouteConfig{
		{Name: "site", UseHost: true, Host: "site.example.com", Mode: "STATIC", Target: "/srv/site"},
		{Name: "app", UseHost: true, Host: "app.example.com", Mode: "PROXY", Target: "http://127.0.0.1:8080"},
	}
	return config
}

func hostPowersServer(t *testing.T) chan struct{} {
	t.Helper()
	restarted := settingsTestServer(t)
	config := hostPowersConfig()
	utils.SaveConfigTofile(config)
	utils.LoadBaseMainConfig(config)
	return restarted
}

// an API token with the configuration permissions only (30 and 31)
func asConfigurationToken(req *http.Request) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{
		Nickname: "admin",
		APIToken: &utils.APITokenContext{Name: "settings", Owner: "admin", Permissions: []utils.Permission{utils.PERM_CONFIGURATION_READ, utils.PERM_CONFIGURATION}},
	}))
}

func readSettingsAs(t *testing.T, as func(*http.Request) *http.Request) utils.Config {
	t.Helper()
	w := httptest.NewRecorder()
	ConfigApiGet(w, as(httptest.NewRequest("GET", "https://cosmos.example.com/cosmos/api/config", nil)))
	if w.Code != http.StatusOK {
		t.Fatalf("GET config: %d %s", w.Code, w.Body.String())
	}
	var got struct {
		Data utils.Config `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got.Data
}

func saveSettingsAs(t *testing.T, restarted chan struct{}, as func(*http.Request) *http.Request, config utils.Config) int {
	t.Helper()
	body, _ := json.Marshal(config)
	w := httptest.NewRecorder()
	ConfigApiSet(w, as(httptest.NewRequest("PUT", "https://cosmos.example.com/cosmos/api/config", bytes.NewReader(body))))
	if w.Code == http.StatusOK {
		select {
		case <-restarted:
		case <-time.After(10 * time.Second):
			t.Fatal("the restart that follows a save did not finish")
		}
	}
	return w.Code
}

// the changes a configuration-only caller may not make, each on the settings as read
var hostPowerChanges = map[string]func(c *utils.Config){
	"a scheduled job's command": func(c *utils.Config) {
		c.CRON["tidy"] = utils.CRONConfig{Name: "tidy", Enabled: true, Crontab: "0 0 3 * * *", Command: "echo changed"}
	},
	"a new scheduled job": func(c *utils.Config) {
		c.CRON["more"] = utils.CRONConfig{Name: "more", Enabled: true, Crontab: "* * * * * *", Command: "echo more"}
	},
	"SnapRAID": func(c *utils.Config) { c.Storage.SnapRAIDs[0].Data["d2"] = "/" },
	"a backup's source": func(c *utils.Config) {
		b := c.Backup.Backups["photos"]
		b.Source = "/"
		c.Backup.Backups["photos"] = b
	},
	"the backup folder":        func(c *utils.Config) { c.BackupOutputDir = "/srv/elsewhere" },
	"remote storage":           func(c *utils.Config) { c.RemoteStorage.Shares[0].Source = "/" },
	"incremental backups":      func(c *utils.Config) { c.Snapshots.Sources = []string{"/srv/music"} },
	"a folder URL's folder":    func(c *utils.Config) { c.HTTPConfig.ProxyConfig.Routes[0].Target = "/" },
	"a folder URL made public": func(c *utils.Config) { c.HTTPConfig.ProxyConfig.Routes[0].Host = "other.example.com" },
	"a URL turned into a folder URL": func(c *utils.Config) {
		c.HTTPConfig.ProxyConfig.Routes[1].Mode = "SPA"
		c.HTTPConfig.ProxyConfig.Routes[1].Target = "/"
	},
	"a new folder URL": func(c *utils.Config) {
		c.HTTPConfig.ProxyConfig.Routes = append(c.HTTPConfig.ProxyConfig.Routes, utils.ProxyRouteConfig{Name: "root", UseHost: true, Host: "root.example.com", Mode: "STATIC", Target: "/"})
	},
}

func TestConfigurationOnlyCannotChangeWhatRunsOnTheHost(t *testing.T) {
	for name, change := range hostPowerChanges {
		restarted := hostPowersServer(t)
		before, _ := json.Marshal(utils.ReadConfigFromFile())
		settings := readSettingsAs(t, asConfigurationToken)
		change(&settings)
		if code := saveSettingsAs(t, restarted, asConfigurationToken, settings); code != http.StatusForbidden {
			t.Errorf("%s by a configuration-only token: %d, want 403", name, code)
		}
		if after, _ := json.Marshal(utils.ReadConfigFromFile()); !bytes.Equal(before, after) {
			t.Errorf("%s by a configuration-only token changed the stored settings", name)
		}
	}
}

func TestConfigurationOnlyStillSavesTheSettingsPageUnchanged(t *testing.T) {
	restarted := hostPowersServer(t)
	settings := readSettingsAs(t, asConfigurationToken)
	// the page sends empty lists for what is stored as none
	settings.Storage.SnapRAIDs[0].Parity = append([]string{}, settings.Storage.SnapRAIDs[0].Parity...)
	settings.Snapshots.Excludes = []string{}
	settings.HTTPConfig.ProxyConfig.Routes[0].AdditionalTargets = nil
	if settings.Backup.Backups["photos"].Password != "***" {
		t.Fatalf("the backup password is not masked for this token: %q", settings.Backup.Backups["photos"].Password)
	}
	settings.ServerCountry = "RO" // something the configuration permission is for
	if code := saveSettingsAs(t, restarted, asConfigurationToken, settings); code != http.StatusOK {
		t.Fatalf("the settings sent back unchanged by a configuration-only token: %d", code)
	}
	saved := utils.ReadConfigFromFile()
	if saved.ServerCountry != "RO" || saved.Backup.Backups["photos"].Password != storedBackupPassword || saved.CRON["tidy"].Command != "echo tidy" {
		t.Errorf("after the save: country %q, backup password kept %v, job %q", saved.ServerCountry, saved.Backup.Backups["photos"].Password == storedBackupPassword, saved.CRON["tidy"].Command)
	}

	// deleting a folder URL needs no more than the configuration permission
	settings = readSettingsAs(t, asConfigurationToken)
	settings.HTTPConfig.ProxyConfig.Routes = settings.HTTPConfig.ProxyConfig.Routes[1:]
	if code := saveSettingsAs(t, restarted, asConfigurationToken, settings); code != http.StatusOK {
		t.Errorf("a folder URL deleted by a configuration-only token: %d", code)
	}
}

func TestAnAdministratorStillChangesWhatRunsOnTheHost(t *testing.T) {
	for name, change := range hostPowerChanges {
		restarted := hostPowersServer(t)
		settings := readSettingsAs(t, asAdmin)
		change(&settings)
		if code := saveSettingsAs(t, restarted, asAdmin, settings); code != http.StatusOK {
			t.Errorf("%s by an administrator: %d, want 200", name, code)
		}
	}
}

func TestFolderURLsThroughTheRouteAPIsNeedTheResourcesPermission(t *testing.T) {
	type call struct {
		name    string
		handler http.HandlerFunc
		method  string
		path    string
		vars    map[string]string
		body    interface{}
		allowed bool // for a configuration-only token
	}
	folder := utils.ProxyRouteConfig{Name: "root", UseHost: true, Host: "root.example.com", Mode: "STATIC", Target: "/"}
	stored := hostPowersConfig().HTTPConfig.ProxyConfig.Routes
	changedSite := stored[0]
	changedSite.Target = "/"
	appAsFolder := stored[1]
	appAsFolder.Mode = "STATIC"
	appAsFolder.Target = "/"
	siteAsProxy := stored[0]
	siteAsProxy.Mode = "PROXY"
	siteAsProxy.Target = "http://127.0.0.1:9090"
	calls := []call{
		{"create a folder URL", createRoute, "POST", "/cosmos/api/routes", nil, folder, false},
		{"change a folder URL", updateRoute, "PUT", "/cosmos/api/routes/site", map[string]string{"name": "site"}, changedSite, false},
		{"turn a URL into a folder URL", updateRoute, "PUT", "/cosmos/api/routes/app", map[string]string{"name": "app"}, appAsFolder, false},
		{"save a folder URL unchanged", updateRoute, "PUT", "/cosmos/api/routes/site", map[string]string{"name": "site"}, stored[0], true},
		{"turn a folder URL into a proxy", updateRoute, "PUT", "/cosmos/api/routes/site", map[string]string{"name": "site"}, siteAsProxy, true},
		{"add a folder URL", ConfigApiPatch, "PATCH", "/cosmos/api/config", nil, UpdateRouteRequest{Operation: "add", NewRoute: &folder}, false},
		{"replace a folder URL", ConfigApiPatch, "PATCH", "/cosmos/api/config", nil, UpdateRouteRequest{Operation: "replace", RouteName: "site", NewRoute: &changedSite}, false},
		{"replace a URL by a folder URL", ConfigApiPatch, "PATCH", "/cosmos/api/config", nil, UpdateRouteRequest{Operation: "replace", RouteName: "app", NewRoute: &appAsFolder}, false},
		{"delete a folder URL", ConfigApiPatch, "PATCH", "/cosmos/api/config", nil, UpdateRouteRequest{Operation: "delete", RouteName: "site"}, true},
		{"delete a folder URL by the route API", deleteRoute, "DELETE", "/cosmos/api/routes/site", map[string]string{"name": "site"}, nil, true},
	}
	run := func(c call, as func(*http.Request) *http.Request) int {
		hostPowersServer(t)
		body, _ := json.Marshal(c.body)
		req := as(httptest.NewRequest(c.method, "https://cosmos.example.com"+c.path, bytes.NewReader(body)))
		if c.vars != nil {
			req = mux.SetURLVars(req, c.vars)
		}
		w := httptest.NewRecorder()
		c.handler(w, req)
		return w.Code
	}
	for _, c := range calls {
		code := run(c, asConfigurationToken)
		if c.allowed && code != http.StatusOK {
			t.Errorf("%s by a configuration-only token: %d, want 200", c.name, code)
		}
		if !c.allowed && code != http.StatusForbidden {
			t.Errorf("%s by a configuration-only token: %d, want 403", c.name, code)
		}
		if code := run(c, asAdmin); code != http.StatusOK {
			t.Errorf("%s by an administrator: %d, want 200", c.name, code)
		}
	}
}
