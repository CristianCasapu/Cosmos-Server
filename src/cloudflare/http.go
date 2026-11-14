package cloudflare

// Fork addition: the routes of the Cloudflare integration, all under
// /cosmos/api/cloudflare. Reading the state is for whoever may read the
// configuration; everything else needs the permission to change it (and so
// the password confirmation), like the routes of the domains themselves.
// Every body is bounded and every name is checked here, whatever the form
// checked before.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/dnsrecords"
	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	maxCheckBody   = 4 << 10
	maxPreviewBody = 8 << 10
	maxSmallBody   = 2 << 10
	maxTakeOver    = 50
)

// replaced by the tests
var (
	triggerReconcile = dnsrecords.Trigger
	restartServer    = func() { go utils.SoftRestartServer() }
)

// AfterChange is called when the hostnames served through Cloudflare or
// Cloudflare's address ranges changed. The server sets it (src/fork_fail2ban.go):
// the never-ban list of Fail2ban follows both.
var AfterChange = func() {}

func readBody(w http.ResponseWriter, req *http.Request, limit int64, into interface{}) bool {
	raw, err := io.ReadAll(io.LimitReader(req.Body, limit+1))
	if err != nil || int64(len(raw)) > limit {
		utils.HTTPError(w, "Request too large", http.StatusRequestEntityTooLarge, "CF001")
		return false
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		utils.HTTPError(w, "Invalid request", http.StatusBadRequest, "CF002")
		return false
	}
	return true
}

func answer(w http.ResponseWriter, data interface{}) {
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": data})
}

func postOnly(w http.ResponseWriter, req *http.Request) bool {
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return false
	}
	return utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION) == nil
}

// domainName checks a text as a domain or hostname: lower case, labels of
// letters, digits and hyphens, at least one dot, no address, no port.
func domainName(text string) (string, bool) {
	name := strings.ToLower(strings.TrimSpace(text))
	name = strings.TrimSuffix(name, ".")
	if name == "" || len(name) > 253 || !strings.Contains(name, ".") || strings.Contains(name, ":") ||
		net.ParseIP(name) != nil || !utils.IsValidHostnameSyntax(name) {
		return "", false
	}
	return name, true
}

// apiFailure answers a failed call to Cloudflare: what kind, and Cloudflare's
// own words. Never the token.
func apiFailure(w http.ResponseWriter, err error) {
	message := ""
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		message = apiErr.Message
	}
	w.WriteHeader(http.StatusBadGateway)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "error",
		"code":    "CF010",
		"message": "Cloudflare: " + kindOf(err),
		"reason":  kindOf(err),
		"detail":  message,
	})
}

// CheckRoute godoc
// @Summary Check a Cloudflare API token
// @Description Tells whether a pasted token works, which domains it sees and whether it may read and edit their records. Nothing is created or changed at Cloudflare, and the token is not stored. With an empty token and a zone, the token stored for that domain is checked.
// @Tags cloudflare
// @Router /api/cloudflare/check [post]
func CheckRoute(w http.ResponseWriter, req *http.Request) {
	if !postOnly(w, req) {
		return
	}
	var body struct {
		Token string `json:"token"`
		Zone  string `json:"zone"`
	}
	if !readBody(w, req, maxCheckBody, &body) {
		return
	}
	token := tokenFor(body.Token, body.Zone)
	if token == "" {
		utils.HTTPError(w, "No token given", http.StatusBadRequest, "CF003")
		return
	}
	answer(w, CheckToken(req.Context(), token))
}

