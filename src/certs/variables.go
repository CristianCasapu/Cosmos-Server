package certs

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Variable is one setting a DNS provider takes, with lego's description.
type Variable struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

// Provider is a DNS provider lego can answer a challenge through.
type Provider struct {
	Name        string     `json:"name"`
	Credentials []Variable `json:"credentials"`
	Optional    []Variable `json:"optional"`
}

// providers.json is written by scripts/fork-lego-providers.go from lego's
// own description of each provider.
//
//go:embed providers.json
var providersJSON []byte

// nameservers.json says which provider a name server belongs to, by a part
// of its name. Data, not code: a wrong or missing line only costs a
// proposal.
//
//go:embed nameservers.json
var nameserversJSON []byte

var (
	catalogue   = map[string]Provider{}
	nameservers []struct {
		Match    string `json:"match"`
		Provider string `json:"provider"`
	}
)

func init() {
	if err := json.Unmarshal(providersJSON, &catalogue); err != nil {
		panic("certs: providers.json: " + err.Error())
	}
	if err := json.Unmarshal(nameserversJSON, &nameservers); err != nil {
		panic("certs: nameservers.json: " + err.Error())
	}
}

// Providers is the catalogue, by lego's code.
func Providers() map[string]Provider { return catalogue }

// neverFromAZone are variables lego declares that a domain may still not
// set: they move the address the provider's API is called at. For
// Cloudflare the token is kept on the server and shown as a mask; with the
// address movable, saving a domain again would hand the kept token to
// another host.
var neverFromAZone = map[string]bool{"CLOUDFLARE_BASE_URL": true, "CF_BASE_URL": true}

// allowedVariable reports whether a domain may hand the variable to its
// provider: lego declares it for that provider, and it is not one of the
// few that are refused.
func allowedVariable(provider string, name string) bool {
	entry, ok := catalogue[provider]
	if !ok || neverFromAZone[name] {
		return false
	}
	for _, v := range entry.Credentials {
		if v.Name == name {
			return true
		}
	}
	for _, v := range entry.Optional {
		if v.Name == name {
			return true
		}
	}
	return false
}

// providerVariables returns the variables of a domain its provider is
// given, and the names of those left out.
func providerVariables(zone utils.DNSZoneConfig) (map[string]string, []string) {
	given := map[string]string{}
	dropped := []string{}
	for name, value := range zone.DNSChallengeConfig {
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !allowedVariable(zone.DNSChallengeProvider, name) {
			dropped = append(dropped, name)
			continue
		}
		given[name] = value
	}
	sort.Strings(dropped)
	return given, dropped
}

// providerForNameservers proposes the provider of a domain from the names
// of its name servers, "" when none is known.
func providerForNameservers(servers []string) string {
	for _, server := range servers {
		host := "." + strings.TrimSuffix(strings.ToLower(strings.TrimSpace(server)), ".")
		for _, known := range nameservers {
			if strings.Contains(host, known.Match) {
				return known.Provider
			}
		}
	}
	return ""
}

// findZoneExact returns the configured domain of that exact name.
func findZoneExact(zones []utils.DNSZoneConfig, name string) (utils.DNSZoneConfig, bool) {
	name = normalName(name)
	for _, zone := range zones {
		if normalName(zone.Zone) == name {
			return zone, true
		}
	}
	return utils.DNSZoneConfig{}, false
}

func normalName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

// canAnswerFor reports whether a domain can answer the challenge of
// another: Let's Encrypt, a DNS provider of its own, not itself delegated.
func canAnswerFor(zone utils.DNSZoneConfig) bool {
	return zone.DNSChallengeProvider != "" && zone.ChallengeVia == "" &&
		(zone.HTTPSCertificateMode == "" || zone.HTTPSCertificateMode == utils.HTTPSCertModeList["LETSENCRYPT"])
}

// ValidateZone checks what this package adds to a domain before it is
// stored: the provider and its variables are ones lego knows, and a
// challenge answered in another domain names a domain that can. It returns
// a sentence for the admin, "" when all is well. zones is what is stored
// now. A variable that was stored before for the same provider is let
// through (it is left out when the order is made): an old odd value does
// not block saving something else.
func ValidateZone(zone *utils.DNSZoneConfig, zones []utils.DNSZoneConfig) string {
	zone.ChallengeVia = normalName(zone.ChallengeVia)
	stored, known := findZoneExact(zones, zone.Zone)

	if zone.DNSChallengeProvider != "" {
		if _, ok := catalogue[zone.DNSChallengeProvider]; !ok {
			return "Unknown DNS provider: " + clean(zone.DNSChallengeProvider, 40)
		}
		for name, value := range zone.DNSChallengeConfig {
			if strings.TrimSpace(value) == "" || allowedVariable(zone.DNSChallengeProvider, strings.TrimSpace(name)) {
				continue
			}
			if known && stored.DNSChallengeProvider == zone.DNSChallengeProvider {
				if _, was := stored.DNSChallengeConfig[name]; was {
					continue
				}
			}
			return "The DNS provider " + zone.DNSChallengeProvider + " has no setting named " + clean(name, 60)
		}
	}

	if zone.ChallengeVia == "" {
		return ""
	}
	if zone.DNSChallengeProvider != "" {
		return "A domain either has DNS access of its own or has its challenge answered in another domain, not both"
	}
	if zone.HTTPSCertificateMode != "" && zone.HTTPSCertificateMode != utils.HTTPSCertModeList["LETSENCRYPT"] {
		return "Only a Let's Encrypt domain has a challenge to answer in another domain"
	}
	if zone.ChallengeVia == normalName(zone.Zone) {
		return "A domain cannot answer its own challenge"
	}
	via, ok := findZoneExact(zones, zone.ChallengeVia)
	if !ok || !canAnswerFor(via) {
		return "The challenge can only be answered in a domain of this server that uses Let's Encrypt with DNS access"
	}
	for _, other := range zones {
		if normalName(other.ChallengeVia) == normalName(zone.Zone) && normalName(other.Zone) != normalName(zone.Zone) {
			return "Another domain has its challenge answered in this one: it needs DNS access of its own"
		}
	}
	return ""
}
