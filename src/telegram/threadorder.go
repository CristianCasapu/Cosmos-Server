package telegram

// Fork addition (plan 48, reviews T1 and 4): the messages of a thread are
// delivered in order, and what is delivered is decided when it is sent,
// from the thread's truth, never from what was queued.
//
// A thread is a problem of a DECLARED PAIR only (review 4, (a)): an opening
// kind of the table with its closing kinds ("closes" in messages.en.json;
// PairedKinds lists them). Every other notification is a message of its
// own, through the plain queue, with the budgets, the cap and the critical
// reserve, exactly as before plan 48: nothing can be swallowed as "already
// told".
//
// Per thread the notifier keeps one entry, known by a hash of the thread's
// name (threadID): the truth (open or closed), the notices that describe
// it, and whether it is due or in flight. Entries never live in the plain
// queues, so nothing there can push them out (review 4, B1). At most one
// send per thread is on the wire; other threads are not held up by it.
//
// "told" is whether the chat holds an opening of this thread that was not
// closed (threads.go; it survives a restart).
//
// The state machine, decided at send time (decideLocked):
//
//	truth   told   other                → action
//	open    no     an opening is known  → OPEN: send the opening (pinned when its kind pins
//	                                      and it is critical); told = yes. Not wanted by
//	                                      the settings now: nothing, decided again when
//	                                      they change (B2)
//	open    yes    a fresh opening      → OPEN: only from a threads file of an earlier
//	                                      version (review 6, D1: a tell while the entry is
//	                                      open is a repeat; a new opening needs an end in
//	                                      between that was acted on)
//	open    yes    a repeat waits       → REPEAT: the repeat (or a further tell) as a reply
//	open    yes    -                    → nothing
//	closed  yes    -                    → CLOSE: unpin and forget the opening ALWAYS; send
//	                                      the closing as a reply (with no closing known:
//	                                      "no longer followed"), unless its kind was
//	                                      switched off by itself (A3: otherwise a closing
//	                                      arrives, at least silently, when its opening did)
//	closed  no     opening and closing  → MERGED: "it happened and is over (was: …)",
//	                                      delivered as the opening would have been; no
//	                                      reply, no pin
//	closed  no     -                    → nothing; the entry is forgotten
//
// Events (threadEventLocked): an opening or a repeat makes the truth open
// (a repeat on a thread the memory thinks closed means it is open: P1); a
// closing makes it closed. After every send the entry is decided again.
//
// The truth is also LEVEL (review 4, (d), levels.go): at start and every
// reconcileEvery the notifier asks the sources whether each thread it
// knows of, or that the notifications hold open, is open, and sets the
// entries right: a lost event, a restart, a lost threads file, a setting
// switched off and on, an end the source made quietly, all heal within one
// period.
//
// Second line (P3): an opening of the same thread is never sent twice
// within brakeWindow.

import (
	"context"
	"sort"
	"strconv"
	"time"
)

const (
	maxThreadEntries = 256
	brakeWindow      = time.Minute
	maxThreadPanics  = 3
)

type threadAction int

const (
	actionNone threadAction = iota
	actionOpen
	actionRepeat
	actionClose
	actionMerged
)

func (a threadAction) String() string {
	return [...]string{"none", "open", "repeat", "close", "merged"}[a]
}

type threadEntry struct {
	id, name string
	kind     string // the opening kind of the pair
	open     bool
	since    time.Time
	// seenOpen: the last moment the thread was seen open (its opening or a
	// repeat, or a reconcile that read it open, as of the start of its
	// reads): an end is believed only on evidence newer than it (review 6,
	// T1: an earlier episode's end is not this one's)
	seenOpen time.Time
	episode  int
	// fresh: an opening came while the chat held an older one
	fresh bool
	// byLevel: the reconcile opened this episode from the level, before
	// the source's own event came (a source changes its state before it
	// writes): that event is this episode's, not a new one
	byLevel bool
	opening *notice // the opening of the current (or last) episode
	closing *notice // the closing of the last episode
	repeat  *notice // a repeat waiting to be sent
	// queued: to be decided; inFlight: a send is on the wire
	queued    bool
	inFlight  bool
	notBefore time.Time
	attempts  int
	noButton  bool
	plain     bool
	panics    int
	seq       uint64 // the last event that changed it
	touched   time.Time
	dirty     bool // its state is to be written to the threads file
}

