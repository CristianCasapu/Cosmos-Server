package cloudflare

// Fork addition: which names Cosmos may write in a Cloudflare zone, and the
// preview of what connecting a domain will do there.
//
// Upstream's rule is that declaring a hostname is consent to replace whatever
// sits at that name. In a zone that also serves the owner's website that is
// too generous, so for Cloudflare zones two kinds of names are taken out of
// what a reconcile pass writes:
//
//   - the zone's own name (the apex): never written, never deleted;
//   - a name that already has an address or alias record and no marker of
//     Cosmos: left alone until an admin says Cosmos may replace it
//     (HTTPConfig.Cloudflare.TakeOver).
//
// Removal is upstream's and unchanged: only names carrying this server's
// marker are ever deleted.

import (
	"context"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/libdns/libdns"

	"github.com/azukaar/cosmos-server/src/dnsrecords"
	"github.com/azukaar/cosmos-server/src/upnp"
	"github.com/azukaar/cosmos-server/src/utils"
)

// Why a name is left alone.
const (
	SkipApex     = "protected" // the domain's own name
	SkipConflict = "conflict"  // a record Cosmos did not create
)

func isCloudflareZone(zone utils.DNSZoneConfig) bool {
	return zone.DNSChallengeProvider == "cloudflare"
}

func fqdnOf(relative string, zone string) string {
	relative = strings.ToLower(strings.TrimSuffix(relative, "."))
	if relative == "" || relative == "@" {
		return zone
	}
	return relative + "." + zone
}

// foreignNames lists the names (relative to the zone) that have an address or
// alias record and no marker of this server (owner) or of a Cosmos from
// before owners existed.
func foreignNames(actual []libdns.Record, owner string) map[string]bool {
	has := map[string]bool{}
	ours := map[string]bool{}
	for _, record := range actual {
		rr := record.RR()
		name := strings.ToLower(rr.Name)
		if name == "" {
			name = "@"
		}
		switch rr.Type {
		case "A", "AAAA", "CNAME":
			has[name] = true
		case "TXT":
			if owned := dnsrecords.OwnedName(name); owned != "" {
				if marker := dnsrecords.MarkerOwner(rr.Data); marker == owner || marker == "" {
					ours[owned] = true
				}
			}
		}
	}
	foreign := map[string]bool{}
	for name := range has {
		if !ours[name] {
			foreign[name] = true
		}
	}
	return foreign
}

// filterDesired takes the names Cosmos leaves alone in a Cloudflare zone out
// of a pass: out of what it may write (desired) and out of what it sees
// (actual), so that upstream's Plan can neither write nor delete anything at
// such a name. It says why, by full name, for the names that were wanted.
//
// The zone's own name is always taken out, wanted or not, with any marker it
// may carry: nothing a pass does can reach the apex.
func filterDesired(providerZone string, desired dnsrecords.Desired, actual []libdns.Record, takeOver []string, owner string) (dnsrecords.Desired, []libdns.Record, map[string]string) {
	providerZone = strings.ToLower(strings.TrimSuffix(providerZone, "."))
	foreign := foreignNames(actual, owner)
	allowed := map[string]bool{}
	for _, name := range takeOver {
		allowed[utils.CloudflareHostname(name)] = true
	}

	kept := dnsrecords.Desired{}
	skipped := map[string]string{}
	hidden := map[string]bool{"@": true}
	for fqdn, ips := range desired {
		name := strings.ToLower(strings.TrimSuffix(fqdn, "."))
		relative := dnsrecords.RelativeName(name, providerZone)
		switch {
		case name == providerZone:
			skipped[name] = SkipApex
		case foreign[relative] && !allowed[name]:
			skipped[name] = SkipConflict
			hidden[relative] = true
		default:
			kept[fqdn] = ips
		}
	}

	visible := make([]libdns.Record, 0, len(actual))
	for _, record := range actual {
		rr := record.RR()
		name := strings.ToLower(rr.Name)
		if name == "" {
			name = "@"
		}
		if owned := dnsrecords.OwnedName(name); rr.Type == "TXT" && owned != "" {
			name = owned
		}
		if !hidden[name] {
			visible = append(visible, record)
		}
	}
	return kept, visible, skipped
}

