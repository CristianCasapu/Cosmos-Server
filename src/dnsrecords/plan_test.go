package dnsrecords

import (
	"net/netip"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/libdns/libdns"
)

const testTTL = 60 * time.Second

func a(name string, ip string) libdns.Record {
	return libdns.Address{Name: name, TTL: testTTL, IP: netip.MustParseAddr(ip)}
}

func marker(name string) libdns.Record {
	return libdns.TXT{Name: markerName(name), TTL: testTTL, Text: markerText}
}

func ownedBy(name string, owner string) libdns.Record {
	return libdns.TXT{Name: markerName(name), TTL: testTTL, Text: markerValue(owner)}
}

func describe(records []libdns.Record) []string {
	out := []string{}
	for _, r := range records {
		rr := r.RR()
		out = append(out, rr.Type+" "+rr.Name+" "+rr.Data)
	}
	sort.Strings(out)
	return out
}

func expect(t *testing.T, what string, got []libdns.Record, want ...string) {
	t.Helper()
	sort.Strings(want)
	if want == nil {
		want = []string{}
	}
	if !reflect.DeepEqual(describe(got), want) {
		t.Errorf("%s = %v, want %v", what, describe(got), want)
	}
}

func TestPlanCreatesRecordsWithMarkers(t *testing.T) {
	desired := Desired{
		"app.example.com": {"1.1.1.1", "2.2.2.2"},
		"example.com":     {"1.1.1.1"},
		"*.example.com":   {"1.1.1.1"},
	}
	changes := Plan("example.com", desired, nil, testTTL, true, "")

	expect(t, "append", changes.Append,
		"A app 1.1.1.1", "A app 2.2.2.2", "TXT _cosmos-owner.app "+markerText,
		"A @ 1.1.1.1", "TXT _cosmos-owner "+markerText,
		"A * 1.1.1.1", "TXT _cosmos-owner._wildcard "+markerText,
	)
	expect(t, "delete", changes.Delete)
}

func TestPlanIsIdempotent(t *testing.T) {
	desired := Desired{"app.example.com": {"2.2.2.2", "1.1.1.1"}}
	actual := []libdns.Record{a("app", "1.1.1.1"), a("app", "2.2.2.2"), marker("app")}

	if changes := Plan("example.com", desired, actual, testTTL, true, ""); !changes.Empty() {
		t.Errorf("nothing to do, got set=%v delete=%v", describe(changes.Append), describe(changes.Delete))
	}
}

// declaring the hostname is consent: a manual record or a CNAME is replaced
func TestPlanTakesOverExistingRecords(t *testing.T) {
	desired := Desired{"app.example.com": {"1.1.1.1"}, "blog.example.com": {"1.1.1.1"}}
	actual := []libdns.Record{
		a("app", "9.9.9.9"),
		libdns.CNAME{Name: "blog", TTL: testTTL, Target: "old.host.net."},
	}
	changes := Plan("example.com", desired, actual, testTTL, true, "")

	expect(t, "append", changes.Append,
		"A app 1.1.1.1", "TXT _cosmos-owner.app "+markerText,
		"A blog 1.1.1.1", "TXT _cosmos-owner.blog "+markerText,
	)
	expect(t, "delete", changes.Delete, "CNAME blog old.host.net.")
	expect(t, "withdraw", changes.Withdraw, "A app 9.9.9.9")
}

// a set changes one record at a time: what stays is not rewritten, and what
// leaves only goes once the new addresses are in
func TestPlanDiffsAddressSets(t *testing.T) {
	desired := Desired{"app.example.com": {"1.1.1.1", "3.3.3.3", "4.4.4.4"}}
	actual := []libdns.Record{a("app", "1.1.1.1"), a("app", "2.2.2.2"), a("app", "1.1.1.1"), marker("app")}
	changes := Plan("example.com", desired, actual, testTTL, true, "")

	expect(t, "append", changes.Append, "A app 3.3.3.3", "A app 4.4.4.4")
	expect(t, "withdraw", changes.Withdraw, "A app 2.2.2.2", "A app 1.1.1.1")
	expect(t, "delete", changes.Delete)
}

// removal only ever touches names carrying our marker
func TestPlanDeletesOnlyOwnedNames(t *testing.T) {
	actual := []libdns.Record{
		a("gone", "1.1.1.1"), marker("gone"),
		a("manual", "5.5.5.5"),
		libdns.TXT{Name: "_acme-challenge", TTL: testTTL, Text: "token"},
		a("*", "1.1.1.1"), marker("*"),
	}
	changes := Plan("example.com", Desired{}, actual, testTTL, true, "")

	expect(t, "append", changes.Append)
	expect(t, "delete", changes.Delete,
		"A gone 1.1.1.1", "TXT _cosmos-owner.gone "+markerText,
		"A * 1.1.1.1", "TXT _cosmos-owner._wildcard "+markerText,
	)
}

// a name with no address to give keeps what it has
func TestPlanNeverEmptiesASet(t *testing.T) {
	actual := []libdns.Record{a("app", "1.1.1.1"), marker("app")}
	changes := Plan("example.com", Desired{"app.example.com": {}}, actual, testTTL, true, "")

	if !changes.Empty() {
		t.Errorf("an empty desired set must leave the records alone, got set=%v delete=%v", describe(changes.Append), describe(changes.Delete))
	}
}

