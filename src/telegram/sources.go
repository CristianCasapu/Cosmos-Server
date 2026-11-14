package telegram

import (
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/azukaar/cosmos-server/src/security"
	"github.com/azukaar/cosmos-server/src/utils"
)

// What becomes a message, and in which words. Three sources:
//
//   - the security record (security.Subscribe),
//   - the notifications Cosmos raises for its administrators
//     (utils.WriteNotification), each forwarded once,
//   - a few situations nothing raised before (utils.ForkNotice).
//
// Every sentence is written here. What comes from an event or a notification
// only ever goes through composer.value.

// securitySentences are the titles of the events a phone is most likely to
// show. The other kinds use the label of the catalogue.
var securitySentences = map[string]string{
	utils.SecMFALocked:            "Two-factor check locked: too many wrong codes. Whoever tried has the password of the account.",
	utils.SecAccessGrantDenied:    "Refused to grant a permission: someone tried to hand out a permission they do not hold themselves.",
	utils.SecShieldBanTemp:        "Temporary ban: SmartShield banned a client after three strikes.",
	utils.SecShieldBanPerm:        "Permanent ban: SmartShield banned a client until it is unbanned.",
	utils.SecUserRoleChanged:      "Role changed: the role of an account was changed.",
	utils.SecHostTerminal:         "Host terminal opened: a terminal was opened on the server itself, as root.",
	utils.SecSnapshotRestore:      "Snapshot restore started: a folder is being replaced by one of its snapshots.",
	utils.SecServerUpdateRejected: "Update refused: a release was not installed because it could not be verified as signed by this fork. The server stays on its version.",
	utils.SecLoginFailed:          "Failed login: a login was refused.",
	utils.SecSudoFailed:           "Wrong password at the confirmation prompt, from a session that is logged in.",
	utils.SecMFAFailed:            "Wrong two-factor code, from a session that has the password.",
	utils.SecSessionForged:        "Forged session: a session cookie this server did not sign was presented.",
	utils.SecScanProbe:            "Probe: a path that scanners look for was requested.",
	utils.SecRecorderDropped:      "The security record could not keep up: events were lost.",
}

// detailLabels are the details of an event that are shown, in this order,
// under these names. A detail that is not listed is not sent.
var detailLabels = []struct{ key, label string }{
	{"reason", "Reason"},
	{"role", "New role"},
	{"permission", "Permission"},
	{"what", "What"},
	{"route", "URL"},
	{"limit", "Limit"},
	{"probe", "Probe"},
	{"country", "Country"},
	{"action", "Action"},
	{"mode", "Mode"},
	{"scope", "Scope"},
	{"snapshot", "Snapshot"},
	{"sections", "Changed"},
	{"token", "API token"},
	{"sources", "Sources"},
}

var outcomeWords = map[string]string{
	security.OutcomeFailed:  "failed",
	security.OutcomeDenied:  "denied",
	security.OutcomeBlocked: "blocked",
	security.OutcomeOK:      "done",
}

// loginHistory returns the last logins of an account, newest first. A
// variable so the tests need no database.
var loginHistory = func(actor string) ([]security.Event, error) {
	page, err := security.List(security.Filter{Kinds: []string{utils.SecLoginSuccess}, Actor: actor}, 0, 200)
	return page.Events, err
}

// noticeFromEvent decides whether an event of the security record is told,
// and how.
func noticeFromEvent(event security.Event, st settings) (notice, bool) {
	if event.Kind == utils.SecLoginSuccess {
		if !st.categories[CategoryLogins] || !isNewAddress(event) {
			return notice{}, false
		}
		return newAddressNotice(event), true
	}
	if !st.categories[CategorySecurity] {
		return notice{}, false
	}

	kind, known := security.KindOf(event.Kind)
	if !known {
		return notice{}, false
	}
	severity, _ := security.ParseSeverity(event.Severity)
	minimum, _ := security.ParseSeverity(st.minSeverity)
	if severity < minimum || severity < security.SeverityLow {
		return notice{}, false
	}

	nt := notice{
		category: CategorySecurity,
		key:      "sec:" + kind.Kind,
		label:    kind.Label,
		priority: priorityNormal,
		at:       event.Time,
		title:    kind.Label,
	}
	switch {
	case severity >= security.SeverityHigh:
		nt.priority = priorityCritical
	case severity <= security.SeverityLow:
		nt.priority = priorityLow
	}
	if sentence, has := securitySentences[kind.Kind]; has {
		nt.title = sentence
	}
	if event.Count > 1 {
		// a summary row of the record: that many more, not stored one by one
		nt.title = strconv.Itoa(event.Count) + " more of: " + kind.Label
	}
	nt.details = func(c *composer) { eventDetails(c, event, kind) }
	return nt, true
}

