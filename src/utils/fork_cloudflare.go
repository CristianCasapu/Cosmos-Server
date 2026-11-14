package utils

// Fork addition: what the rest of the server needs to know about Cloudflare's
// proxy ("the shield"). The integration itself (API client, guided set-up,
// record flags) is the package src/cloudflare; it imports utils, so the parts
// the request path depends on live here:
//
//   - which hostnames the admin serves through the shield (the choice is per
//     application, kept in HTTPConfig.Cloudflare.Shielded);
//   - Cloudflare's published address ranges: a built-in list, replaced by a
//     list fetched from Cloudflare itself once it passed ValidCloudflareRanges.
//     They never come from a client;
//   - whether a peer in those ranges is believed about the visitor's address
//     (TrustedCloudflarePeer, used by IsTrustedProxy and ForwardedClient);
//   - the ports Cloudflare proxies HTTPS on;
//   - masking of the API token wherever a zone is sent to a client.

import (
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ForkCloudflareConfig is HTTPConfig.Cloudflare. It is changed only through
// the routes of src/cloudflare: a full settings save puts the stored value
// back (configapi, keepCloudflareState).
type ForkCloudflareConfig struct {
	// Shielded lists the hostnames served through Cloudflare's proxy. Every
	// other hostname is served directly.
	Shielded []string `json:"Shielded,omitempty"`
	// TrustRanges: a peer inside Cloudflare's ranges is believed about the
	// visitor's address, for shielded hostnames. Set with the first shielded
	// hostname.
	TrustRanges bool `json:"TrustRanges,omitempty"`
	// TakeOver lists names that had a record Cosmos did not create and that
	// the admin allowed Cosmos to replace.
	TakeOver []string `json:"TakeOver,omitempty"`
	// RangesV4 and RangesV6 are the last list fetched from Cloudflare; empty
	// means the built-in list.
	RangesV4      []string  `json:"RangesV4,omitempty"`
	RangesV6      []string  `json:"RangesV6,omitempty"`
	RangesUpdated time.Time `json:"RangesUpdated,omitzero"`
	// OriginPulls: whether a connection from Cloudflare's addresses must
	// prove with a client certificate that Cloudflare's proxy made it, see
	// fork_origin_pulls.go. Off until the admin switches it on.
	OriginPulls ForkOriginPullsConfig `json:"OriginPulls,omitzero"`
}

// CloudflareMaxNames bounds the lists of hostnames kept in the configuration.
const CloudflareMaxNames = 500

// CloudflareBuiltinDate is when Cloudflare last changed the list below
// (https://www.cloudflare.com/ips/).
const CloudflareBuiltinDate = "2023-09-28"

var cloudflareBuiltinV4 = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
	"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
	"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
	"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
}

var cloudflareBuiltinV6 = []string{
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32",
	"2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
}

// CloudflareHTTPSPorts are the ports Cloudflare's proxy accepts HTTPS on
// (developers.cloudflare.com/fundamentals/reference/network-ports, read
// 2026-11-13). A shielded hostname must be published on one of them.
var CloudflareHTTPSPorts = []int{443, 2053, 2083, 2087, 2096, 8443}

// IsCloudflareHTTPSPort reports whether Cloudflare proxies HTTPS on the port.
func IsCloudflareHTTPSPort(port int) bool {
	for _, known := range CloudflareHTTPSPorts {
		if known == port {
			return true
		}
	}
	return false
}

// PublicHTTPSPort is the port visitors from the internet use for HTTPS: the
// one opened on the router when Cosmos asks for another than its own, else
// Cosmos' HTTPS port. 0 when it is not a port.
func PublicHTTPSPort(http HTTPConfig) int {
	value := strings.TrimSpace(http.HTTPSPort)
	if http.UPnP.Enabled && strings.TrimSpace(http.UPnP.ExternalHTTPSPort) != "" {
		value = strings.TrimSpace(http.UPnP.ExternalHTTPSPort)
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0
	}
	return port
}

// ValidCloudflareRanges checks a list of ranges that claims to be
// Cloudflare's, and returns it cleaned (canonical form, sorted, no
// duplicates). ok is false when anything in it is not what such a list can
// contain: text that is not a network, a network of the wrong family, a
// private, loopback, link-local, multicast or unspecified one, one wider than
// /8 (IPv4) or /16 (IPv6), or a list that is empty or longer than 200.
func ValidCloudflareRanges(ranges []string, v6 bool) ([]string, bool) {
	if len(ranges) == 0 || len(ranges) > 200 {
		return nil, false
	}
	seen := map[string]bool{}
	out := []string{}
	for _, text := range ranges {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(text))
		if err != nil || prefix != prefix.Masked() {
			return nil, false
		}
		addr := prefix.Addr()
		if addr.Is4In6() || addr.Is4() == v6 {
			return nil, false
		}
		if (!v6 && prefix.Bits() < 8) || (v6 && prefix.Bits() < 16) {
			return nil, false
		}
		if addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsMulticast() ||
			addr.IsUnspecified() || addr.IsLinkLocalMulticast() || !addr.IsGlobalUnicast() {
			return nil, false
		}
		// carrier NAT and documentation space are nobody's public network
		if !v6 && (netip.MustParsePrefix("100.64.0.0/10").Overlaps(prefix) || netip.MustParsePrefix("192.0.2.0/24").Overlaps(prefix)) {
			return nil, false
		}
		if !seen[prefix.String()] {
			seen[prefix.String()] = true
			out = append(out, prefix.String())
		}
	}
	sort.Strings(out)
	return out, true
}

type cloudflareRangeSet struct {
	key      string
	prefixes []netip.Prefix
}

var (
	cloudflareRangesMu  sync.Mutex
	cloudflareRangesNow cloudflareRangeSet
)

