package dnsrecords

// Fork addition: the four places where the fork's Cloudflare integration
// (src/cloudflare) takes part in a reconcile pass. That package imports this
// one, so it fills in these variables; until it does, and for every provider
// it does not handle, they change nothing.

import (
	"time"

	"github.com/libdns/libdns"

	"github.com/azukaar/cosmos-server/src/utils"
)

// ForkWrapProvider may wrap the record provider of a zone. Hook of New.
var ForkWrapProvider = func(zone utils.DNSZoneConfig, provider Provider) Provider { return provider }

// ForkFilterDesired may take names out of a pass: it returns what the pass
// may write (desired) and the records it may see (actual, as read from the
// provider for the domain). A name missing from both is neither written nor
// deleted, whatever markers it carries. Hook of Reconcile, before Plan.
var ForkFilterDesired = func(zone utils.DNSZoneConfig, providerZone string, desired Desired, actual []libdns.Record) (Desired, []libdns.Record) {
	return desired, actual
}

// ForkMoreHosts may add names to those a pass publishes, each with the
// addresses it must resolve to: the DNS-only names of ports exposed directly
// (src/cloudflare/expose.go). Hook of pass, after HostAddresses.
var ForkMoreHosts = func(hosts map[string][]string) map[string][]string { return hosts }

// ForkAfterReconcile is told that a pass ended well: the records of desired
// are in place at the provider. wrote says whether the pass changed anything.
var ForkAfterReconcile = func(zone utils.DNSZoneConfig, providerZone string, desired Desired, wrote bool) {}

// OwnedName tells which name a record name is the owner marker of ("@" for
// the zone itself), "" when it is not a marker.
func OwnedName(recordName string) string { return ownedName(recordName) }

// MarkerOwner reads the owner out of the text of a marker: "" for a marker
// written before owners existed, "?" for one that cannot be read.
func MarkerOwner(text string) string { return markerOwner(text) }

// RelativeName turns a full name into the one relative to its zone ("@" for
// the zone itself).
func RelativeName(fqdn string, zone string) string { return relativeName(fqdn, zone) }

// ForkTestSetup is for the tests of the fork's other packages, which cannot
// reach what is private here: passes then look no zone up in DNS (lookup
// answers instead), start from nothing and are past the warm-up of a writer
// that just started. The returned function puts everything back.
func ForkTestSetup(lookup func(name string, resolvers []string) (string, error)) (restore func()) {
	stateLock.Lock()
	previousLookup, previousSince, previousStates := lookupZone, writerSince, zoneStates
	lookupZone = lookup
	writerSince = time.Now().Add(-2 * warmUp)
	zoneStates = map[string]*zoneState{}
	stateLock.Unlock()

	return func() {
		stateLock.Lock()
		lookupZone, writerSince, zoneStates = previousLookup, previousSince, previousStates
		stateLock.Unlock()
	}
}
