package edgeblock

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// ---- never the owner, never the infrastructure ----

func TestNeverBlocked(t *testing.T) {
	setup(t, false, func(c *utils.Config) {
		c.ShieldWhitelist = []utils.ShieldWhitelistEntry{{IP: "198.51.100.0/28"}}
		c.AdminWhitelistIPs = []string{"198.51.100.200"}
		c.Fail2ban.IgnoreIP = []string{"192.0.2.128/25", "2001:db8:f00::/48"}
	})
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	admins := map[string]time.Time{"198.51.100.150": now.Add(-time.Hour), "2001:db8:aa::/64": now.Add(-time.Hour), "198.51.100.151": now.Add(-8 * 24 * time.Hour)}
	cases := []struct {
		ip     string
		reason string
	}{
		{"192.168.1.5", ExemptLocal},
		{"10.1.2.3", ExemptLocal},
		{"172.20.0.4", ExemptLocal},
		{"100.100.1.1", ExemptLocal}, // Tailscale's range
		{"127.0.0.1", ExemptInvalid},
		{"fe80::1", ExemptInvalid},
		{"fd00::5", ExemptLocal},
		{"2001:db8:1::99", ExemptServer}, // the server's own IPv6 network
		{"203.0.113.10", ExemptServer},   // the server's public IPv4
		{"173.245.48.5", ExemptCloudflare},
		{"2606:4700::1", ExemptCloudflare},
		{"198.51.100.3", ExemptWhitelist}, // SmartShield's whitelist
		{"198.51.100.200", ExemptWhitelist},
		{"192.0.2.200", ExemptFail2ban},
		{"2001:db8:f00:5::1", ExemptFail2ban}, // inside a /48 on the never-ban list
		{"198.51.100.150", ExemptAdmin},
		{"2001:db8:aa::77", ExemptAdmin}, // the /64 of an administrator
		{"0.0.0.0", ExemptInvalid},
		{"224.0.0.1", ExemptInvalid},
	}
	for _, c := range cases {
		key, ok := KeyOf(c.ip)
		if !ok {
			t.Fatalf("%s: no key", c.ip)
		}
		if got := exemptReason(key, admins, now); got != c.reason {
			t.Errorf("%s (%s): %q, want %q", c.ip, key, got, c.reason)
		}
	}
	// an administrator's address is exempt for seven days only; others never were
	for _, ip := range []string{"198.51.100.151", "198.51.100.77", "2001:db8:bb::1", "192.0.2.5"} {
		key, _ := KeyOf(ip)
		if got := exemptReason(key, admins, now); got != "" {
			t.Errorf("%s: %q, want blockable", ip, got)
		}
	}
	// a /64 is exempt as soon as one exempt address lies in it
	utils.SetBaseMainConfig(func() utils.Config {
		c := utils.GetMainConfig()
		c.ShieldWhitelist = append(c.ShieldWhitelist, utils.ShieldWhitelistEntry{IP: "2001:db8:cc::42"})
		return c
	}())
	if got := exemptReason("2001:db8:cc::/64", admins, now); got != ExemptWhitelist {
		t.Errorf("/64 with a whitelisted address: %q", got)
	}
}

func TestKeysAreTheAddressOrTheIPv6Slash64(t *testing.T) {
	for in, want := range map[string]string{
		"198.51.100.7":         "198.51.100.7",
		"::ffff:198.51.100.7":  "198.51.100.7",
		"2001:db8:5:6:7:8:9:a": "2001:db8:5:6::/64",
		"[2001:db8:5:6::1]":    "2001:db8:5:6::/64",
	} {
		if got, ok := KeyOf(in); !ok || got != want {
			t.Errorf("%s: %q", in, got)
		}
	}
	for _, bad := range []string{"", "example.org", "198.51.100.7/24", "fe80::1%eth0"} {
		if _, ok := KeyOf(bad); ok {
			t.Errorf("%q has a key", bad)
		}
	}
}

// ---- what counts, and the escalation ----

