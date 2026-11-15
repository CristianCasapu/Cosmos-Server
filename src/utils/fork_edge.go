package utils

// Fork addition: what the request path knows for the blocks at Cloudflare's
// edge (src/edgeblock).
//
// Only a visitor whose address Cloudflare's proxy reported for a hostname
// served through it can be blocked at Cloudflare: for anyone else a rule at
// Cloudflare would do nothing (a direct visitor never passes it; Fail2ban is
// the tool there) or would act on an address somebody made up. Events made
// from a request carry that as SecurityEvent.Edge; for what is decided
// without a request (SmartShield's strikes and bans) the addresses
// Cloudflare reported are remembered here for a day, bounded.

import (
	"net/http"
	"sync"
	"time"
)

var edgeClients = struct {
	sync.Mutex
	seen map[string]int64
}{seen: map[string]int64{}}

func noteEdgeClient(ip string) {
	now := time.Now().Unix()
	e := &edgeClients
	e.Lock()
	defer e.Unlock()
	if _, known := e.seen[ip]; !known && len(e.seen) >= forwardedClientsMax {
		limit := now - int64(forwardedClientsFor/time.Second)
		for address, at := range e.seen {
			if at < limit {
				delete(e.seen, address)
			}
		}
		// still full: the older half goes
		if len(e.seen) >= forwardedClientsMax {
			sum := int64(0)
			for _, at := range e.seen {
				sum += at
			}
			mean := sum / int64(len(e.seen))
			for address, at := range e.seen {
				if at <= mean && len(e.seen) > forwardedClientsMax/2 {
					delete(e.seen, address)
				}
			}
		}
	}
	e.seen[ip] = now
}

// WasEdgeClient reports whether Cloudflare's proxy reported an address as
// the visitor of a shielded hostname within the last day.
func WasEdgeClient(ip string) bool {
	e := &edgeClients
	e.Lock()
	defer e.Unlock()
	at, known := e.seen[ip]
	return known && time.Now().Unix()-at < int64(forwardedClientsFor/time.Second)
}

// IsEdgeVisitor reports whether client (the address a request counts as
// coming from) is the visitor Cloudflare's proxy reported: the peer is in
// Cloudflare's ranges and believed, the hostname is shielded, and the
// address is the one CF-Connecting-IP carried.
func IsEdgeVisitor(req *http.Request, client string) bool {
	if req == nil || client == "" {
		return false
	}
	cf := GetMainConfig().HTTPConfig.Cloudflare
	peer, _ := SplitIP(req.RemoteAddr)
	if !cf.TrustRanges || !IsCloudflareAddress(cf, peer) {
		return false
	}
	return edgeVisitor(req, cf, ShieldedIn(cf.Shielded, req.Host)) == client
}

// ForkTestNoteEdgeClient is noteEdgeClient for the tests of other packages.
func ForkTestNoteEdgeClient(ip string) { noteEdgeClient(ip) }
