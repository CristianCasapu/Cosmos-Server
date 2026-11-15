package cffake

// Fork addition: the IP Access Rules of a zone, as the API reference
// describes them (firewall/access_rules: create, list, delete; read
// 2026-11-15), and Cloudflare's rate limit.
//
//   - Listing needs "Firewall Services Read" or "Edit", changing "Edit";
//     otherwise 403 code 10000, the answer every other part gives.
//   - Modes block, challenge, whitelist, js_challenge, managed_challenge;
//     targets ip, ip6, ip_range (IPv4 /16 and /24, IPv6 /32, /48, /64),
//     asn, country (a country block is refused: Enterprise only).
//   - A second rule for the same target and value in a zone: 400. The
//     reference documents no code; the one used here (10009,
//     "firewallaccessrules.api.duplicate_of_existing") is what clients
//     report, and Cosmos does not depend on it.
//   - Listing: filters notes (case-insensitive part), configuration.target,
//     configuration.value, mode; per_page 1..1000 (default 20; more is 400);
//     result_info page, per_page, count, total_count (no total_pages).
//   - 50 000 rules per account.
//   - Rate limit: 1200 requests in 5 minutes, then 429 with Retry-After for
//     the rest of the window (code 971); RateLimitNext forces it for tests.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// AccessRule is an IP Access Rule.
type AccessRule struct {
	ID            string `json:"id"`
	Mode          string `json:"mode"`
	Notes         string `json:"notes"`
	Configuration struct {
		Target string `json:"target"`
		Value  string `json:"value"`
	} `json:"configuration"`
	AllowedModes []string  `json:"allowed_modes"`
	CreatedOn    time.Time `json:"created_on"`
	ModifiedOn   time.Time `json:"modified_on"`
}

type rulesState struct {
	// documentation addresses are refused as values (Cloudflare's own
	// behaviour there is not documented: both are tried by the tests)
	refuseDocumentation bool
	maxRules            int
	// rate limit
	window      time.Time
	count       int
	limit       int
	forcedLimit int
	retryAfter  int
}

var accessRulesPath = regexp.MustCompile(`^/client/v4/zones/([0-9a-f]{32})/firewall/access_rules/rules(?:/([0-9a-f]{32}))?$`)

var allModes = []string{"block", "challenge", "whitelist", "js_challenge", "managed_challenge"}

// RefuseDocumentationAddresses makes rules for 192.0.2.0/24 and the like
// refused with 400.
func (s *Server) RefuseDocumentationAddresses(refuse bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules.refuseDocumentation = refuse
}

// SetMaxRules sets how many rules the account may hold (default 50 000).
func (s *Server) SetMaxRules(max int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules.maxRules = max
}

// SetRateLimit sets how many requests may be made in five minutes (default
// 1200; 0 keeps the default).
func (s *Server) SetRateLimit(limit int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules.limit = limit
}

// RateLimitNext makes the next n requests answer 429 with Retry-After.
func (s *Server) RateLimitNext(n int, retryAfter int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules.forcedLimit, s.rules.retryAfter = n, retryAfter
}

// AccessRules lists the rules of a zone.
func (s *Server) AccessRules(zoneName string) []AccessRule {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, zone := range s.zones {
		if zone.Name == zoneName {
			return append([]AccessRule(nil), zone.AccessRules...)
		}
	}
	return nil
}

// AddAccessRule adds a rule the way the owner would in Cloudflare's
// dashboard, and returns its id.
func (s *Server) AddAccessRule(zoneName string, target string, value string, mode string, notes string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, zone := range s.zones {
		if zone.Name == zoneName {
			rule := AccessRule{ID: s.newID(), Mode: mode, Notes: notes, AllowedModes: allModes, CreatedOn: time.Now().UTC(), ModifiedOn: time.Now().UTC()}
			rule.Configuration.Target, rule.Configuration.Value = target, value
			zone.AccessRules = append(zone.AccessRules, rule)
			return rule.ID
		}
	}
	return ""
}

// RemoveAccessRule deletes a rule the way the owner would in the dashboard.
func (s *Server) RemoveAccessRule(zoneName string, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, zone := range s.zones {
		if zone.Name != zoneName {
			continue
		}
		for i, rule := range zone.AccessRules {
			if rule.ID == id {
				zone.AccessRules = append(zone.AccessRules[:i], zone.AccessRules[i+1:]...)
				return true
			}
		}
	}
	return false
}

