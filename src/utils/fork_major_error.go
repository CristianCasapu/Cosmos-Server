package utils

// Fork addition: upstream's "Server Error" notification (MajorError) gets a
// kind, a plain sentence, the object it is about and a link; the message and
// the error stay complete as the technical text.

import "strings"

// majorErrorInfo is the hook of MajorError.
func majorErrorInfo(message string, errText string) *NotificationInfo {
	return majorErrorInfoOf(majorErrorKind(message), message, errText)
}

// majorErrorInfoOf is majorErrorInfo with the kind given (review 7, N5: a
// job's failure is error.job whatever its name, SnapRAID's included).
func majorErrorInfoOf(kind string, message string, errText string) *NotificationInfo {
	raw := message
	if errText != "" {
		raw += ": " + errText
	}
	short := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(message), ":"))
	info := &NotificationInfo{Kind: kind, Raw: raw, Means: NText("notif.error.means"),
		Thread: kind + ":" + cleanVar(short, 160)}
	what := func(key string) { info.What = NText(key, "message", short) }

	switch kind {
	case "error.backup":
		what("notif.error.backup.what")
		info.Means = NText("notif.error.backup.means")
		info.Thread = kind
		info.Steps = []NotificationStep{{NotificationText: *NText("notif.step.general.open"), Link: "/cosmos-ui/config-general/general"}}
	case "error.job":
		name := strings.TrimSuffix(strings.TrimPrefix(short, "CRON job "), " failed")
		if short == "CRON job scheduling" {
			name = "scheduling"
		}
		info.What = NText("notif.error.job.what", "name", name)
		info.Title = NText("notif.error.job.title")
		info.Means = NText("notif.error.job.means")
		info.Subject = &NotificationSubject{Type: "job", Name: name, Link: "/cosmos-ui/cron"}
		info.Thread = kind + ":" + cleanVar(name, 160)
		info.Facts = []NotificationFact{{Key: "notif.fact.job", Value: name}}
		info.Steps = []NotificationStep{{NotificationText: *NText("notif.step.cron.open"), Link: "/cosmos-ui/cron"}}
	case "error.snapraid":
		what("notif.error.snapraid.what")
		info.Means = NText("notif.error.snapraid.means")
		info.Steps = []NotificationStep{{NotificationText: *NText("notif.step.storage.open"), Link: "/cosmos-ui/storage"}}
	case "error.internet":
		info.What = NText("notif.error.internet.what")
		info.Means = NText("notif.error.internet.means")
		info.Thread = kind
	case "error.database":
		what("notif.error.database.what")
		info.Means = NText("notif.error.database.means")
	case "error.export":
		what("notif.error.export.what")
		info.Means = NText("notif.error.export.means")
		info.Thread = kind
		info.Steps = []NotificationStep{{NotificationText: *NText("notif.step.general.open"), Link: "/cosmos-ui/config-general/general"}}
	case "error.migration":
		what("notif.error.migration.what")
		info.Steps = []NotificationStep{{NotificationText: *NText("notif.step.general.open"), Link: "/cosmos-ui/config-general/general"}}
	case "error.socketproxy":
		what("notif.error.socketproxy.what")
		info.Means = NText("notif.error.socketproxy.means")
		info.Steps = []NotificationStep{{NotificationText: *NText("notif.step.urls.open"), Link: "/cosmos-ui/config-url"}}
	case "error.mail":
		what("notif.error.mail.what")
		info.Thread = kind
		info.Steps = []NotificationStep{{NotificationText: *NText("notif.step.general.open"), Link: "/cosmos-ui/config-general/general"}}
	case "error.setup":
		what("notif.error.setup.what")
	case "cert.order.failed":
		// an order told the old way: the domain is not known here
		info.Kind = "error.other"
		what("notif.error.other.what")
		info.Steps = []NotificationStep{{NotificationText: *NText("notif.step.cert.card"), Link: "/cosmos-ui/config-general/https"}}
	case "container.update.failed":
		info.What = NText("notif.container.update.failed.legacy")
		info.Title = NText("notif.error.other.title")
		info.Means = NText("notif.container.update.failed.means")
		info.Doing = NText("notif.container.update.failed.doing")
		info.Steps = []NotificationStep{{NotificationText: *NText("notif.step.app.logs"), Link: "/cosmos-ui/servapps"}}
	default:
		info.Kind = "error.other"
		what("notif.error.other.what")
	}
	return info
}

// MajorErrorWith is MajorError with the notification's info given by the
// caller (a certificate that could not be obtained knows its domain).
func MajorErrorWith(message string, err error, info *NotificationInfo) {
	Error(message, err)
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	TriggerEvent("cosmos.error", "Critical Error", "error", "", map[string]interface{}{"message": message, "error": errStr})
	if info == nil {
		info = majorErrorInfo(message, errStr)
	}
	WriteNotification(Notification{Recipient: "admin", Title: "header.notification.title.serverError",
		Message: message + " : " + errStr, Level: "error", Fork: info})
}
