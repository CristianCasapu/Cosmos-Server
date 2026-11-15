package proxy

// Fork addition: the events written for every answered request (a response
// through a URL, an API token used) are sampled. Upstream wrote one row into
// the events table per request; that table is read only by the events
// explorer (Monitoring > Events, a URL's and a token's Events tab), while the
// charts and counters come from the metrics, which still see every request.
//
// Per kind of event (its id and object: a URL and success or error, a token)
// at most one row is written per window. The first request after a quiet
// window is written at once; the ones after it are counted, and the row
// written once the window has passed (by the next request or by the flusher)
// carries their number in "requests", the time of the first in "since" and
// the details of the latest one. Refusals (shield blocks and bans, bot and
// local-only refusals, invalid or expired tokens) write their own events and
// are not sampled.

import (
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	eventSampleWindow = time.Minute
	// how often the flusher looks for counted requests whose window is over
	eventSampleTick = 10 * time.Second
	// a bound on the kinds held at once; the kinds come from the
	// configuration (URL names, token names), so this is never reached in
	// practice. Past it a request is written as its own row, as upstream did.
	eventSampleMaxKeys = 4096
)

type sampledEventKey struct {
	eventId string
	object  string
}

// sampledEventState is what is held for one kind: when its last row was
// written, and the requests counted since.
type sampledEventState struct {
	lastRow time.Time
	pending int
	since   time.Time
	label   string
	level   string
	data    map[string]interface{}
}

type sampledEventRow struct {
	eventId, label, level, object string
	data                          map[string]interface{}
}

type eventSampler struct {
	mu      sync.Mutex
	window  time.Duration
	maxKeys int
	kinds   map[sampledEventKey]*sampledEventState
	now     func() time.Time
	write   func(eventId, label, level, object string, data map[string]interface{})
}

func newEventSampler(window time.Duration, maxKeys int, write func(eventId, label, level, object string, data map[string]interface{})) *eventSampler {
	return &eventSampler{
		window:  window,
		maxKeys: maxKeys,
		kinds:   map[sampledEventKey]*sampledEventState{},
		now:     time.Now,
		write:   write,
	}
}

// observe takes one request's event. data is handed over: the sampler adds
// its fields to it and keeps it until it is written.
func (s *eventSampler) observe(eventId, label, level, object string, data map[string]interface{}) {
	key := sampledEventKey{eventId, object}
	now := s.now()

	s.mu.Lock()
	state := s.kinds[key]
	if state == nil {
		if len(s.kinds) >= s.maxKeys {
			s.mu.Unlock()
			data["requests"] = 1
			s.write(eventId, label, level, object, data)
			return
		}
		s.kinds[key] = &sampledEventState{lastRow: now}
		s.mu.Unlock()
		data["requests"] = 1
		s.write(eventId, label, level, object, data)
		return
	}

	if now.Sub(state.lastRow) >= s.window {
		count, since := state.pending+1, state.since
		if state.pending == 0 {
			since = now
		}
		state.lastRow, state.pending, state.data = now, 0, nil
		s.mu.Unlock()
		s.write(eventId, label, level, object, withCount(data, count, since))
		return
	}

	if state.pending == 0 {
		state.since = now
	}
	state.pending++
	state.label, state.level, state.data = label, level, data
	s.mu.Unlock()
}

// flush writes the rows of the kinds whose window is over and that counted
// requests since their last row, and forgets the kinds that are quiet. With
// all set it writes every counted request, whatever the window.
func (s *eventSampler) flush(all bool) {
	now := s.now()
	rows := []sampledEventRow{}

	s.mu.Lock()
	for key, state := range s.kinds {
		due := now.Sub(state.lastRow) >= s.window
		if state.pending == 0 {
			if due {
				delete(s.kinds, key)
			}
			continue
		}
		if !due && !all {
			continue
		}
		rows = append(rows, sampledEventRow{key.eventId, state.label, state.level, key.object, withCount(state.data, state.pending, state.since)})
		state.lastRow, state.pending, state.data = now, 0, nil
	}
	s.mu.Unlock()

	for _, row := range rows {
		s.write(row.eventId, row.label, row.level, row.object, row.data)
	}
}

func (s *eventSampler) size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.kinds)
}

func withCount(data map[string]interface{}, count int, since time.Time) map[string]interface{} {
	data["requests"] = count
	if count > 1 {
		data["since"] = since.UTC().Format(time.RFC3339)
	}
	return data
}

var (
	requestEvents     = newEventSampler(eventSampleWindow, eventSampleMaxKeys, utils.TriggerEvent)
	requestEventsOnce sync.Once
)

// SampledEvent is utils.TriggerEvent for an event written once per request:
// see the top of this file.
func SampledEvent(eventId, label, level, object string, data map[string]interface{}) {
	requestEventsOnce.Do(func() {
		go func() {
			for range time.Tick(eventSampleTick) {
				requestEvents.flush(false)
			}
		}()
	})
	requestEvents.observe(eventId, label, level, object, data)
}
