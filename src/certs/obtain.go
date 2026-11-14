package certs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v5/acme"
	"github.com/go-acme/lego/v5/acme/api"
	"github.com/go-acme/lego/v5/certcrypto"
	"github.com/go-acme/lego/v5/certificate"
	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/challenge/dns01"
	"github.com/go-acme/lego/v5/challenge/http01"
	"github.com/go-acme/lego/v5/challenge/tlsalpn01"
	"github.com/go-acme/lego/v5/lego"
	"github.com/go-acme/lego/v5/providers/dns"
	legocloudflare "github.com/go-acme/lego/v5/providers/dns/cloudflare"
	"github.com/go-acme/lego/v5/registration"

	"github.com/azukaar/cosmos-server/src/utils"
)

// One order at a time: the provider of a domain may need its variables in
// the process environment, and the DNS client of the ACME library is one
// for the process.
var orderLock sync.Mutex

// running: the certificates being ordered right now, key -> start.
var running sync.Map

// longestOrder bounds one order, whatever the CA or the DNS provider do.
const longestOrder = 10 * time.Minute

// clock is the time, for the tests.
var clock = time.Now

// issued is what an order gave.
type issued struct {
	cert string
	key  string
	leaf *x509.Certificate
}

// obtainHook is utils.ForkObtain: upstream's callers ask for a certificate
// for these names (zone: through its DNS provider; nil: over HTTP-01 and
// TLS-ALPN-01) and get the certificate and its key, or two empty texts.
func obtainHook(domains []string, zone *utils.DNSZoneConfig) (string, string) {
	if len(domains) == 0 {
		return "", ""
	}
	config := utils.GetMainConfig()
	key := ""
	if zone != nil {
		key = utils.ForkZoneCertKey(zone.Zone)
	} else {
		key = utils.ForkLocalCertKey(utils.LocalCertZone(config, domains[0]))
	}

	orderLock.Lock()
	defer orderLock.Unlock()

	now := clock()
	// The callers asked before (utils.ForkMayOrder). Asked again here, so
	// that no path reaches the CA around what the CA itself asked for or
	// around the bounds per day and week.
	if ok, why, until := store.mayOrder(key, domains, true, now); !ok {
		store.unforce(key)
		message := "No order for " + key + " before " + until.UTC().Format("2006-01-02 15:04 UTC") + " (" + why + ")"
		utils.Warn("Certificates: " + message)
		utils.LetsEncryptErrors = append(utils.LetsEncryptErrors, message)
		return "", ""
	}

	running.Store(key, now)
	defer running.Delete(key)

	ca := Current()
	ctx, cancel := context.WithTimeout(context.Background(), longestOrder)
	defer cancel()

	result, failure := order(ctx, ca, key, domains, zone, config)
	serial := ""
	if result != nil && result.leaf != nil {
		serial = result.leaf.SerialNumber.String()
	}
	store.finished(key, clock(), ca, domains, serial, failure)
	if failure != nil {
		reason := FailureSentence(failure.Code)
		if failure.Detail != "" {
			reason += " (" + failure.Detail + ")"
		}
		utils.Error("Certificates: no certificate for "+strings.Join(domains, ", ")+" from "+ca.Name+": "+reason, nil)
		utils.LetsEncryptErrors = append(utils.LetsEncryptErrors, reason)
		return "", ""
	}
	utils.Log("Certificates: " + ca.Name + " issued a certificate for " + strings.Join(domains, ", ") +
		", valid until " + result.leaf.NotAfter.UTC().Format("2006-01-02 15:04 UTC"))
	return result.cert, result.key
}

