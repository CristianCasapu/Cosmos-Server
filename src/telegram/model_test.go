package telegram

// Review 4: the permanent model test of the threads. A randomised explorer
// drives the real notifier, step by step on a frozen clock, against a
// stand-in of Telegram that answers ok, 502, 429, a timeout after it took
// the message, a refused button, or refuses a pin; with restarts with and
// without the threads file, settings switched, Telegram switched off and
// on, other messages pressing on the queue, and long and non-Latin thread
// names. After the run Telegram answers, the notifier settles, looks at the
// level once (reconcile) and settles again; then:
//
//	I1 the chat's last word for each thread matches its truth
//	I2 pinned ⇔ open (the pins that succeed eventually)
//	I3 the order within a thread (a reply answers the opening it follows)
//	I4 no opening twice (save after a timeout in flight, a lost file, or
//	   an end the source made quietly)
//	I5 nothing the settings forbid is sent
//	I6 an open critical problem is told within a bounded time once
//	   Telegram answers
//	I7 nothing panics (the sender is driven without its recovery)
//	I8 every message of its own is delivered
//
// A fixed list of seeds runs every time; MODEL_SECONDS=n adds a random
// pass of n seconds. A seed that fails becomes a named test below.

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// modelBot is the stand-in of Telegram.
type modelBot struct {
	mu      sync.Mutex
	m       *model
	nextID  int
	chat    []modelMessage
	pinned  map[int]bool
	pinAt   map[int]time.Time
	refused map[int]int // pin refusals per message
	failing bool        // the run's phase: answers may fail
	// for the named tests: every send refused; every pin refused; the
	// next pin refused; called during a send that is taken
	down       bool
	refuseAll  bool
	refuseOnce bool
	during     func(msg modelMessage)
}

type modelMessage struct {
	id      int
	thread  string // the thread's id, "" for a message of its own
	action  string
	kind    string
	replyTo int
	silent  bool
	text    string
	at      time.Time
	// timedOut: taken, but the notifier was told it timed out
	timedOut bool
}

func (b *modelBot) sendMessage(ctx context.Context, st settings, out outgoing) (int, *sendError) {
	b.mu.Lock()
	defer b.mu.Unlock()
	m := b.m
	parts := strings.Fields(out.trace)
	msg := modelMessage{replyTo: out.replyTo, silent: out.silent, text: out.message.text, at: m.now()}
	if len(parts) >= 2 {
		msg.thread, msg.action = parts[0], parts[1]
	}
	// I5: what the settings allow, as they are now
	if !st.usable {
		m.violate("I5", "sent while switched off: %q", PreviewLine(out.message.text))
	}
	outcome := "ok"
	if b.down {
		return 0, &sendError{Reason: ReasonTelegramError, Message: "bad gateway", Retryable: true}
	}
	if b.failing {
		outcome = m.pick([]string{"ok", "ok", "ok", "502", "429", "timeout", "button"})
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
	}
	b.nextID++
	msg.id = b.nextID
	msg.timedOut = outcome == "timeout"
	b.chat = append(b.chat, msg)
	if b.during != nil {
		during := b.during
		b.mu.Unlock()
		during(msg)
		b.mu.Lock()
	}
	m.sentAt(msg)
	if outcome == "timeout" {
		m.timeouts[msg.thread]++
		m.anyTimeout = true
		return 0, &sendError{Reason: ReasonTimeout, Message: "timeout", Retryable: true}
	}
	return msg.id, nil
}

func (b *modelBot) pin(ctx context.Context, st settings, messageID int, pin bool) *sendError {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.refuseAll || b.refuseOnce || (b.failing && b.refused[messageID] < maxPinTries-1 && b.m.rng.Intn(4) == 0) {
		b.refuseOnce = false
		b.refused[messageID]++
		b.m.log("pin %d %v refused", messageID, pin)
		return &sendError{Reason: ReasonNoRights, Message: "not enough rights"}
	}
	b.m.log("pin %d %v", messageID, pin)
	if pin {
		b.pinned[messageID] = true
		b.pinAt[messageID] = b.m.clock
	} else {
		delete(b.pinned, messageID)
	}
	return nil
}

// modelLevels is the truth as the sources would say it.
type modelLevels struct{ m *model }

func (l modelLevels) openThreads(kinds []string) ([]notice, error) {
	out := []notice{}
	for _, th := range l.m.threads {
		if th.open && th.storeRow != nil {
			out = append(out, *th.storeRow)
		}
	}
	return out, nil
}

