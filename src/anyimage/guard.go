package anyimage

// Which addresses the server may connect to when it reads a registry.
//
// The check is made at dial time, on each address the name resolves to, and
// the connection goes to the address that was checked: a name that resolves
// to a public address when checked and to an inside one when connected
// (DNS rebinding) is not possible. Redirects and token realms go through the
// same dialer, so a registry cannot send the server inside the house either.

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"time"
)

var (
	// always refused: nothing a registry is on
	alwaysRefused = prefixes(
		"0.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "192.0.0.0/24", "224.0.0.0/4", "240.0.0.0/4",
		"::/128", "::1/128", "fe80::/10", "ff00::/8", "2001::/32", "2002::/16", "64:ff9b:1::/48",
	)
	// the local network: refused unless the server is logged in to that registry
	insideRanges = prefixes(
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "198.18.0.0/15", "fc00::/7",
	)
	nat64 = netip.MustParsePrefix("64:ff9b::/96")
)

func prefixes(list ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(list))
	for _, text := range list {
		out = append(out, netip.MustParsePrefix(text))
	}
	return out
}

// errRefusedAddress is returned when a registry, a redirect or a token
// service is on an address the server must not connect to.
var errRefusedAddress = errors.New("refused address")

func plain(addr netip.Addr) netip.Addr {
	addr = addr.Unmap().WithZone("")
	if addr.Is6() && nat64.Contains(addr) {
		b := addr.As16()
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
	}
	return addr
}

// refusedAddr tells whether the server must not connect to addr. insideAllowed
// is true only for a registry the server is logged in to.
func refusedAddr(addr netip.Addr, insideAllowed bool, own []netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	addr = plain(addr)
	if addr.IsLoopback() || addr.IsUnspecified() || addr.IsMulticast() || addr.IsLinkLocalUnicast() {
		return true
	}
	for _, p := range alwaysRefused {
		if p.Contains(addr) {
			return true
		}
	}
	for _, mine := range own {
		if plain(mine) == addr {
			return true
		}
	}
	if insideAllowed {
		return false
	}
	for _, p := range insideRanges {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func ownAddrs() []netip.Addr {
	list, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	out := []netip.Addr{}
	for _, a := range list {
		if prefix, err := netip.ParsePrefix(a.String()); err == nil {
			out = append(out, prefix.Addr())
		}
	}
	return out
}

// Replaced by the tests.
var (
	lookupIP  = net.DefaultResolver.LookupNetIP
	checkAddr = refusedAddr
	ownAddrsF = ownAddrs
)

// guardedDial connects to host:port only through an address the rules allow.
func guardedDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	// Docker Hub's hosts are always on the internet, whatever the server is logged in to
	insideAllowed := !isHubHost(host) && (loggedIn(host) || loggedIn(net.JoinHostPort(host, port)))
	var addrs []netip.Addr
	if literal, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		addrs = []netip.Addr{literal}
	} else {
		lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		addrs, err = lookupIP(lookupCtx, "ip", host)
		cancel()
		if err != nil {
			return nil, err
		}
	}
	own := ownAddrsF()
	dialer := &net.Dialer{Timeout: 6 * time.Second}
	var last error = errRefusedAddress
	for _, addr := range addrs {
		if checkAddr(addr, insideAllowed, own) {
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(plain(addr).String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}

// isHubHost tells whether a host is one of Docker Hub's.
func isHubHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "docker.io" || host == "docker.com" || strings.HasSuffix(host, ".docker.io") || strings.HasSuffix(host, ".docker.com")
}
