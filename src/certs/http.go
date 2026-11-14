package certs

// The routes, all under /cosmos/api/certificates. Reading is for whoever
// may read the configuration; everything else needs the permission to
// change it (and so the password confirmation), like the routes of the
// domains. Every body is bounded, unknown fields are refused and every name
// is checked here, whatever the form checked before.

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const maxBody = 1 << 10

// RenewZone orders the certificate of a domain now, without a restart. Set
// by the server (the order itself lives in package main).
var RenewZone = func(zone string) {}

// limiter allows a number of calls in a period, for everyone together:
// these routes are for administrators, and what they bound is what the
// server does for them (orders, DNS lookups).
type limiter struct {
	mu    sync.Mutex
	max   int
	per   time.Duration
	calls []time.Time
}

func (l *limiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.calls[:0]
	for _, call := range l.calls {
		if now.Sub(call) < l.per {
			kept = append(kept, call)
		}
	}
	l.calls = kept
	if len(l.calls) >= l.max {
		return false
	}
	l.calls = append(l.calls, now)
	return true
}

var (
	renewLimit  = &limiter{max: 4, per: time.Hour}
	lookupLimit = &limiter{max: 30, per: time.Hour}
)

func readBody(w http.ResponseWriter, req *http.Request, into interface{}) bool {
	raw, err := io.ReadAll(io.LimitReader(req.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		utils.HTTPError(w, "Request too large", http.StatusRequestEntityTooLarge, "CT001")
		return false
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		utils.HTTPError(w, "Invalid request", http.StatusBadRequest, "CT002")
		return false
	}
	return true
}

func answer(w http.ResponseWriter, data interface{}) {
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": data})
}

func readOnly(w http.ResponseWriter, req *http.Request) bool {
	if req.Method != "GET" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return false
	}
	return utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION_READ) == nil
}

func postOnly(w http.ResponseWriter, req *http.Request) bool {
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return false
	}
	return utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION) == nil
}

func tooMany(w http.ResponseWriter) {
	utils.HTTPError(w, "Too many requests, try again later", http.StatusTooManyRequests, "CT003")
}

// domainName checks a text as a domain: lower case, labels of letters,
// digits and hyphens, at least one dot, no address, no port, no wildcard.
func domainName(text string) (string, bool) {
	name := normalName(text)
	if name == "" || len(name) > 253 || !strings.Contains(name, ".") || strings.ContainsAny(name, ":*/ ") ||
		net.ParseIP(name) != nil || !utils.IsValidHostnameSyntax(name) {
		return "", false
	}
	return name, true
}

// StatusRoute is GET /api/certificates.
func StatusRoute(w http.ResponseWriter, req *http.Request) {
	if !readOnly(w, req) {
		return
	}
	answer(w, GetStatus())
}

// ProvidersRoute is GET /api/certificates/providers: the DNS providers and
// the settings each takes, in lego's words.
func ProvidersRoute(w http.ResponseWriter, req *http.Request) {
	if !readOnly(w, req) {
		return
	}
	answer(w, Providers())
}

// RenewRoute is POST /api/certificates/renew {domain}: order the
// certificate of a domain now. The answer says whether an order was
// started; how it ends is in the status.
func RenewRoute(w http.ResponseWriter, req *http.Request) {
	if !postOnly(w, req) {
		return
	}
	var body struct {
		Domain string `json:"domain"`
	}
	if !readBody(w, req, &body) {
		return
	}
	name, ok := domainName(body.Domain)
	if !ok {
		utils.HTTPError(w, "Not a domain name", http.StatusBadRequest, "CT004")
		return
	}
	var view *Certificate
	for _, cert := range GetStatus().Certificates {
		if normalName(cert.Domain) == name {
			found := cert
			view = &found
		}
	}
	if view == nil {
		utils.HTTPError(w, "This server has no such domain", http.StatusNotFound, "CT005")
		return
	}
	if !view.CanRenew {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"code":    "CT006",
			"message": renewRefusal(view.RenewBlocked),
			"reason":  view.RenewBlocked,
		})
		return
	}
	if !renewLimit.allow(clock()) {
		tooMany(w)
		return
	}

	key := utils.ForkZoneCertKey(view.Domain)
	store.force(key)
	utils.Log("Certificates: " + utils.GetAuthContext(req).Nickname + " asked for the certificate of " + view.Domain + " to be renewed now")
	utils.TriggerEvent("cosmos.settings", "Certificate renewal asked", "success", "zone@"+view.Domain,
		map[string]interface{}{"zone": view.Domain})
	go RenewZone(view.Domain)
	answer(w, map[string]interface{}{"started": true})
}

