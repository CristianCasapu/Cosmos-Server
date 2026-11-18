package telegram

import (
	"context"
	"strconv"
	"sync"
	"time"
)

// priority decides how a message is treated when there are many, and
// whether it makes a sound.
type priority int

const (
	// priorityLow: good to know. Delivered without sound.
	priorityLow priority = iota
	// priorityNormal: something to look at.
	priorityNormal
	// priorityCritical: a security event of high severity. Has a budget
	// and a queue of its own, is not counted against the cap per minute,
	// is tried for hours and rings during the quiet hours.
	priorityCritical
)

// limits are the numbers the notifier works with. The tests shorten them.
type limits struct {
	pace           time.Duration // between two requests: Telegram allows about one a second per chat
	normalBurst    int           // per key: this many at once ...
	normalRefill   time.Duration // ... then one per this long
	criticalBurst  int
	criticalRefill time.Duration
	normalQueue    int
	criticalQueue  int
	digestWindow   time.Duration // a summary is sent when its first event is this old ...
	digestIdle     time.Duration // ... or when nothing was added to it for this long
	digestGap      time.Duration // and never more often than this
	backoff        []time.Duration
	normalAge      time.Duration // how long a message is tried before it is given up
	criticalAge    time.Duration
	settingsPause  time.Duration // no request for this long after the token or the chat was refused
	maxKeys        int
	maxLabels      int
	// reconcileDelay: the first look at the level of the threads after the
	// start; reconcileEvery: then this often (review 4, (d))
	reconcileDelay time.Duration
	reconcileEvery time.Duration
}

func defaultLimits() limits {
	return limits{
		pace:           1100 * time.Millisecond,
		normalBurst:    3,
		normalRefill:   5 * time.Minute,
		criticalBurst:  2,
		criticalRefill: 2 * time.Minute,
		normalQueue:    64,
		criticalQueue:  32,
		digestWindow:   5 * time.Minute,
		digestIdle:     time.Minute,
		digestGap:      time.Minute,
		backoff:        []time.Duration{5 * time.Second, 15 * time.Second, time.Minute, 5 * time.Minute},
		normalAge:      15 * time.Minute,
		criticalAge:    6 * time.Hour,
		settingsPause:  5 * time.Minute,
		maxKeys:        512,
		maxLabels:      16,
		reconcileDelay: 30 * time.Second,
		reconcileEvery: 10 * time.Minute,
	}
}

// bucket is the budget of one key: it holds at most burst messages and
// gains one per refill.
type bucket struct {
	tokens  float64
	updated time.Time
}

// item is a message that waits to be sent.
type item struct {
	priority priority
	at       time.Time // the age limit counts from here
	// what it stands for, by kind: one event, or for a summary many
	counts map[string]int
	since  time.Time // the first of them
	digest bool

	message    message
	plain      message // the same without values
	triedPlain bool

	// plan 48: how it arrives, and what it opens or closes
	nt notice
	// a retried message waits for its turn again; others pass meanwhile
	attempts  int
	notBefore time.Time
	// thread: the item stands for a thread (threadorder.go): what it sends
	// is decided when it is sent; noButton: sent without the button
	thread   string
	noButton bool
	// pins: the pins and unpins to try again (threads.go)
	pins bool
}

// visitorCaused are the critical kinds a visitor without an account can
// cause (bans, blocks at Cloudflare). They may not take the last
// criticalReserve places of the critical queue.
var visitorCaused = map[string]bool{"shield.ban.temporary": true, "shield.ban.permanent": true, "edge.blocked": true}

const criticalReserve = 8

