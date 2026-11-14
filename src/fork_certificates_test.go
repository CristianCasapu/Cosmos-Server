package main

// Fork addition: the fork's certificate package is wired into the server.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Each of these is one line in a file of upstream's: a merge could drop it
// without any other test noticing.
func TestCertificateHooksAreInPlace(t *testing.T) {
	read := func(path string) string {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	between := func(text string, from string, to string) string {
		start := strings.Index(text, from)
		end := strings.Index(text, to)
		if start < 0 || end < start {
			t.Fatalf("%q … %q not found", from, to)
		}
		return text[start:end]
	}
	server := read("httpServer.go")

	local := between(server, "func issueLocalCerts(", "func localCertsCoverAll(")
	zones := between(server, "func issueZoneCerts(", "func refreshZoneCerts(")
	for name, body := range map[string]string{"issueLocalCerts": local, "issueZoneCerts": zones} {
		if !strings.Contains(body, "ValidUntil: utils.ForkCertExpiry(pub)") || strings.Contains(body, "AddDate(0, 0, 90)") {
			t.Errorf("%s must store the certificate's own end, not 90 days from now", name)
		}
		if !strings.Contains(body, "forkCertificateFailed(") || strings.Contains(body, "utils.MajorError(") {
			t.Errorf("%s must tell a failed order through forkCertificateFailed", name)
		}
		if !strings.Contains(body, "letsEncryptFailure(utils.LetsEncryptErrors)") {
			t.Errorf("the notice of %s must carry the reason", name)
		}
	}
	start := between(server, "func InitServer()", "func StartServer()")
	if !strings.Contains(start, "forkIssueZoneCerts(forceRenewal)") || strings.Contains(start, "\tissueZoneCerts(forceRenewal)") {
		t.Error("InitServer must order zone certificates in the background (forkIssueZoneCerts)")
	}
	if !strings.Contains(start, "registerCertificateRoutes(srapiAdmin)") {
		t.Error("the certificate routes are not registered")
	}

	if !strings.Contains(between(read("utils/certificates.go"), "func DoLetsEncrypt(", "LoadACMEAccount("), "return ForkObtain(domains, zone)") {
		t.Error("DoLetsEncrypt must hand the order to the fork's package")
	}
	status := read("system.go")
	if !strings.Contains(status, `"letsencrypt": forkLetsEncryptEmailNeeded()`) {
		t.Error("the status must not claim that Let's Encrypt needs an email address")
	}
}

func TestTheHomePageIsNotToldAnEmailIsMissing(t *testing.T) {
	previous := utils.GetBaseMainConfig()
	defer utils.LoadBaseMainConfig(previous)
	// the set-up of the server this fork is for: a domain with DNS access,
	// the panel on the domain's own name, no contact address
	config := utils.Config{}
	config.HTTPConfig.Hostname = "example.org"
	config.HTTPConfig.HTTPSCertificateMode = "LETSENCRYPT"
	config.HTTPConfig.SSLEmail = ""
	config.HTTPConfig.DNSZones = []utils.DNSZoneConfig{{Zone: "example.org", HTTPSCertificateMode: "LETSENCRYPT", DNSChallengeProvider: "cloudflare", UseWildcardCertificate: true}}
	utils.LoadBaseMainConfig(config)

	if !utils.NeedsLetsEncrypt(utils.GetMainConfig()) {
		t.Skip("the server does not order certificates in this set-up: upstream's alert would not show either")
	}
	if forkLetsEncryptEmailNeeded() {
		t.Error("the home page is told an email address is needed, though certificates are issued without one")
	}
}

func TestCertificateRoutesAreRegisteredAndConnected(t *testing.T) {
	obtain, due, may, noHTTP := utils.ForkObtain, utils.ForkCertDue, utils.ForkMayOrder, utils.ForkNoHTTPChallenge
	defer func() {
		utils.ForkObtain, utils.ForkCertDue, utils.ForkMayOrder, utils.ForkNoHTTPChallenge = obtain, due, may, noHTTP
	}()

	router := mux.NewRouter()
	registerCertificateRoutes(router)
	if utils.ForkObtain == nil || utils.ForkCertDue == nil || utils.ForkMayOrder == nil || utils.ForkNoHTTPChallenge == nil {
		t.Fatal("registering the routes did not connect the package to upstream's certificate code")
	}
	for _, path := range []string{"", "/providers", "/renew", "/domain", "/delegation", "/settings"} {
		for _, method := range []string{"GET", "POST"} {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(method, "/api/certificates"+path, strings.NewReader("{}")))
			// nobody is logged in: refused, never "not found" and never served
			if recorder.Code != http.StatusUnauthorized && recorder.Code != http.StatusForbidden && recorder.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s /api/certificates%s as nobody: status %d", method, path, recorder.Code)
			}
		}
	}
}
