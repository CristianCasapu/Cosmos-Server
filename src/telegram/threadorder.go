package telegram

// Fork addition (plan 48, review T1): the messages of one thread (a problem
// that starts and ends: an outage, a router that does not answer, a disk
// that fails) are delivered in order, and what is delivered is decided when
// it is sent, from the thread's truth, never from what was queued.
//
// Per thread the notifier keeps one entry: the truth (open or closed, from
// the events, in the order they happened), the notices that describe it,
// and whether an item of the thread is queued or in flight. There is at
// most one item per thread anywhere: nothing of a thread is sent while an
// earlier one of the same thread waits or is on the wire. Other threads
// are not held up by it.
//
// "told" is whether the chat holds an opening of this thread that was not
// closed (threads.go keeps its message id; it survives a restart).
//
// The state machine, decided at send time (decide):
//
//	truth   told   pending repeat   → action
//	open    no     -                → OPEN: send the opening (pin it when its kind pins);
//	                                   told = yes
//	open    yes    yes              → REPEAT: send the repeat as a reply
//	open    yes    no               → nothing (the chat already says it is open;
//	                                   a close and re-open in between changes nothing)
//	closed  yes    -                → CLOSE: unpin and forget the opening ALWAYS; send the
//	                                   closing as a reply only when its kind and severity
//	                                   are wanted; told = no
//	closed  no     -  (an opening   → MERGED: one message "it happened and is over (was: …,
//	               of this episode     12 minutes)", delivered as the opening would have been
//	               was never sent)     (it rings when the opening would), no reply, no pin
//	closed  no     -  (nothing)     → nothing
//
// Events (threadEvent): an opening sets truth open (a new episode when it
// was closed); a closing sets truth closed; a repeat is noted when the
// truth is open. After every send (or failure) the entry is decided again,
// so the last message of a thread always matches its truth: the chat never
// ends on "OK" while the problem is open, nor on a pinned problem that is
// over. A thread is its own flood control: one item at a time, its events
// folded into its truth, so the budgets per kind and the cap per minute do
// not hold its messages back (they would leave the chat on a summary while
// the truth is something else). There are at most maxThreadEntries threads.

import (
	"context"
	"time"
)

const maxThreadEntries = 256

type threadAction int

const (
	actionNone threadAction = iota
	actionOpen
	actionRepeat
	actionClose
	actionMerged
)

type threadEntry struct {
	id       string
	open     bool
	opening  *notice // the opening of the current (or last) episode
	closing  *notice // the last closing
	repeat   *notice // a repeat waiting to be sent
	held     bool    // the opening was counted in a summary instead of being sent
	queued   bool
	inFlight bool
}

func threadOf(nt notice) string {
	if nt.resolves != "" {
		return nt.resolves
	}
	return nt.thread
}

// threadEventLocked records an event of a thread and makes sure the thread
// has its one item queued.
func (n *notifier) threadEventLocked(_ settings, nt notice, now time.Time) {
	id := threadOf(nt)
	e := n.entries[id]
	if e == nil {
		if len(n.entries) >= maxThreadEntries {
			for key, old := range n.entries {
				if !old.queued && !old.inFlight {
					delete(n.entries, key)
					break
				}
			}
		}
		e = &threadEntry{id: id}
		n.entries[id] = e
	}
	switch {
	case nt.resolves != "":
		c := nt
		e.open, e.closing = false, &c
	case nt.repeat:
		if e.open {
			r := nt
			e.repeat = &r
		}
	default:
		if !e.open || e.opening == nil {
			o := nt
			e.open, e.opening, e.held = true, &o, false
		}
		e.open = true
	}
	n.scheduleLocked(e, now)
}

// scheduleLocked queues the thread's item when it has none.
func (n *notifier) scheduleLocked(e *threadEntry, now time.Time) {
	if e.queued || e.inFlight {
		n.signal()
		return
	}
	p := priorityLow
	if e.opening != nil {
		p = e.opening.priority()
	}
	it := &item{priority: p, at: now, since: now, counts: map[string]int{}, thread: e.id}
	if e.opening != nil {
		it.counts[e.opening.kind] = 1
	}
	e.queued = true
	if p == priorityCritical {
		n.critical = append(n.critical, it)
	} else {
		n.normal = append(n.normal, it)
	}
	n.signal()
}

// decideLocked is the state machine above. told: the chat holds the
// thread's opening.
func decideLocked(e *threadEntry, told bool) threadAction {
	switch {
	case e.open && !told && e.opening != nil && !e.held:
		return actionOpen
	case e.open && told && e.repeat != nil:
		return actionRepeat
	case !e.open && told:
		return actionClose
	case !e.open && !told && e.opening != nil && e.closing != nil:
		return actionMerged
	}
	return actionNone
}

func (n *notifier) told(st settings, id string) (openThread, bool) {
	if n.threads == nil {
		return openThread{}, false
	}
	return n.threads.get(st, id)
}

