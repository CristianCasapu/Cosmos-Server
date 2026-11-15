package anyimage

// What bounds the lookups: per minute and per user, Docker Hub's pull
// allowance, and the memory the caches may use.
//
// Docker Hub counts a manifest GET as a pull (100 per 6 hours for a server
// that is not signed in, 200 for a personal account), and the server needs
// that allowance for its real pulls (installs, updates). So:
//   - what does not count is used first: Docker Hub's own API for a tag's
//     platforms and manifest digests, HEAD for a digest, manifests and
//     configurations cached by their verified digest;
//   - a counted read of Docker Hub is allowed at most hubPerHour times an
//     hour for the whole server and hubPerUserHour for one user;
//   - when Docker Hub says how many pulls are left (ratelimit-remaining),
//     Cosmos stops reading at hubReserve and keeps the rest for real pulls.

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	hubPerHour     = 20
	hubPerUserHour = 8
	hubReserve     = 25

	describePerMinute     = 20
	describePerUserMinute = 8
	searchPerUserMinute   = 10

	cacheBytes = 8 << 20 // each cache
)

var (
	errHubReserve = errHubLimit{"hubReserve"}
	errHubBudget  = errHubLimit{"hubBudget"}
)

type errHubLimit struct{ code string }

func (e errHubLimit) Error() string { return e.code }

// perUser is a window per principal (a user's name or an API token's).
type perUser struct {
	mu      sync.Mutex
	windows map[string]*window
	perMin  int
}

func (p *perUser) start(user string, now time.Time) bool {
	p.mu.Lock()
	if p.windows == nil || len(p.windows) > 1024 {
		p.windows = map[string]*window{}
	}
	w := p.windows[user]
	if w == nil {
		w = &window{perMin: p.perMin, atOnce: 2}
		p.windows[user] = w
	}
	p.mu.Unlock()
	return w.start(now)
}

func (p *perUser) done(user string) {
	p.mu.Lock()
	w := p.windows[user]
	p.mu.Unlock()
	if w != nil {
		w.done()
	}
}

var (
	describeUsers = &perUser{perMin: describePerUserMinute}
	searchUsers   = &perUser{perMin: searchPerUserMinute}
)

// hubAllowance follows the counted reads of Docker Hub.
type hubAllowance struct {
	mu        sync.Mutex
	reads     []time.Time
	byUser    map[string][]time.Time
	remaining int // -1: not known
	seenAt    time.Time
}

var hubBudget = &hubAllowance{remaining: -1}

func recent(list []time.Time, now time.Time) []time.Time {
	kept := list[:0]
	for _, t := range list {
		if now.Sub(t) < time.Hour {
			kept = append(kept, t)
		}
	}
	return kept
}

// take reserves one counted read for a user, or says why not.
func (h *hubAllowance) take(user string, now time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.remaining >= 0 && now.Sub(h.seenAt) < 6*time.Hour && h.remaining <= hubReserve {
		return errHubReserve
	}
	h.reads = recent(h.reads, now)
	if h.byUser == nil || len(h.byUser) > 1024 {
		h.byUser = map[string][]time.Time{}
	}
	mine := recent(h.byUser[user], now)
	if len(h.reads) >= hubPerHour || len(mine) >= hubPerUserHour {
		h.byUser[user] = mine
		return errHubBudget
	}
	h.reads = append(h.reads, now)
	h.byUser[user] = append(mine, now)
	return nil
}

// observe reads Docker Hub's "ratelimit-remaining: 76;w=21600".
func (h *hubAllowance) observe(header http.Header, now time.Time) {
	value := header.Get("ratelimit-remaining")
	if value == "" {
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(strings.Split(value, ";")[0]))
	if err != nil || n < 0 {
		return
	}
	h.mu.Lock()
	h.remaining, h.seenAt = n, now
	h.mu.Unlock()
}

func (h *hubAllowance) left() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.remaining
}

// byteCache keeps entries up to a number of bytes, the oldest leaving first.
type byteCache struct {
	mu    sync.Mutex
	max   int
	used  int
	order []string
	items map[string]cacheItem
}

type cacheItem struct {
	at   time.Time
	size int
	data []byte
	desc *Description
}

func newByteCache(max int) *byteCache { return &byteCache{max: max, items: map[string]cacheItem{}} }

func (c *byteCache) get(key string, keep time.Duration) (cacheItem, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[key]
	if !ok {
		return cacheItem{}, false
	}
	if time.Since(item.at) > keep {
		c.remove(key)
		return cacheItem{}, false
	}
	return item, true
}

func (c *byteCache) remove(key string) {
	if item, ok := c.items[key]; ok {
		c.used -= item.size
		delete(c.items, key)
		for i, k := range c.order {
			if k == key {
				c.order = append(c.order[:i], c.order[i+1:]...)
				break
			}
		}
	}
}

func (c *byteCache) put(key string, item cacheItem) {
	if item.size > c.max/4 {
		return // one entry never takes more than a quarter
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.remove(key)
	item.at = time.Now()
	for c.used+item.size > c.max && len(c.order) > 0 {
		c.remove(c.order[0])
	}
	c.items[key] = item
	c.order = append(c.order, key)
	c.used += item.size
}

func (c *byteCache) clear() {
	c.mu.Lock()
	c.items, c.order, c.used = map[string]cacheItem{}, nil, 0
	c.mu.Unlock()
}