// FailureSentence says a failure code in plain English, for the log and
// the notices. The panel has its own, translated, with what to do.
func FailureSentence(code string) string {
	switch code {
	case FailRateLimited:
		return "The certificate authority asked to wait before ordering again"
	case FailCAUnreachable:
		return "The certificate authority could not be reached or had a problem of its own"
	case FailCARefused:
		return "The certificate authority refused the request"
	case FailAccount:
		return "The certificate authority did not accept this server's account"
	case FailDNSCredentials:
		return "The DNS provider refused the credentials"
	case FailDNSZone:
		return "The DNS provider does not have this domain under the credentials given"
	case FailDNSProvider:
		return "The DNS provider could not create the record"
	case FailDNSPropagation:
		return "The record was created but did not show up in the DNS in time"
	case FailValidation:
		return "The certificate authority did not find the record it asked for"
	case FailCAA:
		return "A CAA record of the domain forbids this certificate authority"
	case FailDelegationMissing:
		return "The record that hands the challenge to another domain is missing"
	case FailNoProvider:
		return "Nothing is set up to answer the challenge for this domain"
	case FailTimeout:
		return "The order took too long and was stopped"
	}
	return "The order failed"
}

// access is how the DNS challenge of a domain is answered.
type access struct {
	// zone holds the provider, its variables and the DNS options used
	zone utils.DNSZoneConfig
	// delegated: zone is another domain than the one being certified
	delegated bool
}

// challengeAccess finds the DNS access for a domain: its own, or the one
// of the domain its challenge is delegated to.
func challengeAccess(zones []utils.DNSZoneConfig, zone utils.DNSZoneConfig) (access, *Failure) {
	if zone.ChallengeVia == "" {
		if zone.DNSChallengeProvider == "" {
			return access{}, &Failure{Code: FailNoProvider, Detail: "the domain has no DNS provider"}
		}
		return access{zone: zone}, nil
	}
	via, ok := findZoneExact(zones, zone.ChallengeVia)
	if !ok || !canAnswerFor(via) {
		return access{}, &Failure{Code: FailNoProvider, Detail: "the challenge is handed to " + clean(zone.ChallengeVia, 100) + ", which is not a domain of this server with DNS access"}
	}
	// the options an admin set on the domain itself win over the other domain's
	if zone.DNSChallengeResolvers != "" {
		via.DNSChallengeResolvers = zone.DNSChallengeResolvers
	}
	if zone.DNSChallengePropagationWait > 0 {
		via.DNSChallengePropagationWait = zone.DNSChallengePropagationWait
	}
	via.DisablePropagationChecks = via.DisablePropagationChecks || zone.DisablePropagationChecks
	return access{zone: via, delegated: true}, nil
}

// dnsProvider builds lego's provider for a domain's DNS access. It returns
// the secrets that must stay out of every message, and a function to call
// when the order is over.
func dnsProvider(zone utils.DNSZoneConfig) (challenge.Provider, []string, func(), error) {
	variables, dropped := providerVariables(zone)
	if len(dropped) > 0 {
		utils.Warn("Certificates: settings of " + zone.Zone + " that the DNS provider " + clean(zone.DNSChallengeProvider, 40) +
			" does not have are left out: " + clean(strings.Join(dropped, ", "), 200))
	}
	secrets := []string{}
	for _, value := range variables {
		secrets = append(secrets, value)
	}
	if _, known := catalogue[zone.DNSChallengeProvider]; !known {
		return nil, secrets, func() {}, errors.New("unknown DNS provider")
	}

	first := func(names ...string) string {
		for _, name := range names {
			if variables[name] != "" {
				return variables[name]
			}
		}
		return ""
	}

	// Cloudflare with an API token: built directly, the token never enters
	// the environment and the address of the API cannot be changed.
	if token := first("CF_DNS_API_TOKEN", "CLOUDFLARE_DNS_API_TOKEN"); zone.DNSChallengeProvider == "cloudflare" && token != "" {
		config := legocloudflare.NewDefaultConfig()
		config.AuthToken = token
		config.ZoneToken = first("CF_ZONE_API_TOKEN", "CLOUDFLARE_ZONE_API_TOKEN")
		config.BaseURL = ""
		if seconds, err := strconv.Atoi(first("CLOUDFLARE_PROPAGATION_TIMEOUT")); err == nil && seconds > 0 && seconds <= 3600 {
			config.PropagationTimeout = time.Duration(seconds) * time.Second
		}
		if seconds, err := strconv.Atoi(first("CLOUDFLARE_POLLING_INTERVAL")); err == nil && seconds > 0 && seconds <= 300 {
			config.PollingInterval = time.Duration(seconds) * time.Second
		}
		if ttl, err := strconv.Atoi(first("CLOUDFLARE_TTL")); err == nil && ttl >= 120 && ttl <= 86400 {
			config.TTL = ttl
		}
		provider, err := legocloudflare.NewDNSProviderConfig(config)
		return provider, secrets, func() {}, err
	}

	// Every other provider reads its settings from the environment: only
	// the ones lego declares for it are put there, for the time of the
	// order, and what was there before is put back.
	before := map[string]*string{}
	for name, value := range variables {
		if old, was := os.LookupEnv(name); was {
			before[name] = &old
		} else {
			before[name] = nil
		}
		os.Setenv(name, value)
	}
	restore := func() {
		for name, old := range before {
			if old == nil {
				os.Unsetenv(name)
			} else {
				os.Setenv(name, *old)
			}
		}
	}
	provider, err := dns.NewDNSChallengeProviderByName(zone.DNSChallengeProvider)
	return provider, secrets, restore, err
}

