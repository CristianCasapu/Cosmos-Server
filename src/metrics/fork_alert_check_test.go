package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition (plan 47): tests of fork_alert_check.go.

// what the alert editor sends for a disk template (AlertPage.jsx saveAlert)
func diskAlert() utils.Alert {
	return utils.Alert{
		Name: "Disk nearly full mnt data", Enabled: true, TrackingMetric: "cosmos.system.disk./mnt/data",
		Condition: utils.AlertCondition{Operator: "gt", Value: 90, Percent: true},
		Period:    "latest", Actions: []utils.AlertAction{{Type: "notification"}}, Throttled: true, Severity: "warn",
	}
}

func TestGoodAlertsPass(t *testing.T) {
	good := []utils.Alert{
		diskAlert(),
		{Name: "Memory nearly full", TrackingMetric: "cosmos.system.ram", Condition: utils.AlertCondition{Operator: "gt", Value: 90, Percent: true}, Period: "hourly", Severity: "warn"},
		{Name: "Processor busy", TrackingMetric: "cosmos.system.cpu.0", Condition: utils.AlertCondition{Operator: "gt", Value: 90}, Period: "hourly", Severity: "error", Actions: []utils.AlertAction{{Type: "email"}}},
		{Name: "Jellyfin eats memory", TrackingMetric: "cosmos.system.docker.ram.*", Condition: utils.AlertCondition{Operator: "gt", Value: 50, Percent: true}, Period: "daily", Severity: "info", Actions: []utils.AlertAction{{Type: "restart"}}},
		{Name: "Quiet", TrackingMetric: "cosmos.system.temp.all", Condition: utils.AlertCondition{Operator: "lt", Value: -5}, Period: "latest", Severity: ""},
	}
	for _, alert := range good {
		if err := CheckAlert(alert.Name, alert); err != nil {
			t.Errorf("%s refused: %v", alert.Name, err)
		}
	}
}

func TestTheDefaultAlertsPass(t *testing.T) {
	if len(utils.DefaultConfig.MonitoringAlerts) == 0 {
		t.Fatal("no default alerts to check")
	}
	if err := CheckChangedAlerts(utils.DefaultConfig.MonitoringAlerts, nil); err != nil {
		t.Fatalf("a default alert is refused: %v", err)
	}
}

func TestBadAlertsAreRefusedInWords(t *testing.T) {
	cases := map[string]struct {
		change func(a *utils.Alert)
		words  string
	}{
		"no name":        {func(a *utils.Alert) { a.Name = " " }, "Give the alert a name"},
		"long name":      {func(a *utils.Alert) { a.Name = strings.Repeat("x", 65) }, "at most 64"},
		"slash":          {func(a *utils.Alert) { a.Name = "a/b" }, "cannot hold"},
		"line break":     {func(a *utils.Alert) { a.Name = "a\nb" }, "cannot hold"},
		"no metric":      {func(a *utils.Alert) { a.TrackingMetric = "" }, "pick what to watch"},
		"odd metric":     {func(a *utils.Alert) { a.TrackingMetric = "cosmos.system.ram\x00" }, "not the name of a metric"},
		"operator":       {func(a *utils.Alert) { a.Condition.Operator = "ge" }, "above, below or exactly"},
		"no operator":    {func(a *utils.Alert) { a.Condition.Operator = "" }, "above, below or exactly"},
		"period":         {func(a *utils.Alert) { a.Period = "weekly" }, "average"},
		"percent 101":    {func(a *utils.Alert) { a.Condition.Value = 101 }, "from 0 to 100"},
		"percent of cpu": {func(a *utils.Alert) { a.TrackingMetric = "cosmos.system.cpu.0" }, "no maximum"},
		"percent of net": {func(a *utils.Alert) { a.TrackingMetric = "cosmos.system.docker.netRx.web" }, "no maximum"},
		"severity":       {func(a *utils.Alert) { a.Severity = "critical" }, "severity"},
		"unknown action": {func(a *utils.Alert) { a.Actions = []utils.AlertAction{{Type: "shell"}} }, "does not exist"},
		"stop a disk":    {func(a *utils.Alert) { a.Actions = []utils.AlertAction{{Type: "stop"}} }, "act on a container"},
		"many actions":   {func(a *utils.Alert) { a.Actions = make([]utils.AlertAction, 9) }, "at most 8"},
		"odd target":     {func(a *utils.Alert) { a.Actions = []utils.AlertAction{{Type: "email", Target: "a\nb"}} }, "plain text"},
	}
	for name, tc := range cases {
		alert := diskAlert()
		tc.change(&alert)
		err := CheckAlert(alert.Name, alert)
		if err == nil || !strings.Contains(err.Error(), tc.words) {
			t.Errorf("%s: %v, want %q", name, err, tc.words)
		}
	}
	// filed under another key
	if err := CheckAlert("other", diskAlert()); err == nil {
		t.Error("an alert under another key was accepted")
	}
}

func TestOnlyNewOrChangedAlertsAreChecked(t *testing.T) {
	odd := utils.Alert{Name: "Odd/old", TrackingMetric: "", Period: "weekly"}
	stored := map[string]utils.Alert{"Odd/old": odd, "Disk": withName(diskAlert(), "Disk")}

	// unchanged, and the time it last went off changed by the server: saved as it is
	next := map[string]utils.Alert{"Odd/old": odd, "Disk": withName(diskAlert(), "Disk")}
	fired := next["Disk"]
	fired.LastTriggered = time.Now()
	next["Disk"] = fired
	if err := CheckChangedAlerts(next, stored); err != nil {
		t.Fatalf("unchanged alerts refused: %v", err)
	}
	// switched on: changed, so checked
	switched := odd
	switched.Enabled = true
	next["Odd/old"] = switched
	if err := CheckChangedAlerts(next, stored); err == nil {
		t.Fatal("a changed odd alert was accepted")
	}
	// deleting it is fine
	delete(next, "Odd/old")
	if err := CheckChangedAlerts(next, stored); err != nil {
		t.Fatal(err)
	}
	// a new bad one next to them is refused
	next["New"] = utils.Alert{Name: "New", TrackingMetric: "cosmos.system.ram", Period: "latest", Condition: utils.AlertCondition{Operator: "gt", Value: 200, Percent: true}}
	if err := CheckChangedAlerts(next, stored); err == nil || !strings.Contains(err.Error(), "0 to 100") {
		t.Fatalf("%v", err)
	}
}

func TestAlertUpdateRoute(t *testing.T) {
	odd := utils.Alert{Name: "Odd", Period: "weekly"}
	if err := alertUpdateProblem("Odd", odd, odd); err != nil {
		t.Errorf("an unchanged alert sent back is refused: %v", err)
	}
	renamed := odd
	renamed.Name = "Odd 2"
	if err := alertUpdateProblem("Odd", renamed, odd); err == nil {
		t.Error("a renamed odd alert was accepted")
	}
	good := withName(diskAlert(), "")
	if err := alertUpdateProblem("Disk", good, odd); err != nil {
		t.Errorf("a good alert without a name in the body: %v", err)
	}
}

func withName(a utils.Alert, name string) utils.Alert {
	a.Name = name
	return a
}
