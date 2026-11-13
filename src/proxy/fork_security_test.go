package proxy

// Fork addition: tests of what the proxy and its shields report to the
// security record (fork_security.go and the hooks in the shield code).

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func captureSecurity(t *testing.T) func() []utils.SecurityEvent {
	t.Helper()
	var mu sync.Mutex
	events := []utils.SecurityEvent{}
	utils.SetSecurityRecorder(func(event utils.SecurityEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}, func() {})
	t.Cleanup(func() { utils.SetSecurityRecorder(nil, nil) })
	return func() []utils.SecurityEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]utils.SecurityEvent(nil), events...)
	}
}

func kindsRecorded(events []utils.SecurityEvent) string {
	kinds := make([]string, len(events))
	for i, event := range events {
		kinds[i] = event.Kind
	}
	return strings.Join(kinds, " ")
}

// Strikes and bans all go through one place in the ban store: an address is
// recorded as an address, a logged-in user as a user.
func TestSecurityShieldStrikesAndBansAreRecorded(t *testing.T) {
	resetShieldState(t)
	recorded := captureSecurity(t)
	store := newBanStore()
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)

	store.strike("203.0.113.9", "media", utils.ShieldBanReason{Limit: "requests", Used: 31, Allowed: 30, Route: "media"}, now)
	store.strike("user:alice", "media", utils.ShieldBanReason{Limit: "bytes", Route: "media"}, now)
	store.Lock()
	store.issue("203.0.113.9", TEMP, "media", utils.ShieldBanReason{Limit: "escalation", Route: "media"}, now)
	store.issue("2001:db8::9", PERM, "media", utils.ShieldBanReason{Limit: "escalation", Route: "media"}, now)
	store.Unlock()

	events := recorded()
	if got := kindsRecorded(events); got != utils.SecShieldStrike+" "+utils.SecShieldStrike+" "+utils.SecShieldBanTemp+" "+utils.SecShieldBanPerm {
		t.Fatalf("recorded %s", got)
	}
	if events[0].IP != "203.0.113.9" || events[0].Actor != "" || events[0].Details["limit"] != "requests" || events[0].Details["route"] != "media" || !events[0].Time.Equal(now) {
		t.Errorf("strike of an address = %+v", events[0])
	}
	if events[1].IP != "" || events[1].Actor != "alice" {
		t.Errorf("strike of a user = %+v: a user is not an address", events[1])
	}
	if events[3].IP != "2001:db8::9" {
		t.Errorf("permanent ban = %+v", events[3])
	}
}

// Three strikes become a temporary ban on the next request: recorded too,
// since the escalation issues it through the same place.
func TestSecurityShieldEscalationIsRecorded(t *testing.T) {
	resetShieldState(t)
	recorded := captureSecurity(t)
	store := newBanStore()
	start := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		store.strike("203.0.113.9", "media", utils.ShieldBanReason{Limit: "requests", Route: "media"}, start.Add(time.Duration(i)*2*time.Hour))
	}
	if store.allowed("203.0.113.9", "media", start.Add(7*time.Hour)) {
		t.Fatal("three strikes did not ban")
	}
	events := recorded()
	if len(events) != 4 || events[3].Kind != utils.SecShieldBanTemp || events[3].IP != "203.0.113.9" || events[3].Details["limit"] != "escalation" {
		t.Fatalf("recorded %+v, want three strikes and a temporary ban", events)
	}
}

func TestSecurityShieldRecordsRefusedRequests(t *testing.T) {
	resetShieldState(t)
	withPro(t, true)
	recorded := captureSecurity(t)

	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hi")) })
	h := shieldedHandler(t, "budget", utils.SmartShieldPolicy{Enabled: true, PerUserRequestLimit: 3, PolicyStrictness: 1}, ok)

	codes := []int{}
	for i := 0; i < 5; i++ {
		codes = append(codes, doGet(h, "203.0.113.9:1234", nil).Code)
	}
	if codes[2] != 200 || codes[3] != http.StatusTooManyRequests || codes[4] != http.StatusTooManyRequests {
		t.Fatalf("answers = %v", codes)
	}

	events := recorded()
	if got := kindsRecorded(events); got != utils.SecShieldStrike+" "+utils.SecShieldBlocked+" "+utils.SecShieldBlocked {
		t.Fatalf("recorded %s, want the strike and the two refused requests", got)
	}
	blocked := events[1]
	if blocked.IP != "203.0.113.9" || blocked.Details["route"] != "budget" || blocked.Host != "app.example" || blocked.Path != "/" {
		t.Errorf("refused request = %+v", blocked)
	}
}

func TestSecurityBotAndLocalOnlyRefusalsAreRecorded(t *testing.T) {
	resetShieldState(t)
	recorded := captureSecurity(t)
	reached := 0
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached++ })

	bots := BotDetectionMiddleware(next)
	request := func(agent string) *http.Request {
		req := httptest.NewRequest("GET", "https://media.example/library", nil)
		req.RemoteAddr = "86.120.1.1:40000"
		req.Header.Set("User-Agent", agent)
		return req
	}
	noAgent, listed, browser := httptest.NewRecorder(), httptest.NewRecorder(), httptest.NewRecorder()
	bots.ServeHTTP(noAgent, request(""))
	bots.ServeHTTP(listed, request("sqlmap"))
	bots.ServeHTTP(browser, request("Mozilla/5.0"))

	outside := httptest.NewRecorder()
	local := httptest.NewRequest("GET", "https://jellyfin.local/", nil)
	local.RemoteAddr = "86.120.1.1:40000"
	localNetworkOnly(next).ServeHTTP(outside, local)

	if noAgent.Code != http.StatusForbidden || listed.Code != http.StatusForbidden || browser.Code != http.StatusOK || outside.Code != http.StatusForbidden || reached != 1 {
		t.Fatalf("answers %d %d %d %d, %d let through", noAgent.Code, listed.Code, browser.Code, outside.Code, reached)
	}
	// the metrics of the bot filter are pushed from goroutines: let them finish
	time.Sleep(50 * time.Millisecond)

	events := recorded()
	if got := kindsRecorded(events); got != utils.SecShieldBot+" "+utils.SecShieldBot+" "+utils.SecAccessLocalDenied {
		t.Fatalf("recorded %s", got)
	}
	for _, event := range events {
		if event.IP != "86.120.1.1" {
			t.Errorf("event = %+v, want the visitor's address", event)
		}
	}
	if events[2].Host != "jellyfin.local" {
		t.Errorf("local-only refusal = %+v", events[2])
	}
}