// lookupNS is net.LookupNS, for the tests.
var lookupNS = net.LookupNS

// challengeResolvers lists the name servers the ACME library asks while it
// answers a DNS challenge, as upstream chose them: the ones set for the
// domain, else the domain's own name servers and two public resolvers.
func challengeResolvers(setting string, domain string) []string {
	resolvers := []string{}
	if strings.TrimSpace(setting) != "" {
		for _, resolver := range strings.Split(setting, ",") {
			if resolver = strings.TrimSpace(resolver); resolver != "" {
				resolvers = append(resolvers, resolver)
			}
		}
		return resolvers
	}
	if servers, err := lookupNS(domain); err == nil {
		for _, server := range servers {
			resolvers = append(resolvers, server.Host)
		}
	}
	return append(resolvers, "8.8.8.8", "1.1.1.1")
}

// acmeHTTPClient is the client the ACME library talks to the CA with. For
// the test authority it trusts the given certificates and nothing else.
func acmeHTTPClient(ca CA) *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if ca.roots != nil {
		transport.TLSClientConfig.RootCAs = ca.roots
	}
	return &http.Client{Timeout: 2 * time.Minute, Transport: transport}
}

// acmeClient returns the ACME library's client for the CA with this
// server's account, registering it when there is none yet.
func acmeClient(ctx context.Context, ca CA, email string) (*lego.Client, *Failure) {
	account := loadAccount(ca)
	user := &acmeUser{email: email}
	keyPEM := ""
	if account != nil {
		key, err := account.PrivateKey()
		if err != nil {
			return nil, &Failure{Code: FailAccount, Detail: "the account key cannot be read"}
		}
		user.key = key
		user.registration = &acme.ExtendedAccount{Location: account.URL}
	} else {
		key, encoded, err := utils.NewACMEAccountKey()
		if err != nil {
			return nil, &Failure{Code: FailOther, Detail: clean(err.Error(), maxDetail)}
		}
		user.key = key
		keyPEM = encoded
	}

	config := lego.NewConfig(user)
	config.CADirURL = ca.Directory
	config.HTTPClient = acmeHTTPClient(ca)

	client, err := lego.NewClient(config)
	if err != nil {
		return nil, classify(stageDirectory, err, nil)
	}

	if account == nil {
		reg, err := client.Registration.Register(ctx, registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return nil, classify(stageAccount, err, nil)
		}
		user.registration = reg
		if err := saveAccount(ca, utils.ACMEAccount{Directory: ca.Directory, URL: reg.Location, Email: email, Key: keyPEM}); err != nil {
			utils.Warn("Certificates: the account could not be saved, the next order will register a new one: " + clean(err.Error(), 200))
		} else {
			utils.Log("Certificates: registered this server's account at " + ca.Name)
		}
	}
	return client, nil
}