// Status is what the settings page shows about deliveries.
type Status struct {
	Enabled bool `json:"enabled"`
	// Ready: switched on, with a token and a chat that have the right shape.
	Ready    bool       `json:"ready"`
	LastSent *time.Time `json:"lastSent"`
	// LastError is the last failure in plain words, empty after a success.
	LastError       string     `json:"lastError"`
	LastErrorReason string     `json:"lastErrorReason"`
	LastErrorAt     *time.Time `json:"lastErrorAt"`
	// Waiting: messages queued. Held: events counted for the next summary.
	Waiting int `json:"waiting"`
	Held    int `json:"held"`
	// PausedUntil: no message is sent until then, because Telegram refused
	// the token or the chat.
	PausedUntil *time.Time `json:"pausedUntil"`
	Sent        int64      `json:"sent"`
}

// notifier takes notices, keeps them within their budgets and sends them.
type notifier struct {
	limits   limits
	client   botClient
	settings func() settings
	now      func() time.Time
	// threads remembers the messages that opened something that lasts.
	threads *threadMemory
	// raise tells the administrators in the panel that deliveries fail.
	raise func(reason string)
	// logf writes a line to the server's log.
	logf func(line string)
	// levels says whether threads are open (levels.go); nil: none
	levels levelSource

	mu              sync.Mutex
	buckets         map[string]*bucket // budgets of normal and low messages, by key
	criticalBuckets map[string]*bucket // budgets of critical messages, by key
	minute          []time.Time        // when the last messages under the cap were accepted
	normal          []*item
	critical        []*item
	digest          map[string]int
	digestOther     int
	digestTop       priority
	digestSince     time.Time
	digestLast      time.Time
	lastDigest      time.Time
	lastRequest     time.Time
	holdUntil       time.Time               // no message before then (flood control, back-off)
	entries         map[string]*threadEntry // the threads, see threadorder.go
	removed         map[string]bool         // entries forgotten, to drop from the threads file
	brakes          map[string]brake        // the openings sent lately, per thread
	overflowed      int                     // thread events sent as messages of their own: the table was full
	seq             uint64                  // events counted, for the reconcile
	current         string                  // the thread being sent, for the recovery
	wantsKey        string                  // the settings that decide what is wanted, as last seen
	panics          int                     // the sender's restarts after a panic
	pinsDue         bool                    // pins and unpins refused earlier are to be tried again

	pausedUntil time.Time
	pausedFor   string // the fingerprint of the settings that were refused
	failing     bool   // the administrators were told; reset by a success

	status Status

	wake         chan struct{}
	reconcileNow chan struct{}
	stop         chan struct{}
	done         chan struct{}
}

func newNotifier(client *apiClient, getSettings func() settings) *notifier {
	var bot botClient
	if client != nil {
		bot = client
	}
	return newNotifierWith(bot, getSettings)
}

func newNotifierWith(client botClient, getSettings func() settings) *notifier {
	return &notifier{
		limits:          defaultLimits(),
		client:          client,
		settings:        getSettings,
		now:             time.Now,
		raise:           func(string) {},
		logf:            func(string) {},
		buckets:         map[string]*bucket{},
		criticalBuckets: map[string]*bucket{},
		digest:          map[string]int{},
		entries:         map[string]*threadEntry{},
		removed:         map[string]bool{},
		brakes:          map[string]brake{},
		threads:         newThreadMemory(""),
		reconcileNow:    make(chan struct{}, 1),
		wake:            make(chan struct{}, 1),
		stop:            make(chan struct{}),
		done:            make(chan struct{}),
	}
}

// start runs the goroutines that send and that reconcile.
func (n *notifier) start() {
	n.loadEntries()
	go n.run()
	go n.reconcileLoop()
}

// close ends the goroutine. What waits is dropped.
func (n *notifier) close() {
	select {
	case <-n.stop:
	default:
		close(n.stop)
	}
	<-n.done
}

