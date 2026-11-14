package utils

// Fork addition: names for use at home under the owner's own domain.
//
// With the option on for a domain (HTTPConfig.Cloudflare.HomeNames), every
// app gets a second name, <app>.<label>.<domain> (label "home" unless the
// owner chose another), and the panel one too. The names point at the
// server's private address through one DNS-only wildcard record
// (*.<label>.<domain>, written by src/cloudflare) and are covered by one
// wildcard certificate name: every device at home resolves them through
// plain DNS and trusts the certificate, which .local names cannot offer.
//
// A home name of an app is a copy of the app's route computed from the
// configuration whenever it is needed (HomeRoutes), never stored: it carries
// every setting of its route by construction, and switching the option off,
// deleting a URL or an app leaves nothing behind. Upstream treats the routes
// of Constellation tunnels the same way, so the copies are added exactly
// where those are (each hook marked "Fork addition").
//
// A home name answers the local network only (proxy/fork_home_names.go).

import (
	"net"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// ForkHomeNamesZone is one domain whose apps get names for use at home.
type ForkHomeNamesZone struct {
	Zone  string `json:"Zone"`
	Label string `json:"Label"`
}

const (
	// HomeNamesMaxZones bounds the domains with names for use at home.
	HomeNamesMaxZones = 4
	// HomeNamesMaxPerZone bounds the apps that get a home name per domain.
	HomeNamesMaxPerZone = 512
	// HomeNamesDefaultLabel is the label proposed when the owner names none.
	HomeNamesDefaultLabel = "home"
	// homePanelFallback names the panel when its hostname gives no label.
	homePanelFallback = "cosmos"
)

var homeLabelSyntax = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidHomeLabel reports whether a text can be one label of a DNS name as
// the home names use it: letters, digits and inner hyphens, 1 to 63 long.
func ValidHomeLabel(label string) bool {
	return homeLabelSyntax.MatchString(label)
}

// Why a domain cannot have names for use at home. The UI puts the codes in
// words.
const (
	HomeProblemNoZone        = "no_zone"        // not a configured domain
	HomeProblemNotCloudflare = "not_cloudflare" // its DNS is not at Cloudflare
	HomeProblemNoToken       = "no_token"       // no Cloudflare token is stored
	HomeProblemNoRecords     = "records"        // Cosmos does not manage its records
	HomeProblemNoCertificate = "certificate"    // no Let's Encrypt certificate through DNS
	HomeProblemTooLong       = "too_long"       // label + domain leave no room for an app name
)

func homeZoneIndex(config Config, zone string) int {
	zone = zoneHost(zone)
	for i, z := range config.HTTPConfig.DNSZones {
		if zoneHost(z.Zone) == zone && zone != "" {
			return i
		}
	}
	return -1
}

func homeZoneToken(zone DNSZoneConfig) bool {
	for _, key := range []string{"CF_DNS_API_TOKEN", "CLOUDFLARE_DNS_API_TOKEN"} {
		if strings.TrimSpace(zone.DNSChallengeConfig[key]) != "" {
			return true
		}
	}
	return false
}

// HomeZoneProblem says why a domain cannot have names for use at home with
// a label, "" when it can.
func HomeZoneProblem(config Config, zone string, label string) string {
	i := homeZoneIndex(config, zone)
	if i == -1 {
		return HomeProblemNoZone
	}
	z := config.HTTPConfig.DNSZones[i]
	switch {
	case z.DNSChallengeProvider != "cloudflare":
		return HomeProblemNotCloudflare
	case !homeZoneToken(z):
		return HomeProblemNoToken
	case !z.ManageRecords:
		return HomeProblemNoRecords
	case !IssuesZoneCert(z):
		return HomeProblemNoCertificate
	case len("x."+label+"."+zoneHost(z.Zone)) > 253-62:
		// room for an app label of 63 characters
		return HomeProblemTooLong
	}
	return ""
}

// HomeZone is a domain whose names for use at home are in force.
type HomeZone struct {
	Zone   string `json:"zone"`
	Label  string `json:"label"`
	Suffix string `json:"suffix"` // <label>.<zone>
}

// Wildcard is the DNS record and certificate name that covers the zone's
// home names.
func (z HomeZone) Wildcard() string { return "*." + z.Suffix }

// ActiveHomeZones lists the domains whose names for use at home are in
// force: chosen by the owner, with a valid label, and still qualifying.
func ActiveHomeZones(config Config) []HomeZone {
	out := []HomeZone{}
	seen := map[string]bool{}
	for i, entry := range config.HTTPConfig.Cloudflare.HomeNames {
		if i >= HomeNamesMaxZones {
			break
		}
		zone := zoneHost(entry.Zone)
		if seen[zone] || !ValidHomeLabel(entry.Label) || HomeZoneProblem(config, zone, entry.Label) != "" {
			continue
		}
		seen[zone] = true
		out = append(out, HomeZone{Zone: zone, Label: entry.Label, Suffix: entry.Label + "." + zone})
	}
	return out
}

// HomeZoneOf tells which active domain a host is a home name of: a name
// under <label>.<zone> (the suffix itself is not one: the wildcard does not
// answer for it).
func HomeZoneOf(config Config, host string) (HomeZone, bool) {
	host = zoneHost(host)
	for _, zone := range ActiveHomeZones(config) {
		if strings.HasSuffix(host, "."+zone.Suffix) {
			return zone, true
		}
	}
	return HomeZone{}, false
}

// IsHomeHost reports whether a host (with or without port) is a name for
// use at home.
func IsHomeHost(host string) bool {
	_, ok := HomeZoneOf(GetMainConfig(), host)
	return ok
}

// panelHomeLabel is the label the panel's home name starts with: the first
// label of its hostname, or "cosmos" when that is an address, a domain's
// own name or gives nothing usable.
func panelHomeLabel(config Config) string {
	host := zoneHost(config.HTTPConfig.Hostname)
	if host == "" || net.ParseIP(host) != nil || homeZoneIndex(config, host) != -1 || !strings.Contains(host, ".") {
		return homePanelFallback
	}
	label := strings.SplitN(host, ".", 2)[0]
	if !ValidHomeLabel(label) {
		return homePanelFallback
	}
	return label
}

// PanelHomeHost is the panel's home name in an active domain.
func PanelHomeHost(config Config, zone HomeZone) string {
	return panelHomeLabel(config) + "." + zone.Suffix
}

// IsPanelHomeHost reports whether a host is a home name of the panel.
func IsPanelHomeHost(host string) bool {
	config := GetMainConfig()
	zone, ok := HomeZoneOf(config, host)
	return ok && zoneHost(host) == PanelHomeHost(config, zone)
}

// HomeName is one app's name for use at home.
type HomeName struct {
	Host   string `json:"host"`   // the home name
	Route  string `json:"route"`  // the route it is a copy of
	Source string `json:"source"` // that route's own host
	Zone   string `json:"zone"`
}

// HomeSkip is a route that did not get a home name, and why: "label" (its
// host gives no usable label), "panel" (the panel has that name), "explicit"
// (a URL already has that name), "taken" (another route of the same name
// got it), "too_many".
type HomeSkip struct {
	Route  string `json:"route"`
	Source string `json:"source"`
	Zone   string `json:"zone"`
	Reason string `json:"reason"`
}

// HomePlan is what the home names are for a configuration.
type HomePlan struct {
	Zones   []HomeZone           `json:"zones"`
	Names   []HomeName           `json:"names"`
	Panel   []string             `json:"panel"`
	Skipped []HomeSkip           `json:"skipped"`
	routes  []ProxyRouteConfig
}

// servesPage is BuildFromConfig's test for a route it registers.
func servesPage(route ProxyRouteConfig) bool {
	return !route.Disabled && (strings.HasPrefix(route.Target, "http://") || strings.HasPrefix(route.Target, "https://") ||
		route.Mode == "STATIC" || route.Mode == "SPA")
}

// homeSource returns a route's host when it can have a home name: a name
// without a port, not an address.
func homeSource(route ProxyRouteConfig) string {
	if !route.UseHost || strings.ContainsAny(route.Host, ", :") || !servesPage(route) {
		return ""
	}
	host := zoneHost(route.Host)
	if host == "" || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return ""
	}
	return host
}

