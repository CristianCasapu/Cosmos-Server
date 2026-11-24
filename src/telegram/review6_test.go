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
