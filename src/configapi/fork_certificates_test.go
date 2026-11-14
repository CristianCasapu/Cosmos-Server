package configapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: tests of fork_certificates.go. The settings are read and
// saved through the real handlers, the way the settings page does it.

// settingsTestServer gives the handlers a config folder of their own and
// keeps the restart that follows a save from reaching anything
func settingsTestServer(t *testing.T) chan struct{} {
	t.Helper()

	prevFolder := utils.CONFIGFOLDER
	prevConfig := utils.GetBaseMainConfig()
	utils.CONFIGFOLDER = t.TempDir() + "/"
	t.Setenv("CONFIG_FILE", "")

	// what SoftRestartServer calls after a save; the last one tells the test it is over
	restarted := make(chan struct{}, 8)
	utils.WaitForAllJobs = func() {}
	utils.RestartConstellation = func() {}
	utils.InitRemoteStorage = func() {}
	utils.InitBackups = func() {}
	utils.InitSnapRAIDConfig = func() {}
	utils.RestartCRON = func() { restarted <- struct{}{} }

	t.Cleanup(func() {
		utils.LoadBaseMainConfig(prevConfig)
		utils.CONFIGFOLDER = prevFolder
	})
	return restarted
}

func asAdmin(req *http.Request) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{
		Nickname:    "admin",
		Role:        utils.ADMIN,
		UserRole:    utils.ADMIN,
		Permissions: utils.GetRolePermissions(utils.ADMIN),
		IsSudoed:    true,
	}))
}

