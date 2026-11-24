package telegram

// Reviews 4 and 5: the permanent model test of the threads. A randomised
// explorer drives the real notifier, step by step on a frozen clock,
// against a stand-in of Telegram, and checks what the stand-in RECORDED
// (texts, replies, pin calls, per chat), never the notifier's own labels.
//
// The stand-in answers ok, 502, 429, a timeout after it took the message, a
// timeout before it took it, a refused button, or refuses a pin.
//
// The level is a separate, fallible thing (as in production): per thread
// either a source that registered its level (unknown for a while after each
// start) or the notifications' rows (an open row, resolved rows, closing
// rows), which the generator deletes while the problem lasts (the owner's
// "delete read", the 30-day retention); any read can fail (an error) or be
// unknown. The reconcile runs between steps, DURING them (events between
// its reads and its apply), and on the switch-on path as the loop serves
// it.
//
// Late (TestModelWithLateEvents): a source that registered its level
// changes its state first and writes its notification (row and event)
// up to two minutes later, or never when the server restarts in between;
// the level is then AHEAD of the events. Every source of the server
// changes its state before it writes, or under the same lock (watch: inside
// store.with; Docker: remember() first; reachwatch: under w.mu; cron: the
// run's result first), so a level BEHIND its events is not produced and
// not modelled: a level that said "over" after the opening's event came
// would be wrong evidence, which the rule "over needs evidence" excludes.
//
// The generator also changes the chat (inside the brake window too), makes
// closings time out, fills the table beyond its bound, and lets 31 days
// pass. After the run Telegram answers, the level answers, the notifier
// settles and looks at the level a few times; then:
//
//	I1 the chat's last word for each thread matches its truth (when the
//	   level can know it)
//	I2 pinned ⇔ open, and a pin is only ever of a message sent to that chat
//	I3 the order within a thread (a reply answers the opening it follows)
//	I4 no opening twice (save for the one message whose fate is unknown,
//	   a lost file, or an end the notifier could not hear)
//	I5 nothing the settings forbid is sent
//	I6 an open critical problem is told within a bounded time once
//	   Telegram answers
//	I7 nothing panics (the sender is driven without its recovery)
//	I8 every message of its own is delivered, once
//	I9 the chat is never told a problem ended while it never ended since
//	   it was told
//
// A fixed list of seeds runs every time; MODEL_SECONDS=n adds a random
// pass of n seconds, MODEL_SEED=n replays one seed with its trace (MODEL_LATE=1
// with late events). Every
// failing seed becomes a named test (review4_test.go, review5_test.go).

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const modelChatA, modelChatB = "-1001234567890", "-1009876543210"

// modelMessage is a message as the stand-in recorded it.
type modelMessage struct {
	id       int
	chat     string
	replyTo  int
	silent   bool
	text     string
	at       time.Time
	timedOut bool // taken, but the notifier was told it timed out
	// read from the text by the checker
	thread string // the model thread's marker, "" for none
	action string // open, repeat, close, merged, plain
}

type modelPin struct {
	chat string
	id   int
	pin  bool
	ok   bool
	at   time.Time
}

// modelBot is the stand-in of Telegram.
type modelBot struct {
	m       *model
	nextID  int
	chat    []modelMessage
	pins    []modelPin
	pinned  map[string]map[int]bool // per chat
	refused map[int]int             // pin refusals per message
	failing bool                    // the run's phase: answers may fail
	// for the named tests: every send refused; every pin refused; the next
	// pin refused; the next send times out (taken: lost answer; not taken);
	// called during a send that is taken
	down          bool
	refuseAll     bool
	refuseOnce    bool
	timeoutTaken  bool
	timeoutLost   bool
	during        func(msg modelMessage)
	sentTo        map[string]map[int]bool
	timedOutCount int
}

var markerPattern = regexp.MustCompile(`model thread (T[0-9]+)`)

func (b *modelBot) sendMessage(ctx context.Context, st settings, out outgoing) (int, *sendError) {
	m := b.m
	if !st.usable {
		m.violate("I5", "sent while switched off: %q", PreviewLine(out.message.text))
	}
	outcome := "ok"
	switch {
	case b.down:
		return 0, &sendError{Reason: ReasonTelegramError, Message: "bad gateway", Retryable: true}
	case b.timeoutTaken:
		b.timeoutTaken, outcome = false, "timeout"
	case b.timeoutLost:
		b.timeoutLost = false
		m.lostSend(out)
		return 0, &sendError{Reason: ReasonTimeout, Message: "timeout", Retryable: true}
	case b.failing:
		outcome = m.pick([]string{"ok", "ok", "ok", "ok", "502", "429", "timeout", "lost", "button"})
	}
	if outcome == "button" && out.message.button == nil {
		outcome = "ok"
	}
	switch outcome {
	case "502":
		return 0, &sendError{Reason: ReasonTelegramError, Message: "bad gateway", Retryable: true}
	case "429":
		return 0, &sendError{Reason: ReasonRateLimited, Message: "flood", Retryable: true, RetryAfter: 3 * time.Second}
	case "button":
		return 0, &sendError{Reason: ReasonBadButton, Message: "button"}
	case "lost":
		// not taken, but the notifier cannot know: its fate is unknown
		m.lostSend(out)
		return 0, &sendError{Reason: ReasonTimeout, Message: "timeout", Retryable: true}
	}
	b.nextID++
	msg := modelMessage{id: b.nextID, chat: st.chatID, replyTo: out.replyTo, silent: out.silent, text: out.message.text,
		at: m.clock, timedOut: outcome == "timeout"}
	if b.sentTo[msg.chat] == nil {
		b.sentTo[msg.chat] = map[int]bool{}
	}
	b.sentTo[msg.chat][msg.id] = true
	m.classify(&msg)
	b.chat = append(b.chat, msg)
	m.sentAt(msg, st)
	if b.during != nil {
		b.during(msg)
	}
	if outcome == "timeout" {
		b.timedOutCount++
		m.feature("timeout taken")
		if msg.action == "close" {
			m.feature("closing timed out")
		}
		return 0, &sendError{Reason: ReasonTimeout, Message: "timeout", Retryable: true}
	}
	return msg.id, nil
}

