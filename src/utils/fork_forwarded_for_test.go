package utils

import (
	"net/http/httptest"
	"testing"
)

// Fork addition: tests of fork_forwarded_for.go.

func TestClientAddressBehindATrustedProxy(t *testing.T) {
	prev := GetBaseMainConfig()
	config := Config{}
	config.HTTPConfig.TrustedProxies = []string{"10.0.0.2", "173.245.48.0/20"}
	LoadBaseMainConfig(config)
	t.Cleanup(func() { LoadBaseMainConfig(prev) })

	cases := []struct {
		name   string
		peer   string
		header []string
		want   string
	}{
		{"no proxy, no header", "86.120.1.1:40000", nil, "86.120.1.1"},
		{"a visitor without a proxy cannot name an address", "86.120.1.1:40000", []string{"192.168.1.5"}, "86.120.1.1"},
		{"the proxy reports the visitor", "10.0.0.2:5000", []string{"86.120.1.1"}, "86.120.1.1"},
		// the proxy appended the real address after what the visitor sent
		{"a visitor names a local address in front", "10.0.0.2:5000", []string{"192.168.1.5, 86.120.1.1"}, "86.120.1.1"},
		{"a visitor names several addresses in front", "10.0.0.2:5000", []string{"127.0.0.1, 10.0.0.2, 192.168.1.5, 86.120.1.1"}, "86.120.1.1"},
		{"the same over two header lines", "10.0.0.2:5000", []string{"192.168.1.5", "86.120.1.1"}, "86.120.1.1"},
		{"two trusted proxies in a row", "10.0.0.2:5000", []string{"86.120.1.1, 173.245.48.7"}, "86.120.1.1"},
		{"a visitor claims to be the proxy itself", "10.0.0.2:5000", []string{"10.0.0.2, 86.120.1.1"}, "86.120.1.1"},
		{"every entry is a trusted proxy: the first one", "10.0.0.2:5000", []string{"173.245.48.7, 10.0.0.2"}, "173.245.48.7"},
		{"text that is not an address in front", "10.0.0.2:5000", []string{"unknown, 86.120.1.1"}, "86.120.1.1"},
		{"text that is not an address at the end: the peer", "10.0.0.2:5000", []string{"86.120.1.1, garbage"}, "10.0.0.2"},
		{"an empty header: the peer", "10.0.0.2:5000", []string{""}, "10.0.0.2"},
		{"an address with a port", "10.0.0.2:5000", []string{"86.120.1.1:51000"}, "86.120.1.1"},
		{"IPv6", "10.0.0.2:5000", []string{"fd00::9, 2a02:2f04::77"}, "2a02:2f04::77"},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "https://cosmos.example.com/", nil)
		req.RemoteAddr = c.peer
		for _, line := range c.header {
			req.Header.Add("X-Forwarded-For", line)
		}
		if got := GetClientIP(req); got != c.want {
			t.Errorf("%s: client is %q, want %q", c.name, got, c.want)
		}
	}
}
