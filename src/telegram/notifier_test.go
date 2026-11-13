package telegram

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// harness is a notifier with a configuration the test can change, the
// lines it logged and the notifications it raised.
type harness struct {
	*notifier
	mu     sync.Mutex
	config utils.Config
	clock  time.Time // zero: the real time
	logged []string
	raised []string
}

func (h *harness) setConfig(change func(*utils.Config)) {
	h.mu.Lock()
	change(&h.config)
	h.mu.Unlock()
	h.signal()
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	h.clock = h.clock.Add(d)
	h.mu.Unlock()
}

func (h *harness) raisedNow() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.raised...)
}

func (h *harness) loggedNow() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.logged...)
}

// newHarness makes a notifier that sends to the fake. With frozen set, time
// only moves when the test says so and nothing is sent: the queues can be
// looked at. Without, it runs for real, with waits of milliseconds.
func newHarness(t *testing.T, fake *fakeTelegram, frozen bool, tune ...func(h *harness)) *harness {
	t.Helper()
	h := &harness{config: testConfig()}
	var client *apiClient
	if fake != nil {
		client = fake.client()
	}
	h.notifier = newNotifier(client, func() settings {
		h.mu.Lock()
		defer h.mu.Unlock()
		return settingsOf(h.config)
	})
	h.raise = func(reason string) {
		h.mu.Lock()
		h.raised = append(h.raised, reason)
		h.mu.Unlock()
	}
	h.logf = func(line string) {
		h.mu.Lock()
		h.logged = append(h.logged, line)
		h.mu.Unlock()
	}
	if frozen {
		h.clock = time.Date(2026, 11, 13, 12, 0, 0, 0, time.Local)
		h.now = func() time.Time {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.clock
		}
		for _, change := range tune {
			change(h)
		}
		return h
	}
	h.limits.pace = 5 * time.Millisecond
	h.limits.backoff = []time.Duration{20 * time.Millisecond, 40 * time.Millisecond}
	h.limits.digestWindow = 400 * time.Millisecond
	h.limits.digestIdle = 100 * time.Millisecond
	h.limits.digestGap = 50 * time.Millisecond
	h.limits.normalAge = 300 * time.Millisecond
	h.limits.criticalAge = 2 * time.Second
	h.limits.settingsPause = 500 * time.Millisecond
	// whatever a test changes is changed before the goroutine reads it
	for _, change := range tune {
		change(h)
	}
	h.start()
	t.Cleanup(h.close)
	return h
}

// waitUntil polls the status until it is as wanted.
func (h *harness) waitUntil(t *testing.T, what string, wanted func(Status) bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if wanted(h.Status()) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("never happened: %s; status %+v", what, h.Status())
}

func plainNotice(key string, p priority) notice {
	return notice{category: CategorySecurity, key: key, label: "Label of " + key, priority: p, title: "Title of " + key + "."}
}

func (h *harness) queued() (normal int, critical int, held int) {
	h.notifier.mu.Lock()
	defer h.notifier.mu.Unlock()
	return len(h.notifier.normal), len(h.notifier.critical), h.heldLocked()
}

func TestABudgetPerKind(t *testing.T) {
	h := newHarness(t, nil, true)
	h.setConfig(func(c *utils.Config) { c.Telegram.MaxPerMinute = 15 })

	for i := 0; i < 10; i++ {
		h.Notify(plainNotice("login", priorityNormal))
	}
	if normal, _, held := h.queued(); normal != 3 || held != 7 {
		t.Fatalf("10 of one kind at once: %d queued, %d held, want 3 and 7", normal, held)
	}

	// another kind has a budget of its own
	h.Notify(plainNotice("probe", priorityNormal))
	if normal, _, _ := h.queued(); normal != 4 {
		t.Errorf("another kind was held back by the first: %d queued", normal)
	}

	// the budget comes back: one per five minutes
	h.advance(4 * time.Minute)
	h.Notify(plainNotice("login", priorityNormal))
	if normal, _, _ := h.queued(); normal != 4 {
		t.Errorf("a message of the exhausted kind passed after 4 minutes: %d queued", normal)
	}
	h.advance(61 * time.Second)
	h.Notify(plainNotice("login", priorityNormal))
	h.Notify(plainNotice("login", priorityNormal))
	if normal, _, _ := h.queued(); normal != 5 {
		t.Errorf("after 5 minutes: %d queued, want one more (5)", normal)
	}
}

