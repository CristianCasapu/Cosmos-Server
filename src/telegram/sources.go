package telegram

import (
	"net/netip"
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

// securitySentences were the titles of the security events before the
// table of messages.go; the kinds with words of their own are in the table
// now. Kept as the list of the events the old messages had sentences for:
// fork_edge.go and fork_upnp.go add to it, and a test checks that each of
// them has its kind in the table.
var securitySentences = map[string]string{
	utils.SecMFALocked:            "",
	utils.SecAccessGrantDenied:    "",
	utils.SecShieldBanTemp:        "",
	utils.SecShieldBanPerm:        "",
	utils.SecUserRoleChanged:      "",
	utils.SecHostTerminal:         "",
	utils.SecSnapshotRestore:      "",
	utils.SecServerUpdateRejected: "",
	utils.SecLoginFailed:          "",
	utils.SecSudoFailed:           "",
	utils.SecMFAFailed:            "",
	utils.SecSessionForged:        "",
	utils.SecScanProbe:            "",
	utils.SecRecorderDropped:      "",
}

// eventVars are the details of an event that are passed to the texts, and
// so may be shown. A detail that is not listed is never sent.
var eventVars = []string{"reason", "form", "role", "permission", "what", "route", "limit", "probe", "country", "action", "mode", "via", "scope", "snapshot", "sections", "token", "sources", "jail"}

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
		if !(st.categories[CategoryLogins] || st.forced("auth.login.new")) || !isNewAddress(event) {
			return notice{}, false
		}
		return newAddressNotice(event), true
	}

	kind, known := security.KindOf(event.Kind)
	if !known {
		return notice{}, false
	}
	name := kind.Kind
	if !knownKind(name) || texts.Kinds[name].Category != CategorySecurity {
		name = "security.other"
	}
	if !st.forced(name) {
		if !st.categories[CategorySecurity] {
			return notice{}, false
		}
		severity, _ := security.ParseSeverity(event.Severity)
		minimum, _ := security.ParseSeverity(st.minSeverity)
		if severity < minimum || severity < security.SeverityLow {
			return notice{}, false
		}
	}

	vars := map[string]string{
		"actor": event.Actor, "ip": event.IP, "target": event.Target,
		"_label": kind.Label, "_description": kind.Description,
	}
	for _, key := range eventVars {
		if value := event.Details[key]; value != "" {
			vars[key] = value
		}
	}
	subject := []string{}
	for _, part := range []string{event.Actor, event.Target, event.IP} {
		if part != "" {
			subject = append(subject, part)
		}
	}
	vars["subject"] = strings.Join(subject, " · ")
	if vars["count"] == "" {
		vars["count"] = event.Details["count"]
	}

	nt := newNotice(name, vars)
	nt.key = "sec:" + kind.Kind
	nt.at = event.Time
	if name == "security.other" {
		severity, _ := security.ParseSeverity(event.Severity)
		switch {
		case severity >= security.SeverityHigh:
			nt.severity = sevCritical
		case severity >= security.SeverityMedium:
			nt.severity = sevWarn
		default:
			nt.severity = sevInfo
		}
	}
	if event.Count > 1 {
		// a summary row of the record: that many more, not stored one by one
		nt.counted = event.Count
	}
	if event.Outcome != "" && event.Outcome != kind.Outcome {
		if word, known := outcomeWords[event.Outcome]; known {
			nt.extra = func(c *composer) { c.fixed("Outcome: " + word + ".") }
		}
	}
	if kind.Class == security.ClassIncident && event.Path != "" {
		nt.raw = strings.TrimSpace(event.Method + " " + event.Host + event.Path)
	}
	return nt, true
}

var outcomeWords = map[string]string{
	security.OutcomeFailed:  "failed",
	security.OutcomeDenied:  "denied",
	security.OutcomeBlocked: "blocked",
	security.OutcomeOK:      "done",
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
	nt := newNotice("auth.login.new", map[string]string{"actor": event.Actor, "ip": event.IP})
	nt.at = event.Time
	if event.Details["mfa"] == "pending" {
		nt.extra = func(c *composer) { c.fixed("A two-factor code is still to come.") }
	}
	return nt
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
	// raised by src/turnstile/watch.go (Fork addition)
	titleTurnstileProblem      = "header.notification.title.turnstileProblem"
	messageTurnstileKeyRefused = "header.notification.message.turnstileKeyRefused"
	// raised by src/edgeblock/events.go (Fork addition)
	titleEdgeBlocksProblem = "header.notification.title.edgeBlocksProblem"
	messageEdgeBlocksToken = "header.notification.message.edgeBlocksToken"
	messageEdgeBlocksWAF     = "header.notification.message.edgeBlocksPermissionWAF"
	messageEdgeBlocksAccount = "header.notification.message.edgeBlocksPermissionAccount"
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
	kind     string
	category string
}{
	{"Backup: ", "error.backup", ""},
	{"Couldn't get the TLS certificate", "cert.order.failed", ""},
	{"Zones: couldn't get the TLS certificate", "cert.order.failed", ""},
	{"CRON job SnapRAID", "error.snapraid", ""},
	{"Failed to status ", "error.snapraid", ""},
	{"Failed to create SnapRAID", "error.snapraid", ""},
	{"CRON job ", "error.job", ""},
	{"Container failed to update", "container.update.failed", ""},
	{"Your server has no internet connection", "error.internet", ""},
	{"Database Retention", "error.database", ""},
	{"Cannot open the monitoring database", "error.database", ""},
}

