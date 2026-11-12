package utils

import (
	"net"
	"strings"
	"time"
)

// SmartShield state shared between the proxy package (which enforces it),
// the constellation package (which replicates it over NATS) and the API.

const (
	SHIELD_STRIKE = 0
	SHIELD_TEMP   = 1
	SHIELD_PERM   = 2
)

// ShieldBanReason names the limit a client tripped, so the dashboard can say
// "requests: 36012 of 36000 on route X" instead of dumping two structs.
type ShieldBanReason struct {
	Limit   string  `json:"limit"` // requests, bytes, time, simultaneous, packets, escalation
	Used    float64 `json:"used"`
	Allowed float64 `json:"allowed"`
	Route   string  `json:"route"`
}

// ShieldBan is one entry in a client's history. ID is unique across the
// cluster (identity, timestamp and issuing node) so replicated copies dedupe.
type ShieldBan struct {
	ID       string          `json:"id"`
	ClientID string          `json:"clientID"`
	BanType  int             `json:"banType"`
	Time     time.Time       `json:"time"`
	Reason   ShieldBanReason `json:"reason"`
	ShieldID string          `json:"shieldID"`
	Node     string          `json:"node"`
}

// ShieldWhitelistEntry exempts a source IP or CIDR from SmartShield budgets,
// strikes and the abuse counter. The two flags extend the exemption to the
// geo block and to the per-route IP restrictions. Entries are IPs only:
// a user identity could otherwise let a geo-blocked client reach login.
type ShieldWhitelistEntry struct {
	IP                  string `json:"IP"`
	Label               string `json:"Label"`
	BypassGeo           bool   `json:"BypassGeo"`
	BypassIPRestriction bool   `json:"BypassIPRestriction"`
}

func (e ShieldWhitelistEntry) matches(ip string) bool {
	target := strings.TrimSpace(e.IP)
	if target == "" || ip == "" {
		return false
	}
	if strings.Contains(target, "/") {
		ok, _ := IPInRange(ip, target)
		return ok
	}
	if a, b := net.ParseIP(target), net.ParseIP(ip); a != nil && b != nil {
		return a.Equal(b)
	}
	return target == ip
}

// ShieldWhitelistMatch returns the first whitelist entry covering an IP.
func ShieldWhitelistMatch(ip string) (ShieldWhitelistEntry, bool) {
	for _, entry := range GetMainConfig().ShieldWhitelist {
		if entry.matches(ip) {
			return entry, true
		}
	}
	return ShieldWhitelistEntry{}, false
}

func IsShieldWhitelisted(ip string) bool {
	_, ok := ShieldWhitelistMatch(ip)
	return ok
}

func ShieldBypassesGeo(ip string) bool {
	entry, ok := ShieldWhitelistMatch(ip)
	return ok && entry.BypassGeo
}

func ShieldBypassesIPRestriction(ip string) bool {
	entry, ok := ShieldWhitelistMatch(ip)
	return ok && entry.BypassIPRestriction
}

// ResetIPAbuseCounter clears the counter that drops TCP/UDP connections
// outright once an IP has been blocked too often; unban must clear it too.
func ResetIPAbuseCounter(ip string) {
	BannedIPs.Delete(ip)
}
