package utils

import (
	"os"
	"reflect"
	"sort"
	"testing"
	"time"
)

func localCertConfig() Config {
	config := Config{}
	config.HTTPConfig.DNSZones = []DNSZoneConfig{
		{Zone: "example.com", HTTPSCertificateMode: HTTPSCertModeList["LETSENCRYPT"]},
	}
	return config
}

func TestLocalCertZone(t *testing.T) {
	config := localCertConfig()
	for host, want := range map[string]string{
		"app.example.com":      "example.com",
		"Deep.App.Example.com": "example.com",
		"example.com:8443":     "example.com",
		"solo.net":             "solo.net",
		"www.solo.net":         "www.solo.net",
	} {
		if got := LocalCertZone(config, host); got != want {
			t.Errorf("LocalCertZone(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestLocalCertGroups(t *testing.T) {
	got := LocalCertGroups(localCertConfig(), []string{
		"app.example.com", "example.com", "app.example.com", "solo.net", "www.solo.net", "192.168.1.1", "nas",
	})
	want := map[string][]string{
		"example.com":  {"app.example.com", "example.com"},
		"solo.net":     {"solo.net"},
		"www.solo.net": {"www.solo.net"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestLocalCertsToIssue(t *testing.T) {
	hosts := []string{"app.example.com", "example.com", "solo.net"}
	in60Days := time.Now().AddDate(0, 0, 60)
	in10Days := time.Now().AddDate(0, 0, 10)

	zones := func(toIssue map[string][]string) []string {
		names := []string{}
		for zone := range toIssue {
			names = append(names, zone)
		}
		sort.Strings(names)
		return names
	}

	for name, tc := range map[string]struct {
		local  map[string]ZoneCert
		legacy []string
		legacyUntil time.Time
		force  bool
		want   []string
	}{
		"nothing yet": {
			want: []string{"example.com", "solo.net"},
		},
		"all covered": {
			local: map[string]ZoneCert{
				"example.com": {Hosts: []string{"app.example.com", "example.com"}, ValidUntil: in60Days},
				"solo.net":    {Hosts: []string{"solo.net"}, ValidUntil: in60Days},
			},
			want: []string{},
		},
		"new hostname in a domain": {
			local: map[string]ZoneCert{
				"example.com": {Hosts: []string{"example.com"}, ValidUntil: in60Days},
				"solo.net":    {Hosts: []string{"solo.net"}, ValidUntil: in60Days},
			},
			want: []string{"example.com"},
		},
		"one expiring": {
			local: map[string]ZoneCert{
				"example.com": {Hosts: []string{"app.example.com", "example.com"}, ValidUntil: in10Days},
				"solo.net":    {Hosts: []string{"solo.net"}, ValidUntil: in60Days},
			},
			want: []string{"example.com"},
		},
		"legacy certificate still serves": {
			legacy: []string{"app.example.com", "example.com", "solo.net"}, legacyUntil: in60Days,
			want: []string{},
		},
		"legacy certificate expiring": {
			legacy: []string{"app.example.com", "example.com", "solo.net"}, legacyUntil: in10Days,
			want: []string{"example.com", "solo.net"},
		},
		"legacy covers one domain only": {
			legacy: []string{"solo.net"}, legacyUntil: in60Days,
			want: []string{"example.com"},
		},
		"forced": {
			local: map[string]ZoneCert{
				"example.com": {Hosts: []string{"app.example.com", "example.com"}, ValidUntil: in60Days},
				"solo.net":    {Hosts: []string{"solo.net"}, ValidUntil: in60Days},
			},
			force: true,
			want:  []string{"example.com", "solo.net"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			config := localCertConfig()
			config.HTTPConfig.LocalCerts = tc.local
			if tc.legacy != nil {
				config.HTTPConfig.TLSCert = "cert"
				config.HTTPConfig.TLSKeyHostsCached = tc.legacy
				config.HTTPConfig.TLSValidUntil = tc.legacyUntil
			}
			if got := zones(LocalCertsToIssue(config, hosts, tc.force)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGetHostCertificateLocalCert(t *testing.T) {
	config := localCertConfig()
	config.HTTPConfig.HTTPSCertificateMode = HTTPSCertModeList["LETSENCRYPT"]
	until := time.Now().AddDate(0, 0, 60)
	config.HTTPConfig.LocalCerts = map[string]ZoneCert{
		"example.com": {Hosts: []string{"app.example.com"}, ValidUntil: until},
	}
	config.HTTPConfig.TLSKeyHostsCached = []string{"old.net"}

	got := GetHostCertificate(config, "app.example.com")
	if got.Source != "node" || !got.Covered || got.Zone != "example.com" || !got.ValidUntil.Equal(until) {
		t.Errorf("covered host: got %+v", got)
	}
	got = GetHostCertificate(config, "other.example.com")
	if got.Source != "node" || got.Covered || !reflect.DeepEqual(got.Hosts, []string{"old.net"}) {
		t.Errorf("uncovered host falls back to the legacy certificate: got %+v", got)
	}
}

func TestACMEAccountRoundTrip(t *testing.T) {
	previous := CONFIGFOLDER
	CONFIGFOLDER = t.TempDir() + string(os.PathSeparator)
	t.Cleanup(func() { CONFIGFOLDER = previous })

	const production = "https://acme-v02.api.letsencrypt.org/directory"

	if got := LoadACMEAccount(production); got != nil {
		t.Fatalf("no file yet, got %+v", got)
	}

	key, keyPEM, err := NewACMEAccountKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveACMEAccount(ACMEAccount{Directory: production, URL: "https://acme/acct/1", Email: "a@b.c", Key: keyPEM}); err != nil {
		t.Fatal(err)
	}

	got := LoadACMEAccount(production)
	if got == nil || got.URL != "https://acme/acct/1" {
		t.Fatalf("got %+v", got)
	}
	loadedKey, err := got.PrivateKey()
	if err != nil || !loadedKey.Equal(key) {
		t.Fatalf("key does not round trip: %v", err)
	}

	// an account of another CA is not reused
	if got := LoadACMEAccount("https://acme-staging-v02.api.letsencrypt.org/directory"); got != nil {
		t.Errorf("staging must not reuse the production account, got %+v", got)
	}

	// a broken file is ignored rather than fatal
	os.WriteFile(acmeAccountPath(), []byte("{"), 0600)
	if got := LoadACMEAccount(production); got != nil {
		t.Errorf("broken file: got %+v", got)
	}
}