func TestACapPerMinute(t *testing.T) {
	h := newHarness(t, nil, true) // the default cap: 6 a minute
	for i := 0; i < 10; i++ {
		h.Notify(plainNotice(fmt.Sprintf("kind-%d", i), priorityNormal))
	}
	if normal, _, held := h.queued(); normal != 6 || held != 4 {
		t.Fatalf("10 different kinds at once: %d queued, %d held, want 6 and 4", normal, held)
	}
	h.advance(61 * time.Second)
	h.Notify(plainNotice("kind-later", priorityLow))
	if normal, _, _ := h.queued(); normal != 7 {
		t.Errorf("a minute later: %d queued, want 7", normal)
	}

	h.setConfig(func(c *utils.Config) { c.Telegram.MaxPerMinute = 1 })
	h.advance(61 * time.Second)
	h.Notify(plainNotice("kind-a", priorityNormal))
	h.Notify(plainNotice("kind-b", priorityNormal))
	if normal, _, _ := h.queued(); normal != 8 {
		t.Errorf("with a cap of one a minute: %d queued, want 8", normal)
	}
}

// A flood of what a stranger can cause must not keep a critical message
// from being sent: neither through the cap, nor the queue, nor the budget
// of another critical kind.
func TestCriticalMessagesKeepTheirOwnBudget(t *testing.T) {
	h := newHarness(t, nil, true)
	for i := 0; i < 5000; i++ {
		h.Notify(plainNotice(fmt.Sprintf("noise-%d", i%700), priorityNormal))
	}
	normal, critical, _ := h.queued()
	if normal > h.limits.normalQueue || critical != 0 {
		t.Fatalf("after the flood: %d normal, %d critical", normal, critical)
	}

	h.Notify(plainNotice("host-terminal", priorityCritical))
	if _, critical, _ := h.queued(); critical != 1 {
		t.Fatalf("the critical message was not queued after a flood: %d", critical)
	}

	// a critical kind that floods (bans from many addresses) uses up its own budget only
	for i := 0; i < 500; i++ {
		h.Notify(plainNotice("ban", priorityCritical))
	}
	if _, critical, _ := h.queued(); critical != 3 {
		t.Fatalf("500 bans: %d critical messages queued, want 3 (the terminal and 2 bans)", critical)
	}
	h.Notify(plainNotice("role-changed", priorityCritical))
	if _, critical, _ := h.queued(); critical != 4 {
		t.Errorf("another critical kind was held back by the bans: %d queued", critical)
	}

	// the same key as a normal message does not share the critical budget
	h.Notify(plainNotice("ban", priorityNormal))
	if _, critical, _ := h.queued(); critical != 4 {
		t.Errorf("a normal message went to the critical queue")
	}

	// the budget of a critical kind comes back faster: one per two minutes
	h.advance(2*time.Minute + time.Second)
	h.Notify(plainNotice("ban", priorityCritical))
	if _, critical, _ := h.queued(); critical != 5 {
		t.Errorf("two minutes later a ban is still held back: %d queued", critical)
	}
}

