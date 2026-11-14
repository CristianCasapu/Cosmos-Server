package proxy

// Fork addition: what the TLS listener needs to know about a connection
// before it picks a certificate (src/fork_tls_names.go): whether its peer is
// on the local network, by the fork's one definition of local
// (isLocalVisitor, fork_local.go), and the port of the port route that
// handed it over, if any.

import (
	"crypto/tls"
	"net"
)

// LocalNetworkPeer reports whether an address (with or without a port) is a
// peer of the local network: private, loopback, link-local, the carrier-NAT
// range, or inside a network the server is attached to.
func LocalNetworkPeer(address string) bool {
	if host, _, err := net.SplitHostPort(address); err == nil {
		address = host
	}
	return isLocalVisitor(address, serverNetworks())
}

// HandedOverPort returns the port a connection was accepted on by the
// socket proxy, false for a connection the HTTP server accepted itself.
func HandedOverPort(c net.Conn) (string, bool) {
	if tc, ok := c.(*tls.Conn); ok {
		c = tc.NetConn()
	}
	if hc, ok := c.(*handoffConn); ok {
		return hc.listenPort, true
	}
	return "", false
}
