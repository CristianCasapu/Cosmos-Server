package reach

import (
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/reachwatch"
	"github.com/azukaar/cosmos-server/src/utils"
)

// TestWhatTheWatchSawIsTheStrongestVerdict: seen from outside is
// "reachable" (stronger than "checked"), confirmed down is a problem, a
// name served directly says it cannot be tested from inside, and nothing
// else changes the verdict.
func TestWhatTheWatchSawIsTheStrongestVerdict(t *testing.T) {
	saved := outside
	defer func() { outside = saved }()
	seen := time.Date(2026, 11, 15, 9, 12, 0, 0, time.UTC)
	infos := map[string]reachwatch.OutsideInfo{
		"example.org":         {Watched: true, State: reachwatch.StateReachable, SeenAt: seen},
		"down.example.org":    {Watched: true, State: reachwatch.StateDown, Cause: "connect", Since: seen},
		"suspect.example.org": {Watched: true, State: reachwatch.StateSuspect, Cause: "connect"},
		"lost.example.org":    {Watched: true, State: reachwatch.StateUnknown, Cause: "offline"},
		"off.example.org":     {Watched: true, Off: true},
		"direct.example.org":  {Direct: true},
	}
	outside = func(host string) reachwatch.OutsideInfo { return infos[host] }

	check := func(r RouteResult) *Check {
		for i := range r.Checks {
			if r.Checks[i].ID == "outside" {
				return &r.Checks[i]
			}
		}
		return nil
	}
	cases := []struct {
		host, before, state, status, code string
	}{
		{"example.org", StateProblem, StateReachable, CheckOK, "seen"},
		{"example.org", StateUnverified, StateReachable, CheckOK, "seen"},
		{"down.example.org", StateVerified, StateProblem, CheckProblem, "down"},
		{"suspect.example.org", StateVerified, StateVerified, CheckNote, "pending"},
		{"lost.example.org", StateVerified, StateVerified, CheckNote, "unknown"},
		{"off.example.org", StateUnverified, StateUnverified, CheckNote, "off"},
		{"direct.example.org", StateVerified, StateVerified, CheckNote, "direct"},
	}
	for _, c := range cases {
		got := withOutside(RouteResult{Host: c.host, State: c.before})
		ch := check(got)
		if got.State != c.state || ch == nil || ch.Status != c.status || ch.Code != c.code {
			t.Errorf("%s from %s: %s %+v", c.host, c.before, got.State, ch)
		}
	}
	if r := withOutside(RouteResult{Host: "example.org", State: StateProblem}); r.URL == "" || check(r).Params["at"] != "2026-11-15T09:12:00Z" {
		t.Errorf("seen: %+v", r)
	}
	for _, state := range []string{StateOff, StateLocal, StateLimited} {
		if r := withOutside(RouteResult{Host: "example.org", State: state}); r.State != state || check(r) != nil {
			t.Errorf("%s changed: %+v", state, r)
		}
	}
	if r := withOutside(RouteResult{Host: "example.org:7201", State: StateUnverified}); check(r) != nil {
		t.Errorf("a port of its own: %+v", r)
	}
	if r := withOutside(RouteResult{Host: "other.example.org", State: StateVerified}); check(r) != nil {
		t.Errorf("not watched: %+v", r)
	}
}

// TestRunAsksTheWatch: the hook in Run is in place.
func TestRunAsksTheWatch(t *testing.T) {
	saved := outside
	defer func() { outside = saved }()
	outside = func(host string) reachwatch.OutsideInfo { return reachwatch.OutsideInfo{Direct: true} }
	fakes(t, []utils.ProxyRouteConfig{{Name: "app", Mode: "SERVAPP", UseHost: true, Host: "app.example.org", Target: "http://192.0.2.5:80"}}, ours)
	resp := Run(Options{})
	if len(resp.Routes) != 1 {
		t.Fatalf("%+v", resp)
	}
	found := false
	for _, c := range resp.Routes[0].Checks {
		found = found || (c.ID == "outside" && c.Code == "direct")
	}
	if !found {
		t.Fatalf("no outside check: %+v", resp.Routes[0].Checks)
	}
}