func (b *modelBot) pin(ctx context.Context, st settings, messageID int, pin bool) *sendError {
	// I2: a pin or unpin is only ever of a message sent to that chat
	if !b.sentTo[st.chatID][messageID] {
		b.m.violate("I2", "message %d pinned or unpinned in chat %s, where it was never sent", messageID, st.chatID)
	}
	if b.refuseAll || b.refuseOnce || (b.failing && b.refused[messageID] < maxPinTries-1 && b.m.rng.Intn(4) == 0) {
		b.refuseOnce = false
		b.refused[messageID]++
		b.pins = append(b.pins, modelPin{st.chatID, messageID, pin, false, b.m.clock})
		b.m.log("pin %d %v refused", messageID, pin)
		return &sendError{Reason: ReasonNoRights, Message: "not enough rights"}
	}
	b.m.log("pin %d %v", messageID, pin)
	b.pins = append(b.pins, modelPin{st.chatID, messageID, pin, true, b.m.clock})
	if b.pinned[st.chatID] == nil {
		b.pinned[st.chatID] = map[int]bool{}
	}
	if pin {
		b.pinned[st.chatID][messageID] = true
	} else {
		delete(b.pinned[st.chatID], messageID)
	}
	return nil
}

// lostSend records a send that timed out before Telegram took it.
func (m *model) lostSend(out outgoing) {
	msg := modelMessage{text: out.message.text, replyTo: out.replyTo}
	m.classify(&msg)
	if m.unknownFateAt == nil {
		m.unknownFateAt = map[string][]time.Time{}
	}
	m.unknownFateAt[msg.thread] = append(m.unknownFateAt[msg.thread], m.clock)
}

// modelThread is one problem: its truth, what the notifications hold of
// it, and what its source says.
type modelThread struct {
	marker, name, opening, closing string
	ref                            string
	open                           bool
	history                        []modelChange // the truth over time
	// the notifications: an open row, resolved rows, the closing rows
	rowOpen     bool
	openRow     *notice
	resolvedAt  []time.Time
	lastClosing *notice
	closingAt   time.Time
	// a source that registered its level; unknown until readyAt
	registered bool
	readyAt    time.Time
	closedAt   []time.Time // every end
	quietAt    []time.Time // the ends without an event
	openedAt   []time.Time // every opening event
	lastDue    time.Time   // the last late event of this thread waiting
}

// lateEvent is a notification a source writes after its state changed.
type lateEvent struct {
	due time.Time
	th  *modelThread
	do  func()
}

type modelChange struct {
	at   time.Time
	open bool
}

func (th *modelThread) openAt(t time.Time) bool {
	open := false
	for _, c := range th.history {
		if !c.at.After(t) {
			open = c.open
		}
	}
	return open
}

// modelLevels is the level as the sources and the notifications say it.
type modelLevels struct{ m *model }

func (l modelLevels) openThreads(kinds []string) ([]notice, error) {
	m := l.m
	if !m.final && m.rng.Float64() < m.errP {
		m.feature("store error")
		return nil, errors.New("database is locked")
	}
	out := []notice{}
	for _, th := range m.threads {
		switch {
		case th.rowOpen && th.openRow != nil:
			out = append(out, *th.openRow)
		case th.registered && th.open && !m.clock.Before(th.readyAt):
			// the source holds it open, its notification deleted
			nt := genericOpening(th.opening, th.name, m.clock)
			nt.what = "Opening of model thread " + th.marker + "."
			out = append(out, nt)
		}
	}
	return out, nil
}

func (l modelLevels) state(name string, kind string, since time.Time) (threadLevel, *notice, error) {
	m := l.m
	th := m.byName(name)
	if th == nil {
		return levelUnknown, nil, nil
	}
	if !m.final {
		if m.rng.Float64() < m.errP {
			m.feature("store error")
			return levelUnknown, nil, errors.New("database is locked")
		}
		if m.rng.Float64() < m.unknownP {
			m.feature("unknown level")
			return levelUnknown, nil, nil
		}
	}
	if th.registered && !m.clock.Before(th.readyAt) {
		// the source's own state, as of its last look
		if th.open {
			return levelOpen, nil, nil
		}
		if th.rowOpen {
			th.rowOpen = false // storeLevels resolves the panel's row
			th.resolvedAt = append(th.resolvedAt, m.clock)
		}
		var follow *notice
		if th.lastClosing != nil && !th.closingAt.Before(since) {
			c := *th.lastClosing
			follow = &c
		}
		return levelOver, follow, nil
	}
	if th.registered {
		m.feature("source starting")
	}
	// the notifications: open on an open row; over on a row resolved, or a
	// closing row, since; no row is unknown
	if th.rowOpen {
		return levelOpen, nil, nil
	}
	over := false
	for _, at := range th.resolvedAt {
		if !at.Before(since) {
			over = true
		}
	}
	var follow *notice
	if th.lastClosing != nil && !th.closingAt.Before(since) {
		c := *th.lastClosing
		follow, over = &c, true
	}
	if over {
		return levelOver, follow, nil
	}
	return levelUnknown, nil, nil
}

