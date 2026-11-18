package telegram

// Review 4: the reviewer's sequences, each as a test, on the model's
// stand-in (model_test.go): one goroutine, a frozen clock.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// scripted is a model with the threads named, Telegram answering, and
// nothing random.
func scripted(t *testing.T, threads ...*modelThread) *model {
	t.Helper()
	m := newModel(t, 1)
	m.threads = threads
	m.bot.failing = false
	return m
}

func reachThread(name string) *modelThread {
	return &modelThread{name: name, opening: "reach.down", closing: "reach.restored"}
}

func (m *model) open(th *modelThread) {
	nt := m.notice(th, th.opening)
	th.open, th.storeRow, th.lastClosing = true, &nt, nil
	m.n.Notify(nt)
}

func (m *model) repeatOf(th *modelThread) {
	nt := m.notice(th, th.opening)
	nt.repeat, nt.count = true, 2
	m.n.Notify(nt)
}

func (m *model) closeWith(th *modelThread) {
	nt := m.notice(th, th.closing)
	th.open, th.storeRow, th.lastClosing = false, nil, &nt
	m.n.Notify(nt)
}

func (m *model) quietEnd(th *modelThread) {
	th.open, th.storeRow, th.lastClosing = false, nil, nil
}

func (m *model) actions(th *modelThread) []string {
	m.bot.mu.Lock()
	defer m.bot.mu.Unlock()
	out := []string{}
	for _, msg := range m.bot.chat {
		if msg.thread == threadID(th.name) {
			out = append(out, msg.action)
		}
	}
	return out
}

func (m *model) messages() []modelMessage {
	m.bot.mu.Lock()
	defer m.bot.mu.Unlock()
	return append([]modelMessage(nil), m.bot.chat...)
}

// The paired kinds, listed: a kind is a thread if and only if it is here.
var declaredPairs = map[string][]string{
	"reach.down":              {"reach.restored"},
	"docker.down":             {"docker.restored", "docker.unwatched"},
	"fail2ban.down":           {"fail2ban.restored"},
	"upnp.router.unreachable": {"upnp.router.restored"},
	"upnp.forward.failed":     {"upnp.forward.restored"},
	"smart.failing":           {"smart.gone", "smart.restored"},
	"smart.unreadable":        {"smart.readable"},
	"disk.filling":            {"disk.filling.over"},
	"snapshots.late":          {"snapshots.ontime"},
	"watch.stuck":             {"watch.recovered"},
	"error.job":               {"job.restored"},
}

func TestOnlyDeclaredPairsAreThreads(t *testing.T) {
	closings := map[string]bool{}
	for opening, list := range declaredPairs {
		if got := strings.Join(closingsOf(opening), ","); got != strings.Join(list, ",") {
			t.Errorf("%s: closed by %q, the list says %q", opening, got, list)
		}
		for _, c := range list {
			closings[c] = true
		}
	}
	for _, kind := range KindNames() {
		nt := newNotice(kind, nil)
		nt.thread = "t:" + kind
		_, opening := declaredPairs[kind]
		if (pairedThread(nt) != "") != opening {
			t.Errorf("%s: treated as a thread: %v, declared: %v", kind, pairedThread(nt) != "", opening)
		}
		nt.resolves = nt.thread
		if (pairedThread(nt) != "") != closings[kind] {
			t.Errorf("%s with an end: treated as a closing of a thread: %v, declared: %v", kind, pairedThread(nt) != "", closings[kind])
		}
		if texts.Kinds[kind].Pin && !opening {
			t.Errorf("%s pins without a declared end: its pin would stay for ever", kind)
		}
	}
	if len(PairedKinds()) != len(declaredPairs) {
		t.Errorf("paired kinds %v", PairedKinds())
	}
}

