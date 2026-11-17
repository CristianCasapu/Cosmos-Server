package telegram

// Fork addition (plan 48): the tests of the messages' words, their first
// line, their guard against secrets and hostile values, and how each
// severity is delivered.

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/azukaar/cosmos-server/src/security"
	"github.com/azukaar/cosmos-server/src/utils"
)

// Every kind has its texts, and every value its texts use has a sample:
// adding a kind without them fails here.
func TestEveryKindHasItsTexts(t *testing.T) {
	categories := map[string]bool{}
	for _, name := range Categories() {
		categories[name] = true
	}
	for _, name := range KindNames() {
		kt := texts.Kinds[name]
		if kt.Label == "" || kt.Many == "" || kt.Preview == "" || kt.Todo == "" || kt.Sample == nil {
			t.Errorf("%s: label, many, preview, todo and sample are needed", name)
		}
		if !categories[kt.Category] {
			t.Errorf("%s: unknown category %q", name, kt.Category)
		}
		if kt.Page != "" && !pageOK.MatchString(kt.Page) {
			t.Errorf("%s: the page %q is not a path of the panel", name, kt.Page)
		}
		if kt.What == "" && kt.WhatKey == "" {
			t.Errorf("%s: nothing says what happened", name)
		}
		sentences := []string{kt.Preview, kt.What, kt.WhatAuto, kt.Means, kt.Todo}
		if kt.WhatKey != "" {
			english := utils.NotificationEnglish(kt.WhatKey)
			if english == kt.WhatKey || english == "" {
				t.Errorf("%s: %s has no English text", name, kt.WhatKey)
			}
			sentences = append(sentences, english)
		}
		for _, sentence := range sentences {
			for _, m := range sentenceVar.FindAllStringSubmatch(sentence, -1) {
				if _, has := kt.Sample[m[1]]; !has {
					t.Errorf("%s: the sample has no value for {{%s}}", name, m[1])
				}
			}
		}
	}
	for _, name := range []string{"info", "warn", "error", "critical", "closing", "todo", "button", "hidden", "sample", "seen", "counted", "again", "raw", "request",
		"summaryOne", "summaryMany", "summaryWhat", "summaryTodo", "summaryMeans", "spanMinute", "spanMinutes", "spanHours", "otherEvents"} {
		if word(name) == "" {
			t.Errorf("the word %q is missing", name)
		}
	}
	// every kind of notification Cosmos raises has its texts
	for _, kind := range utils.NotificationKindNames() {
		if kind == "telegram.failed" || kind == "other" {
			continue // never sent through Telegram
		}
		if !knownKind(kind) {
			t.Errorf("the notification kind %s has no texts in messages.en.json", kind)
		}
	}
	// and every event the old messages had a sentence for
	for kind := range securitySentences {
		if !knownKind(kind) {
			t.Errorf("the security event %s has no texts in messages.en.json", kind)
		}
	}
}

// previewSays checks the rule of the first line: the mark and the word of
// the severity, then within previewLimit characters the event and its
// subject, and no markup of any kind.
func previewSays(t *testing.T, name string, m message, kt kindTexts, sev severity, vars map[string]string, closing bool) {
	t.Helper()
	line := PreviewLine(m.text)
	start := severityMarks[sev] + " " + word(sev.String()) + " · "
	if closing {
		start = closingMark + " " + word("closing") + " · "
	}
	if !strings.HasPrefix(line, start) {
		t.Errorf("%s: the first line does not start with %q: %q", name, start, line)
		return
	}
	head := []rune(line)
	if len(head) > previewLimit {
		head = head[:previewLimit]
	}
	shown := string(head)
	lead := kt.Preview
	if loc := sentenceVar.FindStringIndex(lead); loc != nil {
		lead = lead[:loc[0]]
	}
	lead = strings.TrimSpace(lead)
	if utf8.RuneCountInString(lead) > 20 {
		lead = string([]rune(lead)[:20])
	}
	if !strings.Contains(shown, lead) {
		t.Errorf("%s: the event (%q) is not within the first %d characters: %q", name, lead, previewLimit, shown)
	}
	if first := sentenceVar.FindStringSubmatch(kt.Preview); first != nil {
		value := []rune(cleanValue(vars[first[1]], maxPreview))
		if strings.HasPrefix(first[1], "_") {
			value = []rune(cleanValue(vars[first[1]], 20))
		}
		if len(value) > 10 {
			value = value[:10]
		}
		if !strings.Contains(shown, string(value)) {
			t.Errorf("%s: the subject (%q) is not within the first %d characters: %q", name, string(value), previewLimit, shown)
		}
	}
	rest := uncovered(t, line, m.entities)
	if strings.ContainsAny(rest, "<>*_`[]{}#@\\") || strings.Contains(rest, "http") {
		t.Errorf("%s: markup or a link in the first line: %q", name, rest)
	}
	for _, bad := range []string{"Cosmos ·", "server.local", "home.example", "202", ":"} {
		if strings.HasPrefix(strings.TrimPrefix(line, start), bad) {
			t.Errorf("%s: the first line starts with %q", name, bad)
		}
	}
}