// deliverThread sends what the thread's truth asks for now.
func (n *notifier) deliverThread(it *item) bool {
	for attempt := 0; ; attempt++ {
		st := n.settings()
		if !st.usable {
			return true
		}
		n.mu.Lock()
		e := n.entries[it.thread]
		if e == nil {
			n.mu.Unlock()
			return true
		}
		e.queued, e.inFlight = false, true
		opened, told := n.told(st, e.id)
		action := decideLocked(e, told)
		var subject notice // what the message says, and how it is delivered
		var wanted bool
		switch action {
		case actionOpen:
			subject = *e.opening
			wanted = st.wants(subject)
			if !wanted {
				e.held = true // not wanted: nothing to say now, its end is not told either
			}
		case actionRepeat:
			subject = *e.repeat
			wanted = st.wants(subject)
		case actionClose:
			subject = *e.closing
			wanted = st.wants(subject)
		case actionMerged:
			subject = *e.closing
			subject.mergedLabel, subject.mergedSince = e.opening.label(), e.opening.at
			subject.mergedSeverity = e.opening.severity
			// it says what the owner never heard of: as the opening would arrive
			wanted = st.wants(*e.opening)
		}
		n.mu.Unlock()

		messageID := 0
		var failure *sendError
		if action != actionNone && wanted {
			n.mu.Lock()
			paced := n.lastRequest.Add(n.limits.pace).Sub(n.now())
			n.mu.Unlock()
			if !n.sleep(paced) {
				return false
			}
			m := render(st, subject)
			if it.triedPlain {
				m = renderWith(st, subject, false)
				m.button = nil
			}
			if it.noButton {
				m.button = nil
			}
			out := outgoing{message: m, silent: n.silentFor(st, subject, action)}
			if action == actionRepeat || action == actionClose {
				out.replyTo = opened.MessageID
			}
			ctx, cancel := context.WithTimeout(context.Background(), requestTimeout+5*time.Second)
			go func() {
				select {
				case <-n.stop:
					cancel()
				case <-ctx.Done():
				}
			}()
			messageID, failure = n.client.sendMessage(ctx, st, out)
			cancel()
			n.mu.Lock()
			n.lastRequest = n.now()
			n.mu.Unlock()
		}

		if failure != nil {
			if failure.Reason == ReasonBadButton && !it.noButton {
				it.noButton = true
				n.markIdle(it.thread)
				attempt--
				continue
			}
			if failure.Reason == ReasonBadMessage && !it.triedPlain {
				it.triedPlain = true
				n.markIdle(it.thread)
				attempt--
				continue
			}
			if failure.Settings || !failure.Retryable {
				n.failed(failure, st, failure.Settings)
				// the state of the chat is unknown to change: decided again later
				n.retryThread(it, n.limits.backoff[len(n.limits.backoff)-1])
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
				n.mu.Lock()
				if until := n.now().Add(wait); n.holdUntil.Before(until) {
					n.holdUntil = until
				}
				n.mu.Unlock()
			}
			it.attempts++
			n.noteFailure(failure)
			n.retryThread(it, wait)
			return true
		}
		if action != actionNone && wanted {
			n.succeeded()
		}

		// the state changes, message or not
		unpin := []int{}
		switch action {
		case actionOpen:
			if wanted && messageID > 0 && n.threads != nil {
				pin := subject.texts().Pin && subject.severity == sevCritical
				unpin = n.threads.open(st, e.id, messageID, pin)
				if pin {
					n.pinMessage(st, messageID, true)
				}
			}
		case actionClose:
			if n.threads != nil {
				if open, ok := n.threads.close(st, e.id); ok && open.Pinned {
					unpin = append(unpin, open.MessageID)
				}
			}
		}
		for _, id := range unpin {
			n.pinMessage(st, id, false)
		}

		n.mu.Lock()
		e.inFlight = false
		switch action {
		case actionRepeat:
			e.repeat = nil
		case actionClose, actionMerged:
			e.opening, e.closing, e.repeat, e.held = nil, nil, nil, false
		}
		_, told = n.told(st, e.id)
		if decideLocked(e, told) != actionNone {
			n.scheduleLocked(e, n.now())
		} else if !e.open && e.opening == nil {
			delete(n.entries, e.id)
		}
		n.mu.Unlock()
		return true
	}
}

// markIdle lets a thread's item be tried again at once (a form refused).
func (n *notifier) markIdle(id string) {
	n.mu.Lock()
	if e := n.entries[id]; e != nil {
		e.inFlight = false
	}
	n.mu.Unlock()
}

// retryThread puts the thread's item back after wait; what it sends is
// decided again then.
func (n *notifier) retryThread(it *item, wait time.Duration) {
	now := n.now()
	n.mu.Lock()
	defer n.mu.Unlock()
	e := n.entries[it.thread]
	if e == nil {
		return
	}
	e.inFlight, e.queued = false, true
	it.notBefore = now.Add(wait)
	if hold := now.Add(n.limits.backoff[0]); n.holdUntil.Before(hold) {
		n.holdUntil = hold
	}
	if it.priority == priorityCritical {
		n.critical = append(n.critical, it)
	} else {
		n.normal = append(n.normal, it)
	}
	n.signal()
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
	if mode == DeliverSilent {
		return true
	}
	now := n.now()
	critical := sev == sevCritical && action != actionClose
	return st.inQuietHours(now.Hour()*60+now.Minute()) && (!critical || st.silenceCrit)
}
