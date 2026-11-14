package utils

// Fork addition: the hostname and Referer guards count a refusal against the
// visitor, also behind a trusted proxy, not against the proxy.

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAbuseIsCountedAgainstTheVisitorBehindAProxy(t *testing.T) {
	prev := GetBaseMainConfig()
	config := Config{}
	config.HTTPConfig.Hostname = "cosmos.example.com"
	config.HTTPConfig.TrustedProxies = []string{"10.0.0.2"}
	LoadBaseMainConfig(config)
	t.Cleanup(func() { LoadBaseMainConfig(prev) })
	t.Cleanup(CleanBannedIPs)
	previousMetrics := PushShieldMetrics
	PushShieldMetrics = func(string) {}
	t.Cleanup(func() { PushShieldMetrics = previousMetrics })

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	guards := map[string]struct {
		handler http.Handler
		method  string
		host    string
	}{
		"hostname":         {EnsureHostname(next), "GET", "other.example.org"},
		"hostname for API": {EnsureHostnameCosmosAPI(next), "GET", "other.example.org"},
		"referer":          {BlockPostWithoutReferer(next), "POST", "cosmos.example.com"},
	}
	for name, guard := range guards {
		CleanBannedIPs()
		req := httptest.NewRequest(guard.method, "https://"+guard.host+"/cosmos/api/users", nil)
		req.Host = guard.host
		req.RemoteAddr = "10.0.0.2:5000"
		req.Header.Set("X-Forwarded-For", "86.120.1.1")
		guard.handler.ServeHTTP(httptest.NewRecorder(), req)
		if got := GetIPAbuseCounter("86.120.1.1"); got != 1 {
			t.Errorf("%s: the visitor has %d refusals counted, want 1", name, got)
		}
		if got := GetIPAbuseCounter("10.0.0.2"); got != 0 {
			t.Errorf("%s: the proxy has %d refusals counted, want 0", name, got)
		}
	}
}