// eventDetails writes who, from where, about what, and the details that
// have a name.
func eventDetails(c *composer, event security.Event, kind security.Kind) {
	wrote := false
	if event.Actor != "" {
		c.fixed("By ").value(event.Actor, maxValue)
		wrote = true
	}
	if event.IP != "" {
		if wrote {
			c.fixed(" from ")
		} else {
			c.fixed("From ")
		}
		c.value(event.IP, maxValue)
		wrote = true
	}
	if event.Target != "" {
		if wrote {
			c.fixed(" · ")
		}
		c.fixed("About ").value(event.Target, maxValue)
		wrote = true
	}

	second := false
	add := func(label string, value string) {
		if !second {
			if wrote {
				c.line()
			}
			second = true
		} else {
			c.fixed(" · ")
		}
		c.fixed(label+": ").value(value, maxValue)
	}
	for _, detail := range detailLabels {
		if value := event.Details[detail.key]; value != "" {
			add(detail.label, value)
		}
	}
	if event.Outcome != "" && event.Outcome != kind.Outcome {
		if word, known := outcomeWords[event.Outcome]; known {
			if !second {
				if wrote {
					c.line()
				}
				second = true
			} else {
				c.fixed(" · ")
			}
			c.fixed("Outcome: " + word)
		}
	}
	if kind.Class == security.ClassIncident && event.Path != "" {
		add("Asked for", strings.TrimSpace(event.Method+" "+event.Host+event.Path))
	}
}

// isNewAddress reports whether the account of a login was not seen at this
// address before: the same IPv4 address, or the same /64 for IPv6, among
// its last 200 logins in the record. When that cannot be known (no
// database yet), the answer is no.
func isNewAddress(event security.Event) bool {
	if event.Actor == "" || event.IP == "" || event.ID == 0 {
		return false
	}
	earlier, err := loginHistory(event.Actor)
	if err != nil {
		return false
	}
	for _, login := range earlier {
		if login.ID >= event.ID {
			continue
		}
		if sameSource(login.IP, event.IP) {
			return false
		}
	}
	return true
}

// sameSource compares two client addresses the way the record's limits do.
func sameSource(a string, b string) bool {
	first, errA := netip.ParseAddr(a)
	second, errB := netip.ParseAddr(b)
	if errA != nil || errB != nil {
		return a == b
	}
	first, second = first.Unmap().WithZone(""), second.Unmap().WithZone("")
	if first.Is4() || second.Is4() {
		return first == second
	}
	prefixA, _ := first.Prefix(64)
	prefixB, _ := second.Prefix(64)
	return prefixA == prefixB
}

func newAddressNotice(event security.Event) notice {
	return notice{
		category: CategoryLogins,
		key:      "login.new",
		label:    "Login from a new address",
		priority: priorityNormal,
		at:       event.Time,
		title:    "Login from a new address: a password was accepted from an address not seen before for this account.",
		details: func(c *composer) {
			c.fixed("Account ").value(event.Actor, maxValue).fixed(" from ").value(event.IP, maxValue)
			if event.Details["mfa"] == "pending" {
				c.fixed(" · a two-factor code is still to come")
			}
		},
	}
}

