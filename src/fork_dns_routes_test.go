package main

// Fork addition: the name lookups of the address forms answer accounts that
// may read the configuration, not every account; a name with only an IPv6
// address is answered instead of ending the request.

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func dnsRequest(path string, perms ...utils.Permission) *http.Request {
	req := httptest.NewRequest("GET", path, nil)
	auth := &utils.AuthContext{Nickname: "someone", Permissions: append([]utils.Permission{utils.PERM_LOGIN}, perms...)}
	return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
}

func TestNameLookupsNeedConfigurationRead(t *testing.T) {
	prev := utils.GetBaseMainConfig()
	utils.LoadBaseMainConfig(utils.Config{})
	t.Cleanup(func() { utils.LoadBaseMainConfig(prev) })
	captureSecurity(t)

	for name, route := range map[string]http.HandlerFunc{"dns": GetDNSRoute, "dns-check": CheckDNSRoute} {
		rec := httptest.NewRecorder()
		route(rec, dnsRequest("/cosmos/api/"+name+"?url=localhost"))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s for an account that may not read the configuration: %d, want 401", name, rec.Code)
		}
		rec = httptest.NewRecorder()
		route(rec, dnsRequest("/cosmos/api/"+name+"?url=localhost", utils.PERM_CONFIGURATION_READ))
		if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
			t.Errorf("%s for a configuration reader: refused (%d)", name, rec.Code)
		}
	}
}

func TestNameLookupWithOnlyIPv6(t *testing.T) {
	prev := utils.GetBaseMainConfig()
	utils.LoadBaseMainConfig(utils.Config{})
	t.Cleanup(func() { utils.LoadBaseMainConfig(prev) })
	ips, err := net.LookupIP("ip6-localhost")
	if err != nil || len(ips) == 0 {
		t.Skip("no name with only an IPv6 address on this machine")
	}
	for _, ip := range ips {
		if ip.To4() != nil {
			t.Skip("ip6-localhost has an IPv4 address here")
		}
	}
	rec := httptest.NewRecorder()
	GetDNSRoute(rec, dnsRequest("/cosmos/api/dns?url=ip6-localhost", utils.PERM_CONFIGURATION_READ))
	if rec.Code != http.StatusInternalServerError || rec.Body.Len() == 0 {
		t.Errorf("a name with only an IPv6 address: %d %q, want 500 with a sentence", rec.Code, rec.Body.String())
	}
}