type brake struct {
	at        time.Time
	messageID int
}

// brakeKey: a brake is of a thread IN A CHAT (review 5, S3): a message
// number is never given to another chat.
func brakeKey(st settings, id string) string { return chatKey(st) + ":" + id }

// pairedThread is the name of the thread a notice belongs to, "" when its
// kind is not of a declared pair (a message of its own).
func pairedThread(nt notice) string {
	if nt.resolves != "" {
		if nt.texts().Closes != "" {
			return nt.resolves
		}
		return ""
	}
	if nt.thread != "" && pairedOpening(nt.kind) {
		return nt.thread
	}
	return ""
}

// threadEventLocked records an event of a thread and makes it due. It
// reports false when the table is full of open threads: the notice is then
// a message of its own (counted).
func (n *notifier) threadEventLocked(st settings, nt notice, now time.Time) bool {
	name := pairedThread(nt)
	id := threadID(name)
	if n.threads.isPlain(id) {
		// its opening went as a message of its own: so do its repeats. Its
		// end is not told (review 7, N8): messages of their own go under
		// the cap and the summaries, so an "OK" could arrive after, or
		// instead of, a reopening told meanwhile; beyond the table the
		// chat gets silence rather than a false "OK". A later opening is
		// told again, as a message of its own.
		n.notePlainLocked(id)
		if nt.resolves != "" {
			n.threads.markPlain(id, false)
			delete(n.plainSent, brakeKey(st, id))
			return true
		}
		return false
	}
	e := n.entries[id]
	if e == nil {
		_, told := n.threads.get(st, id)
		if nt.resolves != "" && !told && len(n.entries) >= maxThreadEntries && !n.evictLocked() {
			// review 7, N8: the end of a problem the chat was not told, the
			// table full: nothing to answer, and never an "OK" of its own
			n.notePlainLocked(id)
			return true
		}
		// the end of a problem the chat holds always gets its entry (as in
		// the reconcile: bounded by the chat's told threads)
		if !(nt.resolves != "" && told) && len(n.entries) >= maxThreadEntries && !n.evictLocked() {
			if nt.resolves == "" && n.threads.markPlain(id, true) {
				n.plainSent[brakeKey(st, id)] = true
			}
			n.notePlainLocked(id)
			n.overflowed++
			if n.overflowed == 1 || n.overflowed%100 == 0 {
				n.logf("Telegram: more than " + strconv.Itoa(maxThreadEntries) + " problems are open at once; the newest are sent as messages of their own (" + strconv.Itoa(n.overflowed) + " so far)")
			}
			return false
		}
		e = &threadEntry{id: id, name: name}
		n.entries[id] = e
	}
	n.seq++
	e.seq, e.touched = n.seq, now
	if kind := nt.texts().Closes; kind != "" {
		e.kind = kind
	} else {
		e.kind = nt.kind
	}
	c := nt
	byLevel := e.byLevel
	e.byLevel = false
	e.plain = false // new words: the full form is tried again (review 6)
	if nt.resolves == "" && nt.at.After(e.seenOpen) {
		e.seenOpen = nt.at
	}
	switch {
	case nt.resolves != "":
		if e.open {
			e.open, e.since = false, nt.at
		}
		e.closing, e.repeat, e.fresh = &c, nil, false
	case !e.open:
		// a new episode; a repeat says the problem is open too (P1)
		c.repeat = false
		e.open, e.since, e.seenOpen = true, nt.at, nt.at
		e.episode++
		e.opening, e.closing, e.repeat, e.fresh = &c, nil, nil, false
		if nt.repeat {
			r := nt
			e.repeat = &r
		}
	case nt.repeat:
		r := nt
		e.repeat = &r
		if e.opening == nil {
			o := nt
			o.repeat = false
			e.opening = &o
		}
	case byLevel:
		// the event of the episode the level opened already: its words,
		// no second opening
		e.opening = &c
	default:
		// review 6, D1: a further tell while the entry is open (the store
		// groups only within a day; a source in memory tells again after a
		// restart) is never a new opening: an end must come in between,
		// and be acted on. It is a repeat.
		r := nt
		r.repeat = true
		e.repeat = &r
		if e.opening == nil {
			o := nt
			e.opening = &o
		}
	}
	e.dirty = true
	n.dueLocked(e)
	return true
}

