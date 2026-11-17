package utils

// Fork addition (plan 48): the kinds and the English of the notifications
// of the watches (src/watch) and of the two notices added to existing code
// (an update that proved itself, a scheduled job that works again). Added
// to the catalogue when the package loads; the same keys are in
// translation.json (TestNotificationTextsAreInThePanel covers them).

import (
	"strings"
	"time"
)

var watchNotificationKinds = map[string]notificationKind{
	"smart.failing":     {"storage", "notif.smart.failing.title"},
	"smart.restored":    {"storage", "notif.smart.restored.title"},
	"disk.filling":      {"storage", "notif.disk.filling.title"},
	"disk.filling.over": {"storage", "notif.disk.filling.over.title"},
	"docker.down":       {"containers", "notif.docker.down.title"},
	"docker.restored":   {"containers", "notif.docker.restored.title"},
	"snapshots.late":    {"snapshots", "notif.snapshots.late.title"},
	"snapshots.ontime":  {"snapshots", "notif.snapshots.ontime.title"},
	"update.proven":     {"updates", "notif.update.proven.title"},
	"job.restored":      {"jobs", "notif.job.restored.title"},
	"login.newcountry":  {"security", "notif.login.newcountry.title"},
}

var watchNotificationTexts = map[string]string{
	"notif.smart.failing.title":     "Disk {{disk}} is failing",
	"notif.smart.failing.what":      "The disk {{disk}} ({{model}}, serial ending {{serial}}, mounted at {{mounts}}) reports a problem: {{reason}}.",
	"notif.smart.failing.means":     "A disk that reports this often fails completely soon, and what is on it can be lost.",
	"notif.smart.restored.title":    "Disk {{disk}} reads healthy again",
	"notif.smart.restored.what":     "The disk {{disk}} ({{model}}, serial ending {{serial}}) reads healthy again, or was replaced.",
	"notif.disk.filling.title":      "{{mount}} is filling fast",
	"notif.disk.filling.what":       "{{mount}} grew by {{grown}} in the last {{hours}} hours: at that rate it is full in about {{left}} ({{free}} free).",
	"notif.disk.filling.means":      "When it is full, apps and Cosmos fail to write to it: databases can be damaged, downloads and backups stop.",
	"notif.disk.filling.over.title": "{{mount}} no longer fills fast",
	"notif.disk.filling.over.what":  "{{mount}} no longer fills at that rate: {{free}} free.",
	"notif.docker.down.title":       "Docker does not answer",
	"notif.docker.down.what":        "Docker has not answered Cosmos for {{minutes}} minutes ({{reason}}).",
	"notif.docker.down.means":       "Apps cannot be started, stopped, updated or created, and the panel cannot show them; apps that run may go on running.",
	"notif.docker.restored.title":   "Docker answers again",
	"notif.docker.restored.what":    "Docker answers again; it did not answer for {{duration}}.",
	"notif.snapshots.late.title":    "No snapshot of {{folder}} for {{age}}",
	"notif.snapshots.late.what":     "The last snapshot of {{folder}} is from {{last}} ({{age}} ago); its schedule makes one every {{period}}.",
	"notif.snapshots.late.means":    "If the folder were lost now, what changed in it since then would be lost too.",
	"notif.snapshots.ontime.title":  "{{folder}} has a new snapshot",
	"notif.snapshots.ontime.what":   "A snapshot of {{folder}} was made at {{last}}.",
	"notif.update.proven.title":     "Version {{version}} proved itself",
	"notif.update.proven.what":      "Version {{version}} started properly and proved itself in {{duration}} (start {{run}} of its trial). The update is final; the copy of the version before is removed after 72 hours.",
	"notif.job.restored.title":      "{{name}} works again",
	"notif.job.restored.what":       "The scheduled job {{name}} finished without an error again.",
	"notif.login.newcountry.title":  "{{user}} signed in from a new country: {{country}}",
	"notif.login.newcountry.what":   "{{user}} signed in from {{ip}}, in {{country}}: a country this account never signed in from before (until now: {{known}}).",
	"notif.login.newcountry.means":  "That is you travelling or on a VPN abroad, or someone else who has the password.",
}

func init() {
	for kind, k := range watchNotificationKinds {
		notificationKinds[kind] = k
	}
	for key, text := range watchNotificationTexts {
		notificationTexts[key] = text
	}
}

// ForkJobSucceeded is the hook of a scheduled job that finished without an
// error (src/cron): when the job's failure is still open, it is marked
// resolved and a follow-up says the job works again. It never waits long:
// one indexed question to the database, and only then a write.
func ForkJobSucceeded(name string) {
	defer func() { recover() }()
	thread := "error.job:" + cleanVar(name, 160)
	if !ThreadOpen(thread) {
		return
	}
	first, ok := ResolveThread(thread)
	if !ok {
		return
	}
	duration := ""
	if !first.IsZero() {
		duration = NotificationDuration(time.Since(first))
	}
	WriteNotification(Notification{Recipient: "admin", Level: "success", Fork: &NotificationInfo{
		Kind: "job.restored", Source: "jobs", Thread: thread, Resolves: thread,
		Title:   NText("notif.job.restored.title", "name", name),
		What:    NText("notif.job.restored.what", "name", name, "duration", duration),
		Subject: &NotificationSubject{Type: "job", Name: strings.TrimSpace(name), Link: "/cosmos-ui/cron"},
	}})
}

// NotificationKindSource is the source of a kind of notification ("" when
// the kind is unknown): what a notification without its source is
// counted under (plan 48: the Telegram category follows it).
func NotificationKindSource(kind string) string {
	return notificationKinds[kind].Source
}
