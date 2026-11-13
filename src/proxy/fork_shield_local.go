package proxy

// Fork addition: SmartShield never bans an address of the local network for
// good.
//
// Three temporary bans within seven days become a permanent one, which lasts
// until an administrator removes it (or Cosmos restarts). For a device at
// home that is a lock-out nobody can lift without already being logged in:
// a phone with a forgotten password, a television that polls too eagerly.
// So for an address of the local network the escalation stops at temporary
// bans: the same 72 hours a repeated temporary ban lasts, again each time.
// Strikes and temporary bans are exactly as before, and so is everything for
// addresses from the internet.
//
// "Local" is what a local link is (isLocalAddress in fork_local.go): a
// private, loopback or link-local address. The address is the one the shield
// keys its bans on: the peer of the connection, or what a proxy the
// administrator listed as trusted reports (GetClientID). A header sent by
// anyone else is never looked at, so a visitor from the internet cannot
// claim to be local. A ban keyed on a logged-in user ("user:<nickname>") is
// not an address and is left alone.

import (
	"net"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// isLocalIP reports whether an address is one of the local network: private,
// loopback or link-local. isLocalAddress uses it too, so the two agree.
func isLocalIP(ip net.IP) bool {
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast())
}

// isLocalClient reports whether a shield identity is an address of the local
// network. Only a bare IP address qualifies (an IPv6 zone is ignored).
func isLocalClient(clientID string) bool {
	if i := strings.IndexByte(clientID, '%'); i >= 0 {
		clientID = clientID[:i]
	}
	return isLocalIP(net.ParseIP(clientID))
}

// keepLocalBanTemporary is the hook of banStore.allowed, called where three
// temporary bans within the window are about to become a permanent one. For
// an address of the local network it issues another temporary ban instead
// and returns true. Must hold the lock.
func (b *banStore) keepLocalBanTemporary(clientID string, shieldID string, reason utils.ShieldBanReason, now time.Time) bool {
	if !isLocalClient(clientID) {
		return false
	}
	b.issue(clientID, TEMP, shieldID, reason, now)
	utils.Warn("SmartShield: " + clientID + " has been banned temporarily again (an address of the local network is never banned permanently)")
	return true
}
