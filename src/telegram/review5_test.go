package telegram

// Review 5: the reviewer's sequences, each as a test.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// S2: a closing that timed out in flight (Telegram took it), then the
// problem opens again: the chat is told it is open again, as a new opening.
func TestR5CloseTimedOutThenReopened(t *testing.T) {
	for _, restart := range []bool{false, true} {
		th := reachThread("reach:connect")
		m := scripted(t, th)
		m.open(th)
		m.settle(5 * time.Minute)
		m.bot.timeoutTaken = true
		m.closeWith(th)
		m.step()
		if restart {
			m.restartKeeping(true)
		}
		m.open(th)
		m.settle(5 * time.Minute)
		got := strings.Join(m.actions(th), ",")
		if !strings.HasSuffix(got, "close,open") {
			t.Errorf("restart %v: %s", restart, got)
		}
	}
}

// L1: the owner deletes the read notifications (or the retention prunes
// them) while the problem lasts: nothing changes in the chat.
func TestR5DeletedRowEndsNothing(t *testing.T) {
	th := reachThread("reach:connect")
	m := scripted(t, th)
	m.open(th)
	m.settle(5 * time.Minute)
	th.rowOpen, th.resolvedAt, th.lastClosing = false, nil, nil // deleted
	for i := 0; i < 3; i++ {
		m.n.reconcile()
		m.settle(15 * time.Minute)
	}
	if got := strings.Join(m.actions(th), ","); got != "open" || len(m.pinnedNow()) != 1 {
		t.Errorf("after the rows were deleted: %s, pinned %v", got, m.pinnedNow())
	}
	// and an open problem not told yet (its kind off) is not forgotten
	th2 := reachThread("reach:dns")
	m.config.Telegram.Kinds = map[string]bool{"reach.down": false}
	m.open(th2)
	m.settle(5 * time.Minute)
	th2.rowOpen = false
	m.n.reconcile()
	m.settle(5 * time.Minute)
	m.config.Telegram.Kinds = nil
	m.settle(5 * time.Minute)
	if got := strings.Join(m.actions(th2), ","); got != "open" {
		t.Errorf("the problem not told yet: %q", got)
	}
}

// Mutation "a store error read as over": a lookup that fails is unknown,
// and changes nothing in the chat.
func TestR5AStoreErrorIsUnknown(t *testing.T) {
	// no monitoring database in the tests: every lookup fails
	if lvl, _, _ := (storeLevels{}).state("snapshots.late:/srv", "snapshots.late", time.Now().Add(-time.Hour)); lvl != levelUnknown {
		t.Errorf("a failing store reads %s", lvl)
	}
	th := reachThread("reach:connect")
	m := scripted(t, th)
	m.open(th)
	m.settle(5 * time.Minute)
	m.quietEnd(th)
	m.final, m.errP = false, 1 // every read fails
	m.n.reconcile()
	m.settle(5 * time.Minute)
	if got := strings.Join(m.actions(th), ","); got != "open" {
		t.Errorf("a failing level ended the problem: %s", got)
	}
	m.final = true
	m.n.reconcile()
	m.settle(5 * time.Minute)
	if got := strings.Join(m.actions(th), ","); got != "open,close" {
		t.Errorf("once it answers: %s", got)
	}
}

// Mutation "the brake drops instead of delaying": an opening whose answer
// was lost (Telegram did not take it) is sent once the brake allows it.
func TestR5TheBrakeDelaysAndNeverDrops(t *testing.T) {
	th := reachThread("reach:connect")
	m := scripted(t, th)
	m.bot.timeoutLost = true
	m.open(th)
	m.step()
	for i := 0; i < 20; i++ {
		m.step()
		if m.clock.Sub(th.openedAt[0]) >= brakeWindow-time.Second {
			break
		}
	}
	if len(m.actions(th)) != 0 && m.clock.Sub(th.openedAt[0]) < brakeWindow {
		t.Fatalf("sent within the brake: %v", m.actions(th))
	}
	m.settle(5 * time.Minute)
	if got := strings.Join(m.actions(th), ","); got != "open" {
		t.Errorf("after the brake: %q", got)
	}
}

// Mutation "the raw name instead of the hash": a thread is stored by a
// hash of its name, whatever its length and script.
func TestR5AThreadIsKnownByAHash(t *testing.T) {
	name := "reach:" + strings.Repeat("Сервер", 80)
	th := reachThread(name)
	m := scripted(t, th)
	m.open(th)
	m.settle(5 * time.Minute)
	if _, told := m.n.threads.get(m.settings(), threadID(name)); !told {
		t.Fatal("not told under its hash")
	}
	raw, _ := os.ReadFile(m.path)
	var file threadFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	hexID := regexp.MustCompile(`^[0-9a-f]{24}$`)
	for _, chat := range file.Chats {
		for id := range chat.Threads {
			if !hexID.MatchString(id) {
				t.Errorf("a thread kept under %q", id)
			}
		}
	}
	for id := range file.Entries {
		if !hexID.MatchString(id) {
			t.Errorf("an entry kept under %q", id)
		}
	}
}

