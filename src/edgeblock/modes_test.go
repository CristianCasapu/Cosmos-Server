package edgeblock

// The three ways of blocking (IP Access Rules of the domain, custom rules of
// the domain, IP Access Rules of the account), and the test that picks one.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/cloudflare/cffake"
	"github.com/azukaar/cosmos-server/src/utils"
)

var (
	// the owner's token as it was found: the account's access rules, the
	// zone's custom rules readable, nothing of the zone's access rules
	ownersToken = cffake.Token{ReadDNS: true, EditDNS: true, ReadAccountRules: true, EditAccountRules: true, ReadWAF: true}
	// the same once "Zone WAF Edit" is there
	wafToken = cffake.Token{ReadDNS: true, EditDNS: true, ReadAccountRules: true, EditAccountRules: true, ReadWAF: true, EditWAF: true}
)

func (env *testEnv) enableWith(allowAccount bool) (int, map[string]interface{}) {
	env.t.Helper()
	return env.post(map[string]interface{}{"action": "enable", "allowAccount": allowAccount})
}

func firstResult(t *testing.T, code int, body map[string]interface{}) map[string]interface{} {
	t.Helper()
	results, _ := body["results"].([]interface{})
	if results == nil {
		if data, ok := body["data"].(map[string]interface{}); ok {
			results, _ = data["results"].([]interface{})
		}
	}
	if len(results) == 0 {
		t.Fatalf("no results: %d %v", code, body)
	}
	return results[0].(map[string]interface{})
}

func TestTheTestTellsTheCasesApart(t *testing.T) {
	cases := []struct {
		name         string
		rights       cffake.Token
		allowAccount bool
		ownerRules   int
		code         int
		outcome      string
		zoneRules    string
		waf          string
		account      string
	}{
		{"nothing usable", cffake.Token{ReadDNS: true, EditDNS: true}, false, 0, http.StatusConflict, OutcomeNone, "no", "no", "no"},
		{"zone access rules", cffakeToken(true), false, 0, 200, OutcomeZone, "yes", "", ""},
		{"zone access rules readable only, custom rules writable", cffake.Token{ReadDNS: true, ReadFirewall: true, ReadWAF: true, EditWAF: true}, false, 0, 200, OutcomeWAF, "read", "yes", ""},
		{"the owner's token: custom rules readable, account rules usable", ownersToken, false, 0, http.StatusConflict, OutcomeWAFRead, "no", "read", "read"},
		{"the owner's token with Zone WAF Edit", wafToken, false, 0, 200, OutcomeWAF, "no", "yes", ""},
		{"only the account's rules", cffake.Token{ReadDNS: true, ReadAccountRules: true, EditAccountRules: true}, false, 0, http.StatusConflict, OutcomeAccountOnly, "no", "no", "read"},
		{"only the account's rules, chosen", cffake.Token{ReadDNS: true, ReadAccountRules: true, EditAccountRules: true}, true, 0, 200, OutcomeAccount, "no", "no", "yes"},
		{"custom rules full", cffake.Token{ReadDNS: true, ReadWAF: true, EditWAF: true}, false, 5, http.StatusConflict, OutcomeWAFFull, "no", "full", "no"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := setupWith(t, c.rights, nil)
			for i := 0; i < c.ownerRules; i++ {
				env.fake.AddCustomRule("example.org", "block", `(http.request.uri.path eq "/owner`+itoa(i)+`")`, "the owner's")
			}
			code, body := env.enableWith(c.allowAccount)
			first := firstResult(t, code, body)
			if code != c.code || first["outcome"] != c.outcome || first["zoneRules"] != c.zoneRules || first["waf"] != c.waf || first["accountRules"] != c.account {
				t.Fatalf("%d %v", code, first)
			}
			if code >= 500 {
				t.Fatal("a refusal answered with a gateway status")
			}
			if enabled() != (c.code == 200) {
				t.Fatalf("enabled %v", enabled())
			}
			// the test leaves nothing behind: no rule at the domain or the account,
			// only the owner's custom rules
			if len(env.atCloudflare()) != 0 || len(env.fake.AccountRules()) != 0 {
				t.Fatalf("left: %v %v", env.atCloudflare(), env.fake.AccountRules())
			}
			if set := env.fake.Entrypoint("example.org"); set != nil && len(set.Rules) != c.ownerRules {
				t.Fatalf("custom rules left: %+v", set.Rules)
			}
		})
	}
}

