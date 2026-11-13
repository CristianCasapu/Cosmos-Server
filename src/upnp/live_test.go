package upnp

import (
	"os"
	"testing"
)

// TestLiveRouter talks to the real router of the machine running the test. It
// only reads (discovery, external address, one mapping lookup) and is skipped
// unless UPNP_LIVE is set.
func TestLiveRouter(t *testing.T) {
	if os.Getenv("UPNP_LIVE") == "" {
		t.Skip("set UPNP_LIVE=1 to query the real router (read-only)")
	}

	g, err := discover()
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	t.Logf("router %q at %s, service %s, this machine is %s", g.name, g.ip, g.serviceType, g.localIP)

	ip, err := g.externalIP()
	if err != nil {
		t.Fatalf("externalIP: %v", err)
	}
	t.Logf("external address is public: %v", privateAddressNote(ip) == "")

	existing, err := g.lookup("TCP", 2053)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	t.Logf("TCP 2053 currently forwarded: %v", existing != nil)

	entries, listed, truncated := g.list()
	t.Logf("the router lets its table be read: %v (%d entries, truncated %v)", listed, len(entries), truncated)
	for _, e := range entries {
		t.Logf("  %s %d -> port %d of %s, %q, lease %d s", e.Protocol, e.ExternalPort, e.InternalPort, map[bool]string{true: "this machine", false: "another device"}[e.InternalClient == g.localIP.String()], e.Description, e.Lease)
	}
}

// TestLiveRouterMapping creates and removes a forward on the real router. It
// uses a port nothing listens on and a one-minute lease, so nothing is exposed
// and the router cleans up by itself if the test dies. Skipped unless
// UPNP_LIVE_WRITE is set.
func TestLiveRouterMapping(t *testing.T) {
	if os.Getenv("UPNP_LIVE_WRITE") == "" {
		t.Skip("set UPNP_LIVE_WRITE=1 to create and remove a test forward on the real router")
	}
	const port = 39999

	g, err := discover()
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if existing, err := g.lookup("TCP", port); err != nil || existing != nil {
		t.Fatalf("port %d is not free on the router (%+v, err %v)", port, existing, err)
	}

	const description = "Cosmos UPnP self-test"
	if err := g.claim("TCP", port, port, description, 60, false); err != nil {
		t.Fatalf("claim: %v", err)
	}
	defer g.release("TCP", port, port, true)

	existing, err := g.lookup("TCP", port)
	if err != nil || existing == nil || existing.InternalClient != g.localIP.String() || existing.InternalPort != port {
		t.Fatalf("after claim the router reports %+v, err %v; want this machine (%s), port %d", existing, err, g.localIP, port)
	}
	t.Logf("the router keeps the description as written: %v (%q); lease reported: %d s", existing.Description == description, existing.Description, existing.Lease)
	if err := g.claim("TCP", port, port, description, 60, true); err != nil {
		t.Errorf("renewing our own mapping: %v", err)
	}
	entries, listed, _ := g.list()
	found := false
	for _, e := range entries {
		found = found || (e.Protocol == "TCP" && e.ExternalPort == port)
	}
	t.Logf("the table can be listed: %v; our mapping is in it: %v", listed, found)

	if deleted, err := g.release("TCP", port, port, true); err != nil || !deleted {
		t.Fatalf("release: %v, %v", deleted, err)
	}
	if existing, err := g.lookup("TCP", port); err != nil || existing != nil {
		t.Errorf("after release the router still reports %+v, err %v", existing, err)
	}
}
