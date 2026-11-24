package telegram

// Review 6: the reviewer's sequences and surviving mutations, each as a
// test.

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// T1: episode 1 is told and ends while Telegram fails; the source opens
// episode 2 and the reconcile opens it from the level; the server restarts
// and the source does not know yet. Episode 1's end (its resolved row and
// its closing row) is no evidence that episode 2 is over.
func TestR6AnEarlierEpisodesEndIsNotThisOnes(t *testing.T) {
	th := reachThread("reach:connect")
	th.registered = true
	m := scripted(t, th)
	m.open(th)
	m.settle(5 * time.Minute)
	m.bot.failing = true
	m.closeWith(th)
	m.step()
	m.clock = m.clock.Add(time.Minute)
	m.truth(th, true) // episode 2: the source's state, its row not written
	m.n.reconcile()
	m.bot.failing = false
	m.settle(5 * time.Minute)
	m.restartKeeping(true)
	th.readyAt = m.clock.Add(24 * time.Hour) // the source does not know yet
	for i := 0; i < 3; i++ {
		m.n.reconcile()
		m.settle(5 * time.Minute)
	}
	if got := strings.Join(m.actions(th), ","); strings.HasSuffix(got, "close") || len(m.pinnedNow()) != 1 {
		t.Errorf("episode 2 ended by episode 1's end: %s, pinned %v", got, m.pinnedNow())
	}
}

// D1: an open problem told again as a new opening (the store groups only
// within a day; a source in memory tells again after a restart) is a
// repeat of the opening the chat holds, never a second opening.
func TestR6ATellOfAnOpenProblemIsARepeat(t *testing.T) {
	for _, restart := range []bool{false, true} {
		th := reachThread("reach:connect")
		m := scripted(t, th)
		m.open(th)
		m.settle(5 * time.Minute)
		m.clock = m.clock.Add(25 * time.Hour)
		if restart {
			m.restartKeeping(true)
		}
		m.open(th) // not marked as a repeat: a new row
		m.settle(5 * time.Minute)
		msgs := m.messages()
		if got := strings.Join(m.actions(th), ","); got != "open,repeat" || msgs[1].replyTo != msgs[0].id || len(m.pinnedNow()) != 1 {
			t.Errorf("restart %v: %s, pinned %v", restart, got, m.pinnedNow())
		}
	}
}

// m15: an end that comes between the reconcile's reads and its apply is
// not undone by the stale read (the closing is sent; nothing is opened).
func TestR6AStaleReadDoesNotUndoAnEnd(t *testing.T) {
	th := reachThread("reach:connect")
	m := scripted(t, th)
	m.open(th)
	m.settle(5 * time.Minute)
	m.n.reconcileHook = func() {
		m.n.reconcileHook = nil
		m.bot.failing = true // the closing waits: the entry is still there
		m.closeWith(th)
	}
	m.n.reconcile()
	m.bot.failing = false
	m.settle(5 * time.Minute)
	if got := strings.Join(m.actions(th), ","); got != "open,close" || len(m.pinnedNow()) != 0 {
		t.Errorf("after a stale read: %s, pinned %v", got, m.pinnedNow())
	}
}

// m18: the precedence of the source and the notifications.
func TestR6TheSourceComesFirst(t *testing.T) {
	storeErr := errors.New("database is locked")
	for _, c := range []struct {
		sourceKnown, sourceOpen bool
		err                     error
		storeOpen, storeOver    bool
		want                    threadLevel
		resolve                 bool
	}{
		{true, true, nil, false, true, levelOpen, false},
		{true, false, nil, true, false, levelOver, true}, // the source saw it end: the row is resolved
		{true, false, storeErr, false, false, levelOver, false},
		{false, false, storeErr, true, false, levelUnknown, false},
		{false, false, nil, true, false, levelOpen, false},
		{false, false, nil, false, true, levelOver, false},
		{false, false, nil, false, false, levelUnknown, false}, // no row: unknown (L1)
	} {
		lvl, resolve := combineLevel(c.sourceKnown, c.sourceOpen, c.err, c.storeOpen, c.storeOver)
		if lvl != c.want || resolve != c.resolve {
			t.Errorf("%+v: %s, resolve %v", c, lvl, resolve)
		}
	}
}