// Notify takes a notice. It never waits: the notice is queued, counted for
// a summary, or dropped because its category is off, and the call returns.
func (n *notifier) Notify(nt notice) {
	defer func() {
		if r := recover(); r != nil {
			n.logf("Telegram: a notice could not be taken: " + panicText(r))
		}
	}()
	st := n.settings()
	if !st.usable {
		return
	}
	if nt.at.IsZero() {
		nt.at = n.now()
	}
	now := n.now()
	if pairedThread(nt) != "" {
		// a thread's events change its state whether or not their message
		// is wanted: decided when sent (threadorder.go)
		n.mu.Lock()
		taken := n.threadEventLocked(nt, now)
		n.mu.Unlock()
		if taken {
			return
		}
	}
	if !st.wants(nt) {
		return
	}
	// the budgets first (cheap), then the words outside the lock (the guard
	// can take a while), then the queue
	p := nt.priority()
	n.mu.Lock()
	held := false
	switch {
	case n.pausedLocked(st, now):
		// while the token or the chat is refused nothing is queued: it is
		// counted, and told in one message once sending works again
		held = true
	case !n.takeLocked(nt.budgetKey(), p, now):
		held = true
	case p != priorityCritical && !n.underCapLocked(st.maxPerMinute, now):
		held = true
	}
	if held {
		n.holdLocked(nt.kind, 1, p, now)
		n.mu.Unlock()
		return
	}
	n.mu.Unlock()
	rendered := render(st, nt)
	plain := renderWith(st, nt, false)
	plain.button = nil

	n.mu.Lock()
	defer n.mu.Unlock()

	it := &item{priority: p, at: now, since: nt.at, counts: map[string]int{nt.kind: 1}, nt: nt}
	it.message = rendered
	it.plain = plain

	queue, room := &n.normal, n.limits.normalQueue
	if p == priorityCritical {
		queue, room = &n.critical, n.limits.criticalQueue
		if visitorCaused[nt.kind] {
			// what a stranger can cause keeps out of the last places
			count := 0
			for _, waiting := range n.critical {
				if visitorCaused[waiting.nt.kind] {
					count++
				}
			}
			if count >= room-criticalReserve {
				n.holdLocked(nt.kind, 1, p, now)
				return
			}
		}
	}
	if len(*queue) >= room {
		// full: the least severe, oldest message gives way, counted in the
		// summary; a critical one makes room by the oldest a stranger
		// caused; when none can give way, this one is counted
		victim := -1
		for i, waiting := range *queue {
			if waiting.priority < p && (victim < 0 || waiting.priority < (*queue)[victim].priority) {
				victim = i
			}
		}
		if victim < 0 && p == priorityCritical && !visitorCaused[nt.kind] {
			for i, waiting := range *queue {
				if visitorCaused[waiting.nt.kind] {
					victim = i
					break
				}
			}
		}
		if victim < 0 {
			n.holdLocked(nt.kind, 1, p, now)
			return
		}
		dropped := (*queue)[victim]
		*queue = append((*queue)[:victim], (*queue)[victim+1:]...)
		for kind, count := range dropped.counts {
			n.holdLocked(kind, count, dropped.priority, now)
		}
	}
	*queue = append(*queue, it)
	n.signal()
}

