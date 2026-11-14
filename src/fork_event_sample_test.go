package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: an API token used many times leaves one event row a minute
// (proxy/fork_event_sample.go), not one per request.
func TestTokenUseLeavesOneEventRowPerMinute(t *testing.T) {
	const token = "cosmos_fedcba9876543210fedcba9876543210"
	sum := sha256.Sum256([]byte(token))
	prev := utils.GetBaseMainConfig()
	config := utils.Config{}
	config.HTTPConfig.Hostname = "cosmos.example.com"
	config.APITokens = map[string]utils.APITokenConfig{
		"fork-sampled": {Name: "fork-sampled", Owner: "admin", TokenHash: hex.EncodeToString(sum[:]), Permissions: []utils.Permission{utils.PERM_ADMIN}},
	}
	utils.LoadBaseMainConfig(config)
	prevFolder := utils.CONFIGFOLDER
	utils.CONFIGFOLDER = t.TempDir() + "/"
	if err := utils.InitMetricsDatabase(); err != nil {
		t.Fatal(err)
	}
	// the token's owner must exist (fork_token_owner.go)
	if err := utils.InitStore(); err != nil {
		t.Fatal("InitStore:", err)
	}
	if err := utils.CreateUser(utils.User{Nickname: "admin", Password: "x", Role: utils.ADMIN}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		utils.CloseStore()
		utils.CloseMetricsDatabase()
		utils.CONFIGFOLDER = prevFolder
		utils.LoadBaseMainConfig(prev)
	})

	reached := 0
	handler := tokenMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if utils.GetAuthContext(r).APIToken != nil {
			reached++
		}
	}))
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest("GET", "https://cosmos.example.com/cosmos/api/status", nil)
		req.Host = "cosmos.example.com"
		req.RemoteAddr = "192.168.1.20:50000"
		req.Header.Set("Authorization", "Bearer "+token)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
	if reached != 100 {
		t.Fatalf("%d of 100 requests were let in with the token", reached)
	}

	events, total, err := utils.QueryEvents(utils.EventQuery{
		From:   time.Now().Add(-time.Hour),
		To:     time.Now().Add(time.Hour),
		Filter: map[string]interface{}{"object": "token@fork-sampled"},
		Limit:  200,
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || events[0].EventId != "cosmos.api.token.used" || events[0].Data["tokenName"] != "fork-sampled" {
		t.Fatalf("100 uses of the token left %d rows (%+v), want one", total, events)
	}
}