// notePlainLocked remembers that a thread went as messages of its own.
func (n *notifier) notePlainLocked(id string) {
	if len(n.plainEver) < 4*maxThreadEntries {
		n.plainEver[id] = true
	}
	// review 7, N8: the seq of a thread beyond the table, for the
	// reconcile's guard (an event newer than its read wins); bounded
	if n.plainSeq == nil || len(n.plainSeq) >= 4*maxThreadEntries {
		n.plainSeq = map[string]uint64{}
	}
	n.seq++
	n.plainSeq[id] = n.seq
}

// evictLocked makes room: the oldest closed entry that has nothing to do.
// An open entry is never pushed out.
func (n *notifier) evictLocked() bool {
	var victim *threadEntry
	for _, e := range n.entries {
		if e.open || e.inFlight || e.queued {
			continue
		}
		if victim == nil || e.touched.Before(victim.touched) || (e.touched.Equal(victim.touched) && e.id < victim.id) {
			victim = e
		}
	}
	if victim == nil {
		return false
	}
	delete(n.entries, victim.id)
	n.removed[victim.id] = true
	return true
}

// dueLocked makes an entry to be decided by the sender.
func (n *notifier) dueLocked(e *threadEntry) {
	e.queued = true
	n.signal()
}

// allDueLocked: the settings changed, or the truth was set again.
func (n *notifier) allDueLocked() {
	for _, e := range n.entries {
		e.queued = true
	}
	n.signal()
}

func (e *threadEntry) priority() priority {
	if e.open && e.opening != nil {
		return e.opening.priority()
	}
	return priorityLow
}

// nextThreadLocked is the entry to decide now, and how long until the next
// one otherwise.
func (n *notifier) nextThreadLocked(now time.Time) (*threadEntry, time.Duration) {
	var best *threadEntry
	wait := time.Hour
	if now.Before(n.levelWait) {
		return nil, n.levelWait.Sub(now)
	}
	for _, e := range n.entries {
		if !e.queued || e.inFlight {
			continue
		}
		if e.notBefore.After(now) {
			if d := e.notBefore.Sub(now); d < wait {
				wait = d
			}
			continue
		}
		if best == nil || e.priority() > best.priority() ||
			(e.priority() == best.priority() && (e.touched.Before(best.touched) || (e.touched.Equal(best.touched) && e.id < best.id))) {
			best = e
		}
	}
	return best, wait
}

// closingWanted: the closing of a told opening is sent unless its kind was
// switched off by itself (A3). wanted: by the settings as they are (else it
// arrives silently, inherited from its opening).
func closingWanted(st settings, closing notice) (send bool, wanted bool) {
	if on, set := st.kinds[closing.kind]; set && !on {
		return false, false
	}
	return true, st.wants(closing)
}

// endedNotice is the closing of a thread whose end the source made quietly.
func endedNotice(kind string, name string, at time.Time) notice {
	label := texts.Kinds[kind].Label
	if label == "" {
		label = "Notification"
	}
	nt := newNotice("thread.ended", map[string]string{"_label": label})
	if kt, ok := texts.Kinds[kind]; ok {
		nt.category = kt.Category
	}
	nt.at, nt.resolves, nt.key = at, name, "n:thread.ended"
	return nt
}

// decision is what the sender does for a thread now.
type decision struct {
	action  threadAction
	subject notice
	silent  bool // the closing arrives silently (inherited)
	send    bool // a message is sent (CLOSE: else only unpin and forget)
	replyTo int
	wait    time.Duration // the brake: decided again after this (<0: at once)
	episode int
	repeat  *notice
	opening *notice
}

