package configapi

import (
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition (plan 47): the whole-settings save checks new and changed alerts only.
func TestAlertsCheckedOnSave(t *testing.T) {
	stored := utils.Config{MonitoringAlerts: map[string]utils.Alert{"Odd": {Name: "Odd", Period: "weekly"}}}
	request := stored
	if p := checkAlerts(&request, stored); p != nil {
		t.Fatalf("an unchanged odd alert blocks the save: %v", p)
	}
	request = utils.Config{MonitoringAlerts: map[string]utils.Alert{
		"Odd": {Name: "Odd", Period: "weekly"},
		"Hot": {Name: "Hot", TrackingMetric: "cosmos.system.temp.all", Period: "latest", Condition: utils.AlertCondition{Operator: "gt", Value: 80, Percent: true}},
	}}
	if p := checkAlerts(&request, stored); p == nil || p.Code != "UC042" {
		t.Fatalf("a percentage of a temperature was accepted: %v", p)
	}
	request.MonitoringAlerts["Hot"] = utils.Alert{Name: "Hot", TrackingMetric: "cosmos.system.temp.all", Period: "latest", Condition: utils.AlertCondition{Operator: "gt", Value: 80}}
	if p := checkAlerts(&request, stored); p != nil {
		t.Fatalf("a good new alert was refused: %v", p)
	}
}