type model struct {
	t          *testing.T
	seed       int64
	rng        *rand.Rand
	clock      time.Time
	n          *notifier
	bot        *modelBot
	config     utils.Config
	path       string
	threads    []*modelThread
	violations []string
	lostAt     []time.Time // the restarts without the threads file
	restartAt  []time.Time
	offAt      []time.Time // Telegram switched off: what happens is not heard
	chatAt     []time.Time // the chat changed
	settingAt  []time.Time // a kind or the information was switched
	plainIDs   map[string]bool
	// a send that timed out before Telegram took it: the notifier cannot
	// tell it from one taken, and sends again what may already be there
	unknownFateAt map[string][]time.Time // per model thread
	toggledOff    bool
	restarted     bool
	unpaired      map[string]int       // marker → times delivered
	unpairedAt    map[string]time.Time // marker → when it happened
	monthAt       []time.Time          // a month passed (a message older than its age goes into a summary)
	trace         []string
	features      map[string]bool
	// the level's faults, per run
	errP, unknownP, pruneP float64
	// late events (TestModelWithLateEvents): a registered source's
	// notification comes after its state changed
	lateP   float64
	lateRng *rand.Rand
	pending []lateEvent
	stats   map[string]int
	final                  bool
	lastOpenSent           time.Time
}

func (m *model) now() time.Time { return m.clock }

func (m *model) pick(choices []string) string { return choices[m.rng.Intn(len(choices))] }

func (m *model) feature(name string) { m.features[name] = true }

func (m *model) violate(inv string, format string, args ...interface{}) {
	m.violations = append(m.violations, inv+": "+fmt.Sprintf(format, args...))
}

func (m *model) log(format string, args ...interface{}) {
	m.trace = append(m.trace, m.clock.Format("01-02 15:04:05")+" "+fmt.Sprintf(format, args...))
}

func (m *model) settings() settings { return settingsOf(m.config) }

// classify reads a recorded message: which thread, what it says.
func (m *model) classify(msg *modelMessage) {
	if found := markerPattern.FindStringSubmatch(msg.text); found != nil {
		msg.thread = found[1]
	}
	switch {
	case strings.Contains(msg.text, "End of model thread") && strings.Contains(msg.text, "(was: "):
		msg.action = "merged"
	case strings.Contains(msg.text, "End of model thread"):
		msg.action = "close"
	case strings.Contains(msg.text, word("closing")) && strings.Contains(msg.text, "No longer followed"),
		strings.Contains(msg.text, "No longer followed"):
		msg.action = "close"
	case strings.Contains(msg.text, "Opening of model thread") && (msg.replyTo != 0 || strings.Contains(msg.text, word("again"))):
		msg.action = "repeat"
	case strings.Contains(msg.text, "Opening of model thread"):
		msg.action = "open"
	default:
		msg.action = "plain"
	}
	if msg.thread == "" && msg.replyTo != 0 {
		// "No longer followed" names no thread: the opening it answers does
		for _, other := range m.bot.chat {
			if other.id == msg.replyTo && other.chat == msg.chat {
				if found := markerPattern.FindStringSubmatch(other.text); found != nil {
					msg.thread = found[1]
				}
			}
		}
	}
}

// sentAt checks I5 for what was accepted.
func (m *model) sentAt(msg modelMessage, st settings) {
	m.classify(&msg)
	m.log("sent #%d %s %s reply=%d %q", msg.id, msg.action, msg.thread, msg.replyTo, PreviewLine(msg.text))
	if msg.thread == "" {
		for marker := range m.unpaired {
			if strings.Contains(msg.text, marker) {
				m.unpaired[marker]++
			}
		}
		return
	}
	th := m.byMarker(msg.thread)
	if th == nil {
		m.violate("I5", "a message of an unknown thread %s", msg.thread)
		return
	}
	switch msg.action {
	case "open":
		m.lastOpenSent = m.clock
		fallthrough
	case "repeat", "merged":
		// a message of its own (the table full) is decided when it is
		// queued, as before plan 48
		queuedBefore := m.features["full table"] && between(m.settingAt, latestBefore(th.openedAt, m.clock), m.clock)
		if !st.wants(newNotice(th.opening, nil)) && !queuedBefore {
			m.violate("I5", "%s of %s sent while %s is not wanted", msg.action, msg.thread, th.opening)
		}
	case "close":
		if on, set := st.kinds[th.closing]; set && !on {
			m.violate("I5", "a closing switched off by itself was sent")
		}
	}
}

func (m *model) byName(name string) *modelThread {
	for _, th := range m.threads {
		if th.name == name {
			return th
		}
	}
	return nil
}

func (m *model) byMarker(marker string) *modelThread {
	for _, th := range m.threads {
		if th.marker == marker {
			return th
		}
	}
	return nil
}