// m19: an open problem a source holds is found through its open list, even
// when the notifications cannot be read (or the row was deleted).
func TestR6TheSourcesOpenListsAreRead(t *testing.T) {
	utils.RegisterOpenThreads("docker.down", func() ([]string, bool) { return []string{"docker.down"}, true })
	defer utils.RegisterOpenThreads("docker.down", func() ([]string, bool) { return nil, false })
	found, err := (storeLevels{}).openThreads(PairedKinds())
	if err != nil || len(found) != 1 || found[0].thread != "docker.down" || found[0].kind != "docker.down" {
		t.Fatalf("%v %+v", err, found)
	}
}

// m08: the chat that loses the notifications has its pins taken away IN
// THAT CHAT, never in the new one.
func TestR6TheLeftChatsPinsAreUnpinnedThere(t *testing.T) {
	waitForNoChanges(t)
	before := testConfig()
	after := testConfig()
	after.Telegram.ChatID = "-1009999999999"
	n := newNotifierWith(nil, func() settings { return settingsOf(after) })
	n.threads = newThreadMemory(t.TempDir() + "/" + threadsFileName)
	n.threads.open(settingsOf(before), threadID("reach:connect"), "reach:connect", "reach.down", 77, true)
	previous := std.Load()
	std.Store(n)
	defer std.Store(previous)
	fake := newFakeTelegram(t)
	useFake(t, fake)
	AnnounceSettingsChange(before, after, "owner", "192.0.2.7")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, req := range fake.received() {
			if strings.HasSuffix(req.path, "/unpinChatMessage") {
				chat := fmt.Sprint(req.body["chat_id"])
				if f, isNumber := req.body["chat_id"].(float64); isNumber {
					chat = fmt.Sprintf("%.0f", f)
				}
				if chat != testChat || req.body["message_id"] != float64(77) {
					t.Errorf("unpinned %v in chat %s", req.body["message_id"], chat)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("the left chat's pin was not taken away")
}

// Found by the model (review 6): with a closing kind switched off by itself,
// an end the source made quietly is not told either ("No longer followed"):
// the opening stays the chat's last word, unpinned.
func TestR6AQuietEndObeysTheClosingSwitchedOff(t *testing.T) {
	th := reachThread("reach:connect")
	m := scripted(t, th)
	m.open(th)
	m.settle(5 * time.Minute)
	m.config.Telegram.Kinds = map[string]bool{"reach.restored": false}
	m.quietEnd(th)
	m.n.reconcile()
	m.settle(5 * time.Minute)
	if got := strings.Join(m.actions(th), ","); got != "open" || len(m.pinnedNow()) != 0 {
		t.Errorf("%s, pinned %v", got, m.pinnedNow())
	}
}

// Found by the model (review 6): more open critical problems than may be
// pinned; when a pinned one ends, the newest one whose pin gave way is
// pinned again.
func TestR6APinThatGaveWayComesBack(t *testing.T) {
	threads := []*modelThread{}
	for _, cause := range []string{"a", "b", "c", "d"} {
		threads = append(threads, reachThread("reach:"+cause))
	}
	m := scripted(t, threads...)
	for _, th := range threads {
		m.open(th)
		m.settle(time.Minute)
	}
	if len(m.pinnedNow()) != maxPinned {
		t.Fatalf("pinned %v", m.pinnedNow())
	}
	m.closeWith(threads[3])
	m.settle(5 * time.Minute)
	first := m.messages()[0].id
	if len(m.pinnedNow()) != maxPinned || !m.pinnedNow()[first] {
		t.Errorf("after an end: pinned %v, the first opening is %d", m.pinnedNow(), first)
	}
}

// Found by the model (review 6): the chat is left and chosen again within a
// minute of an opening there; the next episode's opening is sent, not taken
// for the message sent before the chat was left.
func TestR6AChatLeftForgetsItsBrakes(t *testing.T) {
	th := reachThread("reach:connect")
	m := scripted(t, th)
	m.open(th)
	for len(m.actions(th)) == 0 {
		m.step()
	}
	was := m.settings()
	m.changeChat()
	leaveChatPins(m.n, was, m.bot)
	m.changeChat()
	m.quietEnd(th)
	m.n.reconcile()
	m.open(th)
	m.settle(5 * time.Minute)
	opens := 0
	for _, msg := range m.messages() {
		if msg.chat == was.chatID && msg.action == "open" {
			opens++
		}
	}
	if opens != 2 {
		t.Errorf("openings in the chat chosen again: %d", opens)
	}
}

// Found by the model (review 6): a message refused for its form goes in its
// plain form; when that send's answer is lost, the next event (new words)
// is tried in the full form again.
func TestR6ANewEventTriesTheFullFormAgain(t *testing.T) {
	th := reachThread("reach:connect")
	m := scripted(t, th)
	m.bot.badFormOnce = true
	m.open(th)
	for len(m.bot.chat) == 0 {
		if m.bot.badFormOnce {
			m.step()
			continue
		}
		m.bot.timeoutTaken = true // the plain form is taken, its answer lost
		m.step()
	}
	if !strings.Contains(m.bot.chat[0].text, "are in the panel") {
		t.Fatalf("not the plain form: %q", m.bot.chat[0].text)
	}
	m.closeWith(th) // before the brake lets the opening go again
	m.settle(5 * time.Minute)
	last := m.bot.chat[len(m.bot.chat)-1].text
	if !strings.Contains(last, "End of model thread") || strings.Contains(last, "are in the panel") {
		t.Errorf("the closing went in the plain form: %q", last)
	}
}

// Found by the model (review 6): a closing refused for its form leaves the
// plain form set; when the level opens the problem again, the opening is
// tried in its full form.
func TestR6AReopeningByTheLevelTriesTheFullForm(t *testing.T) {
	th := reachThread("reach:connect")
	m := scripted(t, th)
	m.open(th)
	m.settle(5 * time.Minute)
	m.quietEnd(th)
	m.bot.badFormOnce = true
	m.n.reconcile()
	for m.bot.badFormOnce {
		m.step() // until the closing is refused for its form
	}
	m.bot.down = true
	m.settle(time.Minute) // its plain form waits
	was := m.settings()
	m.config.Telegram.Enabled = false // the chat left
	leaveChatPins(m.n, was, m.bot)
	m.truth(th, true) // open again, unheard
	th.rowOpen = true
	m.config.Telegram.Enabled = true
	m.bot.down = false
	m.n.reconcile()
	m.settle(5 * time.Minute)
	last := m.bot.chat[len(m.bot.chat)-1]
	if !strings.Contains(last.text, "Opening of model thread") || strings.Contains(last.text, "are in the panel") {
		t.Errorf("the opening: %q", last.text)
	}
}

// Found by the model (review 6): a told problem without an entry (the
// threads file keeps at most maxThreads entries across a restart) ends
// while 256 open problems fill the table: its end is told all the same.
func TestR6TheEndOfAToldProblemGetsAnEntryWhenTheTableIsFull(t *testing.T) {
	th := reachThread("reach:connect")
	m := scripted(t, th)
	m.open(th)
	m.settle(5 * time.Minute)
	m.n.mu.Lock()
	delete(m.n.entries, threadID(th.name))
	for i := 0; i < maxThreadEntries; i++ {
		id := fmt.Sprintf("full%04d", i)
		m.n.entries[id] = &threadEntry{id: id, name: "fill:/x/" + id, kind: "disk.filling", open: true, touched: m.clock}
	}
	m.n.mu.Unlock()
	m.quietEnd(th)
	m.n.reconcile()
	m.settle(5 * time.Minute)
	if got := strings.Join(m.actions(th), ","); got != "open,close" || len(m.pinnedNow()) != 0 {
		t.Errorf("%s, pinned %v", got, m.pinnedNow())
	}
}
