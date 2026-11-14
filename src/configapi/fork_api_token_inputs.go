package configapi

// Fork addition: the lifetime and the address list of an API token are
// checked when it is created or changed.
//
// Upstream took any number of days (a very large one overflows and can give
// a token that expired long ago or never does) and any text as an allowed
// address (a typing error left a token no address can use). Now the
// lifetime is 1 to 3650 days, or none (0, the token does not expire), and
// every entry of the address list must be an IP address or a range in CIDR
// notation, 64 entries at most. Entries are kept trimmed; empty ones are
// dropped.

import (
	"fmt"
	"net"
	"strings"
)

const (
	maxTokenExpiryDays    = 3650
	maxTokenWhitelistSize = 64
)

// tokenExpiryProblem returns why a lifetime in days is refused, "" when it is fine
func tokenExpiryProblem(days int) string {
	if days < 0 || days > maxTokenExpiryDays {
		return fmt.Sprintf("The lifetime of a token is 1 to %d days, or 0 for a token that does not expire.", maxTokenExpiryDays)
	}
	return ""
}

// cleanTokenWhitelist returns the address list as it is kept, or why it is refused
func cleanTokenWhitelist(entries []string) ([]string, string) {
	if entries == nil {
		return nil, ""
	}
	kept := []string{}
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if len(entry) > 64 {
			return nil, "An allowed address is too long."
		}
		if net.ParseIP(entry) == nil {
			if _, _, err := net.ParseCIDR(entry); err != nil {
				return nil, fmt.Sprintf("%q is not an IP address or a range such as 192.168.1.0/24.", entry)
			}
		}
		kept = append(kept, entry)
	}
	if len(kept) > maxTokenWhitelistSize {
		return nil, fmt.Sprintf("A token takes at most %d allowed addresses.", maxTokenWhitelistSize)
	}
	return kept, ""
}
