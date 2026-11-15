package edgeblock

// What is never blocked at Cloudflare, whatever it did.
//
// Every check works on prefixes, so that an IPv6 /64 (the unit of a block)
// is exempt as soon as any exempt address or network overlaps it: a block
// of the /64 would hit that address too.

import (
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The reasons an address is exempt, as the card and the refusal of a manual
// block name them.
const (
	ExemptInvalid    = "invalid"    // not a public address
	ExemptLocal      = "local"      // private, loopback, link-local, carrier NAT
	ExemptServer     = "server"     // the server's own networks and public address
	ExemptCloudflare = "cloudflare" // Cloudflare's own ranges
	ExemptWhitelist  = "whitelist"  // SmartShield's whitelist or the admin address restriction
	ExemptFail2ban   = "fail2ban"   // the Fail2ban never-ban list
	ExemptAdmin      = "admin"      // an administrator used it lately
)

// the fork's notion of local (proxy.isLocalVisitor), as networks
var localNetworks = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("::1/128"), netip.MustParsePrefix("fc00::/7"), netip.MustParsePrefix("fe80::/10"),
}

// AdminFor is how long an address an administrator used stays exempt.
const AdminFor = 7 * 24 * time.Hour

// replaced by the tests
var (
	interfaceNetworks = readInterfaceNetworks
	publicAddress     = func() string { return "" } // the server's public IPv4, set by the wiring
)

var ifaceCache struct {
	sync.Mutex
	networks []netip.Prefix
	at       time.Time
}

func readInterfaceNetworks() []netip.Prefix {
	ifaceCache.Lock()
	defer ifaceCache.Unlock()
	if ifaceCache.networks != nil && time.Since(ifaceCache.at) < time.Minute {
		return ifaceCache.networks
	}
	networks := []netip.Prefix{}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				if prefix, ok := prefixOfIPNet(ipnet); ok {
					networks = append(networks, prefix)
				}
			}
		}
	}
	ifaceCache.networks, ifaceCache.at = networks, time.Now()
	return networks
}

func prefixOfIPNet(ipnet *net.IPNet) (netip.Prefix, bool) {
	addr, ok := netip.AddrFromSlice(ipnet.IP)
	if !ok {
		return netip.Prefix{}, false
	}
	ones, _ := ipnet.Mask.Size()
	addr = addr.Unmap()
	if addr.Is4() && ones > 32 {
		ones -= 96
	}
	prefix, err := addr.Prefix(ones)
	return prefix, err == nil
}

// KeyOf is the unit an address is counted and blocked by: the address
// itself for IPv4, its /64 for IPv6. ok is false for what is not an address.
func KeyOf(ip string) (string, bool) {
	addr, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(ip), "[]"))
	if err != nil || addr.Zone() != "" {
		return "", false
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String(), true
	}
	prefix, _ := addr.Prefix(64)
	return prefix.String(), true
}

// prefixOfKey turns a key (an address or a /64) into a prefix.
func prefixOfKey(key string) (netip.Prefix, bool) {
	if prefix, err := netip.ParsePrefix(key); err == nil {
		return prefix.Masked(), true
	}
	addr, err := netip.ParseAddr(key)
	if err != nil {
		return netip.Prefix{}, false
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), true
}

// parseNetwork reads an address or a network of a list in the settings.
func parseNetwork(text string) (netip.Prefix, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return netip.Prefix{}, false
	}
	if strings.Contains(text, "/") {
		prefix, err := netip.ParsePrefix(text)
		if err != nil {
			return netip.Prefix{}, false
		}
		if prefix.Addr().Is4In6() {
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}
		return prefix.Masked(), true
	}
	return prefixOfKey(text)
}

func overlapsAny(key netip.Prefix, networks []netip.Prefix) bool {
	for _, network := range networks {
		if network.IsValid() && network.Overlaps(key) {
			return true
		}
	}
	return false
}

// exemptReason says why a key may never be blocked at Cloudflare, "" when
// it may. admins are the keys administrators used lately.
func exemptReason(key string, admins map[string]time.Time, now time.Time) string {
	prefix, ok := prefixOfKey(key)
	if !ok {
		return ExemptInvalid
	}
	addr := prefix.Addr()
	if !addr.IsGlobalUnicast() || addr.IsMulticast() || addr.IsUnspecified() {
		return ExemptInvalid
	}
	if addr.Is4() && prefix.Bits() != 32 || addr.Is6() && prefix.Bits() != 64 && prefix.Bits() != 128 {
		return ExemptInvalid
	}
	if overlapsAny(prefix, localNetworks) {
		return ExemptLocal
	}
	if overlapsAny(prefix, interfaceNetworks()) {
		return ExemptServer
	}
	if public := publicAddress(); public != "" {
		if own, ok := parseNetwork(public); ok && own.Overlaps(prefix) {
			return ExemptServer
		}
	}
	// Cloudflare's ranges are far wider than a /64: the network's first
	// address decides
	if utils.IsCloudflareIP(addr.String()) {
		return ExemptCloudflare
	}

	config := utils.GetMainConfig()
	listed := []netip.Prefix{}
	for _, entry := range config.ShieldWhitelist {
		if network, ok := parseNetwork(entry.IP); ok {
			listed = append(listed, network)
		}
	}
	for _, entry := range config.AdminWhitelistIPs {
		if network, ok := parseNetwork(entry); ok {
			listed = append(listed, network)
		}
	}
	if overlapsAny(prefix, listed) {
		return ExemptWhitelist
	}
	never := []netip.Prefix{}
	for _, entry := range config.Fail2ban.IgnoreIP {
		if network, ok := parseNetwork(entry); ok {
			never = append(never, network)
		}
	}
	if overlapsAny(prefix, never) {
		return ExemptFail2ban
	}

	for admin, seen := range admins {
		if now.Sub(seen) >= AdminFor {
			continue
		}
		if network, ok := prefixOfKey(admin); ok && network.Overlaps(prefix) {
			return ExemptAdmin
		}
	}
	return ""
}
