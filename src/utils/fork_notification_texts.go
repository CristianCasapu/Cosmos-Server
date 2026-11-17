package utils

// Fork addition: the kinds of notifications and their sentences in English.
//
// The panel shows these through t(key, english, vars): the same keys are in
// client/src/utils/locales/en/translation.json with the same English
// (TestNotificationTextsAreInThePanel), so other languages can translate
// them. Telegram uses the English here.

import (
	"regexp"
	"sort"
	"strings"
)

type notificationKind struct {
	Source string
	Title  string // translation key of the title
}

// notificationKinds lists every kind. Sources are what the page filters by.
var notificationKinds = map[string]notificationKind{
	"upnp.forward.failed":     {"upnp", "notif.upnp.forward.failed.title"},
	"upnp.forward.restored":   {"upnp", "notif.upnp.forward.restored.title"},
	"upnp.router.unreachable": {"upnp", "notif.upnp.router.unreachable.title"},
	"upnp.router.restored":    {"upnp", "notif.upnp.router.restored.title"},
	"upnp.config.invalid":     {"upnp", "notif.upnp.config.invalid.title"},
	"cert.order.failed":       {"certificates", "notif.cert.order.failed.title"},
	"cert.expiring":           {"certificates", "notif.cert.expiring.title"},
	"cert.renewed":            {"certificates", "notif.cert.renewed.title"},
	"cert.untrusted":          {"certificates", "notif.cert.untrusted.title"},
	"container.updated":       {"containers", "notif.container.updated.title"},
	"container.update.failed": {"containers", "notif.container.update.failed.title"},
	"container.dying":         {"containers", "notif.container.dying.title"},
	"container.startFailed":   {"containers", "notif.container.startFailed.title"},
	"container.leftover":      {"containers", "notif.container.leftover.title"},
	"app.installed":           {"containers", "notif.app.installed.title"},     // plan 45
	"app.installMail.failed":  {"containers", "notif.app.installMail.title"},   // plan 45
	"route.protected":         {"containers", "notif.route.protected.title"},   // plan 45
	"snapshots.failed":        {"snapshots", "notif.snapshots.failed.title"},
	"alert.disk":              {"monitoring", "notif.alert.disk.title"},
	"alert.triggered":         {"monitoring", "notif.alert.triggered.title"},
	"fail2ban.down":           {"fail2ban", "notif.fail2ban.down.title"},
	"fail2ban.restored":       {"fail2ban", "notif.fail2ban.restored.title"},
	"telegram.failed":         {"telegram", "notif.telegram.failed.title"},
	"update.rejected":         {"updates", "notif.update.rejected.title"},
	"originpull.expiring":     {"cloudflare", "header.notification.title.originPullExpiring"},
	"originpull.stopped":      {"cloudflare", "header.notification.title.originPullStopped"},
	"turnstile.problem":       {"cloudflare", "header.notification.title.turnstileProblem"},
	"edgeblock.problem":       {"cloudflare", "header.notification.title.edgeBlocksProblem"},
	"error.backup":            {"backup", "notif.error.backup.title"},
	"error.job":               {"jobs", "notif.error.job.title"},
	"error.snapraid":          {"storage", "notif.error.snapraid.title"},
	"error.internet":          {"server", "notif.error.internet.title"},
	"error.database":          {"server", "notif.error.database.title"},
	"error.export":            {"containers", "notif.error.export.title"},
	"error.migration":         {"server", "notif.error.migration.title"},
	"error.socketproxy":       {"server", "notif.error.socketproxy.title"},
	"error.mail":              {"server", "notif.error.mail.title"},
	"error.setup":             {"server", "notif.error.setup.title"},
	"error.other":             {"server", "notif.error.other.title"},
	"other":                   {"other", ""},
}

