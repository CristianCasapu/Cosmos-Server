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
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// reconcileHorizon: an open problem older than this that the chat never
// heard of is not told at a start (a stale row).
const reconcileHorizon = 7 * 24 * time.Hour

// levelSource says whether threads are open.
type levelSource interface {
	// openThreads: the newest opening of every open thread of these kinds
	openThreads(kinds []string) ([]notice, error)
	// state: whether a thread of an opening kind is open, and when it is
	// over the follow-up that says so (nil: it ended quietly)
	state(name string, kind string, since time.Time) (open bool, closing *notice, err error)
}

// storeLevels reads the notifications.
type storeLevels struct{}

func (storeLevels) openThreads(kinds []string) ([]notice, error) {
	rows, err := utils.ForkOpenThreads(kinds, reconcileHorizon, maxThreadEntries)
	if err != nil {
		return nil, err
	}
	config := utils.GetMainConfig()
	out := []notice{}
	for _, row := range rows {
		if open, ok := utils.SourceThreadLevel(row.Fork.Kind, row.Fork.Thread); ok && !open {
			continue // the source knows better: over
		}
		nt := noticeFromInfo(row, config)
		nt.at = row.Date
		out = append(out, nt)
	}
	return out, nil
}

func (storeLevels) state(name string, kind string, since time.Time) (bool, *notice, error) {
	if open, ok := utils.SourceThreadLevel(kind, name); ok {
		if !open {
			// the source keeps it in memory: the panel is set right too
			utils.ResolveThread(name)
		} else {
			return true, nil, nil
		}
	}
	open, closing, err := utils.ForkThreadState(name, closingsOf(kind), since)
	if err != nil || open || closing == nil {
		return open, nil, err
	}
	nt := noticeFromInfo(*closing, utils.GetMainConfig())
	nt.at = closing.Date
	if pairedThread(nt) == "" {
		return false, nil, nil
	}
	return false, &nt, nil
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
	known := map[string]threadLook{}
	for id, e := range n.entries {
		look := threadLook{name: e.name, since: e.since}
		if e.opening != nil {
			look.kind = e.opening.kind
		} else if e.closing != nil {
			look.kind = e.closing.texts().Closes
		}
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
	for id, look := range known {
		if look.name == "" || !pairedOpening(look.kind) {
			continue
		}
		since := look.since
		if since.IsZero() {
			since = n.now().Add(-reconcileHorizon)
		}
		open, closing, err := n.levels.state(look.name, look.kind, since.Add(-time.Second))
		if err != nil {
			continue // not known now: nothing is changed
		}
		truths[id] = truth{open, closing}
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.now()
	for id, tr := range truths {
		look := known[id]
		e := n.entries[id]
		if e != nil && e.seq > start {
			continue // an event came meanwhile: it is newer
		}
		_, told := n.threads.get(st, id)
		if e == nil {
			if !tr.open && !told {
				continue
			}
			if len(n.entries) >= maxThreadEntries && !n.evictLocked() {
				n.overflowed++
				continue
			}
			e = &threadEntry{id: id, name: look.name, touched: now}
			n.entries[id] = e
		}
		switch {
		case tr.open && !e.open:
			e.open, e.since = true, look.since
			e.episode++
			e.closing, e.repeat = nil, nil
			if o, has := opens[id]; has {
				e.opening = &o
			}
		case tr.open && e.opening == nil:
			if o, has := opens[id]; has {
				e.opening = &o
			}
		case !tr.open && e.open:
			e.open, e.since = false, now
			e.closing, e.repeat, e.fresh = tr.closing, nil, false
		case !tr.open && e.closing == nil && tr.closing != nil:
			e.closing = tr.closing
		}
		e.dirty = true
		e.queued = true
	}
	n.allDueLocked()
	n.pinsDue = true
}

type threadLook struct {
	name, kind string
	since      time.Time
}