var modelNames = []string{
	"reach:connect",
	"docker.down",
	"fail2ban.down",
	"smart:serial:" + strings.Repeat("磁", 120),
	"fill:/mnt/" + strings.Repeat("данные", 40),
	"update.rejected:" + strings.Repeat("x", 199) + "…",
}

var modelPairs = [][2]string{
	{"reach.down", "reach.restored"},
	{"docker.down", "docker.restored"},
	{"fail2ban.down", "fail2ban.restored"},
	{"smart.failing", "smart.restored"},
	{"disk.filling", "disk.filling.over"},
	{"reach.down", "reach.restored"},
}

func newModel(t *testing.T, seed int64) *model {
	m := &model{t: t, seed: seed, rng: rand.New(rand.NewSource(seed)),
		clock: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC), config: testConfig(),
		unpaired: map[string]int{}, unpairedAt: map[string]time.Time{}, features: map[string]bool{}}
	m.config.Telegram.MaxPerMinute = maxMaxPerMinute
	m.config.Telegram.ChatID = modelChatA
	m.path = filepath.Join(t.TempDir(), threadsFileName)
	m.bot = &modelBot{m: m, pinned: map[string]map[int]bool{}, refused: map[int]int{}, failing: true, sentTo: map[string]map[int]bool{}}
	count := 1 + m.rng.Intn(len(modelNames))
	for i := 0; i < count; i++ {
		m.addThread(modelNames[i], modelPairs[i][0], modelPairs[i][1], m.rng.Intn(2) == 0)
	}
	// the level's faults for this run
	if m.rng.Intn(2) == 0 {
		m.errP = 0.08
	}
	if m.rng.Intn(2) == 0 {
		m.unknownP = 0.15
	}
	if m.rng.Intn(2) == 0 {
		m.pruneP = 0.1
	}
	m.boot()
	return m
}

func (m *model) addThread(name, opening, closing string, registered bool) *modelThread {
	i := len(m.threads)
	th := &modelThread{marker: "T" + strconv.Itoa(i), name: name, opening: opening, closing: closing,
		ref: fmt.Sprintf("%016x", 0xabc000+i), registered: registered}
	m.threads = append(m.threads, th)
	return th
}

// boot starts a notifier (the first, or after a restart).
func (m *model) boot() {
	n := newNotifierWith(m.bot, m.settings)
	n.now = m.now
	n.threads = newThreadMemory(m.path)
	n.threads.now = m.now
	n.levels = modelLevels{m}
	n.logf = func(line string) { m.log("log %s", line) }
	n.raise = func(string) {}
	n.traceDecision = func(id string, action string, told bool, open bool) {
		flags := ""
		if e := n.entries[id]; e != nil {
			flags = fmt.Sprintf(" fresh=%v opening=%v", e.fresh, e.opening != nil)
		}
		m.log("decide %s %s told=%v open=%v%s", short(id), action, told, open, flags)
	}
	n.reconcileHook = func() {
		// an event between the reconcile's reads and its apply
		if !m.final && m.rng.Intn(4) == 0 {
			m.feature("event during the reconcile")
			m.event()
		}
	}
	n.limits.pace = 0
	n.limits.reconcileDelay = 5 * time.Second
	n.limits.normalBurst, n.limits.criticalBurst = 1000, 1000
	n.limits.normalAge, n.limits.criticalAge = 24*time.Hour, 24*time.Hour
	n.limits.backoff = []time.Duration{5 * time.Second, 15 * time.Second, time.Minute}
	n.loadEntries()
	m.n = n
	for _, th := range m.threads {
		// a source with its state in memory knows it only after a while
		th.readyAt = m.clock.Add(time.Duration(m.rng.Intn(90)) * time.Second)
	}
}

func short(id string) string {
	if len(id) > 6 {
		return id[:6]
	}
	return id
}

func (m *model) notice(th *modelThread, kind string) notice {
	nt := newNotice(kind, map[string]string{"port": "443", "disk": "/dev/sdb", "mount": "/mnt/data", "left": "2 days", "name": "x"})
	nt.at = m.clock
	nt.thread = th.name
	nt.ref = th.ref
	if texts.Kinds[kind].Closes != "" {
		nt.resolves = th.name
		nt.what = "End of model thread " + th.marker + "."
	} else {
		nt.what = "Opening of model thread " + th.marker + "."
	}
	nt.key = "n:" + kind
	return nt
}

func (m *model) truth(th *modelThread, open bool) {
	th.open = open
	th.history = append(th.history, modelChange{m.clock, open})
	if open {
		th.openedAt = append(th.openedAt, m.clock)
	} else {
		th.closedAt = append(th.closedAt, m.clock)
	}
}

// emit writes what a source writes (its rows, its event): now, or, for a
// registered source in a run with late events, up to two minutes after
// its state changed, in order per thread.
func (m *model) emit(th *modelThread, what string, do func()) {
	late := m.lateP > 0 && th.registered && !m.final && m.lateRng.Float64() < m.lateP
	if !late && !th.lastDue.After(m.clock) {
		do()
		return
	}
	due := th.lastDue
	if late {
		if at := m.clock.Add(time.Duration(1+m.lateRng.Intn(120)) * time.Second); at.After(due) {
			due = at
		}
		m.feature("event late")
	}
	th.lastDue = due
	m.pending = append(m.pending, lateEvent{due: due, th: th, do: do})
	m.log("%s of %s written at %s", what, th.marker, due.Format("15:04:05"))
}