func (l modelLevels) state(name string, kind string, since time.Time) (bool, *notice, error) {
	for _, th := range l.m.threads {
		if th.name == name {
			if th.open {
				return true, nil, nil
			}
			if th.lastClosing != nil {
				c := *th.lastClosing
				return false, &c, nil
			}
			return false, nil, nil
		}
	}
	return false, nil, nil
}

type modelThread struct {
	name, opening, closing string
	open                   bool
	storeRow               *notice     // the open row the notifications hold
	lastClosing            *notice     // the follow-up of the last end, nil when it ended quietly
	quietAt                []time.Time // the ends without an event
	closedAt               []time.Time // every end
	openedAt               []time.Time // every opening event
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
	timeouts   map[string]int
	anyTimeout bool
	lostAt     []time.Time // the restarts without the threads file
	restartAt  []time.Time
	offAt      []time.Time // Telegram switched off: what happens is not heard
	restarts   int
	toggledOff bool
	unpaired   map[string]int // marker → times delivered
	trace      []string
}

func (m *model) now() time.Time { return m.clock }

func (m *model) pick(choices []string) string { return choices[m.rng.Intn(len(choices))] }

func (m *model) violate(inv string, format string, args ...interface{}) {
	m.violations = append(m.violations, inv+": "+fmt.Sprintf(format, args...))
}

func (m *model) log(format string, args ...interface{}) {
	m.trace = append(m.trace, m.clock.Format("15:04:05")+" "+fmt.Sprintf(format, args...))
}

func (m *model) settings() settings { return settingsOf(m.config) }

// sentAt checks I5 for what was accepted.
func (m *model) sentAt(msg modelMessage) {
	st := m.settings()
	m.log("sent %s %s reply=%d %q", msg.action, short(msg.thread), msg.replyTo, PreviewLine(msg.text))
	if msg.thread == "" {
		for marker := range m.unpaired {
			if strings.Contains(msg.text, marker) {
				m.unpaired[marker]++
			}
		}
		return
	}
	th := m.byID(msg.thread)
	if th == nil {
		m.violate("I5", "a message of an unknown thread %s", msg.thread)
		return
	}
	opening := newNotice(th.opening, nil)
	switch msg.action {
	case "open", "repeat", "merged":
		if !st.wants(opening) {
			m.violate("I5", "%s of %s sent while %s is not wanted", msg.action, short(msg.thread), th.opening)
		}
	case "close":
		if on, set := st.kinds[th.closing]; set && !on {
			m.violate("I5", "a closing switched off by itself was sent")
		}
	}
}

func short(id string) string {
	if len(id) > 6 {
		return id[:6]
	}
	return id
}

func (m *model) byID(id string) *modelThread {
	for _, th := range m.threads {
		if threadID(th.name) == id {
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
		timeouts: map[string]int{}, unpaired: map[string]int{}}
	m.config.Telegram.MaxPerMinute = maxMaxPerMinute
	m.path = filepath.Join(t.TempDir(), threadsFileName)
	m.bot = &modelBot{m: m, pinned: map[int]bool{}, pinAt: map[int]time.Time{}, refused: map[int]int{}, failing: true}
	count := 1 + m.rng.Intn(len(modelNames))
	for i := 0; i < count; i++ {
		m.threads = append(m.threads, &modelThread{name: modelNames[i], opening: modelPairs[i][0], closing: modelPairs[i][1]})
	}
	m.boot()
	return m
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
		m.log("decide %s %s told=%v open=%v", short(id), action, told, open)
	}
	n.limits.pace = 0
	n.limits.reconcileDelay = 5 * time.Second
	n.limits.normalBurst, n.limits.criticalBurst = 1000, 1000
	n.limits.normalAge, n.limits.criticalAge = 24*time.Hour, 24*time.Hour
	n.limits.backoff = []time.Duration{5 * time.Second, 15 * time.Second, time.Minute}
	n.loadEntries()
	m.n = n
}

func (m *model) notice(th *modelThread, kind string) notice {
	nt := newNotice(kind, map[string]string{"port": "443", "disk": "/dev/sdb", "mount": "/mnt/data", "left": "2 days", "name": "x"})
	nt.at = m.clock
	nt.thread = th.name
	if texts.Kinds[kind].Closes != "" {
		nt.resolves = th.name
	}
	nt.key = "n:" + kind
	return nt
}