// P1: a told opening, a restart, and the first event a repeat: no crash; a
// repeat on a told thread means open, and is answered as a repeat.
func TestT4RepeatAfterRestart(t *testing.T) {
	th := reachThread("reach:connect")
	m := scripted(t, th)
	m.open(th)
	m.settle(5 * time.Minute)
	m.restartKeeping(true)
	m.repeatOf(th)
	m.settle(5 * time.Minute)
	if got := strings.Join(m.actions(th), ","); got != "open,repeat" {
		t.Fatalf("after the restart: %s", got)
	}
	msgs := m.messages()
	if msgs[1].replyTo != msgs[0].id {
		t.Errorf("the repeat answers %d, not the opening %d", msgs[1].replyTo, msgs[0].id)
	}
	// and the end closes it
	m.closeWith(th)
	m.settle(5 * time.Minute)
	if got := strings.Join(m.actions(th), ","); got != "open,repeat,close" {
		t.Errorf("%s", got)
	}
	// the same with the entry pushed out of memory (no restart): a closing
	// on an entry with neither opening nor closing never panics
	m.n.mu.Lock()
	m.n.entries[threadID(th.name)] = &threadEntry{id: threadID(th.name), name: th.name, queued: true}
	m.n.mu.Unlock()
	m.settle(5 * time.Minute)
}

// P2: a kind without a declared end is a message every time: a second
// update.attention days later is delivered, and rings.
func TestT4SecondOccurrenceOfOneOff(t *testing.T) {
	m := scripted(t)
	for i := 0; i < 2; i++ {
		nt := newNotice("update.attention", map[string]string{"summary": "the previous version could not be restored"})
		nt.thread, nt.at = "update.attention", m.clock
		m.n.Notify(nt)
		m.settle(time.Hour)
		m.clock = m.clock.Add(48 * time.Hour)
	}
	msgs := m.messages()
	if len(msgs) != 2 {
		t.Fatalf("%d messages for two occurrences", len(msgs))
	}
	for _, msg := range msgs {
		if msg.silent || msg.thread != "" || !strings.HasPrefix(msg.text, "🔴 Critical") {
			t.Errorf("silent %v, thread %q: %q", msg.silent, msg.thread, PreviewLine(msg.text))
		}
	}
	if len(m.bot.pinned) != 0 {
		t.Error("a message without a declared end is pinned")
	}
}

// P2: nothing unpaired is ever dropped as "told": three occurrences of
// error kinds that never close, each its own message.
func TestNothingUnpairedIsSwallowed(t *testing.T) {
	m := scripted(t)
	for i, kind := range []string{"alert.disk", "alert.disk", "error.internet", "login.newcountry"} {
		nt := newNotice(kind, map[string]string{"name": "disk", "country": "Elsewhere"})
		nt.thread, nt.at = kind, m.clock
		if kind == "login.newcountry" {
			m.config.Telegram.Kinds = map[string]bool{"login.newcountry": true}
		}
		m.n.Notify(nt)
		m.settle(time.Hour)
		m.clock = m.clock.Add(time.Duration(i+1) * 25 * time.Hour)
	}
	if n := len(m.messages()); n != 4 {
		t.Errorf("%d messages for four occurrences", n)
	}
}

// P2: an outage told and pinned, ended quietly (its cause changed), then
// another days later: the chat hears the quiet end, and the later outage.
func TestALaterOutageAfterAQuietEndIsTold(t *testing.T) {
	for _, look := range []bool{true, false} {
		th := reachThread("reach:connect")
		m := scripted(t, th)
		m.open(th)
		m.settle(5 * time.Minute)
		m.quietEnd(th)
		if look {
			m.n.reconcile()
			m.settle(5 * time.Minute)
		}
		m.clock = m.clock.Add(72 * time.Hour)
		m.open(th)
		m.settle(10 * time.Minute)
		want := "open,open"
		if look {
			want = "open,close,open"
		}
		if got := strings.Join(m.actions(th), ","); got != want {
			t.Errorf("look at the level %v: %s, want %s", look, got, want)
		}
		msgs := m.messages()
		last := msgs[len(msgs)-1]
		if !m.bot.pinned[last.id] || len(m.bot.pinned) != 1 {
			t.Errorf("look %v: pinned %v, the open outage is %d", look, m.bot.pinned, last.id)
		}
		if look && !strings.Contains(msgs[1].text, "No longer followed") {
			t.Errorf("the quiet end reads %q", PreviewLine(msgs[1].text))
		}
	}
}

