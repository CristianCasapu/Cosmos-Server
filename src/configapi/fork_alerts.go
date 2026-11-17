package configapi

// Fork addition (plan 47, I2d): the monitoring alerts saved with the whole
// settings are checked like the alert editor checks them
// (metrics/fork_alert_check.go). Only an alert that is new or changed is
// looked at, so an alert saved earlier never blocks saving something else.

import (
	"github.com/azukaar/cosmos-server/src/metrics"
	"github.com/azukaar/cosmos-server/src/utils"
)

// checkAlerts is the hook of PUT /api/config.
func checkAlerts(request *utils.Config, stored utils.Config) *httpSettingsProblem {
	if err := metrics.CheckChangedAlerts(request.MonitoringAlerts, stored.MonitoringAlerts); err != nil {
		return &httpSettingsProblem{"UC042", err.Error()}
	}
	return nil
}
