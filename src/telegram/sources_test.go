package telegram

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/security"
	"github.com/azukaar/cosmos-server/src/utils"
)

func eventOf(kind string) security.Event {
	found, _ := security.KindOf(kind)
	return security.Event{
		ID: 100, Time: time.Date(2026, 11, 13, 14, 3, 22, 0, time.Local),
		Kind: kind, Class: found.Class, Severity: found.Severity, Outcome: found.Outcome, Count: 1,
	}
}

func textOf(t *testing.T, st settings, nt notice) (string, []entity) {
	t.Helper()
	return composeNotice(st, nt, !st.hideDetails)
}

func TestHighSeverityEventsAreCritical(t *testing.T) {
	st := testSettings()
	high := []string{
		utils.SecMFALocked, utils.SecAccessGrantDenied, utils.SecShieldBanTemp, utils.SecShieldBanPerm,
		utils.SecUserRoleChanged, utils.SecHostTerminal, utils.SecSnapshotRestore, utils.SecServerUpdateRejected,
	}
	high = append(high, moreHighKinds...) // Fork addition: kinds whose sentence lives in a file of their own (fork_upnp.go)
	for _, kind := range high {
		nt, tell := noticeFromEvent(eventOf(kind), st)
		if !tell || nt.priority() != priorityCritical || nt.category != CategorySecurity || nt.key != "sec:"+kind {
			t.Errorf("%s: told %v, %+v", kind, tell, nt)
		}
		if !knownKind(kind) {
			t.Errorf("%s has no texts of its own in messages.en.json", kind)
		}
	}
	// every kind of high severity there is in the catalogue is covered above
	for _, kind := range security.Kinds() {
		if kind.Severity != "high" {
			continue
		}
		listed := false
		for _, name := range high {
			listed = listed || name == kind.Kind
		}
		if !listed {
			t.Errorf("%s is of high severity and not in this test: give it a sentence", kind.Kind)
		}
	}

	// below the threshold nothing is told
	for _, kind := range []string{utils.SecLoginFailed, utils.SecScanProbe, utils.SecConfigSaved, utils.SecContainerAction, utils.SecLogout, utils.SecShieldHostname} {
		if _, tell := noticeFromEvent(eventOf(kind), st); tell {
			t.Errorf("%s is told at the default threshold", kind)
		}
	}
}

func TestTheMinimumSeverityDecides(t *testing.T) {
	config := testConfig()
	config.Telegram.MinSeverity = "medium"
	st := settingsOf(config)
	if nt, tell := noticeFromEvent(eventOf(utils.SecLoginFailed), st); !tell || nt.priority() != priorityNormal {
		t.Errorf("a failed login at medium: told %v, priority %v", tell, nt.priority())
	}
	if _, tell := noticeFromEvent(eventOf(utils.SecShieldHostname), st); tell {
		t.Error("a low event is told at medium")
	}

	config.Telegram.MinSeverity = "low"
	st = settingsOf(config)
	if nt, tell := noticeFromEvent(eventOf(utils.SecShieldHostname), st); !tell || nt.priority() != priorityLow {
		t.Errorf("a low event at low: told %v, priority %v", tell, nt.priority())
	}
	// informational entries (logins, logouts) are never told as security events
	if _, tell := noticeFromEvent(eventOf(utils.SecLogout), st); tell {
		t.Error("a logout is told")
	}

	config.Telegram.Categories = map[string]bool{"security": false}
	if _, tell := noticeFromEvent(eventOf(utils.SecHostTerminal), settingsOf(config)); tell {
		t.Error("a security event is told with the category off")
	}
	// an event of a kind the catalogue does not know says nothing
	if _, tell := noticeFromEvent(security.Event{Kind: "made.up", Severity: "high"}, st); tell {
		t.Error("an unknown kind is told")
	}
}