// P3: names of any length and script are told once, and the brake stops
// a second opening within a minute whatever the memory says.
func TestT4LongThreadLoops(t *testing.T) {
	names := []string{
		"update.rejected:" + strings.Repeat("v", 199) + "…",
		"error.job:" + strings.Repeat("夜間", 120),
		"reach:" + strings.Repeat("Сервер", 60),
	}
	threads := []*modelThread{}
	for _, name := range names {
		threads = append(threads, reachThread(name))
	}
	m := scripted(t, threads...)
	for _, th := range threads {
		m.open(th)
	}
	m.settle(10 * time.Minute)
	if n := len(m.messages()); n != len(names) {
		t.Fatalf("%d messages for %d openings", n, len(names))
	}
	// the memory loses an opening just sent (a bug of tomorrow): the brake
	// holds
	th := reachThread("reach:" + strings.Repeat("ß", 300))
	m.threads = append(m.threads, th)
	m.open(th)
	for len(m.messages()) == len(names) {
		m.step()
	}
	m.n.threads.close(m.settings(), threadID(th.name))
	m.n.mu.Lock()
	m.n.dueLocked(m.n.entries[threadID(th.name)])
	m.n.mu.Unlock()
	for i := 0; i < 50; i++ {
		m.step()
	}
	if n := len(m.messages()); n != len(names)+1 {
		t.Errorf("%d messages after the memory lost one: the brake let it through", n)
	}
	if _, told := m.n.threads.get(m.settings(), threadID(th.name)); !told {
		t.Error("the brake did not give the chat's message back to the memory")
	}
}

// A1: an opening during the send of the closing is not wiped: the chat
// ends on the opening.
func TestT4ReopenDuringClose(t *testing.T) {
	th := reachThread("reach:connect")
	m := scripted(t, th)
	m.open(th)
	m.settle(5 * time.Minute)
	m.closeWith(th)
	reopened := false
	m.bot.during = func(msg modelMessage) {
		if msg.action == "close" && !reopened {
			reopened = true
			m.open(th)
		}
	}
	m.settle(5 * time.Minute)
	if got := strings.Join(m.actions(th), ","); got != "open,close,open" {
		t.Errorf("%s", got)
	}
}

// B1: other messages pressing on a full queue while Telegram is down never
// push a thread out.
func TestAThreadIsNeverPushedOutByOtherMessages(t *testing.T) {
	th := reachThread("reach:connect")
	m := scripted(t, th)
	m.bot.down = true
	for i := 0; i < 200; i++ {
		nt := newNotice("shield.ban.temporary", map[string]string{"ip": "203.0.113.9"})
		nt.at = m.clock
		m.n.Notify(nt)
		m.n.Notify(newNotice("container.dying", map[string]string{"name": "app", "code": "1"}))
	}
	m.open(th)
	for i := 0; i < 50; i++ {
		m.step()
	}
	m.bot.down = false
	m.settle(time.Hour)
	if got := strings.Join(m.actions(th), ","); got != "open" {
		t.Errorf("the outage after the flood: %q", got)
	}
}

// B2: an opening not wanted while the kind is off is told once it is on
// again, while the problem lasts.
func TestAnOpeningHeldByTheSettingsIsToldWhenTheyChange(t *testing.T) {
	th := reachThread("reach:connect")
	m := scripted(t, th)
	m.config.Telegram.Kinds = map[string]bool{"reach.down": false}
	m.open(th)
	m.settle(5 * time.Minute)
	if len(m.messages()) != 0 {
		t.Fatal("sent while switched off")
	}
	m.config.Telegram.Kinds = nil
	m.settle(5 * time.Minute)
	if got := strings.Join(m.actions(th), ","); got != "open" {
		t.Errorf("after switching it on: %q", got)
	}
}

