package edgeblock

// The one goroutine that talks to Cloudflare. It wakes when there is work and
// every few seconds, and in each round:
//
//   - ends what is due: a rule whose time is up is deleted; a rule for an
//     address that became exempt (an administrator signed in from it) too;
//     a rule of a zone that lost its token becomes "orphaned";
//   - reconciles a zone at start and every ten minutes: lists the rules whose
//     note carries this server's marker; a rule on record that is not there
//     any more was deleted by somebody and is not made again; a rule with
//     the marker that is not on record is shown, never touched;
//   - sends deletions first, then creations, one call at a time, within a
//     bucket of 10 calls refilled at 20 a minute; a refusal for lack of the
//     permission pauses the zone and says so; a 429 stops every call for as
//     long as Cloudflare asks (five minutes when it does not say); an
//     unreachable Cloudflare pauses the zone from 30 seconds doubling to 15
//     minutes.

import (
	"context"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/cloudflare"
	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	reconcileEvery = 10 * time.Minute
	tick           = 5 * time.Second
	callBudget     = 20 * time.Second
	firstBackoff   = 30 * time.Second
	maxBackoff     = 15 * time.Minute
	rateLimitWait  = 5 * time.Minute
	permissionWait = 10 * time.Minute
	// a rule just made may not be listed yet: it is not taken for deleted
	listGrace = 2 * time.Minute
)

// replaced by the tests
var (
	edgeZones = cloudflare.EdgeZones
	zoneIDOf  = cloudflare.ZoneID
	clientOf  = func(token string) ruleAPI { return cloudflare.NewClient(token) }
)

// ruleAPI is what the worker needs of Cloudflare's client.
type ruleAPI interface {
	ListAccessRules(ctx context.Context, zoneID string, notes string, value string) ([]cloudflare.AccessRule, bool, error)
	CreateAccessRule(ctx context.Context, zoneID string, key string, mode string, notes string) (cloudflare.AccessRule, error)
	DeleteAccessRule(ctx context.Context, zoneID string, ruleID string) error
	CanDeleteAccessRules(ctx context.Context, zoneID string) string
}

// job is one call to make.
type job struct {
	kind string // create, delete, reconcile
	rule *Rule
	zone cloudflare.EdgeZone
}