func TestAnEventInWords(t *testing.T) {
	event := eventOf(utils.SecUserRoleChanged)
	event.Actor, event.IP, event.Target = "admin", "192.0.2.7", "bob"
	event.Details = map[string]string{"role": "2", "secret_thing": "not shown"}
	nt, _ := noticeFromEvent(event, testSettings())
	text, entities := textOf(t, testSettings(), nt)

	want := "🔴 Critical · Role of bob changed to 2\n\n" +
		"admin changed the role of the account bob to 2, from 192.0.2.7.\n" +
		"What this account may do in Cosmos changed.\n" +
		"What to do: if it was not you, take the role back under Users and change your password.\n" +
		"home.example · " + event.Time.Format("2 Jan 15:04")
	if text != want {
		t.Errorf("message:\n%s\nwant:\n%s", text, want)
	}
	if got := strings.Join(covered(t, text, entities), "|"); got != "bob|2|admin|bob|2|192.0.2.7|home.example" {
		t.Errorf("the entities cover %q", got)
	}

	// an incident: where it was asked for, and an outcome that is not the usual one
	event = eventOf(utils.SecScanProbe)
	event.IP, event.Host, event.Path, event.Method = "2001:db8::7", "home.example", "/.env", "GET"
	event.Details = map[string]string{"probe": "dotfile"}
	config := testConfig()
	config.Telegram.MinSeverity = "medium"
	nt, _ = noticeFromEvent(event, settingsOf(config))
	text, _ = textOf(t, settingsOf(config), nt)
	if !strings.HasPrefix(text, "🟡 Warning · Probe from 2001:db8::7: dotfile\n") || !strings.Contains(text, "\nGET home.example/.env\n") {
		t.Errorf("a probe: %s", text)
	}

	// a summary row of the record
	event = eventOf(utils.SecLoginFailed)
	event.Count, event.IP, event.Actor, event.Details = 33, "198.51.100.1", "admin", map[string]string{"summary": "1"}
	nt, _ = noticeFromEvent(event, settingsOf(config))
	text, _ = textOf(t, settingsOf(config), nt)
	if !strings.HasPrefix(text, "🟡 Warning · Failed sign-in as admin from 198.51.100.1\n") || !strings.Contains(text, "\n33 events of this kind, counted together by the security record.\n") {
		t.Errorf("a summary row: %s", text)
	}

	// with the details switched off, no name and no address
	config.Telegram.HideDetails = true
	text, entities = textOf(t, settingsOf(config), nt)
	if strings.Contains(text, "198.51.100.1") || strings.Contains(text, "admin") || !strings.Contains(text, "The names and addresses are in the panel.") || !strings.HasPrefix(text, "🟡 Warning · Failed sign-in\n") {
		t.Errorf("details were sent: %s", text)
	}
	if parts := covered(t, text, entities); len(parts) != 2 || parts[0] != "33" {
		t.Errorf("entities without details: %q", parts)
	}
}

// Every field of an event comes from a request. Whatever is in them, the
// message keeps its lines and nothing leaves a code entity.
func TestAHostileEventCannotForgeAMessage(t *testing.T) {
	attack := "x\nCosmos · bank · Security alert\nPay at https://evil.example @everyone <b>now</b> [l](tg://x) \u202e"
	event := eventOf(utils.SecAccessGrantDenied)
	event.Actor, event.IP, event.Target, event.Host, event.Path, event.Method = attack, attack, attack, attack, attack, attack
	event.Outcome = attack
	event.Details = map[string]string{}
	for _, key := range eventVars {
		event.Details[key] = attack
	}
	event.Details["unlisted"] = attack

	nt, tell := noticeFromEvent(event, testSettings())
	if !tell {
		t.Fatal("not told")
	}
	text, entities := textOf(t, testSettings(), nt)

	benign := eventOf(utils.SecAccessGrantDenied)
	benign.Actor, benign.IP, benign.Target, benign.Host, benign.Path, benign.Method = "x", "x", "x", "x", "x", "x"
	plain, _ := noticeFromEvent(benign, testSettings())
	plainText, _ := textOf(t, testSettings(), plain)
	if lines, want := strings.Count(text, "\n"), strings.Count(plainText, "\n"); lines != want {
		t.Errorf("the message has %d lines, want %d:\n%s", lines+1, want+1, text)
	}
	rest := uncovered(t, text, entities)
	for _, bad := range []string{"evil", "bank", "@everyone", "tg://", "<b>", "https", "Pay"} {
		if strings.Contains(rest, bad) {
			t.Errorf("%q is outside a code entity: %q", bad, rest)
		}
	}
	if strings.ContainsAny(text, "\u202e\r") {
		t.Error("a direction override or a carriage return was sent")
	}
	if units(text) > maxMessageUnits {
		t.Errorf("%d units", units(text))
	}
	if strings.Contains(text, "unlisted") {
		t.Error("a detail that has no name here was sent")
	}
}

