// Package dnsrecords keeps the public DNS records of the managed zones pointed
// at the servers: the DynDNS side of a zone. Certificates use lego, which can
// only write ACME TXT records, so records go through libdns with the same
// credentials the zone already holds for its DNS challenge.
package dnsrecords

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/libdns/cloudflare"
	"github.com/libdns/desec"
	"github.com/libdns/digitalocean"
	"github.com/libdns/duckdns"
	"github.com/libdns/gandi"
	"github.com/libdns/hetzner"
	"github.com/libdns/libdns"
	"github.com/libdns/namecheap"
	"github.com/libdns/ovh"
	"github.com/libdns/porkbun"
	"github.com/libdns/route53"

	"github.com/azukaar/cosmos-server/src/utils"
)

type Provider interface {
	libdns.RecordGetter
	libdns.RecordAppender
	libdns.RecordDeleter
}

type adapter struct {
	// build turns the lego credentials of the zone into a libdns provider
	build func(env func(keys ...string) string) (Provider, error)
	// noMarker: the provider cannot hold the owner TXT records next to the
	// address records (DuckDNS has a single TXT per domain, used by ACME)
	noMarker bool
	// minTTL when the provider refuses the default one
	minTTL time.Duration
	// exactZone: the provider knows the domain itself and nothing above it
	// (DuckDNS: name.duckdns.org is the whole account, duckdns.org is not ours)
	exactZone bool
}

// DefaultTTL keeps failover and IP changes quick; providers with a higher floor say so
const DefaultTTL = 60 * time.Second

// ZoneProvider is everything a reconcile pass needs to talk to the provider of a zone
type ZoneProvider struct {
	Provider  Provider
	UseMarker bool
	TTL       time.Duration
	// ExactZone: records are written in the domain itself, see findProviderZone
	// for the others
	ExactZone bool
}

// keyed by lego provider name, the one stored in DNSZoneConfig.DNSChallengeProvider
var adapters = map[string]adapter{
	"cloudflare": {build: func(env func(...string) string) (Provider, error) {
		token := env("CF_DNS_API_TOKEN", "CLOUDFLARE_DNS_API_TOKEN")
		if token == "" {
			return nil, errors.New("managing records needs a Cloudflare API token (CF_DNS_API_TOKEN), the global API key is not supported")
		}
		return &cloudflare.Provider{APIToken: token, ZoneToken: env("CF_ZONE_API_TOKEN", "CLOUDFLARE_ZONE_API_TOKEN")}, nil
	}},
	"route53": {build: func(env func(...string) string) (Provider, error) {
		return &route53.Provider{
			AccessKeyId:     env("AWS_ACCESS_KEY_ID"),
			SecretAccessKey: env("AWS_SECRET_ACCESS_KEY"),
			Region:          env("AWS_REGION"),
			Profile:         env("AWS_PROFILE"),
			HostedZoneID:    env("AWS_HOSTED_ZONE_ID"),
		}, nil
	}},
	"ovh": {build: func(env func(...string) string) (Provider, error) {
		if env("OVH_APPLICATION_KEY") == "" || env("OVH_CONSUMER_KEY") == "" {
			return nil, errors.New("managing records needs the OVH application key, secret and consumer key")
		}
		return &ovh.Provider{
			Endpoint:          env("OVH_ENDPOINT"),
			ApplicationKey:    env("OVH_APPLICATION_KEY"),
			ApplicationSecret: env("OVH_APPLICATION_SECRET"),
			ConsumerKey:       env("OVH_CONSUMER_KEY"),
		}, nil
	}},
	"gandiv5": {minTTL: 300 * time.Second, build: func(env func(...string) string) (Provider, error) {
		token := env("GANDIV5_PERSONAL_ACCESS_TOKEN")
		if token == "" {
			return nil, errors.New("managing records needs a Gandi personal access token (GANDIV5_PERSONAL_ACCESS_TOKEN), the legacy API key is not supported")
		}
		return &gandi.Provider{BearerToken: token}, nil
	}},
	"digitalocean": {build: func(env func(...string) string) (Provider, error) {
		return &digitalocean.Provider{APIToken: env("DO_AUTH_TOKEN")}, nil
	}},
	"hetzner": {build: func(env func(...string) string) (Provider, error) {
		return &hetzner.Provider{AuthAPIToken: env("HETZNER_API_TOKEN")}, nil
	}},
	"porkbun": {build: func(env func(...string) string) (Provider, error) {
		return &porkbun.Provider{APIKey: env("PORKBUN_API_KEY"), APISecretKey: env("PORKBUN_SECRET_API_KEY")}, nil
	}},
	"namecheap": {build: func(env func(...string) string) (Provider, error) {
		// the Namecheap API only answers a whitelisted client IP, and wants it in every call
		clientIP, err := DetectPublicIP()
		if err != nil {
			return nil, errors.New("managing Namecheap records needs this server's public IP: " + err.Error())
		}
		provider := &namecheap.Provider{APIKey: env("NAMECHEAP_API_KEY"), User: env("NAMECHEAP_API_USER"), ClientIP: clientIP}
		if env("NAMECHEAP_SANDBOX") == "true" {
			provider.APIEndpoint = "https://api.sandbox.namecheap.com/xml.response"
		}
		return provider, nil
	}},
	"desec": {minTTL: 3600 * time.Second, build: func(env func(...string) string) (Provider, error) {
		return &desec.Provider{Token: env("DESEC_TOKEN")}, nil
	}},
	"duckdns": {noMarker: true, exactZone: true, build: func(env func(...string) string) (Provider, error) {
		return &duckdns.Provider{APIToken: env("DUCKDNS_TOKEN")}, nil
	}},
}

func init() {
	utils.RecordsSupported = Supported
}

// Supported reports whether records can be managed through this lego provider
func Supported(legoProvider string) bool {
	_, ok := adapters[legoProvider]
	return ok
}

// SupportedProviders lists the lego provider names records can be managed through
func SupportedProviders() []string {
	names := make([]string, 0, len(adapters))
	for name := range adapters {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// New builds the record provider of a zone from its DNS challenge credentials
func New(zone utils.DNSZoneConfig) (ZoneProvider, error) {
	a, ok := adapters[zone.DNSChallengeProvider]
	if !ok {
		return ZoneProvider{}, errors.New("records cannot be managed through the DNS provider " + zone.DNSChallengeProvider)
	}

	env := func(keys ...string) string {
		for _, key := range keys {
			if value := strings.TrimSpace(zone.DNSChallengeConfig[key]); value != "" {
				return value
			}
		}
		return ""
	}

	provider, err := a.build(env)
	if err != nil {
		return ZoneProvider{}, err
	}

	ttl := DefaultTTL
	if a.minTTL > ttl {
		ttl = a.minTTL
	}
	return ZoneProvider{Provider: provider, UseMarker: !a.noMarker, TTL: ttl, ExactZone: a.exactZone}, nil
}
