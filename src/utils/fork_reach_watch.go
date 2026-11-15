package utils

// Fork addition: the watch of the server's reachability from the internet
// (src/reachwatch) needs two things the request path owns:
//
//   - its settings, HTTPConfig.Cloudflare.Watch: inside the Cloudflare block,
//     which only the fork's own routes write (a full settings save puts the
//     stored block back), so a stale settings page cannot switch it;
//   - the proof that one of its requests came back in from outside. Before
//     each request the watch draws a random token for the name it tests and
//     sends it in the header X-Cosmos-Watch to https://<name>:<port>/_health.
//     Upstream's /_health hands every request to NoteWatchProbe: a token is
//     marked arrived only when the header holds exactly a pending token
//     (compared in constant time with each of them), the request is for that
//     token's name and the connection comes from Cloudflare's ranges. The
//     answer of /_health does not change in any case: there is nothing
//     secret in it and nothing for a stranger to learn or to claim. A token
//     lives a minute and arrives once.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

// ForkReachWatchConfig is HTTPConfig.Cloudflare.Watch. The zero value is the
// default: on, every ReachWatchDefaultMinutes.
type ForkReachWatchConfig struct {
	Off     bool `json:"Off,omitempty"`
	Minutes int  `json:"Minutes,omitempty"`
}

// The bounds of the interval between rounds, in minutes.
const (
	ReachWatchDefaultMinutes = 5
	ReachWatchMinMinutes     = 2
	ReachWatchMaxMinutes     = 60
)

// ReachWatchInterval is the interval in force: the stored one within the
// bounds, else the default.
func ReachWatchInterval(c ForkReachWatchConfig) time.Duration {
	m := c.Minutes
	if m == 0 {
		m = ReachWatchDefaultMinutes
	}
	if m < ReachWatchMinMinutes {
		m = ReachWatchMinMinutes
	}
	if m > ReachWatchMaxMinutes {
		m = ReachWatchMaxMinutes
	}
	return time.Duration(m) * time.Minute
}

// ReachWatchHeader carries the token of a request of the watch.
const ReachWatchHeader = "X-Cosmos-Watch"

const (
	watchTokenLife = time.Minute
	watchTokensMax = 64
)

type watchToken struct {
	value   [16]byte
	host    string
	expires time.Time
	arrived bool
	visitor string // the address Cloudflare reported for the request
}

var watchTokens = struct {
	sync.Mutex
	list []*watchToken
	now  func() time.Time
}{now: time.Now}

func pruneWatchTokens(now time.Time) {
	kept := watchTokens.list[:0]
	for _, t := range watchTokens.list {
		if now.Before(t.expires) {
			kept = append(kept, t)
		}
	}
	watchTokens.list = kept
}

// NewWatchToken draws a token for a request of the watch to host. It returns
// "" when too many are pending (the watch never has more than a few).
func NewWatchToken(host string) string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return ""
	}
	watchTokens.Lock()
	defer watchTokens.Unlock()
	now := watchTokens.now()
	pruneWatchTokens(now)
	if len(watchTokens.list) >= watchTokensMax {
		return ""
	}
	watchTokens.list = append(watchTokens.list, &watchToken{value: value, host: CloudflareHostname(host), expires: now.Add(watchTokenLife)})
	return hex.EncodeToString(value[:])
}

// WatchTokenArrived reports whether the request that carried the token came
// in, and forgets the token. visitor is the address Cloudflare reported for
// it: the server's public address as the internet sees it.
func WatchTokenArrived(token string) (arrived bool, visitor string) {
	value, ok := decodeWatchToken(token)
	if !ok {
		return false, ""
	}
	watchTokens.Lock()
	defer watchTokens.Unlock()
	for i, t := range watchTokens.list {
		if t.value == value {
			watchTokens.list = append(watchTokens.list[:i], watchTokens.list[i+1:]...)
			return t.arrived, t.visitor
		}
	}
	return false, ""
}

func decodeWatchToken(text string) ([16]byte, bool) {
	var value [16]byte
	if len(text) != 32 {
		return value, false
	}
	if _, err := hex.Decode(value[:], []byte(text)); err != nil {
		return value, false
	}
	return value, true
}

// NoteWatchProbe looks at a request to /_health. It never changes the answer.
func NoteWatchProbe(req *http.Request) {
	if req == nil {
		return
	}
	header := req.Header.Values(ReachWatchHeader)
	if len(header) != 1 {
		return
	}
	value, ok := decodeWatchToken(header[0])
	if !ok {
		return
	}
	peer, _ := SplitIP(req.RemoteAddr)
	if !IsCloudflareIP(peer) {
		return
	}
	host := CloudflareHostname(req.Host)

	watchTokens.Lock()
	defer watchTokens.Unlock()
	now := watchTokens.now()
	var match *watchToken
	// every pending token is compared, so the time taken does not tell
	// how much of a guess was right
	for _, t := range watchTokens.list {
		if subtle.ConstantTimeCompare(t.value[:], value[:]) == 1 && now.Before(t.expires) && match == nil {
			match = t
		}
	}
	if match == nil || match.arrived || match.host != host {
		return
	}
	match.arrived = true
	if visitor, isString := req.Context().Value("ClientID").(string); isString && visitor != peer {
		match.visitor = visitor
	}
}
