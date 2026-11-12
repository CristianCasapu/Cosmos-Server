package dnsrecords

import (
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/libdns/libdns"
)

// Desired maps a fully qualified name of the zone ("app.example.com",
// "example.com", "*.example.com") to the IPv4 addresses it must resolve to.
type Desired map[string][]string

const markerPrefix = "_cosmos-owner"
const markerText = "managed by cosmos"

// unknownOwner is the owner of a marker we cannot read: never ours
const unknownOwner = "?"

// markerValue is the text of an owner marker: the Cosmos server or cluster
// that wrote the records of the name. Without an owner to give, markers are
// the bare text they were before owners existed.
func markerValue(owner string) string {
	if owner == "" {
		return markerText
	}
	return markerText + " " + owner
}

// markerOwner reads the owner back, "" for a marker that carries none
func markerOwner(text string) string {
	text = strings.TrimSpace(strings.Trim(strings.TrimSpace(text), `"`))
	if text == markerText {
		return ""
	}
	if strings.HasPrefix(text, markerText+" ") {
		return strings.TrimSpace(text[len(markerText):])
	}
	return unknownOwner
}

// relativeName turns a fully qualified name into the zone-relative one libdns wants
func relativeName(fqdn string, zone string) string {
	fqdn = strings.TrimSuffix(strings.ToLower(fqdn), ".")
	zone = strings.TrimSuffix(strings.ToLower(zone), ".")
	if fqdn == zone {
		return "@"
	}
	return strings.TrimSuffix(fqdn, "."+zone)
}

// markerName is where the owner TXT record of a name lives. A wildcard cannot
// be a label in the middle of a name, so it is spelled out.
func markerName(relative string) string {
	if relative == "@" {
		return markerPrefix
	}
	if relative == "*" {
		return markerPrefix + "._wildcard"
	}
	if strings.HasPrefix(relative, "*.") {
		return markerPrefix + "._wildcard." + relative[2:]
	}
	return markerPrefix + "." + relative
}

// ownedName is the reverse of markerName, "" when the record is not a marker
func ownedName(marker string) string {
	if marker == markerPrefix {
		return "@"
	}
	if !strings.HasPrefix(marker, markerPrefix+".") {
		return ""
	}
	rest := marker[len(markerPrefix)+1:]
	if rest == "_wildcard" {
		return "*"
	}
	if strings.HasPrefix(rest, "_wildcard.") {
		return "*." + rest[len("_wildcard."):]
	}
	return rest
}

// Changes is what one reconcile pass has to write
//
// Address sets are written record by record, never through SetRecords: what
// SetRecords does with several records of one name differs between providers
// (the Cloudflare one keeps the last address only), appending and deleting
// single records does not.
type Changes struct {
	// Delete goes first: a CNAME has to leave before an address can take its
	// name, and the names nobody declares anymore
	Delete []libdns.Record
	// Append adds the missing addresses and owner markers
	Append []libdns.Record
	// Withdraw goes last, once the new addresses are in: the addresses a
	// declared name must not resolve to anymore. A set is never empty in between.
	Withdraw []libdns.Record
	// Names lists the zone-relative names touched, for logs and status
	Names []string
	// TakenOver lists the names that belonged to another Cosmos server or cluster
	TakenOver []string
}

func (c Changes) Empty() bool {
	return len(c.Append) == 0 && len(c.Delete) == 0 && len(c.Withdraw) == 0
}

// WildcardCovered drops the names already answered by the wildcard of the zone
// with the very same addresses: no record of their own is needed, and a new
// hostname works before any DNS write. A DNS wildcard covers every depth, but
// never the zone itself.
func WildcardCovered(desired Desired, zone string) Desired {
	zone = strings.TrimSuffix(strings.ToLower(zone), ".")
	wildcard, ok := desired["*."+zone]
	if !ok {
		return desired
	}

	out := Desired{}
	for name, ips := range desired {
		if name != "*."+zone && strings.HasSuffix(name, "."+zone) && sameIPs(ips, wildcard) {
			continue
		}
		out[name] = ips
	}
	return out
}

