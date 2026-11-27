package metrics

// Fork addition: what a container alert does when its container is gone, and
// the e-mails of one run of the hourly and daily alerts.
//
// The hourly and daily alerts are checked on the pool that just closed, which
// holds every container measured during that hour or day, also the ones that
// were removed since (short-lived containers started with --rm: builds, test
// runs). Upstream acted on each of them: a notification, an e-mail and a stop
// that fails with "No such container", for every such container, every night.
// Now an alert on a container that Docker says does not exist does nothing
// (when Docker cannot be asked, the alert acts as before), and the e-mails of
// one run go out as one per alert, naming every metric it fired on.

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"sync"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

// containerLiveness asks Docker about a container (a variable for the tests).
var containerLiveness = docker.GetContainerLiveness

// containerOfMetric is the container a metric is measured on, "" for any other metric.
func containerOfMetric(metric utils.AlertMetricTrack) string {
	object, name, found := strings.Cut(metric.Object, "@")
	if !found || object != "container" || name == "" {
		return ""
	}
	return name
}

// alertOnGoneContainer reports that the metric is a container's and Docker
// answered that the container does not exist. An error (Docker down) is not
// an answer: the alert then acts as before.
func alertOnGoneContainer(metric utils.AlertMetricTrack) bool {
	name := containerOfMetric(metric)
	if name == "" {
		return false
	}
	live, err := containerLiveness(name)
	if err != nil {
		return false
	}
	return !live.Exists
}

// The e-mails of the run in progress, by alert name; nil when no run batches them.
var (
	alertMailsMu sync.Mutex
	alertMails   map[string]*alertMailBatch
)

type alertMailBatch struct {
	alert utils.Alert
	keys  []string
}

// sendAlertMail sends one alert e-mail to every administrator (a variable for the tests).
var sendAlertMail = func(alert utils.Alert, keys []string) {
	if !utils.GetMainConfig().EmailConfig.Enabled {
		utils.Warn("Alert triggered but Email is not enabled")
		return
	}
	subject, body := alertMailText(alert, keys)
	for _, user := range utils.ListAllUsers("admin") {
		if user.Email != "" {
			utils.SendEmail([]string{user.Email}, subject, body)
		}
	}
}

// alertMailText is upstream's e-mail, with the list of metrics when there are several.
func alertMailText(alert utils.Alert, keys []string) (string, string) {
	on := ""
	if len(keys) == 1 {
		on = html.EscapeString(keys[0]) + "."
	} else {
		items := make([]string, len(keys))
		for i, key := range keys {
			items[i] = "<li>" + html.EscapeString(key) + "</li>"
		}
		on = fmt.Sprintf("%d metrics:<ul>%s</ul>", len(keys), strings.Join(items, ""))
	}
	return "Alert Triggered: " + alert.Name, fmt.Sprintf(`<h1>Alert Triggered [%s]</h1>
You are recevining this email because you are admin on a Cosmos
server where an Alert has been subscribed to.<br />
You can manage your subscriptions in the Monitoring tab.<br />
Alert triggered on %s Please refer to the Monitoring tab for
more information.<br />`, html.EscapeString(alert.Severity), on)
}

// beginAlertMails makes the e-mails of the alerts checked from now on wait for sendAlertMails.
func beginAlertMails() {
	alertMailsMu.Lock()
	defer alertMailsMu.Unlock()
	alertMails = map[string]*alertMailBatch{}
}

// queueAlertMail keeps the e-mail of an alert for the run in progress; false when no run batches them.
func queueAlertMail(alert utils.Alert, metric utils.AlertMetricTrack) bool {
	alertMailsMu.Lock()
	defer alertMailsMu.Unlock()
	if alertMails == nil {
		return false
	}
	batch := alertMails[alert.Name]
	if batch == nil {
		batch = &alertMailBatch{alert: alert}
		alertMails[alert.Name] = batch
	}
	batch.keys = append(batch.keys, metric.Key)
	return true
}

// sendAlertMails sends one e-mail per alert of the run, in the order of the alerts' names, and ends the run.
func sendAlertMails() {
	alertMailsMu.Lock()
	batches := alertMails
	alertMails = nil
	alertMailsMu.Unlock()

	names := make([]string, 0, len(batches))
	for name := range batches {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		keys := append([]string(nil), batches[name].keys...)
		sort.Strings(keys)
		sendAlertMail(batches[name].alert, keys)
	}
}
