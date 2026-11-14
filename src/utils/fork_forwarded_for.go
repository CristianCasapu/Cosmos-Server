package utils

// Fork addition: which entry of X-Forwarded-For is the client.
//
// The header is a list. Each proxy on the way adds the address it received
// the request from at the END, so the entries on the right were written by
// the proxies nearest to this server, and the entries on the left are
// whatever arrived from further away: a visitor can send any text there.
// Upstream took the first (leftmost) entry whenever the peer was a trusted
// proxy, which is the visitor's own claim as soon as that proxy appends.
//
// The client is the last entry that is not itself a trusted proxy, reading
// from the right. If every entry is a trusted proxy, the first one is the
// client. An entry that is not an address ends the reading: what stands to
// its left cannot be relied on.
//
// Cloudflare (fork_cloudflare.go) is an edge, not a link of a chain the admin
// runs, and it is shared by all its customers. So:
//
//   - what a peer inside Cloudflare's ranges reports is believed only for a
//     hostname the admin serves through Cloudflare. For any other hostname a
//     request from there is a request from that address (a Worker fetching a
//     name that is served directly decides its headers itself);
//   - for such a hostname the visitor is the one address in CF-Connecting-IP,
//     which Cloudflare's proxy sets itself on everything it passes on (for a
//     Worker's request too) and which is what Cloudflare tells origins to
//     read. The list is not read at all then: its entries left of
//     Cloudflare's are the visitor's own, and whether a Worker can shape the
//     last one is not documented;
//   - the address must be a public one. Cloudflare never reports a visitor
//     from a private network, and a claim to be one must not open what is
//     reserved to the local network;
//   - while the admin enforces Authenticated Origin Pulls (fork_origin_pulls.go)
//     the header is believed only on a connection that presented the
//     expected client certificate: Cloudflare's addresses are shared by all
//     its customers, the certificate is what shows whose request it is.
//
// The header is only read at all when the peer of the connection is a
// trusted proxy (the callers check that, as before).

import (
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// An address somebody else reported is not the address a connection came
// from: the firewall cannot cut such a visitor off (the packets are the
// proxy's), and whoever can reach this server through the proxy's addresses
// may have chosen it. What acts on addresses at the firewall (the Fail2ban
// file, package security) leaves these out. Requests carry that with them
// (SecurityEvent.Forwarded); for what is decided without a request at hand
// (SmartShield counts an address whatever way its requests came, and strikes
// it) the addresses that were reported are remembered here for a day.
const (
	forwardedClientsFor = 24 * time.Hour
	forwardedClientsMax = 16384
)

var forwardedClients = struct {
	sync.Mutex
	seen map[string]int64 // address -> unix time last reported
}{seen: map[string]int64{}}

func noteForwardedClient(ip string) {
	now := time.Now().Unix()
	f := &forwardedClients
	f.Lock()
	defer f.Unlock()
	if _, known := f.seen[ip]; !known && len(f.seen) >= forwardedClientsMax {
		// make room: what is older than a day, else the older half
		limit := now - int64(forwardedClientsFor/time.Second)
		for address, at := range f.seen {
			if at < limit {
				delete(f.seen, address)
			}
		}
		if len(f.seen) >= forwardedClientsMax {
			sum := int64(0)
			for _, at := range f.seen {
				sum += at
			}
			mean := sum / int64(len(f.seen))
			for address, at := range f.seen {
				if at <= mean && len(f.seen) > forwardedClientsMax/2 {
					delete(f.seen, address)
				}
			}
		}
	}
	f.seen[ip] = now
}

// WasForwardedClient reports whether an address was reported by a proxy
// (Cloudflare, or a trusted proxy) as the visitor's within the last day.
func WasForwardedClient(ip string) bool {
	f := &forwardedClients
	f.Lock()
	defer f.Unlock()
	at, known := f.seen[ip]
	return known && time.Now().Unix()-at < int64(forwardedClientsFor/time.Second)
}

// IsForwardedClient reports whether the address a request counts as coming
// from (client, as GetClientIP gave it) is somebody's report rather than the
// peer of its connection.
func IsForwardedClient(req *http.Request, client string) bool {
	if req == nil || client == "" {
		return false
	}
	peer, _ := SplitIP(req.RemoteAddr)
	return client != peer
}

// CloudflareNeverBan reports whether an address is one of Cloudflare's while
// a hostname is served through it: every visitor of such a hostname arrives
// from these addresses, so nothing may ever act against them at the firewall.
func CloudflareNeverBan(ip string) bool {
	cf := GetMainConfig().HTTPConfig.Cloudflare
	return len(cf.Shielded) > 0 && IsCloudflareAddress(cf, ip)
}

// ForwardedClient returns the client address a trusted peer reports, "" when
// it reports none that can be used (the caller then takes the peer itself).
// trusted says whether an address is a proxy whose entries can be believed.
func ForwardedClient(req *http.Request, trusted func(ip string) bool) string {
	client := forwardedClient(req, trusted)
	if client != "" && IsForwardedClient(req, client) {
		noteForwardedClient(client)
	}
	return client
}

func forwardedClient(req *http.Request, trusted func(ip string) bool) string {
	cf := GetMainConfig().HTTPConfig.Cloudflare
	edge := func(ip string) bool { return cf.TrustRanges && IsCloudflareAddress(cf, ip) }
	shielded := ShieldedIn(cf.Shielded, req.Host)

	peer, _ := SplitIP(req.RemoteAddr)
	if edge(peer) {
		if !shielded {
			return ""
		}
		if OriginPullMode(cf) == OriginPullEnforce && !OriginPullVerified(cf, req.TLS) {
			// not shown to be Cloudflare's proxy: the guard refuses it, and
			// nothing it says about the visitor counts
			return ""
		}
		return publicVisitor(req.Header.Values("CF-Connecting-IP"))
	}

	entries := []string{}
	for _, line := range req.Header.Values("X-Forwarded-For") {
		entries = append(entries, strings.Split(line, ",")...)
	}

	client := ""
	for i := len(entries) - 1; i >= 0; i-- {
		ip, _ := SplitIP(strings.TrimSpace(entries[i]))
		if ip == "" {
			break
		}
		client = ip
		if !trusted(ip) {
			break
		}
		if edge(ip) {
			// a proxy of the admin's own stands behind Cloudflare: the entry
			// before Cloudflare's address is the one Cloudflare added
			if shielded && i > 0 {
				if visitor := publicVisitor([]string{entries[i-1]}); visitor != "" {
					client = visitor
				}
			}
			break
		}
	}
	return client
}

// publicVisitor returns the one public address a header holds, "" for
// anything else: no value, several, text, or an address that is not a public
// one.
func publicVisitor(values []string) string {
	if len(values) != 1 {
		return ""
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(values[0]))
	if err != nil {
		return ""
	}
	addr = addr.Unmap().WithZone("")
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() ||
		netip.MustParsePrefix("100.64.0.0/10").Contains(addr) {
		return ""
	}
	return addr.String()
}
