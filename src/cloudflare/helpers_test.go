package cloudflare

// Fork addition: what the tests of this package share. Nothing here reaches
// the network: every call goes to cffake, and a call to any other host than
// Cloudflare's API fails the test.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/azukaar/cosmos-server/src/cloudflare/cffake"
	"github.com/azukaar/cosmos-server/src/dnsrecords"
	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	goodToken   = "cfut_Zm9ya3Rlc3R0b2tlbl9mb3JrdGVzdHRva2VuXzAxMjM0NQ12345678"
	readToken   = "readonly0123456789abcdefghijklmnopqrstuvwxYZ"
	otherToken  = "another0123456789abcdefghijklmnopqrstuvwxYZ"
	testAddress = "203.0.113.10"
)

// toFake sends what is meant for Cloudflare's API to the stand-in and
// notices anything meant for another host.
type toFake struct {
	t      *testing.T
	target *url.URL
	mu     sync.Mutex
	others []string
}

func (f *toFake) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.Host != "api.cloudflare.com" {
		f.mu.Lock()
		f.others = append(f.others, req.URL.Host)
		f.mu.Unlock()
		f.t.Errorf("a request left for %s://%s, not for Cloudflare's API", req.URL.Scheme, req.URL.Host)
	}
	clone := req.Clone(req.Context())
	clone.URL.Scheme = f.target.Scheme
	clone.URL.Host = f.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

type testEnv struct {
	fake   *cffake.Server
	server *httptest.Server
}

// setup starts a stand-in with the zone example.org (an apex record, a www
// alias, a mail record: the owner's website), a token that may edit it, one
// that may only read it, and a configuration of Cosmos in a folder of its own.
func setup(t *testing.T, change func(config *utils.Config)) *testEnv {
	t.Helper()

	fake := cffake.New()
	fake.AddZone("example.org",
		cffake.Record{Type: "A", Name: "example.org", Content: "198.51.100.20", Proxied: true},
		cffake.Record{Type: "CNAME", Name: "www.example.org", Content: "example.org", Proxied: true},
		cffake.Record{Type: "MX", Name: "example.org", Content: "mail.example.org"},
		cffake.Record{Type: "A", Name: "mail.example.org", Content: "198.51.100.25"},
		cffake.Record{Type: "TXT", Name: "example.org", Content: `"v=spf1 mx -all"`},
	)
	fake.AddZone("example.net")
	fake.AddToken(goodToken, cffake.Token{Zones: []string{"example.org"}, ReadDNS: true, EditDNS: true})
	fake.AddToken(readToken, cffake.Token{Zones: []string{"example.org"}, ReadDNS: true})
	fake.AddToken(otherToken, cffake.Token{ReadDNS: true, EditDNS: true, ReadSettings: true})

	server := httptest.NewServer(fake)
	target, _ := url.Parse(server.URL)
	previousTransport := apiTransport
	apiTransport = &toFake{t: t, target: target}

	previousFolder := utils.CONFIGFOLDER
	previousConfig := utils.GetBaseMainConfig()
	utils.CONFIGFOLDER = t.TempDir() + "/"

	config := utils.Config{}
	config.HTTPConfig.Hostname = "192.168.1.10"
	config.HTTPConfig.HTTPPort = "2052"
	config.HTTPConfig.HTTPSPort = "2053"
	config.HTTPConfig.HTTPSCertificateMode = "LETSENCRYPT"
	config.HTTPConfig.AuthPublicKey = "test-public-key"
	config.HTTPConfig.AdvertisedAddress = testAddress
	config.HTTPConfig.DNSZones = []utils.DNSZoneConfig{{
		Zone:                 "example.org",
		HTTPSCertificateMode: "LETSENCRYPT",
		DNSChallengeProvider: "cloudflare",
		DNSChallengeConfig:   map[string]string{"CF_DNS_API_TOKEN": goodToken},
		ManageRecords:        true,
	}}
	config.HTTPConfig.ProxyConfig.Routes = []utils.ProxyRouteConfig{
		{Name: "radarr", UseHost: true, Host: "radarr.local", Mode: "SERVAPP", Target: "http://radarr:7878"},
		{Name: "radarr-example.org", UseHost: true, Host: "radarr.example.org", Mode: "SERVAPP", Target: "http://radarr:7878"},
		{Name: "jellyfin-example.org", UseHost: true, Host: "jellyfin.example.org", Mode: "SERVAPP", Target: "http://jellyfin:8096"},
	}
	if change != nil {
		change(&config)
	}
	utils.SetBaseMainConfig(config)

	previousAddress := knownAddress
	knownAddress = func() string { return testAddress }
	previousTrigger, previousRestart := triggerReconcile, restartServer
	triggerReconcile, restartServer = func() {}, func() {}
	previousRouter, previousReload, previousPublished := routerChanged, reloadRoutes, publishedAddress
	routerChanged, reloadRoutes, publishedAddress = func([]string) {}, func() {}, func() string { return testAddress }
	restoreRecords := dnsrecords.ForkTestSetup(func(name string, _ []string) (string, error) {
		return "example.org", nil
	})

	syncMu.Lock()
	synced = map[string]*zoneSync{}
	syncMu.Unlock()

	t.Cleanup(func() {
		server.Close()
		apiTransport = previousTransport
		knownAddress = previousAddress
		triggerReconcile, restartServer = previousTrigger, previousRestart
		routerChanged, reloadRoutes, publishedAddress = previousRouter, previousReload, previousPublished
		restoreRecords()
		utils.CONFIGFOLDER = previousFolder
		utils.LoadBaseMainConfig(previousConfig)
		syncMu.Lock()
		synced = map[string]*zoneSync{}
		syncMu.Unlock()
	})
	return &testEnv{fake: fake, server: server}
}