// wafRule finds Cosmos' custom rule of a mode.
func (env *testEnv) wafRule(mode string) *cffake.RulesetRule {
	set := env.fake.Entrypoint("example.org")
	if set == nil {
		return nil
	}
	word := map[string]string{ModeBlock: "] block", ModeChallenge: "] challenge"}[mode]
	for i := range set.Rules {
		if strings.Contains(set.Rules[i].Description, "cosmos-edge:") && strings.HasSuffix(set.Rules[i].Description, word) {
			return &set.Rules[i]
		}
	}
	return nil
}

func (env *testEnv) wafSetup(t *testing.T) {
	t.Helper()
	// two rules of the owner's own, which must never change
	env.fake.AddCustomRule("example.org", "skip", `(ip.src eq 192.0.2.77)`, "the owner's allow")
	env.fake.AddCustomRule("example.org", "block", `(http.request.uri.path contains "/admin")`, "the owner's block")
	code, body := env.enableWith(false)
	if first := firstResult(t, code, body); code != 200 || first["outcome"] != OutcomeWAF {
		t.Fatalf("%d %v", code, first)
	}
}

func (env *testEnv) ownerRulesIntact(t *testing.T) {
	t.Helper()
	set := env.fake.Entrypoint("example.org")
	if set == nil || len(set.Rules) < 2 || set.Rules[0].Description != "the owner's allow" || set.Rules[0].Expression != `(ip.src eq 192.0.2.77)` ||
		set.Rules[1].Description != "the owner's block" || set.Rules[1].Action != "block" {
		t.Fatalf("the owner's rules changed: %+v", set)
	}
}

func TestCustomRulesMode(t *testing.T) {
	env := setupWith(t, wafToken, nil)
	env.wafSetup(t)

	env.offend(utils.SecScanProbe, "198.51.100.7", 3)
	env.offend(utils.SecLoginFailed, "198.51.100.8", 5)
	env.offend(utils.SecScanProbe, "2001:db8:66:1::5", 3)
	env.fake.Calls(true)
	env.run()
	block, challenge := env.wafRule(ModeBlock), env.wafRule(ModeChallenge)
	if block == nil || block.Expression != "(ip.src in {198.51.100.7 2001:db8:66:1::/64})" || block.Action != "block" || !block.Enabled {
		t.Fatalf("block rule %+v", block)
	}
	if challenge == nil || challenge.Expression != "(ip.src in {198.51.100.8})" || challenge.Action != "managed_challenge" {
		t.Fatalf("challenge rule %+v", challenge)
	}
	env.ownerRulesIntact(t)
	if r, _ := env.ruleOf("198.51.100.7"); r.State != StateActive || r.Via != ViaWAF {
		t.Fatalf("%+v", r)
	}
	// one read and one write per rule: two blocks of the same kind went together
	writes := cffake.Writes(env.fake.Calls(true))
	if writes != 2 {
		t.Fatalf("%d writes", writes)
	}
	for _, event := range *env.events {
		if event.Kind == utils.SecEdgeBlocked && event.Details["via"] != ViaWords[ViaWAF] {
			t.Errorf("the record does not say how: %v", event.Details)
		}
	}

	// another address: one change of the rule
	env.offend(utils.SecScanProbe, "198.51.100.9", 3)
	env.drain()
	if block = env.wafRule(ModeBlock); !strings.Contains(block.Expression, "198.51.100.9") {
		t.Fatalf("%+v", block)
	}
	// the owner deletes one address from Cosmos' rule: respected
	env.fake.SetCustomRuleExpression("example.org", block.ID, "(ip.src in {198.51.100.7 198.51.100.9})")
	env.clock.add(11 * time.Minute)
	env.drain()
	if r, _ := env.ruleOf("2001:db8:66:1::/64"); r.State != StateGone {
		t.Fatalf("an address the owner removed: %+v", r)
	}
	env.offend(utils.SecScanProbe, "2001:db8:66:1::6", 5)
	env.drain()
	if strings.Contains(env.wafRule(ModeBlock).Expression, "2001:db8:66:1::/64") {
		t.Fatal("made again after the owner removed it")
	}

	// expiry: the challenge (4 h) ends, its rule goes; the blocks (24 h) stay
	env.clock.add(4 * time.Hour)
	env.drain()
	if env.wafRule(ModeChallenge) != nil {
		t.Fatal("the challenge rule stayed with nothing in it")
	}
	if !strings.Contains(strings.Join(env.kinds(), " "), "edge.unblocked:expired") {
		t.Errorf("%v", env.kinds())
	}
	env.clock.add(21 * time.Hour)
	env.drain()
	if env.wafRule(ModeBlock) != nil {
		t.Fatalf("%+v", env.wafRule(ModeBlock))
	}
	env.ownerRulesIntact(t)

	// a restart keeps the state of the custom rules
	env.offend(utils.SecScanProbe, "198.51.100.20", 3)
	env.run()
	env.engine.save(true)
	again := newEngine(statePath, env.clock.now)
	again.load()
	if ws := again.st.WAF["example.org"]; ws == nil || ws.Rules[ModeBlock] == "" || len(ws.Sent[ModeBlock]) != 1 {
		t.Fatalf("%+v", again.st.WAF)
	}
	std, env.engine = again, again
	env.clock.add(25 * time.Hour)
	env.drain()
	if env.wafRule(ModeBlock) != nil {
		t.Fatal("not lifted after the restart")
	}
	env.ownerRulesIntact(t)
}