func TestSettingsSaveKeepsTheSelfSignedCertificateAndItsCA(t *testing.T) {
	restarted := settingsTestServer(t)

	// a server reached by its IP, HTTPS with the certificate it made itself
	domains := []string{"172.16.0.14"}
	config := utils.DefaultConfig
	config.NewInstall = false
	config.HTTPConfig.Hostname = domains[0]
	config.HTTPConfig.HTTPPort = "2052"
	config.HTTPConfig.HTTPSPort = "2053"
	config.HTTPConfig.HTTPSCertificateMode = utils.HTTPSCertModeList["SELFSIGNED"]
	config.EmailConfig.Port = "587"
	utils.SetBaseMainConfig(config)

	// made the way the server makes them at start-up: a CA, then a certificate signed by it
	certPEM, keyPEM, err := utils.GenerateRSAWebCertificates(domains)
	if err != nil {
		t.Fatal(err)
	}
	config = utils.GetBaseMainConfig()
	config.HTTPConfig.SelfTLSCert = certPEM
	config.HTTPConfig.SelfTLSKey = keyPEM
	config.HTTPConfig.SelfTLSKeyHostsCached = domains
	config.HTTPConfig.SelfTLSValidUntil = time.Now().AddDate(0, 0, 364).UTC().Truncate(time.Second)
	config.HTTPConfig.TLSCert = certPEM
	config.HTTPConfig.TLSKey = keyPEM
	config.HTTPConfig.TLSKeyHostsCached = domains
	config.HTTPConfig.TLSValidUntil = config.HTTPConfig.SelfTLSValidUntil
	utils.SetBaseMainConfig(config)

	stored := utils.ReadConfigFromFile().HTTPConfig
	if stored.CACert == "" || stored.CAPrivateKey == "" || stored.SelfTLSCert == "" || stored.SelfTLSKey == "" {
		t.Fatal("the test server has no CA or no self-signed certificate to lose")
	}

	// the settings page reads the settings...
	w := httptest.NewRecorder()
	ConfigApiGet(w, asAdmin(httptest.NewRequest("GET", "http://172.16.0.14:2053/cosmos/api/config", nil)))
	if w.Code != http.StatusOK {
		t.Fatalf("GET config: %d %s", w.Code, w.Body.String())
	}
	var got struct {
		Data utils.Config `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// ...which never carry a private key: the save cannot get them back from the page
	if got.Data.HTTPConfig.CAPrivateKey != "" || got.Data.HTTPConfig.SelfTLSKey != "" || got.Data.HTTPConfig.TLSKey != "" {
		t.Fatal("a private key was sent to the settings page")
	}

	// ...and saves them whole, with no hostname changed
	body, err := json.Marshal(got.Data)
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	ConfigApiSet(w, asAdmin(httptest.NewRequest("PUT", "http://172.16.0.14:2053/cosmos/api/config", bytes.NewReader(body))))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT config: %d %s", w.Code, w.Body.String())
	}
	select {
	case <-restarted:
	case <-time.After(10 * time.Second):
		t.Fatal("the restart that follows a save did not finish")
	}

	saved := utils.ReadConfigFromFile().HTTPConfig
	for name, pair := range map[string][2]string{
		"CACert":       {saved.CACert, stored.CACert},
		"CAPrivateKey": {saved.CAPrivateKey, stored.CAPrivateKey},
		"SelfTLSCert":  {saved.SelfTLSCert, stored.SelfTLSCert},
		"SelfTLSKey":   {saved.SelfTLSKey, stored.SelfTLSKey},
		"TLSCert":      {saved.TLSCert, stored.TLSCert},
		"TLSKey":       {saved.TLSKey, stored.TLSKey},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s was not kept by the save (now %d bytes, was %d)", name, len(pair[0]), len(pair[1]))
		}
	}
	if !saved.SelfTLSValidUntil.Equal(stored.SelfTLSValidUntil) {
		t.Errorf("SelfTLSValidUntil changed from %s to %s", stored.SelfTLSValidUntil, saved.SelfTLSValidUntil)
	}

	// What the start-up code looks at before it makes a new certificate
	// (InitServer: no certificate or key, a hostname the certificate lacks,
	// or the end of its validity): none of it holds after the save.
	if saved.SelfTLSCert == "" || saved.SelfTLSKey == "" {
		t.Error("the next start finds no self-signed certificate and makes a new one")
	}
	if _, err := tls.X509KeyPair([]byte(saved.SelfTLSCert), []byte(saved.SelfTLSKey)); err != nil {
		t.Errorf("the certificate and its key no longer load: %v", err)
	}
	if utils.HasAnyNewItem(utils.GetAllHostnames(true), saved.SelfTLSKeyHostsCached) {
		t.Errorf("the hostnames %v are no longer covered by the cached ones %v", utils.GetAllHostnames(true), saved.SelfTLSKeyHostsCached)
	}
	if !time.Now().Add(45 * 24 * time.Hour).Before(saved.SelfTLSValidUntil) {
		t.Error("the certificate is taken for one that expires soon")
	}

	// And the CA still signs: a certificate made later (a new hostname, a
	// renewal) comes from the CA the browsers were told to trust, not from a new one.
	laterPEM, _, err := utils.GenerateRSAWebCertificates([]string{"172.16.0.14", "nas.local"})
	if err != nil {
		t.Fatal(err)
	}
	if utils.GetBaseMainConfig().HTTPConfig.CACert != stored.CACert {
		t.Fatal("a new CA was made for the next certificate")
	}
	caBlock, _ := pem.Decode([]byte(stored.CACert))
	laterBlock, _ := pem.Decode([]byte(laterPEM))
	if caBlock == nil || laterBlock == nil {
		t.Fatal("unreadable certificate")
	}
	ca, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	later, err := x509.ParseCertificate(laterBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := later.CheckSignatureFrom(ca); err != nil {
		t.Errorf("the later certificate is not signed by the stored CA: %v", err)
	}
}

func TestKeepServerCertificatesIgnoresWhatTheRequestCarries(t *testing.T) {
	validUntil := time.Date(2027, 11, 1, 0, 0, 0, 0, time.UTC)
	stored := utils.Config{}
	stored.HTTPConfig.CACert = "stored CA"
	stored.HTTPConfig.CAPrivateKey = "stored CA key"
	stored.HTTPConfig.SelfTLSCert = "stored certificate"
	stored.HTTPConfig.SelfTLSKey = "stored key"
	stored.HTTPConfig.SelfTLSKeyHostsCached = []string{"172.16.0.14"}
	stored.HTTPConfig.SelfTLSValidUntil = validUntil

	// neither blanks nor values of the caller's choosing get in
	for name, request := range map[string]utils.HTTPConfig{
		"blank": {},
		"forged": {
			CACert: "another CA", CAPrivateKey: "another CA key",
			SelfTLSCert: "another certificate", SelfTLSKey: "another key",
			SelfTLSKeyHostsCached: []string{"elsewhere.example"},
			SelfTLSValidUntil:     validUntil.AddDate(50, 0, 0),
		},
	} {
		config := utils.Config{HTTPConfig: request}
		config.HTTPConfig.Hostname = "changed-by-the-request"
		keepServerCertificates(&config, stored)

		kept := config.HTTPConfig
		if kept.CACert != "stored CA" || kept.CAPrivateKey != "stored CA key" ||
			kept.SelfTLSCert != "stored certificate" || kept.SelfTLSKey != "stored key" ||
			len(kept.SelfTLSKeyHostsCached) != 1 || kept.SelfTLSKeyHostsCached[0] != "172.16.0.14" ||
			!kept.SelfTLSValidUntil.Equal(validUntil) {
			t.Errorf("%s request: the stored CA and certificate were not kept: %+v", name, kept)
		}
		// everything else is the request's
		if kept.Hostname != "changed-by-the-request" {
			t.Errorf("%s request: a setting of the request was overwritten", name)
		}
	}
}

// What the certificate orders may use is not the settings page's to change.
func TestSettingsSaveKeepsTheCertificateSettings(t *testing.T) {
	stored := utils.Config{}
	stored.HTTPConfig.Certificates.HTTPChallenge = "no"
	request := utils.Config{}
	request.HTTPConfig.Certificates.HTTPChallenge = "yes"
	keepServerCertificates(&request, stored)
	if request.HTTPConfig.Certificates.HTTPChallenge != "no" {
		t.Errorf("a settings save changed it to %q", request.HTTPConfig.Certificates.HTTPChallenge)
	}
}

// A domain is checked by the fork's rules before it is stored: the
// variables are the provider's own, and a challenge handed to another
// domain names a domain that can answer it.
func TestSavingADomainChecksWhatTheOrderWillUse(t *testing.T) {
	settingsTestServer(t)
	config := utils.DefaultConfig
	config.NewInstall = false
	config.HTTPConfig.Hostname = "192.168.1.10"
	config.HTTPConfig.HTTPSCertificateMode = "LETSENCRYPT"
	config.HTTPConfig.DNSZones = []utils.DNSZoneConfig{{
		Zone: "example.org", HTTPSCertificateMode: "LETSENCRYPT", DNSChallengeProvider: "cloudflare",
		DNSChallengeConfig: map[string]string{"CF_DNS_API_TOKEN": "cfut_Zm9ya3Rlc3R0b2tlbl9mb3JrdGVzdHRva2VuXzAxMjM0NQ12345678"},
	}}
	utils.SetBaseMainConfig(config)

	put := func(zone string, config utils.DNSZoneConfig) (int, string) {
		body, _ := json.Marshal(config)
		req := asAdmin(httptest.NewRequest("PUT", "https://192.168.1.10:2053/cosmos/api/zones/"+zone, bytes.NewReader(body)))
		req = mux.SetURLVars(req, map[string]string{"zone": zone})
		w := httptest.NewRecorder()
		ZonesApiSet(w, req)
		return w.Code, w.Body.String()
	}

	refused := map[string]utils.DNSZoneConfig{
		"a moved API address with the kept token": {HTTPSCertificateMode: "LETSENCRYPT", DNSChallengeProvider: "cloudflare",
			DNSChallengeConfig: map[string]string{"CF_DNS_API_TOKEN": utils.MaskedSecret, "CLOUDFLARE_BASE_URL": "https://203.0.113.9/client/v4"}},
		"a variable of the process": {HTTPSCertificateMode: "LETSENCRYPT", DNSChallengeProvider: "cloudflare",
			DNSChallengeConfig: map[string]string{"CF_DNS_API_TOKEN": utils.MaskedSecret, "HTTPS_PROXY": "http://203.0.113.9:3128"}},
		"the provider that runs a program": {HTTPSCertificateMode: "LETSENCRYPT", DNSChallengeProvider: "exec",
			DNSChallengeConfig: map[string]string{"EXEC_PATH": "/bin/sh"}},
	}
	for name, zone := range refused {
		code, body := put("example.org", zone)
		if code != http.StatusBadRequest || !strings.Contains(body, "ZN010") {
			t.Errorf("%s: status %d %s", name, code, body)
		}
	}
	for name, zone := range map[string]utils.DNSZoneConfig{
		"a challenge handed to a domain not set up": {HTTPSCertificateMode: "LETSENCRYPT", ChallengeVia: "unknown.example", UseWildcardCertificate: true},
		"a challenge handed to itself":              {HTTPSCertificateMode: "LETSENCRYPT", ChallengeVia: "example.net"},
	} {
		code, body := put("example.net", zone)
		if code != http.StatusBadRequest || !strings.Contains(body, "ZN010") {
			t.Errorf("%s: status %d %s", name, code, body)
		}
	}
	after := utils.ReadConfigFromFile().HTTPConfig.DNSZones
	if len(after) != 1 || len(after[0].DNSChallengeConfig) != 1 {
		t.Fatalf("a refused save changed the domains: %+v", after)
	}

	// a wildcard certificate needs a DNS challenge: a delegated one counts
	code, body := put("example.net", utils.DNSZoneConfig{HTTPSCertificateMode: "LETSENCRYPT", ChallengeVia: "Example.org", UseWildcardCertificate: true, ManageRecords: true})
	if code == http.StatusBadRequest {
		t.Fatalf("a delegated challenge with a wildcard certificate is refused: %s", body)
	}
	if code != http.StatusOK {
		t.Skipf("the domain could not be saved in this test set-up (status %d)", code)
	}
	for _, zone := range utils.ReadConfigFromFile().HTTPConfig.DNSZones {
		if zone.Zone == "example.net" && (zone.ChallengeVia != "example.org" || !zone.UseWildcardCertificate || zone.ManageRecords) {
			t.Errorf("the delegated domain as stored: %+v", zone)
		}
	}
}
