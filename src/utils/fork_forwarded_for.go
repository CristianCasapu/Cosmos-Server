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
// The header is only read at all when the peer of the connection is a
// trusted proxy (the callers check that, as before).

import (
	"net/http"
	"strings"
)

// ForwardedClient returns the client address according to X-Forwarded-For, ""
// when the header names none. trusted says whether an address is a proxy
// whose entries can be believed.
func ForwardedClient(req *http.Request, trusted func(ip string) bool) string {
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
	}
	return client
}
