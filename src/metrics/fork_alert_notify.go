package metrics

// Fork addition: an alert's notification in words. A disk alert (its metric
// is a disk's use) names the disk, says what a full disk does, and links to
// Storage.

import (
	"strconv"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

const diskMetricPrefix = "cosmos.system.disk."

// alertDisk is the mount point an alert watches, "" when it watches
// something else.
func alertDisk(alert utils.Alert) string {
	if !strings.HasPrefix(alert.TrackingMetric, diskMetricPrefix) {
		return ""
	}
	return strings.TrimPrefix(alert.TrackingMetric, diskMetricPrefix)
}

func alertCondition(alert utils.Alert) string {
	text := alert.Condition.Operator + " " + strconv.Itoa(alert.Condition.Value)
	if alert.Condition.Percent {
		text += "%"
	}
	return text
}

func forkAlertInfo(alert utils.Alert) *utils.NotificationInfo {
	monitoring := utils.NotificationStep{NotificationText: *utils.NText("notif.step.monitoring.open"), Link: "/cosmos-ui/monitoring"}
	facts := []utils.NotificationFact{{Key: "notif.fact.alert", Value: alert.Name}, {Key: "notif.fact.metric", Value: alert.TrackingMetric},
		{Key: "notif.fact.condition", Value: alertCondition(alert)}}
	if disk := alertDisk(alert); disk != "" {
		return &utils.NotificationInfo{
			Kind:    "alert.disk",
			Subject: &utils.NotificationSubject{Type: "disk", Name: disk, Link: "/cosmos-ui/storage"},
			Title:   utils.NText("notif.alert.disk.title", "disk", disk),
			What:    utils.NText("notif.alert.disk.what", "disk", disk, "name", alert.Name),
			Means:   utils.NText("notif.alert.disk.means"),
			Doing:   utils.NText("notif.alert.doing"),
			Steps: []utils.NotificationStep{{NotificationText: *utils.NText("notif.step.storage.open"), Link: "/cosmos-ui/storage"},
				{NotificationText: *utils.NText("notif.step.disk.free")}, monitoring},
			Facts: append([]utils.NotificationFact{{Key: "notif.fact.disk", Value: disk}}, facts...),
		}
	}
	return &utils.NotificationInfo{
		Kind:    "alert.triggered",
		Subject: &utils.NotificationSubject{Type: "alert", Name: alert.Name, Link: "/cosmos-ui/monitoring"},
		Title:   utils.NText("notif.alert.triggered.title", "name", alert.Name),
		What:    utils.NText("notif.alert.triggered.what", "name", alert.Name),
		Means:   utils.NText("notif.alert.triggered.means"),
		Doing:   utils.NText("notif.alert.doing"),
		Steps:   []utils.NotificationStep{monitoring},
		Facts:   facts,
	}
}