// decideLocked is the state machine above.
func (n *notifier) decideLocked(st settings, e *threadEntry, told openThread, isTold bool, now time.Time) decision {
	d := decision{episode: e.episode, repeat: e.repeat, opening: e.opening}
	switch {
	case e.open && e.opening != nil && (!isTold || e.fresh || told.Uncertain):
		if !st.wants(*e.opening) {
			return decision{} // decided again when the settings change
		}
		if b, braked := n.brakes[brakeKey(st, e.id)]; braked && now.Sub(b.at) < brakeWindow {
			if b.messageID > 0 && !e.fresh && !isTold {
				// sent a moment ago: the chat holds it
				n.threads.open(st, e.id, e.name, e.opening.kind, b.messageID, e.opening.texts().Pin && e.opening.severity == sevCritical)
				return decision{wait: -1}
			}
			if !isTold || b.messageID != told.MessageID || told.Uncertain {
				return decision{wait: brakeWindow - now.Sub(b.at)}
			}
			e.fresh = false
			return decision{wait: -1}
		}
		d.action, d.subject, d.send = actionOpen, *e.opening, true
	case e.open && isTold && e.repeat != nil:
		if !st.wants(*e.repeat) {
			e.repeat = nil
			return decision{}
		}
		d.action, d.subject, d.send, d.replyTo = actionRepeat, *e.repeat, true, told.MessageID
	case !e.open && isTold:
		closing := endedNotice(told.Kind, e.name, now)
		if e.closing != nil {
			closing = *e.closing
		}
		send, wanted := closingWanted(st, closing)
		if closing.kind == "thread.ended" {
			// review 6: an end without its closing obeys the pair's closing
			// kinds switched off by themselves too (the card says so: the
			// opening stays the chat's last word, unpinned)
			for _, kind := range closingsOf(told.Kind) {
				if on, set := st.kinds[kind]; set && !on {
					send = false
				}
			}
		}
		d.action, d.subject, d.send, d.silent, d.replyTo = actionClose, closing, send, !wanted, told.MessageID
	case !e.open && !isTold && e.opening != nil && e.closing != nil:
		subject := *e.closing
		subject.mergedLabel, subject.mergedSince = e.opening.label(), e.opening.at
		subject.mergedSeverity = e.opening.severity
		if !st.wants(*e.opening) {
			e.opening, e.closing, e.repeat = nil, nil, nil
			return decision{}
		}
		d.action, d.subject, d.send = actionMerged, subject, true
	}
	return d
}

// saveDirtyLocked collects the states to write (B3): written before a send
// is attempted.
func (n *notifier) saveDirtyLocked() map[string]*savedEntry {
	out := map[string]*savedEntry{}
	for id, e := range n.entries {
		if !e.dirty {
			continue
		}
		e.dirty = false
		out[id] = &savedEntry{Name: boundName(e.name), Kind: e.kind, Open: e.open, Since: e.since, SeenOpen: e.seenOpen, Fresh: e.fresh,
			Opening: saveNotice(e.opening), Closing: saveNotice(e.closing)}
	}
	for id := range n.removed {
		if _, back := n.entries[id]; !back {
			out[id] = nil
		}
	}
	n.removed = map[string]bool{}
	return out
}

// persist writes the states of the threads that changed (B3); the sender
// does it as soon as it wakes, before anything else.
func (n *notifier) persist() {
	n.mu.Lock()
	saves := n.saveDirtyLocked()
	n.mu.Unlock()
	if len(saves) > 0 {
		n.threads.saveEntries(saves)
	}
}