func TestTheFirstLineOfEveryKindSaysWhatAndToWhat(t *testing.T) {
	st := testSettings()
	for _, name := range KindNames() {
		nt := sampleNotice(name)
		m := render(st, nt)
		previewSays(t, name, m, nt.texts(), nt.severity, nt.vars, nt.closing)
		if !strings.HasSuffix(m.text, "home.example · "+word("sample")) {
			t.Errorf("%s: the sample does not end with the server and the sample mark: %q", name, m.text)
		}
		if strings.Contains(m.text, "{{") || strings.Contains(m.text, "header.notification") || strings.Contains(m.text, "notif.") {
			t.Errorf("%s: a placeholder or a key was sent: %s", name, m.text)
		}
		if m.button == nil || !strings.HasPrefix(m.button.URL, "http") {
			if nt.texts().Page != "" {
				t.Errorf("%s: no button although the kind has a page", name)
			}
		}
	}
	// a summary follows the rule too
	m := composeDigest(st, map[string]int{"auth.login.failed": 47}, time.Now().Add(-10*time.Minute), time.Now(), sevWarn)
	if line := PreviewLine(m.text); line != "🟡 Warning · 47 more failed sign-ins in the last 10 min" {
		t.Errorf("summary: %q", line)
	}
	// and the security events without texts of their own
	config := testConfig()
	config.Telegram.MinSeverity = "low"
	st = settingsOf(config)
	for _, kind := range security.Kinds() {
		event := eventOf(kind.Kind)
		event.Actor, event.IP, event.Target = "admin", "203.0.113.7", "jellyfin"
		nt, tell := noticeFromEvent(event, st)
		if !tell || nt.kind != "security.other" {
			continue // informational entries are never told; the others have their samples
		}
		previewSays(t, kind.Kind, render(st, nt), nt.texts(), nt.severity, nt.vars, false)
	}
}

// hostile values, as a stranger could cause them: in a user name typed at
// a failed sign-in, a host name, a file name, an error
var hostileValues = []string{
	"x\n\n🔴 Critical · Your server was hacked\nPay at https://evil.example",
	"<b>bold</b> <a href=\"https://evil.example\">click</a> &amp;",
	"[click](https://evil.example) *bold* _it_ `code` ```pre``` ||spoiler||",
	"@admin @everyone #urgent /start tg://user?id=1 t.me/evil +1 555 0100 owner@example.com",
	"\u202egnp.exe\u2066\u200b\ufeff\r\x00\x1b[31m",
	strings.Repeat("https://evil.example/ ", 300),
}

func TestHostileValuesCannotForgeAnyKind(t *testing.T) {
	st := testSettings()
	for _, name := range KindNames() {
		benign := sampleNotice(name)
		for key := range benign.vars {
			benign.vars[key] = "x"
		}
		benign.raw = "x"
		want := strings.Count(render(st, benign).text, "\n")

		for _, hostile := range hostileValues {
			nt := sampleNotice(name)
			for key := range nt.vars {
				if !strings.HasPrefix(key, "_") { // "_": this server's own catalogue texts
					nt.vars[key] = hostile
				}
			}
			nt.raw = hostile
			m := render(st, nt)
			if got := strings.Count(m.text, "\n"); got != want {
				t.Errorf("%s: %d lines with a hostile value, %d without:\n%s", name, got+1, want+1, m.text)
			}
			rest := uncovered(t, m.text, m.entities)
			for _, bad := range []string{"evil", "<b>", "<a", "](", "@admin", "@everyone", "#urgent", "/start", "tg://", "t.me", "owner@", "Your server", "```", "||"} {
				if strings.Contains(rest, bad) {
					t.Errorf("%s: %q is outside the values' entities: %q", name, bad, rest)
				}
			}
			if strings.ContainsAny(m.text, "\u202e\u2066\u200b\ufeff\r\x00\x1b") {
				t.Errorf("%s: a control or direction character was sent", name)
			}
			if strings.Count(m.text, "🔴") > 1 {
				t.Errorf("%s: a value carries a severity mark: %q", name, PreviewLine(m.text))
			}
			if units(m.text) > maxMessageUnits {
				t.Errorf("%s: %d units", name, units(m.text))
			}
			if m.button != nil && (strings.Contains(m.button.URL, "evil") || strings.Contains(m.button.URL, "<")) {
				t.Errorf("%s: the button leads to %q", name, m.button.URL)
			}
		}
	}
}

