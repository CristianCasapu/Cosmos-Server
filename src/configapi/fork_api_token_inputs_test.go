package configapi

// Fork addition: tests of fork_api_token_inputs.go, through the handlers.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestATokenTakesABoundedLifetimeAndRealAddresses(t *testing.T) {
	settingsTestServer(t)
	config := hostPowersConfig()
	config.APITokens = map[string]utils.APITokenConfig{"ci": {Name: "ci", Owner: "admin", TokenHash: "HASH", Permissions: []utils.Permission{utils.PERM_ADMIN_READ}}}
	utils.SaveConfigTofile(config)
	utils.LoadBaseMainConfig(config)

	create := func(body string) int {
		w := httptest.NewRecorder()
		createAPIToken(w, asAdmin(httptest.NewRequest("POST", "https://cosmos.example.com/cosmos/api/api-tokens", bytes.NewReader([]byte(body)))))
		return w.Code
	}
	update := func(body string) int {
		req := asAdmin(httptest.NewRequest("PUT", "https://cosmos.example.com/cosmos/api/api-tokens/ci", bytes.NewReader([]byte(body))))
		req = mux.SetURLVars(req, map[string]string{"name": "ci"})
		w := httptest.NewRecorder()
		updateAPIToken(w, req)
		return w.Code
	}

	for name, body := range map[string]string{
		"a lifetime that overflows":         `{"name":"a","permissions":[1],"expiryDays":3000000000}`,
		"a lifetime of ten years and a day": `{"name":"b","permissions":[1],"expiryDays":3651}`,
		"a negative lifetime":               `{"name":"c","permissions":[1],"expiryDays":-1}`,
		"an address with a typo":            `{"name":"d","permissions":[1],"ipWhitelist":["192.168.1.300"]}`,
		"a host name as address":            `{"name":"e","permissions":[1],"ipWhitelist":["example.com"]}`,
		"a range with a typo":               `{"name":"f","permissions":[1],"ipWhitelist":["10.0.0.0/33"]}`,
	} {
		if code := create(body); code != http.StatusBadRequest {
			t.Errorf("creating a token with %s: %d, want 400", name, code)
		}
	}
	if _, exists := utils.ReadConfigFromFile().APITokens["a"]; exists {
		t.Error("a refused token was stored")
	}
	if code := update(`{"ipWhitelist":["not an address"]}`); code != http.StatusBadRequest {
		t.Errorf("changing a token's addresses to a typo: %d, want 400", code)
	}
	if got := utils.ReadConfigFromFile().APITokens["ci"].IPWhitelist; len(got) != 0 {
		t.Errorf("a refused change was stored: %v", got)
	}

	// what is right still goes through
	if problem := tokenExpiryProblem(3650); problem != "" {
		t.Errorf("ten years: %s", problem)
	}
	if problem := tokenExpiryProblem(0); problem != "" {
		t.Errorf("no expiry: %s", problem)
	}
	kept, problem := cleanTokenWhitelist([]string{" 192.168.1.20 ", "10.0.0.0/8", "", "2001:db8::/32", "::1"})
	if problem != "" || len(kept) != 4 || kept[0] != "192.168.1.20" {
		t.Errorf("a list of right addresses: %v %q", kept, problem)
	}
}
