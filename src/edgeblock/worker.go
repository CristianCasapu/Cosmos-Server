package edgeblock

// The one goroutine that talks to Cloudflare. It wakes when there is work and
// every few seconds, and in each round:
//
//   - ends what is due: a rule whose time is up is deleted; a rule for an
//     address that became exempt (an administrator signed in from it) too;
//     a rule of a zone that lost its token becomes "orphaned";
//   - reconciles at start and every ten minutes: the IP Access Rules whose
//     note carries this server's marker, or the domain's custom rules; a rule
//     on record that is not there any more was deleted by somebody and is not
//     made again; a rule with the marker that is not on record is shown,
//     never touched;
//   - makes one round of calls at a time within a bucket of 10 refilled at 20
//     a minute: deletions first, then the custom rules of a domain whose set
//     changed (one call per changed rule), reconciliations, creations; a
//     refusal for lack of the permission pauses the domain and says which; a
//     429 stops every call for as long as Cloudflare asks (five minutes when
//     it does not say); an unreachable Cloudflare pauses the domain from 30
//     seconds doubling to 15 minutes.

import (
	"context"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/cloudflare"
	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	reconcileEvery = 10 * time.Minute
	tick           = 5 * time.Second
	callBudget     = 30 * time.Second
	firstBackoff   = 30 * time.Second
	maxBackoff     = 15 * time.Minute
	rateLimitWait  = 5 * time.Minute
	permissionWait = 10 * time.Minute
	// a rule just made may not be listed yet: it is not taken for deleted
	listGrace = 2 * time.Minute
)

// replaced by the tests
var (
	edgeZones     = cloudflare.EdgeZones
	zoneAccountOf = cloudflare.ZoneAccount
	clientOf      = func(token string) ruleAPI { return cloudflare.NewClient(token) }
)

// ruleAPI is what the worker needs of Cloudflare's client.
type ruleAPI interface {
	ListAccessRules(ctx context.Context, scope string, notes string, value string) ([]cloudflare.AccessRule, bool, error)
	CreateAccessRule(ctx context.Context, scope string, key string, mode string, notes string) (cloudflare.AccessRule, error)
	DeleteAccessRule(ctx context.Context, scope string, ruleID string) error
	CanDeleteAccessRules(ctx context.Context, scope string) string
	CustomEntrypoint(ctx context.Context, zoneID string) (*cloudflare.Ruleset, error)
	CreateCustomEntrypoint(ctx context.Context, zoneID string, rule cloudflare.RulesetRule) (*cloudflare.Ruleset, error)
	AddCustomRule(ctx context.Context, zoneID string, rulesetID string, rule cloudflare.RulesetRule) (*cloudflare.Ruleset, error)
	EditCustomRule(ctx context.Context, zoneID string, rulesetID string, ruleID string, rule cloudflare.RulesetRule) error
	DeleteCustomRule(ctx context.Context, zoneID string, rulesetID string, ruleID string) error
}

// job is one round of calls.
type job struct {
	kind string // create, delete, reconcile, waf, test
	rule *Rule
	via  string
	zone cloudflare.EdgeZone
	test testRule
}

var wafModes = []string{ModeChallenge, ModeBlock}

// noteOfLocked is the note of an IP Access Rule: what it is, the marker, until when.
func (e *Engine) noteOfLocked(r *Rule) string {
	return "Cosmos edge block [" + e.st.markerText() + "] " + r.Reason + ", until " + r.Until.UTC().Format("2006-01-02 15:04 UTC")
}

// wafDescription is the description of Cosmos' custom rule of a mode.
func wafDescription(marker string, mode string) string {
	word := "block"
	if mode == ModeChallenge {
		word = "challenge"
	}
	return "Cosmos edge blocks [" + marker + "] " + word
}

// wafExpression is the expression of a set of keys.
func wafExpression(keys []string) string {
	return "(ip.src in {" + strings.Join(keys, " ") + "})"
}

var wafSet = regexp.MustCompile(`ip\.src in \{([^{}]*)\}`)

// wafKeys reads the keys of an expression Cosmos wrote (and the owner may
// have edited).
func wafKeys(expression string) map[string]bool {
	keys := map[string]bool{}
	match := wafSet.FindStringSubmatch(expression)
	if match == nil {
		return keys
	}
	for _, item := range strings.Fields(match[1]) {
		if strings.Contains(item, "/") {
			if prefix, err := netip.ParsePrefix(item); err == nil {
				keys[prefix.Masked().String()] = true
			}
			continue
		}
		if key, ok := KeyOf(item); ok {
			keys[key] = true
		}
	}
	return keys
}