func TestNoSecretReachesTelegramInAnyKind(t *testing.T) {
	config := testConfig()
	config.EmailConfig.Password = "smtp-password-123"
	config.Database.PostgresPassword = "postgres-password-456"
	config.ServerToken = "server-token-789"
	config.Licence = "licence-key-000111"
	config.HTTPConfig.DNSChallengeConfig = map[string]string{"CF_API_TOKEN": "dns-credential-abc"}
	config.Backup.Backups = map[string]utils.SingleBackupConfig{"b": {Password: "backup-password-ghi"}}
	RememberSecrets(config)
	defer RememberSecrets(utils.Config{})
	st := settingsOf(config)

	laden := "failed: password=SuperSecret123 token: tok-ABCDEFGH1234 " + testToken +
		" postgres://user:dbpass-999@db:5432/x Authorization: Bearer abcdefghijkl0123" +
		" eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.c2lnbmF0dXJlLXZhbHVl" +
		" -----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAAS\n-----END PRIVATE KEY-----" +
		" https://example.org/invite?t=invite-secret-777&x=1 cookie=session-cookie-888 otp: 482913" +
		" smtp-password-123 postgres-password-456 server-token-789 licence-key-000111 dns-credential-abc backup-password-ghi"
	secrets := []string{"SuperSecret123", "tok-ABCDEFGH1234", testToken, "dbpass-999", "abcdefghijkl0123", "eyJhbGciOiJIUzI1NiJ9",
		"MIIEvQIBADANBgkqhkiG9w0BAQEFAAS", "invite-secret-777", "session-cookie-888", "482913",
		"smtp-password-123", "postgres-password-456", "server-token-789", "licence-key-000111", "dns-credential-abc", "backup-password-ghi"}

	for _, name := range KindNames() {
		nt := sampleNotice(name)
		for key := range nt.vars {
			if !strings.HasPrefix(key, "_") { // "_": this server's own catalogue texts
				nt.vars[key] = laden
			}
		}
		nt.raw = laden
		nt.extra = func(c *composer) { c.value(laden, maxFreeText) }
		for _, hide := range []bool{false, true} {
			st.hideDetails = hide
			m := render(st, nt)
			if !hide && !strings.Contains(m.text, hiddenSecret) {
				t.Errorf("%s: the guard of the values cut nothing (the message fell back to no values): %s", name, m.text)
			}
			for _, secret := range secrets {
				mustNotContain(t, name+" (message)", m.text, secret)
				if m.button != nil {
					mustNotContain(t, name+" (button)", m.button.URL, secret)
				}
			}
		}
	}
	st.hideDetails = false

	// the last check: a secret that got into a fixed text sends the
	// message without any value, and without the button
	nt := sampleNotice("auth.login.failed")
	st.panel = "https://home.example"
	m := renderWith(st, nt, true)
	m.text += " smtp-password-123"
	if !leaks(m.text, m.entities, st.secrets) {
		t.Fatal("leaks does not see the password")
	}
	if m := render(st, nt); leaks(m.text, m.entities, st.secrets) {
		t.Error("an ordinary message leaks")
	}
}

func TestALongMessageIsCutAtTheEndOfALine(t *testing.T) {
	c := &composer{}
	for i := 0; c.length < 3*maxMessageUnits; i++ {
		c.fixed("line "+strconv.Itoa(i)+": ").value(strings.Repeat("v", 150), 200).line()
	}
	text, entities := c.result()
	if units(text) > maxMessageUnits || !strings.HasSuffix(text, "\n…") {
		t.Fatalf("%d units, ends %q", units(text), text[len(text)-10:])
	}
	for _, e := range entities {
		if e.Length != 150 {
			t.Errorf("an entity was cut: %+v", e)
		}
	}
	for _, part := range covered(t, text, entities) {
		if part != strings.Repeat("v", 150) {
			t.Errorf("a value was cut: %q", part)
		}
	}
}