func TestOnlyVisitorsCloudflareReportedCount(t *testing.T) {
	env := setup(t, false, nil)
	env.enable()
	for i := 0; i < 10; i++ {
		event := edgeEvent(utils.SecLoginFailed, "198.51.100.7")
		event.Edge = false // a direct visitor: Fail2ban's
		env.engine.Observe(event)
	}
	if _, found := env.ruleOf("198.51.100.7"); found {
		t.Fatal("a direct visitor was blocked at the edge")
	}
	// an audit entry or a kind no jail counts does nothing either
	env.offend(utils.SecShieldBlocked, "198.51.100.8", 20)
	env.offend(utils.SecAccessOriginDenied, "198.51.100.8", 20)
	// nor does Cloudflare's sign-in check when Cloudflare could not be asked, or when the form came
	// without it: only a check Cloudflare refused (auth.turnstile.failed) counts, in the auth jail
	env.offend(utils.SecTurnstileUnavailable, "198.51.100.8", 20)
	env.offend(utils.SecTurnstileMissing, "198.51.100.8", 20)
	if len(env.rules()) != 0 {
		t.Fatalf("%v", env.rules())
	}
}

func TestEscalationMirrorsTheJails(t *testing.T) {
	env := setup(t, false, nil)
	env.enable()

	env.offend(utils.SecLoginFailed, "198.51.100.7", 4)
	if _, found := env.ruleOf("198.51.100.7"); found {
		t.Fatal("blocked before the fifth failure")
	}
	env.offend(utils.SecTurnstileFailed, "198.51.100.7", 1)
	r, found := env.ruleOf("198.51.100.7")
	if !found || r.Reason != "auth" || r.Mode != ModeChallenge || r.Until.Sub(r.Since) != 4*time.Hour || r.State != StatePending {
		t.Fatalf("after five sign-in failures: %+v", r)
	}

	env.offend(utils.SecScanProbe, "198.51.100.9", 3)
	if r, _ := env.ruleOf("198.51.100.9"); r.Reason != "probe" || r.Mode != ModeBlock || r.Until.Sub(r.Since) != 24*time.Hour {
		t.Fatalf("probes: %+v", r)
	}

	// counts older than the jail's window are forgotten
	env.offend(utils.SecScanProbe, "198.51.100.10", 2)
	env.clock.add(61 * time.Minute)
	env.offend(utils.SecScanProbe, "198.51.100.10", 1)
	if _, found := env.ruleOf("198.51.100.10"); found {
		t.Fatal("probes over more than an hour were added up")
	}

	// IPv6: an attacker rotating inside its /64 is one offender, blocked as the /64
	for i := 0; i < 3; i++ {
		env.offend(utils.SecScanProbe, "2001:db8:66:1::"+string(rune('a'+i)), 1)
	}
	if r, found := env.ruleOf("2001:db8:66:1::/64"); !found || r.Mode != ModeBlock {
		t.Fatalf("rotating /64: %+v", env.rules())
	}

	recorded := strings.Join(env.kinds(), " ")
	if strings.Count(recorded, "edge.blocked:") != 3 {
		t.Errorf("events: %s", recorded)
	}
	for _, event := range *env.events {
		if event.Kind == utils.SecEdgeBlocked && !event.Forwarded {
			t.Error("a block's address would reach the Fail2ban file")
		}
	}
}

func TestTheOwnersJailThresholdsApplyWithAFloorOfTwo(t *testing.T) {
	env := setup(t, false, func(c *utils.Config) {
		c.Fail2ban.Jails = map[string]utils.Fail2banJailConfig{
			"cosmos-auth":  {MaxRetry: 1, FindTime: 600, BanTime: 3600},
			"cosmos-probe": {MaxRetry: 2, FindTime: 600, BanTime: 7200},
		}
	})
	env.enable()
	env.offend(utils.SecLoginFailed, "198.51.100.7", 1)
	if _, found := env.ruleOf("198.51.100.7"); found {
		t.Fatal("blocked on one request")
	}
	env.offend(utils.SecLoginFailed, "198.51.100.7", 1)
	if r, found := env.ruleOf("198.51.100.7"); !found || r.Until.Sub(r.Since) != time.Hour {
		t.Fatalf("%+v", r)
	}
	env.offend(utils.SecScanProbe, "198.51.100.8", 2)
	if r, found := env.ruleOf("198.51.100.8"); !found || r.Until.Sub(r.Since) != 2*time.Hour {
		t.Fatalf("%+v", r)
	}
}