// run is the worker's loop.
func (e *Engine) run(stop <-chan struct{}) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		for e.step(context.Background()) {
		}
		e.save(false)
		select {
		case <-stop:
			e.save(true)
			return
		case <-ticker.C:
		case <-e.wake:
		}
	}
}

// ids returns the zone id (and account id) of a domain, asking Cloudflare
// once.
func (e *Engine) ids(ctx context.Context, zone cloudflare.EdgeZone) (string, string, error) {
	e.mu.Lock()
	zoneID, accountID := e.zoneIDs[zone.Name], e.accountIDs[zone.Name]
	e.mu.Unlock()
	if zoneID != "" {
		return zoneID, accountID, nil
	}
	zoneID, accountID, err := zoneAccountOf(ctx, zone.Token, zone.Name)
	if err != nil {
		return "", "", err
	}
	e.mu.Lock()
	e.zoneIDs[zone.Name], e.accountIDs[zone.Name] = zoneID, accountID
	e.mu.Unlock()
	return zoneID, accountID, nil
}

// scopeOf is where an IP Access Rule of a domain lives.
func scopeOf(via string, zoneID string, accountID string) string {
	if via == ViaAccount {
		return cloudflare.AccountScope(accountID)
	}
	return zoneID
}

// step does what is due and at most one round of calls; it reports whether
// it made one (and so whether another round may follow at once).
func (e *Engine) step(ctx context.Context) bool {
	now := e.now()
	zones := map[string]cloudflare.EdgeZone{}
	for _, zone := range edgeZones() {
		zones[zone.Name] = zone
	}
	on := enabled()
	targets := map[string]string{}
	if on {
		for _, t := range provenTargets() {
			targets[t.Zone] = t.Via
		}
	}

	e.mu.Lock()
	e.housekeepLocked(now, zones, on)
	next, ok := e.nextJobLocked(now, zones, on, targets)
	if !ok || !e.calls.take(now) {
		e.mu.Unlock()
		return false
	}
	e.mu.Unlock()

	callCtx, cancel := context.WithTimeout(ctx, callBudget)
	defer cancel()
	zoneID, accountID, err := e.ids(callCtx, next.zone)
	if err != nil {
		e.failed(next.zone.Name, next.via, err)
		return true
	}
	api := clientOf(next.zone.Token)
	scope := scopeOf(next.via, zoneID, accountID)
	if next.via == ViaAccount && accountID == "" {
		e.failed(next.zone.Name, next.via, &cloudflare.APIError{Kind: "not_found", Message: "the account of the domain is not known"})
		return true
	}

	switch next.kind {
	case "reconcile":
		e.reconcile(callCtx, api, next.zone.Name, next.via, scope)
	case "waf":
		e.syncWAF(callCtx, api, next.zone.Name, zoneID)
	case "test":
		var err error
		if next.test.Via == ViaWAF {
			err = api.DeleteCustomRule(callCtx, zoneID, next.test.RulesetID, next.test.RuleID)
		} else {
			err = api.DeleteAccessRule(callCtx, next.test.Scope, next.test.RuleID)
		}
		if err != nil {
			e.failed(next.zone.Name, next.test.Via, err)
			return true
		}
		e.mu.Lock()
		kept := []testRule{}
		for _, t := range e.st.Leftover {
			if t != next.test {
				kept = append(kept, t)
			}
		}
		e.st.Leftover, e.dirty = kept, true
		e.mu.Unlock()
	case "delete":
		e.mu.Lock()
		ruleID := next.rule.RuleID
		e.mu.Unlock()
		if err := api.DeleteAccessRule(callCtx, scope, ruleID); err != nil {
			e.failed(next.zone.Name, next.via, err)
			return true
		}
		e.mu.Lock()
		e.removeLocked(next.rule)
		e.dirty = true
		why := next.rule.Why
		e.mu.Unlock()
		e.fine(next.zone.Name)
		recordLifted(next.rule, why)
	case "create":
		e.mu.Lock()
		key, mode, note := next.rule.Key, next.rule.Mode, e.noteOfLocked(next.rule)
		e.mu.Unlock()
		rule, err := api.CreateAccessRule(callCtx, scope, key, mode, note)
		if err == nil {
			e.mu.Lock()
			if e.holdsLocked(next.rule) && next.rule.State == StatePending {
				next.rule.RuleID, next.rule.State, next.rule.Note = rule.ID, StateActive, ""
			} else {
				// lifted while the call was on its way: delete what was made
				next.rule.RuleID = rule.ID
				next.rule.State = StateDeleting
				if !e.holdsLocked(next.rule) {
					e.st.Rules = append(e.st.Rules, next.rule)
				}
			}
			e.dirty = true
			e.mu.Unlock()
			e.fine(next.zone.Name)
			return true
		}
		kind, status, _ := cloudflare.ErrorKind(err)
		if status == 400 && kind != "malformed_token" {
			// a rule for this value exists already, or the value was refused
			e.explainRefusal(callCtx, api, scope, next.rule)
			return true
		}
		e.failed(next.zone.Name, next.via, err)
	}
	return true
}