// deliverThread does what the thread's truth asks for now.
func (n *notifier) deliverThread(it *item) bool {
	for {
		st := n.settings()
		if !st.usable {
			return true
		}
		now := n.now()
		var (
			e     *threadEntry
			d     decision
			saves map[string]*savedEntry
		)
		func() {
			n.mu.Lock()
			defer n.mu.Unlock()
			e = n.entries[it.thread]
			if e == nil || e.inFlight {
				e = nil
				return
			}
			n.current = e.id
			e.queued = false
			told, isTold := n.threads.get(st, e.id)
			d = n.decideLocked(st, e, told, isTold, now)
			if n.traceDecision != nil {
				n.traceDecision(e.id, d.action.String(), isTold, e.open)
			}
			switch {
			case d.wait < 0:
				e.queued = true // decided again at once
			case d.wait > 0:
				e.queued, e.notBefore = true, now.Add(d.wait)
			case d.action == actionNone:
				if !e.open {
					delete(n.entries, e.id)
					n.removed[e.id] = true
				} else {
					e.dirty = true
				}
			default:
				e.inFlight = true
			}
			saves = n.saveDirtyLocked()
		}()
		n.threads.saveEntries(saves)
		if e == nil || d.action == actionNone {
			if e != nil && d.wait < 0 {
				it = &item{thread: e.id}
				continue
			}
			return true
		}

		messageID := 0
		var failure *sendError
		if d.send {
			if !n.paceWait() {
				n.idle(e, 0)
				return false
			}
			m := render(st, d.subject)
			if e.plain {
				m = renderWith(st, d.subject, false)
				m.button = nil
			}
			if e.noButton {
				m.button = nil
			}
			out := outgoing{message: m, silent: d.silent || n.silentFor(st, d.subject, d.action), replyTo: d.replyTo,
				trace: e.id + " " + d.action.String()}
			messageID, failure = n.send(st, out)
		}

		if failure != nil {
			if failure.Reason == ReasonBadButton && !e.noButton {
				e.noButton = true
				n.idle(e, 0)
				return true
			}
			if failure.Reason == ReasonBadMessage && !e.plain {
				e.plain = true
				n.idle(e, 0)
				return true
			}
			if failure.Reason == ReasonTimeout {
				if d.action == actionOpen {
					// it may have arrived: the same opening waits for the brake
					n.mu.Lock()
					n.brakes[brakeKey(st, e.id)] = brake{at: n.now()}
					n.mu.Unlock()
				}
				if d.action == actionClose {
					// review 5, S2: this chat may show it over: a re-opening
					// before the retry is sent as a new opening
					n.threads.uncertain(st, e.id)
				}
			}
			if failure.Settings || !failure.Retryable {
				n.failed(failure, st, failure.Settings)
				n.idle(e, n.limits.backoff[len(n.limits.backoff)-1])
				return true
			}
			wait := failure.RetryAfter
			if wait <= 0 {
				step := e.attempts
				if step >= len(n.limits.backoff) {
					step = len(n.limits.backoff) - 1
				}
				wait = n.limits.backoff[step]
			} else {
				n.mu.Lock()
				if until := n.now().Add(wait); n.holdUntil.Before(until) {
					n.holdUntil = until
				}
				n.mu.Unlock()
			}
			e.attempts++
			n.noteFailure(failure)
			n.idle(e, wait)
			return true
		}
		if d.send {
			n.succeeded()
		}

		// the state of the chat changes, message or not
		switch d.action {
		case actionOpen:
			pin := d.subject.texts().Pin && d.subject.severity == sevCritical
			if messageID > 0 {
				n.mu.Lock()
				n.pruneBrakesLocked()
				n.brakes[brakeKey(st, e.id)] = brake{at: n.now(), messageID: messageID}
				n.mu.Unlock()
				n.threads.open(st, e.id, e.name, d.subject.kind, messageID, pin)
			}
		case actionClose:
			n.threads.close(st, e.id)
			// the opening it closed is no longer the one a new opening repeats
			n.mu.Lock()
			delete(n.brakes, brakeKey(st, e.id))
			n.mu.Unlock()
		}
		n.tidyPins(st)

		func() {
			n.mu.Lock()
			defer n.mu.Unlock()
			e.inFlight, e.attempts, e.noButton, e.plain, e.panics = false, 0, false, false, 0
			switch d.action {
			case actionOpen:
				if e.opening == d.opening {
					e.fresh = false
				}
			case actionRepeat:
				if e.repeat == d.repeat {
					e.repeat = nil
				}
			case actionClose, actionMerged:
				// A1: only what this decision closed is forgotten; an opening
				// that came during the send stays, and is decided next
				if e.episode == d.episode && !e.open {
					e.opening, e.closing, e.repeat = nil, nil, nil
				}
			}
			e.dirty = true
			if n.entries[e.id] == e {
				n.dueLocked(e)
			}
		}()
		// what was sent is written at once: a restart does not send it again
		n.persist()
		return true
	}
}

