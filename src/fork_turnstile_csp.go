package main

// Fork addition: what the panel's script policy must allow for Cloudflare
// Turnstile on the sign-in pages (src/turnstile). For the policy of
// src/fork_csp.go (branch work/audit2), which allows scripts and frames only
// from the panel itself.
//
// Cloudflare documents two ways for a strict policy
// (developers.cloudflare.com/turnstile/reference/content-security-policy/):
// a nonce on the script tag, or the origin https://challenges.cloudflare.com
// in script-src and frame-src. The panel is a static build served as files,
// with no answer templated, so there is no nonce to give: the origin it is.
// Nothing else is needed: the widget runs in a frame of Cloudflare's own
// origin (its own policy, not ours), and connect-src matters only for
// pre-clearance cookies, which the panel does not use.
//
// When: only while a site key is saved (the check is on, or being tried in
// the settings) and it was not switched off from the console. Not per page:
// the panel is one document for every page and moves between pages without
// loading a new one (the app gate's /cosmos-ui/openid goes on to the sign-in,
// logging out shows the sign-in, the settings card runs the trial), so a
// policy chosen by the path of the first page would break exactly those.
//
// Integration with work/audit2 (src/fork_csp.go, forkPanelHeaders):
//
//	policy := forkTurnstileCSP(forkPanelPolicy(forkReturnOrigins()), turnstile.ScriptAllowed())
//
// The API's own policy (forkAPIPolicy) is not concerned.

import "strings"

// TurnstileOrigin is where Turnstile's script and frame come from.
const TurnstileOrigin = "https://challenges.cloudflare.com"

// forkTurnstileCSP returns policy with Turnstile's origin added to
// script-src and frame-src when active; policy unchanged otherwise. A
// directive "'none'" becomes the origin alone; a directive that is missing
// is left missing (default-src then decides, and the policy of fork_csp.go
// names both).
func forkTurnstileCSP(policy string, active bool) string {
	if !active || policy == "" {
		return policy
	}
	parts := strings.Split(policy, ";")
	for i, part := range parts {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		name := strings.ToLower(fields[0])
		if name != "script-src" && name != "frame-src" {
			continue
		}
		sources := fields[1:]
		present := false
		kept := []string{}
		for _, source := range sources {
			if source == TurnstileOrigin {
				present = true
			}
			if source != "'none'" {
				kept = append(kept, source)
			}
		}
		if !present {
			kept = append(kept, TurnstileOrigin)
		}
		parts[i] = " " + fields[0] + " " + strings.Join(kept, " ")
		if i == 0 {
			parts[i] = strings.TrimPrefix(parts[i], " ")
		}
	}
	return strings.Join(parts, ";")
}
