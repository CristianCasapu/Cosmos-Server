package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHostHomeOnlyIsTheProxysRule(t *testing.T) {
	for host, want := range map[string]bool{
		"":                                  false, // no host: the URL answers every name
		"s3":                                false, // a bare name: anyone may send it
		"localhost":                         false,
		"s3.example.org":                    false,
		"*.example.org":                     false,
		"not a host":                        false,
		"s3.local":                          true,
		"S3.Local:2053":                     true,
		"192.168.1.20":                      true,
		"10.0.0.5:9000":                     true,
		"100.101.102.103":                   false, // a VPN (CGNAT) address: the proxy does not confine it
		"203.0.113.10":                      false,
		strings.Repeat("a", 300) + ".local": false,
	} {
		if got := HostHomeOnly(host); got != want {
			t.Errorf("HostHomeOnly(%q) = %v, want %v", host, got, want)
		}
		if got := isLocalAddress(host); len(host) <= 260 && got != want {
			t.Errorf("the route's answer and the proxy's rule disagree on %q", host)
		}
	}
}

func TestHostHomeOnlyRouteNeedsConfigurationRead(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/cosmos/api/host-home-only?host=s3.local", nil)
	rec := httptest.NewRecorder()
	HostHomeOnlyRoute(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("a request without a session got %d", rec.Code)
	}
}
