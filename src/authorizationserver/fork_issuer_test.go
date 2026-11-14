package authorizationserver

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// On a server reached on 2053 the discovery document and the ID token name
// the same issuer, with the port; upstream said https://example.org for both
// endpoints and token (port 443, another machine).
func TestIssuerNamesThePortInUse(t *testing.T) {
	realConfig, realHTTPS := utils.GetMainConfig(), utils.IsHTTPS
	t.Cleanup(func() { utils.LoadBaseMainConfig(realConfig); utils.IsHTTPS = realHTTPS })
	config := utils.Config{}
	config.HTTPConfig.Hostname = "example.org"
	config.HTTPConfig.HTTPSPort = "2053"
	config.HTTPConfig.HTTPPort = "2052"
	utils.LoadBaseMainConfig(config)
	utils.IsHTTPS = true

	for host, want := range map[string]string{
		"example.org:2053": "https://example.org:2053",
		// a router forwarding 443 to 2053: the visitor named no port
		"example.org":      "https://example.org",
		"example.org:8443": "https://example.org:8443",
	} {
		req := httptest.NewRequest("GET", "https://placeholder/.well-known/openid-configuration", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		discoverEndpoint(rec, req)
		var doc map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		if doc["issuer"] != want || doc["token_endpoint"] != want+"/oauth2/token" || doc["jwks_uri"] != want+"/.well-known/jwks.json" {
			t.Fatalf("%s: issuer %v, token %v, jwks %v; want %s", host, doc["issuer"], doc["token_endpoint"], doc["jwks_uri"], want)
		}
		session := newSession("someone", req)
		if session == nil {
			t.Fatalf("%s: no session", host)
		}
		if got := session.Claims.Issuer; got != want {
			t.Fatalf("%s: token issuer %q, discovery %q", host, got, want)
		}
	}
}

// A request on another name than the panel's keeps upstream's issuer.
func TestIssuerOfAnotherNameUnchanged(t *testing.T) {
	realConfig := utils.GetMainConfig()
	t.Cleanup(func() { utils.LoadBaseMainConfig(realConfig) })
	config := utils.Config{}
	config.HTTPConfig.Hostname = "example.org"
	config.HTTPConfig.HTTPSPort = "2053"
	utils.LoadBaseMainConfig(config)
	req := httptest.NewRequest("GET", "https://placeholder/", nil)
	req.Host = "app.example.org:2053"
	if _, ok := panelIssuer(req); ok {
		t.Fatal("an app's name taken for the panel's")
	}
}