// idle ends a send of a thread: decided again after wait.
func (n *notifier) idle(e *threadEntry, wait time.Duration) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e.inFlight = false
	if n.entries[e.id] != e {
		return
	}
	e.queued = true
	e.notBefore = n.now().Add(wait)
	if wait > 0 {
		if hold := n.now().Add(n.limits.backoff[0]); n.holdUntil.Before(hold) {
			n.holdUntil = hold
		}
	}
	n.signal()
}

func (n *notifier) pruneBrakesLocked() {
	now := n.now()
	for id, b := range n.brakes {
		if now.Sub(b.at) >= brakeWindow {
			delete(n.brakes, id)
		}
	}
}

// paceWait keeps the pace between two requests.
func (n *notifier) paceWait() bool {
	n.mu.Lock()
	paced := n.lastRequest.Add(n.limits.pace).Sub(n.now())
	n.mu.Unlock()
	return n.sleep(paced)
}

// send sends one message to the chat.
func (n *notifier) send(st settings, out outgoing) (int, *sendError) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout+5*time.Second)
	defer cancel()
	go func() {
		defer func() { recover() }()
		select {
		case <-n.stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	id, failure := n.client.sendMessage(ctx, st, out)
	n.mu.Lock()
	n.lastRequest = n.now()
	n.mu.Unlock()
	return id, failure
}

// tidyPins takes away the pins to take away and takes the pins still to
// take, each tried at most maxPinTries times; one given up is said in the
// server's log.
func (n *notifier) tidyPins(st settings) {
	for _, p := range n.threads.unpins(st) {
		ok := n.pinMessage(st, p.MessageID, false)
		if n.threads.unpinned(st, p.MessageID, ok) {
			n.logf("Telegram: message " + strconv.Itoa(p.MessageID) + " could not be unpinned after " + strconv.Itoa(maxPinTries) + " tries; unpin it in the chat")
		}
	}
	pins := n.threads.toPin(st)
	ids := make([]string, 0, len(pins))
	for id := range pins {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		t := pins[id]
		ok := n.pinMessage(st, t.MessageID, true)
		if n.threads.pinned(st, id, t.MessageID, ok) {
			n.logf("Telegram: message " + strconv.Itoa(t.MessageID) + " could not be pinned after " + strconv.Itoa(maxPinTries) + " tries")
		}
	}
}

// silentFor: an opening, a repeat and a merged message arrive as the
// opening's severity is delivered; a closing as information.
func (n *notifier) silentFor(st settings, nt notice, action threadAction) bool {
	sev := nt.severity
	if action == actionMerged {
		sev = nt.mergedSeverity
	}
	mode := st.delivery[sev]
	if action == actionClose {
		mode = st.mode(nt)
	}
	if mode == DeliverSilent || mode == DeliverOff {
		return true
	}
	now := n.now()
	critical := sev == sevCritical && action != actionClose
	return st.inQuietHours(now.Hour()*60+now.Minute()) && (!critical || st.silenceCrit)
}

// loadEntries takes back the threads that were not settled at the last
// stop (B3).
func (n *notifier) loadEntries() {
	saved := n.threads.entries()
	n.mu.Lock()
	defer n.mu.Unlock()
	ids := make([]string, 0, len(saved))
	for id := range saved {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s := saved[id]
		if len(n.entries) >= maxThreadEntries || s.Name == "" || threadID(s.Name) != id {
			n.removed[id] = true
			continue
		}
		n.entries[id] = &threadEntry{id: id, name: s.Name, kind: s.Kind, open: s.Open, since: s.Since, seenOpen: s.SeenOpen, fresh: s.Fresh, episode: 1,
			opening: s.Opening.notice(), closing: s.Closing.notice(), queued: true, touched: n.now()}
	}
	n.signal()
}