// event makes one thing happen.
func (m *model) event() {
	th := m.threads[m.rng.Intn(len(m.threads))]
	switch r := m.rng.Intn(10); {
	case r < 4 && !th.open:
		nt := m.notice(th, th.opening)
		th.open, th.storeRow, th.lastClosing = true, &nt, nil
		th.openedAt = append(th.openedAt, m.clock)
		m.log("event open %s", short(threadID(th.name)))
		m.n.Notify(nt)
	case r < 6 && th.open:
		nt := m.notice(th, th.opening)
		nt.repeat, nt.count = true, 2
		m.log("event repeat %s", short(threadID(th.name)))
		m.n.Notify(nt)
	case r < 8 && th.open:
		nt := m.notice(th, th.closing)
		th.open, th.storeRow, th.lastClosing = false, nil, &nt
		th.closedAt = append(th.closedAt, m.clock)
		m.log("event close %s", short(threadID(th.name)))
		m.n.Notify(nt)
	case r < 9 && th.open:
		// the source ends it quietly (its cause changed, its mount is gone)
		th.open, th.storeRow, th.lastClosing = false, nil, nil
		th.quietAt = append(th.quietAt, m.clock)
		th.closedAt = append(th.closedAt, m.clock)
		m.log("event quiet close %s", short(threadID(th.name)))
	default:
		marker := "m" + strconv.Itoa(m.rng.Intn(1e9))
		if len(m.unpaired) < 8 {
			nt := newNotice("container.dying", map[string]string{"name": marker, "code": "1"})
			nt.at = m.clock
			m.unpaired[marker] = 0
			m.log("event message of its own %s", marker)
			m.n.Notify(nt)
		}
	}
}

// change switches a setting.
func (m *model) change() {
	switch m.rng.Intn(5) {
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
		m.log("setting info %q", m.config.Telegram.Delivery["info"])
	case 2:
		m.config.Telegram.Enabled = !m.config.Telegram.Enabled
		m.toggledOff = true
		if !m.config.Telegram.Enabled {
			m.offAt = append(m.offAt, m.clock)
		}
		m.log("setting enabled %v", m.config.Telegram.Enabled)
	default:
		m.restart()
	}
}

func (m *model) restart() {
	m.restartKeeping(m.rng.Intn(3) > 0)
}

func (m *model) restartKeeping(keep bool) {
	m.restarts++
	if !keep {
		os.Remove(m.path)
		m.lostAt = append(m.lostAt, m.clock)
	}
	m.log("restart (file kept: %v)", keep)
	m.restartAt = append(m.restartAt, m.clock)
	m.n = nil
	m.boot()
	m.unpaired = map[string]int{} // what waited in memory is dropped by design
}

// step lets the sender do one thing, or the time pass.
func (m *model) step() bool {
	m.n.persist()
	it, wait := m.n.next()
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
	// Telegram answers now; settings back on; one look at the level
	m.bot.mu.Lock()
	m.bot.failing = false
	m.bot.mu.Unlock()
	if !m.config.Telegram.Enabled {
		m.config.Telegram.Enabled = true
	}
	start := m.clock
	m.settle(30 * time.Minute)
	m.log("final reconcile")
	m.n.reconcile()
	m.settle(30 * time.Minute)
	m.check(start)
}

