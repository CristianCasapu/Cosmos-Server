package telegram

import (
	"context"
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
}

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
	client   *apiClient
	settings func() settings
	now      func() time.Time
	// threads remembers the messages that opened something that lasts.
	threads *threadMemory
	// raise tells the administrators in the panel that deliveries fail.
	raise func(reason string)
	// logf writes a line to the server's log.
	logf func(line string)

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

	pausedUntil time.Time
	pausedFor   string // the fingerprint of the settings that were refused
	failing     bool   // the administrators were told; reset by a success

	status Status

	wake chan struct{}
	stop chan struct{}
	done chan struct{}
}

func newNotifier(client *apiClient, getSettings func() settings) *notifier {
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
		wake:            make(chan struct{}, 1),
		stop:            make(chan struct{}),
		done:            make(chan struct{}),
	}
}

// start runs the goroutine that sends.
func (n *notifier) start() {
	go n.run()
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
	st := n.settings()
	if !st.usable || !st.wants(nt) {
		return
	}
	if nt.at.IsZero() {
		nt.at = n.now()
	}
	now := n.now()

	n.mu.Lock()
	defer n.mu.Unlock()

	// while the token or the chat is refused nothing is queued: it is
	// counted, and told in one message once sending works again
	p := nt.priority()
	if n.pausedLocked(st, now) {
		n.holdLocked(nt.kind, 1, p, now)
		return
	}
	// a closing message is not held back: it ends what was told
	if !(nt.closing && nt.resolves != "") && !n.takeLocked(nt.budgetKey(), p, now) {
		n.holdLocked(nt.kind, 1, p, now)
		return
	}
	if p != priorityCritical && !n.underCapLocked(st.maxPerMinute, now) {
		n.holdLocked(nt.kind, 1, p, now)
		return
	}

	it := &item{priority: p, at: now, since: nt.at, counts: map[string]int{nt.kind: 1}, nt: nt}
	it.message = render(st, nt)
	plain := renderWith(st, nt, false)
	plain.button = nil
	it.plain = plain

	queue, room := &n.normal, n.limits.normalQueue
	if p == priorityCritical {
		queue, room = &n.critical, n.limits.criticalQueue
	}
	if len(*queue) >= room {
		// full: the least severe, oldest message gives way, counted in the
		// summary; when none is less severe, this one is counted
		victim := -1
		for i, waiting := range *queue {
			if waiting.priority < p && (victim < 0 || waiting.priority < (*queue)[victim].priority) {
				victim = i
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
		return nil, time.Hour
	}
	if n.pausedLocked(st, now) {
		return nil, n.pausedUntil.Sub(now)
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

	if len(n.critical) > 0 {
		it := n.critical[0]
		n.critical = n.critical[1:]
		return it, 0
	}
	if len(n.normal) > 0 {
		it := n.normal[0]
		n.normal = n.normal[1:]
		return it, 0
	}
	return nil, wait
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

// run sends what waits, one message at a time.
func (n *notifier) run() {
	defer close(n.done)
	for {
		it, wait := n.next()
		if it == nil {
			timer := time.NewTimer(wait)
			select {
			case <-n.stop:
				timer.Stop()
				return
			case <-n.wake:
				timer.Stop()
			case <-timer.C:
			}
			continue
		}
		if !n.deliver(it) {
			return
		}
	}
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
		replyTo := n.replyTo(st, it)

		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout+5*time.Second)
		go func() {
			select {
			case <-n.stop:
				cancel()
			case <-ctx.Done():
			}
		}()
		messageID, failure := n.client.sendMessage(ctx, st, outgoing{message: it.message, silent: silent, replyTo: replyTo})
		cancel()

		n.mu.Lock()
		n.lastRequest = n.now()
		n.mu.Unlock()

		if failure == nil {
			n.succeeded()
			n.afterSent(st, it, messageID)
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
			step := attempt
			if step >= len(n.limits.backoff) {
				step = len(n.limits.backoff) - 1
			}
			wait = n.limits.backoff[step]
		}
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
		if !n.sleep(wait) {
			return false
		}
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

// replyTo is the message a closing message or a repeat answers.
func (n *notifier) replyTo(st settings, it *item) int {
	thread := it.nt.resolves
	if thread == "" && it.nt.repeat {
		thread = it.nt.thread
	}
	if thread == "" || n.threads == nil {
		return 0
	}
	if open, ok := n.threads.get(st, thread); ok {
		return open.MessageID
	}
	return 0
}

// afterSent remembers the opening of something that lasts, pins a critical
// one, and for a closing message unpins and forgets its opening. Pins are
// a help: a failure is not retried.
func (n *notifier) afterSent(st settings, it *item, messageID int) {
	if n.threads == nil {
		return
	}
	nt := it.nt
	unpin := []int{}
	switch {
	case nt.resolves != "":
		if open, ok := n.threads.close(st, nt.resolves); ok && open.Pinned {
			unpin = append(unpin, open.MessageID)
		}
	case nt.thread != "" && messageID > 0 && !nt.repeat:
		pin := nt.texts().Pin && nt.severity == sevCritical
		unpin = n.threads.open(st, nt.thread, messageID, pin)
		if pin {
			n.pinMessage(st, messageID, true)
		}
	}
	for _, id := range unpin {
		n.pinMessage(st, id, false)
	}
}

func (n *notifier) pinMessage(st settings, messageID int, pin bool) {
	n.mu.Lock()
	paced := n.lastRequest.Add(n.limits.pace).Sub(n.now())
	n.mu.Unlock()
	if !n.sleep(paced) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout+5*time.Second)
	failure := n.client.pin(ctx, st, messageID, pin)
	cancel()
	n.mu.Lock()
	n.lastRequest = n.now()
	n.mu.Unlock()
	if failure != nil {
		n.logf("Telegram: a message could not be pinned or unpinned: " + failure.Message)
	}
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
	status.Held = n.heldLocked()
	if n.pausedLocked(st, now) {
		until := n.pausedUntil
		status.PausedUntil = &until
	}
	return status
}