// S3: the chat changes within a minute of an opening: the new chat gets an
// opening of its own, and no message of the old chat is pinned there.
func TestR5BrakeAcrossAChangeOfChat(t *testing.T) {
	th := reachThread("reach:connect")
	m := scripted(t, th)
	m.open(th)
	for len(m.actions(th)) == 0 {
		m.step()
	}
	m.changeChat()
	m.n.reconcile()
	m.settle(5 * time.Minute)
	m.n.reconcile()
	m.settle(5 * time.Minute)
	inB := 0
	for _, msg := range m.messages() {
		if msg.chat == modelChatB && msg.action == "open" {
			inB++
		}
	}
	if inB != 1 || len(m.violations) > 0 {
		t.Errorf("openings in the new chat: %d; %v", inB, m.violations)
	}
	if len(m.bot.pinned[modelChatB]) != 1 {
		t.Errorf("pinned in the new chat: %v", m.bot.pinned[modelChatB])
	}
}

// S4: an open problem told 40 days ago is still the chat's: its repeat
// answers it, and it is not pinned twice.
func TestR5ThreadAgeKeepsTheOpening(t *testing.T) {
	th := reachThread("reach:connect")
	m := scripted(t, th)
	m.open(th)
	m.settle(5 * time.Minute)
	m.clock = m.clock.Add(40 * 24 * time.Hour)
	m.repeatOf(th)
	m.n.reconcile()
	m.settle(5 * time.Minute)
	msgs := m.messages()
	if got := strings.Join(m.actions(th), ","); got != "open,repeat" || msgs[1].replyTo != msgs[0].id {
		t.Errorf("after 40 days: %s", got)
	}
	if len(m.pinnedNow()) != 1 {
		t.Errorf("pinned %v", m.pinnedNow())
	}
}

// The table full: a problem sent as a message of its own has its end told
// as a message of its own too.
func TestR5FullTableTellsTheEnd(t *testing.T) {
	m := scripted(t)
	m.config.Telegram.Kinds = map[string]bool{"disk.filling": false}
	for i := 0; i < maxThreadEntries; i++ {
		m.open(&modelThread{name: "fill:/b/" + strings.Repeat("x", i%9) + string(rune('A'+i%26)) + string(rune('a'+i/26)), opening: "disk.filling", closing: "disk.filling.over"})
	}
	m.settle(time.Minute)
	th := reachThread("reach:new")
	m.open(th)
	m.settle(time.Minute)
	m.closeWith(th)
	m.settle(time.Minute)
	if got := strings.Join(m.actions(th), ","); got != "open,close" {
		t.Errorf("beyond the table: %q", got)
	}
}

// Minor: a panic outside a thread is not counted against the last thread
// sent, and a success forgets earlier panics.
func TestR5PanicsAreCountedAgainstTheirThread(t *testing.T) {
	th := reachThread("reach:connect")
	m := scripted(t, th)
	m.open(th)
	m.settle(5 * time.Minute)
	if m.n.current != "" {
		t.Errorf("current after a send: %q", m.n.current)
	}
	m.n.recovered(errors.New("elsewhere"))
	if e := m.n.entries[threadID(th.name)]; e != nil && e.panics != 0 {
		t.Errorf("the thread was blamed: %d", e.panics)
	}
	// a panic of its own, then a success: its count starts again
	m.n.mu.Lock()
	m.n.current = threadID(th.name)
	m.n.mu.Unlock()
	m.n.recovered(errors.New("once"))
	m.repeatOf(th)
	m.settle(10 * time.Minute)
	if e := m.n.entries[threadID(th.name)]; e == nil || e.panics != 0 || strings.Join(m.actions(th), ",") != "open,repeat" {
		t.Fatalf("after a success: %+v, %v", e, m.actions(th))
	}
	recorded := make(chan string, 4)
	m.n.recordDropped = func(kind string) { recorded <- kind }
	for i := 0; i < maxThreadPanics; i++ {
		m.n.mu.Lock()
		m.n.current = threadID(th.name)
		m.n.mu.Unlock()
		m.n.recovered(errors.New("boom"))
	}
	kind := ""
	select {
	case kind = <-recorded:
	case <-time.After(5 * time.Second):
	}
	if _, kept := m.n.entries[threadID(th.name)]; kept || kind != "reach.down" {
		t.Errorf("dropped: kept %v, recorded %q", kept, kind)
	}
}

