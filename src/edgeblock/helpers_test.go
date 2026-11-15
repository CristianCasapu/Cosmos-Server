package edgeblock

// What the tests share. Nothing here reaches the network: Cloudflare's API is
// the stand-in (cffake), and a call meant for any other host fails the test.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/cloudflare"
	"github.com/azukaar/cosmos-server/src/cloudflare/cffake"
	"github.com/azukaar/cosmos-server/src/utils"
)

// tokens made at run time from parts, obviously not real
var (
	fwToken  = "cfut_" + strings.Repeat("Fw", 20) + "edgetest01"
	dnsToken = "cfut_" + strings.Repeat("Dn", 20) + "edgetest02"
)

type clock struct {
	mu sync.Mutex
	at time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
}

type testEnv struct {
	t      *testing.T
	fake   *cffake.Server
	clock  *clock
	engine *Engine
	events *[]utils.SecurityEvent
	evMu   *sync.Mutex
	notes  *[]string
}

// setup: the zone example.org with two shielded names, its token allowed to
// manage firewall rules (or, with dnsOnly, the "Edit zone DNS" token), a
// configuration of its own, an engine with a clock of its own.
func setup(t *testing.T, dnsOnly bool, change func(config *utils.Config)) *testEnv {
	t.Helper()
	if dnsOnly {
		return setupWith(t, cffake.Token{Zones: []string{"example.org"}, ReadDNS: true, EditDNS: true}, change)
	}
	return setupWith(t, cffakeToken(true), change)
}

// setupWith: the same, with a token of the rights given (on top of none).
func setupWith(t *testing.T, rights cffake.Token, change func(config *utils.Config)) *testEnv {
	t.Helper()
	fake := cffake.New()
	fake.AddZone("example.org")
	fake.AddZone("example.net")
	if rights.Zones == nil {
		rights.Zones = []string{"example.org"}
	}
	fake.AddToken(fwToken, rights)
	server := httptest.NewServer(fake)
	target, _ := url.Parse(server.URL)
	restoreTransport := cloudflare.ForkTestTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Scheme != "https" || req.URL.Host != "api.cloudflare.com" {
			t.Errorf("a request left for %s", req.URL.Host)
		}
		clone := req.Clone(req.Context())
		clone.URL.Scheme, clone.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(clone)
	})

	previousFolder := utils.CONFIGFOLDER
	previousConfig := utils.GetBaseMainConfig()
	utils.CONFIGFOLDER = t.TempDir() + "/"
	token := fwToken
	config := utils.Config{}
	config.HTTPConfig.Hostname = "cosmos.example.org"
	config.HTTPConfig.DNSZones = []utils.DNSZoneConfig{{
		Zone: "example.org", DNSChallengeProvider: "cloudflare",
		DNSChallengeConfig: map[string]string{"CF_DNS_API_TOKEN": token},
	}}
	config.HTTPConfig.Cloudflare.Shielded = []string{"cosmos.example.org", "app.example.org"}
	config.HTTPConfig.Cloudflare.TrustRanges = true
	if change != nil {
		change(&config)
	}
	utils.SetBaseMainConfig(config)

	c := &clock{at: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)}
	engine := newEngine(statePath, c.now)
	previousStd := std
	std = engine

	events := &[]utils.SecurityEvent{}
	evMu := &sync.Mutex{}
	notes := &[]string{}
	previousRecord, previousNotify := recordEvent, notifyAdmins
	recordEvent = func(event utils.SecurityEvent) {
		evMu.Lock()
		*events = append(*events, event)
		evMu.Unlock()
	}
	notifyAdmins = func(title string, message string, vars string) {
		evMu.Lock()
		*notes = append(*notes, message+" "+vars)
		evMu.Unlock()
	}
	previousIfaces, previousPublic, previousLocal := interfaceNetworks, publicAddress, isLocalAddress
	interfaceNetworks = func() []netip.Prefix {
		return []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24"), netip.MustParsePrefix("2001:db8:1::/64")}
	}
	publicAddress = func() string { return "203.0.113.10" }
	isLocalAddress = func(ip string) bool {
		addr, err := netip.ParseAddr(ip)
		return err == nil && (addr.IsPrivate() || addr.IsLoopback())
	}
	notified.Lock()
	notified.at = map[string]time.Time{}
	notified.Unlock()

	t.Cleanup(func() {
		server.Close()
		restoreTransport()
		std = previousStd
		recordEvent, notifyAdmins = previousRecord, previousNotify
		interfaceNetworks, publicAddress, isLocalAddress = previousIfaces, previousPublic, previousLocal
		utils.CONFIGFOLDER = previousFolder
		utils.LoadBaseMainConfig(previousConfig)
	})
	return &testEnv{t: t, fake: fake, clock: c, engine: engine, events: events, evMu: evMu, notes: notes}
}

