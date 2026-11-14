package cloudflare

// Fork addition: names for use at home (utils/fork_home_names.go), the
// Cloudflare side: the one DNS-only wildcard record that makes them resolve,
// and the routes that switch them on and say how they stand.
//
// Per domain with the option on, one record: *.<label>.<domain> A <the
// server's private address>, written by upstream's reconciler with its owner
// marker like every name Cosmos manages (so only Cosmos' own record is ever
// changed or deleted, the apex never). It is never proxied: syncShield
// leaves "*." names alone, a home name cannot be shielded, and Cloudflare
// refuses to proxy a private address anyway. Every served name under the
// suffix is taken out of the names published at the public address. One
// wildcard rather than a record per app: two records instead of two per
// app, no app's name in DNS, and a new app resolves without any write.

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/libdns/libdns"

	"github.com/azukaar/cosmos-server/src/dnsrecords"
	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	maxHomeBody      = 1 << 10
	homeChecksAnHour = 20
)

// replaced by the tests: the address the home names point at, and the
// resolver of the check
var (
	homeAddress = utils.HomeLANAddress
	lookupHome  = func(ctx context.Context, name string) ([]string, error) {
		return net.DefaultResolver.LookupHost(ctx, name)
	}
)

func isHomeName(config utils.Config, host string) bool {
	_, ok := utils.HomeZoneOf(config, host)
	return ok
}

// homeHosts adjusts the names a DNS pass publishes: the names under a home
// suffix never get the public address, and each active suffix gets its
// wildcard at the private one. Part of the hook ForkMoreHosts (expose.go).
func homeHosts(hosts map[string][]string) map[string][]string {
	zones := utils.ActiveHomeZones(utils.GetMainConfig())
	if len(zones) == 0 {
		return hosts
	}
	for host := range hosts {
		for _, zone := range zones {
			if strings.HasSuffix(strings.TrimPrefix(host, "*."), "."+zone.Suffix) {
				delete(hosts, host)
			}
		}
	}
	address, _ := homeAddress()
	if address == "" {
		// not known yet: the record is left as it is (homeHidden)
		return hosts
	}
	for _, zone := range zones {
		hosts[zone.Wildcard()] = []string{address}
	}
	return hosts
}

// homeHidden takes the home wildcards whose address is not known out of what
// a pass sees, so that it neither writes nor deletes them: right after a
// start, before the interfaces could be read, the record must not go. Part
// of the hook ForkFilterDesired (shield.go).
func homeHidden(zone utils.DNSZoneConfig, providerZone string, actual []libdns.Record) []libdns.Record {
	if address, _ := homeAddress(); address != "" {
		return actual
	}
	hidden := map[string]bool{}
	for _, home := range utils.ActiveHomeZones(utils.GetMainConfig()) {
		if home.Zone == strings.ToLower(strings.TrimSuffix(zone.Zone, ".")) {
			hidden[dnsrecords.RelativeName(home.Wildcard(), providerZone)] = true
		}
	}
	if len(hidden) == 0 {
		return actual
	}
	out := make([]libdns.Record, 0, len(actual))
	for _, record := range actual {
		rr := record.RR()
		name := strings.ToLower(rr.Name)
		if owned := dnsrecords.OwnedName(name); rr.Type == "TXT" && owned != "" {
			name = owned
		}
		if !hidden[name] {
			out = append(out, record)
		}
	}
	return out
}

// pruneHomeNames drops the entries of domains that are no longer configured.
func pruneHomeNames(config utils.Config) []utils.ForkHomeNamesZone {
	out := []utils.ForkHomeNamesZone{}
	for _, entry := range config.HTTPConfig.Cloudflare.HomeNames {
		if utils.HomeZoneProblem(config, entry.Zone, entry.Label) != utils.HomeProblemNoZone {
			out = append(out, entry)
		}
	}
	return out
}

// HomeZoneStatus is one domain as the home-names card shows it.
type HomeZoneStatus struct {
	Zone string `json:"zone"`
	// Enabled: the owner switched it on; Active: in force (Problem empty)
	Enabled bool   `json:"enabled"`
	Active  bool   `json:"active"`
	Label   string `json:"label"`
	Problem string `json:"problem,omitempty"`
	// for an active domain
	Suffix   string `json:"suffix,omitempty"`
	Wildcard string `json:"wildcard,omitempty"`
	Panel    string `json:"panel,omitempty"`
	// Record: ok (in place at the last pass), pending, conflict (a record
	// Cosmos did not create is at the wildcard), error, no_address
	Record        string `json:"record,omitempty"`
	RecordAddress string `json:"recordAddress,omitempty"`
	RecordMessage string `json:"recordMessage,omitempty"`
	// Certificate: whether the certificate served for the panel's home name
	// covers it, and where it comes from
	CertificateCovered bool   `json:"certificateCovered"`
	CertificateSource  string `json:"certificateSource,omitempty"`
}