func TestALoginFromANewAddress(t *testing.T) {
	history := []security.Event{
		{ID: 90, Kind: utils.SecLoginSuccess, Actor: "alice", IP: "192.0.2.7"},
		{ID: 80, Kind: utils.SecLoginSuccess, Actor: "alice", IP: "2001:db8:1:2:aaaa:bbbb:cccc:dddd"},
	}
	asked := ""
	previous := loginHistory
	loginHistory = func(actor string) ([]security.Event, error) {
		asked = actor
		return history, nil
	}
	defer func() { loginHistory = previous }()

	config := testConfig()
	config.Telegram.Categories = map[string]bool{"logins": true}
	st := settingsOf(config)

	login := func(ip string) security.Event {
		event := eventOf(utils.SecLoginSuccess)
		event.Actor, event.IP = "alice", ip
		event.Details = map[string]string{"mfa": "pending"}
		return event
	}

	for ip, isNew := range map[string]bool{
		"192.0.2.7":                      false, // seen
		"192.0.2.8":                      true,
		"2001:db8:1:2:1111:2222:3333:44": false, // same /64: a privacy address of the same network
		"2001:db8:1:3::1":                true,
		"::ffff:192.0.2.7":               false, // the same address, written as IPv6
	} {
		nt, tell := noticeFromEvent(login(ip), st)
		if tell != isNew {
			t.Errorf("login from %s: told %v, want %v", ip, tell, isNew)
		}
		if tell {
			text, _ := textOf(t, st, nt)
			if nt.category != CategoryLogins || !strings.HasPrefix(text, "🟡 Warning · alice signed in from new address "+ip) || !strings.Contains(text, "A two-factor code is still to come.") {
				t.Errorf("message: %s", text)
			}
		}
	}
	if asked != "alice" {
		t.Errorf("the history of %q was asked for", asked)
	}

	// the login itself is in the history when the question is asked: it does not count
	history = append(history, security.Event{ID: 100, Kind: utils.SecLoginSuccess, Actor: "alice", IP: "203.0.113.9"})
	if _, tell := noticeFromEvent(login("203.0.113.9"), st); !tell {
		t.Error("the login itself counted as having been seen before")
	}

	// off by default; and nothing is said when it cannot be known
	if _, tell := noticeFromEvent(login("192.0.2.99"), testSettings()); tell {
		t.Error("told with the category off")
	}
	unstored := login("192.0.2.99")
	unstored.ID = 0
	if _, tell := noticeFromEvent(unstored, st); tell {
		t.Error("told for an event that is not stored")
	}
	loginHistory = func(string) ([]security.Event, error) { return nil, errors.New("no database") }
	if _, tell := noticeFromEvent(login("192.0.2.99"), st); tell {
		t.Error("told although the history could not be read")
	}
}