// toLibdns turns Cloudflare's records into the form upstream's Plan reads.
func toLibdns(records []Record, zone string) []libdns.Record {
	out := make([]libdns.Record, 0, len(records))
	for _, record := range records {
		data := record.Content
		if record.Type == "TXT" {
			data = strings.Trim(strings.TrimSpace(data), `"`)
		}
		out = append(out, libdns.RR{
			Name: dnsrecords.RelativeName(record.Name, zone),
			Type: record.Type,
			Data: data,
			TTL:  time.Duration(record.TTL) * time.Second,
		})
	}
	return out
}

// PreviewRow is one name Cosmos serves under the domain and what connecting
// will do with its record.
type PreviewRow struct {
	Name string `json:"name"`
	// Role: "panel" for Cosmos' own address, "app" for an application's
	Role string `json:"role"`
	// Action: create, update (an own record gets another address), replace
	// (a record Cosmos did not create, allowed by the admin), keep (already
	// right), conflict (a record Cosmos did not create: left alone), protected
	// (the domain's own name: left alone), remove (an own record nothing uses)
	Action  string `json:"action"`
	Current string `json:"current,omitempty"`
	New     string `json:"new,omitempty"`
	// Proxied: the existing record is served through Cloudflare's proxy;
	// PointsHere: it points at this server's public address (false as well
	// when that address is not known)
	Proxied    bool `json:"proxied,omitempty"`
	PointsHere bool `json:"pointsHere,omitempty"`
}

// ExistingRecord is a record of the zone that is not touched.
type ExistingRecord struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	Apex    bool   `json:"apex,omitempty"`
}

// Preview is what connecting a domain will write, before anything is written.
type Preview struct {
	Zone string `json:"zone"`
	// Address is where the records will point: this server's public address
	// as far as Cosmos knows it now, "" when it will be looked up later.
	Address   string           `json:"address"`
	CanEdit   string           `json:"canEdit"`
	Rows      []PreviewRow     `json:"rows"`
	Untouched []ExistingRecord `json:"untouched"`
	// UntouchedMore: the zone has more records than are listed (none of them
	// is touched either)
	UntouchedMore bool `json:"untouchedMore,omitempty"`
	// Truncated: the zone has more records than Cosmos reads; a preview of
	// such a zone is not complete
	Truncated bool `json:"truncated,omitempty"`
	// Warnings: "caa" (the zone's CAA records do not allow Let's Encrypt),
	// "wildcard" (the zone has a wildcard record: names without a record of
	// their own already resolve), "private_address" (the records would point
	// at a local address)
	Warnings []string `json:"warnings"`
	// SSLMode is Cloudflare's setting for the zone, "" when the token may not
	// read it
	SSLMode string `json:"sslMode"`
	// Apex is what the domain's own name holds today. Cosmos never writes it;
	// when it is proxied and points at this server, the panel can be reached
	// under it on Cosmos' port without any record of Cosmos' own.
	Apex ApexState `json:"apex"`
}

// ApexState describes the address records of the domain's own name.
type ApexState struct {
	Exists     bool `json:"exists"`
	Proxied    bool `json:"proxied"`
	PointsHere bool `json:"pointsHere"`
}

const maxUntouched = 300

// knownAddress is this server's public IPv4 as far as Cosmos knows it without
// asking anyone: what DynDNS advertises, else what the router reports.
var knownAddress = func() string {
	if address := dnsrecords.CurrentAddress(); address != "" {
		return address
	}
	if st := upnp.GetStatus(); st.Enabled {
		if ip := net.ParseIP(st.ExternalIP); ip != nil && ip.To4() != nil {
			return ip.To4().String()
		}
	}
	return ""
}

// placeholder for the address in a plan while it is not known: only the
// comparison with existing records needs one
const unknownAddress = "0.0.0.0"

