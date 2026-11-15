package main

import (
	"strings"
	"testing"
)

// the panel policy of work/audit2 (src/fork_csp.go, forkPanelPolicy) as it is there
const auditPanelPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; font-src 'self'; connect-src 'self' https: http: wss: ws:; media-src 'self'; worker-src 'self'; manifest-src 'self'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func directives(policy string) map[string][]string {
	out := map[string][]string{}
	for _, part := range strings.Split(policy, ";") {
		fields := strings.Fields(part)
		if len(fields) > 0 {
			out[fields[0]] = fields[1:]
		}
	}
	return out
}

func TestTurnstileCSPAddsTheOriginOnlyWhereNeeded(t *testing.T) {
	got := forkTurnstileCSP(auditPanelPolicy, true)
	before, after := directives(auditPanelPolicy), directives(got)
	if len(before) != len(after) {
		t.Fatalf("directives added or lost: %q", got)
	}
	if strings.Join(after["script-src"], " ") != "'self' "+TurnstileOrigin {
		t.Errorf("script-src = %v", after["script-src"])
	}
	if strings.Join(after["frame-src"], " ") != TurnstileOrigin {
		t.Errorf("frame-src = %v ('none' must go, the origin alone stays)", after["frame-src"])
	}
	for name, sources := range before {
		if name == "script-src" || name == "frame-src" {
			continue
		}
		if strings.Join(sources, " ") != strings.Join(after[name], " ") {
			t.Errorf("%s changed: %v -> %v", name, sources, after[name])
		}
	}
	if strings.Contains(got, ";;") || strings.HasPrefix(got, " ") {
		t.Errorf("malformed: %q", got)
	}
}

func TestTurnstileCSPChangesNothingWhenOff(t *testing.T) {
	if got := forkTurnstileCSP(auditPanelPolicy, false); got != auditPanelPolicy {
		t.Errorf("changed while off: %q", got)
	}
	if got := forkTurnstileCSP("", true); got != "" {
		t.Errorf("an empty policy became %q", got)
	}
}

func TestTurnstileCSPIsIdempotent(t *testing.T) {
	once := forkTurnstileCSP(auditPanelPolicy, true)
	if twice := forkTurnstileCSP(once, true); twice != once {
		t.Errorf("applied twice: %q != %q", twice, once)
	}
	// upstream's own policy names neither directive: nothing to add
	if got := forkTurnstileCSP("frame-ancestors 'self'", true); got != "frame-ancestors 'self'" {
		t.Errorf("upstream's policy changed: %q", got)
	}
}