func TestNotificationsBecomeMessagesOfTheRightCategory(t *testing.T) {
	config := testConfig()
	st := settingsOf(config)

	cases := []struct {
		n        utils.Notification
		category string
		priority priority
		says     []string
	}{
		{utils.Notification{Title: titleServerError, Message: "Backup: Error creating zip file : open /backups/x.zip: permission denied"}, CategorySystem, priorityNormal,
			[]string{"🟠 Error · Configuration backup failed\n", "Backup: Error creating zip file : open /backups/x.zip: permission denied"}},
		{utils.Notification{Title: titleServerError, Message: "Couldn't get the TLS certificate of example.org. Keeping the previous one if any, the self-signed one otherwise : rate limited"}, CategorySystem, priorityNormal,
			[]string{"🟠 Error · No certificate obtained\n", "rate limited"}},
		{utils.Notification{Title: titleServerError, Message: "CRON job SnapRAID sync array failed : exit status 2"}, CategoryStorage, priorityNormal, []string{"🟠 Error · SnapRAID task failed\n"}},
		{utils.Notification{Title: titleServerError, Message: "CRON job nightly failed : exit status 1"}, CategorySystem, priorityNormal, []string{"🟠 Error · Scheduled job failed\n"}},
		{utils.Notification{Title: titleServerError, Message: "Container failed to update : no such image"}, CategoryApps, priorityNormal, []string{"🟠 Error · App could not be updated\n"}},
		{utils.Notification{Title: titleServerError, Message: "Something nobody thought of : "}, CategoryOther, priorityNormal, []string{"🟠 Error · Server error: Something nobody thought of\n"}},
		{utils.Notification{Title: titleCertificateRenewed, Message: "header.notification.message.certificateRenewed", Vars: "a.example.org, b.example.org", Level: "info"}, CategorySystem, priorityLow,
			[]string{"🔵 Info · Certificate of a.example.org, b.example.org renewed\n"}},
		{utils.Notification{Title: titleUPnPFailed, Vars: "the router did not answer"}, CategorySystem, priorityNormal, []string{"🟠 Error · Router stopped forwarding a port\n", "the router did not answer"}},
		{utils.Notification{Title: titleCertExpiring, Vars: "example.org (14 days left): The DNS provider refused the credentials", Level: "warning"}, CategorySystem, priorityNormal,
			[]string{"🟡 Warning · Certificate ends soon\n", "example.org (14 days left): The DNS provider refused the credentials"}},
		{utils.Notification{Title: titleCertUntrusted, Vars: "a.b.example.org, app.example.org", Level: "warning"}, CategorySystem, priorityNormal,
			[]string{"🟡 Warning · Untrusted certificate on a.b.example.org, app.exampl…\n", "a.b.example.org, app.example.org"}},
		{utils.Notification{Title: titleSnapshotsFailed, Vars: "/storage/photos: destination not mounted"}, CategoryStorage, priorityNormal, []string{"🟠 Error · Backup had problems: /storage/photos: destinatio…\n", "/storage/photos: destination not mounted"}},
		{utils.Notification{Title: titleContainerUpdate, Vars: "jellyfin", Level: "info"}, CategoryApps, priorityLow, []string{"🔵 Info · jellyfin updated to a new image\n"}},
		{utils.Notification{Title: titleAlertTriggered, Vars: "Disk Full Notification (storage)", Level: "warn"}, CategoryStorage, priorityNormal, []string{"🟡 Warning · Disk storage nearly full\n"}},
		{utils.Notification{Title: titleAlertTriggered, Vars: "Anti Crypto-Miner", Level: "warn"}, CategoryApps, priorityNormal, []string{"🟡 Warning · Alert Anti Crypto-Miner triggered\n"}},
		{utils.Notification{Title: titleAlertTriggered, Vars: "Disk Full Notification", Level: "warn"}, CategoryStorage, priorityNormal, []string{"🟡 Warning · Disk / nearly full\n"}},
		{utils.Notification{Title: titleAlertTriggered, Vars: "CPU", Level: "warn"}, CategorySystem, priorityNormal, []string{"🟡 Warning · Alert CPU triggered\n"}},
		{utils.Notification{Title: titleAlertTriggered, Vars: "An alert that was deleted", Level: "info"}, CategorySystem, priorityLow, []string{"🔵 Info · Alert An alert that was deleted triggered\n"}},
		{utils.Notification{Title: "header.notification.title.somethingNew", Message: "header.notification.message.somethingNew", Vars: "the variable part", Level: "error"}, CategoryOther, priorityNormal,
			[]string{"🟠 Error · the variable part\n"}},
		{utils.Notification{Title: "A title in words", Message: "and a message in words", Level: "info"}, CategoryOther, priorityLow, []string{"🔵 Info · A title in words\n", "A title in words · and a message in words"}},
	}
	for _, tc := range cases {
		tc.n.Recipient = "admin"
		nt, tell := noticeFromNotification(tc.n, config)
		if !tell {
			t.Errorf("%s / %s is not told", tc.n.Title, tc.n.Message)
			continue
		}
		if nt.category != tc.category || nt.priority() != tc.priority {
			t.Errorf("%s / %s: category %s priority %d, want %s %d", tc.n.Title, tc.n.Vars, nt.category, nt.priority(), tc.category, tc.priority)
		}
		text, entities := textOf(t, st, nt)
		for _, part := range tc.says {
			if !strings.Contains(text, part) {
				t.Errorf("%s: the message does not say %q:\n%s", tc.n.Title, part, text)
			}
		}
		if strings.Contains(text, "header.notification") {
			t.Errorf("a translation key was sent: %s", text)
		}
		// what came with the notification is covered; our own words are not
		rest := uncovered(t, text, entities)
		for _, value := range []string{tc.n.Vars, "permission denied", "exit status"} {
			if value != "" && strings.Contains(rest, value) {
				t.Errorf("%q is outside a code entity in: %s", value, text)
			}
		}
		// the label is what a summary prints as its own words: it holds nothing from the notification
		if tc.n.Vars != "" && strings.Contains(nt.label(), tc.n.Vars) {
			t.Errorf("the label %q holds the variable part", nt.label())
		}
	}
}

