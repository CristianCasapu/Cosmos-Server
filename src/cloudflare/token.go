package cloudflare

// Fork addition: what a pasted credential is, and what it can do.

import (
	"context"
	"net/url"
	"regexp"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

const maxTokenLength = 200

var (
	tokenChars   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	globalKeyHex = regexp.MustCompile(`^[0-9a-f]{37,45}$`)
)

// TokenFormat tells what kind of credential a text is, from its form alone
// (developers.cloudflare.com/fundamentals/api/get-started/token-formats):
// "token" (an API token, which is what is needed), "global_key" (the account's
// Global API Key: far too powerful, and not accepted for managing records),
// "origin_ca_key", "oauth" (a Wrangler login), or "malformed".
func TokenFormat(token string) string {
	token = strings.TrimSpace(token)
	switch {
	case strings.HasPrefix(token, "v1.0-"):
		return "origin_ca_key"
	case len(token) < 20 || len(token) > maxTokenLength || !tokenChars.MatchString(token):
		return "malformed"
	case strings.HasPrefix(token, "cfk_") || globalKeyHex.MatchString(token):
		return "global_key"
	case strings.HasPrefix(token, "cfoat_"):
		return "oauth"
	}
	return "token"
}

// ZoneAccess is one domain the token sees and what it may do there.
type ZoneAccess struct {
	Name   string `json:"name"`
	Status string `json:"status"` // Cloudflare's: active, pending, ...
	Paused bool   `json:"paused,omitempty"`
	Plan   string `json:"plan,omitempty"`
	// CanRead and CanEdit: "yes", "no" or "unknown" (not tried, or the answer
	// could not be understood)
	CanRead string `json:"canRead"`
	CanEdit string `json:"canEdit"`
	// Connected: the domain is already one of Cosmos' with this provider
	Connected bool `json:"connected"`
}

// TokenReport is the answer of the token check. It never contains the token.
type TokenReport struct {
	// Status: ok, expired, disabled, invalid, malformed, global_key,
	// origin_ca_key, oauth, no_zones, unreachable, rate_limited, error
	Status string `json:"status"`
	// Message is Cloudflare's own short text for a refusal
	Message string       `json:"message,omitempty"`
	Zones   []ZoneAccess `json:"zones"`
	// MoreZones: the token sees more domains than are listed
	MoreZones bool `json:"moreZones,omitempty"`
	// Broad: the token sees more than one domain
	Broad bool `json:"broad,omitempty"`
}

// zones probed for what the token may do; beyond that the list alone is shown
const maxProbedZones = 10

// CheckToken finds out whether a credential works and which domains it sees.
// Nothing is created or changed at Cloudflare.
func CheckToken(ctx context.Context, token string) TokenReport {
	report := TokenReport{Zones: []ZoneAccess{}}

	if format := TokenFormat(token); format != "token" {
		report.Status = format
		return report
	}
	client := NewClient(token)

	fail := func(err error) TokenReport {
		switch kindOf(err) {
		case "invalid_token":
			report.Status = "invalid"
		case "malformed_token":
			report.Status = "malformed"
		case "unreachable", "server":
			report.Status = "unreachable"
		case "rate_limited":
			report.Status = "rate_limited"
		default:
			report.Status = "error"
		}
		if apiErr, ok := err.(*APIError); ok {
			report.Message = apiErr.Message
		}
		return report
	}

	// A token owned by an account, not a user, is not known to this endpoint:
	// "invalid" here is only final once the zones cannot be listed either.
	status, verifyErr := client.VerifyToken(ctx)
	if verifyErr != nil && kindOf(verifyErr) != "invalid_token" {
		return fail(verifyErr)
	}
	if status == "expired" || status == "disabled" {
		report.Status = status
		return report
	}

	zones, more, err := client.ListZones(ctx, "")
	if err != nil {
		if verifyErr == nil && (kindOf(err) == "forbidden" || kindOf(err) == "invalid_token") {
			// valid, but it may not even list domains
			report.Status = "no_zones"
			return report
		}
		return fail(err)
	}
	if len(zones) == 0 {
		report.Status = "no_zones"
		return report
	}

	connected := map[string]bool{}
	for _, zone := range utils.GetMainConfig().HTTPConfig.DNSZones {
		if zone.DNSChallengeProvider == "cloudflare" {
			connected[strings.ToLower(zone.Zone)] = true
		}
	}

	for i, zone := range zones {
		access := ZoneAccess{
			Name:      zone.Name,
			Status:    cleanText(zone.Status, 30),
			Paused:    zone.Paused,
			Plan:      cleanText(zone.Plan.Name, 60),
			CanRead:   "unknown",
			CanEdit:   "unknown",
			Connected: connected[zone.Name],
		}
		if i < maxProbedZones {
			access.CanRead, access.CanEdit = probeZone(ctx, client, zone.ID)
		}
		report.Zones = append(report.Zones, access)
	}
	report.MoreZones = more
	report.Broad = more || len(zones) > 1
	report.Status = "ok"
	return report
}

// probeZone tells whether the token may read and edit the records of a zone.
func probeZone(ctx context.Context, client *Client, zoneID string) (canRead string, canEdit string) {
	canRead = "unknown"
	if _, err := client.call(ctx, "GET", "/zones/"+zoneID+"/dns_records", url.Values{"per_page": {"1"}}, nil, nil); err == nil {
		canRead = "yes"
	} else if kind := kindOf(err); kind == "forbidden" || kind == "invalid_token" {
		canRead = "no"
	}
	return canRead, client.CanEdit(ctx, zoneID)
}

// storedToken returns the token a configured Cloudflare domain holds, "" when
// there is none.
func storedToken(zone utils.DNSZoneConfig) string {
	if zone.DNSChallengeProvider != "cloudflare" {
		return ""
	}
	for _, key := range []string{"CF_DNS_API_TOKEN", "CLOUDFLARE_DNS_API_TOKEN"} {
		if value := strings.TrimSpace(zone.DNSChallengeConfig[key]); value != "" {
			return value
		}
	}
	return ""
}

// configuredZone finds a domain of the configuration by its name.
func configuredZone(config utils.Config, name string) (utils.DNSZoneConfig, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, zone := range config.HTTPConfig.DNSZones {
		if strings.ToLower(zone.Zone) == name {
			return zone, true
		}
	}
	return utils.DNSZoneConfig{}, false
}

// tokenFor is the token a request means: the one it carries, or, when it
// carries none (or the mask), the one stored for the domain it names.
func tokenFor(token string, zoneName string) string {
	token = strings.TrimSpace(token)
	if token != "" && token != utils.MaskedSecret {
		return token
	}
	if zone, ok := configuredZone(utils.GetMainConfig(), zoneName); ok {
		return storedToken(zone)
	}
	return ""
}