// pass runs one pass of upstream's reconciler for the domain example.org, the
// way its loop does.
func pass(t *testing.T) {
	t.Helper()
	config := utils.GetMainConfig()
	zones := config.HTTPConfig.DNSZones
	hosts := map[string][]string{}
	for _, host := range utils.GetAllHostnames(true) {
		hosts[strings.ToLower(host)] = []string{config.HTTPConfig.AdvertisedAddress}
	}
	hosts = dnsrecords.ForkMoreHosts(hosts) // what the loop's pass adds: the names of exposed ports
	for i, zone := range zones {
		if zone.ManageRecords {
			dnsrecords.Reconcile(zones, i, dnsrecords.BuildDesired(zones, i, hosts, []string{config.HTTPConfig.AdvertisedAddress}))
		}
	}
}

// find returns the records of a name and type in the stand-in's zone.
func (env *testEnv) find(name string, recordType string) []cffake.Record {
	out := []cffake.Record{}
	for _, record := range env.fake.Records("example.org") {
		if record.Name == name && record.Type == recordType {
			out = append(out, record)
		}
	}
	return out
}

func admin(req *http.Request, permissions ...utils.Permission) *http.Request {
	if len(permissions) == 0 {
		permissions = []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN_READ, utils.PERM_CONFIGURATION_READ, utils.PERM_CONFIGURATION}
	}
	return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{
		Nickname: "admin", Permissions: permissions, IsSudoed: true,
	}))
}

// post calls a handler as an admin with a JSON body and returns the status
// and the decoded answer.
func post(t *testing.T, handler http.HandlerFunc, body interface{}) (int, map[string]interface{}) {
	t.Helper()
	raw, _ := json.Marshal(body)
	return do(t, handler, admin(httptest.NewRequest("POST", "/cosmos/api/cloudflare/x", bytes.NewReader(raw))))
}

func do(t *testing.T, handler http.HandlerFunc, req *http.Request) (int, map[string]interface{}) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler(recorder, req)
	var decoded map[string]interface{}
	json.Unmarshal(recorder.Body.Bytes(), &decoded)
	if strings.Contains(recorder.Body.String(), goodToken) || strings.Contains(recorder.Body.String(), readToken) {
		t.Errorf("an answer contains the token: %s", recorder.Body.String())
	}
	return recorder.Code, decoded
}

func data(answer map[string]interface{}) map[string]interface{} {
	d, _ := answer["data"].(map[string]interface{})
	return d
}