func TestOnlyNotificationsForTheAdministratorsAreForwarded(t *testing.T) {
	config := testConfig()
	for recipient, forwarded := range map[string]bool{"admin": true, "all": true, "user": false, "alice": false, "": false} {
		_, tell := noticeFromNotification(utils.Notification{Recipient: recipient, Title: titleServerError, Message: "x"}, config)
		if tell != forwarded {
			t.Errorf("recipient %q: forwarded %v, want %v", recipient, tell, forwarded)
		}
	}
	// the notifier's own "Telegram is failing" must not come back to it
	if _, tell := noticeFromNotification(utils.Notification{Recipient: "admin", Title: TitleDeliveryFailed, Message: MessageDeliveryFailed, Vars: "why"}, config); tell {
		t.Error("the notifier forwards its own failure notice")
	}
}

func TestSecretsAreCutOutOfForwardedText(t *testing.T) {
	config := testConfig()
	config.EmailConfig.Password = "smtp-password-123"
	config.Database.PostgresPassword = "postgres-password-456"
	config.ServerToken = "server-token-789"
	config.HTTPConfig.DNSChallengeConfig = map[string]string{"CF_API_TOKEN": "dns-credential-abc"}
	config.HTTPConfig.DNSZones = []utils.DNSZoneConfig{{Zone: "example.org", DNSChallengeConfig: map[string]string{"KEY": "zone-credential-def"}}}
	config.Backup.Backups = map[string]utils.SingleBackupConfig{"b": {Password: "backup-password-ghi"}}
	config.OpenIDClients = []utils.OpenIDClient{{ID: "c", Secret: "openid-secret-jkl"}}
	RememberSecrets(config)
	defer RememberSecrets(utils.Config{})

	secrets := []string{testToken, "smtp-password-123", "postgres-password-456", "server-token-789", "dns-credential-abc", "zone-credential-def", "backup-password-ghi", "openid-secret-jkl",
		"555555555:CCsomeOtherBotsTokenInAnErrorText0000"}
	message := "Database Retention : connect failed for " + strings.Join(secrets, " and ")

	for _, n := range []utils.Notification{
		{Recipient: "admin", Title: titleServerError, Message: message},
		{Recipient: "admin", Title: titleUPnPFailed, Vars: message},
		{Recipient: "admin", Title: "Words", Message: message, Vars: message},
	} {
		nt, _ := noticeFromNotification(n, config)
		text, _ := textOf(t, settingsOf(config), nt)
		for _, secret := range secrets {
			mustNotContain(t, "the message", text, secret)
		}
		if !strings.Contains(text, "[hidden]") {
			t.Errorf("nothing was cut out of: %s", text)
		}
	}

	// short values are left alone: cutting "ab" out of every text would shred it
	config.EmailConfig.Password = "ab"
	if got := hideSecrets("a table about cabbage", config); got != "a table about cabbage" {
		t.Errorf("a short value was cut out: %q", got)
	}
}