// PreviewRoute godoc
// @Summary Preview what connecting a Cloudflare domain will write
// @Description Reads the domain's records at Cloudflare and returns which records Cosmos would create, change or leave alone. Nothing is written.
// @Tags cloudflare
// @Router /api/cloudflare/preview [post]
func PreviewRoute(w http.ResponseWriter, req *http.Request) {
	if !postOnly(w, req) {
		return
	}
	var body struct {
		Token    string   `json:"token"`
		Zone     string   `json:"zone"`
		Panel    string   `json:"panel"`
		TakeOver []string `json:"takeOver"`
	}
	if !readBody(w, req, maxPreviewBody, &body) {
		return
	}
	zone, ok := domainName(body.Zone)
	if !ok {
		utils.HTTPError(w, "Invalid domain: expected a name such as example.org", http.StatusBadRequest, "CF004")
		return
	}
	panel := ""
	if strings.TrimSpace(body.Panel) != "" {
		if panel, ok = domainName(body.Panel); !ok {
			utils.HTTPError(w, "Invalid address for the panel", http.StatusBadRequest, "CF005")
			return
		}
	}
	takeOver, ok := hostnameList(body.TakeOver)
	if !ok {
		utils.HTTPError(w, "Invalid list of names", http.StatusBadRequest, "CF006")
		return
	}
	token := tokenFor(body.Token, zone)
	if format := TokenFormat(token); format != "token" {
		utils.HTTPError(w, "No usable token given", http.StatusBadRequest, "CF003")
		return
	}

	preview, err := MakePreview(req.Context(), token, zone, panel, takeOver)
	if err != nil {
		apiFailure(w, err)
		return
	}
	answer(w, preview)
}

func hostnameList(names []string) ([]string, bool) {
	if len(names) > maxTakeOver {
		return nil, false
	}
	out := []string{}
	for _, text := range names {
		name, ok := domainName(text)
		if !ok {
			return nil, false
		}
		out = append(out, name)
	}
	return out, true
}

// updateConfig changes the stored configuration under the lock. change
// returns an error to leave everything as it is.
func updateConfig(change func(config *utils.Config) error) error {
	utils.ConfigLock.Lock()
	defer utils.ConfigLock.Unlock()
	// the file, not the copy in memory: that one carries values from the environment
	config := utils.ReadConfigFromFile()
	if err := change(&config); err != nil {
		return err
	}
	utils.SetBaseMainConfig(config)
	return nil
}

func addName(list []string, name string) []string {
	for _, known := range list {
		if known == name {
			return list
		}
	}
	return append(list, name)
}

func removeName(list []string, name string) []string {
	out := []string{}
	for _, known := range list {
		if known != name {
			out = append(out, known)
		}
	}
	return out
}

// servedHostnames is the set of hostnames Cosmos answers for.
func servedHostnames(config utils.Config) map[string]bool {
	served := map[string]bool{}
	for _, host := range utils.ConfigHostnames(config) {
		if host = utils.CloudflareHostname(host); host != "" {
			served[host] = true
		}
	}
	return served
}

