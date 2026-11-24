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
// A fixed list of seeds runs every time; MODEL_RUNS=n (or MODEL_SECONDS=n)
// adds a random pass, MODEL_RANDOM_ONLY=1 runs it alone, MODEL_SEED=n
// replays one seed with its trace (MODEL_LATE=1 with late events). Every
// failing seed of a random pass goes into the fixed lists
// (TestModelSeedsThatFailedOnce) or becomes a named test.

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
	badFormOnce   bool // the next send is refused for its form
	during        func(msg modelMessage)
	sentTo        map[string]map[int]bool
	timedOutCount int
	// refusedForms: the threads of the messages refused for their form, in
	// order; the plain form sent later names nothing (its values are left
	// out) and is matched by its first line
	refusedForms []refusedForm
	plainSeen    map[string]bool // threads whose plain form came
}

var markerPattern = regexp.MustCompile(`model thread (T[0-9]+)`)

type refusedForm struct {
	marker string
	at     time.Time
}

func (b *modelBot) sendMessage(ctx context.Context, st settings, out outgoing) (int, *sendError) {
	m := b.m
	if !st.usable {
		m.violate("I5", "sent while switched off: %q", PreviewLine(out.message.text))
	}
	outcome := "ok"
	switch {
	case b.down:
		return 0, &sendError{Reason: ReasonTelegramError, Message: "bad gateway", Retryable: true}
	case b.badFormOnce:
		b.badFormOnce, outcome = false, "400bad"
	case b.timeoutTaken:
		b.timeoutTaken, outcome = false, "timeout"
	case b.timeoutLost:
		b.timeoutLost = false
		m.lostSend(out, st)
		return 0, &sendError{Reason: ReasonTimeout, Message: "timeout", Retryable: true}
	case b.failing && m.store:
		// review 6: refusals of the settings (a wrong token, the bot
		// blocked, the chat not found) and of the message's form too
		outcome = m.pick([]string{"ok", "ok", "ok", "ok", "ok", "ok", "ok", "ok", "502", "502", "429", "429", "timeout", "timeout",
			"lost", "lost", "button", "401", "403", "400chat", "400bad"})
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
	case "401", "403", "400chat":
		m.pauses = append(m.pauses, [2]time.Time{m.clock, m.clock.Add(m.n.limits.settingsPause)})
	case "400bad":
		for marker := range m.unpaired {
			if strings.Contains(out.message.text, marker) {
				m.unpairedPlain[marker] = true
			}
		}
		marker := m.threadOfText(out.message.text)
		if markerPattern.FindStringSubmatch(out.message.text) == nil && strings.Contains(out.message.text, "are in the panel") {
			// the plain form refused too: the thread of the refusal it follows
			marker = b.plainThread(out.message.text, false)
		}
		if marker == "" && out.replyTo != 0 {
			// a reply that names nothing ("No longer followed"): the thread
			// of the message it answers
			for _, other := range b.chat {
				if other.id == out.replyTo && other.chat == st.chatID {
					marker = m.threadOfMessage(other)
				}
			}
		}
		if marker != "" {
			b.refusedForms = append(b.refusedForms, refusedForm{marker, m.clock})
		}
		m.log("refused for its form: %q", strings.ReplaceAll(out.message.text, "\n", " | "))
	}
	switch outcome {
	case "401":
		m.feature("token refused")
		return 0, &sendError{Reason: ReasonWrongToken, Settings: true, Message: "token"}
	case "403":
		m.feature("bot blocked")
		return 0, &sendError{Reason: ReasonBlocked, Settings: true, Message: "blocked"}
	case "400chat":
		m.feature("chat not found")
		return 0, &sendError{Reason: ReasonChatNotFound, Settings: true, Message: "chat not found"}
	case "400bad":
		m.feature("message refused for its form")
		return 0, &sendError{Reason: ReasonBadMessage, Message: "entities"}
	case "lost":
		// not taken, but the notifier cannot know: its fate is unknown
		m.lostSend(out, st)
		return 0, &sendError{Reason: ReasonTimeout, Message: "timeout", Retryable: true}
	}
	b.nextID++
	msg := modelMessage{id: b.nextID, chat: st.chatID, replyTo: out.replyTo, silent: out.silent, text: out.message.text,
		at: m.clock, timedOut: outcome == "timeout"}
	if b.sentTo[msg.chat] == nil {
		b.sentTo[msg.chat] = map[int]bool{}
	}
	b.sentTo[msg.chat][msg.id] = true
	// only the plain form says that the values are in the panel
	if markerPattern.FindStringSubmatch(msg.text) == nil && strings.Contains(msg.text, "are in the panel") {
		// the latest refusal first: the plain form follows its own refusal
		if marker := b.plainThread(msg.text, outcome != "timeout"); marker != "" { // an answer lost: it comes again
			m.plainOf[msg.id] = marker // one refusal can stand for several plain sends
		}
	} else if found := markerPattern.FindStringSubmatch(msg.text); found != nil {
		// the full form went through: its refusals are over
		kept := b.refusedForms[:0]
		for _, r := range b.refusedForms {
			if r.marker != found[1] {
				kept = append(kept, r)
			}
		}
		b.refusedForms = kept
		delete(b.plainSeen, found[1])
	}
	m.classify(&msg)
	b.chat = append(b.chat, msg)
	m.sentAt(msg, st)
	if b.during != nil {
		b.during(msg)
	}
	if outcome == "timeout" {
		for marker := range m.unpaired {
			if strings.Contains(msg.text, marker) {
				m.unpairedTimedOut[marker] = true // this one's answer was lost
			}
		}
		b.timedOutCount++
		m.feature("timeout taken")
		if msg.action == "close" {
			m.feature("closing timed out")
		}
		return 0, &sendError{Reason: ReasonTimeout, Message: "timeout", Retryable: true}
	}
	return msg.id, nil
}

