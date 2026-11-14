package configapi

// Fork addition: tests of fork_turnstile.go, through the real handlers.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const turnstileTestSecret = "0x4AAAAAAAsecretOfTheTestServer0123"

func turnstileTestConfig() utils.Config {
	config := cloudflareTestConfig()
	config.Turnstile = utils.ForkTurnstileConfig{
		Enabled: true, SiteKey: "0x4AAAAAAAsiteKeyOfTheTest", SecretKey: turnstileTestSecret,
		Confirmed: utils.ForkTurnstileConfirmed{Keys: "0123456789abcdef", Hostname: "example.org"},
	}
	return config
}

func TestTheTurnstileSecretIsNeverSentBack(t *testing.T) {
	settingsTestServer(t)
	utils.SetBaseMainConfig(turnstileTestConfig())

	w := httptest.NewRecorder()
	ConfigApiGet(w, asAdmin(httptest.NewRequest("GET", "https://example.org:2053/cosmos/api/config", nil)))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), turnstileTestSecret) || strings.Contains(w.Body.String(), "0123456789abcdef") {
		t.Errorf("GET /api/config: %d, the secret or its hash in the answer", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"SecretKey":"***"`) {
		t.Errorf("the secret is not shown as set")
	}
	if got := utils.ReadConfigFromFile().Turnstile.SecretKey; got != turnstileTestSecret {
		t.Errorf("the stored secret after reading: %q", got)
	}
}

func TestSettingsSaveKeepsTheTurnstileBlock(t *testing.T) {
	restarted := settingsTestServer(t)
	utils.SetBaseMainConfig(turnstileTestConfig())
	stored := utils.ReadConfigFromFile().Turnstile

	w := httptest.NewRecorder()
	ConfigApiGet(w, asAdmin(httptest.NewRequest("GET", "https://example.org:2053/cosmos/api/config", nil)))
	var got struct {
		Data utils.Config `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// a stale or hostile page: the mask, its own keys, switched off, a fake round trip
	got.Data.Turnstile.Enabled = false
	got.Data.Turnstile.SiteKey = "1x00000000000000000000AA"
	got.Data.Turnstile.Confirmed.Keys = "forged"
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
	before, _ := json.Marshal(stored)
	after, _ := json.Marshal(utils.ReadConfigFromFile().Turnstile)
	if string(before) != string(after) {
		t.Errorf("a settings save changed the Turnstile block:\n was %s\n is  %s", before, after)
	}
}
