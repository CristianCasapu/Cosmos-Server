package utils

// Fork addition: copied from client/src/utils/locales/en/translation.json;
// TestLegacyNotificationTextsAreThePanels keeps the two equal.

// legacyNotificationTexts is the English of the notification keys that
// were written before the fork gave notifications sentences of their own.
var legacyNotificationTexts = map[string]string{
	"header.notification.message.alertTriggered":       "The alert \"{{Vars}}\" was triggered.",
	"header.notification.message.certificateExpiring":  "A certificate is close to its end and could not be renewed: {{Vars}}. See Configuration > Domains > Certificates.",
	"header.notification.message.certificateRenewed":   "The TLS certificate for the following domains has been renewed: {{Vars}}",
	"header.notification.message.certificateUntrusted": "These addresses are served directly with a certificate browsers do not trust, so visitors get a warning: {{Vars}}. See Configuration > Domains > Certificates.",
	"header.notification.message.ciBuildFailed":        "Build {{Vars}}",
	"header.notification.message.containerLeftover":    "A change of {{Vars}} was interrupted when Cosmos stopped: the previous container is kept, stopped. Nothing was deleted. Put it back or remove it at the top of the ServApps page.",
	"header.notification.message.containerUpdate":      "Container {{Vars}} updated to the latest version!",
	"header.notification.message.originPullExpiring":   "The certificate Cloudflare's proxy presents to this server expires in {{Vars}}. After that, requests through Cloudflare are refused while the check is enforced. See Configuration > Domains > Cloudflare, \"Proof that a request came through Cloudflare\".",
	"header.notification.message.originPullStopped":    "Requiring a certificate from Cloudflare's proxy was switched on but not confirmed within three minutes, so Cosmos went back to testing: nothing is refused. If the panel stopped answering through Cloudflare at that moment, the certificate is not in place at Cloudflare yet. See Configuration > Domains > Cloudflare.",
	"header.notification.message.snapshotsFailed":      "The incremental backup did not go cleanly: {{Vars}}. See the list of snapshots under Configuration > General > Incremental backup.",
	"header.notification.message.telegramFailed":       "{{Vars}} The messages that were not sent are counted and summed up once Telegram accepts messages again. See Configuration > General > Telegram notifications.",
	"header.notification.message.updateRejected":       "A server update was refused and NOT installed: {{Vars}}. The server keeps running its current version. If this repeats, a correctly signed release has to be published; do not install the refused one by hand.",
	"header.notification.message.upnpFailed":           "Cosmos could not open its port on the router over UPnP and will not try again by itself: {{Vars}}. Check the router, then press \"Check now\" under Configuration > HTTP > Internet access.",
	"header.notification.title.alertTriggered":         "Alert triggered",
	"header.notification.title.certificateExpiring":    "A certificate is not being renewed",
	"header.notification.title.certificateRenewed":     "Cosmos Certificate Renewed",
	"header.notification.title.certificateUntrusted":   "Addresses without a trusted certificate",
	"header.notification.title.ciBuildFailed":          "CI build failed",
	"header.notification.title.containerLeftover":      "A container change was interrupted",
	"header.notification.title.containerUpdate":        "Container Update",
	"header.notification.title.originPullExpiring":     "Cloudflare certificate expires soon",
	"header.notification.title.originPullStopped":      "Cloudflare certificate check not confirmed",
	"header.notification.title.serverError":            "Server Error",
	"header.notification.title.snapshotsFailed":        "Incremental backup: problems",
	"header.notification.title.telegramFailed":         "Telegram notifications are not delivered",
	"header.notification.title.updateRejected":         "Server update not installed",
	"header.notification.title.upnpFailed":             "Router port forwarding failed",
}
