package maildetect

import (
	"errors"
	"net"
	"net/netip"
	"syscall"
)

// allowedPorts are the only ports detection connects to.
var allowedPorts = map[int]bool{25: true, 465: true, 587: true, 2525: true}

var refusedPrefixes = mustPrefixes(
	"0.0.0.0/8",      // "this network"
	"127.0.0.0/8",    // loopback
	"169.254.0.0/16", // link-local, cloud metadata
	"192.0.0.0/24",   // protocol assignments
	"224.0.0.0/4",    // multicast
	"240.0.0.0/4",    // reserved, broadcast
	"::/128",         // unspecified
	"::1/128",        // loopback
	"fe80::/10",      // link-local
	"ff00::/8",       // multicast
	"2001::/32",      // Teredo: carries an IPv4 address
	"2002::/16",      // 6to4: carries an IPv4 address
	"64:ff9b:1::/48", // local NAT64
)

var nat64 = netip.MustParsePrefix("64:ff9b::/96")

func mustPrefixes(list ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(list))
	for _, text := range list {
		out = append(out, netip.MustParsePrefix(text))
	}
	return out
}

// plainAddr unwraps the forms that carry an IPv4 address inside an IPv6 one.
func plainAddr(addr netip.Addr) netip.Addr {
	addr = addr.Unmap().WithZone("")
	if addr.Is6() && nat64.Contains(addr) {
		b := addr.As16()
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
	}
	return addr
}

// refusedAddr reports whether detection must not connect to addr: loopback,
// link-local, multicast, unspecified and reserved addresses, and the
// server's own. Private ranges are allowed on purpose: a mail server on the
// local network is a legitimate target for an administrator (see the plan
// and FORK.md).
func refusedAddr(addr netip.Addr, own []netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	addr = plainAddr(addr)
	if addr.IsLoopback() || addr.IsUnspecified() || addr.IsMulticast() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsInterfaceLocalMulticast() {
		return true
	}
	for _, prefix := range refusedPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	for _, mine := range own {
		if plainAddr(mine) == addr {
			return true
		}
	}
	return false
}

// interfaceAddrs returns the addresses of the server's own interfaces.
func interfaceAddrs() []netip.Addr {
	list, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	out := make([]netip.Addr, 0, len(list))
	for _, entry := range list {
		if ipnet, ok := entry.(*net.IPNet); ok {
			if addr, ok := netip.AddrFromSlice(ipnet.IP); ok {
				out = append(out, addr.Unmap())
			}
		}
	}
	return out
}

var errRefusedTarget = errors.New("maildetect: refused target")

// socketGuard is the dialer's Control function: the last look at the
// address a socket is about to connect to. The detector only dials
// addresses it validated, so this never fires unless that is broken.
func socketGuard(own func() []netip.Addr) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		target, err := netip.ParseAddrPort(address)
		if err != nil || !allowedPorts[int(target.Port())] || refusedAddr(target.Addr(), own()) {
			return errRefusedTarget
		}
		return nil
	}
}
