package upnp

import (
	"errors"
	"strings"
	"testing"
)

func withProbeDiscover(t *testing.T, f func() (*gateway, error)) {
	t.Helper()
	real := probeDiscover
	probeDiscover = f
	t.Cleanup(func() { probeDiscover = real })
}

func TestProbeReadsTheRouterAndChangesNothing(t *testing.T) {
	r := newFakeRouter(t)
	withProbeDiscover(t, func() (*gateway, error) { return r.gateway(t), nil })

	p := Probe()
	if !p.Answers || p.Name != "Test Router" || p.ExternalIP == "" || p.Error != "" {
		t.Fatalf("probe = %+v", p)
	}
	for _, call := range r.calls {
		if !strings.HasPrefix(call, "GetExternalIPAddress") {
			t.Errorf("the probe asked the router for %q: it must only read", call)
		}
	}
	if len(r.table) != 0 {
		t.Errorf("the probe left mappings: %v", r.keys())
	}
}

func TestProbeSaysWhyWhenNoRouterAnswers(t *testing.T) {
	withProbeDiscover(t, func() (*gateway, error) {
		return nil, errors.New("the router (192.168.1.1) did not answer UPnP discovery;\nUPnP may be turned off on it")
	})
	p := Probe()
	if p.Answers || p.ExternalIP != "" {
		t.Fatalf("probe = %+v", p)
	}
	if p.Error == "" {
		t.Fatal("no reason given")
	}
	for _, c := range p.Error {
		if c < 0x20 {
			t.Fatalf("the reason holds a control character: %q", p.Error)
		}
	}
}

func TestProbeMarksAnAddressThatIsNotPublic(t *testing.T) {
	for address, notPublic := range map[string]bool{
		"203.0.113.7":  false,
		"100.64.12.1":  true, // the carrier's shared address space
		"192.168.0.2":  true,
		"10.1.2.3":     true,
		"198.51.100.1": false,
	} {
		g := &gateway{name: "R", model: "M\x07odel"}
		withProbeDiscover(t, func() (*gateway, error) { return g, nil })
		withExternalIP(t, g, address)
		p := Probe()
		if p.NotPublic != notPublic {
			t.Errorf("%s: notPublic = %v", address, p.NotPublic)
		}
		if p.Model != "M odel" {
			t.Errorf("model not cleaned: %q", p.Model)
		}
	}
}

// withExternalIP points g at a fake router that reports address.
func withExternalIP(t *testing.T, g *gateway, address string) {
	t.Helper()
	r := newFakeRouter(t)
	r.externalIP = address
	real := r.gateway(t)
	g.ip, g.controlURL, g.serviceType = real.ip, real.controlURL, real.serviceType
}

func TestIsPublicIPv4(t *testing.T) {
	for address, want := range map[string]bool{
		"203.0.113.7": true, "100.64.0.1": false, "192.168.1.1": false, "127.0.0.1": false,
		"169.254.1.1": false, "0.0.0.0": false, "2001:db8::1": false, "": false, "nonsense": false,
	} {
		if got := IsPublicIPv4(address); got != want {
			t.Errorf("IsPublicIPv4(%q) = %v", address, got)
		}
	}
}
