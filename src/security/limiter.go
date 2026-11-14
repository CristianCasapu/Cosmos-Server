package security

import (
	"net/netip"
	"sort"
	"strconv"
	"sync"
	"time"
)

// The limits of the record. An event passes two token buckets: one for its
// kind and source (an IPv4 address, an IPv6 /64, else the actor), then one
// for its kind. What does not pass is counted and comes back as a summary
// row ("N more of these"), so nothing is lost silently and a flood costs a
// bounded number of rows.
//
// One flooding source uses up its own bucket and next to nothing of the
// kind's, so other sources keep being recorded one by one. Only a flood from
// many sources fills a kind's bucket; the kind then shows its steady rate
// plus the counts.
type limits struct {
	sourceBurst     float64
	sourcePerMinute float64
	kindBurst       float64
	kindPerMinute   float64
}

var (
	incidentLimits = limits{sourceBurst: 10, sourcePerMinute: 1, kindBurst: 60, kindPerMinute: 10}
	auditLimits    = limits{sourceBurst: 60, sourcePerMinute: 30, kindBurst: 300, kindPerMinute: 60}
)

const (
	// maxSources bounds the limiter's memory: kind and source pairs kept.
	maxSources = 8192
	// a summary is written once its source has been quiet for summaryQuiet,
	// and at the latest summaryEvery after the first event it stands for
	summaryQuiet = time.Minute
	summaryEvery = 5 * time.Minute
	// per kind and pass, at most this many summaries of single sources; the
	// rest is added to the kind's own summary
	maxSourceSummaries = 5
	// a source that neither sent nor owes anything for this long is forgotten
	sourceIdle = 10 * time.Minute
	// the sources of a kind's summary are counted up to this many
	maxCountedSources = 1000
)

func limitsOf(kind Kind) limits {
	if kind.Class == ClassAudit {
		return auditLimits
	}
	return incidentLimits
}

type bucket struct {
	tokens float64
	last   time.Time
}