// B3: an opening and a closing not yet sent survive a restart, with no
// look at the level (the threads file alone).
func TestUnsentStateSurvivesARestart(t *testing.T) {
	th := reachThread("reach:connect")
	m := scripted(t, th)
	m.bot.down = true
	m.open(th)
	m.step()
	m.restartKeeping(true)
	m.n.levels = nil
	m.bot.down = false
	m.settle(5 * time.Minute)
	if got := strings.Join(m.actions(th), ","); got != "open" {
		t.Fatalf("the opening after a restart: %q", got)
	}
	m.bot.down = true
	m.closeWith(th)
	m.step()
	m.restartKeeping(true)
	m.n.levels = nil
	m.bot.down = false
	m.settle(5 * time.Minute)
	if got := strings.Join(m.actions(th), ","); got != "open,close" {
		t.Errorf("the closing after a restart: %q", got)
	}
	if len(m.bot.pinned) != 0 {
		t.Errorf("still pinned: %v", m.bot.pinned)
	}
}

// A3: with information off, the end of a problem the chat was told still
// arrives, silently; a closing kind switched off by itself only unpins.
func TestAClosingArrivesWhenInfoIsOff(t *testing.T) {
	for _, closingOff := range []bool{false, true} {
		th := reachThread("reach:connect")
		m := scripted(t, th)
		m.config.Telegram.Delivery = map[string]string{"info": DeliverOff}
		if closingOff {
			m.config.Telegram.Kinds = map[string]bool{"reach.restored": false}
		}
		m.open(th)
		m.settle(5 * time.Minute)
		m.closeWith(th)
		m.settle(5 * time.Minute)
		msgs := m.messages()
		if closingOff {
			if len(msgs) != 1 {
				t.Errorf("the closing switched off was sent: %d messages", len(msgs))
			}
		} else if len(msgs) != 2 || msgs[1].action != "close" || !msgs[1].silent {
			t.Errorf("info off: %d messages %+v", len(msgs), msgs)
		}
		if len(m.bot.pinned) != 0 {
			t.Errorf("closing off %v: still pinned", closingOff)
		}
	}
}

// The table is bounded; when every entry is an open problem the new one is
// still delivered, as a message of its own, and counted. (The mutation "a
// full table drops the new entry silently" fails here.)
func TestTheTableNeverDropsANewProblem(t *testing.T) {
	m := scripted(t)
	m.config.Telegram.Kinds = map[string]bool{"disk.filling": false}
	for i := 0; i < maxThreadEntries; i++ {
		th := &modelThread{name: "fill:/m" + strings.Repeat("x", i%7) + string(rune('A'+i%26)) + string(rune('a'+i/26)), opening: "disk.filling", closing: "disk.filling.over"}
		m.open(th)
	}
	m.settle(5 * time.Minute)
	if len(m.n.entries) != maxThreadEntries {
		t.Fatalf("%d entries", len(m.n.entries))
	}
	th := reachThread("reach:new")
	m.open(th)
	m.settle(5 * time.Minute)
	msgs := m.messages()
	if len(msgs) != 1 || !strings.Contains(msgs[0].text, "Unreachable") {
		t.Fatalf("the problem beyond the table: %d messages", len(msgs))
	}
	if len(m.n.entries) > maxThreadEntries || m.n.overflowed != 1 {
		t.Errorf("%d entries, %d counted", len(m.n.entries), m.n.overflowed)
	}
	// closed entries make room, the oldest first; open ones never do
	m.config.Telegram.Kinds = nil
	for _, e := range m.n.entries {
		if !e.open {
			t.Errorf("a closed entry kept: %s", e.name)
		}
	}
}

