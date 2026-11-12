package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func withPro(t *testing.T, pro bool) {
	t.Helper()
	prev := utils.IsPro
	utils.IsPro = func() bool { return pro }
	t.Cleanup(func() { utils.IsPro = prev })
}

// fresh stores so tests do not see each other's strikes
func resetShieldState(t *testing.T) {
	t.Helper()
	prevShield, prevSocket := shield, socketShield
	globalShieldState.Lock()
	prevBans, prevIDs := globalShieldState.byClient, globalShieldState.ids
	globalShieldState.byClient = map[string][]*UserBan{}
	globalShieldState.ids = map[string]*UserBan{}
	globalShieldState.Unlock()
	shield = newBudgetStore()
	socketShield = newBudgetStore()
	cfg := utils.Config{}
	cfg.MonitoringDisabled = true
	utils.LoadBaseMainConfig(cfg)
	t.Cleanup(func() {
		shield, socketShield = prevShield, prevSocket
		globalShieldState.Lock()
		globalShieldState.byClient, globalShieldState.ids = prevBans, prevIDs
		globalShieldState.Unlock()
	})
}

func TestApplySmartShieldDefaultsPerProfile(t *testing.T) {
	withPro(t, false)
	oss := utils.ApplySmartShieldDefaults(utils.SmartShieldPolicy{Enabled: true})
	if oss.PerUserRequestLimit != 18000 || oss.MaxGlobalSimultaneous != 2000 || oss.PolicyStrictness != utils.NORMAL {
		t.Fatalf("OSS defaults changed: %+v", oss)
	}
	if !utils.GetSmartShieldProfile().AllowPermanentBan {
		t.Fatal("OSS profile should allow permanent bans")
	}

	withPro(t, true)
	pro := utils.ApplySmartShieldDefaults(utils.SmartShieldPolicy{Enabled: true})
	if pro.PerUserRequestLimit != 200000 || pro.MaxGlobalSimultaneous != 20000 || pro.PerUserSimultaneous != 500 || pro.PolicyStrictness != utils.LENIENT {
		t.Fatalf("Pro defaults: %+v", pro)
	}
	if utils.GetSmartShieldProfile().AllowPermanentBan || utils.GetSmartShieldProfile().GlobalCapWait > 5*time.Second {
		t.Fatalf("Pro profile: %+v", utils.GetSmartShieldProfile())
	}

	// explicit route values always win over the profile
	custom := utils.ApplySmartShieldDefaults(utils.SmartShieldPolicy{Enabled: true, PerUserRequestLimit: 10000, PolicyStrictness: 1})
	if custom.PerUserRequestLimit != 10000 || custom.PolicyStrictness != 1 || custom.MaxGlobalSimultaneous != 20000 {
		t.Fatalf("custom policy: %+v", custom)
	}

	// a disabled policy is left alone
	if off := utils.ApplySmartShieldDefaults(utils.SmartShieldPolicy{}); off.PerUserRequestLimit != 0 {
		t.Fatalf("disabled policy got defaults: %+v", off)
	}
}

func TestBudgetWindowRollsOff(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	b := &clientBudget{}
	b.addRequests(10, now.Add(-2*time.Hour)) // outside the window
	b.addRequests(5, now.Add(-50*time.Minute))
	b.addBytes(1000, now.Add(-10*time.Minute))
	b.addRequests(1, now)

	u := b.consumed("c", now)
	if u.Requests != 6 || u.Bytes != 1000 || u.Simultaneous != 0 {
		t.Fatalf("consumed: %+v", u)
	}

	// an hour later everything but the last request is gone
	u = b.consumed("c", now.Add(55*time.Minute))
	if u.Requests != 1 || u.Bytes != 0 {
		t.Fatalf("consumed after roll-off: %+v", u)
	}

	// in-flight work counts as simultaneous and as elapsed time
	b.begin(now)
	b.begin(now.Add(-30 * time.Second))
	u = b.consumed("c", now.Add(10*time.Second))
	if u.Simultaneous != 2 || u.Time < 49 || u.Time > 51 {
		t.Fatalf("in-flight: %+v", u)
	}
	b.end(now, now.Add(10*time.Second))
	b.end(now.Add(-30*time.Second), now.Add(10*time.Second))
	u = b.consumed("c", now.Add(10*time.Second))
	if u.Simultaneous != 0 || u.Time < 49 || u.Time > 51 {
		t.Fatalf("after end: %+v", u)
	}
}

