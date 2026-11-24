package telegram

// Review 6: the model test runs the REAL level code. Its events are
// written to a real notifications store (SQLite, on the model's clock)
// and reach the notifier through the real forwarder and
// noticeFromNotification; the level is the real storeLevels and
// utils.ForkThreadState, with the sources registered per kind as the
// server's sources are. Faults are put around it: a read that fails or
// does not know (the store's lock, a source still starting), rows deleted
// while the problem lasts (the owner's "delete read", the retention), a
// source that answers late (lagging behind its own events by up to two
// minutes, which no source of the server does: it is excused in the
// checks, and the model checks that the chat heals once it answers right).

import (
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

var (
	storeEnvMu  sync.Mutex
	activeModel *model
	sourcesOnce sync.Once
)

// modelKinds are the opening kinds of the model's threads.
var modelKinds = []string{"reach.down", "docker.down", "fail2ban.down", "smart.failing", "disk.filling", "snapshots.late"}

// modelStoreEnv opens a notifications store of the test's own and makes
// the model's sources the registered ones; everything is put back at the
// end of the test.
func modelStoreEnv(t *testing.T) {
	t.Helper()
	storeEnvMu.Lock()
	folder := utils.CONFIGFOLDER
	dir := t.TempDir() + "/"
	utils.CONFIGFOLDER = dir
	if err := utils.InitMetricsDatabase(); err != nil {
		utils.CONFIGFOLDER = folder
		storeEnvMu.Unlock()
		t.Fatal("InitMetricsDatabase:", err)
	}
	restoreRecipients := utils.SetNotificationRecipients(func(string) []string { return []string{"admin"} })
	utils.SetNotificationForwarder(forwardNotification)
	previousStd := std.Load()
	sourcesOnce.Do(registerModelSources)
	t.Cleanup(func() {
		activeModel = nil
		utils.SetNotificationForwarder(nil)
		restoreRecipients()
		utils.CloseMetricsDatabase()
		utils.CONFIGFOLDER = folder
		std.Store(previousStd)
		os.RemoveAll(dir)
		storeEnvMu.Unlock()
	})
}

// registerModelSources registers, for each kind of the model, a source
// that answers for the active model's registered threads (unknown for the
// others, and with no model running).
func registerModelSources() {
	for _, kind := range modelKinds {
		kind := kind
		utils.RegisterThreadLevel(kind, func(thread string) (bool, bool) {
			m := activeModel
			if m == nil {
				return false, false
			}
			th := m.byName(thread)
			if th == nil || !th.registered || th.opening != kind {
				return false, false
			}
			return m.sourceSays(th)
		})
		utils.RegisterOpenThreads(kind, func() ([]string, bool) {
			m := activeModel
			if m == nil {
				return nil, false
			}
			out := []string{}
			for _, th := range m.threads {
				if th.registered && th.opening == kind {
					if open, known := m.sourceSays(th); known && open {
						out = append(out, utils.ThreadName(th.name))
					}
				}
			}
			return out, true
		})
	}
}

// sourceSays is a registered source's answer: unknown while it starts,
// otherwise its state, as of up to two minutes ago in a run whose sources
// lag.
func (m *model) sourceSays(th *modelThread) (bool, bool) {
	if m.clock.Before(th.readyAt) {
		m.feature("source starting")
		return false, false
	}
	if m.lagging && !m.final {
		lag := time.Duration(m.rng.Intn(120)) * time.Second
		m.feature("source lagging")
		return th.openAt(m.clock.Add(-lag)), true
	}
	return th.open, true
}

// faultyLevels is the real level with the run's faults around it.
type faultyLevels struct{ m *model }

func (l faultyLevels) openThreads(kinds []string) ([]notice, error) {
	m := l.m
	if !m.final && m.rng.Float64() < m.errP {
		m.feature("store error")
		return nil, errReadFailed
	}
	m.readsAt = append(m.readsAt, m.clock) // a reconcile's reads (review 7: closeExcused)
	return storeLevels{now: m.now}.openThreads(kinds)
}

func (l faultyLevels) state(name string, kind string, since time.Time) (threadLevel, *notice, error) {
	m := l.m
	if !m.final {
		if m.rng.Float64() < m.errP {
			m.feature("store error")
			return levelUnknown, nil, errReadFailed
		}
		if m.rng.Float64() < m.unknownP {
			m.feature("unknown level")
			return levelUnknown, nil, nil
		}
	}
	lvl, follow, err := storeLevels{now: m.now}.state(name, kind, since)
	if th := m.byName(name); th != nil {
		m.log("level of %s: %s (since %s, err %v)", th.marker, lvl, since.Format("01-02 15:04:05"), err)
		// a lagging source's wrong "over" stands until a read corrects it
		switch {
		case lvl == levelOver && th.open:
			th.wrongOver = true
		case lvl == levelOpen:
			th.wrongOver = false
		}
	}
	return lvl, follow, err
}

type readError struct{}

func (readError) Error() string { return "database is locked" }

var errReadFailed error = readError{}

// startStoreRun empties the store and makes this run's clock and model the
// ones in use. The caller is inside modelStoreEnv.
func (m *model) startStoreRun() {
	db, err := utils.MonitoringWriteDB()
	if err != nil {
		m.t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM notifications"); err != nil {
		m.t.Fatal(err)
	}
	restoreClock := utils.SetNotificationClock(m.now)
	m.t.Cleanup(restoreClock)
	activeModel = m
}

// markedVars: the sentence that names the model thread, and every value a
// first line of the model's kinds uses set to the thread's name, so that the
// form of a message without its details (sent when Telegram refuses the
// first form) still names it.
func markedVars(kind string, sentence string, marker string) []string {
	pairs := []string{"summary", sentence}
	for _, found := range previewVar.FindAllStringSubmatch(texts.Kinds[kind].Preview, -1) {
		pairs = append(pairs, found[1], "model thread "+marker)
	}
	return pairs
}

var previewVar = regexp.MustCompile(`\{\{([a-z]+)\}\}`)

// storeOpening writes what a source writes when a problem starts (or is
// told again): its row, forwarded to the notifier as the server forwards it.
func (m *model) storeOpening(th *modelThread) {
	th.rowAt = m.clock
	utils.WriteNotification(utils.Notification{Recipient: "admin", Level: "error", Fork: &utils.NotificationInfo{
		Kind: th.opening, Thread: th.name,
		What: utils.NText("notif.update.attention.what", markedVars(th.opening, "Opening of model thread "+th.marker+".", th.marker)...),
	}})
}

// storeClosing: the source resolves its rows and writes its follow-up.
func (m *model) storeClosing(th *modelThread) {
	utils.ResolveThread(th.name)
	utils.WriteNotification(utils.Notification{Recipient: "admin", Level: "success", Fork: &utils.NotificationInfo{
		Kind: th.closing, Thread: th.name, Resolves: th.name,
		What: utils.NText("notif.update.attention.what", markedVars(th.closing, "End of model thread "+th.marker+".", th.marker)...),
	}})
}

// storeDelete: the owner deletes the read notifications, or the retention
// runs.
func (m *model) storeDelete(th *modelThread) {
	db, err := utils.MonitoringWriteDB()
	if err != nil {
		return
	}
	db.Exec(utils.MonitoringRebind("DELETE FROM notifications WHERE fork_thread = ?"), utils.ThreadName(th.name))
}

// firstLine is what a first line without the thread's name says: which
// thread ("" when two threads share it) and whether it is an end.
type firstLine struct {
	marker  string
	closing bool
}

// firstLines: the first lines that do not name the thread (the kind's
// words without values: an opening from a source's level alone, its
// notification gone; or a first line without values), per thread and
// kind. The bulk threads (a full table) always carry their name.
func (m *model) firstLines() map[string]firstLine {
	out := map[string]firstLine{}
	add := func(line string, marker string, closing bool) {
		if old, seen := out[line]; seen && old.marker != marker {
			marker = ""
		}
		out[line] = firstLine{marker, closing}
	}
	st := m.settings()
	for _, th := range m.threads {
		if !th.registered || strings.HasPrefix(th.name, "fill:/bulk/") {
			continue
		}
		// a level-only opening comes from a registered source only; the
		// plain forms are matched by the stand-in's record (linesOf)
		nt := genericOpening(th.opening, th.name, m.clock)
		add(PreviewLine(render(st, nt).text), th.marker, false)
		add(PreviewLine(renderWith(st, nt, false).text), th.marker, false)
	}
	return out
}

// linesOf: every first line a thread's messages can have without its name
// (its kinds' words, in either form).
func (m *model) linesOf(marker string) map[string]bool {
	if lines, done := m.threadLines[marker]; done {
		return lines
	}
	if m.threadLines == nil {
		m.threadLines = map[string]map[string]bool{}
	}
	lines := map[string]bool{}
	if th := m.byMarker(marker); th != nil {
		st := m.settings()
		for _, kind := range []string{th.opening, th.closing} {
			nt := genericOpening(kind, th.name, m.clock)
			lines[PreviewLine(render(st, nt).text)] = true
			lines[PreviewLine(renderWith(st, nt, false).text)] = true
		}
	}
	m.threadLines[marker] = lines
	return lines
}

// checkpoint asserts, in the middle of a run, that once Telegram and the
// level answer the chat is right within a bound (I1, I6 during the run):
// Telegram answers, the level answers, the late writes are made, the
// notifier settles and looks at the level; then the run goes on with its
// faults.
func (m *model) checkpoint() {
	if !m.config.Telegram.Enabled {
		return
	}
	m.feature("checkpoint")
	failing, final := m.bot.failing, m.final
	m.bot.failing, m.final = false, true
	m.deliverDue(true)
	start := m.clock
	m.log("checkpoint")
	for i := 0; i < 2; i++ {
		m.settle(20 * time.Minute)
		m.n.reconcile()
	}
	m.settle(20 * time.Minute)
	m.checkLastWords(start, "during the run")
	m.bot.failing, m.final = failing, final
}

// lastWord is the chat's last message of a thread, in the chat in use.
func (m *model) lastWord(th *modelThread) modelMessage {
	chat := m.settings().chatID
	last := modelMessage{}
	for _, msg := range m.bot.chat {
		m.classify(&msg)
		if msg.thread == th.marker && msg.chat == chat && !msg.at.Before(m.leftAt[chat]) {
			last = msg
		}
	}
	return last
}

// checkLastWords is I1 and I6 at a moment the chat must be right.
func (m *model) checkLastWords(start time.Time, when string) {
	m.notePlain()
	st := m.settings()
	for _, th := range m.threads {
		last := m.lastWord(th)
		wanted := st.wants(newNotice(th.opening, nil))
		known := m.knownAtEnd(th) && !m.plainIDs[threadID(utils.ThreadName(th.name))]
		lostSince := between(m.lostAt, last.at, m.clock)
		if th.open && wanted && known && last.action != "open" && last.action != "repeat" && !m.beyondTheBound(th) {
			m.violate("I1", "%s: %s is open, the chat's last word is %q", when, th.marker, last.action)
		}
		if !th.open && known && (last.action == "open" || last.action == "repeat") && !last.timedOut && !lostSince && !m.beyondTheBound(th) && !m.closingOff(th, last.at) {
			m.violate("I1", "%s: %s is over, the chat's last word is %q (#%d)", when, th.marker, last.action, last.id)
		}
		if th.open && wanted && known && newNotice(th.opening, nil).severity == sevCritical && (last.action == "open" || last.action == "repeat") {
			told := false
			for _, msg := range m.bot.chat {
				c := msg
				m.classify(&c)
				if c.thread == th.marker && c.chat == st.chatID && (c.action == "open" || c.action == "repeat") && !c.at.After(start.Add(15*time.Minute)) {
					told = true
				}
			}
			if !told {
				m.violate("I6", "%s: %s told only after %s", when, th.marker, last.at.Sub(start))
			}
		}
	}
}

// isClosingLine: whether a first line is one of the thread's closing kind.
func (m *model) isClosingLine(marker string, line string) bool {
	th := m.byMarker(marker)
	if th == nil {
		return false
	}
	st := m.settings()
	nt := genericOpening(th.closing, th.name, m.clock)
	return line == PreviewLine(render(st, nt).text) || line == PreviewLine(renderWith(st, nt, false).text)
}

// closingOff: the thread's closing kind was switched off by itself since
// its last opening was told: its end is not sent (A3), the opening stays
// the chat's last word, unpinned.
func (m *model) closingOff(th *modelThread, since time.Time) bool {
	return m.closingOffDuring(th, since, m.clock)
}

// closingOffDuring: the closing kind was off at some moment of [from, to].
func (m *model) closingOffDuring(th *modelThread, from time.Time, to time.Time) bool {
	if between(m.closingOffAt[th.closing], from, to) {
		return true
	}
	// off before from, and not on again before it
	lastOff, lastOn := time.Time{}, time.Time{}
	for _, at := range m.closingOffAt[th.closing] {
		if !at.After(from) && at.After(lastOff) {
			lastOff = at
		}
	}
	for _, at := range m.closingOnAt[th.closing] {
		if !at.After(from) && at.After(lastOn) {
			lastOn = at
		}
	}
	return !lastOff.IsZero() && !lastOn.After(lastOff)
}

// beyondTheBound: a thread of a full table in a run that told more problems
// than a chat's memory keeps (maxThreads): the oldest are forgotten, and
// their end cannot be told; or whose threads file was lost: the store lists
// at most maxThreadEntries open problems to rebuild from, after any restart
// (by design).
func (m *model) beyondTheBound(th *modelThread) bool {
	if !strings.HasPrefix(th.name, "fill:/bulk/") {
		return false
	}
	// a restart after the fill, with more problems open then than the
	// store lists (maxThreadEntries) or without the threads file (review 7:
	// only then, as the comment says)
	for _, at := range m.restartAt {
		if at.Before(m.fillAt) {
			continue
		}
		if between(m.lostAt, at, at) || m.openAt(at) > maxThreadEntries {
			return true
		}
	}
	told := map[string]bool{}
	for _, msg := range m.bot.chat {
		c := msg
		m.classify(&c)
		if c.action == "open" && c.thread != "" {
			told[c.thread] = true
		}
	}
	return len(told) > maxThreads
}

// closeExcused: a closing sent while the problem is open is the model's
// own doing, not the notifier's: a late write of the source is still to
// come, or the source lags (it said over from an older look).
// openAt counts the problems open at a moment.
func (m *model) openAt(at time.Time) int {
	count := 0
	for _, th := range m.threads {
		open := false
		for _, change := range th.history {
			if !change.at.After(at) {
				open = change.open
			}
		}
		if open {
			count++
		}
	}
	return count
}

// readSince: a reconcile read the level after from, at a moment the
// source was ready.
func (m *model) readSince(from time.Time, ready time.Time) bool {
	for _, at := range m.readsAt {
		if at.After(from) && !at.Before(ready) {
			return true
		}
	}
	return false
}

func (m *model) closeExcused(th *modelThread) bool {
	for _, ev := range m.pending {
		if ev.th == th {
			return true // a write of the source is still to come
		}
	}
	// the latest opening was heard but not written down before a restart:
	// until the level is looked at again with the source ready (review 7:
	// that read, not a fixed time)
	if !th.lostOpenAt.IsZero() && !m.readSince(th.lostOpenAt, th.readyAt) {
		return true
	}
	// review 7 (N7): no excuse for "the reopening's write was lost in a
	// restart, the source still starting": T1 keeps that closing back (the
	// end is believed only on evidence newer than the moment the thread was
	// last seen open), and the excuse hid its regression (m36)
	// a source lagging up to two minutes said over from a look before the
	// opening, and no read corrected it since (or within the period after)
	return m.lagging && (th.wrongOver || lagWindow(th, m.clock))
}

// lagWindow: an opening of the thread came within the time a lagging
// source can still contradict it.
func lagWindow(th *modelThread, at time.Time) bool {
	for _, opened := range th.openedAt {
		if !opened.After(at) && at.Sub(opened) <= 15*time.Minute {
			return true
		}
	}
	return false
}