// HomeStatus is the answer of GET /api/home-names.
type HomeStatus struct {
	Zones []HomeZoneStatus `json:"zones"`
	// Address: the private address the names point at (administrators
	// only); AddressFresh: found just now (else the last one known)
	Address      string           `json:"address,omitempty"`
	AddressKnown bool             `json:"addressKnown"`
	AddressFresh bool             `json:"addressFresh"`
	Names        []utils.HomeName `json:"names"`
	Skipped      []utils.HomeSkip `json:"skipped"`
}

func homeRecordState(zone utils.DNSZoneConfig, wildcard string, address string) (state string, recorded string, message string) {
	syncMu.Lock()
	skip := ""
	if s, ok := synced[zone.Zone]; ok {
		skip = s.skipped[wildcard]
	}
	syncMu.Unlock()
	if skip == SkipConflict {
		return "conflict", "", ""
	}
	status, known := dnsrecords.GetStatus()[zone.Zone]
	switch {
	case known && status.LastError != "":
		return "error", "", cleanText(status.LastError, 300)
	case address == "":
		return "no_address", "", ""
	case !known:
		return "pending", "", ""
	}
	if ips, ok := status.Records[wildcard]; ok && len(ips) > 0 {
		recorded = ips[0]
		if recorded == address {
			return "ok", recorded, ""
		}
	}
	return "pending", recorded, ""
}

// GetHomeStatus says how the names for use at home stand.
func GetHomeStatus(withAddresses bool) HomeStatus {
	config := utils.GetMainConfig()
	plan := utils.PlanHomeNames(config)
	address, fresh := homeAddress()
	status := HomeStatus{Zones: []HomeZoneStatus{}, AddressKnown: address != "", AddressFresh: fresh, Names: plan.Names, Skipped: plan.Skipped}
	if withAddresses {
		status.Address = address
	}

	chosen := map[string]utils.ForkHomeNamesZone{}
	for _, entry := range config.HTTPConfig.Cloudflare.HomeNames {
		chosen[strings.ToLower(entry.Zone)] = entry
	}
	active := map[string]utils.HomeZone{}
	for _, zone := range plan.Zones {
		active[zone.Zone] = zone
	}

	names := []string{}
	byName := map[string]utils.DNSZoneConfig{}
	for _, zone := range config.HTTPConfig.DNSZones {
		name := strings.ToLower(strings.TrimSuffix(zone.Zone, "."))
		names = append(names, name)
		byName[name] = zone
	}
	for name := range chosen {
		if _, ok := byName[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	for _, name := range names {
		entry, enabled := chosen[name]
		label := entry.Label
		if label == "" {
			label = utils.HomeNamesDefaultLabel
		}
		row := HomeZoneStatus{Zone: name, Enabled: enabled, Label: label, Problem: utils.HomeZoneProblem(config, name, label)}
		if home, ok := active[name]; ok && enabled {
			row.Active = true
			row.Suffix, row.Wildcard = home.Suffix, home.Wildcard()
			row.Panel = utils.PanelHomeHost(config, home)
			row.Record, row.RecordAddress, row.RecordMessage = homeRecordState(byName[name], row.Wildcard, address)
			if !withAddresses {
				row.RecordAddress = ""
			}
			cert := utils.GetHostCertificate(config, row.Panel)
			row.CertificateCovered, row.CertificateSource = cert.Covered, cert.Source
		}
		status.Zones = append(status.Zones, row)
	}
	return status
}

// HomeNamesRoute godoc
// @Summary Names for use at home
// @Description GET: how the names for use at home stand per domain (the private address only for administrators). POST {zone, label, enabled}: switches them on or off for one domain. A domain qualifies when its DNS is at Cloudflare with a stored token, Cosmos manages its records and it gets a Let's Encrypt certificate through DNS. The server reloads.
// @Tags cloudflare
// @Router /api/home-names [get]
// @Router /api/home-names [post]
func HomeNamesRoute(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case "GET":
		if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION_READ) != nil {
			return
		}
		answer(w, GetHomeStatus(utils.HasPermission(req, utils.PERM_ADMIN_READ)))
	case "POST":
		setHomeNames(w, req)
	default:
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
	}
}

func homeRefusal(w http.ResponseWriter, code string, message string) {
	w.WriteHeader(http.StatusBadRequest)
	writeJSON(w, map[string]interface{}{"status": "error", "code": "HOME002", "message": message, "reason": code})
}

// homeSuffixInUse lists what already answers under a suffix that is to
// become a home suffix: such names would become reachable at home only.
func homeSuffixInUse(config utils.Config, suffix string) []string {
	used := map[string]bool{}
	for host := range servedHostnames(config) {
		if strings.HasSuffix(host, "."+suffix) {
			used[host] = true
		}
	}
	for _, host := range config.HTTPConfig.Cloudflare.Shielded {
		if host = utils.CloudflareHostname(host); strings.HasSuffix(host, "."+suffix) {
			used[host] = true
		}
	}
	out := []string{}
	for host := range used {
		out = append(out, host)
	}
	sort.Strings(out)
	return out
}

