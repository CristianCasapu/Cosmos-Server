package authorizationserver

import (
	"os"
	"strings"
	"testing"

	"github.com/ory/fosite"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestHomeNameReturnAddressesJoinTheRouteClient(t *testing.T) {
	cfg := utils.Config{}
	cfg.HTTPConfig.Hostname = "example.org"
	cfg.HTTPConfig.HTTPSPort = "2053"
	cfg.HTTPConfig.AuthPrivateKey = strings.Repeat("k", 128)
	cfg.HTTPConfig.DNSZones = []utils.DNSZoneConfig{{
		Zone: "example.org", HTTPSCertificateMode: utils.HTTPSCertModeList["LETSENCRYPT"],
		DNSChallengeProvider: "cloudflare", DNSChallengeConfig: map[string]string{"CF_DNS_API_TOKEN": "t"},
		ManageRecords: true,
	}}
	cfg.HTTPConfig.Cloudflare.HomeNames = []utils.ForkHomeNamesZone{{Zone: "example.org", Label: "home"}}
	gated := utils.ProxyRouteConfig{Name: "gated", UseHost: true, Host: "gated.local", Target: "http://x:1", AuthEnabled: true}
	open := utils.ProxyRouteConfig{Name: "open", UseHost: true, Host: "open.local", Target: "http://x:1"}
	cfg.HTTPConfig.ProxyConfig.Routes = []utils.ProxyRouteConfig{gated, open}
	previous := utils.GetMainConfig()
	utils.LoadBaseMainConfig(cfg)
	defer utils.LoadBaseMainConfig(previous)

	clients := map[string]fosite.Client{}
	original := utils.GetProxyOIDCredentials(gated, true)
	clients[original.ID] = original
	before := len(original.RedirectURIs)

	addHomeNameRedirects(clients)

	if len(clients) != 1 {
		t.Fatalf("a client was added for a home name: %d clients", len(clients))
	}
	client := clients[original.ID].(*fosite.DefaultClient)
	has := func(uri string) bool {
		for _, known := range client.RedirectURIs {
			if known == uri {
				return true
			}
		}
		return false
	}
	if !has("https://gated.local:2053/cosmos/oauth2/detect-callback") {
		t.Error("the route's own return address was lost")
	}
	if !has("https://gated.home.example.org:2053/cosmos/oauth2/detect-callback") || len(client.RedirectURIs) <= before {
		t.Errorf("the home name's return address is missing: %v", client.RedirectURIs)
	}
	for _, uri := range client.RedirectURIs {
		if strings.Contains(uri, "open.") {
			t.Errorf("a route without sign-in got an address: %s", uri)
		}
	}
}

func TestHomeNameClientsAreLinkedIn(t *testing.T) {
	source, err := os.ReadFile("oauth2.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "addHomeNameRedirects(store.Clients)") {
		t.Error("Init does not register the return addresses of the home names")
	}
}