func (b *bucket) take(now time.Time, burst float64, perMinute float64) bool {
	if b.last.IsZero() {
		b.tokens = burst
	} else if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens += elapsed.Minutes() * perMinute
		if b.tokens > burst {
			b.tokens = burst
		}
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type sourceState struct {
	bucket
	kind       string
	ip         string // of the last suppressed event
	forwarded  bool   // that address was reported by a proxy
	actor      string
	suppressed int
	since      time.Time // first suppressed event not yet summarised
	lastSeen   time.Time
}

type kindState struct {
	bucket
	suppressed int
	sources    map[string]struct{}
	since      time.Time
	lastSeen   time.Time
}

type sourceKey struct {
	kind   string
	source string
}

type limiter struct {
	mu      sync.Mutex
	sources map[sourceKey]*sourceState
	kinds   map[string]*kindState
	max     int
}

func newLimiter() *limiter {
	return &limiter{sources: map[sourceKey]*sourceState{}, kinds: map[string]*kindState{}, max: maxSources}
}

// sourceOf is what an event is limited by: its address (an IPv6 address
// counts as its /64, so that moving within a prefix earns no new allowance),
// else its actor, else nothing.
func sourceOf(event Event) string {
	if event.IP != "" {
		if addr, err := netip.ParseAddr(event.IP); err == nil {
			// an address a proxy reported and the same address connecting
			// itself are two sources: their summaries go different ways
			// (only the second belongs in the Fail2ban file)
			via := ""
			if event.Forwarded {
				via = " via proxy"
			}
			if addr.Is6() {
				if prefix, err := addr.Prefix(64); err == nil {
					return prefix.Addr().String() + "/64" + via
				}
			}
			return addr.String() + via
		}
	}
	if event.Actor != "" {
		return "actor:" + event.Actor
	}
	return "-"
}

func (l *limiter) kindState(kind string) *kindState {
	state := l.kinds[kind]
	if state == nil {
		state = &kindState{}
		l.kinds[kind] = state
	}
	return state
}

// countForKind adds events that were not recorded to the kind's own count.
// Called with the lock held.
func (l *limiter) countForKind(kind string, source string, events int, now time.Time) {
	state := l.kindState(kind)
	if state.suppressed == 0 {
		state.since = now
	}
	state.suppressed += events
	state.lastSeen = now
	if state.sources == nil {
		state.sources = map[string]struct{}{}
	}
	if len(state.sources) <= maxCountedSources {
		state.sources[source] = struct{}{}
	}
}

// allow reports whether the event is recorded as a row of its own. When it
// is not, it has been counted.
func (l *limiter) allow(event Event, kind Kind, now time.Time) bool {
	limit := limitsOf(kind)
	key := sourceKey{kind: event.Kind, source: sourceOf(event)}

	l.mu.Lock()
	defer l.mu.Unlock()

	state := l.sources[key]
	if state == nil {
		if len(l.sources) >= l.max {
			l.makeRoom(now)
		}
		state = &sourceState{kind: event.Kind}
		l.sources[key] = state
	}
	state.lastSeen = now

	if !state.take(now, limit.sourceBurst, limit.sourcePerMinute) {
		if state.suppressed == 0 {
			state.since = now
		}
		state.suppressed += event.Count
		state.ip, state.actor, state.forwarded = event.IP, event.Actor, event.Forwarded
		return false
	}

	if !l.kindState(event.Kind).take(now, limit.kindBurst, limit.kindPerMinute) {
		l.countForKind(event.Kind, key.source, event.Count, now)
		return false
	}
	return true
}

// makeRoom is called when the table of sources is full: what the idle ones
// still owe goes to their kind's count and they are forgotten; if that is
// not enough, a quarter of the table goes the same way. The kinds' buckets
// keep bounding what is written. Called with the lock held.
func (l *limiter) makeRoom(now time.Time) {
	forget := func(key sourceKey, state *sourceState) {
		if state.suppressed > 0 {
			l.countForKind(key.kind, key.source, state.suppressed, now)
		}
		delete(l.sources, key)
	}
	for key, state := range l.sources {
		if now.Sub(state.lastSeen) >= summaryQuiet {
			forget(key, state)
		}
	}
	if len(l.sources) < l.max {
		return
	}
	drop := l.max / 4
	if drop < 1 {
		drop = 1
	}
	for key, state := range l.sources {
		if drop == 0 {
			break
		}
		forget(key, state)
		drop--
	}
}

// summaries returns the summary rows that are due: for each source that had
// events counted instead of recorded, once it is quiet or has been counting
// for summaryEvery, and likewise for each kind. With all set, everything
// counted so far is returned (used when the recorder stops). The rows are
// ordered by kind, sources first.
func (l *limiter) summaries(now time.Time, all bool) []Event {
	due := func(since time.Time, lastSeen time.Time) bool {
		return all || now.Sub(lastSeen) >= summaryQuiet || now.Sub(since) >= summaryEvery
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	var rows []Event
	perKind := map[string]int{}
	keys := make([]sourceKey, 0, len(l.sources))
	for key := range l.sources {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].kind != keys[j].kind {
			return keys[i].kind < keys[j].kind
		}
		return keys[i].source < keys[j].source
	})
	for _, key := range keys {
		state := l.sources[key]
		if state.suppressed == 0 {
			if now.Sub(state.lastSeen) >= sourceIdle {
				delete(l.sources, key)
			}
			continue
		}
		if !due(state.since, state.lastSeen) {
			continue
		}
		if perKind[key.kind] >= maxSourceSummaries {
			l.countForKind(key.kind, key.source, state.suppressed, now)
		} else {
			perKind[key.kind]++
			rows = append(rows, Event{
				Kind:      key.kind,
				Time:      now,
				IP:        state.ip,
				Forwarded: state.forwarded,
				Actor:     state.actor,
				Count:     state.suppressed,
				Details:   map[string]string{"summary": "1"},
			})
		}
		state.suppressed = 0
	}

	kinds := make([]string, 0, len(l.kinds))
	for kind := range l.kinds {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		state := l.kinds[kind]
		// a source folded in just above makes the kind's summary due at once
		if state.suppressed == 0 || !(due(state.since, state.lastSeen) || perKind[kind] >= maxSourceSummaries) {
			continue
		}
		sources := strconv.Itoa(len(state.sources))
		if len(state.sources) > maxCountedSources {
			sources = strconv.Itoa(maxCountedSources) + "+"
		}
		rows = append(rows, Event{
			Kind:    kind,
			Time:    now,
			Count:   state.suppressed,
			Details: map[string]string{"summary": "1", "sources": sources},
		})
		state.suppressed = 0
		state.sources = nil
	}
	return rows
}

// size is the number of sources the limiter remembers.
func (l *limiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.sources)
}