// deliverDue writes the late notifications that are due, in order.
func (m *model) deliverDue(all bool) {
	for len(m.pending) > 0 {
		i := -1
		for j, ev := range m.pending {
			if all || !ev.due.After(m.clock) {
				i = j
				break
			}
		}
		if i < 0 {
			return
		}
		ev := m.pending[i]
		m.pending = append(m.pending[:i], m.pending[i+1:]...)
		ev.do()
	}
}

// the events of a thread
func (m *model) openThread(th *modelThread) {
	nt := m.notice(th, th.opening)
	m.truth(th, true)
	m.emit(th, "the opening", func() {
		th.rowOpen, th.openRow = true, &nt
		m.log("event open %s", th.marker)
		m.n.Notify(nt)
	})
}

func (m *model) repeatThread(th *modelThread) {
	nt := m.notice(th, th.opening)
	nt.repeat, nt.count = true, 2
	m.emit(th, "a repeat", func() {
		m.log("event repeat %s", th.marker)
		m.n.Notify(nt)
	})
}

func (m *model) closeThread(th *modelThread) {
	nt := m.notice(th, th.closing)
	m.truth(th, false)
	at := m.clock
	m.emit(th, "the closing", func() {
		th.rowOpen, th.openRow = false, nil
		th.resolvedAt = append(th.resolvedAt, m.clock)
		th.lastClosing, th.closingAt = &nt, at
		m.log("event close %s", th.marker)
		m.n.Notify(nt)
	})
}

func (m *model) quietEnd(th *modelThread) {
	m.truth(th, false)
	th.quietAt = append(th.quietAt, m.clock)
	m.emit(th, "the quiet end", func() {
		th.rowOpen, th.openRow = false, nil
		th.resolvedAt = append(th.resolvedAt, m.clock)
		th.lastClosing = nil
		m.log("event quiet close %s", th.marker)
	})
}

// event makes one thing happen.
func (m *model) event() {
	th := m.threads[m.rng.Intn(len(m.threads))]
	switch r := m.rng.Intn(12); {
	case r < 4 && !th.open:
		if !m.lastOpenSent.IsZero() && m.clock.Sub(m.lastOpenSent) < brakeWindow {
			m.feature("flap inside the brake window")
		}
		m.openThread(th)
	case r < 6 && th.open:
		m.repeatThread(th)
	case r < 8 && th.open:
		m.closeThread(th)
	case r < 9 && th.open:
		// the source ends it quietly (its cause changed, its mount is gone)
		m.quietEnd(th)
	case r < 10 && m.rng.Float64() < m.pruneP*5:
		// the owner deletes the read notifications, or the retention runs
		m.feature("rows deleted")
		if th.rowOpen {
			m.feature("row deleted while open")
		}
		th.rowOpen, th.resolvedAt, th.lastClosing = false, nil, nil
		m.log("event rows of %s deleted", th.marker)
	default:
		marker := "m" + strconv.Itoa(m.rng.Intn(1e9))
		if len(m.unpaired) < 8 {
			nt := newNotice("container.dying", map[string]string{"name": marker, "code": "1"})
			nt.at = m.clock
			m.unpaired[marker] = 0
			m.unpairedAt[marker] = m.clock
			m.log("event message of its own %s", marker)
			m.n.Notify(nt)
		}
	}
}

// change switches a setting, restarts, changes the chat, or lets a month
// pass.
func (m *model) change() {
	switch m.rng.Intn(8) {
	case 0:
		kind := m.threads[m.rng.Intn(len(m.threads))].opening
		if m.config.Telegram.Kinds == nil {
			m.config.Telegram.Kinds = map[string]bool{}
		}
		if _, set := m.config.Telegram.Kinds[kind]; set {
			delete(m.config.Telegram.Kinds, kind)
		} else {
			m.config.Telegram.Kinds[kind] = false
		}
		m.feature("kind toggled")
		m.settingAt = append(m.settingAt, m.clock)
		m.log("setting kind %s %v", kind, m.config.Telegram.Kinds)
	case 1:
		if m.config.Telegram.Delivery == nil {
			m.config.Telegram.Delivery = map[string]string{}
		}
		if m.config.Telegram.Delivery["info"] == DeliverOff {
			delete(m.config.Telegram.Delivery, "info")
		} else {
			m.config.Telegram.Delivery["info"] = DeliverOff
		}
		m.feature("information toggled")
		m.settingAt = append(m.settingAt, m.clock)
		m.log("setting info %q", m.config.Telegram.Delivery["info"])
	case 2:
		m.config.Telegram.Enabled = !m.config.Telegram.Enabled
		m.toggledOff = true
		if !m.config.Telegram.Enabled {
			m.offAt = append(m.offAt, m.clock)
		}
		m.feature("Telegram toggled")
		m.log("setting enabled %v", m.config.Telegram.Enabled)
	case 3:
		m.changeChat()
	case 4:
		m.deliverDue(true) // what the sources write comes within minutes
		m.clock = m.clock.Add(31 * 24 * time.Hour)
		m.monthAt = append(m.monthAt, m.clock)
		m.feature("31 days pass")
		m.log("31 days pass")
	default:
		m.restart()
	}
}