// renewRefusal says in English why "Renew now" does nothing, for clients
// that show the message as it is.
func renewRefusal(reason string) string {
	switch reason {
	case "running":
		return "An order for this certificate is running"
	case "notOrdered":
		return "This server does not order the certificate of this domain"
	case "noHTTPChallenge":
		return "The domain needs DNS access or a delegated challenge before a certificate can be ordered"
	case "needsRestart":
		return "This certificate is ordered when the server starts: use the forced renewal in the HTTPS settings"
	case waitRateLimited:
		return "The certificate authority asked to wait before ordering again"
	case waitDaily:
		return "Ten orders were made for this certificate in the last 24 hours: try again later"
	case waitWeekly:
		return "Thirty orders were made for this certificate in the last 7 days: try again later"
	case waitSameNames:
		return "Three certificates were issued for these names in the last 7 days: the current one is recent"
	}
	return "The certificate cannot be renewed now"
}

// DomainInfo is what is found out about a domain before it is added.
type DomainInfo struct {
	Domain string `json:"domain"`
	// Configured: this server already has the domain.
	Configured  bool     `json:"configured"`
	Nameservers []string `json:"nameservers"`
	// NameserversOf is the name the name servers were found at (the domain
	// itself or a parent).
	NameserversOf string `json:"nameserversOf,omitempty"`
	// Provider is the lego code of the DNS provider the name servers
	// belong to, "" when it is not one of the known ones.
	Provider     string `json:"provider"`
	ProviderName string `json:"providerName,omitempty"`
	// LookupFailed: the name servers could not be asked.
	LookupFailed bool `json:"lookupFailed"`
	// CanAnswer are the domains of this server that could answer the
	// challenge, each with the record that would hand it over.
	CanAnswer []Delegation `json:"canAnswer"`
}

// nameserversOf finds the name servers of a domain: its own, or those of
// the nearest parent that has some (a name inside someone's domain).
func nameserversOf(ctx context.Context, r Resolver, domain string) ([]string, string, error) {
	name := domain
	var last error
	for i := 0; i < 5 && strings.Contains(name, "."); i++ {
		lookup, cancel := context.WithTimeout(ctx, lookupTimeout)
		servers, err := r.NS(lookup, name)
		cancel()
		if err != nil {
			last = err
		}
		if len(servers) > 0 {
			return servers, name, nil
		}
		_, rest, _ := strings.Cut(name, ".")
		name = rest
	}
	return nil, "", last
}

