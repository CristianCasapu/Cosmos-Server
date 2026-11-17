package configapi

// Fork addition: tests of fork_jobs.go, through the real handler.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// jobs saved before the check: each would be refused if it were new
func oddOldJobs() map[string]utils.CRONConfig {
	return map[string]utils.CRONConfig{
		"five fields": {Name: "five fields", Enabled: true, Crontab: "0 3 * * *", Command: "echo five"},
		"no command":  {Name: "no command", Enabled: false, Crontab: "0 0 3 * * *", Command: ""},
		"gone":        {Name: "gone", Enabled: true, Crontab: "0 0 3 * * *", Command: "true", Container: "removed-long-ago"},
		"unreadable":  {Name: "unreadable", Enabled: true, Crontab: "nightly", Command: "true"},
	}
}

func saveRaw(t *testing.T, restarted chan struct{}, config utils.Config) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(config)
	w := httptest.NewRecorder()
	ConfigApiSet(w, asAdmin(httptest.NewRequest("PUT", "https://cosmos.example.com/cosmos/api/config", bytes.NewReader(body))))
	if w.Code == http.StatusOK {
		<-restarted
	}
	return w
}

func TestOldJobsStillSaveUnchanged(t *testing.T) {
	restarted := settingsTestServer(t)
	config := hostPowersConfig()
	config.CRON = oddOldJobs()
	utils.SaveConfigTofile(config)
	utils.LoadBaseMainConfig(config)

	settings := readSettingsAs(t, asAdmin)
	settings.ServerCountry = "RO"
	if w := saveRaw(t, restarted, settings); w.Code != http.StatusOK {
		t.Fatalf("the settings with old jobs: %d %s", w.Code, w.Body.String())
	}
	saved := utils.ReadConfigFromFile()
	if saved.ServerCountry != "RO" {
		t.Error("the change was not saved")
	}
	for name, job := range oddOldJobs() {
		if saved.CRON[name] != job {
			t.Errorf("%s: stored %+v, want %+v", name, saved.CRON[name], job)
		}
	}
}

func TestNewAndChangedJobsAreChecked(t *testing.T) {
	restarted := settingsTestServer(t)
	config := hostPowersConfig()
	config.CRON = oddOldJobs()
	utils.SaveConfigTofile(config)
	utils.LoadBaseMainConfig(config)

	bad := map[string]utils.CRONConfig{
		"a schedule gocron cannot read": {Name: "x", Crontab: "every night", Command: "true"},
		"no command":                    {Name: "x", Crontab: "0 0 3 * * *", Command: " "},
		"a container Docker cannot tell": {Name: "x", Crontab: "0 0 3 * * *", Command: "true", Container: "db"},
		"a name that is not its key":     {Name: "y", Crontab: "0 0 3 * * *", Command: "true"},
	}
	for what, job := range bad {
		settings := readSettingsAs(t, asAdmin)
		settings.CRON["x"] = job
		w := saveRaw(t, restarted, settings)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "UC042") {
			t.Errorf("%s: %d %s", what, w.Code, w.Body.String())
		}
		if _, stored := utils.ReadConfigFromFile().CRON["x"]; stored {
			t.Errorf("%s was stored", what)
		}
	}

	// an old odd job that is changed must now pass the check
	settings := readSettingsAs(t, asAdmin)
	job := settings.CRON["unreadable"]
	job.Command = "echo changed"
	settings.CRON["unreadable"] = job
	if w := saveRaw(t, restarted, settings); w.Code != http.StatusBadRequest {
		t.Errorf("a changed job with an unreadable schedule: %d", w.Code)
	}
	job.Crontab = "0 0 4 * * *"
	settings.CRON["unreadable"] = job
	if w := saveRaw(t, restarted, settings); w.Code != http.StatusOK {
		t.Errorf("the job repaired: %d %s", w.Code, w.Body.String())
	}
}