func TestQueuesAndBudgetsAreBounded(t *testing.T) {
	h := newHarness(t, nil, true, func(h *harness) {
		h.limits.normalBurst, h.limits.criticalBurst = 1000, 1000
	})
	h.setConfig(func(c *utils.Config) { c.Telegram.MaxPerMinute = 15 })

	for i := 0; i < 300; i++ {
		h.Notify(plainNotice("critical", priorityCritical))
	}
	if _, critical, held := h.queued(); critical != h.limits.criticalQueue || held != 300-h.limits.criticalQueue {
		t.Errorf("300 critical: %d queued, %d held; the queue holds %d", critical, held, h.limits.criticalQueue)
	}

	// the cap is 15 a minute at most, so the normal queue fills over minutes
	for minute := 0; minute < 10; minute++ {
		for i := 0; i < 40; i++ {
			h.Notify(plainNotice("normal", priorityNormal))
		}
		h.advance(61 * time.Second)
	}
	if normal, _, _ := h.queued(); normal != h.limits.normalQueue {
		t.Errorf("%d normal messages queued, the queue holds %d", normal, h.limits.normalQueue)
	}

	// keys without end (they never come from input, but if they did)
	for i := 0; i < 20000; i++ {
		h.Notify(plainNotice(fmt.Sprintf("key-%d", i), priorityNormal))
	}
	h.notifier.mu.Lock()
	buckets, labels := len(h.notifier.buckets)+len(h.notifier.criticalBuckets), len(h.notifier.digest)
	h.notifier.mu.Unlock()
	if buckets > h.limits.maxKeys+2 {
		t.Errorf("%d budgets are kept, the bound is %d", buckets, h.limits.maxKeys)
	}
	if labels > h.limits.maxLabels {
		t.Errorf("a summary would name %d kinds, the bound is %d", labels, h.limits.maxLabels)
	}
	// nothing was lost without a trace: what is not queued is counted
	normal, critical, held := h.queued()
	if total := normal + critical + held; total != 300+400+20000 {
		t.Errorf("%d notices are queued or counted, %d were given", total, 300+400+20000)
	}
}

func TestNothingIsKeptWhenSwitchedOffOrTheCategoryIsOff(t *testing.T) {
	h := newHarness(t, nil, true)
	h.setConfig(func(c *utils.Config) { c.Telegram.Categories = map[string]bool{"security": false} })
	h.Notify(plainNotice("a", priorityCritical))
	if normal, critical, held := h.queued(); normal+critical+held != 0 {
		t.Errorf("a notice of a category that is off was kept")
	}

	h.setConfig(func(c *utils.Config) { c.Telegram.Categories = nil; c.Telegram.Enabled = false })
	h.Notify(plainNotice("a", priorityCritical))
	if normal, critical, held := h.queued(); normal+critical+held != 0 {
		t.Errorf("a notice was kept while the notifications are off")
	}

	h.setConfig(func(c *utils.Config) { c.Telegram.Enabled = true; c.Telegram.BotToken = "" })
	h.Notify(plainNotice("a", priorityCritical))
	if normal, critical, held := h.queued(); normal+critical+held != 0 {
		t.Errorf("a notice was kept without a token")
	}
}

func TestTheSummarySaysWhatWasNotSent(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.Local)
	text, entities := composeDigest(testSettings(), map[string]int{"Failed login": 12, "Probe for a vulnerable path": 5, otherLabel: 3}, now.Add(-5*time.Minute), now, priorityNormal)
	want := "Cosmos · home.example · Summary\n" +
		"20 more events in the last 5 minutes, not sent one by one:\n" +
		"12 × Failed login\n" +
		"5 × Probe for a vulnerable path\n" +
		"3 × other events\n" +
		"The details are in the panel.\n" +
		now.Format(stampLayout)
	if text != want {
		t.Errorf("summary:\n%s\nwant:\n%s", text, want)
	}
	if parts := covered(t, text, entities); len(parts) != 1 || parts[0] != "home.example" {
		t.Errorf("entities cover %q", parts)
	}

	text, _ = composeDigest(testSettings(), map[string]int{"Temporary ban": 1}, now.Add(-20*time.Second), now, priorityCritical)
	if !strings.Contains(text, "Summary with alerts") || !strings.Contains(text, "1 more event in the last minute") {
		t.Errorf("summary of one critical event: %s", text)
	}
	if got := spanWords(3 * time.Hour); got != "in the last 3 hours" {
		t.Errorf("three hours: %q", got)
	}
}