// DomainRoute is POST /api/certificates/domain {domain}: what the guided
// dialog proposes for a domain. It asks the system's resolver for the name
// servers and nothing else; nothing is stored.
func DomainRoute(w http.ResponseWriter, req *http.Request) {
	if !postOnly(w, req) {
		return
	}
	var body struct {
		Domain string `json:"domain"`
	}
	if !readBody(w, req, &body) {
		return
	}
	name, ok := domainName(body.Domain)
	if !ok || !utils.PublicTLD(name) {
		utils.HTTPError(w, "Not a public domain name", http.StatusBadRequest, "CT004")
		return
	}
	if !lookupLimit.allow(clock()) {
		tooMany(w)
		return
	}

	zones := utils.GetMainConfig().HTTPConfig.DNSZones
	info := DomainInfo{Domain: name, Nameservers: []string{}, CanAnswer: []Delegation{}}
	_, info.Configured = findZoneExact(zones, name)

	ctx, cancel := context.WithTimeout(req.Context(), 4*lookupTimeout)
	defer cancel()
	servers, at, err := nameserversOf(ctx, resolver, name)
	if err != nil && len(servers) == 0 {
		info.LookupFailed = true
	}
	if len(servers) > 8 {
		servers = servers[:8]
	}
	for _, server := range servers {
		info.Nameservers = append(info.Nameservers, clean(server, 253))
	}
	info.NameserversOf = at
	info.Provider = providerForNameservers(servers)
	if entry, known := catalogue[info.Provider]; known {
		info.ProviderName = entry.Name
	}
	for _, zone := range zones {
		if canAnswerFor(zone) && !under(name, zone.Zone) {
			info.CanAnswer = append(info.CanAnswer, Delegation{Name: name, Record: challengeName(name), Target: DelegationTarget(name, zone.Zone)})
		}
	}
	answer(w, info)
}

// DelegationRoute is POST /api/certificates/delegation {domain, via}: is
// the record that hands the challenge of domain to via in the DNS. Looked
// up through the system's resolver; nothing is stored.
func DelegationRoute(w http.ResponseWriter, req *http.Request) {
	if !postOnly(w, req) {
		return
	}
	var body struct {
		Domain string `json:"domain"`
		Via    string `json:"via"`
	}
	if !readBody(w, req, &body) {
		return
	}
	name, ok := domainName(body.Domain)
	viaName, viaOK := domainName(body.Via)
	if !ok || !viaOK {
		utils.HTTPError(w, "Not a domain name", http.StatusBadRequest, "CT004")
		return
	}
	via, found := findZoneExact(utils.GetMainConfig().HTTPConfig.DNSZones, viaName)
	if !found || !canAnswerFor(via) || under(name, viaName) {
		utils.HTTPError(w, "The challenge can only be answered in another domain of this server that uses Let's Encrypt with DNS access", http.StatusBadRequest, "CT007")
		return
	}
	if !lookupLimit.allow(clock()) {
		tooMany(w)
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 4*lookupTimeout)
	defer cancel()
	answer(w, CheckDelegation(ctx, resolver, name, viaName))
}

// updateConfig changes the stored configuration. A variable for the tests.
var updateConfig = func(change func(config *utils.Config)) {
	utils.ConfigLock.Lock()
	defer utils.ConfigLock.Unlock()
	// the file, not the copy in memory: that one carries values from the environment
	config := utils.ReadConfigFromFile()
	change(&config)
	utils.SetBaseMainConfig(config)
}

// restartServer applies a changed setting. A variable for the tests.
var restartServer = func() { go utils.SoftRestartServer() }

// SettingsRoute is POST /api/certificates/settings {httpChallenge}: can a
// certificate authority reach this server on port 80 or 443 ("" to let
// Cosmos work it out, "no", "yes").
func SettingsRoute(w http.ResponseWriter, req *http.Request) {
	if !postOnly(w, req) {
		return
	}
	var body struct {
		HTTPChallenge *string `json:"httpChallenge"`
	}
	if !readBody(w, req, &body) {
		return
	}
	if body.HTTPChallenge == nil {
		utils.HTTPError(w, "Nothing to change", http.StatusBadRequest, "CT002")
		return
	}
	value := *body.HTTPChallenge
	if value != HTTPChallengeDetect && value != HTTPChallengeNo && value != HTTPChallengeYes {
		utils.HTTPError(w, "The setting is one of: empty (detect), no, yes", http.StatusBadRequest, "CT008")
		return
	}
	changed := false
	updateConfig(func(config *utils.Config) {
		changed = config.HTTPConfig.Certificates.HTTPChallenge != value
		config.HTTPConfig.Certificates.HTTPChallenge = value
	})
	if changed {
		utils.TriggerEvent("cosmos.settings", "Certificate settings saved", "success", "", map[string]interface{}{"httpChallenge": value})
		restartServer()
	}
	answer(w, map[string]interface{}{"changed": changed})
}
