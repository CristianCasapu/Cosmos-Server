package telegram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/security"
	"github.com/azukaar/cosmos-server/src/utils"
)

// wire makes a notifier that sends to the fake the one the sources reach,
// with the configuration and a state folder of the test's own.
func wire(t *testing.T, fake *fakeTelegram, config utils.Config) *harness {
	t.Helper()
	useConfig(t, config)
	previousFolder := utils.CONFIGFOLDER
	utils.CONFIGFOLDER = t.TempDir() + string(os.PathSeparator)

	h := newHarness(t, fake, false, func(h *harness) { h.notifier.settings = currentSettings })
	previous := std.Swap(h.notifier)
	t.Cleanup(func() {
		std.Store(previous)
		utils.CONFIGFOLDER = previousFolder
	})
	return h
}

func TestTheSourcesReachTheChat(t *testing.T) {
	fake := newFakeTelegram(t)
	wire(t, fake, testConfig())

	// the security record
	event := eventOf(utils.SecHostTerminal)
	event.Actor, event.IP = "admin", "192.0.2.7"
	handleEvent(event)
	handleEvent(eventOf(utils.SecLoginFailed)) // below the threshold: not told

	// a notification for the administrators
	forwardNotification(utils.Notification{Recipient: "admin", Title: titleUPnPFailed, Vars: "no answer"})
	forwardNotification(utils.Notification{Recipient: "alice", Title: titleUPnPFailed, Vars: "for one user"})

	// a situation
	handleSituation(utils.NoticeContainerDying, map[string]string{"name": "jellyfin", "exits": "3", "code": "1"})

	got := fake.waitFor(t, 3)
	time.Sleep(100 * time.Millisecond)
	texts := strings.Join(fake.texts(), "\n---\n")
	if len(fake.received()) != 3 {
		t.Fatalf("%d messages, want 3:\n%s", len(fake.received()), texts)
	}
	for _, part := range []string{"Host terminal opened", "By admin from 192.0.2.7", "Router port forwarding failed", "Reason: no answer", "Container jellyfin stopped 3 times"} {
		if !strings.Contains(texts, part) {
			t.Errorf("no message says %q:\n%s", part, texts)
		}
	}
	// the critical one overtook nothing here, but it is marked
	first, _ := got[0].body["text"].(string)
	if !strings.Contains(first, "Security alert") {
		t.Errorf("the first message: %s", first)
	}
}

func TestSwitchedOffNothingIsSentAndNothingIsRemembered(t *testing.T) {
	fake := newFakeTelegram(t)
	config := testConfig()
	config.Telegram.Enabled = false
	wire(t, fake, config)

	handleEvent(eventOf(utils.SecHostTerminal))
	forwardNotification(utils.Notification{Recipient: "admin", Title: titleServerError, Message: "x"})
	handleSituation(utils.NoticeUpdateAvailable, map[string]string{"version": "9", "current": "8"})
	announceStart("8", time.Now())
	time.Sleep(200 * time.Millisecond)
	if got := len(fake.received()); got != 0 {
		t.Fatalf("%d messages were sent while the notifications are off", got)
	}
	// the version was not marked as announced: it is told once they are switched on
	if state := readState(statePath()); state == nil || state.Announced != "" {
		t.Errorf("state %+v", state)
	}
}

func TestAnUpdateIsAnnouncedOncePerVersion(t *testing.T) {
	fake := newFakeTelegram(t)
	wire(t, fake, testConfig())

	for i := 0; i < 5; i++ {
		handleSituation(utils.NoticeUpdateAvailable, map[string]string{"version": "0.24.0-fork.9", "current": "0.24.0-fork.8", "at": "02:00"})
	}
	fake.waitFor(t, 1)
	time.Sleep(150 * time.Millisecond)
	if got := len(fake.received()); got != 1 {
		t.Fatalf("%d announcements of one version", got)
	}
	handleSituation(utils.NoticeUpdateAvailable, map[string]string{"version": "0.24.0-fork.10", "current": "0.24.0-fork.8"})
	fake.waitFor(t, 2)

	// with the category off the version is not used up
	config := testConfig()
	config.Telegram.Categories = map[string]bool{"system": false}
	utils.LoadBaseMainConfig(config)
	handleSituation(utils.NoticeUpdateAvailable, map[string]string{"version": "0.24.0-fork.11", "current": "0.24.0-fork.8"})
	if state := readState(statePath()); state.Announced != "0.24.0-fork.10" {
		t.Errorf("announced %q with the category off", state.Announced)
	}
}

