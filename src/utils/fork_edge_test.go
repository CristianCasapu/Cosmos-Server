package utils

import (
	"net/http/httptest"
	"testing"
)

// Fork addition: tests of fork_edge.go. Only a visitor Cloudflare's proxy
// reported for a shielded hostname is an edge visitor.
func TestEdgeVisitorIsOnlyWhatCloudflareReportedForAShieldedName(t *testing.T) {
	prev := GetBaseMainConfig()
	config := Config{}
	config.HTTPConfig.Cloudflare.Shielded = []string{"app.example.org"}
	config.HTTPConfig.Cloudflare.TrustRanges = true
	config.HTTPConfig.TrustedProxies = []string{"10.0.0.2"}
	LoadBaseMainConfig(config)
	t.Cleanup(func() { LoadBaseMainConfig(prev) })

	cases := []struct {
		name   string
		host   string
		peer   string
		header string
		xff    string
		edge   bool
	}{
		{"Cloudflare for a shielded name", "app.example.org", "173.245.48.5:4000", "198.51.100.7", "", true},
		{"Cloudflare for a name served directly", "direct.example.org", "173.245.48.5:4000", "198.51.100.7", "", false},
		{"a direct visitor sending the header", "app.example.org", "198.51.100.9:4000", "198.51.100.7", "", false},
		{"a trusted proxy of the owner's", "app.example.org", "10.0.0.2:4000", "", "198.51.100.7", false},
		{"Cloudflare claiming a local visitor", "app.example.org", "173.245.48.5:4000", "192.168.1.5", "", false},
		{"no header", "app.example.org", "173.245.48.5:4000", "", "", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "https://"+c.host+"/", nil)
		req.RemoteAddr = c.peer
		if c.header != "" {
			req.Header.Set("CF-Connecting-IP", c.header)
		}
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		client := GetClientIP(req)
		event := securityEventOf(req, "", SecLoginFailed, "", nil)
		if event.Edge != c.edge || IsEdgeVisitor(req, client) != c.edge {
			t.Errorf("%s: edge %v (client %s), want %v", c.name, event.Edge, client, c.edge)
		}
	}
	// Cloudflare is believed only while TrustRanges is on
	config.HTTPConfig.Cloudflare.TrustRanges = false
	LoadBaseMainConfig(config)
	req := httptest.NewRequest("GET", "https://app.example.org/", nil)
	req.RemoteAddr = "173.245.48.5:4000"
	req.Header.Set("CF-Connecting-IP", "198.51.100.7")
	if IsEdgeVisitor(req, "198.51.100.7") {
		t.Error("believed without TrustRanges")
	}
	// and what it reported is remembered for SmartShield's strikes
	config.HTTPConfig.Cloudflare.TrustRanges = true
	LoadBaseMainConfig(config)
	req.Header.Set("CF-Connecting-IP", "198.51.100.66")
	GetClientIP(req)
	if !WasEdgeClient("198.51.100.66") || WasEdgeClient("198.51.100.67") {
		t.Error("edge memory")
	}
}