func TestPlanWithoutMarker(t *testing.T) {
	changes := Plan("example.duckdns.org", Desired{"example.duckdns.org": {"1.1.1.1"}}, nil, testTTL, false, "")
	expect(t, "append", changes.Append, "A @ 1.1.1.1")
}

func TestWildcardCovered(t *testing.T) {
	desired := Desired{
		"*.example.com":      {"1.1.1.1", "2.2.2.2"},
		"example.com":        {"1.1.1.1", "2.2.2.2"},
		"app.example.com":    {"2.2.2.2", "1.1.1.1"},
		"a.deep.example.com": {"1.1.1.1", "2.2.2.2"},
		"node-2.example.com": {"3.3.3.3"},
	}
	got := WildcardCovered(desired, "example.com")

	names := []string{}
	for name := range got {
		names = append(names, name)
	}
	sort.Strings(names)
	want := []string{"*.example.com", "example.com", "node-2.example.com"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
}

func TestMarkerNames(t *testing.T) {
	for _, name := range []string{"@", "*", "*.lab", "app", "a.deep"} {
		if got := ownedName(markerName(name)); got != name {
			t.Errorf("marker round-trip of %q gave %q", name, got)
		}
	}
	if ownedName("_acme-challenge.app") != "" || ownedName("_cosmos-ownerx") != "" {
		t.Error("foreign TXT records must not be read as markers")
	}
}

func TestMarkerOwners(t *testing.T) {
	for _, owner := range []string{"", "3f9a1c2b4d5e"} {
		if got := markerOwner(markerValue(owner)); got != owner {
			t.Errorf("owner round-trip of %q gave %q", owner, got)
		}
	}
	// some providers hand TXT values back quoted
	if got := markerOwner(`"managed by cosmos abc"`); got != "abc" {
		t.Errorf("quoted marker read as %q", got)
	}
	if markerOwner("something else") != unknownOwner {
		t.Error("a marker we cannot read is never ours")
	}
}

// a domain can be a part of the zone only: names are relative to the zone
func TestPlanInAParentZone(t *testing.T) {
	desired := Desired{
		"node-1.cluster.example.com": {"1.1.1.1"},
		"cluster.example.com":        {"1.1.1.1"},
		"*.cluster.example.com":      {"2.2.2.2"},
	}
	changes := Plan("example.com", desired, nil, testTTL, true, "me")

	expect(t, "append", changes.Append,
		"A node-1.cluster 1.1.1.1", "TXT _cosmos-owner.node-1.cluster managed by cosmos me",
		"A cluster 1.1.1.1", "TXT _cosmos-owner.cluster managed by cosmos me",
		"A *.cluster 2.2.2.2", "TXT _cosmos-owner._wildcard.cluster managed by cosmos me",
	)
	for _, name := range []string{"node-1.cluster", "cluster", "*.cluster"} {
		if got := ownedName(markerName(name)); got != name {
			t.Errorf("marker round-trip of %q gave %q", name, got)
		}
	}
}

// declaring a hostname takes it over from another Cosmos too: a constellation
// created again must get its names back
func TestPlanTakesOverFromAnotherOwner(t *testing.T) {
	desired := Desired{"app.example.com": {"1.1.1.1"}, "old.example.com": {"1.1.1.1"}}
	actual := []libdns.Record{
		a("app", "1.1.1.1"), ownedBy("app", "them"),
		// written before owners existed: adopted without a word
		a("old", "1.1.1.1"), marker("old"),
	}
	changes := Plan("example.com", desired, actual, testTTL, true, "me")

	expect(t, "append", changes.Append,
		"TXT _cosmos-owner.app managed by cosmos me",
		"TXT _cosmos-owner.old managed by cosmos me",
	)
	expect(t, "delete", changes.Delete,
		"TXT _cosmos-owner.app managed by cosmos them",
		"TXT _cosmos-owner.old managed by cosmos",
	)
	expect(t, "withdraw", changes.Withdraw)
	if !reflect.DeepEqual(changes.TakenOver, []string{"app"}) {
		t.Errorf("taken over = %v, want [app]", changes.TakenOver)
	}

	// once ours, nothing left to do
	actual = []libdns.Record{a("app", "1.1.1.1"), ownedBy("app", "me"), a("old", "1.1.1.1"), ownedBy("old", "me")}
	if changes := Plan("example.com", desired, actual, testTTL, true, "me"); !changes.Empty() {
		t.Errorf("nothing to do, got set=%v delete=%v", describe(changes.Append), describe(changes.Delete))
	}
}

// two Cosmos sharing a zone: what the other one wrote is never cleaned up
func TestPlanLeavesOtherOwnersAlone(t *testing.T) {
	actual := []libdns.Record{
		a("gone", "1.1.1.1"), ownedBy("gone", "me"),
		a("theirs", "7.7.7.7"), ownedBy("theirs", "them"),
		a("old", "8.8.8.8"), marker("old"),
		// both claim it, we do not want it anymore: the addresses stay theirs
		a("shared", "7.7.7.7"), ownedBy("shared", "me"), ownedBy("shared", "them"),
	}
	changes := Plan("example.com", Desired{}, actual, testTTL, true, "me")

	expect(t, "append", changes.Append)
	expect(t, "withdraw", changes.Withdraw)
	expect(t, "delete", changes.Delete,
		"A gone 1.1.1.1", "TXT _cosmos-owner.gone managed by cosmos me",
		"TXT _cosmos-owner.shared managed by cosmos me",
	)
}
