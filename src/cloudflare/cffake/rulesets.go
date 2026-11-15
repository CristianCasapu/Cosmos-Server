package cffake

// Fork addition: the WAF custom rules of a zone (the rulesets of the phase
// http_request_firewall_custom), as the documentation describes them
// (waf/custom-rules/create-api, the API reference of rulesets; read
// 2026-11-15):
//
//   - GET  …/rulesets/phases/http_request_firewall_custom/entrypoint: 404
//     "could not find entrypoint ruleset in the http_request_firewall_custom
//     phase" until it exists; reading needs "Zone WAF Read" or "Edit".
//   - POST …/rulesets {name, kind: zone, phase, rules}: makes it; refused
//     (400) when it exists already.
//   - POST …/rulesets/{id}/rules, PATCH and DELETE …/rules/{rule id}: answer
//     the whole ruleset; changing needs "Zone WAF Edit".
//   - The Free plan allows 5 custom rules; an expression is at most 4 096
//     characters; actions block, challenge, js_challenge, managed_challenge,
//     skip (log: Enterprise only). An expression "ip.src in {…}" must list
//     addresses or networks. The error codes are the stand-in's own where
//     the documentation names none.
//   - GET …/rulesets lists the zone's rulesets (the entrypoint, and
//     Cloudflare's managed ones, which every zone sees).

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

// Ruleset is the entrypoint ruleset of a zone's custom rules.
type Ruleset struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Kind        string        `json:"kind"`
	Phase       string        `json:"phase"`
	Version     string        `json:"version"`
	LastUpdated time.Time     `json:"last_updated"`
	Rules       []RulesetRule `json:"rules"`
}

// RulesetRule is a custom rule.
type RulesetRule struct {
	ID          string    `json:"id"`
	Version     string    `json:"version"`
	Action      string    `json:"action"`
	Expression  string    `json:"expression"`
	Description string    `json:"description"`
	Enabled     bool      `json:"enabled"`
	Ref         string    `json:"ref"`
	LastUpdated time.Time `json:"last_updated"`
}

// FreeCustomRules is how many custom rules a zone on the Free plan may have.
const FreeCustomRules = 5

const maxExpression = 4096

var (
	entrypointPath = regexp.MustCompile(`^/client/v4/zones/([0-9a-f]{32})/rulesets/phases/([a-z_]+)/entrypoint$`)
	rulesetsPath   = regexp.MustCompile(`^/client/v4/zones/([0-9a-f]{32})/rulesets$`)
	rulePath       = regexp.MustCompile(`^/client/v4/zones/([0-9a-f]{32})/rulesets/([0-9a-f]{32})/rules(?:/([0-9a-f]{32}))?$`)
	ipSet          = regexp.MustCompile(`^\(?ip\.src in \{([^{}]*)\}\)?$`)
)

var customActions = map[string]bool{"block": true, "challenge": true, "js_challenge": true, "managed_challenge": true, "skip": true}

// Entrypoint returns a copy of a zone's custom rules (nil: none yet).
func (s *Server) Entrypoint(zoneName string) *Ruleset {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, zone := range s.zones {
		if zone.Name == zoneName && zone.Entrypoint != nil {
			copy := *zone.Entrypoint
			copy.Rules = append([]RulesetRule(nil), zone.Entrypoint.Rules...)
			return &copy
		}
	}
	return nil
}

// AddCustomRule adds a rule the way the owner would in the dashboard (the
// entrypoint is made when missing) and returns its id.
func (s *Server) AddCustomRule(zoneName string, action string, expression string, description string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, zone := range s.zones {
		if zone.Name != zoneName {
			continue
		}
		if zone.Entrypoint == nil {
			zone.Entrypoint = &Ruleset{ID: s.newID(), Name: "default", Kind: "zone", Phase: "http_request_firewall_custom", Version: "1"}
		}
		rule := RulesetRule{ID: s.newID(), Version: "1", Action: action, Expression: expression, Description: description, Enabled: true, LastUpdated: time.Now().UTC()}
		rule.Ref = rule.ID
		zone.Entrypoint.Rules = append(zone.Entrypoint.Rules, rule)
		return rule.ID
	}
	return ""
}

