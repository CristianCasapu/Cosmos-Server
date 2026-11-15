package proxy

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestIsLocalVisitorRequestUsesTheVisitorTheForkBelieves(t *testing.T) {
	for address, want := range map[string]bool{
		"192.168.1.5": true, "10.0.0.2": true, "127.0.0.1": true, "100.64.3.4": true, "fe80::1": true,
		"203.0.113.7": false, "2001:db8::7": false, "": false,
	} {
		req := httptest.NewRequest("POST", "/cosmos/api/login", nil)
		req.RemoteAddr = "192.168.1.200:1234" // the peer: a proxy at home
		if address != "" {
			req = req.WithContext(context.WithValue(req.Context(), "ClientID", address))
		} else {
			req.RemoteAddr = "203.0.113.9:1"
		}
		// a header from the visitor is never read
		req.Header.Set("X-Forwarded-For", "192.168.1.9")
		if got := IsLocalVisitorRequest(req); got != want {
			t.Errorf("%q: %v, want %v", address, got, want)
		}
	}
}