// legacyNotificationKinds gives a kind to the notifications written without
// one, by their title.
var legacyNotificationKinds = map[string]string{
	"header.notification.title.upnpFailed":           "upnp.forward.failed",
	"header.notification.title.certificateRenewed":   "cert.renewed",
	"header.notification.title.certificateExpiring":  "cert.expiring",
	"header.notification.title.certificateUntrusted": "cert.untrusted",
	"header.notification.title.containerUpdate":      "container.updated",
	"header.notification.title.containerLeftover":    "container.leftover",
	"header.notification.title.snapshotsFailed":      "snapshots.failed",
	"header.notification.title.alertTriggered":       "alert.triggered",
	"header.notification.title.telegramFailed":       "telegram.failed",
	"header.notification.title.updateRejected":       "update.rejected",
	"header.notification.title.originPullExpiring":   "originpull.expiring",
	"header.notification.title.originPullStopped":    "originpull.stopped",
	"header.notification.title.serverError":          "error.other",
	"header.notification.title.turnstileProblem":     "turnstile.problem",
	"header.notification.title.edgeBlocksProblem":    "edgeblock.problem",
}

// NotificationSources are the sources, in the order the page offers them.
var NotificationSources = []string{"upnp", "certificates", "containers", "snapshots", "monitoring", "fail2ban",
	"telegram", "updates", "cloudflare", "backup", "jobs", "storage", "server", "security", "other"}

// majorErrors gives a kind to utils.MajorError's messages by how they
// start. The first match wins.
var majorErrors = []struct{ prefix, kind string }{
	{"Backup: ", "error.backup"},
	{"Couldn't get the TLS certificate", "cert.order.failed"},
	{"Zones: couldn't get the TLS certificate", "cert.order.failed"},
	{"CRON job SnapRAID", "error.snapraid"},
	{"Failed to status ", "error.snapraid"},
	{"Failed to create SnapRAID", "error.snapraid"},
	{"CRON job scheduling", "error.job"},
	{"CRON job ", "error.job"},
	{"Container failed to update", "container.update.failed"},
	{"Your server has no internet connection", "error.internet"},
	{"Database Retention", "error.database"},
	{"Cannot open the monitoring database", "error.database"},
	{"ExportDocker", "error.export"},
	{"Export Docker", "error.export"},
	{"MigratePre", "error.migration"},
	{"[SocketProxy]", "error.socketproxy"},
	{"UserLogin: Error while sending login notification email", "error.mail"},
	{"Setup: ", "error.setup"},
}

func majorErrorKind(message string) string {
	for _, known := range majorErrors {
		if strings.HasPrefix(message, known.prefix) {
			return known.kind
		}
	}
	return "error.other"
}

