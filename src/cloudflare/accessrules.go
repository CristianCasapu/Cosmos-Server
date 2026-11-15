package cloudflare

// Fork addition: the IP Access Rules of a zone, for the blocks at
// Cloudflare's edge (src/edgeblock).
//
//	GET    /zones/{zone_id}/firewall/access_rules/rules?notes=…&configuration.value=…&page=…&per_page=…
//	POST   /zones/{zone_id}/firewall/access_rules/rules {mode, configuration:{target, value}, notes}
//	DELETE /zones/{zone_id}/firewall/access_rules/rules/{id}
//
// The same calls at the account (/accounts/{account_id}/…) make a rule that
// applies to every zone of the account: the caller passes the scope
// "account:<account id>" instead of a zone id, and only when the owner chose
// it. Permissions (the API reference names only the account ones for this
// shared endpoint): zone "Firewall Services Write" (token editor: Zone >
// Firewall Services > Edit), account "Account Firewall Access Rules Write"
// (Account > Account Firewall Access Rules > Edit). Without them Cloudflare
// answers 403.

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	accessRulesPage  = 100
	maxAccessRulesPg = 20 // 2000 rules
)

// AccessRule is an IP Access Rule as Cloudflare holds it.
type AccessRule struct {
	ID            string `json:"id"`
	Mode          string `json:"mode"`
	Notes         string `json:"notes"`
	Configuration struct {
		Target string `json:"target"`
		Value  string `json:"value"`
	} `json:"configuration"`
	CreatedOn time.Time `json:"created_on"`
}

// AccountScope is the scope of the rules of a whole account.
func AccountScope(accountID string) string { return "account:" + accountID }

// scopePath turns a zone id or an AccountScope into the path's start.
func scopePath(scope string) (string, bool) {
	if id, isAccount := strings.CutPrefix(scope, "account:"); isAccount {
		return "/accounts/" + id, validID(id)
	}
	return "/zones/" + scope, validID(scope)
}

// the modes Cosmos uses
var accessModes = map[string]bool{"block": true, "managed_challenge": true}

// rule ids are 32 hex characters, like every other id of the API
var accessRuleID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// AccessTarget returns Cloudflare's target and value for an address or an
// IPv6 /64: "ip" for an IPv4 address, "ip6" for an IPv6 address,
// "ip_range" for an IPv6 /64 (the only sizes Cloudflare accepts are /32,
// /48 and /64 for IPv6, /16 and /24 for IPv4). ok is false for anything else.
func AccessTarget(key string) (target string, value string, ok bool) {
	if prefix, err := netip.ParsePrefix(key); err == nil {
		if prefix.Addr().Is6() && !prefix.Addr().Is4In6() && prefix.Bits() == 64 && prefix.Masked() == prefix {
			return "ip_range", prefix.String(), true
		}
		return "", "", false
	}
	addr, err := netip.ParseAddr(key)
	if err != nil || addr.Zone() != "" {
		return "", "", false
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return "ip", addr.String(), true
	}
	return "ip6", addr.String(), true
}

// ListAccessRules lists the rules of a zone whose note contains notes (or
// whose value is value, when given), at most 2000. more is true when there
// are others.
func (c *Client) ListAccessRules(ctx context.Context, zoneID string, notes string, value string) (rules []AccessRule, more bool, err error) {
	base, ok := scopePath(zoneID)
	if !ok {
		return nil, false, &APIError{Kind: "other", Message: "not a zone id"}
	}
	for page := 1; page <= maxAccessRulesPg; page++ {
		query := url.Values{"per_page": {strconv.Itoa(accessRulesPage)}, "page": {strconv.Itoa(page)}}
		if notes != "" {
			query.Set("notes", notes)
		}
		if value != "" {
			query.Set("configuration.value", value)
		}
		var batch []AccessRule
		env, err := c.call(ctx, "GET", base+"/firewall/access_rules/rules", query, nil, &batch)
		if err != nil {
			return nil, false, err
		}
		for _, rule := range batch {
			if accessRuleID.MatchString(rule.ID) {
				rule.Notes = cleanText(rule.Notes, 300)
				rule.Configuration.Value = cleanText(rule.Configuration.Value, 64)
				rules = append(rules, rule)
			}
		}
		if len(batch) < accessRulesPage || env.ResultInfo == nil {
			return rules, false, nil
		}
		total := env.ResultInfo.TotalPages
		if total == 0 && env.ResultInfo.PerPage > 0 {
			// the access rules answer has no total_pages: from the count
			count := env.ResultInfo.TotalCount
			if env.ResultInfo.Count > count {
				count = env.ResultInfo.Count
			}
			total = (count + env.ResultInfo.PerPage - 1) / env.ResultInfo.PerPage
		}
		if page >= total {
			return rules, false, nil
		}
	}
	return rules, true, nil
}