// limited answers 429 when the request is over the rate limit. Runs with
// the lock held.
func (s *Server) limited(w http.ResponseWriter) bool {
	r := &s.rules
	now := time.Now()
	if now.Sub(r.window) >= 5*time.Minute {
		r.window, r.count = now, 0
	}
	r.count++
	limit := r.limit
	if limit <= 0 {
		limit = 1200
	}
	wait := 0
	if r.forcedLimit > 0 {
		r.forcedLimit--
		wait = r.retryAfter
		if wait <= 0 {
			wait = 60
		}
	} else if r.count > limit {
		wait = int(r.window.Add(5*time.Minute).Sub(now).Seconds()) + 1
	} else {
		return false
	}
	w.Header().Set("Retry-After", strconv.Itoa(wait))
	fail(w, http.StatusTooManyRequests, 971, "Please wait and consider throttling your request speed")
	return true
}

var documentation = []netip.Prefix{
	netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("2001:db8::/32"),
}

func (s *Server) validValue(target string, value string) bool {
	check := func(addr netip.Addr) bool {
		if !s.rules.refuseDocumentation {
			return true
		}
		for _, prefix := range documentation {
			if prefix.Contains(addr) {
				return false
			}
		}
		return true
	}
	switch target {
	case "ip":
		addr, err := netip.ParseAddr(value)
		return err == nil && addr.Is4() && check(addr)
	case "ip6":
		addr, err := netip.ParseAddr(value)
		return err == nil && addr.Is6() && !addr.Is4In6() && check(addr)
	case "ip_range":
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix.Masked() != prefix {
			return false
		}
		if prefix.Addr().Is4() {
			return (prefix.Bits() == 16 || prefix.Bits() == 24) && check(prefix.Addr())
		}
		return (prefix.Bits() == 32 || prefix.Bits() == 48 || prefix.Bits() == 64) && check(prefix.Addr())
	case "asn":
		return strings.HasPrefix(value, "AS")
	}
	return false
}

var accountRulesPath = regexp.MustCompile(`^/client/v4/accounts/([0-9a-f]{32})/firewall/access_rules/rules(?:/([0-9a-f]{32}))?$`)

// AccountRules lists the IP Access Rules of the account.
func (s *Server) AccountRules() []AccessRule {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]AccessRule(nil), s.accountRules...)
}

// serveAccessRules runs with the lock held.
func (s *Server) serveAccessRules(w http.ResponseWriter, req *http.Request, zoneID string, ruleID string, token Token, raw []byte) {
	zone := s.zone(zoneID, token)
	if zone == nil {
		fail(w, http.StatusForbidden, 10000, "Authentication error")
		return
	}
	s.serveRuleList(w, req, &zone.AccessRules, ruleID, token.ReadFirewall, token.EditFirewall, raw)
}

// serveAccountRules: the same at the account (every zone of it).
func (s *Server) serveAccountRules(w http.ResponseWriter, req *http.Request, accountID string, ruleID string, token Token, raw []byte) {
	if accountID != AccountID {
		fail(w, http.StatusForbidden, 10000, "Authentication error")
		return
	}
	s.serveRuleList(w, req, &s.accountRules, ruleID, token.ReadAccountRules, token.EditAccountRules, raw)
}

