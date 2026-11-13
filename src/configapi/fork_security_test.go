package configapi

// Fork addition: tests of what the settings code reports to the security
// record (fork_security.go).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func captureSecurity(t *testing.T) func() []utils.SecurityEvent {
	t.Helper()
	var mu sync.Mutex
	events := []utils.SecurityEvent{}
	utils.SetSecurityRecorder(func(event utils.SecurityEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}, func() {})
	t.Cleanup(func() { utils.SetSecurityRecorder(nil, nil) })
	return func() []utils.SecurityEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]utils.SecurityEvent(nil), events...)
	}
}

func adminRequest(method string, path string) *http.Request {
	req := httptest.NewRequest(method, "http://cosmos.example"+path, nil)
	return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{Nickname: "alice"}))
}

func securityTestConfig() utils.Config {
	config := utils.Config{}
	config.HTTPConfig.Hostname = "cosmos.example"
	config.HTTPConfig.TLSKey = "-----BEGIN PRIVATE KEY----- secret-key-material"
	config.EmailConfig.Host = "smtp.example.com"
	config.EmailConfig.Password = "smtp-secret-password"
	config.HTTPConfig.ProxyConfig.Routes = []utils.ProxyRouteConfig{{Name: "media", Host: "media.example", Target: "http://127.0.0.1:8096"}}
	return config
}

func TestSecurityChangedSectionsNamesWhatChanged(t *testing.T) {
	before := securityTestConfig()

	same := securityTestConfig()
	if changed := changedSections(before, same); len(changed) != 0 {
		t.Fatalf("an unchanged configuration is said to have changed: %v", changed)
	}

	// what the form does to a configuration that had nothing in a list: [] and {} for null
	roundTrip := securityTestConfig()
	roundTrip.BlockedCountries = []string{}
	roundTrip.HTTPConfig.TrustedProxies = []string{}
	roundTrip.APITokens = map[string]utils.APITokenConfig{}
	roundTrip.HTTPConfig.ProxyConfig.Routes[0].WhitelistInboundIPs = []string{}
	if changed := changedSections(before, roundTrip); len(changed) != 0 {
		t.Fatalf("empty lists in place of none count as a change: %v", changed)
	}

	after := securityTestConfig()
	after.EmailConfig.Password = "another-smtp-password"
	after.HTTPConfig.Hostname = "new.example"
	after.HTTPConfig.ProxyConfig.Routes = append(after.HTTPConfig.ProxyConfig.Routes, utils.ProxyRouteConfig{Name: "books"})
	after.BlockedCountries = []string{"XX"}
	changed := changedSections(before, after)
	if got := strings.Join(changed, ", "); got != "BlockedCountries, EmailConfig, HTTPConfig.Hostname, HTTPConfig.ProxyConfig" {
		t.Fatalf("changed sections = %s", got)
	}
}

func TestSecuritySettingsEntryNeverCarriesValues(t *testing.T) {
	recorded := captureSecurity(t)
	before, after := securityTestConfig(), securityTestConfig()
	after.EmailConfig.Password = "another-smtp-password"
	after.HTTPConfig.TLSKey = "-----BEGIN PRIVATE KEY----- other-key-material"
	after.HTTPConfig.Hostname = "new.example"

	auditSettings(adminRequest("PUT", "/cosmos/api/config"), before, after)
	auditSettings(adminRequest("PUT", "/cosmos/api/config"), before, before)

	events := recorded()
	if len(events) != 2 || events[0].Kind != utils.SecConfigSaved || events[0].Actor != "alice" {
		t.Fatalf("events = %+v", events)
	}
	if got := events[0].Details["sections"]; got != "EmailConfig, HTTPConfig.Hostname, HTTPConfig.TLSKey" {
		t.Fatalf("sections = %q", got)
	}
	if got := events[1].Details["sections"]; got != "(none)" {
		t.Fatalf("sections of a save that changed nothing = %q", got)
	}
	for _, event := range events {
		for _, value := range event.Details {
			for _, secret := range []string{"smtp-secret", "another-smtp", "key-material", "new.example", "smtp.example.com"} {
				if strings.Contains(value, secret) {
					t.Fatalf("a value of the settings reached the record: %q", value)
				}
			}
		}
	}

	// a save that touches everything names a bounded number of sections
	everything := utils.Config{}
	everything.HTTPConfig.Hostname = "x"
	auditSettings(adminRequest("PUT", "/cosmos/api/config"), fullyChanged(), everything)
	if names := strings.Split(recorded()[2].Details["sections"], ", "); len(names) > maxChangedSections+1 {
		t.Fatalf("%d sections named, want at most %d and an ellipsis", len(names), maxChangedSections)
	}
}

// fullyChanged differs from an empty configuration in many sections
func fullyChanged() utils.Config {
	config := securityTestConfig()
	config.LoggingLevel = "DEBUG"
	config.RequireMFA = true
	config.AutoUpdate = true
	config.BlockedCountries = []string{"XX"}
	config.ServerCountry = "RO"
	config.Timezone = "Europe/Bucharest"
	config.MonitoringDisabled = true
	config.BackupOutputDir = "/backups"
	config.AdminWhitelistIPs = []string{"192.0.2.0/24"}
	config.HTTPConfig.HTTPPort = "80"
	config.HTTPConfig.HTTPSPort = "443"
	config.HTTPConfig.TrustedProxies = []string{"192.0.2.1"}
	config.HTTPConfig.AllowSearchEngine = true
	config.HTTPConfig.PublishMDNS = true
	config.DockerConfig.SkipPruneNetwork = true
	return config
}

func TestSecurityRoutePatchKinds(t *testing.T) {
	recorded := captureSecurity(t)
	req := adminRequest("PATCH", "/cosmos/api/config")

	auditRoutePatch(req, UpdateRouteRequest{Operation: "add", NewRoute: &utils.ProxyRouteConfig{Name: "books"}})
	auditRoutePatch(req, UpdateRouteRequest{Operation: "replace", RouteName: "books", NewRoute: &utils.ProxyRouteConfig{Name: "library"}})
	auditRoutePatch(req, UpdateRouteRequest{Operation: "move_up", RouteName: "books"})
	auditRoutePatch(req, UpdateRouteRequest{Operation: "delete", RouteName: "books"})
	auditRoutePatch(req, UpdateRouteRequest{Operation: "explode", RouteName: "books"})

	got := []string{}
	for _, event := range recorded() {
		got = append(got, event.Kind+" "+event.Target+" "+event.Details["operation"])
	}
	want := []string{
		utils.SecRouteCreated + " books ",
		utils.SecRouteUpdated + " books replace",
		utils.SecRouteUpdated + " books move_up",
		utils.SecRouteDeleted + " books ",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("recorded:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
