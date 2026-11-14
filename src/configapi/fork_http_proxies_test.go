package configapi

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func fakeProxyLookups(t *testing.T, containers []proxyCandidate, countries map[string]string) {
	t.Helper()
	oldContainers, oldCountry := proxyContainers, countryOf
	proxyContainers = func() []proxyCandidate { return containers }
	countryOf = func(ip string) string { return countries[ip] }
	t.Cleanup(func() { proxyContainers, countryOf = oldContainers, oldCountry })
}

func TestCheckSeesTheCountryOfAPublicAddressOnly(t *testing.T) {
	fakeProxyLookups(t, nil, map[string]string{"203.0.113.9": "RO", "192.168.1.5": "XX"})
	if got := httpCheckExtrasFor(requestFrom("203.0.113.9"), utils.Config{}).Country; got != "RO" {
		t.Fatalf("country %q", got)
	}
	if got := httpCheckExtrasFor(requestFrom("192.168.1.5"), utils.Config{}).Country; got != "" {
		t.Fatalf("a local address got a country: %q", got)
	}
}

func TestCheckProposesTheProxyInFront(t *testing.T) {
	fakeProxyLookups(t, nil, nil)
	// a request forwarded by a local proxy that is not trusted
	req := requestFrom("172.18.0.5")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := httpCheckExtrasFor(req, utils.Config{}); got.Via != "172.18.0.5" || got.ViaTrusted {
		t.Fatalf("%+v", got)
	}
	// no forwarding header: no proxy
	if got := httpCheckExtrasFor(requestFrom("172.18.0.5"), utils.Config{}); got.Via != "" {
		t.Fatalf("%+v", got)
	}
	// a public peer with a header is never proposed (anyone can send the header)
	req = requestFrom("198.51.100.7")
	req.Header.Set("X-Forwarded-For", "10.0.0.1")
	if got := httpCheckExtrasFor(req, utils.Config{}); got.Via != "" {
		t.Fatalf("a public address proposed: %+v", got)
	}
	// already trusted: the visitor's address differs from the connection's
	req = httptest.NewRequest("POST", "/", nil)
	req.RemoteAddr = "172.18.0.5:5000"
	req = req.WithContext(context.WithValue(req.Context(), "ClientID", "203.0.113.9"))
	if got := httpCheckExtrasFor(req, utils.Config{}); got.Via != "172.18.0.5" || !got.ViaTrusted {
		t.Fatalf("%+v", got)
	}
}

func TestCheckCloudflareAndContainers(t *testing.T) {
	fakeProxyLookups(t, []proxyCandidate{{Name: "npm", Image: "nginx-proxy-manager", Addresses: []string{"172.18.0.2"}}}, nil)
	var config utils.Config
	config.HTTPConfig.DNSZones = []utils.DNSZoneConfig{{Zone: "example.com", DNSChallengeProvider: "cloudflare"}}
	config.HTTPConfig.Cloudflare.TrustRanges = true
	got := httpCheckExtrasFor(requestFrom("192.168.1.5"), config)
	if !got.Cloudflare.Connected || !got.Cloudflare.Trusted || len(got.Cloudflare.Ranges) < 20 || len(got.Proxies) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestProxyImageName(t *testing.T) {
	for image, want := range map[string]string{
		"jc21/nginx-proxy-manager:latest":      "nginx-proxy-manager",
		"cloudflare/cloudflared@sha256:abc":    "cloudflared",
		"ghcr.io/linuxserver/swag:2.0":          "swag",
		"traefik":                               "traefik",
		"registry.example.com:5000/caddy:2":     "caddy",
	} {
		if got := proxyImageName(image); got != want || !proxyImages[got] {
			t.Errorf("%s: %s", image, got)
		}
	}
}