// order runs one order. Nothing here decides whether to order: the callers
// did. It contacts the CA only once everything it can check by itself is
// in place.
func order(ctx context.Context, ca CA, key string, domains []string, zone *utils.DNSZoneConfig, config utils.Config) (*issued, *Failure) {
	names := utils.LetsEncryptValidOnly(domains, zone != nil)
	if len(names) == 0 {
		return nil, &Failure{Code: FailCARefused, Detail: "none of the names is one a public certificate authority can certify"}
	}

	var provider challenge.Provider
	var secrets []string
	var how access
	if zone != nil {
		found, failure := challengeAccess(config.HTTPConfig.DNSZones, *zone)
		if failure != nil {
			return nil, failure
		}
		how = found
		if how.delegated {
			if failure := delegationReady(ctx, names, how.zone.Zone); failure != nil {
				return nil, failure
			}
		}
		built, hidden, done, err := dnsProvider(how.zone)
		defer done()
		secrets = hidden
		if err != nil {
			return nil, classify(stageProvider, err, secrets)
		}
		provider = built
	}

	// from here on the CA is contacted: the attempt counts
	store.started(key, clock())

	client, failure := acmeClient(ctx, ca, config.HTTPConfig.SSLEmail)
	if failure != nil {
		return nil, failure
	}

	if zone != nil {
		dns01.SetDefaultClient(dns01.NewClient(&dns01.Options{
			RecursiveNameservers: challengeResolvers(how.zone.DNSChallengeResolvers, zone.Zone),
		}))
		wait := how.zone.DNSChallengePropagationWait
		if wait <= 0 {
			wait = 30
		}
		// The record is looked for on the domain's own name servers, which
		// is where the CA's resolvers end up. Not on the public resolvers of
		// the list above as well (the library's default): the library asks
		// them for the challenge name before the record exists, they
		// remember "no such name" for as long as the domain says (half an
		// hour at Cloudflare), and the check would wait for them in vain.
		err := client.Challenge.SetDNS01Provider(provider,
			dns01.DisableRecursiveNSsPropagationRequirement(),
			dns01.CondOptions(how.zone.DisablePropagationChecks,
				dns01.PropagationWait(time.Duration(wait)*time.Second, true)),
		)
		if err != nil {
			return nil, classify(stageProvider, err, secrets)
		}
	} else {
		if err := client.Challenge.SetHTTP01Provider(http01.NewProviderServer("", config.HTTPConfig.HTTPPort)); err != nil {
			return nil, classify(stageProvider, err, nil)
		}
		if err := client.Challenge.SetTLSALPN01Provider(tlsalpn01.NewProviderServer("", config.HTTPConfig.HTTPSPort)); err != nil {
			return nil, classify(stageProvider, err, nil)
		}
	}

	request := certificate.ObtainRequest{
		Domains: names,
		Bundle:  true,
		KeyType: certcrypto.EC256,
	}
	// a renewal names the certificate it replaces, when the CA issued that
	// one: Let's Encrypt then counts it against no limit (RFC 9773). The
	// library leaves it out for a CA without renewal information.
	if previous := previousLeaf(config, key); previous != nil {
		if known := store.get(key); known.IssuedDirectory == ca.Directory && known.IssuedSerial == previous.SerialNumber.String() {
			if id, err := api.MakeARICertID(previous); err == nil {
				request.ReplacesCertID = id
			}
		}
	}

	resource, err := client.Certificate.Obtain(ctx, request)
	if err != nil {
		failure := classify(stageOrder, err, secrets)
		if failure.Code == FailAccount {
			// the CA no longer knows the account: the next order registers anew
			forgetAccount(ca)
		}
		return nil, failure
	}

	leaf := utils.ForkLeaf(string(resource.Certificate))
	if leaf == nil {
		return nil, &Failure{Code: FailOther, Detail: "the certificate authority's answer holds no certificate"}
	}
	return &issued{cert: string(resource.Certificate), key: string(resource.PrivateKey), leaf: leaf}, nil
}

// previousLeaf is the certificate an order replaces, nil when there is none.
func previousLeaf(config utils.Config, key string) *x509.Certificate {
	kind, name, _ := strings.Cut(key, ":")
	var cert utils.ZoneCert
	var ok bool
	if kind == "zone" {
		for zone, stored := range config.HTTPConfig.ZoneCerts {
			if normalName(zone) == name {
				cert, ok = stored, true
			}
		}
	} else {
		for zone, stored := range config.HTTPConfig.LocalCerts {
			if normalName(zone) == name {
				cert, ok = stored, true
			}
		}
	}
	if !ok {
		return nil
	}
	return utils.ForkLeaf(cert.TLSCert)
}
