package main

// Fork addition: what the UI needs to tell the admin about server updates and
// to let them decide when updates are installed.
//
//   - The "new version" notice is driven by this fork's own releases (the feed
//     the updater installs from) instead of upstream's version service, which
//     knows nothing about fork builds. The running version is no longer sent
//     to that service.
//   - /api/server-update reports the running and the latest version without
//     installing anything; installing stays with /api/force-server-update.
//   - The nightly maintenance (database retention, image clean-up, certificate
//     check and, when enabled, the automatic update) runs at the time set in
//     Config.MaintenanceTime instead of always at 02:00.
//   - After a restart the server only looks for an update; it no longer
//     installs one on its own three minutes after every start.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	defaultMaintenanceTime = "02:00"
	updateCheckInterval    = 6 * time.Hour
	updateCheckAtBoot      = 90 * time.Second
)

// ServerUpdateStatus is the last known answer to "is there a newer release".
type ServerUpdateStatus struct {
	CurrentVersion  string     `json:"currentVersion"`
	LatestVersion   string     `json:"latestVersion"`
	UpdateAvailable bool       `json:"updateAvailable"`
	Containerized   bool       `json:"containerized"`
	CheckedAt       *time.Time `json:"checkedAt,omitempty"`
	Error           string     `json:"error,omitempty"`
	AutoUpdate      bool       `json:"autoUpdate"`
	MaintenanceTime string     `json:"maintenanceTime"`
}

var (
	updateStatusMu  sync.Mutex
	updateStatus    ServerUpdateStatus
	updateCheckOnce sync.Once
	lastMaintenance string // the day the nightly maintenance last ran, as 2006-01-02
)

// refreshServerUpdateStatus asks the release feed and records the answer. It
// never installs anything.
func refreshServerUpdateStatus() ServerUpdateStatus {
	now := time.Now()
	status := ServerUpdateStatus{
		CurrentVersion: GetCosmosVersion(),
		Containerized:  utils.IsInsideContainer,
		CheckedAt:      &now,
	}

	if !utils.IsInsideContainer {
		check, _, err := findServerUpdate(utils.GetMainConfig().BetaUpdates)
		if err != nil {
			utils.Error("Update check", err)
			status.Error = "The release list could not be read"
		} else {
			status.LatestVersion = check.LatestVersion
			status.UpdateAvailable = check.UpdateAvailable
		}
	}

	updateStatusMu.Lock()
	updateStatus = status
	updateStatusMu.Unlock()

	utils.NewVersionAvailable = status.UpdateAvailable
	return withUpdateSettings(status)
}

func withUpdateSettings(status ServerUpdateStatus) ServerUpdateStatus {
	config := utils.GetMainConfig()
	hour, minute := maintenanceTime()
	status.AutoUpdate = config.AutoUpdate
	status.MaintenanceTime = twoDigits(hour) + ":" + twoDigits(minute)
	if status.CurrentVersion == "" {
		status.CurrentVersion = GetCosmosVersion()
		status.Containerized = utils.IsInsideContainer
	}
	return status
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// startUpdateChecks looks for a newer release shortly after start and then at
// a fixed interval, so the notice appears without waiting for the night.
func startUpdateChecks() {
	updateCheckOnce.Do(func() {
		go func() {
			time.Sleep(updateCheckAtBoot)
			for {
				refreshServerUpdateStatus()
				time.Sleep(updateCheckInterval)
			}
		}()
	})
}

// checkUpdatesAtBoot refreshes what is known about container and server
// updates after a start, without installing anything.
func checkUpdatesAtBoot() {
	utils.UpdateAvailable = docker.CheckUpdatesAvailable()
	refreshServerUpdateStatus()
}

// parseMaintenanceTime reads "HH:MM" (24 hours); ok is false for anything else.
func parseMaintenanceTime(value string) (hour int, minute int, ok bool) {
	if len(value) != 5 || value[2] != ':' {
		return 0, 0, false
	}
	for _, i := range []int{0, 1, 3, 4} {
		if value[i] < '0' || value[i] > '9' {
			return 0, 0, false
		}
	}
	hour, errH := strconv.Atoi(value[:2])
	minute, errM := strconv.Atoi(value[3:])
	if errH != nil || errM != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, false
	}
	return hour, minute, true
}

// maintenanceTime is the configured time of the nightly maintenance, or the
// default when none or an invalid one is set.
func maintenanceTime() (hour int, minute int) {
	if h, m, ok := parseMaintenanceTime(utils.GetMainConfig().MaintenanceTime); ok {
		return h, m
	}
	h, m, _ := parseMaintenanceTime(defaultMaintenanceTime)
	return h, m
}

// maintenanceDue is asked once a minute and answers true once a day, in the
// configured minute. Reading the setting each time means a changed time takes
// effect without a restart.
func maintenanceDue(now time.Time) bool {
	hour, minute := maintenanceTime()
	if now.Hour() != hour || now.Minute() != minute {
		return false
	}
	today := now.Format("2006-01-02")

	updateStatusMu.Lock()
	defer updateStatusMu.Unlock()
	if lastMaintenance == today {
		return false
	}
	lastMaintenance = today
	return true
}

// ServerUpdateRoute godoc
// @Summary Report whether a newer server release exists
// @Description GET returns the last known answer, POST asks the release feed again. Neither installs anything.
// @Tags system
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/server-update [get]
func ServerUpdateRoute(w http.ResponseWriter, req *http.Request) {
	// Reading the last answer is for anyone who may read the configuration;
	// asking the release feed again is an admin action, like installing.
	permission := utils.PERM_CONFIGURATION_READ
	if req.Method != "GET" {
		permission = utils.PERM_ADMIN
	}
	if utils.CheckPermissions(w, req, permission) != nil {
		return
	}

	var status ServerUpdateStatus
	switch req.Method {
	case "GET":
		updateStatusMu.Lock()
		status = updateStatus
		updateStatusMu.Unlock()
		status = withUpdateSettings(status)
	case "POST":
		utils.Log("API: Check for a server update")
		status = refreshServerUpdateStatus()
	default:
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}

	json.NewEncoder(w).Encode(utils.APIResponse{
		Status: "OK",
		Data:   status,
	})
}
