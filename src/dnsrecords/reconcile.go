package dnsrecords

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libdns/libdns"

	"github.com/azukaar/cosmos-server/src/utils"
)

// records are re-read from the provider this often to catch outside edits. It
// is the only part of a pass that costs API quota when nothing changes, and it
// plays no part in how fast a change of ours goes out.
const driftReadInterval = 10 * time.Minute

// a writer that just started (boot, leadership gained) has an incomplete view
// of the cluster for a while: it only adds, never removes
const warmUp = 3 * time.Minute

// ZoneStatus is the DynDNS state of a zone, for display
type ZoneStatus struct {
	Supported bool                `json:"supported"`
	LastSync  time.Time           `json:"lastSync"`
	LastError string              `json:"lastError,omitempty"`
	Records   map[string][]string `json:"records"`
}

// a provider that keeps failing is asked again after longer and longer breaks.
// The failure after the last of these delays is reported to the admins, and
// from then on the zone is only tried once in a while: a wrong token does not
// fix itself. Saving the domain, or a change in the records to publish, tries
// again right away.
var retryDelays = []time.Duration{time.Minute, 5 * time.Minute}

const givenUpRetryDelay = 24 * time.Hour

// swapped out by the tests
var majorError = utils.MajorError

type zoneState struct {
	// settings of the domain the state was built for: saving it starts over
	settings string
	// providerZone is where the records of the domain live, once looked up
	providerZone string

	actual     []libdns.Record
	actualZone string
	actualRead time.Time
	status     ZoneStatus

	// failures in a row, when the next try is due, and the records the last
	// one was for
	failures  int
	nextTry   time.Time
	attempted string
}

var stateLock sync.Mutex
var zoneStates = map[string]*zoneState{}
var writerSince time.Time

// GetStatus returns the DynDNS state of every zone reconciled by this node
func GetStatus() map[string]ZoneStatus {
	stateLock.Lock()
	defer stateLock.Unlock()

	out := map[string]ZoneStatus{}
	for zone, state := range zoneStates {
		out[zone] = state.status
	}
	return out
}

func isPrivate(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed == nil || parsed.IsPrivate() || parsed.IsLoopback() || parsed.IsLinkLocalUnicast()
}

// sameScope keeps a record set from mixing public and private addresses: a
// resolver handing out both sends part of the visitors to an address they
// cannot reach. Public wins.
func sameScope(name string, ips []string) []string {
	public := []string{}
	for _, ip := range ips {
		if !isPrivate(ip) {
			public = append(public, ip)
		}
	}
	if len(public) == 0 || len(public) == len(ips) {
		return ips
	}
	utils.Warn("[DynDNS] " + name + " is served from both public and private addresses, only the public ones are published")
	return public
}

// BuildDesired is the record set a zone must hold, from the addresses of every
// hostname served (hostAddresses) and where its wildcard goes (wildcardAddresses).
func BuildDesired(zones []utils.DNSZoneConfig, index int, hostAddresses map[string][]string, wildcardAddresses []string) Desired {
	zone := zones[index]
	name := strings.ToLower(strings.TrimSuffix(zone.Zone, "."))
	desired := Desired{}

	for host, ips := range hostAddresses {
		// longest suffix wins: the hostnames of a sub-zone are not ours
		if utils.FindZone(zones, host) != index {
			continue
		}
		desired[strings.ToLower(host)] = sameScope(host, ips)
	}

	if zone.WildcardRecord && len(wildcardAddresses) > 0 {
		desired["*."+name] = sameScope("*."+name, wildcardAddresses)
	}

	return WildcardCovered(desired, name)
}

