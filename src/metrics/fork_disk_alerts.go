package metrics

// Fork addition: a default "disk full" alert for every data disk.
//
// Upstream ships one such alert, for "/", and writes the default alerts only
// when the configuration file is first created. The data disks are not known
// then (and an existing install never gets new defaults), so the alerts for
// them are added while the disk usage is collected: once per disk, the first
// time it is seen. Config.DiskAlertsSeeded remembers which disks were handled,
// so an alert the admin deletes or changes is left alone.

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const diskAlertName = "Disk Full Notification"

var forkDiskAlertsLock sync.Mutex

// diskMetricKey is the tracking metric of a mount point, as GetSystemMetrics
// and pushSetMetric name it.
func diskMetricKey(mountpoint string) string {
	return "cosmos.system.disk." + strings.Replace(mountpoint, ".", "_", -1)
}

// diskAlertNameFor names the alert of a mount point. The name has no "/": it
// is part of the URL of /api/alerts/{name}.
func diskAlertNameFor(mountpoint string) string {
	return diskAlertName + " (" + strings.ReplaceAll(strings.Trim(mountpoint, "/"), "/", " ") + ")"
}

// addDiskAlerts gives every mount point that was not handled before the same
// alert upstream defines for "/", and records it as handled. "/" itself is
// upstream's. It reports whether the configuration changed.
func addDiskAlerts(config *utils.Config, mountpoints []string) bool {
	seeded := map[string]bool{}
	for _, mountpoint := range config.DiskAlertsSeeded {
		seeded[mountpoint] = true
	}

	tracked := map[string]bool{}
	for _, alert := range config.MonitoringAlerts {
		tracked[alert.TrackingMetric] = true
	}

	changed := false
	for _, mountpoint := range mountpoints {
		if mountpoint == "/" || mountpoint == "" || seeded[mountpoint] {
			continue
		}
		seeded[mountpoint] = true
		config.DiskAlertsSeeded = append(config.DiskAlertsSeeded, mountpoint)
		changed = true

		name := diskAlertNameFor(mountpoint)
		metric := diskMetricKey(mountpoint)
		if _, exists := config.MonitoringAlerts[name]; exists || tracked[metric] {
			// the admin already watches this disk, or uses the name for something else
			continue
		}

		if config.MonitoringAlerts == nil {
			config.MonitoringAlerts = map[string]utils.Alert{}
		}
		config.MonitoringAlerts[name] = utils.Alert{
			Name:           name,
			Enabled:        true,
			Period:         "latest",
			TrackingMetric: metric,
			LastTriggered:  time.Time{},
			Condition: utils.AlertCondition{
				Operator: "gt",
				Value:    95,
				Percent:  true,
			},
			Actions: []utils.AlertAction{
				{Type: "notification", Target: ""},
			},
			Throttled: true,
			Severity:  "warn",
		}
		tracked[metric] = true
	}

	sort.Strings(config.DiskAlertsSeeded)
	return changed
}

// unseededDisks returns the mount points that never got their default alert.
func unseededDisks(config utils.Config, mountpoints map[string]bool) []string {
	seeded := map[string]bool{}
	for _, mountpoint := range config.DiskAlertsSeeded {
		seeded[mountpoint] = true
	}

	pending := []string{}
	for mountpoint := range mountpoints {
		if mountpoint != "/" && mountpoint != "" && !seeded[mountpoint] {
			pending = append(pending, mountpoint)
		}
	}
	sort.Strings(pending)
	return pending
}

// forkSeedDiskAlerts is called with the data disks each time the disk usage
// is collected. It only touches the configuration file when a disk shows up
// that was never handled, and not before the setup wizard is finished.
func forkSeedDiskAlerts(mountpoints map[string]bool) {
	forkDiskAlertsLock.Lock()
	defer forkDiskAlertsLock.Unlock()

	current := utils.GetMainConfig()
	if current.NewInstall || len(unseededDisks(current, mountpoints)) == 0 {
		return
	}

	utils.ConfigLock.Lock()
	defer utils.ConfigLock.Unlock()

	// the file, not the copy in memory: that one carries values from the environment
	config := utils.ReadConfigFromFile()
	pending := unseededDisks(config, mountpoints)
	if config.NewInstall || !addDiskAlerts(&config, pending) {
		return
	}

	utils.SetBaseMainConfig(config)
	utils.Log("Metrics - Added the default disk full alert for " + strings.Join(pending, ", "))
}
