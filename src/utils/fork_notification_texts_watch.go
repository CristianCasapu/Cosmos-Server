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
	"smart.gone":        {"storage", "notif.smart.gone.title"},
	"disk.filling":      {"storage", "notif.disk.filling.title"},
	"disk.filling.over": {"storage", "notif.disk.filling.over.title"},
	"docker.down":       {"containers", "notif.docker.down.title"},
	"docker.restored":   {"containers", "notif.docker.restored.title"},
	"snapshots.late":    {"snapshots", "notif.snapshots.late.title"},
	"snapshots.ontime":  {"snapshots", "notif.snapshots.ontime.title"},
	"update.proven":     {"updates", "notif.update.proven.title"},
	"job.restored":      {"jobs", "notif.job.restored.title"},
	"login.newcountry":  {"security", "notif.login.newcountry.title"},
	"smart.unreadable":  {"storage", "notif.smart.unreadable.title"},
	"smart.unwatchable": {"storage", "notif.smart.unwatchable.title"},
	"watch.stuck":       {"server", "notif.watch.stuck.title"},
	"watch.recovered":   {"server", "notif.watch.recovered.title"},
	"smart.readable":    {"storage", "notif.smart.readable.title"},
	"docker.unwatched":  {"containers", "notif.docker.unwatched.title"},
}

var watchNotificationTexts = map[string]string{
	"notif.smart.failing.title":     "Disk {{disk}} is failing",
	"notif.smart.failing.what":      "The disk {{disk}} ({{model}}, serial ending {{serial}}, mounted at {{mounts}}) reports a problem: {{reason}}.",
	"notif.smart.failing.means":     "A disk that reports this often fails completely soon, and what is on it can be lost.",
	"notif.smart.restored.title":    "Disk {{disk}} reads healthy again",
	"notif.smart.restored.what":     "The disk {{disk}} ({{model}}, serial ending {{serial}}) reads healthy again.",
	"notif.smart.gone.title":        "Failing disk {{disk}} no longer present",
	"notif.smart.gone.what":         "The disk {{disk}} ({{model}}, serial ending {{serial}}), which was failing, has not been seen for three checks: it was removed, replaced, or it died. Its failing history is kept.",
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
	"notif.smart.readable.title":    "Disk {{disk}} can be read again",
	"notif.smart.readable.what":     "The S.M.A.R.T. data of the disk {{disk}} ({{model}}) was read without a failure for the last six checks.",
	"notif.watch.recovered.title":   "The check of {{what}} answers again",
	"notif.watch.recovered.what":    "The check of {{what}} finished again after {{age}}.",
	"notif.docker.unwatched.title":  "Docker is no longer watched",
	"notif.docker.unwatched.what":   "The watch of Docker was switched off while Docker did not answer: whether it answers now is not known.",
	"notif.smart.unreadable.title":  "Disk {{disk}} cannot be read",
	"notif.smart.unreadable.what":   "The S.M.A.R.T. data of the disk {{disk}} ({{model}}, serial ending {{serial}}, mounted at {{mounts}}) could not be read three times in a row; it was read before.",
	"notif.smart.unreadable.means":  "A disk that stops answering is often failing: what is on it can be lost.",
	"notif.smart.unwatchable.title": "The health of {{disk}} is not watched",
	"notif.smart.unwatchable.what":  "The disk {{disk}} ({{model}}) does not say whether it sleeps, so Cosmos does not read its S.M.A.R.T. data: that could wake it. Its health is not watched.",
	"notif.watch.stuck.title":       "The check of {{what}} does not answer",
	"notif.watch.stuck.what":        "The check of {{what}} has not finished for {{age}}: a disk or a share it reads does not answer. The other checks go on.",
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
func ForkJobSucceeded(scheduler string, name string) {
	forkJobWorks(JobThread(scheduler, name), name)
}

// ForkJobScheduled is the hook of a job the scheduler took (review 6):
// when it could not be scheduled before, it is said that it works again.
func ForkJobScheduled(scheduler string, name string) {
	forkJobWorks(JobScheduleThread(scheduler, name), name)
}

// ForkJobFailed is the hook of a scheduled job that failed: upstream's
// "Server Error", on the job's own thread (it names the scheduler).
func ForkJobFailed(scheduler string, name string, err error) {
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	message := "CRON job " + name + " failed"
	info := majorErrorInfo(message, errStr)
	info.Thread = JobThread(scheduler, name)
	MajorErrorWith(message, err, info)
}

// ForkJobScheduleFailed is the hook of a job the scheduler refused: its
// own thread, named after the job (review 6).
func ForkJobScheduleFailed(scheduler string, name string, err error) {
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	info := majorErrorInfo("CRON job "+name+" failed", "could not be scheduled: "+errStr)
	info.Thread = JobScheduleThread(scheduler, name)
	MajorErrorWith("CRON job scheduling", err, info)
}

func forkJobWorks(thread string, name string) {
	defer func() { recover() }()
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
