package utils

// Fork addition: the places where upstream's certificate code asks the
// fork's rules (fork_certs.go). The rules themselves are tested in
// src/certs; here: that they are asked, and that without them everything
// is as upstream wrote it.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func forkCertHooks(t *testing.T) {
	t.Helper()
	obtain, due, may, noHTTP := ForkObtain, ForkCertDue, ForkMayOrder, ForkNoHTTPChallenge
	t.Cleanup(func() { ForkObtain, ForkCertDue, ForkMayOrder, ForkNoHTTPChallenge = obtain, due, may, noHTTP })
	ForkObtain, ForkCertDue, ForkMayOrder, ForkNoHTTPChallenge = nil, nil, nil, nil
}

func forkZonesConfig(t *testing.T) Config {
	t.Helper()
	previous := GetBaseMainConfig()
	t.Cleanup(func() { LoadBaseMainConfig(previous) })
	config := Config{}
	config.HTTPConfig.Hostname = "192.168.1.10"
	config.HTTPConfig.HTTPSCertificateMode = HTTPSCertModeList["LETSENCRYPT"]
	config.HTTPConfig.DNSZones = []DNSZoneConfig{
		{Zone: "example.org", HTTPSCertificateMode: "LETSENCRYPT", DNSChallengeProvider: "cloudflare", UseWildcardCertificate: true},
		{Zone: "example.net", HTTPSCertificateMode: "LETSENCRYPT", ChallengeVia: "example.org", UseWildcardCertificate: true},
		{Zone: "example.com", HTTPSCertificateMode: "LETSENCRYPT"},
	}
	for _, host := range []string{"app.example.org", "app.example.net", "x.y.example.net", "app.example.com"} {
		config.HTTPConfig.ProxyConfig.Routes = append(config.HTTPConfig.ProxyConfig.Routes, ProxyRouteConfig{Name: host, UseHost: true, Host: host})
	}
	LoadBaseMainConfig(config)
	return GetMainConfig()
}