// plainThread: the thread of a plain form, from the latest refusal whose
// thread can have its first line.
func (b *modelBot) plainThread(text string, accepted bool) string {
	line := PreviewLine(text)
	// a refused thread whose plain form has not come yet first
	for _, first := range []bool{true, false} {
		for i := len(b.refusedForms) - 1; i >= 0; i-- {
			r := b.refusedForms[i]
			if b.m.linesOf(r.marker)[line] && (!first || !b.plainSeen[r.marker]) {
				if b.plainSeen == nil {
					b.plainSeen = map[string]bool{}
				}
				if accepted {
					b.plainSeen[r.marker] = true // a refused plain form did not come
				}
				return r.marker
			}
		}
	}
	return ""
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
func (m *model) lostSend(out outgoing, st settings) {
	msg := modelMessage{text: out.message.text, replyTo: out.replyTo, chat: st.chatID}
	m.classify(&msg)
	if m.unknownFateAt == nil {
		m.unknownFateAt = map[string][]time.Time{}
	}
	m.unknownFateAt[msg.thread] = append(m.unknownFateAt[msg.thread], m.clock)
	if msg.action == "close" {
		m.lostCloseAt[msg.thread] = append(m.lostCloseAt[msg.thread], m.clock)
	}
	m.log("lost %s %s reply=%d %q", msg.action, msg.thread, msg.replyTo, PreviewLine(msg.text))
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
	rowAt      time.Time   // when its open row was last written
	writtenAt  time.Time   // when its last opening was written (heard by the notifier)
	lostOpenAt time.Time   // a restart came before the notifier wrote its opening down
	wrongOver  bool        // its lagging source's last answer was a wrong "over"
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
	// review 6: the real store and level (model_store_test.go)
	store            bool
	lagging          bool              // the registered sources answer up to two minutes late
	nextReconcile    time.Time         // the reconcile at its real cadence
	unpairedTimedOut map[string]bool   // messages of their own whose answer was lost
	lines            map[string]firstLine // first lines that do not name the thread
	leftChats        map[string]int    // chats left (their pins taken away), with the pins that stayed
	leftAt           map[string]time.Time
	// while the settings are refused, messages of their own are counted
	// into a summary (by design); one refused for its form goes in a form
	// that names nothing
	pauses        [][2]time.Time
	unpairedPlain map[string]bool
	closingOffAt  map[string][]time.Time // a closing kind switched off by itself
	closingOnAt   map[string][]time.Time // ... and on again
	plainOf       map[int]string         // the thread of a message sent in its plain form
	lostCloseAt   map[string][]time.Time // a closing whose fate is unknown (S2: the chat may show it over)
	fillAt        time.Time
	persistedAt   time.Time
	threadLines   map[string]map[string]bool
	excusedClose  map[int]bool // closings the live I9 excused (the model's own doing)
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
// threadOfText is the model thread a message names: its marker, or a
// first line that only that thread's kinds have (review 6).
func (m *model) threadOfText(text string) string {
	if found := markerPattern.FindStringSubmatch(text); found != nil {
		return found[1]
	}
	return m.lines[PreviewLine(text)].marker
}

// threadOfMessage: the marker in its text, else the stand-in's record of a
// plain form, else a first line only one registered thread has.
func (m *model) threadOfMessage(msg modelMessage) string {
	if found := markerPattern.FindStringSubmatch(msg.text); found != nil {
		return found[1]
	}
	if plain := m.plainOf[msg.id]; plain != "" {
		return plain
	}
	return m.lines[PreviewLine(msg.text)].marker
}

func (m *model) classify(msg *modelMessage) {
	plain := ""
	if markerPattern.FindStringSubmatch(msg.text) == nil {
		plain = m.plainOf[msg.id]
	}
	msg.thread = m.threadOfText(msg.text)
	if plain != "" {
		msg.thread = plain // the plain form of a refused message: the stand-in's record
	}
	line, generic := m.lines[PreviewLine(msg.text)]
	if plain != "" {
		line, generic = firstLine{marker: plain, closing: m.isClosingLine(plain, PreviewLine(msg.text))}, true
	}
	switch {
	case strings.Contains(msg.text, "End of model thread") && strings.Contains(msg.text, "(was: "),
		strings.Contains(msg.text, "could not be sent while it lasted"): // the merged message, in either form
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
	case generic && line.closing:
		msg.action = "close"
	case generic && msg.replyTo != 0:
		msg.action = "repeat"
	case generic:
		msg.action = "open"
	default:
		msg.action = "plain"
	}
	if msg.thread == "" && msg.replyTo != 0 {
		// "No longer followed" names no thread: the opening it answers does
		for _, other := range m.bot.chat {
			if other.id == msg.replyTo && other.chat == msg.chat {
				if thread := m.threadOfMessage(other); thread != "" {
					msg.thread = thread
				}
			}
		}
		// a reply in its plain form (values left out): a repeat or a
		// closing of the thread it answers, by its first line
		if msg.thread != "" && msg.action == "plain" {
			msg.action = "repeat"
			if m.isClosingLine(msg.thread, PreviewLine(msg.text)) {
				msg.action = "close"
			}
		}
	}
}

// sentAt checks I5 for what was accepted.
func (m *model) sentAt(msg modelMessage, st settings) {
	m.classify(&msg)
	body := strings.ReplaceAll(msg.text, "\n", " | ")
	if r := []rune(body); len(r) > 400 {
		body = string(r[:400])
	}
	m.log("sent #%d %s %s reply=%d %q", msg.id, msg.action, msg.thread, msg.replyTo, body)
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
		// a message of its own (the table full) is decided when it is
		// queued, as before plan 48
		queuedBefore := m.features["full table"] && msg.replyTo == 0 && between(m.settingAt, latestBefore(th.closedAt, m.clock), m.clock)
		if on, set := st.kinds[th.closing]; set && !on && !queuedBefore {
			m.violate("I5", "a closing switched off by itself was sent")
		}
		// I9, live (review 6): never told over while it is open
		if m.store && th.open {
			if m.closeExcused(th) {
				m.excusedClose[msg.id] = true
			} else {
				m.violate("I9", "%s told over at #%d while it is open", th.marker, msg.id)
			}
		}
	}
}

func (m *model) byName(name string) *modelThread {
	for _, th := range m.threads {
		if th.name == name || utils.ThreadName(th.name) == name {
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
	{"snapshots.late", "snapshots.ontime"}, // review 6: a kind per thread (level-only openings name it)
}

func newModel(t *testing.T, seed int64) *model { return newModelWith(t, seed, false) }

// newModelWith: store, the real notifications store and level (inside
// modelStoreEnv).
func newModelWith(t *testing.T, seed int64, store bool) *model {
	m := &model{t: t, seed: seed, rng: rand.New(rand.NewSource(seed)),
		clock: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC), config: testConfig(),
		unpaired: map[string]int{}, unpairedAt: map[string]time.Time{}, features: map[string]bool{},
		store: store, unpairedTimedOut: map[string]bool{}, leftChats: map[string]int{}, leftAt: map[string]time.Time{}, closingOffAt: map[string][]time.Time{}, closingOnAt: map[string][]time.Time{}, plainOf: map[int]string{}, excusedClose: map[int]bool{}, lostCloseAt: map[string][]time.Time{}, unpairedPlain: map[string]bool{}}
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
	if store {
		if m.rng.Intn(3) == 0 {
			// more pinned critical problems than may be pinned at once
			m.feature("four more critical outages")
			for _, cause := range []string{"dns", "tls", "refused", "cloudflare"} {
				m.addThread("reach:"+cause, "reach.down", "reach.restored", false)
			}
		}
		m.lagging = m.rng.Intn(4) == 0
		m.lines = m.firstLines()
		for _, th := range m.threads {
			if !strings.HasPrefix(th.name, "fill:/bulk/") {
				m.log("thread %s %s (%s, registered %v)", th.marker, short(threadID(utils.ThreadName(th.name))), th.opening, th.registered)
			}
		}
		m.startStoreRun()
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
	if m.store {
		n.levels = faultyLevels{m}
		std.Store(n) // the forwarder's notifier
	}
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
	if !m.store {
		n.limits.reconcileDelay = 5 * time.Second
	}
	m.nextReconcile = m.clock.Add(n.limits.reconcileDelay)
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
	waiting := false
	for _, ev := range m.pending {
		waiting = waiting || ev.th == th
	}
	if !late && !waiting {
		do()
		return
	}
	// after what the source has not written yet, in its order
	due := th.lastDue
	if due.Before(m.clock) {
		due = m.clock
	}
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
		th.writtenAt = m.clock
		th.rowOpen, th.openRow = true, &nt
		m.log("event open %s", th.marker)
		if m.store {
			m.storeOpening(th)
		} else {
			m.n.Notify(nt)
		}
	})
}

func (m *model) repeatThread(th *modelThread) {
	nt := m.notice(th, th.opening)
	nt.repeat, nt.count = true, 2
	m.emit(th, "a repeat", func() {
		m.log("event repeat %s", th.marker)
		if m.store {
			th.rowOpen = true
			m.storeOpening(th) // the store groups it, or not (a day later, rows deleted: D1)
		} else {
			m.n.Notify(nt)
		}
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
		if m.store {
			m.storeClosing(th)
		} else {
			m.n.Notify(nt)
		}
	})
}

func (m *model) quietEnd(th *modelThread) {
	m.truth(th, false)
	th.quietAt = append(th.quietAt, m.clock)
	m.emit(th, "the quiet end", func() {
		resolved := true
		if m.store {
			// the store resolves only a row it holds (deleted rows: none)
			_, resolved = utils.ResolveThread(th.name)
		}
		th.rowOpen, th.openRow = false, nil
		if resolved {
			th.resolvedAt = append(th.resolvedAt, m.clock)
		}
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
		if m.store {
			m.storeDelete(th)
		}
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
	cases := 8
	if m.store {
		cases = 10
	}
	switch m.rng.Intn(cases) {
	case 8:
		// a closing kind switched off by itself (A3: the opening stays the
		// chat's last word, unpinned)
		th := m.threads[m.rng.Intn(len(m.threads))]
		if m.config.Telegram.Kinds == nil {
			m.config.Telegram.Kinds = map[string]bool{}
		}
		if _, set := m.config.Telegram.Kinds[th.closing]; set {
			delete(m.config.Telegram.Kinds, th.closing)
			m.closingOnAt[th.closing] = append(m.closingOnAt[th.closing], m.clock)
		} else {
			m.config.Telegram.Kinds[th.closing] = false
			m.closingOffAt[th.closing] = append(m.closingOffAt[th.closing], m.clock)
		}
		m.feature("closing kind toggled")
		m.settingAt = append(m.settingAt, m.clock)
		m.log("setting kind %s %v", th.closing, m.config.Telegram.Kinds)
	case 9:
		if m.config.Telegram.QuietFrom == "" {
			m.config.Telegram.QuietFrom, m.config.Telegram.QuietTo = "00:00", "23:59"
			m.config.Telegram.QuietSilenceCritical = m.rng.Intn(2) == 0
		} else {
			m.config.Telegram.QuietFrom, m.config.Telegram.QuietTo = "", ""
		}
		m.feature("quiet hours")
		m.log("setting quiet hours %q-%q", m.config.Telegram.QuietFrom, m.config.Telegram.QuietTo)
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
		was := m.settings()
		m.config.Telegram.Enabled = !m.config.Telegram.Enabled
		if m.store && m.n != nil && was.usable && !m.config.Telegram.Enabled {
			// "were switched off" leaves the chat as a change of chat does
			m.leftChats[was.chatID] += leaveChatPins(m.n, was, m.bot) // the pins that stayed, over every leave
			m.leftAt[was.chatID] = m.clock
			m.feature("chat-leave unpin")
		}
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
	was := m.settings()
	defer func() {
		if m.store && m.n != nil && was.usable {
			// the chat that loses the notifications: its pins taken away there
			m.leftChats[was.chatID] += leaveChatPins(m.n, was, m.bot) // the pins that stayed, over every leave
			m.leftAt[was.chatID] = m.clock
			m.feature("chat-leave unpin")
		}
	}()
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
	for _, th := range m.threads {
		// an opening heard after the threads file was last written is
		// lost with the process (the level gives it back)
		if last := latestBefore(th.openedAt, m.clock); th.open && !last.Before(m.persistedAt) {
			th.lostOpenAt = m.clock
		}
	}
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
	if m.store {
		// a source that keeps its state in memory tells an open problem
		// again after a start (Fail2ban, the router, a job that fails again)
		for _, th := range m.threads {
			if th.registered && th.open && m.rng.Intn(2) == 0 {
				m.feature("told again after a restart")
				m.repeatThread(th)
			}
		}
	}
}

// fillTable opens more problems than the table holds.
func (m *model) fillTable() {
	m.feature("full table")
	m.fillAt = m.clock
	for i := 0; i < maxThreadEntries+6; i++ {
		th := m.addThread("fill:/bulk/"+strconv.Itoa(i), "disk.filling", "disk.filling.over", !m.store)
		th.readyAt = m.clock
		m.openThread(th)
	}
}

// step lets the sender do one thing, or the time pass; a reconcile asked
// for (the switch-on path) is served as the loop serves it.
func (m *model) step() bool {
	m.persistedAt = m.clock // the sender writes the threads file as it wakes
	m.deliverDue(false)
	if m.store && !m.clock.Before(m.nextReconcile) {
		// the reconcile at its real cadence (30 s after a start, then every
		// 10 minutes)
		m.log("reconcile (cadence)")
		m.n.reconcile()
		m.nextReconcile = m.clock.Add(m.n.limits.reconcileEvery)
	}
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
	checkpoints := map[int]bool{}
	if m.store {
		// I1 and I6 during the run, twice (review 6)
		checkpoints[m.rng.Intn(steps)] = true
		checkpoints[m.rng.Intn(steps)] = true
	}
	for i := 0; i < steps; i++ {
		if checkpoints[i] {
			m.checkpoint()
		}
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
		// an open row the chat never heard of is not told once it is older
		// than the horizon (a stale row, by design)
		return th.rowOpen && (!m.store || m.clock.Sub(th.rowAt) <= reconcileHorizon)
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
		// what the chat was told before it was last left is no longer
		// followed there (its farewell says so)
		if msg.thread != "" && msg.chat == chat && !msg.at.Before(m.leftAt[chat]) {
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
				if !between(th.closedAt, from, msg.at) && !(m.lagging && lagWindow(th, msg.at)) && !m.excusedClose[msg.id] {
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
		known := m.knownAtEnd(th) && !m.plainIDs[threadID(utils.ThreadName(th.name))]
		lostSince := between(m.lostAt, last.at, m.clock)
		// I1
		if th.open && wanted && known && last.action != "open" && last.action != "repeat" && !m.beyondTheBound(th) {
			m.violate("I1", "%s is open, the chat's last word is %q", th.marker, last.action)
		}
		if !th.open && known && (last.action == "open" || last.action == "repeat") && !last.timedOut && !lostSince && !m.beyondTheBound(th) && !m.closingOff(th, last.at) {
			m.violate("I1", "%s is over, the chat's last word is %q (#%d)", th.marker, last.action, last.id)
		}
		// I3, I4
		outstanding, closed := modelMessage{}, modelMessage{}
		for _, msg := range msgs {
			switch msg.action {
			case "open":
				// review 6: excused only when the earlier opening's answer was
				// lost (it is sent again), the threads file was lost, the chat
				// was left in between (its threads forgotten there), or the
				// thread went as messages of their own (the table full)
				// its closing kind switched off by itself: the end was not
				// told (A3), and the next episode opens anew
				// a closing in between whose fate is unknown (S2: sent again
				// as a new opening)
				left := between(m.chatAt, outstanding.at, msg.at) || m.closingOffDuring(th, outstanding.at, msg.at) ||
					between(m.lostCloseAt[th.marker], outstanding.at, msg.at)
				plainAgain := m.plainIDs[threadID(utils.ThreadName(th.name))]
				if outstanding.id != 0 && !outstanding.timedOut && !between(m.lostAt, outstanding.at, msg.at) && !left && !plainAgain {
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
		if th.open && wanted && (last.action == "open" || last.action == "repeat") && texts.Kinds[th.opening].Pin && newNotice(th.opening, nil).severity == sevCritical {
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i].action == "open" {
					// an opening whose episode ended while its closing kind
					// was off is unpinned and stays (A3): not this episode's
					if m.closingOff(th, msgs[i].at) {
						break
					}
					// a message of its own (the table full) is not pinned
					if !between(m.lostAt, msgs[i].at, m.clock) && !msgs[i].timedOut && !m.plainIDs[threadID(utils.ThreadName(th.name))] {
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
		// a pin older than the chat's last leave was taken away then, or
		// named in the farewell as one that could not be
		if !allowed[id] && knowable && !between(m.lostAt, at, m.clock) && !at.Before(m.leftAt[chat]) {
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
	// I2 in the chats left: their pins were taken away, but for those
	// Telegram refused to take away
	for left, stuck := range m.leftChats {
		if left == chat {
			continue
		}
		kept := 0
		for id := range m.bot.pinned[left] {
			pinnedAt := time.Time{}
			for _, p := range m.bot.pins {
				if p.chat == left && p.id == id && p.pin && p.ok {
					pinnedAt = p.at
				}
			}
			// a pin whose record went with a lost threads file is not known
			if !between(m.lostAt, pinnedAt, m.leftAt[left]) {
				kept++
			}
		}
		if kept > stuck {
			m.violate("I2", "chat %s was left with %d pinned (%d could not be taken away)", left, kept, stuck)
		}
	}
	// I8
	if !m.toggledOff && len(m.chatAt) == 0 {
		for marker, times := range m.unpaired {
			paused := false
			for _, p := range m.pauses {
				// made before a pause ended and never delivered: it waited
				// into the pause, which counts it into a summary
				if !m.unpairedAt[marker].After(p[1]) {
					paused = true
				}
			}
			if times == 0 && !between(m.monthAt, m.unpairedAt[marker], m.clock) && !paused && !m.unpairedPlain[marker] {
				m.violate("I8", "message of its own %s never delivered", marker)
			}
			if times > 1 && !m.unpairedTimedOut[marker] {
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
	m := newModelWith(t, seed, true)
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
	modelSuite(t, false, 3000, modelStats)
}

// TestModelWithLateEvents: the sources' notifications come up to two
// minutes after their state changed (see the header), on 1500 seeds.
func TestModelWithLateEvents(t *testing.T) {
	modelSuite(t, true, 1500, map[string]int{})
}

// modelSuite runs the fixed seeds (a tenth with -short), then a random pass:
// MODEL_RUNS=n random seeds, or MODEL_SECONDS=n seconds of them;
// MODEL_RANDOM_ONLY=1 leaves the fixed seeds out. The random pass goes on
// after a failure and lists every failing seed (for the fixed lists).
func modelSuite(t *testing.T, late bool, runs int, stats map[string]int) {
	modelStoreEnv(t)
	if testing.Short() {
		runs /= 10
	}
	failed := 0
	if os.Getenv("MODEL_RANDOM_ONLY") == "" {
		for seed := int64(1); seed <= int64(runs); seed++ {
			if !runModelWith(t, seed, late, stats) {
				failed++
				if failed > 3 {
					t.Fatal("stopped after 4 failing seeds")
				}
			}
		}
	}
	count, limit := 0, 0
	end := time.Time{}
	if n, _ := strconv.Atoi(os.Getenv("MODEL_RUNS")); n > 0 {
		limit = n
	} else if seconds, _ := strconv.Atoi(os.Getenv("MODEL_SECONDS")); seconds > 0 {
		end = time.Now().Add(time.Duration(seconds) * time.Second)
	}
	failing := []int64{}
	for (limit > 0 && count < limit) || (!end.IsZero() && time.Now().Before(end)) {
		seed := time.Now().UnixNano()
		count++
		if !runModelWith(t, seed, late, stats) {
			failing = append(failing, seed)
			if len(failing) >= 50 {
				break
			}
		}
	}
	if count > 0 {
		t.Logf("random pass (late events %v): %d runs, %d failing: %v", late, count, len(failing), failing)
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
	modelStoreEnv(t)
	m := newModelWith(t, seed, true)
	if os.Getenv("MODEL_LATE") != "" {
		m.lateP, m.lateRng = 0.5, rand.New(rand.NewSource(seed*7919+17))
	}
	m.run(40 + m.rng.Intn(120))
	t.Logf("seed %d: %s\n%s", m.seed, strings.Join(m.violations, "\n"), strings.Join(m.trace, "\n"))
}

// The seeds of the random passes that failed once, kept (the fixed list
// 1..3000 covers the others).
func TestModelSeedsThatFailedOnce(t *testing.T) {
	modelStoreEnv(t)
	for _, seed := range []int64{
		1794975629699488876, 1794975695255890122, 1794975913016431987, 1794975931023152618, // review 4
		1794986394991302526, // review 5: an entry that forgot its kind could not be looked at
		1794986845873144210, // review 5: a closing lost in flight is sent again after a re-opening
		1794991808159429524, // review 5: a closing that timed out is uncertain in its chat, across a change of chat
		// review 6, the real level
		1795511231975621236, // an opening heard but not written down before a restart; the pending closing went
		1795511299980245804, // a closing refused for its form, then reopened by the level: the full form again
		1795511419007793193, // a thread beyond the full table with a name longer than the store keeps
		1795512053694767857, // a full table and a restart (the bounds of the file and of the store's listing)
		1795512583667663259, // failed only inside a long pass: the level's clock was the wall clock (now the notifier's)
		1795512662653143204, // a full table of rows crowded out what a source held open
		1795512767026516955, // the same
		1795515786879045332, // a lagging source's wrong "over", sent after Telegram's refusals and a month
	} {
		runModel(t, seed)
	}
	// review 6, with late events
	for _, seed := range []int64{
		1795509994520303309, // a closing sent after a restart lost the reopening's write, the source still starting
		1795510390319121856, // plain forms of several threads of one kind, refused one after the other
		1795510797275221351, // the end of a thread beyond the full table, queued before its closing was switched off
		1795511211590472632, // the same
		1795511762948446379, // the same
		1795511998330992138, // plain forms of two threads of one kind, one of them refused twice
		1795513930293952598, // a pin pinned again while its refused unpin still waited (TestR6APinWantedAgainDropsItsWaitingUnpin)
	} {
		runModelWith(t, seed, true, map[string]int{})
	}
}