func (s *Server) serveRuleList(w http.ResponseWriter, req *http.Request, list *[]AccessRule, ruleID string, mayRead bool, mayEdit bool, raw []byte) {
	type holder struct{ AccessRules []AccessRule }
	zone := &holder{AccessRules: *list}
	defer func() { *list = zone.AccessRules }()
	token := struct{ ReadFirewall, EditFirewall bool }{mayRead, mayEdit}
	switch {
	case req.Method == "GET" && ruleID == "":
		if !token.ReadFirewall && !token.EditFirewall {
			fail(w, http.StatusForbidden, 10000, "Authentication error")
			return
		}
		query := req.URL.Query()
		perPage := 20
		if text := query.Get("per_page"); text != "" {
			n, err := strconv.Atoi(text)
			if err != nil || n < 1 || n > 1000 {
				fail(w, http.StatusBadRequest, 10001, "per_page must be between 1 and 1000")
				return
			}
			perPage = n
		}
		number, _ := strconv.Atoi(query.Get("page"))
		if number < 1 {
			number = 1
		}
		matching := []AccessRule{}
		for _, rule := range zone.AccessRules {
			if n := query.Get("notes"); n != "" && !strings.Contains(strings.ToLower(rule.Notes), strings.ToLower(n)) {
				continue
			}
			if v := query.Get("configuration.value"); v != "" && v != rule.Configuration.Value {
				continue
			}
			if t := query.Get("configuration.target"); t != "" && t != rule.Configuration.Target {
				continue
			}
			if m := query.Get("mode"); m != "" && m != rule.Mode {
				continue
			}
			matching = append(matching, rule)
		}
		from := (number - 1) * perPage
		if from > len(matching) {
			from = len(matching)
		}
		to := from + perPage
		if to > len(matching) {
			to = len(matching)
		}
		ok(w, matching[from:to], map[string]int{"page": number, "per_page": perPage, "count": to - from, "total_count": len(matching)})

	case req.Method == "POST" && ruleID == "":
		if !token.EditFirewall {
			fail(w, http.StatusForbidden, 10000, "Authentication error")
			return
		}
		var body struct {
			Mode          string `json:"mode"`
			Notes         string `json:"notes"`
			Configuration struct {
				Target string `json:"target"`
				Value  string `json:"value"`
			} `json:"configuration"`
		}
		if json.Unmarshal(raw, &body) != nil {
			fail(w, http.StatusBadRequest, 10001, "Request body is invalid.")
			return
		}
		known := false
		for _, mode := range allModes {
			known = known || mode == body.Mode
		}
		if !known {
			fail(w, http.StatusBadRequest, 10002, "firewallaccessrules.api.validation.invalid_mode")
			return
		}
		if body.Configuration.Target == "country" {
			fail(w, http.StatusForbidden, 10003, "firewallaccessrules.api.country_block_not_allowed")
			return
		}
		if !s.validValue(body.Configuration.Target, body.Configuration.Value) {
			fail(w, http.StatusBadRequest, 10004, "firewallaccessrules.api.validation.invalid_ip_address")
			return
		}
		for _, existing := range zone.AccessRules {
			if existing.Configuration.Target == body.Configuration.Target && existing.Configuration.Value == body.Configuration.Value {
				fail(w, http.StatusBadRequest, 10009, "firewallaccessrules.api.duplicate_of_existing")
				return
			}
		}
		total := len(s.accountRules)
		for _, other := range s.zones {
			total += len(other.AccessRules)
		}
		max := s.rules.maxRules
		if max <= 0 {
			max = 50000
		}
		if total >= max {
			fail(w, http.StatusBadRequest, 10010, "firewallaccessrules.api.quota_exceeded")
			return
		}
		rule := AccessRule{ID: s.newID(), Mode: body.Mode, Notes: body.Notes, AllowedModes: allModes, CreatedOn: time.Now().UTC(), ModifiedOn: time.Now().UTC()}
		rule.Configuration = body.Configuration
		zone.AccessRules = append(zone.AccessRules, rule)
		ok(w, rule, nil)

	case req.Method == "DELETE" && ruleID != "":
		if !token.EditFirewall {
			fail(w, http.StatusForbidden, 10000, "Authentication error")
			return
		}
		for i, rule := range zone.AccessRules {
			if rule.ID == ruleID {
				zone.AccessRules = append(zone.AccessRules[:i], zone.AccessRules[i+1:]...)
				ok(w, map[string]string{"id": ruleID}, nil)
				return
			}
		}
		fail(w, http.StatusNotFound, 10005, fmt.Sprintf("firewallaccessrules.api.not_found"))

	default:
		fail(w, http.StatusMethodNotAllowed, 10405, "Method not allowed for this authentication scheme")
	}
}

// RemoveAccountRule deletes a rule of the account the way the owner would.
func (s *Server) RemoveAccountRule(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, rule := range s.accountRules {
		if rule.ID == id {
			s.accountRules = append(s.accountRules[:i], s.accountRules[i+1:]...)
			return true
		}
	}
	return false
}