func TestRepeatOffendersGetAWeek(t *testing.T) {
	env := setup(t, false, nil)
	env.enable()
	for round := 1; round <= 3; round++ {
		env.offend(utils.SecScanProbe, "198.51.100.7", 3)
		r, found := env.ruleOf("198.51.100.7")
		if !found {
			t.Fatalf("round %d: no block", round)
		}
		if round < 3 && (r.Reason != "probe" || r.Until.Sub(r.Since) != 24*time.Hour) {
			t.Fatalf("round %d: %+v", round, r)
		}
		if round == 3 && (r.Reason != "repeat" || r.Mode != ModeBlock || r.Until.Sub(r.Since) != 7*24*time.Hour) {
			t.Fatalf("third block: %+v", r)
		}
		// while blocked, more offences change nothing
		env.offend(utils.SecScanProbe, "198.51.100.7", 10)
		if len(env.rules()) != 1 {
			t.Fatalf("round %d: %d rules", round, len(env.rules()))
		}
		env.clock.add(25 * time.Hour)
		env.engine.mu.Lock()
		env.engine.housekeepLocked(env.clock.now(), map[string]cloudflareZone{"example.org": {Name: "example.org"}}, true)
		env.engine.mu.Unlock()
	}
}

// ---- at Cloudflare: creation, expiry, reconciliation ----

func TestABlockIsMadeAndDeletedWhenItsTimeIsUp(t *testing.T) {
	env := setup(t, false, nil)
	env.enable()
	if len(env.atCloudflare()) != 0 {
		t.Fatalf("the test rule stayed: %+v", env.atCloudflare())
	}
	env.offend(utils.SecLoginFailed, "198.51.100.7", 5)
	env.run()
	at := env.atCloudflare()
	if len(at) != 1 || at[0].Configuration.Target != "ip" || at[0].Configuration.Value != "198.51.100.7" || at[0].Mode != "managed_challenge" {
		t.Fatalf("at Cloudflare: %+v", at)
	}
	if !strings.Contains(at[0].Notes, "cosmos-edge:"+env.engine.st.Marker) {
		t.Fatalf("note without the marker: %q", at[0].Notes)
	}
	if r, _ := env.ruleOf("198.51.100.7"); r.State != StateActive || r.RuleID != at[0].ID {
		t.Fatalf("%+v", r)
	}
	env.clock.add(4*time.Hour + time.Minute)
	env.drain()
	if len(env.atCloudflare()) != 0 {
		t.Fatalf("not deleted: %+v", env.atCloudflare())
	}
	if _, found := env.ruleOf("198.51.100.7"); found {
		t.Fatal("still on record")
	}
	if !strings.Contains(strings.Join(env.kinds(), " "), "edge.unblocked:expired") {
		t.Errorf("events: %v", env.kinds())
	}
}

func TestIPv6BlocksTheSlash64(t *testing.T) {
	env := setup(t, false, nil)
	env.enable()
	env.offend(utils.SecScanProbe, "2001:db8:66:1::5", 3)
	env.run()
	at := env.atCloudflare()
	if len(at) != 1 || at[0].Configuration.Target != "ip_range" || at[0].Configuration.Value != "2001:db8:66:1::/64" {
		t.Fatalf("%+v", at)
	}
}