// RemoveCustomRule deletes a rule the way the owner would.
func (s *Server) RemoveCustomRule(zoneName string, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, zone := range s.zones {
		if zone.Name != zoneName || zone.Entrypoint == nil {
			continue
		}
		for i, rule := range zone.Entrypoint.Rules {
			if rule.ID == id {
				zone.Entrypoint.Rules = append(zone.Entrypoint.Rules[:i], zone.Entrypoint.Rules[i+1:]...)
				return true
			}
		}
	}
	return false
}

// SetCustomRuleExpression changes a rule's expression the way the owner would.
func (s *Server) SetCustomRuleExpression(zoneName string, id string, expression string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, zone := range s.zones {
		if zone.Name != zoneName || zone.Entrypoint == nil {
			continue
		}
		for i := range zone.Entrypoint.Rules {
			if zone.Entrypoint.Rules[i].ID == id {
				zone.Entrypoint.Rules[i].Expression = expression
				return true
			}
		}
	}
	return false
}

// checkCustomRule says what Cloudflare would refuse in a rule, "" for nothing.
func checkCustomRule(rule RulesetRule) (int, string) {
	if !customActions[rule.Action] {
		if rule.Action == "log" {
			return 20120, "the log action is available on the Enterprise plan only"
		}
		return 20015, "unknown action " + rule.Action
	}
	if strings.TrimSpace(rule.Expression) == "" {
		return 20021, "expression is required"
	}
	if len(rule.Expression) > maxExpression {
		return 20022, "the expression is longer than 4096 characters"
	}
	if match := ipSet.FindStringSubmatch(strings.TrimSpace(rule.Expression)); match != nil {
		for _, item := range strings.Fields(match[1]) {
			if _, err := netip.ParseAddr(item); err == nil {
				continue
			}
			if prefix, err := netip.ParsePrefix(item); err == nil && prefix.Masked() == prefix {
				continue
			}
			return 20023, "filter parsing error: invalid IP address or network " + item
		}
	}
	return 0, ""
}

