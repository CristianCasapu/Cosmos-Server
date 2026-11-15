package configapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// TestSettingsSaveKeepsTheReachabilityWatch: the watch's switch and interval
// are written by its own route only; a settings page that sends them back
// changed (or empty) changes nothing.
func TestSettingsSaveKeepsTheReachabilityWatch(t *testing.T) {
	restarted := settingsTestServer(t)
	config := cloudflareTestConfig()
	config.HTTPConfig.Cloudflare.Watch = utils.ForkReachWatchConfig{Off: true, Minutes: 30}
	utils.SetBaseMainConfig(config)

	w := httptest.NewRecorder()
	ConfigApiGet(w, asAdmin(httptest.NewRequest("GET", "https://example.org:2053/cosmos/api/config", nil)))
	var got struct {
		Data utils.Config `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	got.Data.HTTPConfig.Cloudflare.Watch = utils.ForkReachWatchConfig{Minutes: 1}
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
	if saved := utils.ReadConfigFromFile().HTTPConfig.Cloudflare.Watch; !saved.Off || saved.Minutes != 30 {
		t.Errorf("a settings save changed the watch: %+v", saved)
	}
}
