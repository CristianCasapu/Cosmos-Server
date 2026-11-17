package metrics

// Fork addition (plan 47, I2d): the server's own check of a monitoring alert
// before it is saved, the same rules as the alert editor
// (client/src/utils/alertForm.js). Upstream saved anything the whole-settings
// save or the alert routes received. Only an alert that is new or changed is
// checked (LastTriggered, which the server writes itself, does not count as a
// change), so an alert saved earlier, odd or not, still loads, works and
// saves as it is.

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	maxAlerts       = 200
	maxAlertName    = 64
	maxAlertMetric  = 200
	maxAlertActions = 8
	maxActionTarget = 256
)

// alertKind is what a metric's key says about it: whether it has a maximum
// to take a percentage of, and whether it belongs to a container.
type alertKind struct {
	prefix    string
	percent   bool
	container bool
}

// The metrics the server collects (system.go), longest prefix first.
var alertKinds = []alertKind{
	{"cosmos.system.disk-health.temperature.", false, false},
	{"cosmos.system.docker.cpu.", false, true},
	{"cosmos.system.docker.ram.", true, true}, // its maximum is the server's memory
	{"cosmos.system.docker.net", false, true},
	{"cosmos.system.disk.", true, false},
	{"cosmos.system.temp.", false, false},
	{"cosmos.system.cpu.", false, false},
	{"cosmos.system.ram", true, false},
	{"cosmos.system.net", false, false},
	{"cosmos.proxy.", false, false},
}

func kindOfMetric(metric string) *alertKind {
	var best *alertKind
	for i := range alertKinds {
		k := &alertKinds[i]
		if strings.HasPrefix(metric, k.prefix) && (best == nil || len(k.prefix) > len(best.prefix)) {
			best = k
		}
	}
	return best
}

func plainAlertText(value string) bool {
	for _, r := range value {
		if r == utf8.RuneError || !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// CheckAlert says in words why an alert cannot be saved, nil when it can.
func CheckAlert(key string, alert utils.Alert) error {
	name := alert.Name
	switch {
	case strings.TrimSpace(name) == "":
		return errors.New("Give the alert a name")
	case name != strings.TrimSpace(name):
		return fmt.Errorf("The name of the alert %q starts or ends with a space", name)
	case utf8.RuneCountInString(name) > maxAlertName:
		return fmt.Errorf("The name of an alert is at most %d characters", maxAlertName)
	case !plainAlertText(name) || strings.ContainsAny(name, `/\?#%`):
		return fmt.Errorf("The name of the alert %q holds a character an alert name cannot hold (/ \\ ? # %% or an invisible one)", name)
	case key != name:
		return fmt.Errorf("The alert %q is filed under another name (%q): save it under its own name", name, key)
	}

	metric := alert.TrackingMetric
	switch {
	case strings.TrimSpace(metric) == "":
		return fmt.Errorf("Alert %q: pick what to watch", name)
	case len(metric) > maxAlertMetric || !plainAlertText(metric) || strings.ContainsRune(metric, ' '):
		return fmt.Errorf("Alert %q: %q is not the name of a metric", name, metric)
	}

	switch alert.Condition.Operator {
	case "gt", "lt", "eq":
	default:
		return fmt.Errorf("Alert %q: the condition is above, below or exactly a value", name)
	}
	switch alert.Period {
	case "latest", "hourly", "daily":
	default:
		return fmt.Errorf("Alert %q: the alert looks at every measurement, the hour's average or the day's average", name)
	}
	kind := kindOfMetric(metric)
	if alert.Condition.Percent {
		if kind != nil && !kind.percent {
			return fmt.Errorf("Alert %q: this metric has no maximum to take a percentage of; compare with a value in its own unit", name)
		}
		if alert.Condition.Value < 0 || alert.Condition.Value > 100 {
			return fmt.Errorf("Alert %q: a percentage is from 0 to 100", name)
		}
	}
	switch alert.Severity {
	case "", "info", "warn", "error":
	default:
		return fmt.Errorf("Alert %q: the severity is information, warning or error", name)
	}

	if len(alert.Actions) > maxAlertActions {
		return fmt.Errorf("Alert %q: at most %d actions", name, maxAlertActions)
	}
	for _, action := range alert.Actions {
		switch action.Type {
		case "notification", "email":
		case "stop", "restart":
			if kind == nil || !kind.container {
				return fmt.Errorf("Alert %q: stopping and restarting act on a container; this metric is not a container's", name)
			}
		default:
			return fmt.Errorf("Alert %q: the action %q does not exist; an alert shows a notification, sends an e-mail, or stops or restarts the container", name, action.Type)
		}
		if len(action.Target) > maxActionTarget || !plainAlertText(action.Target) {
			return fmt.Errorf("Alert %q: the target of an action is plain text of at most %d characters", name, maxActionTarget)
		}
	}
	return nil
}

// sameAlert compares two alerts without the time they last went off.
func sameAlert(a, b utils.Alert) bool {
	a.LastTriggered, b.LastTriggered = time.Time{}, time.Time{}
	if len(a.Actions) == 0 && len(b.Actions) == 0 {
		a.Actions, b.Actions = nil, nil
	}
	return reflect.DeepEqual(a, b)
}

// sameButSwitch: the two alerts differ at most in Enabled and in the time
// they last went off. Switching a stored alert on or off is never refused: an
// odd alert saved earlier can always be switched off.
func sameButSwitch(a, b utils.Alert) bool {
	a.Enabled, b.Enabled = false, false
	return sameAlert(a, b)
}

// CheckChangedAlerts checks the alerts of next that are new or differ from
// those of stored. The first problem is returned, in words.
func CheckChangedAlerts(next map[string]utils.Alert, stored map[string]utils.Alert) error {
	if len(next) > maxAlerts && len(next) > len(stored) {
		return fmt.Errorf("At most %d alerts", maxAlerts)
	}
	keys := make([]string, 0, len(next))
	for key := range next {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if old, found := stored[key]; found && sameButSwitch(old, next[key]) {
			continue
		}
		if err := CheckAlert(key, next[key]); err != nil {
			return err
		}
	}
	return nil
}

// alertUpdateProblem is the check of PUT /api/alerts/{name}: the alert as it
// will be stored (under its new name when it is renamed), checked unless it
// is the stored one unchanged.
func alertUpdateProblem(name string, alert utils.Alert, stored utils.Alert) error {
	key := name
	if alert.Name != "" {
		key = alert.Name
	}
	alert.Name = key
	if key == name && sameButSwitch(stored, alert) {
		return nil
	}
	return CheckAlert(key, alert)
}
