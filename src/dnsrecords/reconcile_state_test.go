package dnsrecords

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/libdns/libdns"

	"github.com/azukaar/cosmos-server/src/utils"
)

// fakeProvider holds zones in memory, by name with the trailing dot
type fakeProvider struct {
	zones map[string][]libdns.Record
	err   error
	calls int
}

func (p *fakeProvider) GetRecords(_ context.Context, zone string) ([]libdns.Record, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	records, ok := p.zones[zone]
	if !ok {
		return nil, errors.New("expected 1 zone, got 0 for " + zone)
	}
	return append([]libdns.Record{}, records...), nil
}

func (p *fakeProvider) AppendRecords(_ context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	p.zones[zone] = append(p.zones[zone], records...)
	return records, nil
}

func (p *fakeProvider) DeleteRecords(_ context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	kept := []libdns.Record{}
	for _, record := range p.zones[zone] {
		gone := false
		for _, target := range records {
			gone = gone || record.RR() == target.RR()
		}
		if !gone {
			kept = append(kept, record)
		}
	}
	p.zones[zone] = kept
	return records, nil
}

// withFakeProvider plugs the provider "fake" in, as a writer past its warm-up.
// zoneOf is what the DNS answers: the zone each domain lives in.
func withFakeProvider(t *testing.T, provider *fakeProvider, zoneOf map[string]string) {
	t.Helper()

	previousLookup, previousOwner, previousMajor := lookupZone, OwnerID, majorError
	adapters["fake"] = adapter{build: func(func(...string) string) (Provider, error) { return provider, nil }}
	lookupZone = func(name string, _ []string) (string, error) {
		if zone, ok := zoneOf[name]; ok {
			return zone, nil
		}
		return "", errors.New("no answer")
	}
	OwnerID = func() string { return "me" }
	majorError = func(string, error) {}

	stateLock.Lock()
	zoneStates = map[string]*zoneState{}
	writerSince = time.Now().Add(-time.Hour)
	stateLock.Unlock()

	t.Cleanup(func() {
		delete(adapters, "fake")
		lookupZone, OwnerID, majorError = previousLookup, previousOwner, previousMajor
		ResetWriter()
	})
}

func fakeZone(name string) utils.DNSZoneConfig {
	return utils.DNSZoneConfig{Zone: name, DNSChallengeProvider: "fake", ManageRecords: true}
}

func TestFindProviderZone(t *testing.T) {
	answers := map[string]string{
		"cluster.example.com": "example.com",
		"example.com":         "example.com",
		// not delegated yet: the lookup walks up to the registry
		"undelegated.com": "com",
		"shop.co.uk":      "co.uk",
		// its own zone under a suffix anyone can register in
		"me.dedyn.io":      "me.dedyn.io",
		"lost.example.org": "elsewhere.net",
	}
	var asked [][]string
	previous := lookupZone
	lookupZone = func(name string, resolvers []string) (string, error) {
		asked = append(asked, resolvers)
		if zone, ok := answers[name]; ok {
			return zone, nil
		}
		return "", errors.New("no answer")
	}
	defer func() { lookupZone = previous }()

	for domain, want := range map[string]string{
		"cluster.example.com": "example.com",
		"Example.com.":        "example.com",
		"me.dedyn.io":         "me.dedyn.io",
	} {
		if got, err := findProviderZone(utils.DNSZoneConfig{Zone: domain}); got != want || err != nil {
			t.Errorf("zone of %s = %q, %v, want %q", domain, got, err, want)
		}
	}

	// no usable answer: the domain itself, and why
	for _, domain := range []string{"undelegated.com", "shop.co.uk", "lost.example.org", "offline.example.net"} {
		if got, err := findProviderZone(utils.DNSZoneConfig{Zone: domain}); got != domain || err == nil {
			t.Errorf("zone of %s = %q, %v, want the domain itself and a reason", domain, got, err)
		}
	}

	// the resolvers of the server first, then the public ones, unless the domain has its own
	asked = nil
	findProviderZone(utils.DNSZoneConfig{Zone: "offline.example.net"})
	if want := [][]string{nil, publicResolvers}; !reflect.DeepEqual(asked, want) {
		t.Errorf("resolvers asked = %v, want %v", asked, want)
	}
	asked = nil
	findProviderZone(utils.DNSZoneConfig{Zone: "offline.example.net", DNSChallengeResolvers: " 9.9.9.9:53 , 10.0.0.1 "})
	if want := [][]string{{"9.9.9.9:53", "10.0.0.1"}}; !reflect.DeepEqual(asked, want) {
		t.Errorf("resolvers asked = %v, want %v", asked, want)
	}
}

