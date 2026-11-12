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

	owner, err := g.mappingOwner("TCP", 2053)
	if err != nil {
		t.Fatalf("mappingOwner: %v", err)
	}
	t.Logf("TCP 2053 currently forwarded: %v", owner != "")
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
	if owner, err := g.mappingOwner("TCP", port); err != nil || owner != "" {
		t.Fatalf("port %d is not free on the router (owner %q, err %v)", port, owner, err)
	}

	if err := g.addMapping("TCP", port, port, "Cosmos UPnP self-test", 60); err != nil {
		t.Fatalf("addMapping: %v", err)
	}
	defer g.removeMapping("TCP", port)

	owner, err := g.mappingOwner("TCP", port)
	if err != nil || owner != g.localIP.String() {
		t.Fatalf("after addMapping the router reports owner %q, err %v; want %s", owner, err, g.localIP)
	}
	if err := g.addMapping("TCP", port, port, "Cosmos UPnP self-test", 60); err != nil {
		t.Errorf("renewing our own mapping: %v", err)
	}

	if err := g.removeMapping("TCP", port); err != nil {
		t.Fatalf("removeMapping: %v", err)
	}
	if owner, err := g.mappingOwner("TCP", port); err != nil || owner != "" {
		t.Errorf("after removeMapping the router still reports owner %q, err %v", owner, err)
	}
}