// servedUnder lists the hostnames Cosmos serves that belong to the domain
// zoneName (as the longest matching domain), with the panel's address
// replaced by panelHost when that is given.
func servedUnder(config utils.Config, zoneName string, panelHost string) (hosts []string, panel string) {
	zones := append([]utils.DNSZoneConfig{}, config.HTTPConfig.DNSZones...)
	index := -1
	for i, zone := range zones {
		if strings.ToLower(zone.Zone) == zoneName {
			index = i
		}
	}
	if index == -1 {
		zones = append(zones, utils.DNSZoneConfig{Zone: zoneName})
		index = len(zones) - 1
	}

	panel = utils.CloudflareHostname(config.HTTPConfig.Hostname)
	if panelHost != "" {
		panel = utils.CloudflareHostname(panelHost)
	}
	seen := map[string]bool{}
	add := func(host string) {
		host = utils.CloudflareHostname(host)
		if host == "" || host == "0.0.0.0" || seen[host] || net.ParseIP(host) != nil || strings.ContainsAny(host, ", *") {
			return
		}
		if utils.FindZone(zones, host) == index {
			seen[host] = true
			hosts = append(hosts, host)
		}
	}
	add(panel)
	for _, route := range config.HTTPConfig.ProxyConfig.Routes {
		if route.UseHost {
			add(route.Host)
		}
	}
	sort.Strings(hosts)
	return hosts, panel
}

