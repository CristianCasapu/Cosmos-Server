package certs

// Fork addition: what the tests of this package share. Nothing here reaches
// a certificate authority, a DNS provider or a name server: the resolver is
// a table, the clock is set by the test, and certificates come from an
// authority made for the test.

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const testToken = "cfut_Zm9ya3Rlc3R0b2tlbl9mb3JrdGVzdHRva2VuXzAxMjM0NQ12345678"

// t0 is "now" at the start of every test.
var t0 = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

type testEnv struct {
	now     time.Time
	notices []utils.Notification
	errors  []string
	ca      *testAuthority
	served  map[string]*tls.Certificate
}

// setup gives the package a configuration in a folder of its own, a clock,
// an empty memory of orders and a trust store holding the test authority.
func setup(t *testing.T, change func(config *utils.Config)) *testEnv {
	t.Helper()
	env := &testEnv{now: t0, ca: newAuthority(t, "Test Root"), served: map[string]*tls.Certificate{}}

	folder := t.TempDir() + "/"
	previousFolder, previousConfig := utils.CONFIGFOLDER, utils.GetBaseMainConfig()
	utils.CONFIGFOLDER = folder

	config := utils.Config{}
	config.HTTPConfig.Hostname = "192.168.1.10"
	config.HTTPConfig.HTTPPort = "2052"
	config.HTTPConfig.HTTPSPort = "2053"
	config.HTTPConfig.HTTPSCertificateMode = "LETSENCRYPT"
	config.HTTPConfig.AuthPublicKey = "test-public-key"
	config.HTTPConfig.DNSZones = []utils.DNSZoneConfig{{
		Zone:                   "example.org",
		HTTPSCertificateMode:   "LETSENCRYPT",
		DNSChallengeProvider:   "cloudflare",
		DNSChallengeConfig:     map[string]string{"CF_DNS_API_TOKEN": testToken},
		UseWildcardCertificate: true,
	}}
	config.HTTPConfig.ProxyConfig.Routes = []utils.ProxyRouteConfig{
		{Name: "app", UseHost: true, Host: "app.example.org", Mode: "PROXY", Target: "http://127.0.0.1:1"},
	}
	if change != nil {
		change(&config)
	}
	utils.SetBaseMainConfig(config)

	previous := struct {
		clock       func() time.Time
		resolver    Resolver
		notify      func(utils.Notification)
		majorError  func(string, error)
		certFor     func(string) *tls.Certificate
		roots       *x509.CertPool
		ports       func() (int, int)
		renew       func(string)
		restart     func()
		renewalInfo func(context.Context, CA, string, *x509.Certificate) (time.Time, time.Time, time.Duration, error)
		test        *CA
		update      func(func(*utils.Config))
	}{clock, resolver, notify, majorError, certificateFor, systemRoots, ExternalPorts, RenewZone, restartServer, renewalInfo, testCA, updateConfig}

	clock = func() time.Time { return env.now }
	resolver = tableResolver{}
	notify = func(n utils.Notification) { env.notices = append(env.notices, n) }
	majorError = func(message string, err error) {
		text := message
		if err != nil {
			text += " : " + err.Error()
		}
		env.errors = append(env.errors, text)
	}
	certificateFor = func(host string) *tls.Certificate { return env.served[host] }
	systemRoots = env.ca.pool()
	ExternalPorts = nil
	RenewZone = func(string) {}
	restartServer = func() {}
	testCA = nil
	store.reset()
	renewLimit.calls, lookupLimit.calls = nil, nil

	t.Cleanup(func() {
		clock, resolver, notify, majorError = previous.clock, previous.resolver, previous.notify, previous.majorError
		certificateFor, systemRoots, ExternalPorts = previous.certFor, previous.roots, previous.ports
		RenewZone, restartServer, renewalInfo, testCA, updateConfig = previous.renew, previous.restart, previous.renewalInfo, previous.test, previous.update
		utils.CONFIGFOLDER = previousFolder
		utils.LoadBaseMainConfig(previousConfig)
		store.reset()
	})
	return env
}

