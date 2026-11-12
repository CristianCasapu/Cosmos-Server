package proxy

import (
	"sync"
	"sync/atomic"
	"time"
)

// Per-client budget accounting for the HTTP and TCP shields.
//
// Usage is kept as aggregate counters, not as a history of requests: each
// client has a ring of fixed slots covering the last hour, plus live counters
// for what is currently in flight. Reading a budget is a dozen additions under
// the client's own lock, whatever the traffic volume, and finished requests
// cost nothing to remember.

const (
	budgetWindow = time.Hour
	budgetSlots  = 12
	slotLength   = budgetWindow / budgetSlots
)

type budgetSlot struct {
	start    int64 // unix seconds of the slot start; a stale slot is reset on first write
	requests int
	packets  int64
	bytes    int64
	seconds  float64
}

type clientBudget struct {
	sync.Mutex
	slots [budgetSlots]budgetSlot
	// in-flight work: count, and the sum of start times so the elapsed time of
	// everything still running is n*now - sum without walking anything
	inflight        int
	inflightStartNs int64
	lastSeen        time.Time
	// live TCP connections of this client (nil for HTTP)
	live map[*TCPConnectionWrapper]struct{}
}

func (b *clientBudget) slot(now time.Time) *budgetSlot {
	sec := int64(slotLength.Seconds())
	start := now.Unix() - now.Unix()%sec
	s := &b.slots[(start/sec)%budgetSlots]
	if s.start != start {
		*s = budgetSlot{start: start}
	}
	return s
}

// finished sums every slot still inside the window. Must hold the lock.
func (b *clientBudget) finished(now time.Time) budgetSlot {
	total := budgetSlot{}
	cutoff := now.Add(-budgetWindow - slotLength).Unix()
	for i := range b.slots {
		s := &b.slots[i]
		if s.start == 0 || s.start <= cutoff {
			continue
		}
		total.requests += s.requests
		total.packets += s.packets
		total.bytes += s.bytes
		total.seconds += s.seconds
	}
	return total
}

func (b *clientBudget) begin(now time.Time) {
	b.Lock()
	b.inflight++
	b.inflightStartNs += now.UnixNano()
	b.lastSeen = now
	b.Unlock()
}

func (b *clientBudget) end(started time.Time, now time.Time) {
	b.Lock()
	b.inflight--
	b.inflightStartNs -= started.UnixNano()
	b.slot(now).seconds += now.Sub(started).Seconds()
	b.lastSeen = now
	b.Unlock()
}

func (b *clientBudget) addRequests(n int, now time.Time) {
	b.Lock()
	b.slot(now).requests += n
	b.Unlock()
}

func (b *clientBudget) addBytes(n int64, now time.Time) {
	b.Lock()
	b.slot(now).bytes += n
	b.Unlock()
}

func (b *clientBudget) addPackets(n int64, now time.Time) {
	b.Lock()
	b.slot(now).packets += n
	b.Unlock()
}

// idle reports whether nothing is running and nothing recent is recorded.
func (b *clientBudget) idle(now time.Time) bool {
	b.Lock()
	defer b.Unlock()
	return b.inflight == 0 && len(b.live) == 0 && b.lastSeen.Add(budgetWindow+slotLength).Before(now)
}

type budgetStore struct {
	mu       sync.RWMutex
	clients  map[string]*clientBudget
	inflight map[string]*atomic.Int64 // per shieldID
}

func newBudgetStore() *budgetStore {
	return &budgetStore{
		clients:  map[string]*clientBudget{},
		inflight: map[string]*atomic.Int64{},
	}
}

func (s *budgetStore) client(shieldID string, clientID string) *clientBudget {
	key := shieldID + "\x00" + clientID
	s.mu.RLock()
	b := s.clients[key]
	s.mu.RUnlock()
	if b != nil {
		return b
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if b = s.clients[key]; b == nil {
		b = &clientBudget{lastSeen: time.Now()}
		s.clients[key] = b
	}
	return b
}

// shieldInflight returns the live request counter of a shield (route).
func (s *budgetStore) shieldInflight(shieldID string) *atomic.Int64 {
	s.mu.RLock()
	c := s.inflight[shieldID]
	s.mu.RUnlock()
	if c != nil {
		return c
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c = s.inflight[shieldID]; c == nil {
		c = &atomic.Int64{}
		s.inflight[shieldID] = c
	}
	return c
}

func (s *budgetStore) size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.clients)
}

// cleanup forgets idle clients and returns how many were dropped.
func (s *budgetStore) cleanup(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for key, b := range s.clients {
		if b.idle(now) {
			delete(s.clients, key)
			removed++
		}
	}
	return removed
}

// each calls fn for every tracked client budget.
func (s *budgetStore) each(fn func(*clientBudget)) {
	s.mu.RLock()
	all := make([]*clientBudget, 0, len(s.clients))
	for _, b := range s.clients {
		all = append(all, b)
	}
	s.mu.RUnlock()
	for _, b := range all {
		fn(b)
	}
}
