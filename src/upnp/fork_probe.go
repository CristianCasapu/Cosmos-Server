package upnp

// Fork addition (plan 47, C0): what `cosmos fork-detect` asks the router.
// Read only: discovery, the device description and GetExternalIPAddress.
// Nothing is mapped, nothing is written, no configuration is read.

import "net"

// RouterProbe is what the default gateway said about itself over UPnP.
type RouterProbe struct {
	// Answers is true when the gateway answered discovery with a usable
	// port-forwarding service.
	Answers bool `json:"answers"`
	// Name and Model come from the device description, cleaned (no control
	// characters, at most 64 characters each).
	Name  string `json:"name"`
	Model string `json:"model"`
	// ExternalIP is the router's own outside address, empty when it did not
	// say.
	ExternalIP string `json:"externalIP"`
	// NotPublic is true when ExternalIP is not a public address (private,
	// 100.64.0.0/10 shared address space, loopback, link-local): the router
	// sits behind the provider's NAT or another router. The same rule as
	// the UPnP card's note (privateAddressNote).
	NotPublic bool `json:"notPublic"`
	// Error says, in words, why the router did not answer or did not give
	// its address. Empty on success.
	Error string `json:"error,omitempty"`
}

// probeDiscover finds the gateway's service; replaced by the tests.
var probeDiscover = func() (*gateway, error) { return discoverGateway() }

// Probe asks the default gateway what it is and which outside address it
// has. It takes at most a few seconds when the router does not answer
// (discovery waits 3 s); callers that need a hard bound run it with a timer.
func Probe() RouterProbe {
	g, err := probeDiscover()
	if err != nil {
		return RouterProbe{Error: cleanText(err.Error(), 200)}
	}
	p := RouterProbe{
		Answers: true,
		Name:    cleanText(g.name, maxRouterText),
		Model:   cleanText(g.model, maxRouterText),
	}
	ip, err := g.externalIP()
	if err != nil {
		p.Error = cleanText(err.Error(), 200)
		return p
	}
	p.ExternalIP = ip
	p.NotPublic = privateAddressNote(ip) != ""
	return p
}

// IsPublicIPv4 reports whether address is an IPv4 address that can be
// reached from the internet, by the rule used for the router's address.
func IsPublicIPv4(address string) bool {
	ip := net.ParseIP(address)
	return ip != nil && ip.To4() != nil && privateAddressNote(address) == "" && !ip.IsUnspecified() && !ip.IsMulticast()
}