func TestStartsAndEndsAreToldAcrossRuns(t *testing.T) {
	fake := newFakeTelegram(t)
	wire(t, fake, testConfig())
	now := time.Now()

	announceStart("0.24.0-fork.8", now.Add(-5*time.Hour)) // the first start: nothing
	handleSituation(utils.NoticeExitUpdate, map[string]string{"version": "0.24.0-fork.9"})
	if state := readState(statePath()); state == nil || state.Running || state.Exit != exitUpdate || state.UpdateTo != "0.24.0-fork.9" {
		t.Fatalf("the state before the process ends: %+v", state)
	}
	announceStart("0.24.0-fork.9", now.Add(-4*time.Hour))
	handleSituation(utils.NoticeExitRestart, nil)
	announceStart("0.24.0-fork.9", now.Add(-3*time.Hour))
	announceStart("0.24.0-fork.9", now.Add(-2*time.Hour)) // no word of an end: a crash

	fake.waitFor(t, 3)
	texts := strings.Join(fake.texts(), "\n---\n")
	for _, part := range []string{"From version 0.24.0-fork.8 to 0.24.0-fork.9", "Cosmos restarted, as asked for", "after an unexpected stop"} {
		if !strings.Contains(texts, part) {
			t.Errorf("no message says %q:\n%s", part, texts)
		}
	}
	if _, err := os.Stat(filepath.Join(utils.CONFIGFOLDER, stateFileName)); err != nil {
		t.Errorf("the state file is not in the configuration folder: %v", err)
	}
}

func TestAnUpdateThatFailedIsToldWithoutSecrets(t *testing.T) {
	fake := newFakeTelegram(t)
	config := testConfig()
	config.EmailConfig.Password = "smtp-password-123"
	wire(t, fake, config)

	handleSituation(utils.NoticeUpdateFailed, map[string]string{"version": "9", "reason": "could not write: smtp-password-123 and " + testToken})
	text, _ := fake.waitFor(t, 1)[0].body["text"].(string)
	if !strings.Contains(text, "The server update could not be installed") || !strings.Contains(text, "[hidden]") {
		t.Errorf("message: %s", text)
	}
	mustNotContain(t, "the message", text, "smtp-password-123")
	mustNotContain(t, "the message", text, testToken)
}

func TestTheChatIsToldWhenItLosesTheNotifications(t *testing.T) {
	fake := newFakeTelegram(t)
	useFake(t, fake)
	before := testConfig()

	changes := map[string]func(*utils.Config){
		"were switched off":                 func(c *utils.Config) { c.Telegram.Enabled = false },
		"now use another bot":               func(c *utils.Config) { c.Telegram.BotToken = "987654321:BBotherOtherOtherOtherOtherOtherOther" },
		"now go to another chat":            func(c *utils.Config) { c.Telegram.ChatID = "666" },
		"no longer include security events": func(c *utils.Config) { c.Telegram.Categories = map[string]bool{"security": false} },
	}
	sent := 0
	for what, change := range changes {
		after := testConfig()
		change(&after)
		AnnounceSettingsChange(before, after, "mallory", "203.0.113.66")
		sent++
		req := fake.waitFor(t, sent)[sent-1]
		text, _ := req.body["text"].(string)
		// to the chat and with the bot as they were, not as they are now
		if req.path != "/bot"+testToken+"/sendMessage" || !strings.Contains(req.raw, `"chat_id":`+testChat) {
			t.Errorf("%s: sent to %s %s", what, req.path, req.raw)
		}
		if !strings.Contains(text, "Security alert") || !strings.Contains(text, "The Telegram notifications of this server "+what) || !strings.Contains(text, "Changed by mallory from 203.0.113.66") {
			t.Errorf("%s: %s", what, text)
		}
	}

	// changes that take nothing away say nothing; nor does a chat that received nothing
	quiet := []func(*utils.Config, *utils.Config){
		func(was, is *utils.Config) {},
		func(was, is *utils.Config) { is.Telegram.MaxPerMinute = 3; is.Telegram.ServerName = "x" },
		func(was, is *utils.Config) { is.Telegram.Categories = map[string]bool{"apps": false} },
		func(was, is *utils.Config) { was.Telegram.Enabled = false },
		func(was, is *utils.Config) { was.Telegram.BotToken = ""; is.Telegram.ChatID = "5" },
	}
	for _, change := range quiet {
		was, is := testConfig(), testConfig()
		change(&was, &is)
		AnnounceSettingsChange(was, is, "admin", "192.0.2.7")
	}
	time.Sleep(200 * time.Millisecond)
	if got := len(fake.received()); got != sent {
		t.Errorf("%d messages for changes that take nothing away", got-sent)
	}

	// without details, no name and no address
	was := testConfig()
	was.Telegram.HideDetails = true
	is := testConfig()
	is.Telegram.Enabled = false
	AnnounceSettingsChange(was, is, "mallory", "203.0.113.66")
	text, _ := fake.waitFor(t, sent+1)[sent].body["text"].(string)
	if strings.Contains(text, "mallory") || strings.Contains(text, "203.0.113.66") {
		t.Errorf("details were sent: %s", text)
	}
}

// Subscribing to the record must deliver what the record writes.
func TestTheNotifierHearsTheSecurityRecord(t *testing.T) {
	fake := newFakeTelegram(t)
	wire(t, fake, testConfig())

	unsubscribe := security.Subscribe("telegram-test", handleEvent)
	defer unsubscribe()
	utils.RecordSecurityEvent(utils.SecurityEvent{Kind: utils.SecShieldBanPerm, IP: "203.0.113.9"})

	text, _ := fake.waitFor(t, 1)[0].body["text"].(string)
	if !strings.Contains(text, "Permanent ban") || !strings.Contains(text, "From 203.0.113.9") {
		t.Errorf("message: %s", text)
	}
}