func TestARuleTheOwnerDeletedIsNotMadeAgain(t *testing.T) {
	env := setup(t, false, nil)
	env.enable()
	env.offend(utils.SecScanProbe, "198.51.100.7", 3)
	env.run()
	at := env.atCloudflare()
	if len(at) != 1 {
		t.Fatalf("%+v", at)
	}
	env.fake.RemoveAccessRule("example.org", at[0].ID)
	env.clock.add(11 * time.Minute)
	env.drain()
	r, found := env.ruleOf("198.51.100.7")
	if !found || r.State != StateGone {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(strings.Join(env.kinds(), " "), "edge.unblocked:removed_at_cloudflare") {
		t.Errorf("events: %v", env.kinds())
	}
	env.offend(utils.SecScanProbe, "198.51.100.7", 10)
	env.drain()
	if len(env.atCloudflare()) != 0 {
		t.Fatalf("made again: %+v", env.atCloudflare())
	}
	// once that ban's time is over, a new offence is a new ban
	env.clock.add(24 * time.Hour)
	env.drain()
	env.offend(utils.SecScanProbe, "198.51.100.7", 3)
	env.drain()
	if len(env.atCloudflare()) != 1 {
		t.Fatalf("a new ban was not made: %+v", env.atCloudflare())
	}
}

func TestTheOwnersOwnRuleIsLeftAloneAndLeftoversAreNotTouched(t *testing.T) {
	env := setup(t, false, nil)
	env.enable()
	ownerRule := env.fake.AddAccessRule("example.org", "ip", "198.51.100.7", "whitelist", "my phone")
	leftover := env.fake.AddAccessRule("example.org", "ip", "198.51.100.99", "block", "Cosmos edge block [cosmos-edge:"+env.engine.st.Marker+"] probe")
	env.offend(utils.SecScanProbe, "198.51.100.7", 3)
	env.drain()
	r, _ := env.ruleOf("198.51.100.7")
	if r.State != StateOwner {
		t.Fatalf("%+v", r)
	}
	env.clock.add(25 * time.Hour)
	env.drain()
	ids := map[string]bool{}
	for _, rule := range env.atCloudflare() {
		ids[rule.ID] = true
	}
	if !ids[ownerRule] || !ids[leftover] || len(ids) != 2 {
		t.Fatalf("Cloudflare holds %+v", env.atCloudflare())
	}
	status := env.engine.Status(true)
	if list := status["leftovers"].([]Leftover); len(list) != 1 || list[0].RuleID != leftover {
		t.Fatalf("leftovers %+v", list)
	}
}

func TestStateSurvivesARestart(t *testing.T) {
	env := setup(t, false, nil)
	env.enable()
	env.offend(utils.SecScanProbe, "198.51.100.7", 3)
	env.offend(utils.SecLoginFailed, "198.51.100.8", 3)
	env.run()
	env.engine.NoteAdmin("198.51.100.150")
	env.engine.save(true)
	info, err := os.Stat(statePath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("state file: %v %v", info, err)
	}

	again := newEngine(statePath, env.clock.now)
	again.load()
	if again.st.Marker != env.engine.st.Marker || len(again.st.Rules) != 1 || again.st.Rules[0].State != StateActive {
		t.Fatalf("rules after restart: %+v", again.st.Rules)
	}
	if hits := again.st.Offenders["198.51.100.8"].Hits["auth"]; len(hits) != 3 {
		t.Fatalf("counts after restart: %v", hits)
	}
	if _, ok := again.st.Admins["198.51.100.150"]; !ok {
		t.Fatal("administrator's address forgotten")
	}
	// the rule still ends: deleted after its time by the new engine
	std = again
	env.engine = again
	env.clock.add(25 * time.Hour)
	env.drain()
	if len(env.atCloudflare()) != 0 {
		t.Fatalf("%+v", env.atCloudflare())
	}
	// a broken file gives an empty state, not a crash
	os.WriteFile(statePath(), []byte("{not json"), 0600)
	broken := newEngine(statePath, env.clock.now)
	broken.load()
	if len(broken.st.Rules) != 0 || len(broken.st.Marker) != 12 {
		t.Fatal("broken file")
	}
}

func TestAnAdministratorsAddressIsNeverBlockedAndIsUnblocked(t *testing.T) {
	env := setup(t, false, nil)
	env.enable()
	env.engine.NoteAdmin("198.51.100.150")
	env.offend(utils.SecLoginFailed, "198.51.100.150", 20)
	if len(env.rules()) != 0 {
		t.Fatal("an administrator's address was blocked")
	}
	// blocked first (a challenge the owner passed at a hotel), then signed in
	env.offend(utils.SecLoginFailed, "198.51.100.160", 5)
	env.run()
	if len(env.atCloudflare()) != 1 {
		t.Fatal("no rule")
	}
	env.engine.NoteAdmin("198.51.100.160")
	env.drain()
	if len(env.atCloudflare()) != 0 {
		t.Fatal("the rule stayed after the administrator signed in from it")
	}
	if !strings.Contains(strings.Join(env.kinds(), " "), "edge.unblocked:admin_seen") {
		t.Errorf("%v", env.kinds())
	}
	// local addresses are not remembered as administrators'
	env.engine.NoteAdmin("192.168.1.30")
	if _, kept := env.engine.st.Admins["192.168.1.30"]; kept {
		t.Error("a local address was noted")
	}
}

// ---- caps ----

func TestCaps(t *testing.T) {
	env := setup(t, false, nil)
	env.enable()
	// at most 60 new automatic blocks an hour
	for i := 0; i < 70; i++ {
		env.offend(utils.SecScanProbe, "198.18."+string(rune('0'+i/10))+"."+itoa(i%10+1), 3)
	}
	if n := len(env.rules()); n != MaxBlocksAnHour {
		t.Fatalf("%d blocks in one hour", n)
	}
	if env.engine.st.Dropped != 10 {
		t.Fatalf("dropped %d", env.engine.st.Dropped)
	}
	// the calls: at most a bucket of 10 at once
	calls := 0
	for env.engine.step(context.Background()) {
		calls++
	}
	if calls != 10 {
		t.Fatalf("%d calls at once", calls)
	}

	// the ceiling of rules: the automatic rule that ends soonest goes first,
	// a manual one never makes room
	env.engine.mu.Lock()
	env.engine.st.Rules = nil
	now := env.clock.now()
	for i := 0; i < MaxRules; i++ {
		reason := "probe"
		if i == 0 {
			reason = "manual"
		}
		env.engine.st.Rules = append(env.engine.st.Rules, &Rule{Key: "198.51.100." + itoa(i%250), Zone: "example.org", State: StatePending, Reason: reason, Since: now, Until: now.Add(time.Duration(i+1) * time.Hour)})
	}
	ok := env.engine.makeRoomLocked(1, now)
	live := env.engine.liveLocked()
	first := env.engine.st.Rules[0]
	env.engine.mu.Unlock()
	if !ok || live != MaxRules-1 || first.Reason != "manual" {
		t.Fatalf("room %v live %d first %+v", ok, live, first)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

// ---- failures ----

func TestWithoutThePermissionSwitchingOnIsRefusedAndSaysWhich(t *testing.T) {
	env := setup(t, true, nil)
	code, body := env.post(map[string]interface{}{"action": "enable"})
	if code != http.StatusBadGateway || body["reason"] != "proof_failed" {
		t.Fatalf("%d %v", code, body)
	}
	results := body["results"].([]interface{})
	first := results[0].(map[string]interface{})
	if first["ok"] != false || first["step"] != "list" || first["reason"] != "permission" {
		t.Fatalf("%v", first)
	}
	if enabled() {
		t.Fatal("switched on without the permission")
	}
}

func TestTheProofWhenCloudflareRefusesTheDocumentationAddress(t *testing.T) {
	env := setup(t, false, nil)
	env.fake.RefuseDocumentationAddresses(true)
	code, body := env.post(map[string]interface{}{"action": "enable"})
	if code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	first := body["data"].(map[string]interface{})["results"].([]interface{})[0].(map[string]interface{})
	if first["ok"] != true || first["inferred"] != true {
		t.Fatalf("%v", first)
	}
	if len(env.atCloudflare()) != 0 {
		t.Fatal("something was left at Cloudflare")
	}
}

func TestRateLimitAndUnreachableCloudflare(t *testing.T) {
	env := setup(t, false, nil)
	env.enable()
	env.offend(utils.SecScanProbe, "198.51.100.7", 3)
	env.fake.RateLimitNext(1, 120)
	env.run()
	env.engine.mu.Lock()
	paused := env.engine.pausedUntil["example.org"]
	problem := env.engine.problems["example.org"]
	env.engine.mu.Unlock()
	if problem.Reason != "rate_limited" || paused.Sub(env.clock.now()) != 120*time.Second {
		t.Fatalf("%+v %v", problem, paused)
	}
	if len(env.atCloudflare()) != 0 {
		t.Fatal("a call went out while paused")
	}
	env.clock.add(121 * time.Second)
	env.drain()
	if len(env.atCloudflare()) != 1 {
		t.Fatal("not made after the pause")
	}

	// unreachable: a pause that doubles
	env.offend(utils.SecScanProbe, "198.51.100.8", 3)
	env.fake.SetDown(true)
	waits := []time.Duration{}
	for i := 0; i < 3; i++ {
		env.drain()
		env.engine.mu.Lock()
		waits = append(waits, env.engine.pausedUntil["example.org"].Sub(env.clock.now()))
		env.engine.mu.Unlock()
		env.clock.add(waits[len(waits)-1])
	}
	if waits[0] != 30*time.Second || waits[1] != 60*time.Second || waits[2] != 120*time.Second {
		t.Fatalf("back-off %v", waits)
	}
	env.fake.SetDown(false)
	env.drain()
	if len(env.atCloudflare()) != 2 {
		t.Fatalf("%+v", env.atCloudflare())
	}
}

func TestAPermissionTakenAwayPausesAndTellsTheAdmins(t *testing.T) {
	env := setup(t, false, nil)
	env.enable()
	env.fake.AddToken(fwToken, cffakeToken(false))
	env.offend(utils.SecScanProbe, "198.51.100.7", 3)
	env.run()
	env.engine.mu.Lock()
	problem := env.engine.problems["example.org"]
	env.engine.mu.Unlock()
	if problem.Reason != "permission" {
		t.Fatalf("%+v", problem)
	}
	time.Sleep(50 * time.Millisecond)
	env.evMu.Lock()
	notes := strings.Join(*env.notes, " ")
	env.evMu.Unlock()
	if !strings.Contains(notes, MessagePermission+" example.org") {
		t.Fatalf("notes %q", notes)
	}
}

func TestADisconnectedZoneLeavesOrphansThatCanBeForgotten(t *testing.T) {
	env := setup(t, false, nil)
	env.enable()
	env.offend(utils.SecScanProbe, "198.51.100.7", 3)
	env.run()
	config := utils.GetMainConfig()
	config.HTTPConfig.DNSZones = nil
	utils.SetBaseMainConfig(config)
	env.drain()
	r, _ := env.ruleOf("198.51.100.7")
	if r.State != StateOrphaned || len(env.atCloudflare()) != 1 {
		t.Fatalf("%+v", r)
	}
	code, _ := env.post(map[string]interface{}{"action": "forget", "key": "198.51.100.7"})
	if _, found := env.ruleOf("198.51.100.7"); code != 200 || found {
		t.Fatal("not forgotten")
	}
}

func TestSwitchingOffRemovesEverything(t *testing.T) {
	env := setup(t, false, nil)
	env.enable()
	env.offend(utils.SecScanProbe, "198.51.100.7", 3)
	env.offend(utils.SecScanProbe, "198.51.100.8", 3)
	env.run()
	if len(env.atCloudflare()) != 2 {
		t.Fatal("setup")
	}
	if code, _ := env.post(map[string]interface{}{"action": "disable"}); code != 200 {
		t.Fatal(code)
	}
	env.drain()
	if len(env.atCloudflare()) != 0 || len(env.rules()) != 0 {
		t.Fatalf("%+v %+v", env.atCloudflare(), env.rules())
	}
	// nothing is counted while off
	env.offend(utils.SecScanProbe, "198.51.100.9", 10)
	if len(env.rules()) != 0 {
		t.Fatal("counted while off")
	}
}

// ---- the routes ----

func TestRoutesPermissionsMethodsAndBounds(t *testing.T) {
	env := setup(t, false, nil)
	// reading needs the configuration read permission
	code, _ := env.do(asAdmin(httptest.NewRequest("GET", "/cosmos/api/cloudflare/edge-blocks", nil), utils.PERM_LOGIN))
	if code < 400 {
		t.Fatalf("read without permission: %d", code)
	}
	// readers without the log permission see no addresses
	code, body := env.do(asAdmin(httptest.NewRequest("GET", "/cosmos/api/cloudflare/edge-blocks", nil), utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ))
	if code != 200 || body["data"].(map[string]interface{})["withAddresses"] != false {
		t.Fatalf("%d %v", code, body)
	}
	// changing needs the configuration permission
	if code, _ := env.post(map[string]interface{}{"action": "enable"}, utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ); code < 400 || enabled() {
		t.Fatalf("enable without permission: %d", code)
	}
	for _, method := range []string{"PUT", "DELETE", "PATCH"} {
		if code, _ := env.do(asAdmin(httptest.NewRequest(method, "/cosmos/api/cloudflare/edge-blocks", nil))); code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d", method, code)
		}
	}
	if code, _ := env.post(map[string]interface{}{"action": "block", "address": "198.51.100.7", "duration": "1d", "extra": 1}); code != http.StatusBadRequest {
		t.Errorf("unknown field: %d", code)
	}
	if code, _ := env.post(map[string]interface{}{"action": "block", "address": strings.Repeat("1", 5000)}); code != http.StatusRequestEntityTooLarge {
		t.Errorf("large body: %d", code)
	}
	if code, _ := env.post(map[string]interface{}{"action": "nothing"}); code != http.StatusBadRequest {
		t.Errorf("unknown action: %d", code)
	}
}

func TestManualBlockAndUnblock(t *testing.T) {
	env := setup(t, false, nil)
	// off: refused
	if code, body := env.post(map[string]interface{}{"action": "block", "address": "198.51.100.7", "duration": "1d"}); code != http.StatusConflict || body["reason"] != "off" {
		t.Fatalf("%d %v", code, body)
	}
	env.enable()
	for _, c := range []struct {
		address, reason string
	}{
		{"192.168.1.5", "exempt"}, {"173.245.48.5", "exempt"}, {"203.0.113.10", "exempt"}, {"not an address", "not_an_address"},
	} {
		if code, body := env.post(map[string]interface{}{"action": "block", "address": c.address, "duration": "1d"}); code < 400 || body["reason"] != c.reason {
			t.Errorf("%s: %d %v", c.address, code, body)
		}
	}
	if code, _ := env.post(map[string]interface{}{"action": "block", "address": "198.51.100.7", "duration": "1y"}); code != http.StatusBadRequest {
		t.Error("a duration not offered")
	}
	if code, _ := env.post(map[string]interface{}{"action": "block", "address": "198.51.100.7", "duration": "1d", "mode": "whitelist"}); code != http.StatusBadRequest {
		t.Error("a mode not offered")
	}
	code, _ := env.post(map[string]interface{}{"action": "block", "address": "198.51.100.7", "duration": "7d"})
	env.run()
	r, _ := env.ruleOf("198.51.100.7")
	if code != 200 || r.Reason != "manual" || r.Actor != "admin" || r.Mode != ModeBlock || len(env.atCloudflare()) != 1 {
		t.Fatalf("%d %+v", code, r)
	}
	if code, _ := env.post(map[string]interface{}{"action": "unblock", "key": "198.51.100.7"}); code != 200 {
		t.Fatal(code)
	}
	env.drain()
	if len(env.atCloudflare()) != 0 {
		t.Fatal("not lifted")
	}
	// an IPv6 /64 by its key
	env.post(map[string]interface{}{"action": "block", "address": "2001:db8:66:1::9", "duration": "1h"})
	env.run()
	if code, _ := env.post(map[string]interface{}{"action": "unblock", "key": "2001:db8:66:1::/64"}); code != 200 {
		t.Fatal(code)
	}
	env.offend(utils.SecScanProbe, "198.51.100.8", 3)
	env.offend(utils.SecScanProbe, "198.51.100.9", 3)
	env.drain()
	if code, _ := env.post(map[string]interface{}{"action": "unblockAll"}); code != 200 {
		t.Fatal(code)
	}
	env.drain()
	if len(env.atCloudflare()) != 0 {
		t.Fatalf("%+v", env.atCloudflare())
	}
}

func TestTheAdministratorsOwnAddressCannotBeBlockedByHand(t *testing.T) {
	env := setup(t, false, nil)
	env.enable()
	req := asAdmin(httptest.NewRequest("POST", "/cosmos/api/cloudflare/edge-blocks", strings.NewReader(`{"action":"block","address":"198.51.100.40","duration":"1d"}`)))
	req.RemoteAddr = "198.51.100.40:4000"
	code, body := env.do(req)
	if code != http.StatusConflict || body["exempt"] != ExemptAdmin {
		t.Fatalf("%d %v", code, body)
	}
}