// noticeFromNotification turns a notification for the administrators into
// a notice. Notifications for a single user are theirs, not the chat's.
func noticeFromNotification(n utils.Notification, config utils.Config) (notice, bool) {
	if n.Recipient != "admin" && n.Recipient != "all" {
		return notice{}, false
	}
	if n.Title == TitleDeliveryFailed || (n.Fork != nil && n.Fork.Kind == "telegram.failed") {
		return notice{}, false
	}
	// a notification that carries its sentences is told with them
	if n.Fork != nil && n.Fork.What != nil {
		return noticeFromInfo(n, config), true
	}

	vars := n.Vars
	// the old notifications carry one free text: it names the subject of
	// the first line when it is short, and is shown as the text at the end
	withVar := func(kind string, name string) notice {
		nt := newNotice(kind, map[string]string{name: vars})
		if texts.Kinds[nt.kind].What == "" {
			nt.raw = vars
		}
		return nt
	}

	switch n.Title {
	case titleServerError:
		nt := newNotice("error.other", map[string]string{})
		for _, known := range serverErrors {
			if strings.HasPrefix(n.Message, known.prefix) {
				nt = newNotice(known.kind, map[string]string{})
				break
			}
		}
		// the text of the error: what happened, at the end, shortened
		nt.vars["message"] = strings.TrimSuffix(strings.TrimSpace(n.Message), ":")
		nt.raw = nt.vars["message"]
		return nt, true

	case titleCertificateRenewed:
		return withVar("cert.renewed", "domains"), true
	case titleCertExpiring:
		return withVar("cert.expiring", "domain"), true
	case titleCertUntrusted:
		return withVar("cert.untrusted", "hosts"), true
	case titleUPnPFailed:
		nt := newNotice("upnp.forward.failed", map[string]string{})
		nt.raw = vars
		return nt, true
	case titleSnapshotsFailed:
		return withVar("snapshots.failed", "summary"), true
	case titleOriginPullExpiring:
		return withVar("originpull.expiring", "text"), true
	case titleOriginPullStopped:
		return newNotice("originpull.stopped", nil), true
	case titleTurnstileProblem:
		nt := newNotice("turnstile.problem", nil)
		if n.Message == messageTurnstileKeyRefused {
			nt.key = "turnstile.key"
		}
		return nt, true
	case titleEdgeBlocksProblem:
		nt := withVar("edgeblock.problem", "text")
		nt.key = "edgeblocks:" + n.Message
		return nt, true
	case titleContainerUpdate:
		return withVar("container.updated", "name"), true
	case titleAlertTriggered:
		nt := withVar("alert.triggered", "name")
		// The notification carries the alert's name only. The alerts
		// themselves are in a map that the monitoring changes in place
		// while it runs, so it is not read here: the alerts Cosmos and
		// this fork create are told by their names, any other is "system".
		switch {
		case strings.HasPrefix(vars, diskAlertPrefix):
			nt = newNotice("alert.disk", map[string]string{"name": vars, "disk": strings.Trim(strings.TrimSpace(strings.TrimPrefix(vars, diskAlertPrefix)), "()")})
			if nt.vars["disk"] == "" {
				nt.vars["disk"] = "/"
			}
		case appAlertNames[vars]:
			nt.category = CategoryApps
		}
		nt.severity = levelSeverity(n.Level, nt.severity)
		return nt, true
	}

	// anything else Cosmos raises, now or in a later version
	parts := []string{}
	for _, text := range []string{n.Title, n.Message, vars} {
		// a translation key says nothing on a phone
		if text != "" && !isTranslationKey(text) {
			parts = append(parts, text)
		}
	}
	nt := newNotice("notification.other", map[string]string{"text": strings.Join(parts, " · ")})
	if len(parts) > 0 {
		nt.vars["title"] = parts[0]
	}
	nt.severity = levelSeverity(n.Level, sevWarn)
	return nt, true
}

// levelSeverity is the severity a notification's level allows: an
// information stays one, a warning stays a warning; an error is what the
// kind says (at least an error).
func levelSeverity(level string, kind severity) severity {
	switch strings.ToLower(level) {
	case "info", "success":
		return sevInfo
	case "warning", "warn":
		if kind > sevWarn {
			return sevWarn
		}
		return kind
	case "error":
		if kind < sevError {
			return sevError
		}
	}
	return kind
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
		return newNotice("update.available", values), true

	case utils.NoticeUpdateFailed:
		nt := newNotice("update.failed", values)
		nt.raw = values["reason"]
		return nt, true

	case utils.NoticeContainerDying:
		return newNotice("container.dying", values), true

	case utils.NoticePublicAddressChanged:
		return addressNotice(values), true
	}
	return notice{}, false
}

// addressNotice tells that the server's address on the internet changed
// (plan 47, E4, reports it): old, new, records ("updated", "not_updated",
// "none") and shared ("yes" when the new address is a carrier's shared one,
// which visitors cannot reach).
func addressNotice(values map[string]string) notice {
	vars := map[string]string{"old": values["old"], "new": values["new"]}
	nt := newNotice("address.changed", vars)
	nt.key = "address.changed"
	sentences := map[string]string{
		"updated":     "The DNS records were updated.",
		"not_updated": "The DNS records could not be updated: visitors are sent to the old address.",
		"none":        "Cosmos manages no DNS record for it.",
	}
	records := sentences[values["records"]]
	switch values["records"] {
	case "updated", "none":
		nt.severity = sevInfo
	case "not_updated":
		nt.severity = sevError
	}
	shared := values["shared"] == "yes"
	if shared && nt.severity < sevError {
		nt.severity = sevError
	}
	nt.extra = func(c *composer) {
		if records != "" {
			c.fixed(records)
		}
		if shared {
			if records != "" {
				c.fixed(" ")
			}
			c.fixed("The new address is shared by the internet provider among many customers (CGNAT): visitors from outside cannot reach this server directly.")
		}
	}
	return nt
}