func TestNoParseModeIsEverSent(t *testing.T) {
	m := render(testSettings(), sampleNotice("auth.login.failed"))
	raw, _ := json.Marshal(sendRequest{Text: m.text, Entities: m.entities, Markup: &inlineKeyboard{Rows: [][]button{{*m.button}}}})
	if strings.Contains(string(raw), "parse_mode") {
		t.Errorf("a parse mode in %s", raw)
	}
	if m.button == nil || m.button.URL != "https://home.example/cosmos-ui/security" && m.button.URL != "http://home.example/cosmos-ui/security" {
		t.Errorf("button %+v", m.button)
	}
}

func TestTheSettingsOfForkThirtyFourLoadAndBehaveAsBefore(t *testing.T) {
	// what fork.34 saved: no Kinds, no Delivery
	saved := `{"Enabled":true,"BotToken":"` + testToken + `","ChatID":"` + testChat + `","TopicID":0,"ServerName":"","Categories":{"apps":false,"logins":true},"MinSeverity":"medium","HideDetails":false,"QuietFrom":"","QuietTo":"","QuietSilenceCritical":false,"MaxPerMinute":0}`
	var cfg utils.TelegramConfig
	if err := json.Unmarshal([]byte(saved), &cfg); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(&cfg); err != nil {
		t.Fatalf("the old settings are refused: %v", err)
	}
	config := testConfig()
	config.Telegram = cfg
	st := settingsOf(config)
	for i, name := range SeverityNames {
		if st.delivery[i] != DefaultDelivery[name] {
			t.Errorf("%s is delivered %s", name, st.delivery[i])
		}
	}
	// what was sent is sent, what was not is not
	if st.wants(newNotice("container.dying", nil)) {
		t.Error("an app notice although apps are off")
	}
	if !st.wants(newNotice("cert.order.failed", nil)) || !st.wants(newNotice("cert.renewed", nil)) {
		t.Error("a system notice is no longer sent")
	}
	failed := eventOf(utils.SecLoginFailed)
	if _, tell := noticeFromEvent(failed, st); !tell {
		t.Error("a failed sign-in at medium is no longer sent")
	}
	if _, tell := noticeFromEvent(eventOf(utils.SecShieldHostname), st); tell {
		t.Error("a low event is sent at medium")
	}
	// info stays silent, the rest rings, as with the priorities before
	if st.mode(newNotice("cert.renewed", nil)) != DeliverSilent || st.mode(newNotice("cert.order.failed", nil)) != DeliverSound || st.mode(newNotice("auth.mfa.locked", nil)) != DeliverSound {
		t.Error("the default delivery changed")
	}
	// and the saved form round-trips without the new fields
	raw, _ := json.Marshal(cfg)
	if strings.Contains(string(raw), "Kinds") || strings.Contains(string(raw), "Delivery") {
		t.Errorf("empty new fields are written: %s", raw)
	}
}