// Minor: a farewell whose sending fails on an error of the code is
// recorded as lost, and the queue is not left stopped.
func TestR5AFarewellThatPanicsIsRecordedLost(t *testing.T) {
	waitForNoChanges(t)
	previous := directClient
	directClient = nil // its use panics
	lost := make(chan string, 4)
	changeMu.Lock()
	previousLost := lostWarning
	lostWarning = func(what string, by string) { lost <- what }
	changeMu.Unlock()
	defer func() {
		directClient = previous
		changeMu.Lock()
		lostWarning = previousLost
		changeMu.Unlock()
	}()
	moved := testConfig()
	moved.Telegram.ChatID = "-1009999999999"
	AnnounceSettingsChange(testConfig(), moved, "owner", "192.0.2.7")
	select {
	case what := <-lost:
		if !strings.Contains(what, "another chat") {
			t.Errorf("lost: %q", what)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("not recorded as lost")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		changeMu.Lock()
		running := changeRunning
		changeMu.Unlock()
		if !running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("the queue of farewells stays stopped")
}

// Found by the model with late events (TestModelWithLateEvents, seeds 373,
// 700, 1064, 1244): a source changes its state, the reconcile sends the
// opening from the level, then the source's own notification of that
// episode arrives: it is the same episode, not a second opening. An
// opening after that is a new one again (P2).
func TestR5LateEventIsTheSameEpisode(t *testing.T) {
	th := reachThread("reach:connect")
	th.registered = true
	m := scripted(t, th)
	m.truth(th, true) // the source's state, its notification not written yet
	m.n.reconcile()
	m.settle(5 * time.Minute)
	nt := m.notice(th, th.opening)
	th.rowOpen, th.openRow = true, &nt
	m.n.Notify(nt)
	m.settle(5 * time.Minute)
	if got := strings.Join(m.actions(th), ","); got != "open" || len(m.pinnedNow()) != 1 {
		t.Fatalf("the late notification of the same episode: %s, pinned %v", got, m.pinnedNow())
	}
	// ended quietly and told again before any look: no end was acted on,
	// so it is a repeat (review 6, D1)
	m.quietEnd(th)
	m.open(th)
	m.settle(5 * time.Minute)
	if got := strings.Join(m.actions(th), ","); got != "open,repeat" || len(m.pinnedNow()) != 1 {
		t.Errorf("a re-opening the chat did not see the end of: %s, pinned %v", got, m.pinnedNow())
	}
}

// Minor: a threads file that would pass its bound is still written, without
// the entries' notices (the level gives them back), and that is logged
// once.
func TestR5TheThreadsFileIsBoundedByItsContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), threadsFileName)
	mem := newThreadMemory(path)
	logged := []string{}
	mem.logf = func(line string) { logged = append(logged, line) }
	big := func(i int) *savedNotice {
		vars := map[string]string{}
		for k := 0; k < 12; k++ {
			vars["v"+strconv.Itoa(k)] = strings.Repeat("Ж", 300)
		}
		return &savedNotice{Kind: "disk.filling", Vars: vars, What: strings.Repeat("Ж", 300), Thread: "fill:/b/" + strconv.Itoa(i)}
	}
	for round := 0; round < 2; round++ {
		entries := map[string]*savedEntry{}
		for i := 0; i < maxThreads; i++ {
			name := "fill:/b/" + strconv.Itoa(i)
			entries[threadID(name)] = &savedEntry{Name: name, Kind: "disk.filling", Open: true, Since: time.Unix(int64(round), 0), Opening: big(i), Closing: big(i)}
		}
		mem.saveEntries(entries)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() > maxThreadsBytes {
		t.Fatalf("the file: %v, %v", info, err)
	}
	raw, _ := os.ReadFile(path)
	var file threadFile
	if err := json.Unmarshal(raw, &file); err != nil || len(file.Entries) != maxThreads {
		t.Fatalf("entries kept: %d, %v", len(file.Entries), err)
	}
	for _, e := range file.Entries {
		if !e.Open || e.Name == "" || e.Kind != "disk.filling" {
			t.Fatalf("an entry lost its state: %+v", e)
		}
	}
	if len(logged) != 0 {
		t.Errorf("logged although written: %v", logged)
	}
}

// Minor: a reconcile that changes nothing does not write the threads file.
func TestR5AReconcileThatChangesNothingWritesNothing(t *testing.T) {
	th := reachThread("reach:connect")
	m := scripted(t, th)
	m.open(th)
	m.settle(5 * time.Minute)
	before, err := os.Stat(m.path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		m.n.reconcile()
		m.settle(5 * time.Minute)
	}
	after, err := os.Stat(m.path)
	if err != nil || !os.SameFile(before, after) {
		t.Errorf("the threads file was written again (%v)", err)
	}
}

// Minor: an entry forgotten between the reconcile's reads and its apply
// (its end was sent meanwhile) is not made again from the stale read.
func TestR5AStaleReadDoesNotBringBackAForgottenEntry(t *testing.T) {
	th := reachThread("reach:connect")
	m := scripted(t, th)
	m.open(th)
	m.settle(5 * time.Minute)
	m.n.reconcileHook = func() {
		m.n.reconcileHook = nil
		m.closeWith(th)
		m.settle(time.Minute)
	}
	m.n.reconcile()
	m.settle(time.Minute)
	if got := strings.Join(m.actions(th), ","); got != "open,close" || len(m.pinnedNow()) != 0 {
		t.Errorf("after a stale read: %s, pinned %v", got, m.pinnedNow())
	}
}
