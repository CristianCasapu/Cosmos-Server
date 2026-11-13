package proxy

// Fork addition: local links answer on the local network only.
//
// Every app has a local address (a .local name, or the server's private IP
// with a port) and, when a domain is attached to it, an external one. Without
// this check a local address is "local" only because outsiders cannot resolve
// the name: once the router forwards Cosmos' port, anyone who guesses the
// name can ask for it by sending it as the Host. So a route whose address is
// a local one is refused to visitors from outside the server's own networks.

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// isLocalAddress reports whether a route's host is a local link: a .local
// name or a private IP address, with or without a port.
func isLocalAddress(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
	if host == "" {
		return false
	}
	if strings.HasSuffix(host, ".local") {
		return true
	}
	return isLocalIP(net.ParseIP(host)) // the ranges are in fork_shield_local.go, shared with the shield
}

var (
	ownNetworksMu   sync.Mutex
	ownNetworks     []*net.IPNet
	ownNetworksRead time.Time
	sharedAddresses = mustCIDR("100.64.0.0/10") // carrier NAT range, used by Tailscale
)

func mustCIDR(cidr string) *net.IPNet {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}
	return network
}

// serverNetworks lists the networks the server itself is attached to, read
// again at most once a minute.
func serverNetworks() []*net.IPNet {
	ownNetworksMu.Lock()
	defer ownNetworksMu.Unlock()
	if ownNetworks != nil && time.Since(ownNetworksRead) < time.Minute {
		return ownNetworks
	}
	networks := []*net.IPNet{}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			if network, ok := addr.(*net.IPNet); ok {
				networks = append(networks, network)
			}
		}
	}
	ownNetworks, ownNetworksRead = networks, time.Now()
	return networks
}

// isLocalVisitor reports whether an address belongs to the local network:
// a private, loopback or link-local address, or one inside a network the
// server is attached to (IPv6 networks at home use public addresses).
func isLocalVisitor(address string, networks []*net.IPNet) bool {
	ip := net.ParseIP(strings.Trim(address, "[]"))
	if ip == nil {
		return false
	}
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || sharedAddresses.Contains(ip) {
		return true
	}
	for _, network := range networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// localNetworkOnly refuses visitors from outside the local network.
func localNetworkOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// the address the other restrictions use (set early in the chain, also
		// for requests handed over by the port listeners), else the peer's
		visitor, _ := req.Context().Value("ClientID").(string)
		if visitor == "" {
			visitor = utils.GetClientIP(req)
		}
		if !isLocalVisitor(visitor, serverNetworks()) {
			utils.RecordSecurity(req, utils.SecAccessLocalDenied, "") // security record
			utils.Warn("Local address " + req.Host + " was requested from outside the local network (" + visitor + "): refused")
			http.Error(w, "This address is available on the local network only.", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, req)
	})
}
