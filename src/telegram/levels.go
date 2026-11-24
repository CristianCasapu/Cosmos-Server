package telegram

// Fork addition (plan 48, review 4 (d)): the truth of a thread is LEVEL.
// At start (after reconcileDelay) and every reconcileEvery, the notifier
// asks whether each thread is open: the threads the chat holds, those in
// its memory, and those the notifications hold open (the sources write
// there; a source that keeps its state in memory says it itself, see
// utils.RegisterThreadLevel). It then sets the entries right: open and not
// told → OPEN; over and told → CLOSE (with the follow-up the source wrote,
// or "no longer followed" when it ended quietly). Pins and unpins refused
// earlier are tried again at the same time.

import (
	"sort"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// reconcileHorizon: an open problem older than this that the chat never
// heard of is not told at a start (a stale row).
const reconcileHorizon = 7 * 24 * time.Hour

// threadLevel is what is known of a thread now (review 5): open, over, or
// unknown. Unknown changes nothing in the chat; "over" needs positive
// evidence.
type threadLevel int

const (
	levelUnknown threadLevel = iota
	levelOpen
	levelOver
)

func (l threadLevel) String() string { return [...]string{"unknown", "open", "over"}[l] }

// levelSource says whether threads are open.
type levelSource interface {
	// openThreads: the newest opening of every open thread of these kinds
	openThreads(kinds []string) ([]notice, error)
	// state: the level of a thread of an opening kind, and when it is over
	// the follow-up that says so (nil: it ended quietly). An error is
	// unknown.
	state(name string, kind string, since time.Time) (threadLevel, *notice, error)
}

// storeLevels reads the notifications.
type storeLevels struct{}

func (storeLevels) openThreads(kinds []string) ([]notice, error) {
	out := []notice{}
	seen := map[string]bool{}
	rows, err := utils.ForkOpenThreads(kinds, reconcileHorizon, maxThreadEntries)
	if err == nil {
		config := utils.GetMainConfig()
		for _, row := range rows {
			if open, ok := utils.SourceThreadLevel(row.Fork.Kind, row.Fork.Thread); ok && !open {
				continue // the source knows better: over
			}
			nt := noticeFromInfo(row, config)
			nt.at = row.Date
			out = append(out, nt)
			seen[row.Fork.Thread] = true
		}
	}
	// review 5: what the sources hold open, its notification deleted or not
	for kind, threads := range utils.SourceOpenThreads() {
		if !pairedOpening(kind) {
			continue
		}
		for _, name := range threads {
			if !seen[name] && len(out) < maxThreadEntries {
				seen[name] = true
				out = append(out, genericOpening(kind, name, time.Now()))
			}
		}
	}
	if len(out) == 0 && err != nil {
		return nil, err
	}
	return out, nil
}

// genericOpening is the opening of a problem a source holds open whose
// notification is gone: the kind's own words, without its values.
func genericOpening(kind string, name string, at time.Time) notice {
	nt := newNotice(kind, map[string]string{})
	nt.thread, nt.at, nt.key = name, at, "n:"+kind
	return nt
}

// state: what the source says (its own state: utils.RegisterThreadLevel),
// else what the notifications prove; a store error is unknown.
func (storeLevels) state(name string, kind string, since time.Time) (threadLevel, *notice, error) {
	sourceOpen, sourceKnown := utils.SourceThreadLevel(kind, name)
	if sourceKnown && sourceOpen {
		return levelOpen, nil, nil
	}
	open, over, closing, err := utils.ForkThreadState(name, closingsOf(kind), since)
	lvl, resolveRow := combineLevel(sourceKnown, sourceOpen, err, open, over)
	if resolveRow {
		// the source knows it is over: the panel is set right too
		utils.ResolveThread(name)
	}
	if lvl == levelUnknown {
		return levelUnknown, nil, err
	}
	var follow *notice
	if lvl == levelOver && err == nil && closing != nil {
		nt := noticeFromInfo(*closing, utils.GetMainConfig())
		nt.at = closing.Date
		if pairedThread(nt) != "" {
			follow = &nt
		}
	}
	return lvl, follow, nil
}

// combineLevel is the precedence of the two answers (review 6, m18): the
// source first, since a source says "over" only on evidence of its own
// (a successful look of this run, or the owner's settings), and then it
// also resolves a row the panel still holds open; the notifications only
// when the source does not know: open on an unresolved row, over on a
// resolved or closing row since the thread was last seen open, unknown
// otherwise and on any error of the store.
func combineLevel(sourceKnown, sourceOpen bool, storeErr error, storeOpen, storeOver bool) (lvl threadLevel, resolveRow bool) {
	switch {
	case sourceKnown && sourceOpen:
		return levelOpen, false
	case sourceKnown:
		return levelOver, storeErr == nil && storeOpen
	case storeErr != nil:
		return levelUnknown, false
	case storeOpen:
		return levelOpen, false
	case storeOver:
		return levelOver, false
	}
	return levelUnknown, false
}

// reconcileLoop looks at the level of the threads now and then. A panic is
// logged and the loop goes on.
func (n *notifier) reconcileLoop() {
	wait := n.limits.reconcileDelay
	for {
		timer := time.NewTimer(wait)
		select {
		case <-n.stop:
			timer.Stop()
			return
		case <-n.reconcileNow:
			timer.Stop()
		case <-timer.C:
		}
		n.reconcileSafely()
		wait = n.limits.reconcileEvery
	}
}

func (n *notifier) reconcileSafely() {
	defer func() {
		if r := recover(); r != nil {
			n.logf("Telegram: looking at the open problems stopped on an error, tried again later: " + panicText(r))
		}
	}()
	n.reconcile()
}

// reconcile sets the threads right from their level.
func (n *notifier) reconcile() {
	st := n.settings()
	if !st.usable || n.levels == nil {
		return
	}
	n.mu.Lock()
	start := n.seq
	readAt := n.now() // what the reads see is as of now (review 6, T1)
	known := map[string]threadLook{}
	for id, e := range n.entries {
		look := threadLook{name: e.name, since: e.since, kind: e.kind}
		if e.seenOpen.After(look.since) {
			look.since = e.seenOpen
		}
		if look.kind == "" && e.opening != nil {
			look.kind = e.opening.kind
		} else if look.kind == "" && e.closing != nil {
			look.kind = e.closing.texts().Closes
		}
		look.inMemory = true
		known[id] = look
	}
	n.mu.Unlock()
	for id, t := range n.threads.all(st) {
		if _, has := known[id]; !has {
			known[id] = threadLook{name: t.Name, kind: t.Kind, since: t.At}
		}
	}

	opens := map[string]notice{}
	if found, err := n.levels.openThreads(PairedKinds()); err == nil {
		for _, nt := range found {
			if name := pairedThread(nt); name != "" {
				opens[threadID(name)] = nt
				if _, has := known[threadID(name)]; !has {
					known[threadID(name)] = threadLook{name: name, kind: nt.kind, since: nt.at}
				}
			}
		}
	}

	type truth struct {
		open    bool
		closing *notice
	}

	truths := map[string]truth{}
	ids := make([]string, 0, len(known))
	for id := range known {
		ids = append(ids, id)
	}
	sort.Strings(ids) // the same order every time (the model replays seeds)
	for _, id := range ids {
		look := known[id]
		if look.name == "" || !pairedOpening(look.kind) {
			continue
		}
		since := look.since
		if since.IsZero() {
			since = n.now().Add(-reconcileHorizon)
		}
		lvl, closing, err := n.levels.state(look.name, look.kind, since.Add(-time.Second))
		if err != nil || lvl == levelUnknown {
			continue // not known now: nothing is changed
		}
		truths[id] = truth{lvl == levelOpen, closing}
	}

	if n.reconcileHook != nil {
		n.reconcileHook() // the tests: events between the reads and the apply
	}
	plain := []notice{}
	defer func() {
		for _, nt := range plain {
			n.Notify(nt) // the thread is marked: it goes as a message of its own
		}
	}()
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.now()
	for _, id := range ids {
		tr, has := truths[id]
		if !has {
			continue
		}
		look := known[id]
		if n.threads.isPlain(id) {
			// its messages go as messages of their own (the table was full):
			// its opening once per run of the server, if it is still open
			if o, has := opens[id]; has && tr.open && !n.plainSent[brakeKey(st, id)] {
				n.plainSent[brakeKey(st, id)] = true
				n.notePlainLocked(id)
				plain = append(plain, o)
			}
			continue
		}
		e := n.entries[id]
		if e != nil && e.seq > start {
			continue // an event came meanwhile: it is newer
		}
		_, told := n.threads.get(st, id)
		if e == nil {
			if !tr.open && !told {
				continue
			}
			if look.inMemory {
				continue // forgotten since it was read: the read is stale
			}
			if len(n.entries) >= maxThreadEntries && !n.evictLocked() {
				// the table is full of open problems: an open one the chat
				// was not told goes as a message of its own, once
				if o, has := opens[id]; has && tr.open && !told && !n.plainSent[brakeKey(st, id)] && n.threads.markPlain(id, true) {
					n.plainSent[brakeKey(st, id)] = true
					n.notePlainLocked(id)
					n.overflowed++
					plain = append(plain, o)
				}
				continue
			}
			e = &threadEntry{id: id, name: look.name, kind: look.kind, touched: now}
			n.entries[id] = e
		}
		if tr.open && readAt.After(e.seenOpen) {
			// seen open by the level: an end is believed only on newer
			// evidence (kept in memory; written with the entry's next change)
			e.seenOpen = readAt
		}
		changed := true
		switch {
		case tr.open && !e.open:
			// review 6, T1: this episode starts now; the previous one's end
			// is not evidence about it
			e.open, e.since = true, readAt
			e.episode++
			e.closing, e.repeat, e.byLevel = nil, nil, true
			if o, has := opens[id]; has {
				e.opening = &o
			}
		case tr.open && e.opening == nil:
			if o, has := opens[id]; has {
				e.opening = &o
			} else {
				changed = false
			}
		case !tr.open && e.open:
			e.open, e.since = false, now
			e.closing, e.repeat, e.fresh = tr.closing, nil, false
		case !tr.open && e.closing == nil && tr.closing != nil:
			e.closing = tr.closing
		default:
			changed = false
		}
		if changed {
			e.dirty = true
		}
		e.queued = true
	}
	n.allDueLocked()
	n.pinsDue = true
	n.levelWait = time.Time{}
}

type threadLook struct {
	name, kind string
	since      time.Time
	inMemory   bool
}
