package proxy

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// threeTempBans gives a client three temporary bans within the seven-day
// window, each one served, and returns the moment after the third: the next
// look at the client is where a permanent ban would be issued.
func threeTempBans(t *testing.T, client string) time.Time {
	t.Helper()
	at := time.Now()
	for i, block := range []time.Duration{4 * time.Hour, 24 * time.Hour, 72 * time.Hour} {
		globalShieldState.Lock()
		globalShieldState.issue(client, TEMP, "s", utils.ShieldBanReason{Limit: "escalation"}, at)
		globalShieldState.Unlock()
		if globalShieldState.allowed(client, "s", at.Add(block-time.Minute)) {
			t.Fatalf("%s: temporary ban %d should last %s", client, i+1, block)
		}
		at = at.Add(block + time.Hour)
	}
	return at
}

func lastBan(client string) *UserBan {
	globalShieldState.Lock()
	defer globalShieldState.Unlock()
	bans := globalShieldState.byClient[client]
	if len(bans) == 0 {
		return nil
	}
	return bans[len(bans)-1]
}

func TestLocalAddressIsNeverBannedPermanently(t *testing.T) {
	resetShieldState(t)
	withPro(t, false) // the profile of this install: permanent bans are issued

	for _, client := range []string{"192.168.1.50", "172.16.0.20", "10.9.8.7", "127.0.0.1", "fd00::5", "fe80::1234", "fe80::1234%eth0", "::ffff:192.168.1.50"} {
		at := threeTempBans(t, client)

		if globalShieldState.allowed(client, "s", at) {
			t.Errorf("%s: a fourth offence in the window must still block", client)
		}
		last := lastBan(client)
		if last == nil || last.BanType != TEMP {
			t.Errorf("%s: expected another temporary ban, got %s", client, describeBan(last))
			continue
		}
		// it lasts what a repeated temporary ban lasts, then the address is free again
		if globalShieldState.allowed(client, "s", at.Add(71*time.Hour)) {
			t.Errorf("%s: the repeated temporary ban should last 72 hours", client)
		}
		if !globalShieldState.allowed(client, "s", at.Add(8*24*time.Hour)) {
			t.Errorf("%s: a local address must be allowed again once its temporary bans are over", client)
		}
		for _, ban := range globalShieldState.byClient[client] {
			if ban.BanType == PERM {
				t.Errorf("%s: holds a permanent ban: %s", client, describeBan(ban))
			}
		}
	}
}

func TestInternetAddressAndUserAreBannedPermanentlyAsBefore(t *testing.T) {
	resetShieldState(t)
	withPro(t, false)

	// an address from the internet, the carrier NAT range (not a local link),
	// and a ban keyed on a logged-in user, whatever its name looks like
	for _, client := range []string{"203.0.113.9", "2001:db8::1", "100.101.102.103", "user:alice", "user:192.168.1.50", "printer.local"} {
		at := threeTempBans(t, client)

		if globalShieldState.allowed(client, "s", at) {
			t.Errorf("%s: a fourth offence must block", client)
		}
		if last := lastBan(client); last == nil || last.BanType != PERM {
			t.Errorf("%s: expected a permanent ban, got %s", client, describeBan(last))
		}
		if globalShieldState.allowed(client, "s", at.Add(1000*time.Hour)) {
			t.Errorf("%s: a permanent ban does not end", client)
		}
	}
}

// The address the rule looks at is the one the shield keys its bans on. A
// visitor from the internet who sends a local address in X-Forwarded-For is
// still that visitor: no proxy is trusted here, so the header is ignored.
func TestForgedForwardedForDoesNotMakeAVisitorLocal(t *testing.T) {
	resetShieldState(t)
	withPro(t, false)

	req := httptest.NewRequest("POST", "https://cosmos.example.com/cosmos/api/login", nil)
	req.RemoteAddr = "86.120.1.1:40000"
	req.Header.Set("X-Forwarded-For", "192.168.1.5")
	req.Header.Set("X-Real-IP", "192.168.1.5")

	client := GetShieldIdentity(req, GetClientID(req, utils.ProxyRouteConfig{}))
	if client != "86.120.1.1" {
		t.Fatalf("the shield identity of the request is %q, expected the peer address", client)
	}
	if isLocalClient(client) {
		t.Fatalf("%s counts as local", client)
	}

	at := threeTempBans(t, client)
	if globalShieldState.allowed(client, "s", at) {
		t.Error("a fourth offence must block")
	}
	if last := lastBan(client); last == nil || last.BanType != PERM {
		t.Errorf("expected a permanent ban for the visitor from the internet, got %s", describeBan(last))
	}
	if bans := globalShieldState.byClient["192.168.1.5"]; len(bans) != 0 {
		t.Errorf("the forged address received bans: %v", bans)
	}
}

// The shield and the local links must agree on what a local address is.
func TestIsLocalClientMatchesLocalLinks(t *testing.T) {
	for _, address := range []string{"192.168.1.10", "172.16.0.14", "10.0.0.5", "127.0.0.1", "169.254.10.1", "::1", "fe80::1", "fd12:3456::1", "8.8.8.8", "172.32.0.1", "100.64.0.1", "2a02:2f04::1", "2001:4860:4860::8888"} {
		if isLocalClient(address) != isLocalAddress(address) {
			t.Errorf("%s: the shield says local=%v, the local links say local=%v", address, isLocalClient(address), isLocalAddress(address))
		}
	}
	for _, notAnAddress := range []string{"", "user:alice", "user:10.0.0.1", "jellyfin.local", "192.168.1.10:443", "not-an-ip"} {
		if isLocalClient(notAnAddress) {
			t.Errorf("%q is not a bare address and must not count as a local client", notAnAddress)
		}
	}
}