// noteOf is the note of a rule: what it is, the marker, until when.
func (e *Engine) noteOfLocked(r *Rule) string {
	return "Cosmos edge block [" + e.st.markerText() + "] " + r.Reason + ", until " + r.Until.UTC().Format("2006-01-02 15:04 UTC")
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

// step does what is due and at most one call; it reports whether it made a
// call (and so whether another round may follow at once).
func (e *Engine) step(ctx context.Context) bool {
	now := e.now()
	zones := map[string]cloudflare.EdgeZone{}
	for _, zone := range edgeZones() {
		zones[zone.Name] = zone
	}
	on := enabled()

	e.mu.Lock()
	e.housekeepLocked(now, zones, on)
	next, ok := e.nextJobLocked(now, zones, on)
	if !ok {
		e.mu.Unlock()
		return false
	}
	if !e.calls.take(now) {
		e.mu.Unlock()
		return false
	}
	zoneID := e.zoneIDs[next.zone.Name]
	e.mu.Unlock()

	callCtx, cancel := context.WithTimeout(ctx, callBudget)
	defer cancel()
	if zoneID == "" {
		id, err := zoneIDOf(callCtx, next.zone.Token, next.zone.Name)
		if err != nil {
			e.failed(next.zone.Name, err)
			return true
		}
		e.mu.Lock()
		e.zoneIDs[next.zone.Name] = id
		e.mu.Unlock()
		return true
	}

	api := clientOf(next.zone.Token)
	switch next.kind {
	case "reconcile":
		e.reconcile(callCtx, api, next.zone.Name, zoneID)
	case "delete":
		e.mu.Lock()
		ruleID := next.rule.RuleID
		e.mu.Unlock()
		err := api.DeleteAccessRule(callCtx, zoneID, ruleID)
		e.mu.Lock()
		if err == nil {
			e.removeLocked(next.rule)
			e.dirty = true
			why := next.rule.Why
			e.mu.Unlock()
			e.fine(next.zone.Name)
			recordLifted(next.rule, why)
			return true
		}
		e.mu.Unlock()
		e.failed(next.zone.Name, err)
	case "create":
		e.mu.Lock()
		key, mode, note := next.rule.Key, next.rule.Mode, e.noteOfLocked(next.rule)
		e.mu.Unlock()
		rule, err := api.CreateAccessRule(callCtx, zoneID, key, mode, note)
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
			e.explainRefusal(callCtx, api, zoneID, next.rule)
			return true
		}
		e.failed(next.zone.Name, err)
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
			if hasZone && r.RuleID != "" {
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

// nextJobLocked picks the next call: deletions, then reconciliations that
// are due, then creations, in zones that are not paused.
func (e *Engine) nextJobLocked(now time.Time, zones map[string]cloudflare.EdgeZone, on bool) (job, bool) {
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
	for _, r := range e.st.Rules {
		if r.State == StateDeleting && r.RuleID != "" {
			if zone, ok := ready(r.Zone); ok {
				return job{kind: "delete", rule: r, zone: zone}, true
			}
		}
	}
	// a zone is reconciled when it has rules on record or the feature is on
	zonesWithRules := map[string]bool{}
	for _, r := range e.st.Rules {
		zonesWithRules[r.Zone] = true
	}
	for name := range zones {
		if !on && !zonesWithRules[name] {
			continue
		}
		if on && !proven(name) && !zonesWithRules[name] {
			continue
		}
		if now.Sub(e.reconciled[name]) >= reconcileEvery {
			if zone, ok := ready(name); ok {
				return job{kind: "reconcile", zone: zone}, true
			}
		}
	}
	if !on {
		return job{}, false
	}
	for _, r := range e.st.Rules {
		if r.State == StatePending {
			if zone, ok := ready(r.Zone); ok {
				return job{kind: "create", rule: r, zone: zone}, true
			}
		}
	}
	return job{}, false
}

func proven(zone string) bool {
	for _, name := range provenZones() {
		if name == zone {
			return true
		}
	}
	return false
}

// reconcile compares what Cloudflare holds with the record.
func (e *Engine) reconcile(ctx context.Context, api ruleAPI, zone string, zoneID string) {
	e.mu.Lock()
	marker := e.st.markerText()
	e.mu.Unlock()
	rules, more, err := api.ListAccessRules(ctx, zoneID, marker, "")
	if err != nil {
		e.failed(zone, err)
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
	e.reconciled[zone] = now
	known := map[string]bool{}
	for _, r := range append([]*Rule(nil), e.st.Rules...) {
		if r.Zone != zone || r.RuleID == "" {
			continue
		}
		known[r.RuleID] = true
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
	e.leftovers[zone] = leftovers
	e.dirty = true
	e.mu.Unlock()

	for _, r := range lifted {
		recordLifted(r, "removed_at_cloudflare")
	}
}

// explainRefusal finds out why a creation was refused with 400: a rule for
// the value exists (the owner's, or one with the marker that is not on
// record) or the value itself was refused.
func (e *Engine) explainRefusal(ctx context.Context, api ruleAPI, zoneID string, r *Rule) {
	_, value, _ := cloudflare.AccessTarget(r.Key)
	existing, _, err := api.ListAccessRules(ctx, zoneID, "", value)
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
func (e *Engine) failed(zone string, err error) {
	kind, _, retryAfter := cloudflare.ErrorKind(err)
	now := e.now()
	e.mu.Lock()
	reason := "other"
	wait := firstBackoff
	switch kind {
	case "forbidden":
		reason, wait = "permission", permissionWait
	case "invalid_token", "malformed_token":
		reason, wait = "invalid_token", permissionWait
	case "rate_limited":
		reason, wait = "rate_limited", rateLimitWait
		if retryAfter > 0 {
			wait = retryAfter
		}
	case "unreachable", "server", "other", "not_found":
		reason = map[string]string{"unreachable": "unreachable", "server": "server", "other": "other", "not_found": "not_found"}[kind]
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
	e.problems[zone] = Problem{Zone: zone, Reason: reason, Since: since, Until: until}
	e.mu.Unlock()
	if reason == "permission" || reason == "invalid_token" {
		notifyProblem(zone, reason, now)
	}
	utils.Debug("Cloudflare edge blocks: " + zone + ": " + reason)
}