func (e *Engine) holdsLocked(r *Rule) bool {
	for _, other := range e.st.Rules {
		if other == r {
			return true
		}
	}
	return false
}

// housekeepLocked applies what is due without calling anybody.
func (e *Engine) housekeepLocked(now time.Time, zones map[string]cloudflare.EdgeZone, on bool) {
	before := e.fingerprintLocked()
	defer func() {
		if e.fingerprintLocked() != before {
			e.dirty = true
		}
	}()
	for _, r := range append([]*Rule(nil), e.st.Rules...) {
		_, hasZone := zones[r.Zone]
		atCloudflare := r.RuleID != "" || r.how() == ViaWAF
		switch r.State {
		case StatePending:
			switch {
			case !on && r.Reason != "test":
				e.removeLocked(r)
			case !hasZone || !r.Until.After(now):
				e.removeLocked(r)
			}
		case StateActive:
			switch {
			case !hasZone:
				r.State, r.Note = StateOrphaned, ""
			case !on:
				r.State, r.Why = StateDeleting, "off"
			case !r.Until.After(now):
				r.State, r.Why = StateDeleting, "expired"
			case !r.Manual():
				if reason := exemptReason(r.Key, e.st.Admins, now); reason != "" {
					r.State, r.Why = StateDeleting, map[bool]string{true: "admin_seen", false: "exempt"}[reason == ExemptAdmin]
				}
			}
		case StateDeleting:
			if !hasZone {
				r.State = StateOrphaned
			}
		case StateGone, StateOwner, StateRefused:
			if !r.Until.After(now) {
				e.removeLocked(r)
			}
		case StateOrphaned:
			if hasZone && atCloudflare {
				// the token is back: delete it now
				r.State, r.Why = StateDeleting, map[bool]string{true: "expired", false: "off"}[on]
			} else if now.Sub(r.Until) > historyFor {
				e.removeLocked(r)
			}
		}
	}
}

// fingerprintLocked sums up the rules' states, to tell whether a round
// changed anything worth saving.
func (e *Engine) fingerprintLocked() string {
	var b strings.Builder
	for _, r := range e.st.Rules {
		b.WriteString(r.Key + r.Zone + r.State + r.Why + ";")
	}
	return b.String()
}

// wafDesiredLocked is what a domain's custom rules should hold: per mode,
// the keys of the rules being made or in force, sorted.
func (e *Engine) wafDesiredLocked(zone string) map[string][]string {
	desired := map[string][]string{}
	for _, mode := range wafModes {
		desired[mode] = []string{}
	}
	seen := map[string]bool{}
	for _, r := range e.st.Rules {
		if r.Zone != zone || r.how() != ViaWAF || (r.State != StatePending && r.State != StateActive) {
			continue
		}
		if !seen[r.Mode+" "+r.Key] {
			seen[r.Mode+" "+r.Key] = true
			desired[r.Mode] = append(desired[r.Mode], r.Key)
		}
	}
	for _, mode := range wafModes {
		sort.Strings(desired[mode])
	}
	return desired
}