// prune drops the names Cosmos no longer serves from a list.
func prune(list []string, served map[string]bool) []string {
	out := []string{}
	for _, name := range list {
		if served[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// TakeOverRoute godoc
// @Summary Let Cosmos replace existing records
// @Description Names with a record at Cloudflare that Cosmos did not create are left alone until they are listed here. Each name must be under a domain of the configuration, or under the domain given (one that is about to be connected).
// @Tags cloudflare
// @Router /api/cloudflare/takeover [post]
func TakeOverRoute(w http.ResponseWriter, req *http.Request) {
	if !postOnly(w, req) {
		return
	}
	var body struct {
		Names []string `json:"names"`
		Zone  string   `json:"zone"`
	}
	if !readBody(w, req, maxPreviewBody, &body) {
		return
	}
	names, ok := hostnameList(body.Names)
	if !ok || len(names) == 0 {
		utils.HTTPError(w, "Invalid list of names", http.StatusBadRequest, "CF006")
		return
	}
	newZone := ""
	if strings.TrimSpace(body.Zone) != "" {
		if newZone, ok = domainName(body.Zone); !ok {
			utils.HTTPError(w, "Invalid domain", http.StatusBadRequest, "CF004")
			return
		}
	}

	err := updateConfig(func(config *utils.Config) error {
		zones := append([]utils.DNSZoneConfig{}, config.HTTPConfig.DNSZones...)
		if newZone != "" {
			zones = append(zones, utils.DNSZoneConfig{Zone: newZone})
		}
		list := config.HTTPConfig.Cloudflare.TakeOver
		for _, name := range names {
			index := utils.FindZone(zones, name)
			if index == -1 || strings.ToLower(zones[index].Zone) == name {
				// outside every domain, or a domain's own name: never Cosmos' to replace
				return errors.New(name + " is not a name under one of the domains")
			}
			list = addName(list, name)
		}
		if len(list) > utils.CloudflareMaxNames {
			return errors.New("too many names")
		}
		sort.Strings(list)
		config.HTTPConfig.Cloudflare.TakeOver = list
		return nil
	})
	if err != nil {
		utils.HTTPError(w, err.Error(), http.StatusBadRequest, "CF007")
		return
	}
	utils.AuditSecurity(req, utils.SecConfigSaved, "", "sections", "cloudflare")
	triggerReconcile()
	answer(w, map[string]interface{}{"takeOver": utils.GetMainConfig().HTTPConfig.Cloudflare.TakeOver})
}

// managesRecord reports whether Cosmos sets the record of a hostname at
// Cloudflare, proxy flag included: the domain's records are managed, and the
// name is neither the domain itself nor held by a record Cosmos did not
// create. For any other name the record is the admin's to keep.
func managesRecord(config utils.Config, host string) bool {
	zones := config.HTTPConfig.DNSZones
	index := utils.FindZone(zones, host)
	if index == -1 || !isCloudflareZone(zones[index]) || !zones[index].ManageRecords || strings.ToLower(zones[index].Zone) == host {
		return false
	}
	syncMu.Lock()
	defer syncMu.Unlock()
	state, ok := synced[zones[index].Zone]
	return !ok || state.skipped[host] == ""
}

// shieldProblem says why a hostname cannot be served through Cloudflare's
// proxy, "" when it can. The codes are turned into sentences by the UI.
//
// For a name whose record Cosmos does not set, the choice only tells Cosmos
// what to expect (whom to believe about the visitor, whom to refuse); the
// conditions about the record itself then do not apply.
func shieldProblem(config utils.Config, host string, address string) string {
	zones := config.HTTPConfig.DNSZones
	index := utils.FindZone(zones, host)
	switch {
	case !servedHostnames(config)[host]:
		return "not_served"
	case index == -1 || !isCloudflareZone(zones[index]):
		return "not_cloudflare"
	case utils.HostIsHTTPOnly(config, host):
		return "http_only"
	case hasOwnPort(config, host):
		return "own_port"
	case !utils.IsCloudflareHTTPSPort(utils.PublicHTTPSPort(config.HTTPConfig)):
		return "port"
	}
	if !managesRecord(config, host) {
		return ""
	}
	switch {
	case storedToken(zones[index]) == "":
		return "no_token"
	case zones[index].WildcardRecord:
		return "wildcard_record"
	}
	if ip := net.ParseIP(address); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
		return "private_address"
	}
	return ""
}

// hasOwnPort reports whether a URL answers for the hostname on a port of its
// own that Cloudflare does not proxy: a name is proxied for all its ports.
func hasOwnPort(config utils.Config, host string) bool {
	for _, route := range config.HTTPConfig.ProxyConfig.Routes {
		if !route.UseHost || utils.CloudflareHostname(route.Host) != host {
			continue
		}
		if _, port, err := net.SplitHostPort(strings.TrimSpace(route.Host)); err == nil {
			if number, err := strconv.Atoi(port); err != nil || !utils.IsCloudflareHTTPSPort(number) {
				return true
			}
		}
	}
	return false
}

// ShieldRoute godoc
// @Summary Serve a hostname through Cloudflare's proxy, or directly
// @Description Sets the admin's choice for one hostname Cosmos serves under a Cloudflare domain. Switching the first one on makes Cosmos believe Cloudflare's addresses about the visitor; the answer says when the last one was switched off.
// @Tags cloudflare
// @Router /api/cloudflare/shield [post]
func ShieldRoute(w http.ResponseWriter, req *http.Request) {
	if !postOnly(w, req) {
		return
	}
	var body struct {
		Hostname string `json:"hostname"`
		Shielded bool   `json:"shielded"`
	}
	if !readBody(w, req, maxSmallBody, &body) {
		return
	}
	host, ok := domainName(body.Hostname)
	if !ok {
		utils.HTTPError(w, "Invalid hostname", http.StatusBadRequest, "CF008")
		return
	}

	lastShield := false
	problem := ""
	err := updateConfig(func(config *utils.Config) error {
		cf := &config.HTTPConfig.Cloudflare
		served := servedHostnames(*config)
		list := prune(cf.Shielded, served)
		if body.Shielded {
			// the ports in use, which the environment may set over the file's
			effective := *config
			running := utils.GetMainConfig().HTTPConfig
			effective.HTTPConfig.HTTPPort, effective.HTTPConfig.HTTPSPort = running.HTTPPort, running.HTTPSPort
			if problem = shieldProblem(effective, host, knownAddress()); problem != "" {
				return errors.New("this address cannot be served through Cloudflare: " + problem)
			}
			list = addName(list, host)
			if len(list) > utils.CloudflareMaxNames {
				return errors.New("too many names")
			}
			cf.TrustRanges = true
		} else {
			had := len(list)
			list = removeName(list, host)
			lastShield = had > 0 && len(list) == 0 && cf.TrustRanges
		}
		sort.Strings(list)
		cf.Shielded = list
		cf.TakeOver = prune(cf.TakeOver, served)
		return nil
	})
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "error", "code": "CF009", "message": err.Error(), "reason": problem,
		})
		return
	}

	outcome := "directly"
	if body.Shielded {
		outcome = "shielded"
	}
	utils.AuditSecurity(req, utils.SecConfigSaved, host, "sections", "cloudflare", "served", outcome)
	forgetSync()
	triggerReconcile()
	AfterChange()

	cf := utils.GetMainConfig().HTTPConfig.Cloudflare
	answer(w, map[string]interface{}{
		"shielded":    cf.Shielded,
		"trustRanges": cf.TrustRanges,
		"lastShield":  lastShield,
	})
}