func TestSituationsInWords(t *testing.T) {
	st := testSettings()
	nt, tell := noticeFromSituation(utils.NoticeUpdateAvailable, map[string]string{"version": "0.24.0-fork.9", "current": "0.24.0-fork.8", "at": "02:00"})
	text, _ := textOf(t, st, nt)
	if !tell || nt.category != CategorySystem || nt.priority() != priorityLow ||
		!strings.HasPrefix(text, "🔵 Info · Server update 0.24.0-fork.9 available\n\nVersion 0.24.0-fork.9 is available; this server runs 0.24.0-fork.8. It is installed automatically at 02:00.\n") {
		t.Errorf("update available: %s", text)
	}
	nt, _ = noticeFromSituation(utils.NoticeUpdateAvailable, map[string]string{"version": "9", "current": "8"})
	if text, _ = textOf(t, st, nt); strings.Contains(text, "automatically at") {
		t.Errorf("update available, not automatic: %s", text)
	}

	nt, tell = noticeFromSituation(utils.NoticeUpdateFailed, map[string]string{"version": "9", "reason": "the download was cut"})
	if text, _ = textOf(t, st, nt); !tell || nt.priority() != priorityNormal || !strings.HasPrefix(text, "🟠 Error · Server update 9 not installed\n") || !strings.Contains(text, "\nthe download was cut\n") {
		t.Errorf("update failed: %s", text)
	}

	nt, tell = noticeFromSituation(utils.NoticeContainerDying, map[string]string{"name": "jellyfin", "exits": "3", "code": "137"})
	if text, _ = textOf(t, st, nt); !tell || nt.category != CategoryApps || !strings.HasPrefix(text, "🟠 Error · jellyfin keeps stopping (exit 137)\n") {
		t.Errorf("container: %s", text)
	}

	if _, tell := noticeFromSituation("made.up", nil); tell {
		t.Error("an unknown situation is told")
	}
}

// The public address (plan 47, E4, reports it; plan 48 gives it words).
func TestThePublicAddressChange(t *testing.T) {
	st := testSettings()
	for _, tc := range []struct {
		values map[string]string
		start  string
		says   string
	}{
		{map[string]string{"old": "203.0.113.7", "new": "203.0.113.42", "records": "updated"}, "🔵 Info · Public address changed to 203.0.113.42\n", "The DNS records were updated."},
		{map[string]string{"old": "203.0.113.7", "new": "203.0.113.42", "records": "not_updated"}, "🟠 Error · Public address changed to 203.0.113.42\n", "could not be updated"},
		{map[string]string{"old": "203.0.113.7", "new": "100.64.1.2", "records": "updated", "shared": "yes"}, "🟠 Error · Public address changed to 100.64.1.2\n", "CGNAT"},
		{map[string]string{"old": "203.0.113.7", "new": "203.0.113.42"}, "🟡 Warning · Public address changed to 203.0.113.42\n", "from 203.0.113.7 to 203.0.113.42"},
	} {
		nt, tell := noticeFromSituation(utils.NoticePublicAddressChanged, tc.values)
		text, _ := textOf(t, st, nt)
		if !tell || !strings.HasPrefix(text, tc.start) || !strings.Contains(text, tc.says) {
			t.Errorf("%v:\n%s", tc.values, text)
		}
	}
}