// LimitWithdrawals slows down the shrinking of multi-address sets: a pass takes
// out at most half of the addresses a name currently has. A wrong view of the
// cluster then needs several passes in a row to do real damage, a right one
// still converges in a couple of passes.
func LimitWithdrawals(desired Desired, zone string, actual []libdns.Record) Desired {
	actualA := map[string][]string{}
	for _, record := range actual {
		if rr := record.RR(); rr.Type == "A" {
			actualA[strings.ToLower(rr.Name)] = append(actualA[strings.ToLower(rr.Name)], rr.Data)
		}
	}

	out := Desired{}
	for fqdn, ips := range desired {
		out[fqdn] = ips

		current := actualA[relativeName(fqdn, zone)]
		if len(current) < 2 || len(ips) == 0 {
			continue
		}

		wanted := map[string]bool{}
		for _, ip := range ips {
			wanted[ip] = true
		}
		leaving := []string{}
		for _, ip := range current {
			if !wanted[ip] {
				leaving = append(leaving, ip)
			}
		}

		allowed := len(current) / 2
		if len(leaving) <= allowed {
			continue
		}
		sort.Strings(leaving)
		out[fqdn] = append(append([]string{}, ips...), leaving[allowed:]...)
	}
	return out
}

// additiveOnly is the warm-up mode: keep every address a name already has
func additiveOnly(desired Desired, zone string, actual []libdns.Record) Desired {
	out := Desired{}
	for fqdn, ips := range desired {
		out[fqdn] = append([]string{}, ips...)
	}
	for _, record := range actual {
		rr := record.RR()
		if rr.Type != "A" {
			continue
		}
		for fqdn := range out {
			if relativeName(fqdn, zone) == strings.ToLower(rr.Name) {
				found := false
				for _, ip := range out[fqdn] {
					found = found || ip == rr.Data
				}
				if !found {
					out[fqdn] = append(out[fqdn], rr.Data)
				}
			}
		}
	}
	return out
}

// scopeRecords keeps the records of a provider zone that belong to one domain.
// The zone can hold more than the domain: the rest of example.com around
// cluster.example.com, or another domain of ours filed in the same zone. Each
// name goes to the most specific domain, like the hostnames of BuildDesired, so
// two domains sharing a zone never touch the records of each other.
func scopeRecords(actual []libdns.Record, providerZone string, zones []utils.DNSZoneConfig, index int) []libdns.Record {
	out := []libdns.Record{}
	for _, record := range actual {
		rr := record.RR()
		name := strings.ToLower(rr.Name)
		if name == "" {
			name = "@"
		}
		// a marker follows the name it is the marker of
		if owned := ownedName(name); rr.Type == "TXT" && owned != "" {
			name = owned
		}

		fqdn := providerZone
		if name != "@" {
			fqdn = strings.TrimSuffix(name, ".") + "." + providerZone
		}
		if utils.FindZone(zones, fqdn) == index {
			out = append(out, record)
		}
	}
	return out
}

func settingsOf(zone utils.DNSZoneConfig) string {
	settings, _ := json.Marshal(zone)
	return string(settings)
}

func signature(desired Desired) string {
	names := make([]string, 0, len(desired))
	for name, ips := range desired {
		ips = append([]string{}, ips...)
		sort.Strings(ips)
		names = append(names, name+"="+strings.Join(ips, ","))
	}
	sort.Strings(names)
	return strings.Join(names, " ")
}

