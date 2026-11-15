// Package edgeblock is a fork addition: abusers who come through
// Cloudflare's proxy are blocked at Cloudflare, with IP Access Rules of the
// zone, for a limited time.
//
// Fail2ban cannot act on such a visitor (its packets are Cloudflare's), and
// SmartShield refuses it only inside Cosmos, in memory. This package counts
// the same offences the Fail2ban jails count, with the jails' thresholds,
// but only for visitors whose address Cloudflare's proxy reported for a
// hostname served through it (utils.SecurityEvent.Edge), and asks Cloudflare
// to challenge or block the address (an IPv6 /64) when one is crossed.
//
//   - The state survives a restart: one file of its own in the
//     configuration folder (state.go), bounded.
//   - Rules have no expiry at Cloudflare: Cosmos deletes them when their
//     time is up, and reconciles what Cloudflare holds with its record,
//     touching only rules whose note carries its marker and whose id it
//     has on record (worker.go).
//   - Never the owner and never the infrastructure (exempt.go).
//   - Capped: rules kept, new blocks an hour, calls a minute, back-off.
//   - Nothing of it runs on a request's path: the subscriber to the
//     security record only counts and queues; one worker talks to
//     Cloudflare.
//
// Off until the administrator switches it on, after a test with the zone's
// token (api.go). The token needs "Zone > Firewall Services > Edit", which
// the "Edit zone DNS" token does not include.
package edgeblock