// PanelRoute godoc
// @Summary Give the panel a name under a domain
// @Description Sets Cosmos' own hostname to a configured domain or a name under one, and keeps the panel answering at the server's local address. No record is written for a domain's own name. The server reloads.
// @Tags cloudflare
// @Router /api/cloudflare/panel [post]
func PanelRoute(w http.ResponseWriter, req *http.Request) {
	if !postOnly(w, req) {
		return
	}
	var body struct {
		Hostname string `json:"hostname"`
	}
	if !readBody(w, req, maxSmallBody, &body) {
		return
	}
	host, ok := domainName(body.Hostname)
	if !ok {
		utils.HTTPError(w, "Invalid hostname: expected a name such as cosmos.example.org", http.StatusBadRequest, "CF008")
		return
	}

	changed := false
	err := updateConfig(func(config *utils.Config) error {
		zones := config.HTTPConfig.DNSZones
		index := utils.FindZone(zones, host)
		if index == -1 {
			return errors.New("this name is not under one of the configured domains")
		}
		for _, route := range config.HTTPConfig.ProxyConfig.Routes {
			if route.UseHost && utils.CloudflareHostname(route.Host) == host {
				return errors.New("this name is already the address of the URL " + route.Name)
			}
		}
		changed = utils.CloudflareHostname(config.HTTPConfig.Hostname) != host || !config.HTTPConfig.AllowHTTPLocalIPAccess
		config.HTTPConfig.Hostname = host
		// without this the panel refuses every request made to the server's
		// local IP address once it has a name: the way in that never depends
		// on DNS, a router or a certificate
		config.HTTPConfig.AllowHTTPLocalIPAccess = true
		return nil
	})
	if err != nil {
		utils.HTTPError(w, err.Error(), http.StatusBadRequest, "CF011")
		return
	}
	utils.AuditSecurity(req, utils.SecConfigSaved, host, "sections", "hostname")
	answer(w, map[string]interface{}{"hostname": host, "changed": changed})
	if changed {
		restartServer()
	}
}

// refreshRanges fetches Cloudflare's published ranges and stores them when
// they are what such a list can be.
func refreshRanges(ctx context.Context) error {
	v4, v6, err := PublishedRanges(ctx)
	if err != nil {
		return err
	}
	cleanV4, ok4 := utils.ValidCloudflareRanges(v4, false)
	cleanV6, ok6 := utils.ValidCloudflareRanges(v6, true)
	if !ok4 || !ok6 {
		return &APIError{Kind: "other", Message: "the list Cloudflare sent is not a list of public networks: kept the previous one"}
	}
	err = updateConfig(func(config *utils.Config) error {
		config.HTTPConfig.Cloudflare.RangesV4 = cleanV4
		config.HTTPConfig.Cloudflare.RangesV6 = cleanV6
		config.HTTPConfig.Cloudflare.RangesUpdated = time.Now().UTC().Truncate(time.Second)
		return nil
	})
	if err == nil {
		AfterChange()
	}
	return err
}