func (m *model) changeChat() {
	if m.config.Telegram.ChatID == modelChatA {
		m.config.Telegram.ChatID = modelChatB
	} else {
		m.config.Telegram.ChatID = modelChatA
	}
	m.chatAt = append(m.chatAt, m.clock)
	m.feature("chat changed")
	if !m.lastOpenSent.IsZero() && m.clock.Sub(m.lastOpenSent) < brakeWindow {
		m.feature("chat changed inside the brake window")
	}
	m.log("setting chat %s", m.config.Telegram.ChatID)
}

func (m *model) restart() {
	m.restartKeeping(m.rng.Intn(3) > 0)
}

func (m *model) restartKeeping(keep bool) {
	if !keep {
		os.Remove(m.path)
		m.lostAt = append(m.lostAt, m.clock)
		m.feature("restart without the file")
	} else {
		m.feature("restart with the file")
	}
	m.log("restart (file kept: %v)", keep)
	if len(m.pending) > 0 {
		// what a source had not written yet is lost with the process; its
		// state (registered) is not
		m.feature("late event lost at a restart")
		m.pending = nil
	}
	for _, th := range m.threads {
		th.lastDue = time.Time{}
	}
	m.restartAt = append(m.restartAt, m.clock)
	m.restarted = true
	m.notePlain()
	m.n = nil
	m.boot()
	m.unpaired = map[string]int{} // what waited in memory is dropped by design
}

// fillTable opens more problems than the table holds.
func (m *model) fillTable() {
	m.feature("full table")
	for i := 0; i < maxThreadEntries+6; i++ {
		th := m.addThread("fill:/bulk/"+strconv.Itoa(i), "disk.filling", "disk.filling.over", true)
		th.readyAt = m.clock
		m.openThread(th)
	}
}

// step lets the sender do one thing, or the time pass; a reconcile asked
// for (the switch-on path) is served as the loop serves it.
func (m *model) step() bool {
	m.deliverDue(false)
	m.n.persist()
	it, wait := m.n.next()
	select {
	case <-m.n.reconcileNow:
		m.log("reconcile (asked for)")
		m.n.reconcile()
	default:
	}
	if it == nil {
		if wait > time.Minute {
			wait = time.Minute
		}
		if wait < time.Second {
			wait = time.Second
		}
		m.clock = m.clock.Add(wait)
		return false
	}
	m.n.deliver(it)
	m.clock = m.clock.Add(100 * time.Millisecond)
	return true
}

// settle runs the sender until nothing is due for a long while.
func (m *model) settle(limit time.Duration) {
	end := m.clock.Add(limit)
	idle := time.Duration(0)
	for m.clock.Before(end) && idle < 5*time.Minute {
		before := m.clock
		if m.step() {
			idle = 0
		} else {
			idle += m.clock.Sub(before)
		}
	}
}

func (m *model) run(steps int) {
	if m.rng.Intn(25) == 0 {
		m.fillTable()
	}
	for i := 0; i < steps; i++ {
		switch r := m.rng.Intn(10); {
		case r < 4:
			m.event()
		case r < 5:
			m.change()
		case r < 6:
			m.log("reconcile")
			m.n.reconcile()
		default:
			m.step()
		}
	}
	// Telegram answers now, the level answers, Telegram is on; a few looks
	// at the level
	m.bot.failing = false
	m.final = true
	m.deliverDue(true)
	m.config.Telegram.Enabled = true
	start := m.clock
	for i := 0; i < 3; i++ {
		m.settle(20 * time.Minute)
		m.log("final reconcile")
		m.n.reconcile()
	}
	m.settle(30 * time.Minute)
	m.check(start)
}

// knownAtEnd: whether the level can tell this thread's truth at the end.
func (m *model) knownAtEnd(th *modelThread) bool {
	if th.registered {
		return true
	}
	if th.open {
		return th.rowOpen
	}
	return len(th.resolvedAt) > 0 || th.lastClosing != nil
}

// notePlain keeps which threads the notifier sent as messages of their
// own (the table full): they are under the cap and the summaries.
func (m *model) notePlain() {
	if m.plainIDs == nil {
		m.plainIDs = map[string]bool{}
	}
	m.n.mu.Lock()
	for id := range m.n.plainEver {
		m.plainIDs[id] = true
	}
	m.n.mu.Unlock()
}

