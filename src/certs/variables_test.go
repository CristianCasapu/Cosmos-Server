package certs

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/go-acme/lego/v5/providers/dns"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestOnlyTheProvidersOwnSettingsAreHandedOver(t *testing.T) {
	setup(t, nil)
	allowed := map[string][]string{
		"cloudflare": {"CF_DNS_API_TOKEN", "CLOUDFLARE_DNS_API_TOKEN", "CF_ZONE_API_TOKEN", "CF_API_EMAIL", "CF_API_KEY", "CLOUDFLARE_PROPAGATION_TIMEOUT"},
		"hetzner":    {"HETZNER_API_TOKEN", "HETZNER_TTL"},
		"route53":    {"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_REGION"},
		"ovh":        {"OVH_ENDPOINT", "OVH_APPLICATION_KEY"},
	}
	for provider, names := range allowed {
		for _, name := range names {
			if !allowedVariable(provider, name) {
				t.Errorf("%s must be given %s", provider, name)
			}
		}
	}
	refused := map[string][]string{
		// the address of Cloudflare's API cannot be moved: the kept token would follow
		"cloudflare": {"CLOUDFLARE_BASE_URL", "CF_BASE_URL", "PATH", "LD_PRELOAD", "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY",
			"LEGO_CA_CERTIFICATES", "LEGO_DISABLE_CNAME_SUPPORT", "SSL_CERT_FILE", "COSMOS_CONFIG_FOLDER", "HOME",
			"HETZNER_API_TOKEN", "cf_dns_api_token", "CF_DNS_API_TOKEN_FILE", ""},
		"hetzner":   {"CF_DNS_API_TOKEN", "PATH"},
		"exec":      {"EXEC_PATH", "EXEC_MODE"},
		"manual":    {"ANYTHING"},
		"":          {"CF_DNS_API_TOKEN"},
		"notathing": {"CF_DNS_API_TOKEN"},
	}
	for provider, names := range refused {
		for _, name := range names {
			if allowedVariable(provider, name) {
				t.Errorf("%q must not be given %q", provider, name)
			}
		}
	}

	given, dropped := providerVariables(utils.DNSZoneConfig{DNSChallengeProvider: "cloudflare", DNSChallengeConfig: map[string]string{
		" CF_DNS_API_TOKEN ": " " + testToken + " ", "CLOUDFLARE_BASE_URL": "https://203.0.113.9/", "PATH": "/tmp", "CF_API_KEY": "  ",
	}})
	if len(given) != 1 || given["CF_DNS_API_TOKEN"] != testToken {
		t.Errorf("given: %v", given)
	}
	if strings.Join(dropped, ",") != "CLOUDFLARE_BASE_URL,PATH" {
		t.Errorf("left out: %v", dropped)
	}
}

func TestTheCatalogueMatchesTheLibrary(t *testing.T) {
	if len(Providers()) < 150 {
		t.Fatalf("the catalogue holds %d providers", len(Providers()))
	}
	for _, never := range []string{"exec", "manual"} {
		if _, in := Providers()[never]; in {
			t.Errorf("%q must not be on offer: a server cannot be told to run a program or wait for a keyboard", never)
		}
	}
	for code, provider := range Providers() {
		if provider.Name == "" {
			t.Errorf("%s has no name", code)
		}
		// the library knows the code: without credentials it fails for
		// another reason than "unrecognized"
		_, err := dns.NewDNSChallengeProviderByName(code)
		if err != nil && strings.Contains(err.Error(), "unrecognized DNS provider") {
			t.Errorf("lego does not know the provider %q of the catalogue: run scripts/fork-lego-providers.go", code)
		}
		for _, variable := range append(append([]Variable{}, provider.Credentials...), provider.Optional...) {
			if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(variable.Name) {
				t.Errorf("%s: odd variable name %q", code, variable.Name)
			}
		}
	}
	for _, known := range nameservers {
		if _, ok := Providers()[known.Provider]; !ok {
			t.Errorf("nameservers.json names the provider %q, which the catalogue does not have", known.Provider)
		}
	}
}