func TestBanEscalation(t *testing.T) {
	resetShieldState(t)
	now := time.Now()

	withPro(t, false)
	for i := 0; i < 3; i++ {
		if !globalShieldState.allowed("a", "s", now) {
			t.Fatalf("strike %d should not block before it is issued", i)
		}
		globalShieldState.strike("a", "s", utils.ShieldBanReason{Limit: "requests", Route: "s"}, now)
		if globalShieldState.allowed("a", "s", now) {
			t.Fatal("a fresh strike must block")
		}
		now = now.Add(2 * time.Hour) // past the strike block, inside the 24h window
	}
	// 3 strikes in 24h escalate to a temporary ban
	if globalShieldState.allowed("a", "s", now) {
		t.Fatal("third strike should have escalated")
	}
	if last := globalShieldState.byClient["a"][len(globalShieldState.byClient["a"])-1]; last.BanType != TEMP {
		t.Fatalf("expected TEMP, got %d", last.BanType)
	}
	if globalShieldState.allowed("a", "s", now.Add(3*time.Hour)) {
		t.Fatal("temp ban lasts 4h")
	}
	if !globalShieldState.allowed("a", "s", now.Add(5*time.Hour)) {
		t.Fatal("temp ban should be over after 4h")
	}

	// temporary bans get longer as they repeat: 4h, 24h, then 72h. Three of
	// them in 7 days: permanent on OSS, temporary again on Pro
	seed := func(client string, pro bool) time.Time {
		withPro(t, pro)
		at := time.Now()
		for i, block := range []time.Duration{4 * time.Hour, 24 * time.Hour, 72 * time.Hour} {
			globalShieldState.Lock()
			globalShieldState.issue(client, TEMP, "s", utils.ShieldBanReason{Limit: "escalation"}, at)
			globalShieldState.Unlock()
			if globalShieldState.allowed(client, "s", at.Add(block-time.Minute)) {
				t.Fatalf("temp ban %d should last %s", i+1, block)
			}
			at = at.Add(block + time.Hour)
		}
		if globalShieldState.allowed(client, "s", at) {
			t.Fatal("third temp ban should escalate")
		}
		return at
	}
	at := seed("oss", false)
	if last := globalShieldState.last("oss"); last != nil {
		t.Fatalf("last() only reports strikes, got %v", last)
	}
	if bans := globalShieldState.byClient["oss"]; bans[len(bans)-1].BanType != PERM {
		t.Fatal("OSS escalates to a permanent ban")
	}
	if globalShieldState.allowed("oss", "s", at.Add(1000*time.Hour)) {
		t.Fatal("permanent is permanent")
	}

	at = seed("pro", true)
	if bans := globalShieldState.byClient["pro"]; bans[len(bans)-1].BanType != TEMP {
		t.Fatal("Pro must not issue permanent bans")
	}
	if globalShieldState.allowed("pro", "s", at.Add(71*time.Hour)) {
		t.Fatal("a repeated Pro temp ban lasts 72h")
	}
	if !globalShieldState.allowed("pro", "s", at.Add(80*time.Hour)) {
		t.Fatal("Pro client should be allowed again once the temp bans age out")
	}

	// cleanup keeps permanent bans and drops the rest after 7 days
	globalShieldState.cleanup(time.Now().Add(500 * time.Hour))
	if _, ok := globalShieldState.byClient["pro"]; ok {
		t.Fatal("expired temp bans should be dropped")
	}
	if len(globalShieldState.byClient["oss"]) != 1 || globalShieldState.byClient["oss"][0].BanType != PERM {
		t.Fatalf("permanent ban should survive cleanup: %v", globalShieldState.byClient["oss"])
	}
}

func shieldedHandler(t *testing.T, shieldID string, policy utils.SmartShieldPolicy, next http.Handler) http.Handler {
	t.Helper()
	route := utils.ProxyRouteConfig{Name: shieldID, SmartShield: policy}
	return SmartShieldMiddleware(shieldID, route)(next)
}

