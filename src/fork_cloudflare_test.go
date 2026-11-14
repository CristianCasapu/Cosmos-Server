package main

// Fork addition: the Cloudflare integration is wired into the server.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/libdns/libdns"

	"github.com/azukaar/cosmos-server/src/dnsrecords"
	"github.com/azukaar/cosmos-server/src/reach"
	"github.com/azukaar/cosmos-server/src/utils"
)

// The rule that the domain's own name is never written depends on a hook the
// reconciler calls: if the integration were not linked in, upstream's rule
// (whatever sits at a served name is replaced) would be back.
func TestTheReconcilerNeverSeesTheApexOfACloudflareDomain(t *testing.T) {
	zone := utils.DNSZoneConfig{Zone: "example.org", DNSChallengeProvider: "cloudflare", ManageRecords: true}
	desired := dnsrecords.Desired{"example.org": {"203.0.113.10"}, "radarr.example.org": {"203.0.113.10"}, "www.example.org": {"203.0.113.10"}}
	actual := []libdns.Record{
		libdns.RR{Name: "@", Type: "A", Data: "203.0.113.10"},
		libdns.RR{Name: "_cosmos-owner", Type: "TXT", Data: "managed by cosmos " + dnsrecords.OwnerID()},
		libdns.RR{Name: "www", Type: "CNAME", Data: "example.org."},
		libdns.RR{Name: "mail", Type: "A", Data: "198.51.100.25"},
	}

	kept, visible := dnsrecords.ForkFilterDesired(zone, "example.org", desired, actual)
	if _, wanted := kept["example.org"]; wanted || len(kept) != 1 || kept["radarr.example.org"] == nil {
		t.Fatalf("what a pass may write: %v", kept)
	}
	for _, record := range visible {
		if name := record.RR().Name; name == "@" || name == "_cosmos-owner" || name == "www" {
			t.Errorf("a pass can see %s %s", record.RR().Type, name)
		}
	}
	changes := dnsrecords.Plan("example.org", kept, visible, dnsrecords.DefaultTTL, true, dnsrecords.OwnerID())
	for _, record := range append(append(changes.Delete, changes.Append...), changes.Withdraw...) {
		if name := record.RR().Name; name == "@" || name == "_cosmos-owner" || name == "www" {
			t.Errorf("the plan touches %s %s", record.RR().Type, name)
		}
	}

	// other providers keep upstream's behaviour
	zone.DNSChallengeProvider = "ovh"
	if kept, visible := dnsrecords.ForkFilterDesired(zone, "example.org", desired, actual); len(kept) != 3 || len(visible) != 4 {
		t.Errorf("another provider was filtered: %v", kept)
	}
}

func TestCloudflareIsWiredIn(t *testing.T) {
	if reach.Cloudflare == nil || reach.IsCloudflareAddress == nil {
		t.Error("the reachability check does not know about Cloudflare")
	}

	router := mux.NewRouter()
	registerCloudflareRoutes(router.PathPrefix("/cosmos").Subrouter())
	for path, method := range map[string]string{
		"/cosmos/api/cloudflare":              "GET",
		"/cosmos/api/cloudflare/check":        "POST",
		"/cosmos/api/cloudflare/preview":      "POST",
		"/cosmos/api/cloudflare/takeover":     "POST",
		"/cosmos/api/cloudflare/shield":       "POST",
		"/cosmos/api/cloudflare/panel":        "POST",
		"/cosmos/api/cloudflare/ranges":       "POST",
		"/cosmos/api/cloudflare/origin-pulls": "POST",
		"/cosmos/api/expose":                  "POST",
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(method, path, strings.NewReader("{}")))
		// nobody is logged in: every route refuses
		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a login: status %d", method, path, recorder.Code)
		}
	}

	source, err := os.ReadFile("httpServer.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, hook := range []string{"router.Use(proxy.CloudflareOriginGuard)", "registerCloudflareRoutes(srapiAdmin)", "TLSConfig: originPullTLS(&tls.Config{"} {
		if strings.Count(string(source), hook) != 1 {
			t.Errorf("httpServer.go: %q is not there exactly once", hook)
		}
	}
	// the guard stands in front of everything that answers for a hostname
	if strings.Index(string(source), "router.Use(proxy.CloudflareOriginGuard)") > strings.Index(string(source), "proxy.BuildFromConfig(router") {
		t.Error("the guard is added after the URLs")
	}
}

// The names of ports exposed directly reach the DNS pass through one line in
// upstream's loop (src/dnsrecords/run.go); without it they get no record.
func TestExposedPortNamesAreHookedIntoTheDNSPass(t *testing.T) {
	source, err := os.ReadFile("dnsrecords/run.go")
	if err != nil {
		t.Fatal(err)
	}
	pass := string(source)
	hook := strings.Index(pass, "hosts = ForkMoreHosts(hosts)")
	if hook == -1 || hook < strings.Index(pass, "hosts, wildcard, ok := HostAddresses()") {
		t.Error("the hook that adds the names of exposed ports to the DNS pass is gone")
	}
}