func setHomeNames(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION) != nil {
		return
	}
	var body struct {
		Zone    string `json:"zone"`
		Label   string `json:"label"`
		Enabled bool   `json:"enabled"`
	}
	if !readBody(w, req, maxHomeBody, &body) {
		return
	}
	zone, ok := domainName(body.Zone)
	if !ok {
		homeRefusal(w, "zone", "Invalid domain")
		return
	}
	label := strings.ToLower(strings.TrimSpace(body.Label))
	if label == "" {
		label = utils.HomeNamesDefaultLabel
	}
	if body.Enabled && !utils.ValidHomeLabel(label) {
		homeRefusal(w, "label", "The label must be 1 to 63 letters, digits or inner hyphens")
		return
	}

	reason := ""
	err := updateConfig(func(config *utils.Config) error {
		cf := &config.HTTPConfig.Cloudflare
		list := []utils.ForkHomeNamesZone{}
		for _, entry := range cf.HomeNames {
			if strings.ToLower(entry.Zone) != zone {
				list = append(list, entry)
			}
		}
		if body.Enabled {
			if problem := utils.HomeZoneProblem(*config, zone, label); problem != "" {
				reason = problem
				return errors.New("this domain cannot have names for use at home: " + problem)
			}
			if used := homeSuffixInUse(*config, label+"."+zone); len(used) > 0 {
				reason = "in_use"
				return errors.New("names under " + label + "." + zone + " are already in use: " + strings.Join(used, ", "))
			}
			if len(list) >= utils.HomeNamesMaxZones {
				reason = "too_many"
				return errors.New("names for use at home are possible on at most 4 domains")
			}
			list = append(list, utils.ForkHomeNamesZone{Zone: zone, Label: label})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Zone < list[j].Zone })
		cf.HomeNames = list
		return nil
	})
	if err != nil {
		homeRefusal(w, reason, err.Error())
		return
	}

	outcome := "off"
	if body.Enabled {
		outcome = "on"
	}
	utils.AuditSecurity(req, utils.SecConfigSaved, zone, "sections", "home_names", "home_names", outcome)
	triggerReconcile()
	reloadRoutes()
	answer(w, GetHomeStatus(utils.HasPermission(req, utils.PERM_ADMIN_READ)))
}

var homeChecks struct {
	sync.Mutex
	times []time.Time
}

func homeCheckAllowed(now time.Time) bool {
	homeChecks.Lock()
	defer homeChecks.Unlock()
	kept := homeChecks.times[:0]
	for _, at := range homeChecks.times {
		if now.Sub(at) < time.Hour {
			kept = append(kept, at)
		}
	}
	homeChecks.times = kept
	if len(kept) >= homeChecksAnHour {
		return false
	}
	homeChecks.times = append(homeChecks.times, now)
	return true
}

// HomeCheck is what the server's own resolver answers for the panel's home
// name of a domain. It is the server's view only: a device at home asks the
// router, which may answer otherwise.
type HomeCheck struct {
	Name string `json:"name"`
	// Result: ok (answers the server's private address), other (answers
	// something else), none (no answer: unknown name, or filtered),
	// no_address (the private address is not known), error
	Result    string   `json:"result"`
	Addresses []string `json:"addresses,omitempty"`
}

// HomeNamesCheckRoute godoc
// @Summary Does the panel's home name resolve from the server
// @Description Asks the server's own resolver for the panel's name for use at home in one domain (never a name from the request) and compares the answer with the server's private address. At most 20 checks an hour.
// @Tags cloudflare
// @Router /api/home-names/check [post]
func HomeNamesCheckRoute(w http.ResponseWriter, req *http.Request) {
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION_READ) != nil {
		return
	}
	var body struct {
		Zone string `json:"zone"`
	}
	if !readBody(w, req, maxHomeBody, &body) {
		return
	}
	config := utils.GetMainConfig()
	var home *utils.HomeZone
	for _, zone := range utils.ActiveHomeZones(config) {
		if zone.Zone == strings.ToLower(strings.TrimSpace(body.Zone)) {
			z := zone
			home = &z
		}
	}
	if home == nil {
		homeRefusal(w, "zone", "This domain has no names for use at home")
		return
	}
	if !homeCheckAllowed(time.Now()) {
		w.WriteHeader(http.StatusTooManyRequests)
		writeJSON(w, map[string]interface{}{"status": "error", "code": "HOME003", "message": "Too many checks: try again later", "reason": "rate_limited"})
		return
	}

	check := HomeCheck{Name: utils.PanelHomeHost(config, *home)}
	address, _ := homeAddress()
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()
	found, err := lookupHome(ctx, check.Name)
	var dnsErr *net.DNSError
	switch {
	case address == "":
		check.Result = "no_address"
	case err != nil && errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		check.Result = "none"
	case err != nil:
		check.Result = "error"
	default:
		check.Result = "other"
		for _, ip := range found {
			if ip == address {
				check.Result = "ok"
			}
		}
	}
	if utils.HasPermission(req, utils.PERM_ADMIN_READ) && len(found) <= 8 {
		check.Addresses = found
	}
	answer(w, check)
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	json.NewEncoder(w).Encode(data)
}