func TestScopeRecords(t *testing.T) {
	zones := []utils.DNSZoneConfig{fakeZone("example.com"), fakeZone("cluster.example.com")}
	actual := []libdns.Record{
		a("@", "1.1.1.1"), ownedBy("@", "me"),
		a("www", "1.1.1.1"), ownedBy("www", "me"),
		a("*", "1.1.1.1"), ownedBy("*", "me"),
		a("cluster", "2.2.2.2"), ownedBy("cluster", "me"),
		a("node-1.cluster", "2.2.2.2"), ownedBy("node-1.cluster", "me"),
		a("*.cluster", "2.2.2.2"), ownedBy("*.cluster", "me"),
		libdns.TXT{Name: "_acme-challenge.cluster", TTL: testTTL, Text: "token"},
	}

	expect(t, "example.com", scopeRecords(actual, "example.com", zones, 0),
		"A @ 1.1.1.1", "TXT _cosmos-owner managed by cosmos me",
		"A www 1.1.1.1", "TXT _cosmos-owner.www managed by cosmos me",
		"A * 1.1.1.1", "TXT _cosmos-owner._wildcard managed by cosmos me",
	)
	expect(t, "cluster.example.com", scopeRecords(actual, "example.com", zones, 1),
		"A cluster 2.2.2.2", "TXT _cosmos-owner.cluster managed by cosmos me",
		"A node-1.cluster 2.2.2.2", "TXT _cosmos-owner.node-1.cluster managed by cosmos me",
		"A *.cluster 2.2.2.2", "TXT _cosmos-owner._wildcard.cluster managed by cosmos me",
		"TXT _acme-challenge.cluster token",
	)
}

// cluster.example.com is not a zone at the provider, example.com is
func TestReconcileWritesInTheParentZone(t *testing.T) {
	provider := &fakeProvider{zones: map[string][]libdns.Record{"example.com.": {
		a("www", "9.9.9.9"),
		a("old.cluster", "1.1.1.1"), ownedBy("old.cluster", "me"),
		a("theirs.cluster", "7.7.7.7"), ownedBy("theirs.cluster", "them"),
	}}}
	withFakeProvider(t, provider, map[string]string{"cluster.example.com": "example.com"})

	zones := []utils.DNSZoneConfig{fakeZone("cluster.example.com")}
	desired := Desired{"node-1.cluster.example.com": {"1.1.1.1"}, "*.cluster.example.com": {"1.1.1.1", "2.2.2.2"}}
	Reconcile(zones, 0, desired)

	expect(t, "example.com", provider.zones["example.com."],
		// not in the domain: never looked at
		"A www 9.9.9.9",
		// in the domain, of another Cosmos: left alone
		"A theirs.cluster 7.7.7.7", "TXT _cosmos-owner.theirs.cluster managed by cosmos them",
		"A node-1.cluster 1.1.1.1", "TXT _cosmos-owner.node-1.cluster managed by cosmos me",
		"A *.cluster 1.1.1.1", "A *.cluster 2.2.2.2", "TXT _cosmos-owner._wildcard.cluster managed by cosmos me",
	)
	if status := GetStatus()["cluster.example.com"]; status.LastError != "" || status.LastSync.IsZero() {
		t.Errorf("status = %+v, want a clean sync", status)
	}
	if _, wrote := provider.zones["cluster.example.com."]; wrote {
		t.Error("the domain is not a zone at the provider, nothing may be written to it")
	}

	// nothing left to do: the next pass does not even read
	calls := provider.calls
	Reconcile(zones, 0, desired)
	Reconcile(zones, 0, desired)
	if provider.calls != calls+1 {
		t.Errorf("an idle pass made %d provider calls, want the single read back", provider.calls-calls)
	}
}

// two domains of ours filed in one zone at the provider
func TestReconcileDomainsSharingAZone(t *testing.T) {
	provider := &fakeProvider{zones: map[string][]libdns.Record{"example.com.": {}}}
	withFakeProvider(t, provider, map[string]string{"example.com": "example.com", "cluster.example.com": "example.com"})

	zones := []utils.DNSZoneConfig{fakeZone("example.com"), fakeZone("cluster.example.com")}
	Reconcile(zones, 1, Desired{"node-1.cluster.example.com": {"2.2.2.2"}})
	Reconcile(zones, 0, Desired{"www.example.com": {"1.1.1.1"}})
	// both again: neither sees the names of the other as leftovers of its own
	Reconcile(zones, 1, Desired{"node-1.cluster.example.com": {"2.2.2.2"}})
	Reconcile(zones, 0, Desired{"www.example.com": {"1.1.1.1"}})

	expect(t, "both domains", provider.zones["example.com."],
		"A www 1.1.1.1", "TXT _cosmos-owner.www managed by cosmos me",
		"A node-1.cluster 2.2.2.2", "TXT _cosmos-owner.node-1.cluster managed by cosmos me",
	)

	// a name leaving one domain goes, the other domain keeps its own
	Reconcile(zones, 1, Desired{})
	Reconcile(zones, 1, Desired{})
	expect(t, "after cluster.example.com emptied", provider.zones["example.com."],
		"A www 1.1.1.1", "TXT _cosmos-owner.www managed by cosmos me",
	)
}

