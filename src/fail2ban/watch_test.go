package fail2ban

import (
	"net/netip"
	"testing"

	"github.com/azukaar/cosmos-server/src/security"
	"github.com/azukaar/cosmos-server/src/utils"
)

func TestNewBansAreWhatWasNotBannedAtTheLastLook(t *testing.T) {
	first := map[string][]netip.Addr{"cosmos-auth": addresses("203.0.113.7"), "sshd": addresses("198.51.100.1")}

	// the first look only notes what is there
	added, known := newBans(nil, first)
	if len(added) != 0 || len(known) != 2 {
		t.Fatalf("first look: added %v, known %v", added, known)
	}

	// one new address, one address in a second jail, one gone, one listed twice
	second := map[string][]netip.Addr{
		"cosmos-auth":  addresses("203.0.113.7", "203.0.113.8", "203.0.113.8"),
		"cosmos-probe": addresses("203.0.113.7"),
		"made-up":      addresses("203.0.113.9"),
	}
	added, known = newBans(known, second)
	want := []banKey{{"cosmos-auth", netip.MustParseAddr("203.0.113.8")}, {"cosmos-probe", netip.MustParseAddr("203.0.113.7")}}
	if len(added) != 2 || added[0] != want[0] || added[1] != want[1] {
		t.Errorf("added = %v, want %v", added, want)
	}
	if len(known) != 3 {
		t.Errorf("known = %v, want three bans", known)
	}

	// nothing changed: nothing new
	if added, _ = newBans(known, second); len(added) != 0 {
		t.Errorf("added again: %v", added)
	}
	// banned again after its ban ended: new again
	_, known = newBans(known, map[string][]netip.Addr{})
	if added, _ = newBans(known, second); len(added) != 3 {
		t.Errorf("after the bans ended and came back: added %v, want all 3", added)
	}
}

func TestWatcherRecordsEachNewBanOnce(t *testing.T) {
	recorded := captureSecurity(t)
	banned := "[{'cosmos-auth': []}]"
	fails := false
	client := useFake(t, true, func(args []string) (string, error) {
		if fails {
			return "", errClient
		}
		return banned, nil
	})

	known := look(nil) // the first look
	banned = "[{'cosmos-auth': ['203.0.113.7']}, {'sshd': ['2001:db8::7']}]"
	known = look(known)
	known = look(known) // nothing new
	fails = true
	known = look(known) // Fail2ban does not answer: what is known is kept
	fails = false
	known = look(known) // it answers again: the same bans are not new

	events := recorded()
	if len(events) != 2 {
		t.Fatalf("%d events, want 2: %+v", len(events), events)
	}
	for position, want := range []struct{ ip, jail string }{{"203.0.113.7", "cosmos-auth"}, {"2001:db8::7", "sshd"}} {
		event := events[position]
		if event.Kind != utils.SecFail2banBan || event.IP != want.ip || event.Details["jail"] != want.jail || event.Actor != "" {
			t.Errorf("event %d = %+v, want a ban of %s by %s", position, event, want.ip, want.jail)
		}
	}
	for _, call := range client.called() {
		if len(call) != 1 || call[0] != "banned" {
			t.Errorf("the watcher ran %v", call)
		}
	}
	_ = known
}

func TestWatcherDoesNothingWithoutFail2ban(t *testing.T) {
	recorded := captureSecurity(t)
	client := useFake(t, false, realAnswers(t))
	if known := look(map[banKey]bool{}); known != nil {
		t.Errorf("known = %v, want nothing to compare with", known)
	}
	if len(client.called()) != 0 || len(recorded()) != 0 {
		t.Errorf("calls %v, events %v", client.called(), recorded())
	}
}

// The kinds this package records are in the catalogue, with the class the
// reference describes: a ban is an incident (it goes to the security log,
// where no filter of the fork counts it), the administrator's actions are
// audit entries.
func TestKindsAreInTheCatalogue(t *testing.T) {
	for kind, class := range map[string]string{
		utils.SecFail2banBan:      security.ClassIncident,
		utils.SecFail2banUnban:    security.ClassAudit,
		utils.SecFail2banSettings: security.ClassAudit,
	} {
		found, ok := security.KindOf(kind)
		if !ok || found.Class != class || found.Severity != "medium" {
			t.Errorf("%s = %+v, %v; want class %s, severity medium", kind, found, ok, class)
		}
	}
}