// check asserts the invariants on what the stand-in recorded.
func (m *model) check(start time.Time) {
	m.notePlain()
	st := m.settings()
	chat := st.chatID
	byThread := map[string][]modelMessage{}
	for _, msg := range m.bot.chat {
		m.classify(&msg)
		if msg.thread != "" && msg.chat == chat {
			byThread[msg.thread] = append(byThread[msg.thread], msg)
		}
		// I9 in every chat
		if msg.thread != "" && msg.action == "close" {
			th := m.byMarker(msg.thread)
			var opening *modelMessage
			for i := range m.bot.chat {
				if m.bot.chat[i].id == msg.replyTo {
					o := m.bot.chat[i]
					opening = &o
				}
			}
			if th != nil && opening != nil {
				from := latestBefore(th.openedAt, opening.at)
				if !between(th.closedAt, from, msg.at) {
					m.violate("I9", "%s: told over at #%d while it never ended since #%d", msg.thread, msg.id, opening.id)
				}
			}
		}
	}
	pinWanted := []int{}
	mayPin := map[int]bool{}
	for _, th := range m.threads {
		msgs := byThread[th.marker]
		last := modelMessage{}
		if len(msgs) > 0 {
			last = msgs[len(msgs)-1]
		}
		wanted := st.wants(newNotice(th.opening, nil))
		// a thread sent as messages of its own (the table full) is under the
		// cap and the summaries, as before plan 48: not asserted here
		known := m.knownAtEnd(th) && !m.plainIDs[threadID(th.name)]
		lostSince := between(m.lostAt, last.at, m.clock)
		// I1
		if th.open && wanted && known && last.action != "open" && last.action != "repeat" {
			m.violate("I1", "%s is open, the chat's last word is %q", th.marker, last.action)
		}
		if !th.open && known && (last.action == "open" || last.action == "repeat") && !last.timedOut && !lostSince {
			m.violate("I1", "%s is over, the chat's last word is %q (#%d)", th.marker, last.action, last.id)
		}
		// I3, I4
		outstanding, closed := modelMessage{}, modelMessage{}
		for _, msg := range msgs {
			switch msg.action {
			case "open":
				from := latestBefore(th.openedAt, outstanding.at)
				unheard := between(th.closedAt, from, msg.at) && (between(th.quietAt, from, msg.at) || between(m.restartAt, from, msg.at) ||
					between(m.offAt, from, msg.at) || between(m.chatAt, from, msg.at))
				// a message of its own (the table full) is sent again after a
				// restart: whether it went before is not known
				plainAgain := m.plainIDs[threadID(th.name)] || between(m.unknownFateAt[th.marker], outstanding.at, msg.at)
				if outstanding.id != 0 && !outstanding.timedOut && !between(m.lostAt, outstanding.at, msg.at) && !unheard && !plainAgain {
					m.violate("I4", "%s opened twice (#%d then #%d)", th.marker, outstanding.id, msg.id)
				}
				outstanding = msg
			case "repeat":
				plain := m.features["full table"] && msg.replyTo == 0
				if !plain && (outstanding.id == 0 || msg.replyTo != outstanding.id) {
					m.violate("I3", "%s: a repeat #%d answers %d, the opening is %d", th.marker, msg.id, msg.replyTo, outstanding.id)
				}
			case "close":
				plain := m.features["full table"] && msg.replyTo == 0
				// the closing whose answer was lost is sent again: the one
				// message whose fate was unknown
				again := outstanding.id == 0 && closed.timedOut && msg.replyTo == closed.replyTo
				if !plain && !again && (outstanding.id == 0 || msg.replyTo != outstanding.id) && !outstanding.timedOut {
					m.violate("I3", "%s: a closing #%d answers %d, the opening is %d", th.marker, msg.id, msg.replyTo, outstanding.id)
				}
				outstanding, closed = modelMessage{}, msg
			case "merged":
				if msg.replyTo != 0 {
					m.violate("I3", "%s: the merged message answers %d", th.marker, msg.replyTo)
				}
			}
		}
		// I6: told within the bound once Telegram answers
		if th.open && wanted && known && newNotice(th.opening, nil).severity == sevCritical {
			if last.id != 0 && (last.action == "open" || last.action == "repeat") && last.at.Sub(start) > 15*time.Minute {
				told := false
				for _, msg := range msgs {
					if msg.action == "open" && !msg.at.After(start.Add(15*time.Minute)) {
						told = true
					}
				}
				if !told {
					m.violate("I6", "%s told only after %s", th.marker, last.at.Sub(start))
				}
			}
		}
		// any opening of the open episode may stay pinned (one whose answer
		// was lost is not the notifier's to know)
		if th.open {
			for i := len(msgs) - 1; i >= 0 && ((msgs[i].action != "close" || msgs[i].timedOut) && msgs[i].action != "merged"); i-- {
				if msgs[i].action == "open" {
					mayPin[msgs[i].id] = true
				}
			}
		}
		// the opening the chat shows for an open problem is to be pinned
		if th.open && (last.action == "open" || last.action == "repeat") && texts.Kinds[th.opening].Pin && newNotice(th.opening, nil).severity == sevCritical {
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i].action == "open" {
					// a message of its own (the table full) is not pinned
					if !between(m.lostAt, msgs[i].at, m.clock) && !msgs[i].timedOut && !m.plainIDs[threadID(th.name)] {
						pinWanted = append(pinWanted, msgs[i].id)
					}
					break
				}
			}
		}
	}
	// I2 in the chat in use: what is pinned is an open problem's opening,
	// within maxPinned
	allowed := mayPin
	for _, id := range pinWanted {
		allowed[id] = true
	}
	pinned := []int{}
	pinAt := map[int]time.Time{}
	for _, p := range m.bot.pins {
		if p.ok && p.pin && p.chat == chat {
			pinAt[p.id] = p.at
		}
	}
	for id := range m.bot.pinned[chat] {
		pinned = append(pinned, id)
		var at time.Time
		knowable := true
		for _, msg := range m.bot.chat {
			if msg.id == id {
				at = msg.at
				if th := m.byMarker(msg.thread); th != nil && !m.knownAtEnd(th) {
					knowable = false // the level cannot tell it is over
				}
			}
		}
		if !allowed[id] && knowable && !between(m.lostAt, at, m.clock) {
			m.violate("I2", "message %d is pinned, its problem is over", id)
		}
	}
	sort.Ints(pinned)
	want := len(pinWanted)
	if want > maxPinned {
		want = maxPinned
	}
	if len(pinned) < want {
		m.violate("I2", "%d pinned of %d open critical problems (%v of %v)", len(pinned), len(pinWanted), pinned, pinWanted)
	}
	// I8
	if !m.toggledOff && len(m.chatAt) == 0 {
		for marker, times := range m.unpaired {
			if times == 0 && !between(m.monthAt, m.unpairedAt[marker], m.clock) {
				m.violate("I8", "message of its own %s never delivered", marker)
			}
			if times > 1 && m.bot.timedOutCount == 0 {
				m.violate("I8", "message of its own %s delivered %d times", marker, times)
			}
		}
	}
}