func sameKeys(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// wafDueLocked reports whether a domain's custom rules must be written:
// their set changed, or a rule is waiting to be lifted.
func (e *Engine) wafDueLocked(zone string) bool {
	ws := e.st.WAF[zone]
	desired := e.wafDesiredLocked(zone)
	for _, mode := range wafModes {
		var sent []string
		if ws != nil {
			sent = ws.Sent[mode]
		}
		if !sameKeys(desired[mode], sent) {
			return true
		}
	}
	for _, r := range e.st.Rules {
		if r.Zone == zone && r.how() == ViaWAF && r.State == StateDeleting {
			return true
		}
	}
	return false
}

// nextJobLocked picks the next round: deletions, custom rules, then
// reconciliations that are due, then creations, in domains not paused.
func (e *Engine) nextJobLocked(now time.Time, zones map[string]cloudflare.EdgeZone, on bool, targets map[string]string) (job, bool) {
	ready := func(name string) (cloudflare.EdgeZone, bool) {
		zone, ok := zones[name]
		if !ok {
			return zone, false
		}
		if until, paused := e.pausedUntil[name]; paused && now.Before(until) {
			return zone, false
		}
		return zone, true
	}
	for _, t := range e.st.Leftover {
		if zone, ok := ready(t.Zone); ok {
			return job{kind: "test", zone: zone, via: t.Via, test: t}, true
		}
	}
	for _, r := range e.st.Rules {
		if r.State == StateDeleting && r.RuleID != "" && r.how() != ViaWAF {
			if zone, ok := ready(r.Zone); ok {
				return job{kind: "delete", rule: r, via: r.how(), zone: zone}, true
			}
		}
	}
	// what each domain is reconciled for: the ways its rules on record were
	// made, and the way it uses now
	vias := map[string]map[string]bool{}
	add := func(zone string, via string) {
		if vias[zone] == nil {
			vias[zone] = map[string]bool{}
		}
		vias[zone][via] = true
	}
	for _, r := range e.st.Rules {
		add(r.Zone, r.how())
	}
	for zone, via := range targets {
		add(zone, via)
	}
	for zone, ws := range e.st.WAF {
		if ws != nil && len(ws.Rules) > 0 {
			add(zone, ViaWAF)
		}
	}
	names := make([]string, 0, len(vias))
	for name := range vias {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if vias[name][ViaWAF] && e.wafDueLocked(name) {
			if zone, ok := ready(name); ok {
				return job{kind: "waf", via: ViaWAF, zone: zone}, true
			}
		}
	}
	for _, name := range names {
		for _, via := range []string{ViaZone, ViaWAF, ViaAccount} {
			if !vias[name][via] || now.Sub(e.reconciled[name+"|"+via]) < reconcileEvery {
				continue
			}
			if zone, ok := ready(name); ok {
				kind := "reconcile"
				if via == ViaWAF {
					kind = "waf"
				}
				return job{kind: kind, via: via, zone: zone}, true
			}
		}
	}
	if !on {
		return job{}, false
	}
	for _, r := range e.st.Rules {
		if r.State == StatePending && r.how() != ViaWAF {
			if zone, ok := ready(r.Zone); ok {
				return job{kind: "create", rule: r, via: r.how(), zone: zone}, true
			}
		}
	}
	return job{}, false
}

// reconcile compares the IP Access Rules Cloudflare holds with the record.
func (e *Engine) reconcile(ctx context.Context, api ruleAPI, zone string, via string, scope string) {
	e.mu.Lock()
	marker := e.st.markerText()
	e.mu.Unlock()
	rules, more, err := api.ListAccessRules(ctx, scope, marker, "")
	if err != nil {
		e.failed(zone, via, err)
		return
	}
	e.fine(zone)
	listed := map[string]cloudflare.AccessRule{}
	for _, rule := range rules {
		if strings.Contains(rule.Notes, marker) {
			listed[rule.ID] = rule
		}
	}
	now := e.now()
	lifted := []*Rule{}

	e.mu.Lock()
	e.reconciled[zone+"|"+via] = now
	known := map[string]bool{}
	for _, r := range e.st.Rules {
		if r.RuleID != "" && r.how() == via {
			known[r.RuleID] = true
		}
	}
	for _, t := range e.st.Leftover {
		known[t.RuleID] = true
	}
	for _, r := range append([]*Rule(nil), e.st.Rules...) {
		if r.Zone != zone || r.RuleID == "" || r.how() != via {
			continue
		}
		_, there := listed[r.RuleID]
		if there || more {
			// with an incomplete list nothing is taken for deleted
			continue
		}
		switch r.State {
		case StateActive:
			if now.Sub(r.Since) < listGrace {
				continue
			}
			// somebody deleted it at Cloudflare: respected, not made again
			r.State, r.Note = StateGone, "removed_at_cloudflare"
			lifted = append(lifted, r)
		case StateDeleting, StateOrphaned:
			e.removeLocked(r)
		}
	}
	leftovers := []Leftover{}
	for id, rule := range listed {
		if !known[id] && len(leftovers) < 50 {
			leftovers = append(leftovers, Leftover{Zone: zone, RuleID: id, Value: rule.Configuration.Value, Mode: rule.Mode, Since: rule.CreatedOn})
		}
	}
	e.leftovers[zone+"|"+via] = leftovers
	e.dirty = true
	e.mu.Unlock()

	for _, r := range lifted {
		recordLifted(r, "removed_at_cloudflare")
	}
}

// syncWAF reads a domain's custom rules, notices what the owner deleted
// there, and writes Cosmos' two rules (at most) when their set changed: one
// call per rule that changed. It never touches a rule Cosmos did not make.
func (e *Engine) syncWAF(ctx context.Context, api ruleAPI, zone string, zoneID string) {
	e.mu.Lock()
	marker := e.st.markerText()
	desired := e.wafDesiredLocked(zone)
	ws := wafState{Rules: map[string]string{}, Sent: map[string][]string{}}
	if held := e.st.WAF[zone]; held != nil {
		ws.RulesetID = held.RulesetID
		for mode, id := range held.Rules {
			ws.Rules[mode] = id
		}
		for mode, keys := range held.Sent {
			ws.Sent[mode] = append([]string(nil), keys...)
		}
	}
	e.mu.Unlock()

	set, err := api.CustomEntrypoint(ctx, zoneID)
	if err != nil {
		e.failed(zone, ViaWAF, err)
		return
	}
	gone := map[string]map[string]bool{}
	live := map[string]*cloudflare.RulesetRule{}
	ws.RulesetID = ""
	if set != nil {
		ws.RulesetID = set.ID
	}
	for _, mode := range wafModes {
		gone[mode] = map[string]bool{}
		id := ws.Rules[mode]
		if id == "" {
			continue
		}
		var found *cloudflare.RulesetRule
		if set != nil {
			for i := range set.Rules {
				if set.Rules[i].ID == id && strings.Contains(set.Rules[i].Description, marker) {
					found = &set.Rules[i]
				}
			}
		}
		if found == nil {
			// the owner deleted Cosmos' rule: everything in it is respected
			for _, key := range ws.Sent[mode] {
				gone[mode][key] = true
			}
			delete(ws.Rules, mode)
			ws.Sent[mode] = nil
			continue
		}
		live[mode] = found
		there := wafKeys(found.Expression)
		for _, key := range ws.Sent[mode] {
			if !there[key] {
				gone[mode][key] = true // the owner took it out of the rule
			}
		}
	}

	// what to write: the desired sets without what the owner removed
	written := map[string]bool{}
	var failure error
	for _, mode := range wafModes {
		want := []string{}
		for _, key := range desired[mode] {
			if !gone[mode][key] {
				want = append(want, key)
			}
		}
		if len(want) > MaxWAFPerAction {
			want = want[:MaxWAFPerAction]
		}
		rule := cloudflare.RulesetRule{Action: mode, Expression: wafExpression(want), Description: wafDescription(marker, mode), Enabled: true}
		current := live[mode]
		switch {
		case current != nil && len(want) == 0:
			failure = api.DeleteCustomRule(ctx, zoneID, ws.RulesetID, current.ID)
			if failure == nil {
				delete(ws.Rules, mode)
			}
		case current != nil:
			liveKeys := wafKeys(current.Expression)
			same := len(liveKeys) == len(want) && current.Description == rule.Description && current.Action == mode
			for _, key := range want {
				same = same && liveKeys[key]
			}
			if same {
				break
			}
			rule.Enabled = current.Enabled // the owner may have switched it off: left so
			failure = api.EditCustomRule(ctx, zoneID, ws.RulesetID, current.ID, rule)
		case len(want) > 0:
			before := map[string]bool{}
			var made *cloudflare.Ruleset
			if set == nil {
				made, failure = api.CreateCustomEntrypoint(ctx, zoneID, rule)
			} else {
				for _, r := range set.Rules {
					before[r.ID] = true
				}
				made, failure = api.AddCustomRule(ctx, zoneID, set.ID, rule)
			}
			if failure == nil {
				for _, r := range made.Rules {
					if !before[r.ID] && r.Description == rule.Description {
						ws.Rules[mode] = r.ID
					}
				}
				set, ws.RulesetID = made, made.ID
				if ws.Rules[mode] == "" {
					failure = &cloudflare.APIError{Kind: "other", Message: "the new rule is not in the answer"}
				}
			}
		}
		if failure != nil {
			break
		}
		ws.Sent[mode] = want
		written[mode] = true
	}

	now := e.now()
	lifted := []*Rule{}
	liftedWhy := []string{}
	e.mu.Lock()
	e.st.WAF[zone] = &ws
	e.reconciled[zone+"|"+ViaWAF] = now
	for _, r := range append([]*Rule(nil), e.st.Rules...) {
		if r.Zone != zone || r.how() != ViaWAF {
			continue
		}
		if gone[r.Mode][r.Key] && (r.State == StateActive || r.State == StatePending) {
			r.State, r.Note = StateGone, "removed_at_cloudflare"
			lifted, liftedWhy = append(lifted, r), append(liftedWhy, "removed_at_cloudflare")
			continue
		}
		if gone[r.Mode][r.Key] && r.State == StateDeleting {
			e.removeLocked(r) // gone already
			continue
		}
		if !written[r.Mode] {
			continue
		}
		in := false
		for _, key := range ws.Sent[r.Mode] {
			in = in || key == r.Key
		}
		switch {
		case r.State == StatePending && in:
			r.State, r.Note = StateActive, ""
		case r.State == StateDeleting && !in:
			e.removeLocked(r)
			lifted, liftedWhy = append(lifted, r), append(liftedWhy, r.Why)
		}
	}
	e.dirty = true
	e.mu.Unlock()

	for i, r := range lifted {
		recordLifted(r, liftedWhy[i])
	}
	if failure != nil {
		e.failed(zone, ViaWAF, failure)
		return
	}
	e.fine(zone)
}

// explainRefusal finds out why a creation was refused with 400: a rule for
// the value exists (the owner's, or one with the marker that is not on
// record) or the value itself was refused.
func (e *Engine) explainRefusal(ctx context.Context, api ruleAPI, scope string, r *Rule) {
	_, value, _ := cloudflare.AccessTarget(r.Key)
	existing, _, err := api.ListAccessRules(ctx, scope, "", value)
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.holdsLocked(r) || r.State != StatePending {
		return
	}
	e.dirty = true
	if err != nil {
		// look again later
		r.Note = "lookup_failed"
		return
	}
	for _, rule := range existing {
		if rule.Configuration.Value == value {
			// not Cosmos': left alone and not adopted
			r.State, r.Note = StateOwner, "existing_rule"
			return
		}
	}
	r.State, r.Note = StateRefused, "value_refused"
}

// fine clears a zone's trouble after a call that worked.
func (e *Engine) fine(zone string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.problems, zone)
	delete(e.backoff, zone)
	delete(e.pausedUntil, zone)
}

