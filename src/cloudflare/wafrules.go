package cloudflare

// Fork addition: WAF custom rules of a zone, for the blocks at Cloudflare's
// edge (src/edgeblock) when the token may not use IP Access Rules.
//
//	GET    /zones/{zone_id}/rulesets/phases/http_request_firewall_custom/entrypoint   (404: none yet)
//	POST   /zones/{zone_id}/rulesets   {name, kind: "zone", phase, rules}               (creates it)
//	POST   /zones/{zone_id}/rulesets/{ruleset_id}/rules            {action, expression, description, enabled}
//	PATCH  /zones/{zone_id}/rulesets/{ruleset_id}/rules/{rule_id}  {action, expression, description, enabled}
//	DELETE /zones/{zone_id}/rulesets/{ruleset_id}/rules/{rule_id}
//
// Permission: zone "Zone WAF Write" (token editor: Zone > Zone WAF > Edit);
// "Zone WAF Read" to read. Rulesets are supported by account-owned tokens.
// The entrypoint is never replaced as a whole (PUT): that would remove the
// owner's own rules. Only rules by their id are added, changed or deleted.

import (
	"context"
	"errors"
	"regexp"
)

// CustomPhase is the phase of the WAF custom rules.
const CustomPhase = "http_request_firewall_custom"

// MaxExpression is the longest expression Cloudflare accepts.
const MaxExpression = 4096

// Ruleset is a zone's entrypoint ruleset of the custom rules phase.
type Ruleset struct {
	ID    string        `json:"id"`
	Name  string        `json:"name"`
	Phase string        `json:"phase"`
	Rules []RulesetRule `json:"rules"`
}

// RulesetRule is one rule of a ruleset.
type RulesetRule struct {
	ID          string `json:"id,omitempty"`
	Action      string `json:"action"`
	Expression  string `json:"expression"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

var rulesetID = regexp.MustCompile(`^[0-9a-f]{32}$`)

var wafActions = map[string]bool{"block": true, "managed_challenge": true}

func cleanRuleset(set *Ruleset) (*Ruleset, error) {
	if set == nil || !rulesetID.MatchString(set.ID) {
		return nil, &APIError{Kind: "other", Message: "the answer is not in the expected form"}
	}
	kept := []RulesetRule{}
	for _, rule := range set.Rules {
		if rulesetID.MatchString(rule.ID) {
			rule.Description = cleanText(rule.Description, 500)
			kept = append(kept, rule)
		}
	}
	set.Rules = kept
	return set, nil
}

func checkRule(rule RulesetRule) error {
	if !wafActions[rule.Action] || rule.Expression == "" || len(rule.Expression) > MaxExpression || len(rule.Description) > 500 {
		return &APIError{Kind: "other", Message: "not a rule Cosmos makes"}
	}
	return nil
}

// CustomEntrypoint reads the zone's custom rules. nil without an error when
// the zone has none yet (Cloudflare answers 404).
func (c *Client) CustomEntrypoint(ctx context.Context, zoneID string) (*Ruleset, error) {
	if !validID(zoneID) {
		return nil, &APIError{Kind: "other", Message: "not a zone id"}
	}
	var set Ruleset
	_, err := c.call(ctx, "GET", "/zones/"+zoneID+"/rulesets/phases/"+CustomPhase+"/entrypoint", nil, nil, &set)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Kind == "not_found" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return cleanRuleset(&set)
}

// CreateCustomEntrypoint makes the zone's custom rules with one rule in it.
// Only for a zone that has none (CustomEntrypoint gave nil).
func (c *Client) CreateCustomEntrypoint(ctx context.Context, zoneID string, rule RulesetRule) (*Ruleset, error) {
	if !validID(zoneID) {
		return nil, &APIError{Kind: "other", Message: "not a zone id"}
	}
	if err := checkRule(rule); err != nil {
		return nil, err
	}
	rule.ID = ""
	body := map[string]interface{}{"name": "default", "kind": "zone", "phase": CustomPhase, "rules": []RulesetRule{rule}}
	var set Ruleset
	if _, err := c.call(ctx, "POST", "/zones/"+zoneID+"/rulesets", nil, body, &set); err != nil {
		return nil, err
	}
	return cleanRuleset(&set)
}

// AddCustomRule adds a rule at the end of the zone's custom rules.
func (c *Client) AddCustomRule(ctx context.Context, zoneID string, rulesetID string, rule RulesetRule) (*Ruleset, error) {
	if !validID(zoneID) || !validID(rulesetID) {
		return nil, &APIError{Kind: "other", Message: "not a ruleset id"}
	}
	if err := checkRule(rule); err != nil {
		return nil, err
	}
	rule.ID = ""
	var set Ruleset
	if _, err := c.call(ctx, "POST", "/zones/"+zoneID+"/rulesets/"+rulesetID+"/rules", nil, rule, &set); err != nil {
		return nil, err
	}
	return cleanRuleset(&set)
}

// EditCustomRule changes one rule.
func (c *Client) EditCustomRule(ctx context.Context, zoneID string, rulesetID string, ruleID string, rule RulesetRule) error {
	if !validID(zoneID) || !validID(rulesetID) || !validID(ruleID) {
		return &APIError{Kind: "other", Message: "not a rule id"}
	}
	if err := checkRule(rule); err != nil {
		return err
	}
	rule.ID = ""
	_, err := c.call(ctx, "PATCH", "/zones/"+zoneID+"/rulesets/"+rulesetID+"/rules/"+ruleID, nil, rule, nil)
	return err
}

// DeleteCustomRule deletes one rule; one that is not there any more is not
// an error.
func (c *Client) DeleteCustomRule(ctx context.Context, zoneID string, rulesetID string, ruleID string) error {
	if !validID(zoneID) || !validID(rulesetID) || !validID(ruleID) {
		return &APIError{Kind: "other", Message: "not a rule id"}
	}
	_, err := c.call(ctx, "DELETE", "/zones/"+zoneID+"/rulesets/"+rulesetID+"/rules/"+ruleID, nil, nil, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Kind == "not_found" {
		return nil
	}
	return err
}