// The titles of the notifications Cosmos raises, as translation keys.
const (
	titleServerError        = "header.notification.title.serverError"
	titleCertificateRenewed = "header.notification.title.certificateRenewed"
	titleAlertTriggered     = "header.notification.title.alertTriggered"
	titleUPnPFailed         = "header.notification.title.upnpFailed"
	titleSnapshotsFailed    = "header.notification.title.snapshotsFailed"
	titleContainerUpdate    = "header.notification.title.containerUpdate"
	titleCertExpiring       = "header.notification.title.certificateExpiring"  // src/certs/notify.go
	titleCertUntrusted      = "header.notification.title.certificateUntrusted" // src/certs/notify.go
	// raised by src/cloudflare/originpulls.go
	titleOriginPullExpiring = "header.notification.title.originPullExpiring"
	titleOriginPullStopped  = "header.notification.title.originPullStopped"
	// TitleDeliveryFailed is this package's own notification. It is never
	// forwarded: Telegram is what does not work.
	TitleDeliveryFailed   = "header.notification.title.telegramFailed"
	MessageDeliveryFailed = "header.notification.message.telegramFailed"
)

// The names of the alerts that exist without the administrator writing
// them: "Disk Full Notification" for "/" (upstream) and "Disk Full
// Notification (<disk>)" for each data disk (metrics/fork_disk_alerts.go),
// and upstream's two alerts on what containers use.
const diskAlertPrefix = "Disk Full Notification"

var appAlertNames = map[string]bool{"Anti Crypto-Miner": true, "Anti Memory Leak": true}

// serverErrors sorts the "Server Error" notifications (utils.MajorError) by
// how their text starts. The first match wins; what matches nothing is the
// catch-all.
var serverErrors = []struct {
	prefix   string
	category string
	key      string
	title    string
}{
	{"Backup: ", CategorySystem, "err:backup", "The backup of the configuration failed."},
	{"Couldn't get the TLS certificate", CategorySystem, "err:certificate", "A certificate could not be obtained."},
	{"Zones: couldn't get the TLS certificate", CategorySystem, "err:certificate", "A certificate could not be obtained."},
	{"CRON job SnapRAID", CategoryStorage, "err:snapraid", "A SnapRAID job failed."},
	{"Failed to status ", CategoryStorage, "err:snapraid", "SnapRAID could not report its status."},
	{"Failed to create SnapRAID", CategoryStorage, "err:snapraid", "The SnapRAID configuration could not be written."},
	{"CRON job ", CategorySystem, "err:job", "A scheduled job failed."},
	{"Container failed to update", CategoryApps, "err:container-update", "A container could not be updated."},
	{"Your server has no internet connection", CategorySystem, "err:internet", "The server had no internet connection."},
	{"Database Retention", CategorySystem, "err:database", "The clean-up of the database failed."},
	{"Cannot open the monitoring database", CategorySystem, "err:database", "The monitoring database could not be opened."},
}

