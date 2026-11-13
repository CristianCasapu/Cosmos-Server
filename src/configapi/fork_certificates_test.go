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
	"testing"
	"time"

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