func TestTheProviderIsProposedFromTheNameServers(t *testing.T) {
	cases := map[string][]string{
		"cloudflare":   {"ada.ns.cloudflare.com.", "bob.ns.cloudflare.com."},
		"route53":      {"ns-123.awsdns-45.org", "ns-9.awsdns-1.co.uk"},
		"hetzner":      {"hydrogen.ns.hetzner.com", "helium.ns.hetzner.de"},
		"gandiv5":      {"NS-12-A.GANDI.NET"},
		"digitalocean": {"ns1.digitalocean.com"},
		"":             {"ns1.example.com", "cloudflare.com.evil.example", "nscloudflare.com"},
	}
	for want, servers := range cases {
		if got := providerForNameservers(servers); got != want {
			t.Errorf("%v: proposed %q, want %q", servers, got, want)
		}
	}
	if got := providerForNameservers(nil); got != "" {
		t.Errorf("no name server: %q", got)
	}
}

func TestCloudflareTokenNeverEntersTheEnvironment(t *testing.T) {
	setup(t, nil)
	t.Setenv("CLOUDFLARE_BASE_URL", "https://203.0.113.9/client/v4")
	zone := utils.DNSZoneConfig{Zone: "example.org", DNSChallengeProvider: "cloudflare", DNSChallengeConfig: map[string]string{
		"CF_DNS_API_TOKEN": testToken, "CLOUDFLARE_BASE_URL": "https://203.0.113.9/client/v4",
	}}
	provider, secrets, done, err := dnsProvider(zone)
	if err != nil || provider == nil {
		t.Fatalf("the provider could not be built: %v", err)
	}
	for _, name := range []string{"CF_DNS_API_TOKEN", "CLOUDFLARE_DNS_API_TOKEN"} {
		if value, set := os.LookupEnv(name); set {
			t.Errorf("%s is in the environment (%d characters)", name, len(value))
		}
	}
	if len(secrets) != 1 || secrets[0] != testToken {
		t.Errorf("the secrets to keep out of messages: %d", len(secrets))
	}
	done()

	// another provider gets its settings through the environment, for the
	// time of the order only, and what was there before comes back
	t.Setenv("HETZNER_TTL", "777")
	zone = utils.DNSZoneConfig{Zone: "example.net", DNSChallengeProvider: "hetzner", DNSChallengeConfig: map[string]string{
		"HETZNER_API_TOKEN": "hetzner-token-0123456789", "HETZNER_TTL": "300", "PATH": "/nowhere",
	}}
	path := os.Getenv("PATH")
	_, _, done, err = dnsProvider(zone)
	if err != nil {
		t.Fatalf("hetzner: %v", err)
	}
	if os.Getenv("HETZNER_API_TOKEN") != "hetzner-token-0123456789" || os.Getenv("HETZNER_TTL") != "300" {
		t.Error("the provider's settings are not in the environment during the order")
	}
	if os.Getenv("PATH") != path {
		t.Error("a setting that is not the provider's reached the environment")
	}
	done()
	if _, set := os.LookupEnv("HETZNER_API_TOKEN"); set {
		t.Error("the token stays in the environment after the order")
	}
	if os.Getenv("HETZNER_TTL") != "777" {
		t.Errorf("what was in the environment before is not put back: %q", os.Getenv("HETZNER_TTL"))
	}

	// a provider that is not on offer is not built
	if _, _, done, err := dnsProvider(utils.DNSZoneConfig{DNSChallengeProvider: "exec", DNSChallengeConfig: map[string]string{"EXEC_PATH": "/bin/true"}}); err == nil {
		t.Error("the provider \"exec\" was built")
	} else {
		done()
	}
}