func zonesToIssue(m map[string][]string) string {
	keys := []string{}
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func TestWhichNamesGoOnWhichCertificate(t *testing.T) {
	forkCertHooks(t)
	config := forkZonesConfig(t)
	zones := config.HTTPConfig.DNSZones

	// a domain with DNS access and a domain whose challenge is answered in
	// another one get a certificate of their own; a domain with neither is
	// left to the HTTP challenge
	if !IssuesZoneCert(zones[0]) || !IssuesZoneCert(zones[1]) || IssuesZoneCert(zones[2]) {
		t.Errorf("own certificate: %v %v %v", IssuesZoneCert(zones[0]), IssuesZoneCert(zones[1]), IssuesZoneCert(zones[2]))
	}
	selfSigned := DNSZoneConfig{Zone: "x.example", HTTPSCertificateMode: "SELFSIGNED", ChallengeVia: "example.org"}
	if IssuesZoneCert(selfSigned) {
		t.Error("a self-signed domain orders nothing, whatever else it carries")
	}

	toIssue := ZoneCertsToIssue(config, false)
	if !reflect.DeepEqual(toIssue["example.org"], []string{"example.org", "*.example.org"}) {
		t.Errorf("example.org: %v", toIssue["example.org"])
	}
	// the wildcard pair, plus the name two levels down
	if !reflect.DeepEqual(toIssue["example.net"], []string{"example.net", "*.example.net", "x.y.example.net"}) {
		t.Errorf("example.net (delegated): %v", toIssue["example.net"])
	}
	if _, has := toIssue["example.com"]; has {
		t.Error("a domain without DNS access is not a zone certificate")
	}
	hosts := LocalCertHostnames(config, GetAllHostnames(true))
	if !reflect.DeepEqual(LetsEncryptValidOnly(hosts, false), []string{"app.example.com"}) {
		t.Errorf("left to the HTTP challenge: %v", hosts)
	}
}

func TestTheForksRulesDecideWhenToOrder(t *testing.T) {
	forkCertHooks(t)
	config := forkZonesConfig(t)
	config.HTTPConfig.ZoneCerts = map[string]ZoneCert{
		// upstream's rule: 45 days before the stored end
		"example.org": {Hosts: []string{"example.org", "*.example.org"}, ValidUntil: time.Now().AddDate(0, 0, 60)},
		"example.net": {Hosts: []string{"example.net", "*.example.net", "x.y.example.net"}, ValidUntil: time.Now().AddDate(0, 0, 30)},
	}
	if got := zonesToIssue(ZoneCertsToIssue(config, false)); got != "example.net" {
		t.Errorf("without the fork's rule (45 days before the end): %s", got)
	}

	asked := []string{}
	ForkCertDue = func(key string, cert ZoneCert, now time.Time) bool {
		asked = append(asked, key)
		return key == "zone:example.org"
	}
	if got := zonesToIssue(ZoneCertsToIssue(config, false)); got != "example.org" {
		t.Errorf("with the fork's rule: %s", got)
	}
	sort.Strings(asked)
	if strings.Join(asked, ",") != "zone:example.net,zone:example.org" {
		t.Errorf("asked about: %v", asked)
	}

	// an order that may not start is not on the list, forced or not
	var names []string
	ForkMayOrder = func(key string, wanted []string, force bool, now time.Time) bool {
		if key == "zone:example.org" {
			names = wanted
		}
		return false
	}
	for _, force := range []bool{false, true} {
		if got := ZoneCertsToIssue(config, force); len(got) != 0 {
			t.Errorf("force=%v, although no order may start: %v", force, got)
		}
	}
	if !reflect.DeepEqual(names, []string{"example.org", "*.example.org"}) {
		t.Errorf("the names of the order are not passed on: %v", names)
	}
	ForkMayOrder = func(key string, wanted []string, force bool, now time.Time) bool { return force }
	if got := zonesToIssue(ZoneCertsToIssue(config, true)); got != "example.net,example.org" {
		t.Errorf("forced, and allowed: %s", got)
	}
}

func TestNoOrderOverHTTPWhereItCannotWork(t *testing.T) {
	forkCertHooks(t)
	config := forkZonesConfig(t)
	hosts := []string{"app.example.com", "lone.example.info"}

	if got := zonesToIssue(LocalCertsToIssue(config, hosts, false)); got != "example.com,lone.example.info" {
		t.Errorf("upstream: %s", got)
	}
	ForkNoHTTPChallenge = func(Config) bool { return true }
	for _, force := range []bool{false, true} {
		if got := LocalCertsToIssue(config, hosts, force); len(got) != 0 {
			t.Errorf("force=%v: orders that cannot succeed: %v", force, got)
		}
	}
	ForkNoHTTPChallenge = func(Config) bool { return false }
	ForkMayOrder = func(key string, wanted []string, force bool, now time.Time) bool { return key != "local:example.com" }
	if got := zonesToIssue(LocalCertsToIssue(config, hosts, false)); got != "lone.example.info" {
		t.Errorf("with a wait on example.com: %s", got)
	}

	// due by the fork's rule, not 45 days before the stored end
	ForkMayOrder = nil
	config.HTTPConfig.LocalCerts = map[string]ZoneCert{
		"example.com":       {Hosts: []string{"app.example.com"}, ValidUntil: time.Now().AddDate(0, 0, 20)},
		"lone.example.info": {Hosts: []string{"lone.example.info"}, ValidUntil: time.Now().AddDate(0, 0, 80)},
	}
	ForkCertDue = func(key string, cert ZoneCert, now time.Time) bool { return key == "local:lone.example.info" }
	if got := zonesToIssue(LocalCertsToIssue(config, hosts, false)); got != "lone.example.info" {
		t.Errorf("due by the fork's rule: %s", got)
	}
	// a certificate that does not cover a wanted name is ordered whatever the rule says
	ForkCertDue = func(string, ZoneCert, time.Time) bool { return false }
	if got := zonesToIssue(LocalCertsToIssue(config, append(hosts, "new.example.com"), false)); got != "example.com" {
		t.Errorf("a new name under example.com: %s", got)
	}
}

func TestTheOrderIsHandedToTheFork(t *testing.T) {
	forkCertHooks(t)
	var got []string
	var gotZone *DNSZoneConfig
	ForkObtain = func(domains []string, zone *DNSZoneConfig) (string, string) {
		got, gotZone = domains, zone
		return "cert", "key"
	}
	zone := DNSZoneConfig{Zone: "example.org", DNSChallengeProvider: "cloudflare"}
	cert, key := DoLetsEncrypt([]string{"example.org", "*.example.org"}, &zone)
	if cert != "cert" || key != "key" || len(got) != 2 || gotZone != &zone {
		t.Errorf("DoLetsEncrypt did not hand the order over: %q %q %v", cert, key, got)
	}
}

func TestTheStoredEndIsTheCertificatesOwn(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	end := time.Now().Add(45 * 24 * time.Hour).UTC().Truncate(time.Second)
	template := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "example.org"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: end}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	if got := ForkCertExpiry(certPEM + certPEM); !got.Equal(end) {
		t.Errorf("a 45-day certificate ends %v, stored %v", end, got)
	}
	// what cannot be read gets upstream's guess
	guess := ForkCertExpiry("not a certificate")
	if left := time.Until(guess); left < 89*24*time.Hour || left > 91*24*time.Hour {
		t.Errorf("an unreadable text: %v", guess)
	}
	if ForkLeaf("") != nil || ForkLeaf("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n") != nil {
		t.Error("something that is no certificate was read as one")
	}
	if ForkZoneCertKey("Example.ORG.") != "zone:example.org" || ForkLocalCertKey("App.Example.com:2053") != "local:app.example.com" {
		t.Errorf("keys: %q %q", ForkZoneCertKey("Example.ORG."), ForkLocalCertKey("App.Example.com:2053"))
	}
}
