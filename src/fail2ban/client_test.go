package fail2ban

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

// Everything this package can make fail2ban-client do, and the exact
// argument lists it does it with.
func TestArgumentListsAreFixed(t *testing.T) {
	client := useFake(t, true, realAnswers(t))
	auth, _ := jailByName("cosmos-auth")

	ping()
	loadedJails()
	jailCounts(auth)
	jailBans(auth)
	jailThresholds(auth)
	jailIgnored(auth)
	bannedNow()
	unban(auth, netip.MustParseAddr("203.0.113.7"))
	unban(auth, netip.MustParseAddr("2001:db8::7"))
	reload()

	want := [][]string{
		{"ping"},
		{"status"},
		{"status", "cosmos-auth"},
		{"get", "cosmos-auth", "banip", "--with-time"},
		{"get", "cosmos-auth", "maxretry"},
		{"get", "cosmos-auth", "findtime"},
		{"get", "cosmos-auth", "bantime"},
		{"get", "cosmos-auth", "ignoreip"},
		{"banned"},
		{"set", "cosmos-auth", "unbanip", "203.0.113.7"},
		{"set", "cosmos-auth", "unbanip", "2001:db8::7"},
		{"reload"},
	}
	if got := client.called(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls:\n%v\nwant:\n%v", got, want)
	}
}

func TestUnbanOnlyEverPassesAnAddress(t *testing.T) {
	client := useFake(t, true, realAnswers(t))
	auth, _ := jailByName("cosmos-auth")

	if lifted, err := unban(auth, netip.MustParseAddr("203.0.113.7")); err != nil || !lifted {
		t.Errorf("unban of a banned address = %v, %v", lifted, err)
	}
	if lifted, err := unban(auth, netip.MustParseAddr("203.0.113.99")); err != nil || lifted {
		t.Errorf("unban of an address that is not banned = %v, %v", lifted, err)
	}
	// values a parser could produce but that must not be passed on
	before := len(client.called())
	if _, err := unban(auth, netip.Addr{}); err == nil {
		t.Error("the zero address was passed on")
	}
	if _, err := unban(auth, netip.MustParseAddr("fe80::1%eth0")); err == nil {
		t.Error("an address with a zone was passed on")
	}
	if len(client.called()) != before {
		t.Errorf("the client was called for an invalid address: %v", client.called()[before:])
	}
	for _, call := range client.called() {
		last := call[len(call)-1]
		if _, err := netip.ParseAddr(last); err != nil || strings.HasPrefix(last, "-") {
			t.Errorf("unbanip was given %q", last)
		}
	}
}

func TestOnlyTheForksJailsHaveAName(t *testing.T) {
	for _, name := range []string{"cosmos-auth", "cosmos-probe", "cosmos-scan", "cosmos-abuse", "sshd", "recidive"} {
		if known, ok := jailByName(name); !ok || known.name != name {
			t.Errorf("jailByName(%q) = %+v, %v", name, known, ok)
		}
	}
	for _, name := range []string{"", "SSHD", "sshd ", " sshd", "apache-auth", "--all", "sshd\n", "cosmos-auth;id", "cosmos-*", "../sshd"} {
		if known, ok := jailByName(name); ok {
			t.Errorf("jailByName(%q) = %+v, want none", name, known)
		}
	}
}

func TestCappedKeepsTheBeginningAndSaysThereWasMore(t *testing.T) {
	out := &capped{max: 10}
	if n, err := out.Write([]byte("12345")); n != 5 || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if out.truncated {
		t.Error("truncated before the cap")
	}
	// the writer is told everything was taken, so that the process is not
	// stopped by a full pipe
	if n, err := out.Write([]byte("6789012345")); n != 10 || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
	out.Write([]byte("more"))
	if got := out.buffer.String(); got != "1234567890" || !out.truncated {
		t.Errorf("kept %q, truncated %v; want the first ten bytes and true", got, out.truncated)
	}
}