// buildPreview compares what Cosmos would write for the hostnames with the
// records of the zone, with upstream's own Plan.
func buildPreview(zoneName string, hosts []string, panel string, records []Record, address string, takeOver []string, owner string) Preview {
	preview := Preview{Zone: zoneName, Address: address, Rows: []PreviewRow{}, Untouched: []ExistingRecord{}, Warnings: []string{}}

	target := address
	if target == "" {
		target = unknownAddress
	}
	desired := dnsrecords.Desired{}
	for _, host := range hosts {
		desired[host] = []string{target}
	}

	actual := toLibdns(records, zoneName)
	foreign := foreignNames(actual, owner)
	kept, visible, skipped := filterDesired(zoneName, desired, actual, takeOver, owner)
	changes := dnsrecords.Plan(zoneName, kept, visible, dnsrecords.DefaultTTL, true, owner)

	touched := map[string]bool{}
	for _, name := range changes.Names {
		touched[fqdnOf(name, zoneName)] = true
	}

	current := map[string][]string{}
	proxied := map[string]bool{}
	pointsHere := map[string]bool{}
	for _, record := range records {
		if record.Type == "A" || record.Type == "AAAA" || record.Type == "CNAME" {
			current[record.Name] = append(current[record.Name], record.Type+" "+cleanText(record.Content, 80))
			proxied[record.Name] = proxied[record.Name] || record.Proxied
			pointsHere[record.Name] = pointsHere[record.Name] || (record.Type == "A" && address != "" && record.Content == address)
		}
	}

	newText := "A " + address
	if address == "" {
		newText = "A"
	}
	written := map[string]bool{}
	for _, host := range hosts {
		row := PreviewRow{Name: host, Role: "app", Current: strings.Join(current[host], ", "), Proxied: proxied[host], PointsHere: pointsHere[host]}
		if host == panel {
			row.Role = "panel"
		}
		relative := dnsrecords.RelativeName(host, zoneName)
		switch {
		case skipped[host] != "":
			row.Action = skipped[host]
		case !touched[host]:
			row.Action = "keep"
		case foreign[relative]:
			row.Action, row.New = "replace", newText
		case len(current[host]) > 0 && address == "":
			// an own record, and no address to compare it with yet
			row.Action = "keep"
		case len(current[host]) > 0:
			row.Action, row.New = "update", newText
		default:
			row.Action, row.New = "create", newText
		}
		if row.Action == "create" || row.Action == "update" || row.Action == "replace" {
			written[host] = true
		}
		preview.Rows = append(preview.Rows, row)
	}

	// own records of names Cosmos no longer serves go, as upstream decides
	served := map[string]bool{}
	for _, host := range hosts {
		served[host] = true
	}
	for name := range touched {
		if !served[name] {
			preview.Rows = append(preview.Rows, PreviewRow{Name: name, Role: "app", Action: "remove", Current: strings.Join(current[name], ", ")})
			written[name] = true
		}
	}

	hasWildcard := false
	caaIssue, caaAllows := false, false
	for _, record := range records {
		if record.Type == "TXT" && dnsrecords.OwnedName(dnsrecords.RelativeName(record.Name, zoneName)) != "" {
			continue // Cosmos' own markers
		}
		if strings.HasPrefix(record.Name, "*.") && (record.Type == "A" || record.Type == "AAAA" || record.Type == "CNAME") {
			hasWildcard = true
		}
		if record.Type == "CAA" && record.Name == zoneName {
			content := strings.ToLower(record.Content)
			if strings.Contains(content, "issue") {
				caaIssue = true
				if strings.Contains(content, "letsencrypt.org") {
					caaAllows = true
				}
			}
		}
		if written[record.Name] && (record.Type == "A" || record.Type == "CNAME") {
			continue
		}
		if len(preview.Untouched) >= maxUntouched {
			preview.UntouchedMore = true
			continue
		}
		preview.Untouched = append(preview.Untouched, ExistingRecord{
			Name:    record.Name,
			Type:    cleanText(record.Type, 10),
			Content: cleanText(record.Content, 80),
			Proxied: record.Proxied,
			Apex:    record.Name == zoneName,
		})
	}
	sort.SliceStable(preview.Untouched, func(i, j int) bool {
		a, b := preview.Untouched[i], preview.Untouched[j]
		if a.Apex != b.Apex {
			return a.Apex
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Type < b.Type
	})
	sort.SliceStable(preview.Rows, func(i, j int) bool {
		a, b := preview.Rows[i], preview.Rows[j]
		if (a.Role == "panel") != (b.Role == "panel") {
			return a.Role == "panel"
		}
		return a.Name < b.Name
	})

	preview.Apex = ApexState{Exists: len(current[zoneName]) > 0, Proxied: proxied[zoneName], PointsHere: pointsHere[zoneName]}

	if caaIssue && !caaAllows {
		preview.Warnings = append(preview.Warnings, "caa")
	}
	if hasWildcard {
		preview.Warnings = append(preview.Warnings, "wildcard")
	}
	if ip := net.ParseIP(address); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
		preview.Warnings = append(preview.Warnings, "private_address")
	}
	return preview
}

// MakePreview reads the zone at Cloudflare with the token and says what
// connecting it would write. Nothing is changed.
func MakePreview(ctx context.Context, token string, zoneName string, panelHost string, takeOver []string) (Preview, error) {
	client := NewClient(token)
	zones, _, err := client.ListZones(ctx, zoneName)
	if err != nil {
		return Preview{}, err
	}
	var zone *Zone
	for i := range zones {
		if zones[i].Name == zoneName {
			zone = &zones[i]
		}
	}
	if zone == nil {
		return Preview{}, &APIError{Kind: "not_found", Message: "the token does not see this domain"}
	}

	records, truncated, err := client.ListRecords(ctx, zone.ID, "")
	if err != nil {
		return Preview{}, err
	}

	config := utils.GetMainConfig()
	hosts, panel := servedUnder(config, zoneName, panelHost)
	allowed := append(append([]string{}, config.HTTPConfig.Cloudflare.TakeOver...), takeOver...)

	preview := buildPreview(zoneName, hosts, panel, records, knownAddress(), allowed, dnsrecords.OwnerID())
	preview.Truncated = truncated
	preview.CanEdit = client.CanEdit(ctx, zone.ID)
	if mode, err := client.SSLMode(ctx, zone.ID); err == nil {
		preview.SSLMode = mode
	}
	return preview, nil
}