func TestMessagesArriveCriticalFirst(t *testing.T) {
	fake := newFakeTelegram(t)
	fake.always(fakeAnswer{status: 200, body: answerOK.body, delay: 30 * time.Millisecond})
	h := newHarness(t, fake, false)

	h.Notify(plainNotice("first", priorityNormal))
	fake.waitFor(t, 1) // the worker is busy with this one
	h.Notify(plainNotice("second", priorityNormal))
	h.Notify(plainNotice("third", priorityLow))
	h.Notify(plainNotice("urgent", priorityCritical))

	got := fake.waitFor(t, 4)
	order := []string{}
	for _, req := range got {
		text, _ := req.body["text"].(string)
		order = append(order, strings.Split(text, "\n")[1])
	}
	want := []string{"Title of first.", "Title of urgent.", "Title of second.", "Title of third."}
	if strings.Join(order, "|") != strings.Join(want, "|") {
		t.Errorf("order %q, want %q", order, want)
	}
	if !strings.HasPrefix(fake.texts()[1], "Cosmos · home.example · Security alert\n") {
		t.Errorf("a critical message does not say so: %q", fake.texts()[1])
	}
	// low priority arrives without sound, the others with
	if got[3].body["disable_notification"] != true || got[0].body["disable_notification"] != nil || got[1].body["disable_notification"] != nil {
		t.Errorf("sound: low %v, normal %v, critical %v", got[3].body["disable_notification"], got[0].body["disable_notification"], got[1].body["disable_notification"])
	}

	h.waitUntil(t, "four messages count as sent", func(status Status) bool { return status.Sent == 4 })
	status := h.Status()
	if status.LastSent == nil || status.LastError != "" || !status.Ready || status.Waiting != 0 {
		t.Errorf("status %+v", status)
	}
}

func TestNotifyNeverWaitsForTelegram(t *testing.T) {
	fake := newFakeTelegram(t)
	fake.always(fakeAnswer{status: 200, body: answerOK.body, delay: 3 * time.Second})
	h := newHarness(t, fake, false)

	started := time.Now()
	for i := 0; i < 5000; i++ {
		h.Notify(plainNotice(fmt.Sprintf("kind-%d", i%50), priority(i%3)))
	}
	if took := time.Since(started); took > time.Second {
		t.Errorf("5000 notices took %v while Telegram was slow", took)
	}
}

func TestRequestsArePaced(t *testing.T) {
	fake := newFakeTelegram(t)
	h := newHarness(t, fake, false, func(h *harness) {
		h.limits.pace = 150 * time.Millisecond
	})

	for i := 0; i < 3; i++ {
		h.Notify(plainNotice(fmt.Sprintf("kind-%d", i), priorityNormal))
	}
	got := fake.waitFor(t, 3)
	for i := 1; i < len(got); i++ {
		if gap := got[i].at.Sub(got[i-1].at); gap < 140*time.Millisecond {
			t.Errorf("requests %d and %d are %v apart, the pace is 150ms", i-1, i, gap)
		}
	}
}

func TestFloodControlIsHonoured(t *testing.T) {
	fake := newFakeTelegram(t)
	fake.answerWith(func(n int, _ fakeRequest) fakeAnswer {
		if n == 0 {
			return fakeAnswer{status: 429, body: `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 1","parameters":{"retry_after":1}}`}
		}
		return answerOK
	})
	h := newHarness(t, fake, false, func(h *harness) {
		h.limits.normalAge = 10 * time.Second
	})

	h.Notify(plainNotice("a", priorityNormal))
	h.Notify(plainNotice("b", priorityNormal))
	got := fake.waitFor(t, 3)

	if gap := got[1].at.Sub(got[0].at); gap < 950*time.Millisecond {
		t.Errorf("tried again %v after Telegram asked for a second", gap)
	}
	// the same message again, then the next: nothing lost, nothing sent twice
	texts := fake.texts()
	if texts[0] != texts[1] || !strings.Contains(texts[2], "Title of b.") {
		t.Errorf("after the wait: %q", texts)
	}
	if raised := h.raisedNow(); len(raised) != 0 {
		t.Errorf("a wait asked for by Telegram was reported as a failure: %q", raised)
	}
}