// A pin or an unpin Telegram refuses is tried again, a few times, then
// said in the log.
func TestARefusedUnpinIsTriedAgainThenSaid(t *testing.T) {
	th := reachThread("reach:connect")
	m := scripted(t, th)
	m.open(th)
	m.settle(5 * time.Minute)
	m.bot.refuseAll = true
	m.closeWith(th)
	m.settle(5 * time.Minute)
	for i := 0; i < maxPinTries; i++ {
		m.n.reconcile()
		m.settle(5 * time.Minute)
	}
	if !strings.Contains(strings.Join(m.trace, "\n"), "could not be unpinned after") {
		t.Error("an unpin given up is not said")
	}
	if left := m.n.threads.unpins(m.settings()); len(left) != 0 {
		t.Errorf("tried for ever: %v", left)
	}
	// refused once, then taken
	th2 := reachThread("reach:dns")
	m.threads = append(m.threads, th2)
	m.bot.refuseAll = false
	m.bot.refuseOnce = true
	m.open(th2)
	m.settle(5 * time.Minute)
	m.closeWith(th2)
	m.settle(5 * time.Minute)
	m.n.reconcile()
	m.settle(5 * time.Minute)
	for id := range m.bot.pinned {
		if id != m.messages()[0].id {
			t.Errorf("message %d still pinned", id)
		}
	}
}

// (b) A panic in the code of a message does not end the process: the
// sender logs it, starts again, and sends the next message.
func TestAPanicDoesNotStopTheSender(t *testing.T) {
	th := reachThread("reach:connect")
	m := scripted(t, th)
	logs := make(chan string, 16)
	n := newNotifierWith(m.bot, m.settings)
	n.threads = newThreadMemory("")
	n.logf = func(line string) { logs <- line }
	n.limits.pace = 0
	n.limits.backoff = []time.Duration{10 * time.Millisecond}
	panicked := false
	n.traceDecision = func(string, string, bool, bool) {
		if !panicked {
			panicked = true
			var nothing *notice
			_ = nothing.kind // a nil dereference, as P1 was
		}
	}
	m.n = n
	n.start()
	defer n.close()
	m.open(th)
	select {
	case line := <-logs:
		if !strings.Contains(line, "starts again (1 so far)") {
			t.Errorf("log: %s", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no recovery")
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && len(m.messages()) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if len(m.messages()) != 1 {
		t.Errorf("nothing sent after the recovery")
	}
}

// T4: a farewell waiting when the server stops is recorded as lost at the
// next start; the chat that loses the notifications is unpinned.
func TestTheFarewellWaitingAtARestartIsRecordedLost(t *testing.T) {
	path := t.TempDir() + "/" + threadsFileName
	memory := newThreadMemory(path)
	memory.farewell(&farewellMark{What: "now go to another chat", By: "mallory from 203.0.113.66", At: time.Now()})
	again := newThreadMemory(path)
	mark := again.takeFarewell()
	if mark == nil || mark.What != "now go to another chat" || !strings.Contains(mark.By, "mallory") {
		t.Fatalf("after the restart: %+v", mark)
	}
	if newThreadMemory(path).takeFarewell() != nil {
		t.Error("recorded twice")
	}

	st := testSettings()
	memory.open(st, "a", "reach:connect", "reach.down", 41, true)
	memory.pinned(st, "a", 41, true)
	memory.open(st, "b", "docker.down", "docker.down", 42, false)
	if pins := memory.leave(st); len(pins) != 1 || pins[0] != 41 {
		t.Errorf("the pins of the chat left: %v", pins)
	}
	if _, kept := memory.get(st, "a"); kept {
		t.Error("the chat left is still followed")
	}
	if !leavesChat([]string{"now go to another chat"}) || leavesChat([]string{"no longer send: Disk failing"}) {
		t.Error("leavesChat")
	}
}

// Minor: docker.unwatched and smart.gone do not read as "OK".
func TestEndsThatAreNotRecoveriesDoNotReadOK(t *testing.T) {
	for _, kind := range []string{"docker.unwatched", "smart.gone", "thread.ended"} {
		m := render(testSettings(), sampleNotice(kind))
		if strings.HasPrefix(m.text, closingMark) {
			t.Errorf("%s: %q", kind, PreviewLine(m.text))
		}
	}
}

var _ = context.Background