func (n *notifier) signal() {
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

// pausedLocked reports whether sending is on hold for these settings. A
// change of the token, the chat or the topic lifts the hold.
func (n *notifier) pausedLocked(st settings, now time.Time) bool {
	if n.pausedUntil.IsZero() {
		return false
	}
	if st.fingerprint != n.pausedFor || !now.Before(n.pausedUntil) {
		n.pausedUntil = time.Time{}
		n.pausedFor = ""
		return false
	}
	return true
}

// takeLocked takes one message out of the budget of a key. Critical
// messages have budgets of their own, kept apart: however many kinds of
// normal messages there are, they cannot crowd a critical kind out.
func (n *notifier) takeLocked(key string, p priority, now time.Time) bool {
	buckets, burst, refill := n.buckets, n.limits.normalBurst, n.limits.normalRefill
	if p == priorityCritical {
		buckets, burst, refill = n.criticalBuckets, n.limits.criticalBurst, n.limits.criticalRefill
	}

	b, known := buckets[key]
	if !known {
		if len(buckets) >= n.limits.maxKeys {
			pruneBuckets(buckets, burst, refill, now)
		}
		if len(buckets) >= n.limits.maxKeys {
			// more keys than there can be: they share one budget
			key = overflowKey
			b, known = buckets[key]
		}
		if !known {
			b = &bucket{tokens: float64(burst), updated: now}
			buckets[key] = b
		}
	}

	if elapsed := now.Sub(b.updated); elapsed > 0 && refill > 0 {
		b.tokens += float64(elapsed) / float64(refill)
		if b.tokens > float64(burst) {
			b.tokens = float64(burst)
		}
	}
	b.updated = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// overflowKey is the budget shared by the keys there is no room for.
const overflowKey = "\x00overflow"

// pruneBuckets forgets the budgets that are full again.
func pruneBuckets(buckets map[string]*bucket, burst int, refill time.Duration, now time.Time) {
	for key, b := range buckets {
		if key != overflowKey && b.tokens+float64(now.Sub(b.updated))/float64(refill) >= float64(burst) {
			delete(buckets, key)
		}
	}
}

// underCapLocked counts a message against the cap per minute.
func (n *notifier) underCapLocked(max int, now time.Time) bool {
	kept := n.minute[:0]
	for _, at := range n.minute {
		if now.Sub(at) < time.Minute {
			kept = append(kept, at)
		}
	}
	n.minute = kept
	if len(n.minute) >= max {
		return false
	}
	n.minute = append(n.minute, now)
	return true
}

// holdLocked counts events that are not sent one by one. They go out
// together, as one summary.
func (n *notifier) holdLocked(kind string, count int, p priority, now time.Time) {
	if count <= 0 {
		return
	}
	if len(n.digest) == 0 && n.digestOther == 0 {
		n.digestSince = now
		n.digestTop = p
	}
	if p > n.digestTop {
		n.digestTop = p
	}
	if _, known := n.digest[kind]; known || len(n.digest) < n.limits.maxLabels {
		n.digest[kind] += count
	} else {
		n.digestOther += count
	}
	n.digestLast = now
	n.signal()
}

// heldLocked is how many events wait for the next summary.
func (n *notifier) heldLocked() int {
	total := n.digestOther
	for _, count := range n.digest {
		total += count
	}
	return total
}

// next gives the message to send now, or how long to wait for one.
func (n *notifier) next() (*item, time.Duration) {
	st := n.settings()
	now := n.now()

	n.mu.Lock()
	defer n.mu.Unlock()

	if !st.usable {
		// switched off: nothing waits for a later that may never come
		n.normal, n.critical = nil, nil
		n.digest, n.digestOther = map[string]int{}, 0
		n.wantsKey = ""
		return nil, time.Hour
	}
	if key := st.wantsFingerprint(); key != n.wantsKey {
		// switched on, or what is wanted changed: every thread is decided
		// again, and the truth is looked at (B2)
		n.allDueLocked()
		if n.wantsKey == "" {
			select {
			case n.reconcileNow <- struct{}{}:
			default:
			}
		}
		n.wantsKey = key
	}
	if n.pausedLocked(st, now) {
		return nil, n.pausedUntil.Sub(now)
	}
	if now.Before(n.holdUntil) {
		// Telegram asked to wait, or could not be reached: everyone waits
		return nil, n.holdUntil.Sub(now)
	}

	wait := time.Hour
	if n.heldLocked() > 0 {
		due := n.digestSince.Add(n.limits.digestWindow)
		if idle := n.digestLast.Add(n.limits.digestIdle); idle.Before(due) {
			due = idle
		}
		if gap := n.lastDigest.Add(n.limits.digestGap); gap.After(due) {
			due = gap
		}
		if !now.Before(due) {
			return n.takeDigestLocked(st, now), 0
		}
		wait = due.Sub(now)
	}

	if n.pinsDue {
		n.pinsDue = false
		return &item{pins: true}, 0
	}
	e, threadWait := n.nextThreadLocked(now)
	if threadWait < wait {
		wait = threadWait
	}
	threadItem := func() (*item, time.Duration) {
		return &item{thread: e.id, priority: e.priority(), at: e.touched}, 0
	}
	if e != nil && e.priority() == priorityCritical {
		if it := n.firstReadyLocked(&n.critical, now, &wait, false); it != nil && !it.at.After(e.touched) {
			return n.firstReadyLocked(&n.critical, now, &wait, true), 0
		}
		return threadItem()
	}
	if it := n.firstReadyLocked(&n.critical, now, &wait, true); it != nil {
		return it, 0
	}
	if e != nil {
		if it := n.firstReadyLocked(&n.normal, now, &wait, false); it != nil && it.at.Before(e.touched) {
			return n.firstReadyLocked(&n.normal, now, &wait, true), 0
		}
		return threadItem()
	}
	if it := n.firstReadyLocked(&n.normal, now, &wait, true); it != nil {
		return it, 0
	}
	return nil, wait
}

// firstReadyLocked is the first item of a queue that may be sent now,
// taken out of it when take.
func (n *notifier) firstReadyLocked(queue *[]*item, now time.Time, wait *time.Duration, take bool) *item {
	for i, it := range *queue {
		if it.notBefore.After(now) {
			if until := it.notBefore.Sub(now); until < *wait {
				*wait = until
			}
			continue
		}
		if take {
			*queue = append((*queue)[:i], (*queue)[i+1:]...)
		}
		return it
	}
	return nil
}

// requeue puts a message that failed for a reason that can pass back in its
// queue, to be tried after wait; the others are tried before it meanwhile.
func (n *notifier) requeue(it *item, wait time.Duration) {
	now := n.now()
	n.mu.Lock()
	defer n.mu.Unlock()
	it.notBefore = now.Add(wait)
	// the next attempt of anything waits at least the shortest back-off
	if hold := now.Add(n.limits.backoff[0]); n.holdUntil.Before(hold) {
		n.holdUntil = hold
	}
	queue, room := &n.normal, n.limits.normalQueue
	if it.priority == priorityCritical {
		queue, room = &n.critical, n.limits.criticalQueue
	}
	if len(*queue) >= room {
		for kind, count := range it.counts {
			n.holdLocked(kind, count, it.priority, now)
		}
		return
	}
	*queue = append(*queue, it)
	n.signal()
}

// takeDigestLocked turns what was counted into one message.
func (n *notifier) takeDigestLocked(st settings, now time.Time) *item {
	counts := n.digest
	if n.digestOther > 0 {
		counts[otherKinds] += n.digestOther
	}
	top := sevInfo
	switch n.digestTop {
	case priorityCritical:
		top = sevCritical
	case priorityNormal:
		top = sevWarn
	}
	for kind := range counts {
		if kt, known := texts.Kinds[kind]; known && kt.severity > top && kt.severity < sevCritical {
			top = kt.severity
		}
	}
	it := &item{priority: n.digestTop, at: now, since: n.digestSince, counts: counts, digest: true}
	it.message = composeDigest(st, counts, n.digestSince, now, top)
	it.plain = it.message
	it.nt = notice{kind: "summary", severity: top}

	n.digest, n.digestOther = map[string]int{}, 0
	n.digestTop = priorityLow
	n.lastDigest = now
	return it
}

// run sends what waits, one message at a time. A panic in the code of a
// message never ends the server (review 4, (b)): it is logged, counted, and
// the loop starts again; a thread that panics maxThreadPanics times is
// dropped.
func (n *notifier) run() {
	defer close(n.done)
	for !n.runLoop() {
	}
}

// runLoop reports true when the notifier was closed.
func (n *notifier) runLoop() (closed bool) {
	defer func() {
		if r := recover(); r != nil {
			n.recovered(r)
			closed = !n.sleep(n.limits.backoff[0])
		}
	}()
	for {
		it, wait := n.next()
		if it == nil {
			timer := time.NewTimer(wait)
			select {
			case <-n.stop:
				timer.Stop()
				return true
			case <-n.wake:
				timer.Stop()
			case <-timer.C:
			}
			continue
		}
		if !n.deliver(it) {
			return true
		}
	}
}

// recovered records a panic of the sender, and settles the thread that was
// being sent.
func (n *notifier) recovered(r interface{}) {
	n.mu.Lock()
	n.panics++
	count := n.panics
	dropped := false
	if e := n.entries[n.current]; e != nil && n.current != "" {
		e.inFlight = false
		e.panics++
		if e.panics >= maxThreadPanics {
			delete(n.entries, e.id)
			n.removed[e.id] = true
			dropped = true
		} else {
			e.queued, e.notBefore = true, n.now().Add(n.limits.backoff[len(n.limits.backoff)-1])
		}
	}
	n.current = ""
	n.mu.Unlock()
	line := "Telegram: the sender stopped on an error and starts again (" + strconv.Itoa(count) + " so far): " + panicText(r)
	if dropped {
		line += "; the problem it was sending is dropped"
	}
	n.logf(line)
}

func panicText(r interface{}) string {
	text := ""
	switch v := r.(type) {
	case error:
		text = v.Error()
	case string:
		text = v
	default:
		text = "unexpected value"
	}
	return cleanValue(text, 200)
}

// sleep waits, and reports false when the notifier is closed meanwhile.
func (n *notifier) sleep(d time.Duration) bool {
	if d <= 0 {
		select {
		case <-n.stop:
			return false
		default:
			return true
		}
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-n.stop:
		return false
	case <-timer.C:
		return true
	}
}

// deliver sends one message, trying again while that can help. It reports
// false when the notifier was closed.
func (n *notifier) deliver(it *item) bool {
	if it.pins {
		if st := n.settings(); st.usable {
			n.tidyPins(st)
		}
		return true
	}
	if it.thread != "" {
		return n.deliverThread(it)
	}
	for attempt := 0; ; attempt++ {
		st := n.settings()
		if !st.usable {
			return true // switched off meanwhile
		}

		n.mu.Lock()
		paced := n.lastRequest.Add(n.limits.pace).Sub(n.now())
		n.mu.Unlock()
		if !n.sleep(paced) {
			return false
		}

		now := n.now()
		silent := n.silent(st, it, now)

		_, failure := n.send(st, outgoing{message: it.message, silent: silent})

		if failure == nil {
			n.succeeded()
			return true
		}

		// the button was refused (the panel's address): once more without it
		if failure.Reason == ReasonBadButton && it.message.button != nil {
			it.message.button = nil
			attempt--
			continue
		}
		// refused for its form: once more, without the values
		if failure.Reason == ReasonBadMessage && !it.triedPlain && !it.digest {
			it.triedPlain = true
			it.message = it.plain
			attempt--
			continue
		}

		if failure.Settings {
			n.failed(failure, st, true)
			n.giveUp(it)
			return true
		}
		if !failure.Retryable {
			n.failed(failure, st, false)
			return true
		}

		wait := failure.RetryAfter
		if wait <= 0 {
			step := it.attempts
			if step >= len(n.limits.backoff) {
				step = len(n.limits.backoff) - 1
			}
			wait = n.limits.backoff[step]
		} else {
			// Telegram's flood control is for the whole bot: everyone waits
			n.mu.Lock()
			if until := n.now().Add(wait); n.holdUntil.Before(until) {
				n.holdUntil = until
			}
			n.mu.Unlock()
		}
		it.attempts++
		age := n.limits.normalAge
		if it.priority == priorityCritical {
			age = n.limits.criticalAge
		}
		n.noteFailure(failure)
		if n.now().Add(wait).Sub(it.at) > age {
			// too old to be worth a message of its own: it is counted, and
			// told in a summary once Telegram can be reached again
			n.failed(failure, st, false)
			n.giveUp(it)
			return true
		}
		n.requeue(it, wait)
		return true
	}
}

// silent decides whether a message arrives without sound: its severity
// says so, or it is the quiet hours (critical ones still ring, unless the
// settings say otherwise).
func (n *notifier) silent(st settings, it *item, now time.Time) bool {
	if st.mode(it.nt) == DeliverSilent {
		return true
	}
	return st.inQuietHours(now.Hour()*60+now.Minute()) && (it.priority != priorityCritical || st.silenceCrit)
}

// pinMessage pins or unpins a message; it reports whether Telegram did.
func (n *notifier) pinMessage(st settings, messageID int, pin bool) bool {
	if !n.paceWait() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout+5*time.Second)
	failure := n.client.pin(ctx, st, messageID, pin)
	cancel()
	n.mu.Lock()
	n.lastRequest = n.now()
	n.mu.Unlock()
	if failure != nil {
		n.logf("Telegram: a message could not be pinned or unpinned, tried again later: " + failure.Message)
		return false
	}
	return true
}

// giveUp moves what a message stood for into the next summary.
func (n *notifier) giveUp(it *item) {
	now := n.now()
	n.mu.Lock()
	defer n.mu.Unlock()
	empty := n.heldLocked() == 0
	for kind, count := range it.counts {
		n.holdLocked(kind, count, it.priority, now)
	}
	if empty || it.since.Before(n.digestSince) {
		n.digestSince = it.since
	}
	// the summary is not due before the window has passed again
	n.digestLast = now
	n.lastDigest = now
}

func (n *notifier) succeeded() {
	now := n.now()
	n.mu.Lock()
	wasFailing := n.failing || n.status.LastError != ""
	n.failing = false
	n.pausedUntil, n.pausedFor = time.Time{}, ""
	n.status.LastSent = &now
	n.status.LastError, n.status.LastErrorReason, n.status.LastErrorAt = "", "", nil
	n.status.Sent++
	n.mu.Unlock()
	if wasFailing {
		n.logf("Telegram: messages are delivered again")
		go resolveInPanel()
	}
}

// noteFailure records a failure that is tried again.
func (n *notifier) noteFailure(failure *sendError) {
	now := n.now()
	n.mu.Lock()
	first := n.status.LastError == ""
	n.status.LastError, n.status.LastErrorReason, n.status.LastErrorAt = failure.Message, failure.Reason, &now
	n.mu.Unlock()
	if first {
		n.logf("Telegram: a message could not be sent and is tried again: " + failure.Message)
	}
}

// failed records a failure that ended a delivery, pauses sending when the
// settings are the cause, and tells the administrators once.
func (n *notifier) failed(failure *sendError, st settings, pause bool) {
	now := n.now()
	n.mu.Lock()
	n.status.LastError, n.status.LastErrorReason, n.status.LastErrorAt = failure.Message, failure.Reason, &now
	if pause {
		n.pausedUntil = now.Add(n.limits.settingsPause)
		n.pausedFor = st.fingerprint
	}
	tell := !n.failing
	n.failing = true
	n.mu.Unlock()

	if tell {
		n.logf("Telegram: a message was not delivered: " + failure.Message)
		n.raise(failure.Message)
	}
}

// delivered records a message that was sent outside the queue with the
// settings in use (the test message): they were just shown to work, so a
// pause is lifted and the last problem is over.
func (n *notifier) delivered() {
	n.succeeded()
	n.signal()
}

// Status reports the state of deliveries.
func (n *notifier) Status() Status {
	st := n.settings()
	now := n.now()

	n.mu.Lock()
	defer n.mu.Unlock()
	status := n.status
	status.Enabled = st.enabled
	status.Ready = st.usable
	status.Waiting = len(n.normal) + len(n.critical)
	for _, e := range n.entries {
		if e.queued || e.inFlight {
			status.Waiting++
		}
	}
	status.Held = n.heldLocked()
	if n.pausedLocked(st, now) {
		until := n.pausedUntil
		status.PausedUntil = &until
	}
	return status
}