// enableDirectly switches the feature on with a proof, as the route does.
func (env *testEnv) enable() {
	env.t.Helper()
	code, body := env.post(map[string]interface{}{"action": "enable"})
	if code != 200 {
		env.t.Fatalf("enable: %d %v", code, body)
	}
}

// run lets the worker do everything it can now.
func (env *testEnv) run() {
	for i := 0; i < 200 && env.engine.step(context.Background()); i++ {
	}
}

// drain refills the call bucket and runs (time passes for calls only).
func (env *testEnv) drain() {
	env.engine.mu.Lock()
	env.engine.calls.full = false
	env.engine.mu.Unlock()
	env.run()
}

func edgeEvent(kind string, ip string) utils.SecurityEvent {
	return utils.SecurityEvent{Kind: kind, Class: "incident", IP: ip, Edge: true, Forwarded: true}
}

// offend sends n events of a kind for an address.
func (env *testEnv) offend(kind string, ip string, n int) {
	for i := 0; i < n; i++ {
		event := edgeEvent(kind, ip)
		event.Time = env.clock.now()
		env.engine.Observe(event)
	}
}

func (env *testEnv) rules() []Rule {
	env.engine.mu.Lock()
	defer env.engine.mu.Unlock()
	out := []Rule{}
	for _, r := range env.engine.st.Rules {
		out = append(out, *r)
	}
	return out
}

func (env *testEnv) ruleOf(key string) (Rule, bool) {
	for _, r := range env.rules() {
		if r.Key == key && r.Reason != "test" {
			return r, true
		}
	}
	return Rule{}, false
}

func (env *testEnv) atCloudflare() []cffake.AccessRule {
	return env.fake.AccessRules("example.org")
}

func (env *testEnv) kinds() []string {
	env.evMu.Lock()
	defer env.evMu.Unlock()
	out := []string{}
	for _, event := range *env.events {
		out = append(out, event.Kind+":"+event.Details["reason"])
	}
	return out
}

func asAdmin(req *http.Request, permissions ...utils.Permission) *http.Request {
	if len(permissions) == 0 {
		permissions = []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN_READ, utils.PERM_CONFIGURATION_READ, utils.PERM_CONFIGURATION}
	}
	req.RemoteAddr = "192.168.1.20:50000"
	return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{
		Nickname: "admin", Permissions: permissions, IsSudoed: true, UserRole: utils.ADMIN,
	}))
}

func (env *testEnv) post(body interface{}, permissions ...utils.Permission) (int, map[string]interface{}) {
	env.t.Helper()
	raw, _ := json.Marshal(body)
	return env.do(asAdmin(httptest.NewRequest("POST", "/cosmos/api/cloudflare/edge-blocks", bytes.NewReader(raw)), permissions...))
}

func (env *testEnv) do(req *http.Request) (int, map[string]interface{}) {
	env.t.Helper()
	recorder := httptest.NewRecorder()
	StatusRoute(recorder, req)
	text := recorder.Body.String()
	if strings.Contains(text, fwToken) || strings.Contains(text, dnsToken) {
		env.t.Errorf("an answer contains the token: %s", text)
	}
	var decoded map[string]interface{}
	json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder.Code, decoded
}

type cloudflareZone = cloudflare.EdgeZone

// cffakeToken is the rights of a token that may or may not manage firewall rules.
func cffakeToken(firewall bool) cffake.Token {
	return cffake.Token{Zones: []string{"example.org"}, ReadDNS: true, EditDNS: true, ReadFirewall: firewall, EditFirewall: firewall}
}