var homePlanCache struct {
	sync.Mutex
	config *Config
	plan   HomePlan
}

// PlanHomeNames works out every home name of a configuration.
func PlanHomeNames(config Config) HomePlan {
	zones := ActiveHomeZones(config)
	plan := HomePlan{Zones: zones, Names: []HomeName{}, Panel: []string{}, Skipped: []HomeSkip{}}
	if len(zones) == 0 {
		return plan
	}
	routes := config.HTTPConfig.ProxyConfig.Routes

	for _, zone := range zones {
		panel := PanelHomeHost(config, zone)
		plan.Panel = append(plan.Panel, panel)

		// names the owner gave a URL under the suffix himself
		explicit := map[string]bool{}
		for _, route := range routes {
			if host := zoneHost(route.Host); route.UseHost && strings.HasSuffix(host, "."+zone.Suffix) {
				explicit[host] = true
			}
		}

		type candidate struct {
			index int
			host  string
			local bool
		}
		byLabel := map[string][]candidate{}
		labels := []string{}
		for i, route := range routes {
			host := homeSource(route)
			if host == "" || strings.HasSuffix(host, "."+zone.Suffix) {
				continue
			}
			label := strings.SplitN(host, ".", 2)[0]
			if !ValidHomeLabel(label) {
				plan.Skipped = append(plan.Skipped, HomeSkip{Route: route.Name, Source: host, Zone: zone.Zone, Reason: "label"})
				continue
			}
			if _, known := byLabel[label]; !known {
				labels = append(labels, label)
			}
			byLabel[label] = append(byLabel[label], candidate{index: i, host: host, local: strings.HasSuffix(host, ".local")})
		}

		count := 0
		for _, label := range labels {
			list := byLabel[label]
			// the address meant for home wins, then the first in the list
			sort.SliceStable(list, func(a, b int) bool { return list[a].local && !list[b].local })
			chosen := list[0].host
			name := label + "." + zone.Suffix
			reason := ""
			switch {
			case name == panel:
				reason = "panel"
			case explicit[name]:
				reason = "explicit"
			case count >= HomeNamesMaxPerZone:
				reason = "too_many"
			}
			if reason == "" {
				count++
			}
			for _, c := range list {
				route := routes[c.index]
				why := reason
				if why == "" && c.host != chosen {
					why = "taken"
				}
				if why != "" {
					plan.Skipped = append(plan.Skipped, HomeSkip{Route: route.Name, Source: c.host, Zone: zone.Zone, Reason: why})
					continue
				}
				plan.Names = append(plan.Names, HomeName{Host: name, Route: route.Name, Source: c.host, Zone: zone.Zone})
				twin := route
				twin.Host = name
				twin.ManagedByKind, twin.ManagedByName = "", "" // a copy in memory, nobody's to edit
				plan.routes = append(plan.routes, twin)
			}
		}
	}
	return plan
}