// Reconcile brings the records of a domain to the desired state. It talks to
// the provider only when something has to change, or to re-read the records
// once in a while.
func Reconcile(zones []utils.DNSZoneConfig, index int, desired Desired) {
	zone := zones[index]
	settings := settingsOf(zone)
	wanted := signature(desired)

	stateLock.Lock()
	state, ok := zoneStates[zone.Zone]
	if !ok || state.settings != settings {
		state = &zoneState{settings: settings}
		state.status = ZoneStatus{Supported: Supported(zone.DNSChallengeProvider), Records: map[string][]string{}}
		zoneStates[zone.Zone] = state
	}
	if writerSince.IsZero() {
		writerSince = time.Now()
	}
	warmingUp := time.Since(writerSince) < warmUp
	waiting := state.failures > 0 && time.Now().Before(state.nextTry) && state.attempted == wanted
	stateLock.Unlock()

	if waiting {
		return
	}

	zoneProvider, err := New(zone)
	if err != nil {
		stateLock.Lock()
		state.status = ZoneStatus{Supported: Supported(zone.DNSChallengeProvider), LastError: err.Error(), Records: map[string][]string{}}
		stateLock.Unlock()
		return
	}

	providerZone := state.providerZone
	var lookupErr error
	if providerZone == "" {
		providerZone = zoneName(zone.Zone)
		if !zoneProvider.ExactZone {
			providerZone, lookupErr = findProviderZone(zone)
		}
		if lookupErr == nil {
			state.providerZone = providerZone
		}
	}
	libdnsZone := providerZone + "."

	fail := func(err error) {
		// where it went wrong: the zone is not always the domain
		message := zone.DNSChallengeProvider + ", DNS zone " + providerZone + ": " + err.Error()
		if lookupErr != nil {
			message += " (the DNS zone of this domain could not be looked up: " + lookupErr.Error() + ")"
		}
		err = errors.New(message)

		stateLock.Lock()
		state.failures++
		failures := state.failures
		delay := givenUpRetryDelay
		if failures <= len(retryDelays) {
			delay = retryDelays[failures-1]
		}
		state.nextTry = time.Now().Add(delay)
		state.attempted = wanted
		state.status.LastError = err.Error()
		stateLock.Unlock()

		if failures == len(retryDelays)+1 {
			majorError("[DynDNS] "+zone.Zone+": the DNS records could not be updated "+strconv.Itoa(failures)+" times in a row. Trying again once a day, or as soon as the domain is saved", err)
		} else {
			utils.Error("[DynDNS] "+zone.Zone, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if state.actual == nil || state.actualZone != providerZone || time.Since(state.actualRead) > driftReadInterval {
		actual, err := zoneProvider.Provider.GetRecords(ctx, libdnsZone)
		if err != nil {
			fail(err)
			return
		}
		if actual == nil {
			actual = []libdns.Record{}
		}
		state.actual = actual
		state.actualZone = providerZone
		state.actualRead = time.Now()
	}

	// from here on only the records of the domain exist
	actual := scopeRecords(state.actual, providerZone, zones, index)

	if warmingUp {
		desired = additiveOnly(desired, providerZone, actual)
	} else {
		desired = LimitWithdrawals(desired, providerZone, actual)
	}

	changes := Plan(providerZone, desired, actual, zoneProvider.TTL, zoneProvider.UseMarker, OwnerID())
	if warmingUp {
		// the names nobody declared yet may just not have been heard of yet
		kept := changes.Delete[:0]
		for _, record := range changes.Delete {
			if record.RR().Type == "CNAME" {
				kept = append(kept, record)
			}
		}
		changes.Delete = kept
	}

	if !changes.Empty() {
		utils.Log("[DynDNS] " + zone.Zone + ": updating " + strings.Join(changes.Names, ", ") + " in the DNS zone " + providerZone)
		if len(changes.TakenOver) > 0 {
			utils.Warn("[DynDNS] " + zone.Zone + ": taking " + strings.Join(changes.TakenOver, ", ") + " over from another Cosmos server or cluster")
		}

		// a CNAME has to go before an address can take its name
		if len(changes.Delete) > 0 {
			if _, err := zoneProvider.Provider.DeleteRecords(ctx, libdnsZone, changes.Delete); err != nil {
				state.actual = nil
				fail(err)
				return
			}
		}
		if len(changes.Append) > 0 {
			if _, err := zoneProvider.Provider.AppendRecords(ctx, libdnsZone, changes.Append); err != nil {
				state.actual = nil
				fail(err)
				return
			}
		}
		if len(changes.Withdraw) > 0 {
			if _, err := zoneProvider.Provider.DeleteRecords(ctx, libdnsZone, changes.Withdraw); err != nil {
				state.actual = nil
				fail(err)
				return
			}
		}

		// read back on the next pass rather than guessing what the provider stored
		state.actual = nil

		utils.TriggerEvent(
			"cosmos.dns.records",
			"DNS records updated",
			"success",
			"zone@"+zone.Zone,
			map[string]interface{}{
				"zone":  zone.Zone,
				"names": changes.Names,
			})
	}

	stateLock.Lock()
	state.failures = 0
	state.nextTry = time.Time{}
	state.attempted = ""
	state.status = ZoneStatus{Supported: true, LastSync: time.Now(), Records: desired}
	stateLock.Unlock()
}

// ResetWriter forgets everything learned as the writer: the next pass starts a
// warm-up and re-reads every zone. Called when this node stops being the writer.
func ResetWriter() {
	stateLock.Lock()
	defer stateLock.Unlock()
	writerSince = time.Time{}
	zoneStates = map[string]*zoneState{}
}