// failed pauses a zone after a call that did not work.
func (e *Engine) failed(zone string, via string, err error) {
	kind, status, retryAfter := cloudflare.ErrorKind(err)
	now := e.now()
	e.mu.Lock()
	reason := "other"
	wait := firstBackoff
	switch {
	case kind == "forbidden":
		reason, wait = "permission", permissionWait
	case kind == "invalid_token" || kind == "malformed_token":
		reason, wait = "invalid_token", permissionWait
	case kind == "rate_limited":
		reason, wait = "rate_limited", rateLimitWait
		if retryAfter > 0 {
			wait = retryAfter
		}
	default:
		reason = map[string]string{"unreachable": "unreachable", "server": "server", "other": "other", "not_found": "not_found"}[kind]
		if reason == "" {
			reason = "other"
		}
		if status == 400 {
			reason = "refused" // Cloudflare refused what was sent (a full list of custom rules, a too long expression)
		}
		wait = e.backoff[zone] * 2
		if wait < firstBackoff {
			wait = firstBackoff
		}
		if wait > maxBackoff {
			wait = maxBackoff
		}
		e.backoff[zone] = wait
	}
	until := now.Add(wait)
	if reason == "rate_limited" {
		// Cloudflare stops every call of the account, not one zone's
		for name := range e.zoneIDs {
			e.pausedUntil[name] = until
		}
	}
	e.pausedUntil[zone] = until
	previous, had := e.problems[zone]
	since := now
	if had && previous.Reason == reason {
		since = previous.Since
	}
	e.problems[zone] = Problem{Zone: zone, Reason: reason, Via: via, Since: since, Until: until}
	e.mu.Unlock()
	if reason == "permission" || reason == "invalid_token" {
		notifyProblem(zone, reason, via, now)
	}
	utils.Debug("Cloudflare edge blocks: " + zone + ": " + reason)
}