// check asserts the invariants.
func (m *model) check(start time.Time) {
	st := m.settings()
	m.bot.mu.Lock()
	defer m.bot.mu.Unlock()
	byThread := map[string][]modelMessage{}
	for _, msg := range m.bot.chat {
		if msg.thread != "" {
			byThread[msg.thread] = append(byThread[msg.thread], msg)
		}
	}
	pinWanted := []int{}
	for _, th := range m.threads {
		id := threadID(th.name)
		msgs := byThread[id]
		last := ""
		if len(msgs) > 0 {
			last = msgs[len(msgs)-1].action
		}
		wanted := st.wants(newNotice(th.opening, nil))
		lastAt, lastTimedOut := time.Time{}, false
		if len(msgs) > 0 {
			lastAt, lastTimedOut = msgs[len(msgs)-1].at, msgs[len(msgs)-1].timedOut
		}
		// I1 (a message the notifier was told did not go, or a file lost
		// since, cannot be answered: the exceptions)
		if th.open && wanted && last != "open" && last != "repeat" && !lastTimedOut {
			m.violate("I1", "%s is open, the chat's last word is %q", short(id), last)
		}
		if !th.open && (last == "open" || last == "repeat") && !lastTimedOut && !between(m.lostAt, lastAt, m.clock) {
			m.violate("I1", "%s is over, the chat's last word is %q", short(id), last)
		}
		// I3, I4
		outstanding, outstandingAt := 0, time.Time{}
		for _, msg := range msgs {
			switch msg.action {
			case "open":
				// allowed: the truth ended after the opening that was told, and
				// the end was quiet or lost with a restart (the chat could not
				// hear it); a timeout in flight; a lost file
				from := latestBefore(th.openedAt, outstandingAt)
				if outstanding != 0 && m.timeouts[id] == 0 && !between(m.lostAt, outstandingAt, msg.at) &&
					!(between(th.closedAt, from, msg.at) && (between(th.quietAt, from, msg.at) || between(m.restartAt, from, msg.at) || between(m.offAt, from, msg.at))) {
					m.violate("I4", "%s opened twice (%d then %d)", short(id), outstanding, msg.id)
				}
				outstanding, outstandingAt = msg.id, msg.at
			case "repeat":
				if m.timeouts[id] == 0 && (outstanding == 0 || msg.replyTo != outstanding) {
					m.violate("I3", "%s: a repeat answers %d, the opening is %d", short(id), msg.replyTo, outstanding)
				}
			case "close":
				if m.timeouts[id] == 0 && (outstanding == 0 || msg.replyTo != outstanding) {
					m.violate("I3", "%s: a closing answers %d, the opening is %d", short(id), msg.replyTo, outstanding)
				}
				outstanding = 0
			case "merged":
				if msg.replyTo != 0 {
					m.violate("I3", "%s: the merged message answers %d", short(id), msg.replyTo)
				}
			}
		}
		// I6: told within the bound once Telegram answers
		if th.open && wanted && newNotice(th.opening, nil).severity == sevCritical {
			told := false
			for _, msg := range msgs {
				if (msg.action == "open" || msg.action == "repeat") && msg.at.Sub(start) <= 15*time.Minute {
					told = true
				}
			}
			if !told && len(msgs) > 0 && msgs[len(msgs)-1].at.After(start.Add(15*time.Minute)) {
				m.violate("I6", "%s told only after %s", short(id), msgs[len(msgs)-1].at.Sub(start))
			}
		}
		if th.open && last == "open" && texts.Kinds[th.opening].Pin && newNotice(th.opening, nil).severity == sevCritical {
			pinWanted = append(pinWanted, msgs[len(msgs)-1].id)
		}
		if th.open && last == "repeat" && texts.Kinds[th.opening].Pin {
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i].action == "open" {
					pinWanted = append(pinWanted, msgs[i].id)
					break
				}
			}
		}
	}
	// I2: what is pinned is an open problem's opening, within maxPinned
	allowed := map[int]bool{}
	for _, id := range pinWanted {
		allowed[id] = true
	}
	pinned := []int{}
	for id := range m.bot.pinned {
		pinned = append(pinned, id)
		// a pin the notifier lost with its file cannot be taken away
		if !allowed[id] && !m.anyTimeout && !between(m.lostAt, m.bot.pinAt[id], m.clock) {
			m.violate("I2", "message %d is pinned, its problem is over", id)
		}
	}
	sort.Ints(pinned)
	want := len(pinWanted)
	if want > maxPinned {
		want = maxPinned
	}
	if len(pinned) < want && !m.anyTimeout {
		m.violate("I2", "%d pinned of %d open critical problems (%v of %v)", len(pinned), len(pinWanted), pinned, pinWanted)
	}
	// I8
	if !m.toggledOff {
		for marker, times := range m.unpaired {
			if times == 0 {
				m.violate("I8", "message of its own %s never delivered", marker)
			}
			if times > 1 && !m.anyTimeout {
				m.violate("I8", "message of its own %s delivered %d times", marker, times)
			}
		}
	}
}

func (m *model) report() string {
	return fmt.Sprintf("seed %d: %s\n%s", m.seed, strings.Join(m.violations, "\n"), strings.Join(m.trace, "\n"))
}

func runModel(t *testing.T, seed int64) bool {
	t.Helper()
	m := newModel(t, seed)
	m.run(40 + m.rng.Intn(120))
	if len(m.violations) > 0 {
		t.Errorf("%s", m.report())
		return false
	}
	return true
}

// modelRuns counts what the explorer did, for the report.
var modelRuns struct {
	sync.Mutex
	runs, sends int
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

// TestModelSeed replays one seed with its trace: MODEL_SEED=n.
func TestModelSeed(t *testing.T) {
	seed, _ := strconv.ParseInt(os.Getenv("MODEL_SEED"), 10, 64)
	if seed == 0 {
		t.Skip("MODEL_SEED=n replays a seed")
	}
	m := newModel(t, seed)
	m.run(40 + m.rng.Intn(120))
	t.Logf("%s", m.report())
}