// serveRulesets runs with the lock held.
func (s *Server) serveRulesets(w http.ResponseWriter, req *http.Request, token Token, raw []byte) {
	path := req.URL.Path
	answer := func(zone *Zone) { ok(w, zone.Entrypoint, nil) }
	mayRead := token.ReadWAF || token.EditWAF

	if match := entrypointPath.FindStringSubmatch(path); match != nil && req.Method == "GET" {
		zone := s.zone(match[1], token)
		if zone == nil || !mayRead {
			fail(w, http.StatusForbidden, 10000, "Authentication error")
			return
		}
		if match[2] != "http_request_firewall_custom" || zone.Entrypoint == nil {
			fail(w, http.StatusNotFound, 10003, "could not find entrypoint ruleset in the "+match[2]+" phase")
			return
		}
		answer(zone)
		return
	}

	if match := rulesetsPath.FindStringSubmatch(path); match != nil {
		zone := s.zone(match[1], token)
		if zone == nil {
			fail(w, http.StatusForbidden, 10000, "Authentication error")
			return
		}
		switch req.Method {
		case "GET":
			if !mayRead {
				fail(w, http.StatusForbidden, 10000, "Authentication error")
				return
			}
			list := []map[string]string{}
			for i, name := range []string{"Cloudflare Managed Free Ruleset", "Cloudflare Normalization Ruleset", "DDoS L7 ruleset", "Cloudflare Exposed Credentials Check", "Cloudflare Sensitive Data Detection"} {
				list = append(list, map[string]string{"id": strings.Repeat("e", 31) + string(rune('0'+i)), "name": name, "kind": "managed", "phase": "http_request_firewall_managed"})
			}
			if zone.Entrypoint != nil {
				list = append(list, map[string]string{"id": zone.Entrypoint.ID, "name": zone.Entrypoint.Name, "kind": "zone", "phase": zone.Entrypoint.Phase})
			}
			ok(w, list, nil)
		case "POST":
			if !token.EditWAF {
				fail(w, http.StatusForbidden, 10000, "Authentication error")
				return
			}
			var body struct {
				Name  string        `json:"name"`
				Kind  string        `json:"kind"`
				Phase string        `json:"phase"`
				Rules []RulesetRule `json:"rules"`
			}
			if json.Unmarshal(raw, &body) != nil || body.Name == "" || body.Kind != "zone" || body.Phase != "http_request_firewall_custom" {
				fail(w, http.StatusBadRequest, 20003, "invalid ruleset: name, kind zone and the phase are required")
				return
			}
			if zone.Entrypoint != nil {
				fail(w, http.StatusBadRequest, 20217, "A similar configuration with rules already exists and overwriting will have unintended consequences.")
				return
			}
			if len(body.Rules) > FreeCustomRules {
				fail(w, http.StatusBadRequest, 20202, "exceeded the maximum number of rules in the phase http_request_firewall_custom: 5")
				return
			}
			set := &Ruleset{ID: s.newID(), Name: body.Name, Kind: "zone", Phase: body.Phase, Version: "1", LastUpdated: time.Now().UTC()}
			for _, rule := range body.Rules {
				if code, problem := checkCustomRule(rule); code != 0 {
					fail(w, http.StatusBadRequest, code, problem)
					return
				}
				rule.ID, rule.Version, rule.LastUpdated = s.newID(), "1", time.Now().UTC()
				rule.Ref = rule.ID
				set.Rules = append(set.Rules, rule)
			}
			zone.Entrypoint = set
			answer(zone)
		default:
			fail(w, http.StatusMethodNotAllowed, 10405, "Method not allowed for this authentication scheme")
		}
		return
	}

	match := rulePath.FindStringSubmatch(path)
	if match == nil {
		fail(w, http.StatusNotFound, 7003, "Could not route to "+path)
		return
	}
	zone := s.zone(match[1], token)
	if zone == nil || !token.EditWAF {
		fail(w, http.StatusForbidden, 10000, "Authentication error")
		return
	}
	if zone.Entrypoint == nil || zone.Entrypoint.ID != match[2] {
		fail(w, http.StatusNotFound, 10006, "could not find ruleset "+match[2])
		return
	}
	set := zone.Entrypoint
	ruleID := match[3]
	index := -1
	for i, rule := range set.Rules {
		if rule.ID == ruleID {
			index = i
		}
	}
	var change struct {
		Action      *string `json:"action"`
		Expression  *string `json:"expression"`
		Description *string `json:"description"`
		Enabled     *bool   `json:"enabled"`
	}
	switch {
	case req.Method == "POST" && ruleID == "":
		if len(set.Rules) >= FreeCustomRules {
			fail(w, http.StatusBadRequest, 20202, "exceeded the maximum number of rules in the phase http_request_firewall_custom: 5")
			return
		}
		var rule RulesetRule
		if json.Unmarshal(raw, &rule) != nil {
			fail(w, http.StatusBadRequest, 20003, "Request body is invalid.")
			return
		}
		if code, problem := checkCustomRule(rule); code != 0 {
			fail(w, http.StatusBadRequest, code, problem)
			return
		}
		rule.ID, rule.Version, rule.LastUpdated = s.newID(), "1", time.Now().UTC()
		rule.Ref = rule.ID
		set.Rules = append(set.Rules, rule)
		answer(zone)
	case req.Method == "PATCH" && ruleID != "":
		if index < 0 {
			fail(w, http.StatusNotFound, 10007, "could not find rule "+ruleID)
			return
		}
		if json.Unmarshal(raw, &change) != nil {
			fail(w, http.StatusBadRequest, 20003, "Request body is invalid.")
			return
		}
		updated := set.Rules[index]
		if change.Action != nil {
			updated.Action = *change.Action
		}
		if change.Expression != nil {
			updated.Expression = *change.Expression
		}
		if change.Description != nil {
			updated.Description = *change.Description
		}
		if change.Enabled != nil {
			updated.Enabled = *change.Enabled
		}
		if code, problem := checkCustomRule(updated); code != 0 {
			fail(w, http.StatusBadRequest, code, problem)
			return
		}
		updated.LastUpdated = time.Now().UTC()
		set.Rules[index] = updated
		answer(zone)
	case req.Method == "DELETE" && ruleID != "":
		if index < 0 {
			fail(w, http.StatusNotFound, 10007, "could not find rule "+ruleID)
			return
		}
		set.Rules = append(set.Rules[:index], set.Rules[index+1:]...)
		answer(zone)
	default:
		fail(w, http.StatusMethodNotAllowed, 10405, "Method not allowed for this authentication scheme")
	}
}