// noticeFromNotification turns a notification for the administrators into
// a notice. Notifications for a single user are theirs, not the chat's.
func noticeFromNotification(n utils.Notification, config utils.Config) (notice, bool) {
	if n.Recipient != "admin" && n.Recipient != "all" {
		return notice{}, false
	}
	if n.Title == TitleDeliveryFailed {
		return notice{}, false
	}

	vars := n.Vars
	withVars := func(before string, after string) func(c *composer) {
		return func(c *composer) {
			c.fixed(before).value(hideSecrets(vars, config), maxFreeText).fixed(after)
		}
	}

	switch n.Title {
	case titleServerError:
		text := hideSecrets(n.Message, config)
		nt := notice{category: CategoryOther, key: "err:other", label: "Server error", priority: priorityNormal,
			title: "Server error."}
		for _, known := range serverErrors {
			if strings.HasPrefix(n.Message, known.prefix) {
				nt.category, nt.key, nt.title = known.category, known.key, known.title
				nt.label = strings.TrimSuffix(known.title, ".")
				break
			}
		}
		nt.details = func(c *composer) { c.value(strings.TrimSuffix(strings.TrimSpace(text), ":"), maxFreeText) }
		return nt, true

	case titleCertificateRenewed:
		return notice{category: CategorySystem, key: "certificate.renewed", label: "Certificate renewed", priority: priorityLow,
			title: "A certificate was renewed.", details: withVars("For ", "")}, true

	case titleCertExpiring:
		return notice{category: CategorySystem, key: "certificate.expiring", label: "Certificate not renewed", priority: priorityNormal,
			title:   "A certificate is close to its end and could not be renewed. See Configuration > Domains > Certificates.",
			details: withVars("", "")}, true

	case titleCertUntrusted:
		return notice{category: CategorySystem, key: "certificate.untrusted", label: "Address without a trusted certificate", priority: priorityNormal,
			title:   "Addresses served directly are answered with a certificate browsers do not trust: visitors get a warning. See Configuration > Domains > Certificates.",
			details: withVars("", "")}, true

	case titleUPnPFailed:
		return notice{category: CategorySystem, key: "upnp.failed", label: "Router port forwarding failed", priority: priorityNormal,
			title:   "Router port forwarding failed: Cosmos could not open its port on the router over UPnP and will not try again by itself.",
			details: withVars("Reason: ", "")}, true

	case titleSnapshotsFailed:
		return notice{category: CategorySystem, key: "snapshots.failed", label: "Incremental backup: problems", priority: priorityNormal,
			title:   "The incremental backup did not go cleanly.",
			details: withVars("", "")}, true

	case titleOriginPullExpiring:
		return notice{category: CategorySystem, key: "originpull.expiring", label: "Cloudflare certificate expires", priority: priorityNormal,
			title:   "The certificate Cloudflare's proxy presents to this server expires soon. After that, requests through Cloudflare are refused while the check is enforced.",
			details: withVars("Expires in ", "")}, true

	case titleOriginPullStopped:
		return notice{category: CategorySystem, key: "originpull.stopped", label: "Cloudflare certificate check not confirmed", priority: priorityNormal,
			title: "Requiring a certificate from Cloudflare's proxy was not confirmed in time and is back to testing: nothing is refused."}, true

	case titleContainerUpdate:
		return notice{category: CategoryApps, key: "container.updated", label: "Container updated", priority: priorityLow,
			title: "A container was updated to the latest version.", details: withVars("Container ", "")}, true

	case titleAlertTriggered:
		nt := notice{category: CategorySystem, key: "alert:other", label: "Monitoring alert", priority: priorityNormal,
			title: "A monitoring alert was triggered.", details: withVars("Alert ", "")}
		// The notification carries the alert's name only. The alerts
		// themselves are in a map that the monitoring changes in place
		// while it runs, so it is not read here: the alerts Cosmos and
		// this fork create are told by their names, any other is "system".
		switch {
		case strings.HasPrefix(vars, diskAlertPrefix):
			nt.category, nt.key, nt.label = CategoryStorage, "alert:disk", "Disk alert"
			nt.title = "A disk alert was triggered: a disk is nearly full."
		case appAlertNames[vars]:
			nt.category, nt.key, nt.label = CategoryApps, "alert:apps", "App alert"
			nt.title = "A monitoring alert about an app was triggered."
		}
		if n.Level == "info" {
			nt.priority = priorityLow
		}
		return nt, true
	}

	// anything else Cosmos raises, now or in a later version
	nt := notice{category: CategoryOther, key: "notification:other", label: "Notification", priority: priorityNormal,
		title: "Cosmos raised a notification."}
	if n.Level == "info" || n.Level == "success" {
		nt.priority = priorityLow
	}
	title, message := n.Title, n.Message
	nt.details = func(c *composer) {
		wrote := false
		for _, text := range []string{title, message, vars} {
			// a translation key says nothing on a phone
			if text == "" || isTranslationKey(text) {
				continue
			}
			if wrote {
				c.fixed(" · ")
			}
			c.value(hideSecrets(text, config), maxFreeText)
			wrote = true
		}
	}
	return nt, true
}

