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
)

// ForwardedClient returns the client address a trusted peer reports, "" when
// it reports none that can be used (the caller then takes the peer itself).
// trusted says whether an address is a proxy whose entries can be believed.
func ForwardedClient(req *http.Request, trusted func(ip string) bool) string {
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