// CurrentHomePlan is PlanHomeNames of the configuration in force, worked out
// again only when the configuration was replaced (it is asked on every
// request through GetAllHostnames).
func CurrentHomePlan() HomePlan {
	current := mainConfig.Load()
	if current == nil {
		return PlanHomeNames(Config{})
	}
	homePlanCache.Lock()
	defer homePlanCache.Unlock()
	if homePlanCache.config != current {
		homePlanCache.plan = PlanHomeNames(*current)
		homePlanCache.config = current
	}
	return homePlanCache.plan
}

// HomeRoutes are the routes of the apps' home names: copies of their routes
// under the home name. Hook of BuildFromConfig, FindRouteByReqHost and the
// OpenID clients.
func HomeRoutes() []ProxyRouteConfig {
	routes := CurrentHomePlan().routes
	return append([]ProxyRouteConfig{}, routes...)
}

// HomeHostnames are the home names served: the apps' and the panel's. Hook
// of GetAllHostnames.
func HomeHostnames() []string {
	plan := CurrentHomePlan()
	out := append([]string{}, plan.Panel...)
	for _, name := range plan.Names {
		out = append(out, name.Host)
	}
	return out
}

// ForkHomeCertNames adjusts what a domain's certificate must cover: one
// wildcard per active home suffix in it instead of every home name on its
// own, so a single name is ordered and no app's name ends up in the public
// Certificate Transparency logs. Hook of ZoneCertHostnames.
func ForkHomeCertNames(zone DNSZoneConfig, names []string) []string {
	homeZones := ActiveHomeZones(GetMainConfig())
	wildcards := []string{}
	for _, home := range homeZones {
		if home.Zone == zoneHost(zone.Zone) {
			wildcards = append(wildcards, home.Wildcard())
		}
	}
	if len(wildcards) == 0 {
		return names
	}
	out := []string{}
	for _, name := range names {
		covered := false
		for _, wildcard := range wildcards {
			covered = covered || name == wildcard || CertCovers([]string{wildcard}, name)
		}
		if !covered {
			out = append(out, name)
		}
	}
	// after the domain's own pair, so the order stays upstream's
	insert := 0
	for insert < len(out) && (out[insert] == zoneHost(zone.Zone) || out[insert] == "*."+zoneHost(zone.Zone)) {
		insert++
	}
	result := append([]string{}, out[:insert]...)
	result = append(result, wildcards...)
	return append(result, out[insert:]...)
}

// ForkLANAddress returns the server's IPv4 address on the home network, ""
// when there is none. The proxy package sets it to the interface the .local
// names are published on (proxy/fork_mdns_ifaces.go).
var ForkLANAddress = func() string { return "" }

var homeLAN struct {
	sync.Mutex
	last string
}

// HomeLANAddress is the private IPv4 the home names point at, and whether
// it was found just now. When it cannot be found the last one is kept: a
// failed read is not a change of address.
func HomeLANAddress() (string, bool) {
	address := ForkLANAddress()
	ip := net.ParseIP(address)
	fresh := ip != nil && ip.To4() != nil && ip.IsPrivate()
	homeLAN.Lock()
	defer homeLAN.Unlock()
	if fresh {
		homeLAN.last = ip.To4().String()
	}
	return homeLAN.last, fresh
}