// isTranslationKey recognises "header.notification.title.something".
func isTranslationKey(text string) bool {
	if !strings.Contains(text, ".") || strings.ContainsAny(text, " \t\n") {
		return false
	}
	for _, r := range text {
		if !(r == '.' || r == '_' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

const hiddenSecret = "[hidden]"

// knownSecrets are the secrets of the configuration that live in its maps
// and lists (DNS provider credentials, backup passwords, OpenID client
// secrets). Other parts of the server change those maps in place while it
// runs, so they are never read from the configuration in use: the list is
// made by RememberSecrets, at moments when nothing else can be changing
// the configuration it is given.
var knownSecrets atomic.Pointer[[]string]

// RememberSecrets notes the secrets of a configuration, to cut them out of
// texts that are forwarded later. It must be given a configuration that no
// other goroutine is changing: the one just loaded when the server starts,
// or the one of a save before it is put to use.
func RememberSecrets(config utils.Config) {
	secrets := []string{}
	for _, value := range config.HTTPConfig.DNSChallengeConfig {
		secrets = append(secrets, value)
	}
	for _, zone := range config.HTTPConfig.DNSZones {
		for _, value := range zone.DNSChallengeConfig {
			secrets = append(secrets, value)
		}
	}
	for _, backup := range config.Backup.Backups {
		secrets = append(secrets, backup.Password)
	}
	for _, client := range config.OpenIDClients {
		secrets = append(secrets, client.Secret)
	}
	kept := secrets[:0]
	for _, secret := range secrets {
		if worthHiding(secret) {
			kept = append(kept, secret)
		}
	}
	if len(kept) > maxKnownSecrets {
		kept = kept[:maxKnownSecrets]
	}
	knownSecrets.Store(&kept)
}

const maxKnownSecrets = 256

// worthHiding: short values are not secrets worth the name, and cutting
// them out of every text would shred ordinary words.
func worthHiding(secret string) bool {
	return len(secret) >= 8 && secret != MaskedValue
}

// hideSecrets cuts the secrets of the configuration out of a free text
// (the text of an error can quote what it failed with), and anything that
// has the shape of a bot token.
func hideSecrets(text string, config utils.Config) string {
	secrets := []string{
		config.Telegram.BotToken,
		config.EmailConfig.Password,
		config.Database.PostgresPassword,
		config.ServerToken,
		config.Licence,
	}
	if remembered := knownSecrets.Load(); remembered != nil {
		secrets = append(secrets, *remembered...)
	}
	for _, secret := range secrets {
		if worthHiding(secret) {
			text = strings.ReplaceAll(text, secret, hiddenSecret)
		}
	}
	return tokenShapePattern.ReplaceAllString(text, hiddenToken)
}

// noticeFromSituation puts a situation reported with utils.ForkNotice into
// words.
func noticeFromSituation(kind string, values map[string]string) (notice, bool) {
	switch kind {
	case utils.NoticeUpdateAvailable:
		return notice{category: CategorySystem, key: kind, label: "Server update available", priority: priorityLow,
			title: "A server update is available.",
			details: func(c *composer) {
				c.fixed("Version ").value(values["version"], maxValue).fixed(" (running: ").value(values["current"], maxValue).fixed(").")
				if values["at"] != "" {
					c.fixed(" It is installed automatically at ").value(values["at"], 5).fixed(".")
				} else {
					c.fixed(" Automatic updates are off: install it under Configuration > General.")
				}
			}}, true

	case utils.NoticeUpdateFailed:
		return notice{category: CategorySystem, key: kind, label: "Server update failed", priority: priorityNormal,
			title: "The server update could not be installed: Cosmos keeps running the current version.",
			details: func(c *composer) {
				c.fixed("Version ").value(values["version"], maxValue)
				if values["reason"] != "" {
					c.fixed(" · Reason: ").value(values["reason"], maxFreeText)
				}
			}}, true

	case utils.NoticeContainerDying:
		return notice{category: CategoryApps, key: kind, label: "Container keeps stopping", priority: priorityNormal,
			title: "A container keeps stopping with an error.",
			details: func(c *composer) {
				c.fixed("Container ").value(values["name"], maxValue).fixed(" stopped ").value(values["exits"], 4).
					fixed(" times within ten minutes (last exit code ").value(values["code"], 8).fixed(").")
			}}, true
	}
	return notice{}, false
}
