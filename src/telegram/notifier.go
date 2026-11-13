package telegram

import (
	"context"
	"sort"
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

// notice is one thing to tell the administrator, before it is put into
// words for the settings in use.
type notice struct {
	category string
	// key is what the budget is kept per: a kind of security event, a
	// kind of notification. It comes from a bounded set, never from input.
	key string
	// label is what a summary calls this, in fixed words.
	label    string
	priority priority
	at       time.Time
	// title is the first line of the body, in fixed words.
	title string
	// details writes what is known about this one event: who, where,
	// what. It is left out when the settings say so. May be nil.
	details func(c *composer)
}

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
	// what it stands for, by label: one event, or for a summary many
	counts map[string]int
	since  time.Time // the first of them
	digest bool

	text          string
	entities      []entity
	plainText     string // the same without values
	plainEntities []entity
	triedPlain    bool
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
	// raise tells the administrators in the panel that deliveries fail.
	raise func(reason string)
	// logf writes a line to the server's log.
	logf func(line string)

	mu          sync.Mutex
	buckets     map[string]*bucket
	minute      []time.Time // when the last messages under the cap were accepted
	normal      []*item
	critical    []*item
	digest      map[string]int
	digestOther int
	digestTop   priority
	digestSince time.Time
	digestLast  time.Time
	lastDigest  time.Time
	lastRequest time.Time

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
		limits:   defaultLimits(),
		client:   client,
		settings: getSettings,
		now:      time.Now,
		raise:    func(string) {},
		logf:     func(string) {},
		buckets:  map[string]*bucket{},
		digest:   map[string]int{},
		wake:     make(chan struct{}, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
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
	if !st.usable || !st.categories[nt.category] {
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
	if n.pausedLocked(st, now) {
		n.holdLocked(nt.label, 1, nt.priority, now)
		return
	}
	if !n.takeLocked(nt.key, nt.priority, now) {
		n.holdLocked(nt.label, 1, nt.priority, now)
		return
	}
	if nt.priority != priorityCritical && !n.underCapLocked(st.maxPerMinute, now) {
		n.holdLocked(nt.label, 1, nt.priority, now)
		return
	}

	it := &item{priority: nt.priority, at: now, since: nt.at, counts: map[string]int{nt.label: 1}}
	it.text, it.entities = composeNotice(st, nt, !st.hideDetails)
	it.plainText, it.plainEntities = composeNotice(st, nt, false)

	queue, room := &n.normal, n.limits.normalQueue
	if nt.priority == priorityCritical {
		queue, room = &n.critical, n.limits.criticalQueue
	}
	if len(*queue) >= room {
		n.holdLocked(nt.label, 1, nt.priority, now)
		return
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

// takeLocked takes one message out of the budget of a key.
func (n *notifier) takeLocked(key string, p priority, now time.Time) bool {
	burst, refill := n.limits.normalBurst, n.limits.normalRefill
	if p == priorityCritical {
		burst, refill = n.limits.criticalBurst, n.limits.criticalRefill
		key = "!" + key
	}

	b, known := n.buckets[key]
	if !known {
		if len(n.buckets) >= n.limits.maxKeys {
			n.pruneLocked(now)
		}
		if len(n.buckets) >= n.limits.maxKeys {
			// more keys than there can be: they share one budget
			key = "\x00overflow"
			if p == priorityCritical {
				key = "!" + key
			}
			b, known = n.buckets[key]
		}
		if !known {
			b = &bucket{tokens: float64(burst), updated: now}
			n.buckets[key] = b
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

// pruneLocked forgets the budgets that are full again.
func (n *notifier) pruneLocked(now time.Time) {
	for key, b := range n.buckets {
		refill, burst := n.limits.normalRefill, n.limits.normalBurst
		if len(key) > 0 && key[0] == '!' {
			refill, burst = n.limits.criticalRefill, n.limits.criticalBurst
		}
		if b.tokens+float64(now.Sub(b.updated))/float64(refill) >= float64(burst) {
			delete(n.buckets, key)
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
func (n *notifier) holdLocked(label string, count int, p priority, now time.Time) {
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
	if _, known := n.digest[label]; known || len(n.digest) < n.limits.maxLabels {
		n.digest[label] += count
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
		counts[otherLabel] += n.digestOther
	}
	it := &item{priority: n.digestTop, at: now, since: n.digestSince, counts: counts, digest: true}
	it.text, it.entities = composeDigest(st, counts, n.digestSince, now, n.digestTop)
	it.plainText, it.plainEntities = it.text, it.entities

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
		silent := it.priority == priorityLow ||
			(st.inQuietHours(now.Hour()*60+now.Minute()) && (it.priority != priorityCritical || st.silenceCrit))

		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout+5*time.Second)
		go func() {
			select {
			case <-n.stop:
				cancel()
			case <-ctx.Done():
			}
		}()
		failure := n.client.send(ctx, st, it.text, it.entities, silent)
		cancel()

		n.mu.Lock()
		n.lastRequest = n.now()
		n.mu.Unlock()

		if failure == nil {
			n.succeeded()
			return true
		}

		// refused for its form: once more, without the values
		if failure.Reason == ReasonBadMessage && !it.triedPlain && !it.digest {
			it.triedPlain = true
			it.text, it.entities = it.plainText, it.plainEntities
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

// giveUp moves what a message stood for into the next summary.
func (n *notifier) giveUp(it *item) {
	now := n.now()
	n.mu.Lock()
	defer n.mu.Unlock()
	empty := n.heldLocked() == 0
	for label, count := range it.counts {
		n.holdLocked(label, count, it.priority, now)
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

// resume lifts a pause: the settings were just shown to work.
func (n *notifier) resume() {
	n.mu.Lock()
	n.pausedUntil, n.pausedFor = time.Time{}, ""
	n.mu.Unlock()
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

// otherLabel stands for the kinds a summary has no room to name.
const otherLabel = "other events"

var categoryTitles = map[string]string{
	CategorySecurity: "Security",
	CategoryLogins:   "Login",
	CategorySystem:   "System",
	CategoryStorage:  "Storage",
	CategoryApps:     "Apps",
	CategoryOther:    "Notice",
}

const stampLayout = "2006-01-02 15:04:05 MST"

// header writes the first line: where the message comes from.
func header(c *composer, st settings, what string) {
	c.fixed("Cosmos · ").value(st.serverName, maxServerName).fixed(" · " + what).line()
}

// composeNotice puts a notice into words.
func composeNotice(st settings, nt notice, details bool) (string, []entity) {
	c := &composer{}
	what := categoryTitles[nt.category]
	if what == "" {
		what = categoryTitles[CategoryOther]
	}
	if nt.priority == priorityCritical {
		what += " alert"
	}
	header(c, st, what)
	c.fixed(nt.title).line()
	if details && nt.details != nil {
		before := c.length
		nt.details(c)
		if c.length > before {
			c.line()
		}
	} else if nt.details != nil {
		c.fixed("The details are in the panel.").line()
	}
	c.fixed(nt.at.Local().Format(stampLayout))
	return c.result()
}

// composeDigest writes the summary of what was not sent one by one. It
// holds fixed words and numbers only.
func composeDigest(st settings, counts map[string]int, since time.Time, now time.Time, top priority) (string, []entity) {
	labels := make([]string, 0, len(counts))
	total := 0
	for label, count := range counts {
		labels = append(labels, label)
		total += count
	}
	sort.Slice(labels, func(i, j int) bool {
		if (labels[i] == otherLabel) != (labels[j] == otherLabel) {
			return labels[j] == otherLabel
		}
		if counts[labels[i]] != counts[labels[j]] {
			return counts[labels[i]] > counts[labels[j]]
		}
		return labels[i] < labels[j]
	})

	c := &composer{}
	what := "Summary"
	if top == priorityCritical {
		what = "Summary with alerts"
	}
	header(c, st, what)

	events := " more events"
	if total == 1 {
		events = " more event"
	}
	c.fixed(strconv.Itoa(total) + events + " " + spanWords(now.Sub(since)) + ", not sent one by one:").line()
	for _, label := range labels {
		c.fixed(strconv.Itoa(counts[label]) + " × " + label).line()
	}
	c.fixed("The details are in the panel.").line()
	c.fixed(now.Local().Format(stampLayout))
	return c.result()
}

// spanWords says how long ago the first event of a summary was.
func spanWords(span time.Duration) string {
	minutes := int((span + 30*time.Second) / time.Minute)
	switch {
	case minutes <= 1:
		return "in the last minute"
	case minutes < 120:
		return "in the last " + strconv.Itoa(minutes) + " minutes"
	default:
		return "in the last " + strconv.Itoa((minutes+30)/60) + " hours"
	}
}
