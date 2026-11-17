package proxy

import (
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition (plan 50): the proxy judges a visitor with utils.ShieldLimits,
// the numbers the guide shows for an address: just at a limit passes, one more
// is over it, for each kind of limit.
func TestBudgetViolationUsesTheShownLimits(t *testing.T) {
	policy := utils.SmartShieldPolicy{Enabled: true, PolicyStrictness: utils.NORMAL, PerUserTimeBudget: 1000,
		PerUserRequestLimit: 10, PerUserByteLimit: 5000, PerUserSimultaneous: 3, MaxGlobalSimultaneous: 100}
	limits := utils.ShieldLimits(policy)
	if limits.Requests != 20 || limits.Bytes != 10000 || limits.TimeMs != 2000 || limits.SimultaneousHTTP != 90 || limits.SimultaneousTCP != 6 || limits.Packets != 20000 {
		t.Fatalf("the shield's arithmetic: %+v", limits)
	}
	cases := []struct {
		what string
		at   userUsedBudget
		over userUsedBudget
		tcp  bool
	}{
		{"requests", userUsedBudget{Requests: limits.Requests}, userUsedBudget{Requests: limits.Requests + 1}, false},
		{"bytes", userUsedBudget{Bytes: limits.Bytes}, userUsedBudget{Bytes: limits.Bytes + 1}, false},
		{"time", userUsedBudget{Time: limits.TimeMs / 1000}, userUsedBudget{Time: limits.TimeMs/1000 + 0.01}, false},
		{"simultaneous", userUsedBudget{Simultaneous: limits.SimultaneousHTTP}, userUsedBudget{Simultaneous: limits.SimultaneousHTTP + 1}, false},
		{"simultaneous", userUsedBudget{Simultaneous: limits.SimultaneousTCP}, userUsedBudget{Simultaneous: limits.SimultaneousTCP + 1}, true},
		{"packets", userUsedBudget{Packets: limits.Packets}, userUsedBudget{Packets: limits.Packets + 1}, true},
	}
	for _, c := range cases {
		if _, over := budgetViolation(policy, c.at, "r", c.tcp); over {
			t.Errorf("%s: at the limit is refused", c.what)
		}
		if reason, over := budgetViolation(policy, c.over, "r", c.tcp); !over || reason.Limit != c.what {
			t.Errorf("%s: one over the limit passes (%+v)", c.what, reason)
		}
	}
}

// the proxy's judgement before plan 50, kept here to compare with: for any
// strictness, also 0 and below (not reachable from the panel), the verdict
// and the reason must be the same as with utils.ShieldLimits
func oldBudgetViolation(policy utils.SmartShieldPolicy, u userUsedBudget, route string, tcp bool) (utils.ShieldBanReason, bool) {
	strictness := policy.PolicyStrictness
	reason := utils.ShieldBanReason{Route: route}
	overTime := policy.PerUserTimeBudget > 0 && u.Time*1000 > policy.PerUserTimeBudget*float64(strictness)
	switch {
	case overTime:
		reason.Limit, reason.Used, reason.Allowed = "time", u.Time*1000, policy.PerUserTimeBudget*float64(strictness)
	case !tcp && u.Requests > policy.PerUserRequestLimit*strictness:
		reason.Limit, reason.Used, reason.Allowed = "requests", float64(u.Requests), float64(policy.PerUserRequestLimit*strictness)
	case tcp && u.Packets > int64(policy.PerUserRequestLimit*1000*strictness):
		reason.Limit, reason.Used, reason.Allowed = "packets", float64(u.Packets), float64(policy.PerUserRequestLimit*1000*strictness)
	case u.Bytes > policy.PerUserByteLimit*int64(strictness):
		reason.Limit, reason.Used, reason.Allowed = "bytes", float64(u.Bytes), float64(policy.PerUserByteLimit*int64(strictness))
	case !tcp && u.Simultaneous > policy.PerUserSimultaneous*strictness*15:
		reason.Limit, reason.Used, reason.Allowed = "simultaneous", float64(u.Simultaneous), float64(policy.PerUserSimultaneous*strictness*15)
	case tcp && u.Simultaneous > policy.PerUserSimultaneous*strictness:
		reason.Limit, reason.Used, reason.Allowed = "simultaneous", float64(u.Simultaneous), float64(policy.PerUserSimultaneous*strictness)
	default:
		return reason, false
	}
	return reason, true
}

func TestBudgetViolationIsTheOldOneForAnyStrictness(t *testing.T) {
	usages := []userUsedBudget{{}, {Time: 0.5}, {Time: 5, Requests: 3}, {Requests: 50}, {Packets: 70000}, {Bytes: 20000}, {Simultaneous: 200}, {Time: 100, Requests: 100, Bytes: 1 << 20, Simultaneous: 500, Packets: 1 << 20}}
	for strictness := -3; strictness <= 4; strictness++ {
		for _, budget := range []float64{0, 1000, -5} {
			policy := utils.SmartShieldPolicy{Enabled: true, PolicyStrictness: strictness, PerUserTimeBudget: budget, PerUserRequestLimit: 10, PerUserByteLimit: 5000, PerUserSimultaneous: 3}
			for _, u := range usages {
				for _, tcp := range []bool{false, true} {
					oldReason, oldOver := oldBudgetViolation(policy, u, "r", tcp)
					newReason, newOver := budgetViolation(policy, u, "r", tcp)
					if oldOver != newOver || oldReason != newReason {
						t.Errorf("strictness %d, budget %v, usage %+v, tcp %v: old %+v %v, new %+v %v", strictness, budget, u, tcp, oldReason, oldOver, newReason, newOver)
					}
				}
			}
		}
	}
}
