package turnstile

// How often a visitor may make the server ask Cloudflare. Without it the
// sign-in form would be a way to make the server send requests to
// Cloudflare as fast as anyone can post: a visitor (IPv4 address, IPv6 /64)
// gets a burst of 6 and then one every 30 seconds; everyone together a burst
// of 60 and then one a second. A request without a token costs nothing and
// is not counted. The state is bounded.

import (
	"net"
	"sync"
	"time"
)

const (
	visitorBurst  = 6
	visitorRefill = 30 * time.Second
	globalBurst   = 60
	globalRefill  = time.Second
	maxVisitors   = 4096
)

type bucket struct {
	tokens float64
	at     time.Time
}

func (b *bucket) take(burst int, refill time.Duration, t time.Time) bool {
	if b.at.IsZero() {
		b.tokens, b.at = float64(burst), t
	}
	b.tokens += float64(t.Sub(b.at)) / float64(refill)
	if b.tokens > float64(burst) {
		b.tokens = float64(burst)
	}
	b.at = t
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type limiter struct {
	mu       sync.Mutex
	visitors map[string]*bucket
	global   bucket
}

var checks = &limiter{visitors: map[string]*bucket{}}

// visitorKey is the address a visitor is counted by.
func visitorKey(address string) string {
	ip := net.ParseIP(address)
	if ip == nil {
		return "unknown"
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// allow takes one check for a visitor.
func (l *limiter) allow(address string, t time.Time) bool {
	key := visitorKey(address)
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.visitors[key]
	if b == nil {
		if len(l.visitors) >= maxVisitors {
			// drop the ones that are full again: they lose nothing
			for k, other := range l.visitors {
				if t.Sub(other.at) >= visitorRefill*visitorBurst {
					delete(l.visitors, k)
				}
			}
			if len(l.visitors) >= maxVisitors {
				// a flood from many addresses: the global bucket limits it anyway
				l.visitors = map[string]*bucket{}
			}
		}
		b = &bucket{}
		l.visitors[key] = b
	}
	if !b.take(visitorBurst, visitorRefill, t) {
		return false
	}
	return l.global.take(globalBurst, globalRefill, t)
}

func (l *limiter) reset() {
	l.mu.Lock()
	l.visitors = map[string]*bucket{}
	l.global = bucket{}
	l.mu.Unlock()
}