// CloudflareRangeLists returns the ranges in force (IPv4, IPv6) and whether
// they are the built-in ones. A stored list that does not pass
// ValidCloudflareRanges is not used.
func CloudflareRangeLists(cf ForkCloudflareConfig) (v4 []string, v6 []string, builtin bool) {
	v4, ok4 := ValidCloudflareRanges(cf.RangesV4, false)
	v6, ok6 := ValidCloudflareRanges(cf.RangesV6, true)
	if !ok4 || !ok6 {
		return append([]string{}, cloudflareBuiltinV4...), append([]string{}, cloudflareBuiltinV6...), true
	}
	return v4, v6, false
}

func cloudflarePrefixes(cf ForkCloudflareConfig) []netip.Prefix {
	key := strings.Join(cf.RangesV4, ",") + "|" + strings.Join(cf.RangesV6, ",")

	cloudflareRangesMu.Lock()
	defer cloudflareRangesMu.Unlock()
	if cloudflareRangesNow.prefixes != nil && cloudflareRangesNow.key == key {
		return cloudflareRangesNow.prefixes
	}
	v4, v6, _ := CloudflareRangeLists(cf)
	prefixes := make([]netip.Prefix, 0, len(v4)+len(v6))
	for _, text := range append(v4, v6...) {
		if prefix, err := netip.ParsePrefix(text); err == nil {
			prefixes = append(prefixes, prefix)
		}
	}
	cloudflareRangesNow = cloudflareRangeSet{key: key, prefixes: prefixes}
	return prefixes
}

// IsCloudflareAddress reports whether an address (with or without a port or
// brackets) lies in the ranges of cf.
func IsCloudflareAddress(cf ForkCloudflareConfig, address string) bool {
	address = strings.TrimSpace(address)
	if host, _, err := net.SplitHostPort(address); err == nil {
		address = host
	}
	addr, err := netip.ParseAddr(strings.Trim(address, "[]"))
	if err != nil {
		return false
	}
	addr = addr.Unmap().WithZone("")
	for _, prefix := range cloudflarePrefixes(cf) {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// IsCloudflareIP is IsCloudflareAddress with the ranges of the running
// configuration.
func IsCloudflareIP(address string) bool {
	return IsCloudflareAddress(GetMainConfig().HTTPConfig.Cloudflare, address)
}

// TrustedCloudflarePeer reports whether an address is one of Cloudflare's and
// Cloudflare is believed about visitors at all. Hook of IsTrustedProxy.
func TrustedCloudflarePeer(address string) bool {
	cf := GetMainConfig().HTTPConfig.Cloudflare
	return cf.TrustRanges && IsCloudflareAddress(cf, address)
}

// CloudflareHostname brings a hostname to the form the lists hold: lower
// case, no port, no trailing dot.
func CloudflareHostname(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(host, ".")
}

// ShieldedIn reports whether a hostname is in a list of shielded hostnames.
func ShieldedIn(shielded []string, host string) bool {
	if len(shielded) == 0 {
		return false
	}
	host = CloudflareHostname(host)
	if host == "" {
		return false
	}
	for _, name := range shielded {
		if name == host {
			return true
		}
	}
	return false
}

// IsShieldedHost reports whether the admin serves a hostname through
// Cloudflare's proxy.
func IsShieldedHost(host string) bool {
	return ShieldedIn(GetMainConfig().HTTPConfig.Cloudflare.Shielded, host)
}

// The keys under which a zone holds a Cloudflare credential (lego's names).
var cloudflareSecretKeys = map[string]bool{
	"CF_DNS_API_TOKEN": true, "CLOUDFLARE_DNS_API_TOKEN": true,
	"CF_ZONE_API_TOKEN": true, "CLOUDFLARE_ZONE_API_TOKEN": true,
	"CF_API_KEY": true, "CLOUDFLARE_API_KEY": true,
}

// MaskedSecret is what a client gets instead of a stored secret, and what it
// sends back to keep it.
const MaskedSecret = "***"

// MaskCloudflareSecrets replaces the Cloudflare credentials of the zones by
// the mask. The slice and its maps are copied: the caller's are not changed.
func MaskCloudflareSecrets(zones []DNSZoneConfig) []DNSZoneConfig {
	out := make([]DNSZoneConfig, len(zones))
	for i, zone := range zones {
		out[i] = zone
		out[i].DNSChallengeConfig = MaskCloudflareSecretMap(zone.DNSChallengeConfig)
	}
	return out
}

// MaskCloudflareSecretMap is MaskCloudflareSecrets for one zone's variables.
func MaskCloudflareSecretMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	masked := make(map[string]string, len(values))
	for key, value := range values {
		if cloudflareSecretKeys[key] && value != "" {
			value = MaskedSecret
		}
		masked[key] = value
	}
	return masked
}

// RestoreCloudflareSecrets puts the stored credential back wherever a zone
// arrives with the mask in its place. A mask with nothing stored behind it
// becomes empty: the mask itself is never saved.
func RestoreCloudflareSecrets(incoming []DNSZoneConfig, stored []DNSZoneConfig) {
	byName := map[string]map[string]string{}
	for _, zone := range stored {
		byName[zone.Zone] = zone.DNSChallengeConfig
	}
	for i := range incoming {
		RestoreCloudflareSecretMap(incoming[i].DNSChallengeConfig, byName[incoming[i].Zone])
	}
}

// RestoreCloudflareSecretMap is RestoreCloudflareSecrets for one zone.
func RestoreCloudflareSecretMap(incoming map[string]string, stored map[string]string) {
	for key, value := range incoming {
		if cloudflareSecretKeys[key] && value == MaskedSecret {
			incoming[key] = stored[key]
		}
	}
}
