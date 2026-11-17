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
