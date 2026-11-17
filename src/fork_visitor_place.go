package main

// Fork addition (plan 49): where the visitor of the panel is (home, VPN,
// away), for the links of its apps. See src/proxy/fork_visitor_place.go.

import (
	"net/http"

	"github.com/azukaar/cosmos-server/src/dnsrecords"
	"github.com/azukaar/cosmos-server/src/proxy"
	"github.com/azukaar/cosmos-server/src/upnp"
)

// forkVisitorPlace is the hook of StatusRoute. The home's public addresses
// are the ones the server already keeps (no request is made for them).
func forkVisitorPlace(req *http.Request) proxy.VisitorPlace {
	return proxy.PlaceOf(req, []string{dnsrecords.CurrentAddress(), upnp.GetStatus().ExternalIP})
}
