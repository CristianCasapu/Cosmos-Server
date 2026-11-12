package dnsrecords

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/go-acme/lego/v5/challenge/dns01"
	"golang.org/x/net/publicsuffix"

	"github.com/azukaar/cosmos-server/src/utils"
)

// asked when the resolvers of the server have no answer
var publicResolvers = []string{"1.1.1.1:53", "8.8.8.8:53"}

// lookupZone finds the DNS zone a name lives in: its closest parent with a SOA
// record, the way lego finds where to write the DNS challenge. resolvers is
// empty for the ones of the server.
var lookupZone = func(name string, resolvers []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// a client of our own: the default one belongs to the certificates, which
	// set it up for the zone they are issuing
	client := dns01.NewClient(&dns01.Options{RecursiveNameservers: resolvers, Timeout: 5 * time.Second})
	found, err := client.FindZoneByFqdn(ctx, name+".")
	if err != nil {
		return "", err
	}
	return zoneName(found), nil
}

func zoneName(zone string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(zone)), ".")
}

// registryZone reports whether a zone is the one of a registry (com, co.uk):
// what the lookup of a domain that is not delegated yet walks up to
func registryZone(found string, name string) bool {
	if !strings.Contains(found, ".") {
		return true
	}
	suffix, icann := publicsuffix.PublicSuffix(name)
	return icann && (found == suffix || strings.HasSuffix(suffix, "."+found))
}

// findProviderZone is the zone the records of a domain go to at its provider.
// A domain is not always a zone of its own there: cluster.example.com usually
// lives in the zone example.com, under the names *.cluster.
//
// When no zone can be found the domain itself is returned along with the
// reason: the best guess, and what was always used before.
func findProviderZone(zone utils.DNSZoneConfig) (string, error) {
	name := zoneName(zone.Zone)

	attempts := [][]string{nil, publicResolvers}
	custom := []string{}
	for _, resolver := range strings.Split(zone.DNSChallengeResolvers, ",") {
		if resolver = strings.TrimSpace(resolver); resolver != "" {
			custom = append(custom, resolver)
		}
	}
	if len(custom) > 0 {
		attempts = [][]string{custom}
	}

	var reason error
	for _, resolvers := range attempts {
		found, err := lookupZone(name, resolvers)
		if err != nil {
			reason = err
			continue
		}
		if found == name || (strings.HasSuffix(name, "."+found) && !registryZone(found, name)) {
			return found, nil
		}
		reason = errors.New("no DNS zone is delegated for " + name + " yet")
	}
	return name, reason
}