// CreateAccessRule makes a rule for a key (an address or an IPv6 /64).
func (c *Client) CreateAccessRule(ctx context.Context, zoneID string, key string, mode string, notes string) (AccessRule, error) {
	base, ok := scopePath(zoneID)
	if !ok {
		return AccessRule{}, &APIError{Kind: "other", Message: "not a zone id"}
	}
	target, value, ok := AccessTarget(key)
	if !ok || !accessModes[mode] {
		return AccessRule{}, &APIError{Kind: "other", Message: "not an address Cosmos blocks"}
	}
	body := map[string]interface{}{
		"mode":          mode,
		"configuration": map[string]string{"target": target, "value": value},
		"notes":         notes,
	}
	var rule AccessRule
	if _, err := c.call(ctx, "POST", base+"/firewall/access_rules/rules", nil, body, &rule); err != nil {
		return AccessRule{}, err
	}
	if !accessRuleID.MatchString(rule.ID) {
		return AccessRule{}, &APIError{Kind: "other", Message: "the answer is not in the expected form"}
	}
	return rule, nil
}

// DeleteAccessRule deletes a rule by its id. A rule that is not there any
// more is not an error.
func (c *Client) DeleteAccessRule(ctx context.Context, zoneID string, ruleID string) error {
	base, ok := scopePath(zoneID)
	if !ok || !accessRuleID.MatchString(ruleID) {
		return &APIError{Kind: "other", Message: "not a rule id"}
	}
	_, err := c.call(ctx, "DELETE", base+"/firewall/access_rules/rules/"+ruleID, nil, nil, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Kind == "not_found" {
		return nil
	}
	return err
}

// CanDeleteAccessRules finds out without changing anything whether the token
// may delete rules: it asks for the deletion of the rule id nobody has.
// "yes" for "not found", "no" for a refusal, "unknown" otherwise.
func (c *Client) CanDeleteAccessRules(ctx context.Context, zoneID string) string {
	base, ok := scopePath(zoneID)
	if !ok {
		return "unknown"
	}
	_, err := c.call(ctx, "DELETE", base+"/firewall/access_rules/rules/"+noRecordID, nil, nil, nil)
	var apiErr *APIError
	if err == nil || !errors.As(err, &apiErr) {
		return "unknown"
	}
	switch {
	case apiErr.Status == http.StatusNotFound:
		return "yes"
	case apiErr.Status == http.StatusForbidden || apiErr.Status == http.StatusUnauthorized:
		return "no"
	}
	return "unknown"
}

// ErrorKind says what went wrong with a call ("forbidden", "rate_limited",
// "unreachable", "server", "not_found", "invalid_token", "other"), the status
// and how long Cloudflare asked to wait.
func ErrorKind(err error) (kind string, status int, retryAfter time.Duration) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Kind, apiErr.Status, apiErr.RetryAfter
	}
	return "other", 0, 0
}

// EdgeZone is a configured Cloudflare domain with a stored token.
type EdgeZone struct {
	Name     string
	Token    string
	TokenTag string // a short digest of the token, never the token
	Shielded int    // how many shielded names lie under it
}

// EdgeZones lists the configured Cloudflare domains that have a token, with
// the number of shielded names under each.
func EdgeZones() []EdgeZone {
	config := utils.GetMainConfig()
	zones := []EdgeZone{}
	for _, zone := range config.HTTPConfig.DNSZones {
		token := storedToken(zone)
		if token == "" {
			continue
		}
		name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(zone.Zone), "."))
		count := 0
		for _, host := range config.HTTPConfig.Cloudflare.Shielded {
			if host == name || strings.HasSuffix(host, "."+name) {
				count++
			}
		}
		zones = append(zones, EdgeZone{Name: name, Token: token, TokenTag: tokenMark(token), Shielded: count})
	}
	return zones
}

// ZoneID finds the id of a domain the token sees.
func ZoneID(ctx context.Context, token string, name string) (string, error) {
	return lookupZoneID(ctx, NewClient(token), name)
}

// ZoneAccount finds the id of a domain and of the account that holds it.
func ZoneAccount(ctx context.Context, token string, name string) (zoneID string, accountID string, err error) {
	zones, _, err := NewClient(token).ListZones(ctx, name)
	if err != nil {
		return "", "", err
	}
	for _, zone := range zones {
		if zone.Name == name {
			if !validID(zone.Account.ID) {
				return zone.ID, "", nil
			}
			return zone.ID, zone.Account.ID, nil
		}
	}
	return "", "", &APIError{Kind: "not_found", Message: "the token does not see this domain"}
}