// change stores a changed configuration.
func change(alter func(config *utils.Config)) {
	config := utils.GetBaseMainConfig()
	alter(&config)
	utils.SetBaseMainConfig(config)
}

// ---- a certificate authority for the tests

type testAuthority struct {
	t    *testing.T
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	next int64
}

func newAuthority(t *testing.T, name string) *testAuthority {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name, Organization: []string{"Test Authority"}},
		NotBefore: t0.Add(-10 * 365 * 24 * time.Hour), NotAfter: t0.Add(10 * 365 * 24 * time.Hour),
		IsCA: true, KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &testAuthority{t: t, cert: cert, key: key, next: 100}
}

func (a *testAuthority) pool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(a.cert)
	return pool
}

// issue makes a certificate for the names, valid between the two moments,
// and returns it as PEM with its key.
func (a *testAuthority) issue(names []string, from time.Time, to time.Time) (string, string) {
	a.t.Helper()
	a.next++
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(a.next), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: from, NotAfter: to,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		AuthorityKeyId: []byte{1, 2, 3, 4},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.cert, &key.PublicKey, a.key)
	if err != nil {
		a.t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

// zoneCert stores a certificate as the one of a domain, and serves it for
// the hosts given.
func (env *testEnv) zoneCert(t *testing.T, authority *testAuthority, zone string, names []string, from time.Time, to time.Time, hosts ...string) utils.ZoneCert {
	t.Helper()
	certPEM, keyPEM := authority.issue(names, from, to)
	cert := utils.ZoneCert{TLSCert: certPEM, TLSKey: keyPEM, Hosts: names, ValidUntil: to}
	change(func(config *utils.Config) {
		if config.HTTPConfig.ZoneCerts == nil {
			config.HTTPConfig.ZoneCerts = map[string]utils.ZoneCert{}
		}
		config.HTTPConfig.ZoneCerts[zone] = cert
	})
	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range hosts {
		env.served[host] = &pair
	}
	return cert
}

// ---- a resolver that is a table

type tableResolver struct {
	cname map[string]string
	ns    map[string][]string
	fail  map[string]bool
}

func (r tableResolver) CNAME(_ context.Context, name string) (string, error) {
	if r.fail[name] {
		return "", errors.New("lookup failed: timeout\nsecond line")
	}
	return r.cname[name], nil
}

func (r tableResolver) NS(_ context.Context, name string) ([]string, error) {
	if r.fail[name] {
		return nil, errors.New("lookup failed")
	}
	return r.ns[name], nil
}

// ---- requests

func admin(req *http.Request, permissions ...utils.Permission) *http.Request {
	if len(permissions) == 0 {
		permissions = []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN_READ, utils.PERM_CONFIGURATION_READ, utils.PERM_CONFIGURATION}
	}
	return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{
		Nickname: "admin", Permissions: permissions, IsSudoed: true,
	}))
}

func do(t *testing.T, handler http.HandlerFunc, req *http.Request) (int, map[string]interface{}) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler(recorder, req)
	var decoded map[string]interface{}
	json.Unmarshal(recorder.Body.Bytes(), &decoded)
	if strings.Contains(recorder.Body.String(), testToken) {
		t.Errorf("an answer contains the token: %s", recorder.Body.String())
	}
	return recorder.Code, decoded
}

func post(t *testing.T, handler http.HandlerFunc, body interface{}) (int, map[string]interface{}) {
	t.Helper()
	raw, _ := json.Marshal(body)
	return do(t, handler, admin(httptest.NewRequest("POST", "/cosmos/api/certificates/x", bytes.NewReader(raw))))
}

func data(answer map[string]interface{}) map[string]interface{} {
	d, _ := answer["data"].(map[string]interface{})
	return d
}
