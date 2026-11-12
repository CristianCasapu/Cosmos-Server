package metrics

import (
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestAlertTracksMetric(t *testing.T) {
	cases := []struct {
		tracking string
		key      string
		want     bool
	}{
		// the default alerts, against the keys the collectors really write
		{"cosmos.system.docker.cpu.*", "cosmos.system.docker.cpu.jellyfin", true},
		{"cosmos.system.docker.ram.*", "cosmos.system.docker.ram.sonarr", true},
		{"system.disk-health.temperature.*", "system.disk-health.temperature./dev/sda", true},
		{"cosmos.system.disk./", "cosmos.system.disk./", true},

		{"cosmos.system.docker.cpu.*", "cosmos.system.docker.ram.jellyfin", false},
		{"cosmos.system.docker.cpu.*", "cosmos.system.docker.cpu", false},
		{"cosmos.system.disk./", "cosmos.system.disk./storage", false},
		{"cosmos.system.cpu.0", "cosmos.system.cpu.01", false},

		// a wildcard in the middle, and the whole key has to match
		{"cosmos.proxy.*.success", "cosmos.proxy.route.jellyfin.success", true},
		{"cosmos.proxy.*.success", "cosmos.proxy.route.jellyfin.success.total", false},
		{"*.docker.cpu.*", "cosmos.system.docker.cpu.radarr", true},
		{"*", "anything", true},

		// the rest of the tracking metric is text, not a regular expression
		{"cosmos.system.docker.cpu.*", "cosmosXsystemXdockerXcpuXjellyfin", false},
		{"cosmos.system.disk./mnt/(a|b)*", "cosmos.system.disk./mnt/a", false},
		{"cosmos.system.disk./mnt/(a|b)*", "cosmos.system.disk./mnt/(a|b)c", true},
		{"cosmos.system.netRx[*", "cosmos.system.netRx[eth0", true},
	}

	for _, c := range cases {
		if got := alertTracksMetric(c.tracking, c.key); got != c.want {
			t.Errorf("alertTracksMetric(%q, %q) = %v, want %v", c.tracking, c.key, got, c.want)
		}
	}
}

func TestAlertsForMetric(t *testing.T) {
	alerts := map[string]utils.Alert{
		"Anti Crypto-Miner": {Name: "Anti Crypto-Miner", Period: "daily", TrackingMetric: "cosmos.system.docker.cpu.*"},
		"CPU now":           {Name: "CPU now", Period: "latest", TrackingMetric: "cosmos.system.docker.cpu.*"},
		"Jellyfin CPU":      {Name: "Jellyfin CPU", Period: "latest", TrackingMetric: "cosmos.system.docker.cpu.jellyfin"},
		"Jellyfin CPU 2":    {Name: "Jellyfin CPU 2", Period: "latest", TrackingMetric: "cosmos.system.docker.cpu.jellyfin", Enabled: true},
		"Memory":            {Name: "Memory", Period: "latest", TrackingMetric: "cosmos.system.docker.ram.*"},
	}

	names := func(found []utils.Alert) []string {
		list := []string{}
		for _, alert := range found {
			list = append(list, alert.Name)
		}
		return list
	}
	same := func(got []string, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	// the wildcard alert and both alerts on the exact metric, every time, in the same order
	for i := 0; i < 20; i++ {
		got := names(alertsForMetric(alerts, "cosmos.system.docker.cpu.jellyfin", "latest"))
		if !same(got, "CPU now", "Jellyfin CPU", "Jellyfin CPU 2") {
			t.Fatalf("latest alerts for jellyfin's CPU = %v", got)
		}
	}

	if got := names(alertsForMetric(alerts, "cosmos.system.docker.cpu.jellyfin", "daily")); !same(got, "Anti Crypto-Miner") {
		t.Errorf("daily alerts for jellyfin's CPU = %v", got)
	}
	if got := names(alertsForMetric(alerts, "cosmos.system.docker.cpu.emby", "latest")); !same(got, "CPU now") {
		t.Errorf("latest alerts for emby's CPU = %v", got)
	}
	if got := names(alertsForMetric(alerts, "cosmos.system.docker.ram.emby", "hourly")); len(got) != 0 {
		t.Errorf("hourly alerts for emby's memory = %v, want none", got)
	}
	if got := alertsForMetric(nil, "cosmos.system.cpu.0", "latest"); len(got) != 0 {
		t.Errorf("no alerts configured, got %v", got)
	}

	// the alert comes back whole
	if found := alertsForMetric(alerts, "cosmos.system.docker.cpu.jellyfin", "latest"); !found[2].Enabled {
		t.Error("the alert's own settings were lost")
	}
}
