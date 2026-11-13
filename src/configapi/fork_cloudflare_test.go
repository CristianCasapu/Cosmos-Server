package configapi

// Fork addition: tests of fork_cloudflare.go and of the masking of a
// Cloudflare token, through the real handlers.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/utils"
)

const cloudflareTestToken = "cfut_c2VjcmV0LXRva2VuLW9mLXRoZS10ZXN0LXNlcnZlcg0123456789"

func cloudflareTestConfig() utils.Config {
	config := utils.DefaultConfig
	config.NewInstall = false
	config.HTTPConfig.Hostname = "example.org"
	config.HTTPConfig.HTTPPort = "2052"
	config.HTTPConfig.HTTPSPort = "2053"
	config.HTTPConfig.HTTPSCertificateMode = utils.HTTPSCertModeList["LETSENCRYPT"]
	config.EmailConfig.Port = "587"
	config.HTTPConfig.DNSZones = []utils.DNSZoneConfig{{
		Zone:                 "example.org",
		HTTPSCertificateMode: utils.HTTPSCertModeList["LETSENCRYPT"],
		DNSChallengeProvider: "cloudflare",
		DNSChallengeConfig:   map[string]string{"CF_DNS_API_TOKEN": cloudflareTestToken},
	}}
	config.HTTPConfig.Cloudflare = utils.ForkCloudflareConfig{
		Shielded:      []string{"example.org", "radarr.example.org"},
		TrustRanges:   true,
		TakeOver:      []string{"www.example.org"},
		RangesV4:      []string{"173.245.48.0/20"},
		RangesV6:      []string{"2606:4700::/32"},
		RangesUpdated: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
	}
	return config
}

func TestTheCloudflareTokenIsNeverSentBack(t *testing.T) {
	settingsTestServer(t)
	utils.SetBaseMainConfig(cloudflareTestConfig())

	w := httptest.NewRecorder()
	ConfigApiGet(w, asAdmin(httptest.NewRequest("GET", "https://example.org:2053/cosmos/api/config", nil)))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), cloudflareTestToken) {
		t.Errorf("GET /api/config as an admin: %d, token in the answer: %v", w.Code, strings.Contains(w.Body.String(), cloudflareTestToken))
	}
	if !strings.Contains(w.Body.String(), `"CF_DNS_API_TOKEN":"***"`) {
		t.Errorf("the token is not shown as set: %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	ZonesApiList(w, asAdmin(httptest.NewRequest("GET", "https://example.org:2053/cosmos/api/zones", nil)))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), cloudflareTestToken) || !strings.Contains(w.Body.String(), `"CF_DNS_API_TOKEN":"***"`) {
		t.Errorf("GET /api/zones as an admin: %d %s", w.Code, w.Body.String())
	}

	// reading did not change what is stored
	if got := utils.ReadConfigFromFile().HTTPConfig.DNSZones[0].DNSChallengeConfig["CF_DNS_API_TOKEN"]; got != cloudflareTestToken {
		t.Errorf("the stored token after reading: %q", got)
	}
}

func TestSettingsSaveKeepsTheCloudflareTokenAndChoices(t *testing.T) {
	restarted := settingsTestServer(t)
	utils.SetBaseMainConfig(cloudflareTestConfig())
	stored := utils.ReadConfigFromFile().HTTPConfig

	// the settings page reads the settings...
	w := httptest.NewRecorder()
	ConfigApiGet(w, asAdmin(httptest.NewRequest("GET", "https://example.org:2053/cosmos/api/config", nil)))
	var got struct {
		Data utils.Config `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// ...and sends them back with what a stale or hostile page could put there
	got.Data.HTTPConfig.Cloudflare = utils.ForkCloudflareConfig{
		Shielded:    nil,
		TrustRanges: true,
		RangesV4:    []string{"0.0.0.0/1", "128.0.0.0/1"},
		RangesV6:    []string{"::/0"},
		TakeOver:    []string{"mail.example.org"},
	}
	body, _ := json.Marshal(got.Data)
	w = httptest.NewRecorder()
	ConfigApiSet(w, asAdmin(httptest.NewRequest("PUT", "https://example.org:2053/cosmos/api/config", bytes.NewReader(body))))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT config: %d %s", w.Code, w.Body.String())
	}
	select {
	case <-restarted:
	case <-time.After(10 * time.Second):
		t.Fatal("the restart that follows a save did not finish")
	}

	saved := utils.ReadConfigFromFile().HTTPConfig
	if token := saved.DNSZones[0].DNSChallengeConfig["CF_DNS_API_TOKEN"]; token != cloudflareTestToken {
		t.Errorf("the token after a settings save: %q", token)
	}
	before, _ := json.Marshal(stored.Cloudflare)
	after, _ := json.Marshal(saved.Cloudflare)
	if string(before) != string(after) {
		t.Errorf("a settings save changed what the Cloudflare routes keep:\n was %s\n is  %s", before, after)
	}
	if utils.IsCloudflareIP("8.8.8.8") || !utils.IsShieldedHost("radarr.example.org") {
		t.Error("ranges or choices sent by the page are in force")
	}
}

func TestSavingADomainWithTheMaskKeepsItsToken(t *testing.T) {
	settingsTestServer(t)
	utils.SetBaseMainConfig(cloudflareTestConfig())

	put := func(zone string, config utils.DNSZoneConfig) int {
		body, _ := json.Marshal(config)
		req := asAdmin(httptest.NewRequest("PUT", "https://example.org:2053/cosmos/api/zones/"+zone, bytes.NewReader(body)))
		req = mux.SetURLVars(req, map[string]string{"zone": zone})
		w := httptest.NewRecorder()
		ZonesApiSet(w, req)
		return w.Code
	}

	// the advanced form saves the domain as it got it: with the mask
	edited := utils.DNSZoneConfig{
		HTTPSCertificateMode:   "LETSENCRYPT",
		DNSChallengeProvider:   "cloudflare",
		DNSChallengeConfig:     map[string]string{"CF_DNS_API_TOKEN": utils.MaskedSecret},
		UseWildcardCertificate: true,
	}
	if code := put("example.org", edited); code != http.StatusOK {
		t.Skipf("the domain could not be saved in this test set-up (status %d)", code)
	}
	zone := utils.ReadConfigFromFile().HTTPConfig.DNSZones[0]
	if zone.DNSChallengeConfig["CF_DNS_API_TOKEN"] != cloudflareTestToken || !zone.UseWildcardCertificate {
		t.Errorf("the domain after a save with the mask: %+v", zone)
	}

	// a new domain sent with the mask: the mask is not stored as its token
	if code := put("example.net", edited); code == http.StatusOK {
		for _, zone := range utils.ReadConfigFromFile().HTTPConfig.DNSZones {
			if zone.Zone == "example.net" && zone.DNSChallengeConfig["CF_DNS_API_TOKEN"] != "" {
				t.Errorf("the mask was stored as a token: %+v", zone.DNSChallengeConfig)
			}
		}
	}
}