func (m *model) report() string {
	tail := m.trace
	if len(tail) > 400 {
		tail = tail[len(tail)-400:]
	}
	return fmt.Sprintf("seed %d: %s\n%s", m.seed, strings.Join(m.violations, "\n"), strings.Join(tail, "\n"))
}

// modelStats counts the generator's features over the runs.
var modelStats = map[string]int{}

func runModel(t *testing.T, seed int64) bool {
	return runModelWith(t, seed, false, modelStats)
}

// runModelWith runs one seed, with late events or not.
func runModelWith(t *testing.T, seed int64, late bool, stats map[string]int) bool {
	t.Helper()
	m := newModel(t, seed)
	if late {
		// its own random source: the seed's other choices stay as they are
		m.lateP, m.lateRng = 0.5, rand.New(rand.NewSource(seed*7919+17))
	}
	m.run(40 + m.rng.Intn(120))
	for name := range m.features {
		stats[name]++
	}
	if len(m.threads) >= 5 {
		stats["5 or more threads"]++
	}
	stats["runs"]++
	if len(m.violations) > 0 {
		t.Errorf("%s", m.report())
		return false
	}
	return true
}

func TestModelOfTheThreads(t *testing.T) {
	failed := 0
	runs := 3000
	if testing.Short() {
		runs = 300
	}
	for seed := int64(1); seed <= int64(runs); seed++ {
		if !runModel(t, seed) {
			failed++
			if failed > 3 {
				t.Fatal("stopped after 4 failing seeds")
			}
		}
	}
	if seconds, _ := strconv.Atoi(os.Getenv("MODEL_SECONDS")); seconds > 0 {
		end := time.Now().Add(time.Duration(seconds) * time.Second)
		count := 0
		for time.Now().Before(end) {
			seed := time.Now().UnixNano()
			count++
			if !runModel(t, seed) {
				failed++
				if failed > 3 {
					t.Fatal("stopped after 4 failing seeds")
				}
			}
		}
		t.Logf("random pass: %d runs", count)
	}
	names := []string{}
	for name := range modelStats {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Logf("generator: %-40s %d", name, modelStats[name])
	}
}

// TestModelWithLateEvents: the sources' notifications come up to two
// minutes after their state changed (see the header), on 1500 seeds.
func TestModelWithLateEvents(t *testing.T) {
	runs := 1500
	if testing.Short() {
		runs = 150
	}
	stats := map[string]int{}
	failed := 0
	for seed := int64(1); seed <= int64(runs); seed++ {
		if !runModelWith(t, seed, true, stats) {
			failed++
			if failed > 3 {
				t.Fatal("stopped after 4 failing seeds")
			}
		}
	}
	names := []string{}
	for name := range stats {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Logf("generator: %-40s %d", name, stats[name])
	}
}

// between: one of the moments lies in [from, to].
func between(moments []time.Time, from time.Time, to time.Time) bool {
	for _, at := range moments {
		if !at.Before(from) && !at.After(to) {
			return true
		}
	}
	return false
}

// latestBefore is the last moment at or before t (t itself when none).
func latestBefore(moments []time.Time, t time.Time) time.Time {
	best := t
	found := false
	for _, at := range moments {
		if !at.After(t) && (!found || at.After(best)) {
			best, found = at, true
		}
	}
	return best
}

// TestModelSeed replays one seed with its trace: MODEL_SEED=n (MODEL_LATE=1
// with late events).
func TestModelSeed(t *testing.T) {
	seed, _ := strconv.ParseInt(os.Getenv("MODEL_SEED"), 10, 64)
	if seed == 0 {
		t.Skip("MODEL_SEED=n replays a seed")
	}
	m := newModel(t, seed)
	if os.Getenv("MODEL_LATE") != "" {
		m.lateP, m.lateRng = 0.5, rand.New(rand.NewSource(seed*7919+17))
	}
	m.run(40 + m.rng.Intn(120))
	t.Logf("seed %d: %s\n%s", m.seed, strings.Join(m.violations, "\n"), strings.Join(m.trace, "\n"))
}

// The seeds of the random passes that failed once, kept (the fixed list
// 1..3000 covers the others).
func TestModelSeedsThatFailedOnce(t *testing.T) {
	for _, seed := range []int64{
		1794975629699488876, 1794975695255890122, 1794975913016431987, 1794975931023152618, // review 4
		1794986394991302526, // review 5: an entry that forgot its kind could not be looked at
		1794986845873144210, // review 5: a closing lost in flight is sent again after a re-opening
		1794991808159429524, // review 5: a closing that timed out is uncertain in its chat, across a change of chat
	} {
		runModel(t, seed)
	}
}