func sameIPs(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a = append([]string{}, a...)
	b = append([]string{}, b...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Plan compares the desired state of a zone with its actual records.
//
// Declaring a hostname in a managed zone is consent: whatever sits at that name
// (other addresses, a CNAME, the marker of another Cosmos) is replaced, and
// the name is ours from then on. Removal is the careful side: only names
// carrying the marker of this very owner are ever deleted, so records we never
// wrote are never lost, be they manual or the ones of another Cosmos sharing
// the zone. A name with no address to give keeps what it has: a set is never
// emptied.
//
// zone is the zone at the provider, which the domain may only be a part of:
// names are relative to it, and actual holds the records of the domain only.
func Plan(zone string, desired Desired, actual []libdns.Record, ttl time.Duration, useMarker bool, owner string) Changes {
	changes := Changes{}

	actualA := map[string][]string{}
	actualCNAME := map[string][]libdns.Record{}
	actualAByName := map[string][]libdns.Record{}
	// the markers of a name: ours, the ones written before owners existed, and
	// the ones of another Cosmos
	mine := map[string][]libdns.Record{}
	ownerless := map[string][]libdns.Record{}
	others := map[string][]libdns.Record{}

	for _, record := range actual {
		rr := record.RR()
		name := strings.ToLower(rr.Name)
		if name == "" {
			name = "@"
		}
		switch rr.Type {
		case "A":
			actualA[name] = append(actualA[name], rr.Data)
			actualAByName[name] = append(actualAByName[name], record)
		case "CNAME":
			actualCNAME[name] = append(actualCNAME[name], record)
		case "TXT":
			if owned := ownedName(name); owned != "" {
				switch markerOwner(rr.Data) {
				case owner:
					mine[owned] = append(mine[owned], record)
				case "":
					ownerless[owned] = append(ownerless[owned], record)
				default:
					others[owned] = append(others[owned], record)
				}
			}
		}
	}

	wanted := map[string]bool{}
	names := make([]string, 0, len(desired))
	for fqdn := range desired {
		names = append(names, fqdn)
	}
	sort.Strings(names)

	for _, fqdn := range names {
		ips := desired[fqdn]
		name := relativeName(fqdn, zone)
		wanted[name] = true

		if len(ips) == 0 {
			continue
		}

		touched := false

		if !sameIPs(ips, actualA[name]) {
			wantedIPs := map[string]bool{}
			for _, ip := range ips {
				wantedIPs[ip] = true
			}
			present := map[string]bool{}
			for _, record := range actualAByName[name] {
				ip := record.RR().Data
				if !wantedIPs[ip] || present[ip] {
					changes.Withdraw = append(changes.Withdraw, record)
				}
				present[ip] = true
			}

			sorted := append([]string{}, ips...)
			sort.Strings(sorted)
			for _, ip := range sorted {
				addr, err := netip.ParseAddr(ip)
				if err != nil || !addr.Is4() || present[ip] {
					continue
				}
				changes.Append = append(changes.Append, libdns.Address{Name: name, TTL: ttl, IP: addr})
			}
			touched = true
		}
		if len(actualCNAME[name]) > 0 {
			changes.Delete = append(changes.Delete, actualCNAME[name]...)
			touched = true
		}

		if useMarker {
			if len(mine[name]) == 0 {
				changes.Append = append(changes.Append, libdns.TXT{Name: markerName(name), TTL: ttl, Text: markerValue(owner)})
				touched = true
				if len(others[name]) > 0 {
					changes.TakenOver = append(changes.TakenOver, name)
				}
			}
			// one owner per name: the markers of the previous one go
			if len(ownerless[name])+len(others[name]) > 0 {
				changes.Delete = append(changes.Delete, ownerless[name]...)
				changes.Delete = append(changes.Delete, others[name]...)
				touched = true
			}
		}

		if touched {
			changes.Names = append(changes.Names, name)
		}
	}

	// names we own that nothing wants anymore
	owned := make([]string, 0, len(mine))
	for name := range mine {
		owned = append(owned, name)
	}
	sort.Strings(owned)

	for _, name := range owned {
		if wanted[name] {
			continue
		}
		// claimed by another Cosmos too: the addresses are theirs to keep, only
		// our claim goes
		if len(others[name]) == 0 {
			changes.Delete = append(changes.Delete, actualAByName[name]...)
		}
		changes.Delete = append(changes.Delete, mine[name]...)
		changes.Delete = append(changes.Delete, ownerless[name]...)
		changes.Names = append(changes.Names, name)
	}

	return changes
}