func TestADomainIsCheckedBeforeItIsStored(t *testing.T) {
	setup(t, nil)
	zones := utils.GetMainConfig().HTTPConfig.DNSZones
	zones = append(zones,
		utils.DNSZoneConfig{Zone: "selfsigned.example", HTTPSCertificateMode: "SELFSIGNED", DNSChallengeProvider: "cloudflare"},
		utils.DNSZoneConfig{Zone: "plain.example", HTTPSCertificateMode: "LETSENCRYPT"},
		utils.DNSZoneConfig{Zone: "delegated.example", HTTPSCertificateMode: "LETSENCRYPT", ChallengeVia: "example.org"},
		utils.DNSZoneConfig{Zone: "old.example", HTTPSCertificateMode: "LETSENCRYPT", DNSChallengeProvider: "cloudflare",
			DNSChallengeConfig: map[string]string{"CF_DNS_API_TOKEN": testToken, "CLOUDFLARE_BASE_URL": "https://old.example/"}},
	)
	le := "LETSENCRYPT"
	good := []utils.DNSZoneConfig{
		{Zone: "example.net", HTTPSCertificateMode: le},
		{Zone: "example.net", HTTPSCertificateMode: le, DNSChallengeProvider: "hetzner", DNSChallengeConfig: map[string]string{"HETZNER_API_TOKEN": "x", "HETZNER_TTL": ""}},
		{Zone: "example.net", HTTPSCertificateMode: le, ChallengeVia: " Example.ORG. ", UseWildcardCertificate: true},
		{Zone: "example.net", ChallengeVia: "example.org"},
		// an old odd value does not block saving something else
		{Zone: "old.example", HTTPSCertificateMode: le, DNSChallengeProvider: "cloudflare", UseWildcardCertificate: true,
			DNSChallengeConfig: map[string]string{"CF_DNS_API_TOKEN": "***", "CLOUDFLARE_BASE_URL": "https://old.example/"}},
	}
	for _, zone := range good {
		if problem := ValidateZone(&zone, zones); problem != "" {
			t.Errorf("%+v refused: %s", zone, problem)
		}
		if zone.ChallengeVia != "" && zone.ChallengeVia != "example.org" {
			t.Errorf("the other domain's name is not normalised: %q", zone.ChallengeVia)
		}
	}
	bad := map[string]utils.DNSZoneConfig{
		"a provider lego does not have":            {Zone: "example.net", DNSChallengeProvider: "nonsense"},
		"the provider that runs a program":         {Zone: "example.net", DNSChallengeProvider: "exec", DNSChallengeConfig: map[string]string{"EXEC_PATH": "/bin/sh"}},
		"a setting that is not the provider's":     {Zone: "example.net", DNSChallengeProvider: "hetzner", DNSChallengeConfig: map[string]string{"PATH": "/tmp"}},
		"a new address for Cloudflare's API":       {Zone: "example.org", DNSChallengeProvider: "cloudflare", DNSChallengeConfig: map[string]string{"CF_DNS_API_TOKEN": "***", "CLOUDFLARE_BASE_URL": "https://203.0.113.9/"}},
		"both DNS access and a delegation":         {Zone: "example.net", HTTPSCertificateMode: le, DNSChallengeProvider: "hetzner", ChallengeVia: "example.org"},
		"a delegation without Let's Encrypt":       {Zone: "example.net", HTTPSCertificateMode: "SELFSIGNED", ChallengeVia: "example.org"},
		"a delegation to itself":                   {Zone: "example.net", HTTPSCertificateMode: le, ChallengeVia: "example.net"},
		"a delegation to a domain not set up":      {Zone: "example.net", HTTPSCertificateMode: le, ChallengeVia: "unknown.example"},
		"to a domain without DNS access":           {Zone: "example.net", HTTPSCertificateMode: le, ChallengeVia: "plain.example"},
		"to a domain that does not use the CA":     {Zone: "example.net", HTTPSCertificateMode: le, ChallengeVia: "selfsigned.example"},
		"to a domain that is delegated itself":     {Zone: "example.net", HTTPSCertificateMode: le, ChallengeVia: "delegated.example"},
		"by the domain another one is answered in": {Zone: "example.org", HTTPSCertificateMode: le, ChallengeVia: "old.example"},
	}
	for name, zone := range bad {
		problem := ValidateZone(&zone, zones)
		if problem == "" {
			t.Errorf("%s: stored without a word", name)
		}
		if strings.ContainsAny(problem, "\n\r") || len(problem) > 300 {
			t.Errorf("%s: the answer is not one bounded line: %q", name, problem)
		}
	}
	long := utils.DNSZoneConfig{Zone: "example.net", DNSChallengeProvider: strings.Repeat("x", 5000) + "\nfake line"}
	if problem := ValidateZone(&long, zones); len(problem) > 200 || strings.Contains(problem, "\n") {
		t.Errorf("a long provider name is echoed whole: %d bytes", len(problem))
	}
}