// notificationTexts is the English of every sentence.
var notificationTexts = map[string]string{
	// --- UPnP
	"notif.upnp.forward.failed.title":         "The router does not forward port {{port}}",
	"notif.upnp.forward.failed.https.absent":  "The router no longer forwards port {{port}} to this server: Cosmos cannot be reached from the internet.",
	"notif.upnp.forward.failed.https.unknown": "Cosmos could not get the router to forward port {{port}} to this server. If the router does not forward it, Cosmos cannot be reached from the internet.",
	"notif.upnp.forward.failed.http.absent":   "The router no longer forwards port {{port}} (plain HTTP) to this server: visitors who type the address without https, and certificate orders over HTTP, do not reach Cosmos.",
	"notif.upnp.forward.failed.http.unknown":  "Cosmos could not get the router to forward port {{port}} (plain HTTP) to this server: visitors who type the address without https, and certificate orders over HTTP, may not reach Cosmos.",
	"notif.upnp.forward.failed.app.absent":    "The router does not forward port {{port}} to this server: the app {{app}} cannot be reached from the internet on that port.",
	"notif.upnp.forward.failed.app.unknown":   "Cosmos could not get the router to forward port {{port}} for the app {{app}}. If the router does not forward it, the app cannot be reached from the internet on that port.",
	"notif.upnp.means.router":                 "The router closed the connection or did not answer when Cosmos asked for the forward. Routers do this while their UPnP service restarts or when it is overloaded; it usually passes by itself.",
	"notif.upnp.means.refused":                "The router refused the request. It will not change its mind by itself: something has to change on the router (UPnP switched off, the port reserved for something else, a limit on the number of forwards).",
	"notif.upnp.means.conflict":               "Port {{port}} is already forwarded on the router to something else. A router forwards a port to one device only.",
	"notif.upnp.means.conflict.holder":        "Port {{port}} is already forwarded on the router to {{holder}}. A router forwards a port to one device only.",
	"notif.upnp.means.reserved":               "Port {{port}} is on the list of ports Cosmos never asks the router for (Configuration > HTTP > Internet access).",
	"notif.upnp.doing.retry":                  "Cosmos asks the router again by itself at {{at}}; it has tried {{attempts}} times so far. While it fails it keeps trying, at least every 15 minutes.",
	"notif.upnp.doing.retryRefused":           "Cosmos asks the router again by itself at {{at}}, then every hour, since only a change on the router can make it succeed.",
	"notif.upnp.doing.none":                   "Cosmos does not ask the router again by itself for this port: press Check now once the cause is fixed.",
	"notif.upnp.forward.restored.title":       "Port {{port}} is forwarded again",
	"notif.upnp.forward.restored.https":       "The router forwards port {{port}} to this server again since {{at}}: Cosmos can be reached from the internet.",
	"notif.upnp.forward.restored.http":        "The router forwards port {{port}} (plain HTTP) to this server again since {{at}}.",
	"notif.upnp.forward.restored.app":         "The router forwards port {{port}} to this server again since {{at}}: the app {{app}} can be reached from the internet.",
	"notif.upnp.restored.means":               "It was not forwarded for {{duration}}.",
	"notif.upnp.restored.doing":               "Cosmos renews the forward by itself and checks every five minutes that the router still has it.",
	"notif.upnp.router.unreachable.title":     "Cosmos cannot reach the router",
	"notif.upnp.router.unreachable.what":      "Cosmos cannot reach the router to keep its port forwards. If the router dropped them, Cosmos cannot be reached from the internet.",
	"notif.upnp.router.unreachable.means":     "No router answered on the local network. The router may be restarting, UPnP may have been switched off on it, or this server may have lost its network connection.",
	"notif.upnp.router.restored.title":        "The router answers again",
	"notif.upnp.router.restored.what":         "The router answers again since {{at}}: Cosmos keeps its port forwards up to date again.",
	"notif.upnp.router.restored.means":        "It could not be reached for {{duration}}.",
	"notif.upnp.config.invalid.title":         "The port forwarding settings cannot be applied",
	"notif.upnp.config.invalid.what":          "The settings for forwarding ports on the router cannot be applied: {{answer}}.",
	"notif.upnp.config.invalid.means":         "Nothing is asked of the router until the settings are corrected.",
	"notif.step.upnp.check":                   "Check now: ask the router again at once",
	"notif.step.upnp.settings":                "Open Configuration > HTTP > Internet access",
	"notif.step.upnp.router":                  "On the router's own page ({{router}}), check that UPnP is switched on and that port {{port}} is forwarded to {{local}}, port {{internal}}.",
	"notif.step.upnp.routerUnknown":           "On the router's own page, check that UPnP is switched on, and that this server is connected to the router's network.",
	"notif.step.upnp.wait":                    "If the router was restarted, give it a few minutes: Cosmos asks again by itself.",
	"notif.step.upnp.conflict":                "Free port {{port}} on the router, or choose another external port under Internet access.",
	"notif.step.upnp.refused":                 "Look on the router for a UPnP setting that limits forwards (allowed ports, a maximum number, a \"secure mode\"), or forward port {{port}} by hand.",

	// --- certificates
	"notif.cert.order.failed.title": "No certificate for {{domain}}",
	"notif.cert.order.failed.what":  "Cosmos could not obtain a certificate for {{domain}}: {{reason}}",
	"notif.cert.means.valid":        "The current certificate stays in use until {{until}}. If no order succeeds before then, browsers show a security warning for {{domain}}.",
	"notif.cert.means.generic":      "The certificate in use stays until it ends (the state below says until when). If no order succeeds before then, browsers show a security warning for {{domain}}.",
	"notif.cert.means.none":         "{{domain}} has no certificate from a trusted authority yet: browsers show a security warning.",
	"notif.cert.doing.retry":        "Cosmos tries again by itself. You are told again if the reason changes, or after a day.",
	"notif.cert.expiring.title":     "The certificate of {{domain}} ends soon",
	"notif.cert.expiring.what":      "The certificate of {{domain}} ends on {{until}} ({{left}}) and has not been renewed: {{why}}.",
	"notif.cert.expired.what":       "The certificate of {{domain}} ended on {{until}} and has not been renewed: {{why}}.",
	"notif.cert.expiring.means":     "When it ends, browsers show a security warning for {{domain}}, and apps reached through it stop working for most visitors.",
	"notif.cert.expiring.doing":     "Cosmos keeps trying to renew it. You are told once a day while this lasts.",
	"notif.cert.renewed.title":      "Certificate renewed",
	"notif.cert.renewed.what":       "The certificate of {{domains}} was renewed.",
	"notif.cert.renewed.means":      "Nothing to do: visitors get the new certificate from now on.",
	"notif.cert.untrusted.title":    "Addresses without a trusted certificate",
	"notif.cert.untrusted.what":     "These addresses are served directly with a certificate browsers do not trust, so visitors get a warning: {{hosts}}.",
	"notif.cert.untrusted.means":    "Visitors have to click through a warning, and many apps refuse to connect at all.",
	"notif.step.cert.card":          "Open the certificates card (Configuration > Domains)",
	"notif.step.cert.dns":           "Check the DNS records of {{domain}} at your DNS provider, and that the way the certificate is obtained (port 80 from the internet, or the DNS provider's access) still works, as the card describes.",
	"notif.step.cert.renew":         "Once the cause is fixed, press Renew now on the certificate card.",

	// --- containers
	"notif.container.updated.title":        "{{name}} was updated",
	"notif.container.updated.what":         "The app {{name}} was updated to a new version of its image ({{image}}) and restarted.",
	"notif.container.updated.means":        "It was down for a moment while it was recreated. If it misbehaves now, look at its logs on its page.",
	"notif.container.update.failed.title":  "{{name}} could not be updated",
	"notif.container.update.failed.what":   "Cosmos downloaded a new version of the image of {{name}}, but could not recreate the container with it.",
	"notif.container.update.failed.legacy": "Cosmos downloaded a new version of a container's image, but could not recreate the container with it.",
	"notif.container.update.failed.means":  "The app may be stopped, or still running the old version: see its state below.",
	"notif.container.update.failed.doing":  "Cosmos tries again at the next update check.",
	"notif.container.dying.title":          "{{name}} keeps stopping",
	"notif.container.dying.what":           "The app {{name}} keeps stopping with an error: {{exits}} times within ten minutes (last exit code {{code}}).",
	"notif.container.dying.means":          "Docker starts it again each time and it stops again: the app does not work.",
	"notif.container.dying.doing":          "Cosmos does not change anything about it. You are told again if it goes on for hours.",
	"notif.container.startFailed.title":    "{{name}} does not start",
	"notif.container.startFailed.what":     "The app {{name}} was just created, but it does not run: {{reason}}.",
	"notif.container.startFailed.means":    "The app does not work until this is fixed.",
	"notif.container.startFailed.doing":    "Its tile in ServApps says why and, when Cosmos recognises the reason, offers the repair.",
	"notif.container.leftover.title":       "A change of {{app}} was interrupted",
	"notif.container.leftover.what":        "A change of {{app}} was interrupted when Cosmos stopped: the previous container is kept, stopped, as {{name}}.",
	"notif.container.leftover.means":       "Nothing was deleted. Until you decide, {{app}} may be missing, or running as the new version.",
	"notif.step.app.open":                  "Open the page of {{name}} (its state and logs)",
	"notif.step.app.logs":                  "Read the error below and the app's logs; check what changed: its settings, its image, a full disk, a service it needs.",
	"notif.step.servapps.leftover":         "At the top of ServApps, put the previous container back or remove it",
	// plan 45: an installation, and its e-mail
	"notif.app.installed.title":     "{{name}} was installed",
	"notif.app.installed.what":      "{{name}} was created. Its sign-in details (username, password, address) are on its page, under Sign-in details.",
	"notif.app.installed.means":     "The passwords are kept only in the app's own settings; Cosmos shows them there after you confirm your password.",
	"notif.app.installed.mailSent":  "An e-mail with the app's name, address and username (never a password) was sent to your address.",
	"notif.app.installed.mailNone":  "No e-mail was sent: {{reason}}.",
	"notif.app.installMail.title":   "The e-mail about {{name}} was not sent",
	"notif.app.installMail.what":    "Cosmos could not send the e-mail about the installation of {{name}}: {{reason}}.",
	"notif.app.installMail.means":   "Nothing else is affected: the app was created, and its sign-in details are on its page.",
	"notif.step.app.signIn":         "Open the sign-in details of {{name}}",
	"notif.step.config.mail":        "Check the e-mail settings (Configuration, General)",
	"notif.route.protected.title":   "{{names}}: for administrators only",
	"notif.route.protected.what":    "Cosmos made the URL {{names}} open for administrators only, behind Cosmos' sign-in: it leads to a tool that controls a database or has no sign-in of its own.",
	"notif.route.protected.means":   "Only administrators reach it, after signing in to Cosmos. The URL's other settings are as you saved them.",

	// --- snapshots
	"notif.snapshots.failed.title": "The incremental backup had problems",
	"notif.snapshots.failed.what":  "The incremental backup did not go cleanly: {{summary}}.",
	"notif.snapshots.failed.means": "Some folders may have no new snapshot from this run. Earlier snapshots are untouched.",
	"notif.snapshots.failed.doing": "The next scheduled run tries again.",
	"notif.step.snapshots.open":    "Open Configuration > General > Incremental backup",

	// --- monitoring
	"notif.alert.disk.title":      "Disk nearly full: {{disk}}",
	"notif.alert.disk.what":       "The disk {{disk}} is nearly full (alert \"{{name}}\").",
	"notif.alert.disk.means":      "When it is full, apps and Cosmos fail to write to it: databases can be damaged, downloads and backups stop.",
	"notif.alert.triggered.title": "Alert: {{name}}",
	"notif.alert.triggered.what":  "The alert \"{{name}}\" was triggered.",
	"notif.alert.triggered.means": "A value Monitoring watches went past the limit set in the alert.",
	"notif.alert.doing":           "Monitoring checks again; you are told again when it triggers again after a while.",
	"notif.step.monitoring.open":  "Open Monitoring (the alerts and the charts)",
	"notif.step.storage.open":     "Open Storage",
	"notif.step.disk.free":        "Remove what is not needed, or move data to another disk.",

	// --- Fail2ban
	"notif.fail2ban.down.title":     "Fail2ban is not running",
	"notif.fail2ban.down.what":      "Fail2ban is installed and set up, but it does not answer: addresses that attack this server are not banned at the firewall.",
	"notif.fail2ban.down.means":     "SmartShield still blocks inside Cosmos. Bans at the firewall, and the bans Cosmos asks Fail2ban for, do not happen until it runs again.",
	"notif.fail2ban.down.doing":     "Cosmos looks every minute and tells you when it answers again.",
	"notif.fail2ban.restored.title": "Fail2ban runs again",
	"notif.fail2ban.restored.what":  "Fail2ban answers again since {{at}}.",
	"notif.fail2ban.restored.means": "It did not answer for {{duration}}.",
	"notif.step.security.open":      "Open Security (the Fail2ban section)",
	"notif.step.fail2ban.host":      "On the server, look at the state and the log of the fail2ban service (for example with systemctl status fail2ban).",

	// --- Telegram
	"notif.telegram.failed.title": "Telegram messages are not delivered",
	"notif.telegram.failed.what":  "Telegram does not accept Cosmos' messages: {{reason}}",
	"notif.telegram.failed.means": "Until it works again you are told in the panel only. What was not sent is counted and summed up once Telegram accepts messages again.",
	"notif.step.telegram.open":    "Open Configuration > General > Telegram notifications, check the settings and send a test message",

	// --- updates
	"notif.update.rejected.title": "A server update was not installed",
	"notif.update.rejected.what":  "A server update was refused and not installed: {{summary}}.",
	"notif.update.rejected.means": "The server keeps running its current version. Do not install the refused release by hand.",
	"notif.step.audit.open":       "Open the security record",

	// --- errors raised through MajorError
	"notif.error.backup.title":      "The configuration backup failed",
	"notif.error.backup.what":       "The backup of the configuration failed: {{message}}.",
	"notif.error.backup.means":      "The settings are not lost, but the newest copy of them is missing.",
	"notif.error.job.title":         "A scheduled job failed",
	"notif.error.job.what":          "The scheduled job {{name}} failed.",
	"notif.error.job.means":         "What it does did not happen this time.",
	"notif.error.snapraid.title":    "A SnapRAID job failed",
	"notif.error.snapraid.what":     "A SnapRAID task failed: {{message}}.",
	"notif.error.snapraid.means":    "The parity may not cover the latest changes: a disk that fails now may lose recent files.",
	"notif.error.internet.title":    "No internet connection",
	"notif.error.internet.what":     "Cosmos found no internet connection.",
	"notif.error.internet.means":    "Updates, certificate orders and checks from outside fail until it is back.",
	"notif.error.database.title":    "Database problem",
	"notif.error.database.what":     "A database problem: {{message}}.",
	"notif.error.database.means":    "Monitoring, events or notifications may not be kept.",
	"notif.error.export.title":      "The export of the containers failed",
	"notif.error.export.what":       "The export of the containers' settings failed: {{message}}.",
	"notif.error.export.means":      "The copy of the containers' settings is older than it should be.",
	"notif.error.migration.title":   "Settings from an older version need attention",
	"notif.error.migration.what":    "Settings from an older version could not be carried over: {{message}}.",
	"notif.error.socketproxy.title": "Two URLs use the same port",
	"notif.error.socketproxy.what":  "Two URLs try to use the same port: {{message}}",
	"notif.error.socketproxy.means": "Only one of them works.",
	"notif.error.mail.title":        "An e-mail could not be sent",
	"notif.error.mail.what":         "An e-mail could not be sent: {{message}}.",
	"notif.error.setup.title":       "A setup step failed",
	"notif.error.setup.what":        "A setup step failed: {{message}}.",
	"notif.error.other.title":       "Server error",
	"notif.error.other.what":        "Cosmos reported an error: {{message}}.",
	"notif.error.means":             "The technical text below says what failed.",
	"notif.step.cron.open":          "Open Scheduled jobs",
	"notif.step.general.open":       "Open Configuration > General",
	"notif.step.urls.open":          "Open URLs",

	// --- Cloudflare sign-in check and edge blocks
	"notif.turnstile.doing":     "Cosmos keeps checking with Cloudflare and marks this resolved when the sign-in check works again.",
	"notif.step.turnstile.open": "Open Configuration > Domains > Sign-in check",
	"notif.edgeblock.means":     "Until the token works, Cosmos adds no blocks at Cloudflare's edge and lifts none: SmartShield and Fail2ban still block here.",
	"notif.step.edgeblock.open": "Open Configuration > Domains and check the Cloudflare token of that domain",

	// --- generic
	"notif.doing.nothing": "Cosmos does nothing more about it by itself.",

	// --- labels of the facts
	"notif.fact.port":      "Port on the router",
	"notif.fact.internal":  "Port on this server",
	"notif.fact.protocol":  "Protocol",
	"notif.fact.router":    "Router",
	"notif.fact.server":    "This server",
	"notif.fact.external":  "Router's internet address",
	"notif.fact.answer":    "Router's answer",
	"notif.fact.attempts":  "Attempts so far",
	"notif.fact.nextTry":   "Next try",
	"notif.fact.holder":    "Port held by",
	"notif.fact.domain":    "Domain",
	"notif.fact.until":     "Valid until",
	"notif.fact.reason":    "Reason",
	"notif.fact.image":     "Image",
	"notif.fact.container": "Container",
	"notif.fact.exitCode":  "Last exit code",
	"notif.fact.exits":     "Stops within ten minutes",
	"notif.fact.kept":      "Kept as",
	"notif.fact.disk":      "Disk",
	"notif.fact.alert":     "Alert",
	"notif.fact.summary":   "Summary",
	"notif.fact.release":   "Release",
	"notif.fact.job":       "Job",
	"notif.fact.downFor":   "Down for",
	"notif.fact.metric":    "Metric",
	"notif.fact.condition": "Condition",
	// --- the state now (details view)
	"notif.state.col.port":              "Port",
	"notif.state.col.to":                "Forwarded to",
	"notif.state.col.what":              "For",
	"notif.state.upnp.configBad":        "Right now the settings still cannot be applied: {{answer}}",
	"notif.state.upnp.configOk":         "Right now the settings can be applied: Cosmos asks the router for its forwards.",
	"notif.state.upnp.off":              "Forwarding ports over UPnP is switched off now.",
	"notif.state.upnp.paused":           "Right now the router does not answer. Cosmos asks it again at {{at}}.",
	"notif.state.upnp.routerOk":         "Right now the router answers (last asked at {{at}}).",
	"notif.state.upnp.gone":             "Cosmos no longer asks the router for this port.",
	"notif.state.upnp.active":           "Right now the router forwards port {{port}} to this server (since {{since}}).",
	"notif.state.upnp.failed":           "Right now port {{port}} is not forwarded. Cosmos asks the router again at {{at}}.",
	"notif.state.upnp.failedNoRetry":    "Right now port {{port}} is not forwarded.",
	"notif.state.cert.renewing":         "A new certificate for {{domain}} is being ordered right now.",
	"notif.state.cert.missing":          "Right now {{domain}} has no certificate of its own.",
	"notif.state.cert.expired":          "Right now the certificate of {{domain}} has ended ({{until}}).",
	"notif.state.cert.valid":            "Right now the certificate of {{domain}} is valid until {{until}}.",
	"notif.state.cert.unknown":          "{{domain}} is not among the certificates this server orders now.",
	"notif.state.docker.unavailable":    "Docker does not answer right now.",
	"notif.state.container.missing":     "There is no container named {{name}} right now.",
	"notif.state.container.restarting":  "Right now {{name}} is restarting (last exit code {{code}}).",
	"notif.state.container.running":     "Right now {{name}} is running (started {{started}}).",
	"notif.state.container.stopped":     "Right now {{name}} is stopped (exit code {{code}}).",
	"notif.state.leftover.there":        "The previous container {{name}} is still kept, stopped.",
	"notif.state.leftover.gone":         "The previous container {{name}} is no longer there: it was put back or removed.",
	"notif.state.snapshots.running":     "A backup run is going on right now.",
	"notif.state.snapshots.none":        "No run has finished since Cosmos started.",
	"notif.state.snapshots.last":        "Last run: {{ok}} folders backed up cleanly, {{failed}} with problems.",
	"notif.state.disk.use":              "Right now {{disk}} is {{percent}}% full ({{free}} free).",
	"notif.state.disk.unknown":          "{{disk}} is not a disk this server has mounted right now.",
	"notif.state.fail2ban.notInstalled": "Fail2ban is not installed on this server right now.",
	"notif.state.fail2ban.running":      "Right now Fail2ban answers ({{jails}} jails of Cosmos).",
	"notif.state.fail2ban.down":         "Right now Fail2ban does not answer.",
	"notif.state.telegram.off":          "Telegram notifications are switched off now.",
	"notif.state.telegram.failing":      "Right now messages are not delivered: {{reason}}",
	"notif.state.telegram.ok":           "The last message was delivered at {{at}}.",
	"notif.state.telegram.nothingYet":   "No message was sent since Cosmos started.",
	"notif.log.unreadable":              "The server's log could not be read.",
	"notif.log.nothing":                 "The log has no line of this source around that time (the log keeps the last lines only).",
	"notif.fact.lastAsked":              "Last asked",
	"notif.fact.issuer":                 "Issued by",
	"notif.fact.renewAt":                "Renewal due",
	"notif.fact.lastAttempt":            "Last attempt",
	"notif.fact.restarts":               "Restarts",
	"notif.fact.folder":                 "Folder",
	"notif.fact.device":                 "Device",
}

var textVar = regexp.MustCompile(`\{\{(\w+)\}\}`)

// fillVars puts values into an English text.
func fillVars(text string, vars map[string]string) string {
	return textVar.ReplaceAllStringFunc(text, func(whole string) string {
		name := whole[2 : len(whole)-2]
		if value, ok := vars[name]; ok {
			return value
		}
		return whole
	})
}

// NotificationEnglish is the English of a key, "" when it has none.
func NotificationEnglish(key string) string {
	if text, ok := notificationTexts[key]; ok {
		return text
	}
	return legacyNotificationTexts[key]
}

// RenderNotificationText is a sentence in English with its values.
func RenderNotificationText(t NotificationText) string {
	text := NotificationEnglish(t.Key)
	if text == "" {
		return ""
	}
	return fillVars(text, t.Vars)
}

// LegacyNotificationEnglish is the English of an old title or message key
// with its value.
func LegacyNotificationEnglish(key string, vars string) string {
	text, ok := legacyNotificationTexts[key]
	if !ok {
		return ""
	}
	return fillVars(text, map[string]string{"Vars": vars})
}

// NotificationTextKeys lists every key with English, for the tests.
func NotificationTextKeys() []string {
	keys := []string{}
	for key := range notificationTexts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