import (
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Limits.
const (
	MaxRules        = 500 // rules Cosmos keeps at Cloudflare at once
	MaxBlocksAnHour = 60  // new automatic blocks
	maxEntries      = 1000
	maxOffenders    = 4096
	maxHits         = 32
	maxAdmins       = 64
	historyFor      = 30 * 24 * time.Hour
	repeatWindow    = 7 * 24 * time.Hour
	minMaxRetry     = 2 // never on the strength of one request
)

// Rule states.
const (
	StatePending  = "pending"  // to be made at Cloudflare
	StateActive   = "active"   // made, on record
	StateDeleting = "deleting" // to be deleted at Cloudflare
	StateGone     = "gone"     // deleted at Cloudflare by somebody else: not made again until its end
	StateOwner    = "owner"    // a rule for the value that Cosmos did not make: left alone
	StateRefused  = "refused"  // Cloudflare refused the value
	StateOrphaned = "orphaned" // the zone has no token any more: the rule stays at Cloudflare
)

// Mode of a rule at Cloudflare.
const (
	ModeBlock     = "block"
	ModeChallenge = "managed_challenge"
)

// Rule is one rule Cosmos made or wants at Cloudflare, in one zone.
type Rule struct {
	Key    string    `json:"key"` // an IPv4 address or an IPv6 /64
	Zone   string    `json:"zone"`
	RuleID string    `json:"ruleId,omitempty"`
	Mode   string    `json:"mode"`
	Reason string    `json:"reason"` // auth, probe, scan, abuse, repeat, manual, test
	State  string    `json:"state"`
	Since  time.Time `json:"since"`
	Until  time.Time `json:"until"`
	Actor  string    `json:"actor,omitempty"`
	// Address is the address whose offences led to it (for a /64 one of them)
	Address string `json:"address,omitempty"`
	// Why is what is lifted when a deletion completes (admin, all, off, expired, admin_seen, evicted)
	Why string `json:"why,omitempty"`
	// Note: the last trouble with this rule, a fixed word
	Note string `json:"note,omitempty"`
}

// Manual reports whether an administrator made the rule.
func (r *Rule) Manual() bool { return r.Reason == "manual" }

type offender struct {
	Hits   map[string][]time.Time `json:"hits,omitempty"`
	Blocks []time.Time            `json:"blocks,omitempty"`
	Last   time.Time              `json:"last"`
}

// jail is a counter of one family of offences.
type jail struct {
	name     string   // as the edge names it
	f2b      string   // the Fail2ban jail whose thresholds it uses
	kinds    []string // the same kinds as the jail's filter
	mode     string   // of the first block
	defaults utils.Fail2banJailConfig
}

var jails = []jail{
	{"auth", "cosmos-auth", []string{utils.SecLoginFailed, utils.SecRegisterFailed, utils.SecMFAFailed, utils.SecMFALocked, utils.SecSudoFailed, utils.SecTokenInvalid, utils.SecTurnstileFailed}, ModeChallenge, utils.Fail2banJailConfig{MaxRetry: 5, FindTime: 3600, BanTime: 14400}},
	{"probe", "cosmos-probe", []string{utils.SecScanProbe}, ModeBlock, utils.Fail2banJailConfig{MaxRetry: 3, FindTime: 3600, BanTime: 86400}},
	{"scan", "cosmos-scan", []string{utils.SecShieldHostname, utils.SecShieldBot}, ModeChallenge, utils.Fail2banJailConfig{MaxRetry: 10, FindTime: 600, BanTime: 14400}},
	{"abuse", "cosmos-abuse", []string{utils.SecShieldStrike, utils.SecShieldBanTemp, utils.SecShieldBanPerm}, ModeChallenge, utils.Fail2banJailConfig{MaxRetry: 3, FindTime: 86400, BanTime: 14400}},
}

// the repeat offenders: Fail2ban's recidive, over a week (see the plan)
var repeatDefaults = utils.Fail2banJailConfig{MaxRetry: 3, FindTime: int(repeatWindow / time.Second), BanTime: 604800}

func jailOfKind(kind string) (jail, bool) {
	for _, j := range jails {
		for _, k := range j.kinds {
			if k == kind {
				return j, true
			}
		}
	}
	return jail{}, false
}

// thresholds are the jail's as the owner set them for Fail2ban, bounded
// the way the Fail2ban panel bounds them, and never below two events.
func thresholds(name string, defaults utils.Fail2banJailConfig) utils.Fail2banJailConfig {
	set, ok := utils.GetMainConfig().Fail2ban.Jails[name]
	if !ok || set.MaxRetry < 1 || set.MaxRetry > 10 || set.FindTime < 600 || set.FindTime > 7*86400 || set.BanTime < 300 || set.BanTime > 31*86400 {
		set = defaults
	}
	if set.MaxRetry < minMaxRetry {
		set.MaxRetry = minMaxRetry
	}
	return set
}

func repeatThresholds() utils.Fail2banJailConfig {
	set := thresholds("recidive", repeatDefaults)
	// a week to look back over, whatever recidive's own window
	set.FindTime = int(repeatWindow / time.Second)
	return set
}

// Engine is the state of the feature: what is counted, what is at
// Cloudflare, what has to be done. One exists (std); the tests make their own.
type Engine struct {
	mu  sync.Mutex
	st  state
	now func() time.Time

	dirty   bool
	savedAt time.Time
	path    func() string

	hourly bucket // new automatic blocks
	calls  bucket // calls to Cloudflare

	// per zone: when calls may be made again, and why they paused
	pausedUntil map[string]time.Time
	problems    map[string]Problem
	backoff     map[string]time.Duration
	reconciled  map[string]time.Time
	zoneIDs     map[string]string
	leftovers   map[string][]Leftover

	wake chan struct{}
}

// Problem is a lasting trouble with a zone, for the card.
type Problem struct {
	Zone   string    `json:"zone"`
	Reason string    `json:"reason"` // permission, unreachable, rate_limited, server, invalid_token, other
	Since  time.Time `json:"since"`
	Until  time.Time `json:"until,omitzero"`
}

// Leftover is a rule at Cloudflare that carries Cosmos' marker but is not on
// record (the state file was lost): Cosmos does not touch it.
type Leftover struct {
	Zone   string    `json:"zone"`
	RuleID string    `json:"ruleId"`
	Value  string    `json:"value"`
	Mode   string    `json:"mode"`
	Since  time.Time `json:"since"`
}

func newEngine(path func() string, now func() time.Time) *Engine {
	e := &Engine{
		now:         now,
		path:        path,
		pausedUntil: map[string]time.Time{},
		problems:    map[string]Problem{},
		backoff:     map[string]time.Duration{},
		reconciled:  map[string]time.Time{},
		zoneIDs:     map[string]string{},
		leftovers:   map[string][]Leftover{},
		wake:        make(chan struct{}, 1),
	}
	e.hourly = bucket{size: MaxBlocksAnHour, every: time.Hour / MaxBlocksAnHour}
	e.calls = bucket{size: 10, every: 3 * time.Second} // 20 a minute
	e.st = emptyState()
	return e
}

func settings() utils.ForkEdgeBlocksConfig {
	return utils.GetMainConfig().HTTPConfig.Cloudflare.EdgeBlocks
}

// enabled reports whether blocks are made at all.
func enabled() bool { return settings().Enabled }

func (e *Engine) poke() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// Observe counts an event of the security record. It never waits for
// Cloudflare.
func (e *Engine) Observe(event utils.SecurityEvent) {
	if !event.Edge || event.IP == "" || event.Class != "incident" || !enabled() {
		return
	}
	j, counted := jailOfKind(event.Kind)
	if !counted {
		return
	}
	key, ok := KeyOf(event.IP)
	if !ok {
		return
	}
	at := event.Time
	now := e.now()
	if at.IsZero() || at.After(now) {
		at = now
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if exemptReason(key, e.st.Admins, now) != "" {
		return
	}
	if e.coveredLocked(key, now) {
		return
	}
	o := e.offenderLocked(key, now)
	limits := thresholds(j.f2b, j.defaults)
	window := time.Duration(limits.FindTime) * time.Second
	hits := []time.Time{}
	for _, hit := range o.Hits[j.name] {
		if now.Sub(hit) < window {
			hits = append(hits, hit)
		}
	}
	// a summary row stands for several events, as one line does for Fail2ban
	hits = append(hits, at)
	if len(hits) > maxHits {
		hits = hits[len(hits)-maxHits:]
	}
	o.Hits[j.name] = hits
	e.dirty = true
	if len(hits) < limits.MaxRetry {
		return
	}
	// the block uses up the counts that led to it
	delete(o.Hits, j.name)
	e.issueLocked(key, event.IP, j, limits, now)
}

// coveredLocked reports whether a key already has a rule that counts: one
// being made or in force, the owner's own, or one somebody deleted at
// Cloudflare (respected until its end).
func (e *Engine) coveredLocked(key string, now time.Time) bool {
	for _, r := range e.st.Rules {
		if r.Key != key || !r.Until.After(now) {
			continue
		}
		switch r.State {
		case StatePending, StateActive, StateGone, StateOwner, StateOrphaned:
			return true
		}
	}
	return false
}

func (e *Engine) offenderLocked(key string, now time.Time) *offender {
	o, ok := e.st.Offenders[key]
	if !ok {
		if len(e.st.Offenders) >= maxOffenders {
			// the least recently active goes
			oldest, oldestAt := "", now
			for k, other := range e.st.Offenders {
				if other.Last.Before(oldestAt) || oldest == "" {
					oldest, oldestAt = k, other.Last
				}
			}
			delete(e.st.Offenders, oldest)
		}
		o = &offender{Hits: map[string][]time.Time{}}
		e.st.Offenders[key] = o
	}
	if o.Hits == nil {
		o.Hits = map[string][]time.Time{}
	}
	o.Last = now
	return o
}

// issueLocked decides the block of a key that crossed a threshold.
func (e *Engine) issueLocked(key string, address string, j jail, limits utils.Fail2banJailConfig, now time.Time) {
	o := e.offenderLocked(key, now)
	recent := []time.Time{}
	for _, at := range o.Blocks {
		if now.Sub(at) < historyFor {
			recent = append(recent, at)
		}
	}
	reason, mode, length := j.name, j.mode, time.Duration(limits.BanTime)*time.Second
	repeat := repeatThresholds()
	inWindow := 0
	for _, at := range recent {
		if now.Sub(at) < time.Duration(repeat.FindTime)*time.Second {
			inWindow++
		}
	}
	if inWindow+1 >= repeat.MaxRetry {
		reason, mode, length = "repeat", ModeBlock, time.Duration(repeat.BanTime)*time.Second
	}

	if !e.hourly.take(now) {
		e.st.Dropped++
		e.st.DroppedAt = now
		e.dirty = true
		return
	}
	zones := provenZones()
	if len(zones) == 0 {
		return
	}
	if !e.makeRoomLocked(len(zones), now) {
		e.st.Dropped++
		e.st.DroppedAt = now
		e.dirty = true
		return
	}
	o.Blocks = append(recent, now)
	if len(o.Blocks) > 20 {
		o.Blocks = o.Blocks[len(o.Blocks)-20:]
	}
	until := now.Add(length)
	for _, zone := range zones {
		e.addRuleLocked(&Rule{Key: key, Zone: zone, Mode: mode, Reason: reason, State: StatePending, Since: now, Until: until, Address: address})
	}
	e.dirty = true
	recordBlocked(key, address, reason, mode, until, zones)
	e.poke()
}

// liveLocked counts the rules that are, or are about to be, at Cloudflare.
func (e *Engine) liveLocked() int {
	n := 0
	for _, r := range e.st.Rules {
		if r.State == StatePending || r.State == StateActive {
			n++
		}
	}
	return n
}

// makeRoomLocked makes room for n new rules under MaxRules: the automatic
// rules that end soonest go first; manual ones never make room.
func (e *Engine) makeRoomLocked(n int, now time.Time) bool {
	for e.liveLocked()+n > MaxRules {
		var soonest *Rule
		for _, r := range e.st.Rules {
			if (r.State == StatePending || r.State == StateActive) && !r.Manual() && (soonest == nil || r.Until.Before(soonest.Until)) {
				soonest = r
			}
		}
		if soonest == nil {
			return false
		}
		e.liftLocked(soonest, "evicted")
	}
	return true
}

// addRuleLocked adds an entry; the oldest finished entries make room.
func (e *Engine) addRuleLocked(r *Rule) {
	if len(e.st.Rules) >= maxEntries {
		sort.SliceStable(e.st.Rules, func(i, j int) bool { return e.st.Rules[i].Until.Before(e.st.Rules[j].Until) })
		for i, other := range e.st.Rules {
			if other.State == StateGone || other.State == StateOwner || other.State == StateRefused || other.State == StateOrphaned {
				e.st.Rules = append(e.st.Rules[:i], e.st.Rules[i+1:]...)
				break
			}
		}
		if len(e.st.Rules) >= maxEntries {
			return
		}
	}
	e.st.Rules = append(e.st.Rules, r)
}

// liftLocked ends a rule: one never sent is dropped, one at Cloudflare is
// deleted by the worker.
func (e *Engine) liftLocked(r *Rule, why string) {
	switch r.State {
	case StatePending:
		e.removeLocked(r)
	case StateActive:
		r.State, r.Why = StateDeleting, why
	case StateGone, StateOwner, StateRefused, StateOrphaned:
		if why != "expired" && why != "evicted" {
			e.removeLocked(r)
		}
	}
	e.dirty = true
	e.poke()
}

func (e *Engine) removeLocked(r *Rule) {
	for i, other := range e.st.Rules {
		if other == r {
			e.st.Rules = append(e.st.Rules[:i], e.st.Rules[i+1:]...)
			return
		}
	}
}

// NoteAdmin remembers that an administrator's session made a request from
// an address; a rule Cosmos made for it is lifted at once.
func (e *Engine) NoteAdmin(ip string) {
	key, ok := KeyOf(ip)
	if !ok {
		return
	}
	if addr, err := netip.ParseAddr(ip); err != nil || !addr.Unmap().IsGlobalUnicast() || isLocalAddress(ip) {
		return
	}
	now := e.now()
	e.mu.Lock()
	defer e.mu.Unlock()
	last, known := e.st.Admins[key]
	if known && now.Sub(last) < time.Minute {
		return
	}
	if !known && len(e.st.Admins) >= maxAdmins {
		oldest, oldestAt := "", now
		for k, at := range e.st.Admins {
			if oldest == "" || at.Before(oldestAt) {
				oldest, oldestAt = k, at
			}
		}
		delete(e.st.Admins, oldest)
	}
	e.st.Admins[key] = now
	if !known || now.Sub(last) > time.Hour {
		e.dirty = true
	}
	delete(e.st.Offenders, key)
	for _, r := range append([]*Rule(nil), e.st.Rules...) {
		if r.Key == key && !r.Manual() && (r.State == StatePending || r.State == StateActive) {
			e.liftLocked(r, "admin_seen")
		}
	}
}

// provenZones are the zones blocks are made in: configured with a token,
// shielded names under them, and a passed test with that same token.
func provenZones() []string {
	proven := map[string]string{}
	for _, proof := range settings().Proven {
		proven[proof.Zone] = proof.Token
	}
	zones := []string{}
	for _, zone := range edgeZones() {
		if zone.Shielded > 0 && proven[zone.Name] != "" && proven[zone.Name] == zone.TokenTag {
			zones = append(zones, zone.Name)
		}
	}
	sort.Strings(zones)
	return zones
}

// bucket is a token bucket.
type bucket struct {
	size   int
	every  time.Duration
	tokens float64
	at     time.Time
	full   bool
}

func (b *bucket) refill(now time.Time) {
	if !b.full {
		b.tokens, b.at, b.full = float64(b.size), now, true
		return
	}
	if now.After(b.at) {
		b.tokens += float64(now.Sub(b.at)) / float64(b.every)
		if b.tokens > float64(b.size) {
			b.tokens = float64(b.size)
		}
		b.at = now
	}
}

func (b *bucket) take(now time.Time) bool {
	b.refill(now)
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

var std = newEngine(statePath, time.Now)