func TestKindsAndDeliveryAreChecked(t *testing.T) {
	good := utils.TelegramConfig{Kinds: map[string]bool{"auth.login.failed": true, "container.dying": false}, Delivery: map[string]string{"info": "off", "warn": "silent", "critical": "sound"}}
	if err := ValidateConfig(&good); err != nil {
		t.Errorf("refused: %v", err)
	}
	for _, bad := range []utils.TelegramConfig{
		{Kinds: map[string]bool{"made.up": true}},
		{Kinds: map[string]bool{"telegram.test": false}},
		{Delivery: map[string]string{"loud": "sound"}},
		{Delivery: map[string]string{"info": "maybe"}},
	} {
		if err := ValidateConfig(&bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	// a file edited by hand: what is unknown is ignored
	config := testConfig()
	config.Telegram.Kinds = map[string]bool{"made.up": false}
	config.Telegram.Delivery = map[string]string{"info": "maybe"}
	st := settingsOf(config)
	if len(st.kinds) != 0 || st.delivery[sevInfo] != DeliverSilent {
		t.Errorf("unknown values were used: %v %v", st.kinds, st.delivery)
	}
}

func TestASwitchPerKind(t *testing.T) {
	config := testConfig()
	// switched on: sent below the minimum severity
	config.Telegram.Kinds = map[string]bool{"auth.login.failed": true, "auth.mfa.locked": false}
	st := settingsOf(config)
	if _, tell := noticeFromEvent(eventOf(utils.SecLoginFailed), st); !tell {
		t.Error("a kind switched on is not told")
	}
	nt, tell := noticeFromEvent(eventOf(utils.SecMFALocked), st)
	if tell && st.wants(nt) {
		t.Error("a kind switched off is told")
	}
	// a category off does not stop a kind switched on by itself
	config.Telegram.Categories = map[string]bool{"security": false}
	st = settingsOf(config)
	if nt, tell := noticeFromEvent(eventOf(utils.SecLoginFailed), st); !tell || !st.wants(nt) {
		t.Error("a kind switched on is stopped by its category")
	}
}

func TestEachSeverityIsDeliveredAsSet(t *testing.T) {
	fake := newFakeTelegram(t)
	h := newHarness(t, fake, false)
	h.setConfig(func(c *utils.Config) { c.Telegram.MaxPerMinute = 15 })

	send := func(kinds ...string) []fakeRequest {
		before := len(fake.received())
		for _, kind := range kinds {
			nt := sampleNotice(kind)
			nt.sample = false
			h.Notify(nt)
		}
		got := fake.waitFor(t, before+len(kinds))
		return got[before:]
	}
	silentOf := func(req fakeRequest) bool { return req.body["disable_notification"] == true }

	got := send("cert.renewed", "container.leftover", "container.dying", "auth.mfa.locked")
	want := map[string]bool{"🔵": true, "🟡": false, "🟠": false, "🔴": false}
	for _, req := range got {
		text, _ := req.body["text"].(string)
		mark := string([]rune(text)[0])
		if silentOf(req) != want[mark] {
			t.Errorf("%s: silent %v", PreviewLine(text), silentOf(req))
		}
		if req.body["parse_mode"] != nil {
			t.Errorf("a parse mode was sent")
		}
		if req.body["reply_markup"] == nil {
			t.Errorf("%s: no button", PreviewLine(text))
		}
	}

	h.setConfig(func(c *utils.Config) {
		c.Telegram.Delivery = map[string]string{"info": "sound", "warn": "silent", "error": "off"}
	})
	got = send("cert.renewed", "container.leftover", "auth.mfa.locked")
	time.Sleep(100 * time.Millisecond)
	if len(fake.received()) != 4+3 {
		t.Errorf("%d messages: an error was sent although errors are off", len(fake.received()))
	}
	want = map[string]bool{"🔵": false, "🟡": true, "🔴": false}
	for _, req := range got {
		text, _ := req.body["text"].(string)
		if mark := string([]rune(text)[0]); silentOf(req) != want[mark] {
			t.Errorf("%s: silent %v", PreviewLine(text), silentOf(req))
		}
	}
	h.Notify(sampleNotice("container.dying"))
	time.Sleep(100 * time.Millisecond)
	if len(fake.received()) != 7 {
		t.Error("an error was sent although errors are off")
	}
}

// numbered answers like Telegram's: each message gets its id
func numbered(fake *fakeTelegram) {
	fake.answerWith(func(n int, req fakeRequest) fakeAnswer {
		if strings.HasSuffix(req.path, "/sendMessage") {
			return fakeAnswer{status: 200, body: `{"ok":true,"result":{"message_id":` + strconv.Itoa(500+n) + `}}`}
		}
		return fakeAnswer{status: 200, body: `{"ok":true,"result":true}`}
	})
}

func TestTheEndOfSomethingAnswersItsStartAndUnpinsIt(t *testing.T) {
	fake := newFakeTelegram(t)
	numbered(fake)
	h := newHarness(t, fake, false, func(h *harness) { h.threads = newThreadMemory("") })

	down := sampleNotice("reach.down")
	down.sample, down.thread, down.ref = false, "reach:connect", "0123456789abcdef"
	h.Notify(down)
	got := fake.waitFor(t, 2)
	if !strings.HasSuffix(got[0].path, "/sendMessage") || !strings.HasSuffix(got[1].path, "/pinChatMessage") || got[1].body["message_id"] != float64(500) || got[1].body["disable_notification"] != true {
		t.Fatalf("the outage: %s %s %s", got[0].path, got[1].path, got[1].raw)
	}

	// it lasts: the repeat answers it, and is not pinned again
	again := down
	again.repeat, again.count = true, 3
	h.Notify(again)
	got = fake.waitFor(t, 3)
	if reply, _ := got[2].body["reply_parameters"].(map[string]interface{}); reply == nil || reply["message_id"] != float64(500) || reply["allow_sending_without_reply"] != true {
		t.Errorf("the repeat: %s", got[2].raw)
	}

	back := sampleNotice("reach.restored")
	back.sample, back.thread, back.resolves = false, "reach:connect", "reach:connect"
	h.Notify(back)
	got = fake.waitFor(t, 5)
	text, _ := got[3].body["text"].(string)
	reply, _ := got[3].body["reply_parameters"].(map[string]interface{})
	if !strings.HasPrefix(text, "🟢 OK · Reachable from the internet again") || reply == nil || reply["message_id"] != float64(500) || got[3].body["disable_notification"] != true {
		t.Errorf("the end: %s", got[3].raw)
	}
	if !strings.HasSuffix(got[4].path, "/unpinChatMessage") || got[4].body["message_id"] != float64(500) {
		t.Errorf("not unpinned: %s %s", got[4].path, got[4].raw)
	}

	// an end whose start is not known is sent without a reply
	h.Notify(back)
	got = fake.waitFor(t, 6)
	if got[5].body["reply_parameters"] != nil {
		t.Errorf("a reply to nothing: %s", got[5].raw)
	}
}

func TestPinsAreBounded(t *testing.T) {
	m := newThreadMemory("")
	st := testSettings()
	unpinned := []int{}
	for i := 1; i <= 5; i++ {
		unpinned = append(unpinned, m.open(st, "t"+strconv.Itoa(i), i, true)...)
	}
	if len(unpinned) != 2 || unpinned[0] != 1 || unpinned[1] != 2 {
		t.Errorf("unpinned %v, want the two oldest", unpinned)
	}
	for i := 0; i < maxThreads+50; i++ {
		m.open(st, "n"+strconv.Itoa(i), 1000+i, false)
	}
	if len(m.data.Threads) > maxThreads {
		t.Errorf("%d threads kept", len(m.data.Threads))
	}
	// another chat: nothing of the first one is answered
	other := st
	other.chatID = "777"
	if _, ok := m.get(other, "t5"); ok {
		t.Error("a message of another chat is answered")
	}
}

func TestTheThreadsAreKeptAcrossARestart(t *testing.T) {
	path := t.TempDir() + "/" + threadsFileName
	st := testSettings()
	newThreadMemory(path).open(st, "fail2ban.down", 42, false)
	if open, ok := newThreadMemory(path).get(st, "fail2ban.down"); !ok || open.MessageID != 42 {
		t.Errorf("after a restart: %+v %v", open, ok)
	}
}

func TestARefusedButtonIsLeftOut(t *testing.T) {
	fake := newFakeTelegram(t)
	fake.answerWith(func(n int, req fakeRequest) fakeAnswer {
		if req.body["reply_markup"] != nil {
			return refusal(400, "Bad Request: BUTTON_URL_INVALID")
		}
		return answerOK
	})
	h := newHarness(t, fake, false)
	nt := sampleNotice("container.dying")
	nt.sample = false
	h.Notify(nt)
	got := fake.waitFor(t, 2)
	if got[1].body["reply_markup"] != nil || got[1].body["text"] != got[0].body["text"] {
		t.Errorf("the second attempt: %s", got[1].raw)
	}
	h.waitUntil(t, "sent", func(s Status) bool { return s.Sent == 1 })
}

// A full queue makes room by the least severe message, which is counted in
// the summary, never by a more severe one.
func TestAFullQueueDropsTheLeastSevereFirst(t *testing.T) {
	h := newHarness(t, nil, true, func(h *harness) { h.limits.normalQueue = 3 })
	h.setConfig(func(c *utils.Config) { c.Telegram.MaxPerMinute = 15 })
	for _, kind := range []string{"cert.renewed", "container.updated", "app.installed"} {
		h.Notify(sampleNotice(kind)) // three of information
	}
	h.Notify(sampleNotice("container.dying")) // an error
	normal, _, held := h.queued()
	if normal != 3 || held != 1 {
		t.Fatalf("%d queued, %d held", normal, held)
	}
	h.notifier.mu.Lock()
	kinds := []string{}
	for _, it := range h.notifier.normal {
		kinds = append(kinds, it.nt.kind)
	}
	dropped := h.notifier.digest
	h.notifier.mu.Unlock()
	if strings.Join(kinds, ",") != "container.updated,app.installed,container.dying" || dropped["cert.renewed"] != 1 {
		t.Errorf("queued %v, counted %v", kinds, dropped)
	}
	// one more of information finds no room: it is counted, nothing is dropped for it
	h.Notify(sampleNotice("deps.quiet"))
	if normal, _, held := h.queued(); normal != 3 || held != 2 {
		t.Errorf("%d queued, %d held", normal, held)
	}
}