func doGet(h http.Handler, remote string, ctx context.Context) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "http://app.example/", nil)
	r.RemoteAddr = remote
	if ctx != nil {
		r = r.WithContext(ctx)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestMiddlewareStrikesWhenOverBudget(t *testing.T) {
	resetShieldState(t)
	withPro(t, true)

	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hi")) })
	h := shieldedHandler(t, "budget", utils.SmartShieldPolicy{Enabled: true, PerUserRequestLimit: 3, PolicyStrictness: 1}, ok)

	for i := 0; i < 3; i++ {
		if w := doGet(h, "203.0.113.9:1234", nil); w.Code != 200 {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	// 4 > 3*1: the 4th request is refused and the client is struck
	if w := doGet(h, "203.0.113.9:1234", nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
	if GetLastBan("203.0.113.9") == nil {
		t.Fatal("expected a strike")
	}
	// another IP is unaffected
	if w := doGet(h, "203.0.113.10:1234", nil); w.Code != 200 {
		t.Fatalf("other client: %d", w.Code)
	}
	// budgets and bans are tracked
	if GetShield() < 2 {
		t.Fatalf("GetShield: %d", GetShield())
	}
}

func TestMiddlewareBudgetsAuthenticatedUsersSeparately(t *testing.T) {
	resetShieldState(t)
	withPro(t, true)

	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hi")) })
	// PrivilegedGroups 2 = admins only, so plain logged-in users are still shielded
	h := shieldedHandler(t, "nat", utils.SmartShieldPolicy{Enabled: true, PerUserRequestLimit: 2, PolicyStrictness: 1, PrivilegedGroups: 2}, ok)

	as := func(nick string) context.Context {
		return context.WithValue(context.Background(), utils.AuthCtxKey, &utils.AuthContext{Nickname: nick, Permissions: []utils.Permission{utils.PERM_LOGIN}})
	}

	// three users behind the same NAT IP each spend their own budget
	for _, nick := range []string{"alice", "bob", "carol"} {
		for i := 0; i < 2; i++ {
			if w := doGet(h, "198.51.100.1:5555", as(nick)); w.Code != 200 {
				t.Fatalf("%s request %d: %d", nick, i, w.Code)
			}
		}
	}
	if w := doGet(h, "198.51.100.1:5555", as("alice")); w.Code != http.StatusTooManyRequests {
		t.Fatalf("alice over budget: %d", w.Code)
	}
	if GetLastBan("user:alice") == nil || GetLastBan("198.51.100.1") != nil {
		t.Fatal("strike must be on the user, not the NAT IP")
	}
	// anonymous traffic from that IP still has its own budget
	if w := doGet(h, "198.51.100.1:5555", nil); w.Code != 200 {
		t.Fatalf("anonymous from NAT: %d", w.Code)
	}
}

func TestMiddlewareGlobalCapFailsFastOnPro(t *testing.T) {
	resetShieldState(t)
	withPro(t, true)

	release := make(chan struct{})
	started := make(chan struct{}, 1)
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-release
		w.Write([]byte("done"))
	})
	h := shieldedHandler(t, "cap", utils.SmartShieldPolicy{Enabled: true, MaxGlobalSimultaneous: 1}, slow)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		doGet(h, "203.0.113.1:1", nil)
	}()
	<-started

	begin := time.Now()
	w := doGet(h, "203.0.113.2:1", nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 while the route is full, got %d", w.Code)
	}
	if waited := time.Since(begin); waited > 4*time.Second {
		t.Fatalf("Pro should shed load in ~2s, waited %s", waited)
	}
	close(release)
	wg.Wait()

	// the slot is freed once the slow request returns
	if n := shield.shieldInflight("cap").Load(); n != 0 {
		t.Fatalf("inflight after completion: %d", n)
	}
	if w := doGet(h, "203.0.113.2:1", nil); w.Code != 200 {
		t.Fatalf("after release: %d", w.Code)
	}
}

func TestErrorResponsesCostMore(t *testing.T) {
	resetShieldState(t)
	withPro(t, true)

	fail := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	h := shieldedHandler(t, "errors", utils.SmartShieldPolicy{Enabled: true, PerUserRequestLimit: 100, PolicyStrictness: 1}, fail)

	// each 404 GET costs 30: the 4th request finds 90 spent, the 5th finds 120 > 100
	for i := 0; i < 4; i++ {
		if w := doGet(h, "203.0.113.20:1", nil); w.Code != 404 {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	if w := doGet(h, "203.0.113.20:1", nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after 4 errors, got %d", w.Code)
	}
}

func TestTCPBudgetFoldsOnClose(t *testing.T) {
	resetShieldState(t)
	withPro(t, false)

	policy := utils.ApplySmartShieldDefaults(utils.SmartShieldPolicy{Enabled: true})
	route := utils.ProxyRouteConfig{Name: "db"}

	client, server := net.Pipe()
	defer client.Close()
	w := TCPSmartShieldWrapper(server, "tcp-db", route, policy)
	// pipe addresses have no host:port, so the wrapper keys on ""
	if got := socketShield.GetServerConnections("tcp-db"); got != 1 {
		t.Fatalf("live connections: %d", got)
	}

	go io.WriteString(client, "hello")
	buf := make([]byte, 16)
	n, _ := w.Read(buf)
	if n != 5 {
		t.Fatalf("read %d", n)
	}
	u := socketShield.GetUserUsedBudgets("tcp-db", w.ClientID)
	if u.Bytes != 5 || u.Packets != 1 || u.Simultaneous != 1 {
		t.Fatalf("live usage: %+v", u)
	}

	w.Close()
	w.Close() // idempotent
	if got := socketShield.GetServerConnections("tcp-db"); got != 0 {
		t.Fatalf("live connections after close: %d", got)
	}
	u = socketShield.GetUserUsedBudgets("tcp-db", w.ClientID)
	if u.Bytes != 5 || u.Packets != 1 || u.Simultaneous != 0 {
		t.Fatalf("usage after close: %+v", u)
	}
}

func TestTCPSimultaneousStrike(t *testing.T) {
	resetShieldState(t)
	withPro(t, false)

	policy := utils.ApplySmartShieldDefaults(utils.SmartShieldPolicy{Enabled: true, PerUserSimultaneous: 2, PolicyStrictness: 1})
	u := userUsedBudget{ClientID: "203.0.113.30", Simultaneous: 2}
	if !IsAllowedToConnect("tcp", policy, u) {
		t.Fatal("at the limit is allowed")
	}
	u.Simultaneous = 3
	if IsAllowedToConnect("tcp", policy, u) {
		t.Fatal("over the limit strikes")
	}
	if GetLastBan("203.0.113.30") == nil {
		t.Fatal("expected a strike")
	}
	// enforcement strikes once per client, and idle clients are forgotten
	socketShield.EnforceBudget()
	if socketShield.cleanup(time.Now().Add(2*time.Hour)) != 0 {
		t.Fatal("nothing tracked yet for that client")
	}
}

func withWhitelist(t *testing.T, entries ...utils.ShieldWhitelistEntry) {
	t.Helper()
	cfg := utils.GetMainConfig()
	cfg.ShieldWhitelist = entries
	utils.LoadBaseMainConfig(cfg)
}

func TestWhitelistExemptsFromShield(t *testing.T) {
	resetShieldState(t)
	withPro(t, true)
	withWhitelist(t, utils.ShieldWhitelistEntry{IP: "203.0.113.0/24", Label: "office"})

	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hi")) })
	h := shieldedHandler(t, "wl", utils.SmartShieldPolicy{Enabled: true, PerUserRequestLimit: 1, PolicyStrictness: 1}, ok)

	for i := 0; i < 5; i++ {
		if w := doGet(h, "203.0.113.77:1", nil); w.Code != 200 {
			t.Fatalf("whitelisted request %d: %d", i, w.Code)
		}
	}
	if GetLastBan("203.0.113.77") != nil {
		t.Fatal("whitelisted IP must never be struck")
	}
	// the CIDR does not cover this one
	doGet(h, "198.51.100.5:1", nil)
	if w := doGet(h, "198.51.100.5:1", nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("non-whitelisted: %d", w.Code)
	}

	// TCP and UDP read the same list
	if !utils.IsShieldWhitelisted("203.0.113.1") || utils.IsShieldWhitelisted("203.0.114.1") {
		t.Fatal("whitelist match")
	}
}

func TestWhitelistBypassFlags(t *testing.T) {
	resetShieldState(t)
	withWhitelist(t,
		utils.ShieldWhitelistEntry{IP: "203.0.113.10", BypassIPRestriction: true},
		utils.ShieldWhitelistEntry{IP: "203.0.113.11", BypassGeo: true},
	)

	// route-level IP restriction: only the entry with the flag gets through
	if !utils.CheckRouteIPAccess("203.0.113.10", "203.0.113.10", false, []string{"10.0.0.0/8"}) {
		t.Fatal("BypassIPRestriction should satisfy a route whitelist")
	}
	if utils.CheckRouteIPAccess("203.0.113.11", "203.0.113.11", false, []string{"10.0.0.0/8"}) {
		t.Fatal("an entry without the flag must not bypass route whitelists")
	}
	if !utils.CheckRouteIPAccess("203.0.113.10", "203.0.113.10", true, nil) {
		t.Fatal("BypassIPRestriction should satisfy RestrictToConstellation")
	}
	if utils.ShieldBypassesGeo("203.0.113.10") || !utils.ShieldBypassesGeo("203.0.113.11") {
		t.Fatal("geo bypass flag")
	}

	// the abuse gate that drops TCP/UDP outright is skipped for whitelisted IPs
	policy := utils.ApplySmartShieldDefaults(utils.SmartShieldPolicy{Enabled: true})
	for i := 0; i < 300; i++ {
		utils.IncrementIPAbuseCounter("203.0.113.11")
	}
	t.Cleanup(func() { utils.ResetIPAbuseCounter("203.0.113.11") })
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	ln := &whitelistedAddrConn{Conn: server, addr: "203.0.113.11:4444"}
	if got := TCPSmartShieldMiddleware("tcp-wl", utils.ProxyRouteConfig{Name: "wl", SmartShield: policy})(ln); got == nil {
		t.Fatal("whitelisted IP should connect despite the abuse counter")
	}
}

type whitelistedAddrConn struct {
	net.Conn
	addr string
}

func (c *whitelistedAddrConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP(c.addr[:len(c.addr)-5]), Port: 4444}
}

func TestUnbanClearsEverything(t *testing.T) {
	resetShieldState(t)
	withPro(t, false)
	now := time.Now()

	globalShieldState.strike("203.0.113.40", "s", utils.ShieldBanReason{Limit: "bytes", Used: 10, Allowed: 5, Route: "s"}, now)
	utils.IncrementIPAbuseCounter("203.0.113.40")
	if globalShieldState.allowed("203.0.113.40", "s", now) {
		t.Fatal("struck client is blocked")
	}

	statuses := globalShieldState.statuses(now)
	if len(statuses) != 1 || statuses[0].Status != "strike" || statuses[0].BlockedUntil == nil || len(statuses[0].History) != 1 {
		t.Fatalf("statuses: %+v", statuses)
	}
	if statuses[0].History[0].Reason.Limit != "bytes" || statuses[0].History[0].Node != "local" {
		t.Fatalf("history entry: %+v", statuses[0].History[0])
	}

	if removed := globalShieldState.unban("203.0.113.40"); removed != 1 {
		t.Fatalf("removed %d", removed)
	}
	if !globalShieldState.allowed("203.0.113.40", "s", now) || utils.GetIPAbuseCounter("203.0.113.40") != 0 {
		t.Fatal("unban must clear bans and the abuse counter")
	}
	if len(globalShieldState.statuses(now)) != 0 || GetShield() != 0 {
		t.Fatal("nothing should be tracked after unban")
	}
}

func TestRemoteBansMergeAndReconcile(t *testing.T) {
	resetShieldState(t)
	withPro(t, false)
	now := time.Now()

	remote := utils.ShieldBan{
		ID: "abc.1", ClientID: "203.0.113.50", BanType: TEMP, Time: now, Node: "other",
		Reason: utils.ShieldBanReason{Limit: "escalation", Route: "r"},
	}
	globalShieldState.applyRemote(remote)
	globalShieldState.applyRemote(remote) // duplicates are ignored
	if globalShieldState.allowed("203.0.113.50", "s", now) {
		t.Fatal("a ban issued elsewhere blocks here")
	}
	if globalShieldState.count() != 1 {
		t.Fatalf("count %d", globalShieldState.count())
	}

	// a local strike survives reconcile, the remote one goes when the bucket no longer has it
	globalShieldState.strike("203.0.113.51", "s", utils.ShieldBanReason{Limit: "requests"}, now)
	globalShieldState.reconcile(map[string]bool{})
	if globalShieldState.allowed("203.0.113.50", "s", now) == false {
		t.Fatal("remote ban missing from the snapshot should be dropped")
	}
	if globalShieldState.allowed("203.0.113.51", "s", now) {
		t.Fatal("local strikes are not subject to reconcile")
	}

	// a delete op removes by id
	globalShieldState.applyRemote(remote)
	globalShieldState.removeRemote("abc.1")
	if !globalShieldState.allowed("203.0.113.50", "s", now) {
		t.Fatal("deleted remote ban should not block")
	}

	// each node only expires its own entries on the bucket; all expire locally
	old := remote
	old.ID, old.Time = "abc.2", now.Add(-200*time.Hour)
	globalShieldState.applyRemote(old)
	if globalShieldState.cleanup(now) != 1 {
		t.Fatal("expired remote entry should be dropped locally")
	}
}