func TestTelegramsOwnErrorsAreTriedAgain(t *testing.T) {
	fake := newFakeTelegram(t)
	fake.answerWith(func(n int, _ fakeRequest) fakeAnswer {
		if n < 2 {
			return refusal(502, "Bad Gateway")
		}
		return answerOK
	})
	h := newHarness(t, fake, false, func(h *harness) {
		h.limits.normalAge = 5 * time.Second
	})

	h.Notify(plainNotice("a", priorityNormal))
	got := fake.waitFor(t, 3)
	if first, second := got[1].at.Sub(got[0].at), got[2].at.Sub(got[1].at); first < 15*time.Millisecond || second < 35*time.Millisecond {
		t.Errorf("waits of %v and %v, want at least 20ms and 40ms", first, second)
	}
	h.waitUntil(t, "the message counts as sent", func(status Status) bool { return status.Sent == 1 })
	if status := h.Status(); status.Sent != 1 || status.LastError != "" {
		t.Errorf("status after the retries: %+v", status)
	}
	logged := strings.Join(h.loggedNow(), "\n")
	if !strings.Contains(logged, "tried again") || !strings.Contains(logged, "delivered again") {
		t.Errorf("the log does not tell the failure and the recovery: %q", logged)
	}
}

// When Telegram cannot be reached for longer than a message is worth,
// the message is counted and told in a summary once it works again.
func TestWhatCouldNotBeDeliveredIsToldLater(t *testing.T) {
	fake := newFakeTelegram(t)
	fake.always(refusal(500, "Internal Server Error"))
	h := newHarness(t, fake, false)

	h.Notify(plainNotice("a", priorityNormal))
	h.Notify(plainNotice("b", priorityNormal))

	h.waitUntil(t, "both messages given up", func(status Status) bool { return status.Held == 2 && status.Waiting == 0 })
	raised := h.raisedNow()
	if len(raised) != 1 || !strings.Contains(raised[0], "Telegram has a problem") {
		t.Fatalf("the administrators were told %q, want once that Telegram has a problem", raised)
	}
	if status := h.Status(); status.LastError == "" || status.LastErrorReason != ReasonTelegramError || status.Sent != 0 {
		t.Errorf("status while Telegram is down: %+v", status)
	}

	before := len(fake.received())
	fake.always(answerOK)
	deadline := time.Now().Add(5 * time.Second)
	summary := ""
	for summary == "" && time.Now().Before(deadline) {
		for _, text := range fake.texts()[before:] {
			if strings.Contains(text, "not sent one by one") {
				summary = text
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(summary, "2 more events") || !strings.Contains(summary, "1 × Label of a") || !strings.Contains(summary, "1 × Label of b") {
		t.Errorf("the summary after the outage: %q", summary)
	}
	// told once, however long it lasted
	if raised := h.raisedNow(); len(raised) != 1 {
		t.Errorf("the administrators were told %d times", len(raised))
	}
}

func TestARefusedTokenPausesSendingAndIsToldOnce(t *testing.T) {
	fake := newFakeTelegram(t)
	fake.always(refusal(401, "Unauthorized"))
	h := newHarness(t, fake, false, func(h *harness) {
		h.limits.settingsPause = time.Hour
	})

	h.Notify(plainNotice("a", priorityCritical))
	fake.waitFor(t, 1)
	h.waitUntil(t, "sending paused", func(status Status) bool { return status.PausedUntil != nil })

	for i := 0; i < 50; i++ {
		h.Notify(plainNotice(fmt.Sprintf("kind-%d", i%5), priorityNormal))
	}
	time.Sleep(300 * time.Millisecond)

	if got := len(fake.received()); got != 1 {
		t.Errorf("%d requests with a token Telegram refused, want 1", got)
	}
	status := h.Status()
	if status.PausedUntil == nil || status.LastErrorReason != ReasonWrongToken || status.Held != 51 {
		t.Errorf("status %+v, want paused with 51 held", status)
	}
	raised := h.raisedNow()
	if len(raised) != 1 || !strings.Contains(raised[0], "BotFather") {
		t.Errorf("the administrators were told %q", raised)
	}
	for _, text := range append(raised, h.loggedNow()...) {
		mustNotContain(t, "what was reported", text, testToken)
	}
	mustNotContain(t, "the status", fmt.Sprintf("%+v", status)+status.LastError, testToken)

	// a new token lifts the pause at once; what was held comes as one summary
	fake.always(answerOK)
	h.setConfig(func(c *utils.Config) { c.Telegram.BotToken = "987654321:BBotherOtherOtherOtherOtherOtherOther" })
	deadline := time.Now().Add(5 * time.Second)
	summary := ""
	for summary == "" && time.Now().Before(deadline) {
		for _, req := range fake.received()[1:] {
			if text, _ := req.body["text"].(string); strings.Contains(text, "not sent one by one") {
				summary = text
				if !strings.HasPrefix(req.path, "/bot987654321:") {
					t.Errorf("sent with the old token after the change: %s", req.path)
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(summary, "51 more events") || !strings.Contains(summary, "Summary with alerts") {
		t.Errorf("the summary after the pause: %q", summary)
	}
}

func TestThePauseEndsByItself(t *testing.T) {
	fake := newFakeTelegram(t)
	fake.answerWith(func(n int, _ fakeRequest) fakeAnswer {
		if n == 0 {
			return refusal(400, "Bad Request: chat not found")
		}
		return answerOK
	})
	h := newHarness(t, fake, false, func(h *harness) {
		h.limits.settingsPause = 300 * time.Millisecond
	})

	h.Notify(plainNotice("a", priorityNormal))
	got := fake.waitFor(t, 2)
	if gap := got[1].at.Sub(got[0].at); gap < 280*time.Millisecond {
		t.Errorf("the next request came %v after a refusal, the pause is 300ms", gap)
	}
	if text := fake.texts()[1]; !strings.Contains(text, "1 more event") {
		t.Errorf("after the pause: %q", text)
	}
}

func TestAFloodBecomesAFewMessagesAndASummary(t *testing.T) {
	fake := newFakeTelegram(t)
	h := newHarness(t, fake, false)

	for i := 0; i < 12; i++ {
		h.Notify(notice{category: CategorySecurity, key: "sec:auth.login.failed", label: "Failed login", priority: priorityNormal, title: "Failed login."})
	}
	for i := 0; i < 5; i++ {
		h.Notify(notice{category: CategorySecurity, key: "sec:scan.probe", label: "Probe for a vulnerable path", priority: priorityNormal, title: "Probe."})
	}
	// 3 failed logins and 3 probes fit the budgets and the cap of 6; 11 do not
	got := fake.waitFor(t, 7)
	time.Sleep(200 * time.Millisecond)
	if all := fake.received(); len(all) != 7 {
		t.Fatalf("%d messages for 17 events, want 6 and a summary: %q", len(all), fake.texts())
	}
	summary, _ := got[6].body["text"].(string)
	for _, part := range []string{"11 more events", "9 × Failed login", "2 × Probe for a vulnerable path"} {
		if !strings.Contains(summary, part) {
			t.Errorf("the summary does not say %q:\n%s", part, summary)
		}
	}
	if status := h.Status(); status.Held != 0 || status.Waiting != 0 {
		t.Errorf("after the summary: %+v", status)
	}
}

func TestQuietHoursDeliverWithoutSound(t *testing.T) {
	fake := newFakeTelegram(t)
	h := newHarness(t, fake, false)
	now := time.Now()
	h.setConfig(func(c *utils.Config) {
		c.Telegram.QuietFrom = now.Add(-time.Hour).Format("15:04")
		c.Telegram.QuietTo = now.Add(time.Hour).Format("15:04")
	})

	h.Notify(plainNotice("normal", priorityNormal))
	h.Notify(plainNotice("critical", priorityCritical))
	got := fake.waitFor(t, 2)
	for _, req := range got {
		text, _ := req.body["text"].(string)
		silent := req.body["disable_notification"] == true
		if critical := strings.Contains(text, "critical"); silent == critical {
			t.Errorf("in the quiet hours, silent=%v for %q", silent, strings.Split(text, "\n")[1])
		}
	}

	h.setConfig(func(c *utils.Config) { c.Telegram.QuietSilenceCritical = true })
	h.Notify(plainNotice("critical-2", priorityCritical))
	got = fake.waitFor(t, 3)
	if got[2].body["disable_notification"] != true {
		t.Error("a critical message rang although the quiet hours include them")
	}

	// outside the quiet hours everything but low priority rings
	h.setConfig(func(c *utils.Config) {
		c.Telegram.QuietFrom = now.Add(2 * time.Hour).Format("15:04")
		c.Telegram.QuietTo = now.Add(3 * time.Hour).Format("15:04")
	})
	h.Notify(plainNotice("normal-2", priorityNormal))
	got = fake.waitFor(t, 4)
	if got[3].body["disable_notification"] != nil {
		t.Error("a message was silent outside the quiet hours")
	}
}

func TestAMessageRefusedForItsFormGoesOutWithoutValues(t *testing.T) {
	fake := newFakeTelegram(t)
	fake.answerWith(func(n int, req fakeRequest) fakeAnswer {
		if req.body["entities"] != nil && strings.Contains(req.raw, "mallory") {
			return refusal(400, "Bad Request: can't parse entities: something")
		}
		return answerOK
	})
	h := newHarness(t, fake, false)

	nt := plainNotice("a", priorityCritical)
	nt.details = func(c *composer) { c.fixed("By ").value("mallory", maxValue) }
	h.Notify(nt)

	got := fake.waitFor(t, 2)
	first, _ := got[0].body["text"].(string)
	second, _ := got[1].body["text"].(string)
	if !strings.Contains(first, "By mallory") {
		t.Errorf("the first attempt: %q", first)
	}
	if strings.Contains(second, "mallory") || !strings.Contains(second, "Title of a.") || !strings.Contains(second, "The details are in the panel.") {
		t.Errorf("the second attempt: %q", second)
	}
	h.waitUntil(t, "the message counts as sent", func(status Status) bool { return status.Sent == 1 })
	time.Sleep(100 * time.Millisecond)
	if len(fake.received()) != 2 || h.Status().Sent != 1 {
		t.Errorf("%d requests, %d sent", len(fake.received()), h.Status().Sent)
	}
}

func TestDetailsCanBeLeftOut(t *testing.T) {
	fake := newFakeTelegram(t)
	h := newHarness(t, fake, false)
	h.setConfig(func(c *utils.Config) { c.Telegram.HideDetails = true; c.Telegram.ServerName = "Cellar" })

	nt := plainNotice("a", priorityNormal)
	nt.details = func(c *composer) {
		c.fixed("By ").value("alice", maxValue).fixed(" from ").value("192.0.2.7", maxValue)
	}
	h.Notify(nt)

	text := fake.waitFor(t, 1)[0].body["text"].(string)
	if strings.Contains(text, "alice") || strings.Contains(text, "192.0.2.7") {
		t.Errorf("a name or an address left the server: %q", text)
	}
	if !strings.HasPrefix(text, "Cosmos · Cellar · Security\nTitle of a.\nThe details are in the panel.\n") {
		t.Errorf("message: %q", text)
	}
}

func TestSwitchingOffDropsWhatWaits(t *testing.T) {
	fake := newFakeTelegram(t)
	fake.always(fakeAnswer{status: 200, body: answerOK.body, delay: 100 * time.Millisecond})
	h := newHarness(t, fake, false)

	for i := 0; i < 5; i++ {
		h.Notify(plainNotice(fmt.Sprintf("kind-%d", i), priorityNormal))
	}
	fake.waitFor(t, 1)
	h.setConfig(func(c *utils.Config) { c.Telegram.Enabled = false })
	time.Sleep(400 * time.Millisecond)
	if got := len(fake.received()); got > 2 {
		t.Errorf("%d messages were sent after the notifications were switched off", got-1)
	}
	if status := h.Status(); status.Enabled || status.Ready || status.Waiting != 0 {
		t.Errorf("status %+v", status)
	}
}