func TestCustomRulesTheOwnerDeletedAreNotMadeAgain(t *testing.T) {
	env := setupWith(t, wafToken, nil)
	env.wafSetup(t)
	env.offend(utils.SecScanProbe, "198.51.100.7", 3)
	env.run()
	block := env.wafRule(ModeBlock)
	env.fake.RemoveCustomRule("example.org", block.ID)
	env.clock.add(11 * time.Minute)
	env.drain()
	if r, _ := env.ruleOf("198.51.100.7"); r.State != StateGone {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(strings.Join(env.kinds(), " "), "edge.unblocked:removed_at_cloudflare") {
		t.Errorf("%v", env.kinds())
	}
	env.offend(utils.SecScanProbe, "198.51.100.7", 10)
	env.drain()
	if env.wafRule(ModeBlock) != nil {
		t.Fatal("made again")
	}
	// another address makes a new rule of Cosmos'
	env.offend(utils.SecScanProbe, "198.51.100.8", 3)
	env.drain()
	if block = env.wafRule(ModeBlock); block == nil || block.Expression != "(ip.src in {198.51.100.8})" {
		t.Fatalf("%+v", block)
	}
	env.ownerRulesIntact(t)
}

func TestCustomRulesNeverHoldTheOwnersAddresses(t *testing.T) {
	env := setupWith(t, wafToken, func(c *utils.Config) {
		c.ShieldWhitelist = []utils.ShieldWhitelistEntry{{IP: "198.51.100.30"}}
	})
	env.wafSetup(t)
	env.engine.NoteAdmin("198.51.100.150")
	for _, ip := range []string{"198.51.100.150", "198.51.100.30", "192.168.1.5", "173.245.48.5", "203.0.113.10"} {
		env.offend(utils.SecScanProbe, ip, 10)
	}
	env.run()
	if env.wafRule(ModeBlock) != nil || len(env.rules()) != 0 {
		t.Fatalf("%+v %+v", env.wafRule(ModeBlock), env.rules())
	}
	// a challenge an administrator passed and then signed in from is lifted
	env.offend(utils.SecLoginFailed, "198.51.100.160", 5)
	env.run()
	if env.wafRule(ModeChallenge) == nil {
		t.Fatal("no challenge")
	}
	env.engine.NoteAdmin("198.51.100.160")
	env.drain()
	if env.wafRule(ModeChallenge) != nil {
		t.Fatal("not lifted when an administrator signed in from it")
	}
}

func TestCustomRulesCap(t *testing.T) {
	env := setupWith(t, wafToken, nil)
	env.wafSetup(t)
	env.engine.mu.Lock()
	now := env.clock.now()
	for i := 0; i < MaxWAFPerAction; i++ {
		env.engine.st.Rules = append(env.engine.st.Rules, &Rule{Key: "198.18.0." + itoa(i+1), Zone: "example.org", Via: ViaWAF, State: StatePending, Mode: ModeBlock, Reason: "probe", Since: now, Until: now.Add(time.Duration(i+1) * time.Hour)})
	}
	env.engine.mu.Unlock()
	env.drain()
	if block := env.wafRule(ModeBlock); block == nil || len(block.Expression) > 4096 || strings.Count(block.Expression, " ")+1 != MaxWAFPerAction+2 {
		t.Fatalf("%v", block)
	}
	// the 101st: the one that ends soonest makes room
	env.offend(utils.SecScanProbe, "198.51.100.7", 3)
	env.drain()
	expression := env.wafRule(ModeBlock).Expression
	if !strings.Contains(expression, "198.51.100.7") || strings.Contains(expression, "198.18.0.1 ") || len(wafKeys(expression)) != MaxWAFPerAction {
		t.Fatalf("%s", expression)
	}
	if status := env.engine.Status(false); status["maxRules"] != 2*MaxWAFPerAction {
		t.Fatalf("cap %v", status["maxRules"])
	}
}

func TestCustomRulesPermissionTakenAway(t *testing.T) {
	env := setupWith(t, wafToken, nil)
	env.wafSetup(t)
	env.fake.AddToken(fwToken, ownersToken)
	env.offend(utils.SecScanProbe, "198.51.100.7", 3)
	env.run()
	env.engine.mu.Lock()
	problem := env.engine.problems["example.org"]
	env.engine.mu.Unlock()
	if problem.Reason != "permission" || problem.Via != ViaWAF {
		t.Fatalf("%+v", problem)
	}
	time.Sleep(50 * time.Millisecond)
	env.evMu.Lock()
	notes := strings.Join(*env.notes, " ")
	env.evMu.Unlock()
	if !strings.Contains(notes, MessagePermissionWAF+" example.org") {
		t.Fatalf("%q", notes)
	}
}

func TestAccountRulesOnlyWhenChosen(t *testing.T) {
	env := setupWith(t, ownersToken, nil)
	// not chosen: nothing switched on, nothing made anywhere
	code, body := env.enableWith(false)
	if first := firstResult(t, code, body); code != http.StatusConflict || first["outcome"] != OutcomeWAFRead || first["accountRules"] != "read" {
		t.Fatalf("%d %v", code, first)
	}
	env.offend(utils.SecScanProbe, "198.51.100.7", 3)
	env.run()
	if len(env.fake.AccountRules()) != 0 {
		t.Fatal("an account rule without the owner's choice")
	}
	// chosen
	code, body = env.enableWith(true)
	if first := firstResult(t, code, body); code != 200 || first["outcome"] != OutcomeAccount {
		t.Fatalf("%d %v", code, first)
	}
	env.offend(utils.SecScanProbe, "198.51.100.8", 3)
	env.run()
	rules := env.fake.AccountRules()
	if len(rules) != 1 || rules[0].Configuration.Value != "198.51.100.8" || len(env.atCloudflare()) != 0 {
		t.Fatalf("%+v", rules)
	}
	if r, _ := env.ruleOf("198.51.100.8"); r.Via != ViaAccount || r.State != StateActive {
		t.Fatalf("%+v", r)
	}
	// deleted by the owner: respected
	env.fake.RemoveAccountRule(rules[0].ID)
	env.clock.add(11 * time.Minute)
	env.drain()
	if r, _ := env.ruleOf("198.51.100.8"); r.State != StateGone {
		t.Fatalf("%+v", r)
	}
	// expiry and switching off delete at the account too
	env.offend(utils.SecScanProbe, "198.51.100.9", 3)
	env.drain()
	if len(env.fake.AccountRules()) != 1 {
		t.Fatal("not made")
	}
	env.clock.add(25 * time.Hour)
	env.drain()
	if len(env.fake.AccountRules()) != 0 {
		t.Fatal("not deleted at its end")
	}
	// the choice taken back (enable without it): no more account rules
	env.enableWith(false)
	if len(provenTargets()) != 0 {
		t.Fatalf("%v", provenTargets())
	}
}

func TestAnAccountProofCountsOnlyWithTheOwnersChoice(t *testing.T) {
	env := setupWith(t, ownersToken, nil)
	env.enableWith(true)
	if targets := provenTargets(); len(targets) != 1 || targets[0].Via != ViaAccount {
		t.Fatalf("%v", targets)
	}
	// the choice is taken out of the settings (by hand, or an older page): no account rules
	config := utils.GetMainConfig()
	config.HTTPConfig.Cloudflare.EdgeBlocks.AllowAccount = false
	utils.SetBaseMainConfig(config)
	if targets := provenTargets(); len(targets) != 0 {
		t.Fatalf("%v", targets)
	}
	if zones := env.engine.Status(true)["zones"].([]ZoneState); zones[0].Proven {
		t.Fatal("shown as in use")
	}
}

func TestRefusalsAreSentences(t *testing.T) {
	env := setupWith(t, ownersToken, nil)
	_, body := env.enableWith(false)
	if message, _ := body["message"].(string); !strings.HasPrefix(message, "Not switched on") {
		t.Fatalf("%v", body["message"])
	}
	_, body = env.post(map[string]interface{}{"action": "block", "address": "198.51.100.7", "duration": "1d"})
	if message, _ := body["message"].(string); strings.Contains(message, "_") {
		t.Fatalf("%v", body["message"])
	}
}