// no answer from the DNS: the domain itself is tried, and looked up again next time
func TestReconcileWithoutZoneLookup(t *testing.T) {
	provider := &fakeProvider{zones: map[string][]libdns.Record{"example.com.": {}}}
	withFakeProvider(t, provider, map[string]string{})

	zones := []utils.DNSZoneConfig{fakeZone("example.com")}
	Reconcile(zones, 0, Desired{"app.example.com": {"1.1.1.1"}})

	expect(t, "example.com", provider.zones["example.com."],
		"A app 1.1.1.1", "TXT _cosmos-owner.app managed by cosmos me",
	)
	if cached := zoneStates["example.com"].providerZone; cached != "" {
		t.Errorf("a guessed zone must not be kept, got %q", cached)
	}
}

func TestReconcileBacksOff(t *testing.T) {
	provider := &fakeProvider{zones: map[string][]libdns.Record{}}
	withFakeProvider(t, provider, map[string]string{"cluster.example.com": "example.com"})
	majors := []string{}
	majorError = func(message string, err error) { majors = append(majors, message+" : "+err.Error()) }

	zones := []utils.DNSZoneConfig{fakeZone("cluster.example.com")}
	desired := Desired{"node-1.cluster.example.com": {"1.1.1.1"}}
	state := func() *zoneState { return zoneStates["cluster.example.com"] }
	// due makes the wait of the last failure pass
	due := func() { state().nextTry = time.Now().Add(-time.Second) }
	wait := func() time.Duration { return time.Until(state().nextTry) }

	// the zone is not held by this account: the user's case
	Reconcile(zones, 0, desired)
	if provider.calls != 1 || state().failures != 1 || wait() > retryDelays[0] || wait() < retryDelays[0]-time.Minute/2 {
		t.Fatalf("first failure: calls=%d failures=%d wait=%v", provider.calls, state().failures, wait())
	}
	if lastError := GetStatus()["cluster.example.com"].LastError; !strings.Contains(lastError, "DNS zone example.com") || !strings.Contains(lastError, "expected 1 zone, got 0") {
		t.Errorf("the error must name the zone at the provider, got %q", lastError)
	}

	// passes come every few seconds: none of them reaches the provider
	for i := 0; i < 5; i++ {
		Reconcile(zones, 0, desired)
	}
	if provider.calls != 1 {
		t.Fatalf("a failing zone was asked again before its wait was over: %d calls", provider.calls)
	}

	due()
	Reconcile(zones, 0, desired)
	if provider.calls != 2 || state().failures != 2 || len(majors) != 0 {
		t.Fatalf("second failure: calls=%d failures=%d majors=%d", provider.calls, state().failures, len(majors))
	}

	// the third in a row is the one reported, and the zone is then left for a day
	due()
	Reconcile(zones, 0, desired)
	if provider.calls != 3 || len(majors) != 1 || !strings.Contains(majors[0], "cluster.example.com") || !strings.Contains(majors[0], "DNS zone example.com") {
		t.Fatalf("third failure: calls=%d majors=%v", provider.calls, majors)
	}
	if wait() < givenUpRetryDelay-time.Minute {
		t.Errorf("given up: next try in %v, want %v", wait(), givenUpRetryDelay)
	}

	// the daily try fails without a second report
	due()
	Reconcile(zones, 0, desired)
	if provider.calls != 4 || len(majors) != 1 || wait() < givenUpRetryDelay-time.Minute {
		t.Errorf("daily try: calls=%d majors=%d wait=%v", provider.calls, len(majors), wait())
	}

	// new records to publish are worth a try of their own
	Reconcile(zones, 0, Desired{"node-1.cluster.example.com": {"1.1.1.1"}, "node-2.cluster.example.com": {"2.2.2.2"}})
	Reconcile(zones, 0, Desired{"node-1.cluster.example.com": {"1.1.1.1"}, "node-2.cluster.example.com": {"2.2.2.2"}})
	if provider.calls != 5 || len(majors) != 1 {
		t.Errorf("changed records: calls=%d majors=%d, want one more try", provider.calls, len(majors))
	}

	// saving the domain starts over: tried right away, counted from one
	zones[0].DNSChallengeConfig = map[string]string{"TOKEN": "fixed"}
	Reconcile(zones, 0, desired)
	if provider.calls != 6 || state().failures != 1 {
		t.Errorf("saved domain: calls=%d failures=%d, want a fresh first try", provider.calls, state().failures)
	}

	// and once it works nothing is left of the failures
	provider.zones["example.com."] = []libdns.Record{}
	due()
	Reconcile(zones, 0, desired)
	if status := GetStatus()["cluster.example.com"]; state().failures != 0 || status.LastError != "" || status.LastSync.IsZero() {
		t.Errorf("after a success: failures=%d status=%+v", state().failures, status)
	}
	expect(t, "example.com", provider.zones["example.com."],
		"A node-1.cluster 1.1.1.1", "TXT _cosmos-owner.node-1.cluster managed by cosmos me",
	)
}
