package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestParseMaintenanceTime(t *testing.T) {
	valid := map[string][2]int{"02:00": {2, 0}, "00:00": {0, 0}, "23:59": {23, 59}, "04:30": {4, 30}}
	for value, want := range valid {
		hour, minute, ok := parseMaintenanceTime(value)
		if !ok || hour != want[0] || minute != want[1] {
			t.Errorf("parseMaintenanceTime(%q) = %d, %d, %v", value, hour, minute, ok)
		}
	}
	for _, value := range []string{"", "2:00", "24:00", "12:60", "12-30", "ab:cd", "02:00:00", "-1:00", "+2:30"} {
		if _, _, ok := parseMaintenanceTime(value); ok {
			t.Errorf("parseMaintenanceTime(%q) must be rejected", value)
		}
	}
}

func setMaintenanceTime(value string) {
	config := utils.Config{}
	config.MaintenanceTime = value
	utils.LoadBaseMainConfig(config)
	updateStatusMu.Lock()
	lastMaintenance = ""
	updateStatusMu.Unlock()
}

func TestMaintenanceDue(t *testing.T) {
	at := func(day, hour, minute, second int) time.Time {
		return time.Date(2026, 11, day, hour, minute, second, 0, time.Local)
	}

	setMaintenanceTime("04:30")
	if maintenanceDue(at(12, 4, 29, 59)) {
		t.Error("due a minute early")
	}
	if !maintenanceDue(at(12, 4, 30, 5)) {
		t.Error("not due at the configured time")
	}
	if maintenanceDue(at(12, 4, 30, 50)) {
		t.Error("due twice in the same minute")
	}
	if maintenanceDue(at(12, 4, 31, 0)) {
		t.Error("due after the configured minute")
	}
	if !maintenanceDue(at(13, 4, 30, 0)) {
		t.Error("not due the next day")
	}

	// no setting, or a broken one: 02:00 as before
	for _, value := range []string{"", "nonsense"} {
		setMaintenanceTime(value)
		if maintenanceDue(at(12, 4, 30, 0)) || !maintenanceDue(at(12, 2, 0, 0)) {
			t.Errorf("setting %q must fall back to 02:00", value)
		}
	}

	// changing the time takes effect without a restart, and can run again the same day
	setMaintenanceTime("03:00")
	if !maintenanceDue(at(14, 3, 0, 0)) {
		t.Error("not due at 03:00")
	}
	config := utils.Config{}
	config.MaintenanceTime = "05:00"
	utils.LoadBaseMainConfig(config)
	if maintenanceDue(at(14, 5, 0, 0)) {
		t.Error("ran twice on the same day after the time was changed")
	}
	if !maintenanceDue(at(15, 5, 0, 0)) {
		t.Error("not due at the new time the next day")
	}
}

// updateRequest builds a request from nobody ("none"), from an admin who has
// not confirmed their password ("admin") or from one who has ("sudo").
func updateRequest(method string, who string) *http.Request {
	req := httptest.NewRequest(method, "/cosmos/api/server-update", nil)
	if who != "none" {
		auth := &utils.AuthContext{
			Nickname:    "admin",
			Permissions: []utils.Permission{utils.PERM_ADMIN, utils.PERM_CONFIGURATION_READ},
			IsSudoed:    who == "sudo",
		}
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
	}
	return req
}

func TestServerUpdateRoute(t *testing.T) {
	setMaintenanceTime("04:30")

	// not logged in
	rec := httptest.NewRecorder()
	ServerUpdateRoute(rec, updateRequest("GET", "none"))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("without a session: status %d, want 401", rec.Code)
	}

	// asking the release feed again needs a confirmed admin
	rec = httptest.NewRecorder()
	ServerUpdateRoute(rec, updateRequest("POST", "admin"))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("POST without sudo: status %d, want 401", rec.Code)
	}

	// an admin reads the last known answer without sudo; nothing is fetched or installed
	rec = httptest.NewRecorder()
	ServerUpdateRoute(rec, updateRequest("GET", "admin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("admin GET: status %d: %s", rec.Code, rec.Body.String())
	}
	var reply struct {
		Status string
		Data   ServerUpdateStatus
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Status != "OK" || reply.Data.MaintenanceTime != "04:30" || reply.Data.UpdateAvailable || reply.Data.CheckedAt != nil {
		t.Errorf("unexpected reply before any check: %s", rec.Body.String())
	}

	// only GET and POST
	rec = httptest.NewRecorder()
	ServerUpdateRoute(rec, updateRequest("DELETE", "sudo"))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE: status %d, want 405", rec.Code)
	}
}
