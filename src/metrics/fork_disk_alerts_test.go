package metrics

import (
	"reflect"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func upstreamRootAlert() utils.Alert {
	return utils.DefaultConfig.MonitoringAlerts["Disk Full Notification"]
}

func TestAddDiskAlerts(t *testing.T) {
	cases := []struct {
		name        string
		alerts      map[string]utils.Alert
		seeded      []string
		mountpoints []string
		wantChanged bool
		wantAlerts  []string // names, besides the ones that were there
		wantSeeded  []string
	}{
		{
			name:        "existing install: the data disks get an alert, / keeps upstream's",
			alerts:      map[string]utils.Alert{"Disk Full Notification": upstreamRootAlert()},
			mountpoints: []string{"/", "/archive", "/storage"},
			wantChanged: true,
			wantAlerts:  []string{"Disk Full Notification (archive)", "Disk Full Notification (storage)"},
			wantSeeded:  []string{"/archive", "/storage"},
		},
		{
			name:        "no alerts at all yet",
			mountpoints: []string{"/mnt/disk.1/data"},
			wantChanged: true,
			wantAlerts:  []string{"Disk Full Notification (mnt disk.1 data)"},
			wantSeeded:  []string{"/mnt/disk.1/data"},
		},
		{
			name:        "handled before: an alert the admin deleted stays deleted",
			alerts:      map[string]utils.Alert{"Disk Full Notification": upstreamRootAlert()},
			seeded:      []string{"/archive", "/storage"},
			mountpoints: []string{"/", "/archive", "/storage"},
			wantSeeded:  []string{"/archive", "/storage"},
		},
		{
			name:        "a disk that appears later gets its alert then",
			alerts:      map[string]utils.Alert{"Disk Full Notification": upstreamRootAlert()},
			seeded:      []string{"/storage"},
			mountpoints: []string{"/archive", "/storage"},
			wantChanged: true,
			wantAlerts:  []string{"Disk Full Notification (archive)"},
			wantSeeded:  []string{"/archive", "/storage"},
		},
		{
			name: "the admin's own alert on the disk is kept, no second one",
			alerts: map[string]utils.Alert{
				"My storage": {Name: "My storage", TrackingMetric: "cosmos.system.disk./storage", Condition: utils.AlertCondition{Operator: "gt", Value: 80, Percent: true}},
			},
			mountpoints: []string{"/storage"},
			wantChanged: true, // only the list of handled disks
			wantSeeded:  []string{"/storage"},
		},
		{
			name: "a name that is already taken is not overwritten",
			alerts: map[string]utils.Alert{
				"Disk Full Notification (storage)": {Name: "Disk Full Notification (storage)", TrackingMetric: "cosmos.system.cpu.0"},
			},
			mountpoints: []string{"/storage"},
			wantChanged: true,
			wantSeeded:  []string{"/storage"},
		},
		{
			name:        "only /: nothing to do",
			alerts:      map[string]utils.Alert{"Disk Full Notification": upstreamRootAlert()},
			mountpoints: []string{"/"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			config := utils.Config{DiskAlertsSeeded: c.seeded}
			before := map[string]utils.Alert{}
			if c.alerts != nil {
				config.MonitoringAlerts = map[string]utils.Alert{}
				for name, alert := range c.alerts {
					config.MonitoringAlerts[name] = alert
					before[name] = alert
				}
			}

			if changed := addDiskAlerts(&config, c.mountpoints); changed != c.wantChanged {
				t.Errorf("changed = %v, want %v", changed, c.wantChanged)
			}
			if !reflect.DeepEqual(config.DiskAlertsSeeded, c.wantSeeded) {
				t.Errorf("seeded = %v, want %v", config.DiskAlertsSeeded, c.wantSeeded)
			}

			for name, alert := range before {
				if !reflect.DeepEqual(config.MonitoringAlerts[name], alert) {
					t.Errorf("alert %q was changed", name)
				}
			}
			if len(config.MonitoringAlerts) != len(before)+len(c.wantAlerts) {
				t.Errorf("%d alerts, want %d: %v", len(config.MonitoringAlerts), len(before)+len(c.wantAlerts), config.MonitoringAlerts)
			}
			for _, name := range c.wantAlerts {
				alert, ok := config.MonitoringAlerts[name]
				if !ok {
					t.Errorf("alert %q is missing", name)
					continue
				}
				if alert.Name != name || !alert.Enabled || alert.Period != "latest" || !alert.Throttled {
					t.Errorf("alert %q = %+v", name, alert)
				}
			}
		})
	}
}

// The alert of a data disk is upstream's alert for "/", for another metric.
func TestDiskAlertMatchesUpstreamDefault(t *testing.T) {
	config := utils.Config{}
	addDiskAlerts(&config, []string{"/storage"})

	got := config.MonitoringAlerts["Disk Full Notification (storage)"]
	want := upstreamRootAlert()
	want.Name = "Disk Full Notification (storage)"
	want.TrackingMetric = "cosmos.system.disk./storage"
	if !reflect.DeepEqual(got, want) {
		t.Errorf("alert = %+v, want %+v", got, want)
	}
}

func TestDiskMetricKey(t *testing.T) {
	cases := map[string]string{
		"/":             "cosmos.system.disk./",
		"/storage":      "cosmos.system.disk./storage",
		"/mnt/disk.1/a": "cosmos.system.disk./mnt/disk_1/a",
	}
	for mountpoint, want := range cases {
		if got := diskMetricKey(mountpoint); got != want {
			t.Errorf("diskMetricKey(%q) = %q, want %q", mountpoint, got, want)
		}
	}
	if got := diskMetricKey("/"); got != upstreamRootAlert().TrackingMetric {
		t.Errorf("the key of / is %q, upstream's alert tracks %q", got, upstreamRootAlert().TrackingMetric)
	}
}

func TestUnseededDisks(t *testing.T) {
	config := utils.Config{DiskAlertsSeeded: []string{"/storage"}}
	got := unseededDisks(config, map[string]bool{"/": true, "/storage": true, "/archive": true, "/mnt/x": true})
	want := []string{"/archive", "/mnt/x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unseededDisks = %v, want %v", got, want)
	}
}