// RangesRoute godoc
// @Summary Cloudflare's address ranges
// @Description action "refresh" fetches Cloudflare's published ranges; "trust" and "distrust" switch on and off whether Cosmos believes those addresses about the visitor (it cannot be switched off while a hostname is served through Cloudflare).
// @Tags cloudflare
// @Router /api/cloudflare/ranges [post]
func RangesRoute(w http.ResponseWriter, req *http.Request) {
	if !postOnly(w, req) {
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if !readBody(w, req, maxSmallBody, &body) {
		return
	}

	switch body.Action {
	case "refresh":
		if err := refreshRanges(req.Context()); err != nil {
			apiFailure(w, err)
			return
		}
	case "trust", "distrust":
		err := updateConfig(func(config *utils.Config) error {
			cf := &config.HTTPConfig.Cloudflare
			cf.Shielded = prune(cf.Shielded, servedHostnames(*config))
			if body.Action == "distrust" && len(cf.Shielded) > 0 {
				return errors.New("an application is served through Cloudflare: switch that off first")
			}
			cf.TrustRanges = body.Action == "trust"
			return nil
		})
		if err != nil {
			utils.HTTPError(w, err.Error(), http.StatusBadRequest, "CF012")
			return
		}
	default:
		utils.HTTPError(w, "Unknown action", http.StatusBadRequest, "CF013")
		return
	}
	utils.AuditSecurity(req, utils.SecConfigSaved, "", "sections", "cloudflare", "ranges", body.Action)
	answer(w, rangesStatus(utils.GetMainConfig().HTTPConfig.Cloudflare, true))
}

// RangesState is what the UI shows about Cloudflare's ranges.
type RangesState struct {
	Trusted bool `json:"trusted"`
	Builtin bool `json:"builtin"`
	// Date: when the list in use was fetched, or when Cloudflare last changed
	// the built-in one
	Date   string   `json:"date"`
	Count  int      `json:"count"`
	Ranges []string `json:"ranges,omitempty"`
}

func rangesStatus(cf utils.ForkCloudflareConfig, withList bool) RangesState {
	v4, v6, builtin := utils.CloudflareRangeLists(cf)
	state := RangesState{Trusted: cf.TrustRanges, Builtin: builtin, Count: len(v4) + len(v6), Date: utils.CloudflareBuiltinDate}
	if !builtin {
		state.Date = cf.RangesUpdated.UTC().Format("2006-01-02")
	}
	if withList {
		state.Ranges = append(v4, v6...)
	}
	return state
}

// ZoneState is a Cloudflare domain of the configuration.
type ZoneState struct {
	Name           string `json:"name"`
	HasToken       bool   `json:"hasToken"`
	ManageRecords  bool   `json:"manageRecords"`
	WildcardRecord bool   `json:"wildcardRecord"`
	// SSLMode as last read while a hostname was shielded; "" when not read,
	// "unreadable" when the token may not read it
	SSLMode string `json:"sslMode"`
}

// HostState is one hostname Cosmos serves under a Cloudflare domain.
type HostState struct {
	Host     string `json:"host"`
	Zone     string `json:"zone"`
	Panel    bool   `json:"panel,omitempty"`
	Shielded bool   `json:"shielded"`
	// Managed: Cosmos sets this name's record at Cloudflare, proxy flag
	// included. When false the record is the admin's, and Shielded only says
	// what Cosmos expects.
	Managed bool        `json:"managed"`
	Record  *HostRecord `json:"record,omitempty"`
	// CannotShield says why the switch is not offered, "" when it is
	CannotShield string `json:"cannotShield,omitempty"`
	// URL is the address visitors use, with the port
	URL string `json:"url"`
}

// Status is the answer of GET /api/cloudflare.
type Status struct {
	Zones []ZoneState `json:"zones"`
	Hosts []HostState `json:"hosts"`
	// PublicPort is the HTTPS port visitors use; PortOK whether Cloudflare
	// proxies it
	PublicPort   int         `json:"publicPort"`
	PortOK       bool        `json:"portOK"`
	AllowedPorts []int       `json:"allowedPorts"`
	Ranges       RangesState `json:"ranges"`
	// Address: where the records point, for admins only
	Address string `json:"address,omitempty"`
	// Hostname is the panel's own address now
	Hostname string `json:"hostname"`
	// OriginPulls: whether Cloudflare's proxy must prove itself with a
	// client certificate, see originpulls.go
	OriginPulls OriginPullsState `json:"originPulls"`
}

// GetStatus gathers what the screens show. Nothing is asked of Cloudflare.
func GetStatus(withAddresses bool) Status {
	config := utils.GetMainConfig()
	cf := config.HTTPConfig.Cloudflare
	address := knownAddress()

	status := Status{
		Zones:        []ZoneState{},
		Hosts:        []HostState{},
		PublicPort:   utils.PublicHTTPSPort(config.HTTPConfig),
		AllowedPorts: utils.CloudflareHTTPSPorts,
		Ranges:       rangesStatus(cf, withAddresses),
		Hostname:     utils.CloudflareHostname(config.HTTPConfig.Hostname),
		OriginPulls:  originPullsStatus(config),
	}
	status.PortOK = utils.IsCloudflareHTTPSPort(status.PublicPort)
	if withAddresses {
		status.Address = address
	}

	zones := config.HTTPConfig.DNSZones
	for _, zone := range zones {
		if !isCloudflareZone(zone) {
			continue
		}
		syncMu.Lock()
		mode := ""
		if state, ok := synced[zone.Zone]; ok {
			mode = state.sslMode
		}
		syncMu.Unlock()
		status.Zones = append(status.Zones, ZoneState{
			Name:           strings.ToLower(zone.Zone),
			HasToken:       storedToken(zone) != "",
			ManageRecords:  zone.ManageRecords,
			WildcardRecord: zone.WildcardRecord,
			SSLMode:        mode,
		})
	}

	panel := utils.CloudflareHostname(config.HTTPConfig.Hostname)
	names := []string{}
	for name := range servedHostnames(config) {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		index := utils.FindZone(zones, name)
		if index == -1 || !isCloudflareZone(zones[index]) {
			continue
		}
		host := HostState{
			Host:     name,
			Zone:     strings.ToLower(zones[index].Zone),
			Panel:    name == panel,
			Shielded: utils.ShieldedIn(cf.Shielded, name),
			Managed:  managesRecord(config, name),
			URL:      publicURL(config, name, status.PublicPort),
		}
		if record, ok := RecordOf(name); ok {
			if !withAddresses {
				record.Message = ""
			}
			host.Record = &record
		}
		if !host.Shielded {
			host.CannotShield = shieldProblem(config, name, address)
		}
		status.Hosts = append(status.Hosts, host)
	}
	return status
}

func publicURL(config utils.Config, host string, port int) string {
	if utils.HostIsHTTPOnly(config, host) {
		return "http://" + host
	}
	if port == 443 || port == 0 {
		return "https://" + host
	}
	return "https://" + host + ":" + strconv.Itoa(port)
}

// StatusRoute godoc
// @Summary State of the Cloudflare integration
// @Description The Cloudflare domains of the configuration, every hostname Cosmos serves under them with the state of its record and whether it is served through Cloudflare's proxy, the public HTTPS port and whether Cloudflare proxies it, and Cloudflare's address ranges. Addresses are included for admins only. Nothing is asked of Cloudflare.
// @Tags cloudflare
// @Router /api/cloudflare [get]
func StatusRoute(w http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION_READ) != nil {
		return
	}
	answer(w, GetStatus(utils.HasPermission(req, utils.PERM_ADMIN_READ)))
}

var startOnce sync.Once

// how old a fetched (or the built-in) list may get while it is relied on
const rangesMaxAge = 7 * 24 * time.Hour

// Start keeps Cloudflare's ranges current while they are relied on: once a
// day it looks whether the list in use is older than a week and, if so,
// fetches it again. Nothing is fetched while no hostname is served through
// Cloudflare and its addresses are not trusted.
func Start() {
	startOnce.Do(func() {
		startOriginPullWatch()
		go func() {
			for {
				time.Sleep(24 * time.Hour)
				cf := utils.GetMainConfig().HTTPConfig.Cloudflare
				if !cf.TrustRanges || time.Since(cf.RangesUpdated) < rangesMaxAge {
					continue
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				if err := refreshRanges(ctx); err != nil {
					utils.Warn("[Cloudflare] the address ranges could not be refreshed: " + err.Error())
				}
				cancel()
			}
		}()
	})
}
