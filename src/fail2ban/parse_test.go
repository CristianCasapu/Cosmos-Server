package fail2ban

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"
)

func addresses(texts ...string) []netip.Addr {
	list := []netip.Addr{}
	for _, text := range texts {
		list = append(list, netip.MustParseAddr(text))
	}
	return list
}

// The outputs in testdata were captured from fail2ban-client 1.1.0-8 in a
// Debian 13 container set up with the fork's installer.
func TestParsersReadWhatFail2banReallyPrints(t *testing.T) {
	if !parsePong(fixture(t, "ping.txt")) {
		t.Error("ping: the real answer is not taken for a pong")
	}
	if parsePong(fixture(t, "ping-stopped.txt")) {
		t.Error("ping: the error of a stopped server is taken for a pong")
	}

	wantJails := []string{"cosmos-abuse", "cosmos-auth", "cosmos-probe", "cosmos-scan", "recidive", "sshd"}
	if got := parseJailList(fixture(t, "status.txt")); !reflect.DeepEqual(got, wantJails) {
		t.Errorf("jail list = %v, want %v", got, wantJails)
	}
	if got := parseJailList(fixture(t, "status-stopped.txt")); len(got) != 0 {
		t.Errorf("jail list of a stopped server = %v, want none", got)
	}

	got, err := parseJailStatus(fixture(t, "jail-banned.txt"))
	if want := (counts{CurrentlyFailed: 1, TotalFailed: 17, CurrentlyBanned: 3, TotalBanned: 3}); err != nil || got != want {
		t.Errorf("jail status = %+v, %v; want %+v", got, err, want)
	}
	if got, err := parseJailStatus(fixture(t, "jail-empty.txt")); err != nil || got != (counts{}) {
		t.Errorf("status of an empty jail = %+v, %v", got, err)
	}
	if _, err := parseJailStatus(fixture(t, "unknown-jail.txt")); err == nil {
		t.Error("the answer for an unknown jail was read as a status")
	}

	zone := time.FixedZone("test", 2*3600)
	bans := parseBanTimes(fixture(t, "banip-with-time.txt"), zone)
	if len(bans) != 3 {
		t.Fatalf("%d bans, want 3: %+v", len(bans), bans)
	}
	since := time.Date(2026, 11, 13, 14, 2, 54, 0, zone)
	for position, address := range addresses("203.0.113.7", "2001:db8::7", "198.51.100.23") {
		ban := bans[position]
		if ban.Address != address || !ban.Since.Equal(since) || !ban.Until.Equal(since.Add(4*time.Hour)) {
			t.Errorf("ban %d = %+v, want %s from %s for 4h", position, ban, address, since)
		}
	}
	if bans := parseBanTimes(fixture(t, "banip-empty.txt"), zone); len(bans) != 0 {
		t.Errorf("bans of an empty jail: %+v", bans)
	}

	banned := parseBanned(fixture(t, "banned.txt"))
	wantBanned := map[string][]netip.Addr{
		"sshd": {}, "recidive": {}, "cosmos-scan": {}, "cosmos-abuse": {},
		"cosmos-auth":  addresses("203.0.113.7", "2001:db8::7", "198.51.100.23"),
		"cosmos-probe": addresses("203.0.113.7"),
	}
	if !reflect.DeepEqual(banned, wantBanned) {
		t.Errorf("banned = %v, want %v", banned, wantBanned)
	}

	networks, others := parseIgnoreList(fixture(t, "ignoreip.txt"))
	wantNetworks := []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "100.64.0.0/10", "fc00::/7", "fe80::/10", "::1"}
	gotNetworks := []string{}
	for _, network := range networks {
		gotNetworks = append(gotNetworks, formatPrefix(network))
	}
	if !reflect.DeepEqual(gotNetworks, wantNetworks) || others != 0 {
		t.Errorf("ignored = %v (%d others), want %v", gotNetworks, others, wantNetworks)
	}

	for name, want := range map[string]int{"get-maxretry.txt": 5, "get-findtime.txt": 3600, "get-bantime.txt": 14400, "unban-one.txt": 1, "unban-none.txt": 0} {
		if got, ok := parseNumber(fixture(t, name)); !ok || got != want {
			t.Errorf("%s = %d, %v; want %d", name, got, ok, want)
		}
	}
	if _, ok := parseNumber(fixture(t, "reload.txt")); ok {
		t.Error(`"OK" was read as a number`)
	}
}

// Output that is not what Fail2ban prints: cut off, padded, or crafted.
// The parsers must return less, never something that was not checked, and
// never fail.
func TestParsersDropWhatTheyDoNotExpect(t *testing.T) {
	long := strings.Repeat("A", 1<<20)

	jailLists := map[string][]string{
		"":                                nil,
		"Status\n|- Number of jail:\t2\n": nil,
		"`- Jail list:\t":                 {},
		"`- Jail list:\tsshd, <script>alert(1)</script>, ../../etc, a b, $(reboot), recidive": {"sshd", "recidive"},
		"`- Jail list:\t" + long: {},
		"`- Jail list:\tsshd,\x00evil,\x1b[31mred, cosmos-auth\n`- Jail list:\tother": {"sshd", "cosmos-auth"},
		"`- Jail list:\t-rf, --all, sshd":                                             {"sshd"},
	}
	for out, want := range jailLists {
		if got := parseJailList(out); !reflect.DeepEqual(got, want) {
			t.Errorf("parseJailList(%.60q) = %v, want %v", out, got, want)
		}
	}
	many := "`- Jail list:\t" + strings.Repeat("jail, ", 5000)
	if got := parseJailList(many); len(got) != maxJails {
		t.Errorf("%d jail names read, want the bound %d", len(got), maxJails)
	}

	for _, out := range []string{
		"",
		"Status for the jail: x\n|- Filter\n|  |- Currently failed:\t1\n",                                  // cut off
		strings.Replace(fixture(t, "jail-banned.txt"), "Currently banned:\t3", "Currently banned:\t-3", 1), // negative
		strings.Replace(fixture(t, "jail-banned.txt"), "Total failed:\t17", "Total failed:\t17; reboot", 1),
		strings.Replace(fixture(t, "jail-banned.txt"), "Total banned:\t3", "Total banned:\t99999999999999999999", 1),
		strings.Replace(fixture(t, "jail-banned.txt"), "Total banned:\t3", "Total banned:\t0x10", 1),
		long,
	} {
		if got, err := parseJailStatus(out); err == nil {
			t.Errorf("parseJailStatus(%.60q) = %+v, want an error", out, got)
		}
	}

	zone := time.UTC
	banLines := strings.Join([]string{
		"203.0.113.7 \t2026-11-13 14:02:54 + 14400 = 2026-11-13 18:02:54", // the one good line
		"evil.example.com \t2026-11-13 14:02:54 + 14400 = 2026-11-13 18:02:54",
		"203.0.113.8; reboot \t2026-11-13 14:02:54 + 14400 = 2026-11-13 18:02:54",
		"203.0.113.9 \t2026-13-45 99:99:99 + 14400 = 2026-11-13 18:02:54",
		"203.0.113.10 \t2026-11-13 14:02:54 + lots = 2026-11-13 18:02:54",
		"203.0.113.11 \t2026-11-13 14:02:54 + 14400",
		"fe80::1%eth0 \t2026-11-13 14:02:54 + 14400 = 2026-11-13 18:02:54",
		"999.1.1.1 \t2026-11-13 14:02:54 + 14400 = 2026-11-13 18:02:54",
		"<b>203.0.113.12</b> \t2026-11-13 14:02:54 + 14400 = 2026-11-13 18:02:54",
		"203.0.113.13 \t2026-11-13 14:02:54 + 14400 = 2026-11-13 18:02:54 trailing words",
		"203.0.113.14 \t2026-11-13 14:02:5", // cut off in the middle
	}, "\n")
	bans := parseBanTimes(banLines, zone)
	if len(bans) != 1 || bans[0].Address != netip.MustParseAddr("203.0.113.7") {
		t.Errorf("bans read from hostile lines: %+v, want only 203.0.113.7", bans)
	}
	// a ban without end, and an address written as IPv4 inside IPv6
	bans = parseBanTimes("::ffff:203.0.113.20 \t2026-11-13 14:02:54 + -1 = 9999-12-31 23:59:59\n", zone)
	if len(bans) != 1 || bans[0].Address != netip.MustParseAddr("203.0.113.20") || !bans[0].Until.IsZero() {
		t.Errorf("permanent ban = %+v, want 203.0.113.20 without an end", bans)
	}
	if got := parseBanTimes(strings.Repeat("203.0.113.7 \t2026-11-13 14:02:54 + 60 = 2026-11-13 14:03:54\n", maxBans+500), zone); len(got) != maxBans {
		t.Errorf("%d bans read, want the bound %d", len(got), maxBans)
	}
	if got := parseBanTimes(long, zone); len(got) != 0 {
		t.Errorf("bans read from noise: %d", len(got))
	}

	banned := parseBanned(`[{'cosmos-auth': ['203.0.113.7', 'evil.example', '1.2.3.4; id', '$(id)', '::1', '-h']}, {'made-up': ['203.0.113.8']}, {'sshd': ['203.0.113.9'`)
	if want := map[string][]netip.Addr{"cosmos-auth": addresses("203.0.113.7", "::1")}; !reflect.DeepEqual(banned, want) {
		t.Errorf("banned from a hostile answer = %v, want %v", banned, want)
	}
	for _, out := range []string{"", "[]", "[{}]", "{'cosmos-auth': '203.0.113.7'}", long, "[{'cosmos-auth': [" + long} {
		if got := parseBanned(out); len(got) != 0 {
			t.Errorf("parseBanned(%.40q) = %v, want nothing", out, got)
		}
	}

	networks, others := parseIgnoreList("These IP addresses/networks are ignored:\n|- 10.0.0.0/8\n|- home.example.org\n|- 1.2.3.4/99\n|- 203.0.113.77/24\n|- \n`- fe80::1%eth0\nnoise 192.0.2.1\n")
	if len(networks) != 2 || formatPrefix(networks[0]) != "10.0.0.0/8" || formatPrefix(networks[1]) != "203.0.113.0/24" || others != 4 {
		t.Errorf("ignored = %v with %d others, want 10.0.0.0/8, 203.0.113.0/24 and 4 others", networks, others)
	}
	if networks, others := parseIgnoreList("No IP address/network is ignored\n"); len(networks) != 0 || others != 0 {
		t.Errorf("empty ignore list read as %v, %d", networks, others)
	}

	for _, out := range []string{"", " ", "-1", "+1", "1.5", "1e3", "0x10", "12 34", "1\n2", "９", "99999999999999", long} {
		if got, ok := parseNumber(out); ok {
			t.Errorf("parseNumber(%.20q) = %d, want it refused", out, got)
		}
	}
	for _, out := range []string{"", "pong", "Server replied: pong\nand more", "Server replied: pong; reboot"} {
		if parsePong(out) {
			t.Errorf("parsePong(%q) is true", out)
		}
	}
}

func TestAddressesAndNetworksAreReadStrictly(t *testing.T) {
	good := map[string]string{
		"203.0.113.7":          "203.0.113.7",
		"2001:DB8::7":          "2001:db8::7",
		"::ffff:203.0.113.7":   "203.0.113.7",
		"2001:db8:0:0:0:0:0:7": "2001:db8::7",
	}
	for text, want := range good {
		if got, ok := parseAddress(text); !ok || got.String() != want {
			t.Errorf("parseAddress(%q) = %v, %v; want %s", text, got, ok, want)
		}
	}
	for _, text := range []string{"", " ", "203.0.113.7 ", " 203.0.113.7", "203.0.113", "203.0.113.7/32", "fe80::1%eth0", "[2001:db8::7]", "example.com",
		"-h", "--all", "203.0.113.7;id", "203.0.113.7\n203.0.113.8", "0x7f.1", "1.2.3.4.5", "localhost", strings.Repeat("1", 200)} {
		if got, ok := parseAddress(text); ok {
			t.Errorf("parseAddress(%q) = %v, want it refused", text, got)
		}
	}

	networks := map[string]string{
		"203.0.113.77/24":    "203.0.113.0/24",
		"203.0.113.7":        "203.0.113.7",
		"203.0.113.7/32":     "203.0.113.7",
		"2001:db8:5::1/48":   "2001:db8:5::/48",
		"::1":                "::1",
		"::ffff:203.0.113.7": "203.0.113.7",
	}
	for text, want := range networks {
		if got, ok := parsePrefix(text); !ok || formatPrefix(got) != want {
			t.Errorf("parsePrefix(%q) = %v, %v; want %s", text, got, ok, want)
		}
	}
	for _, text := range []string{"", "/", "/24", "203.0.113.0/", "203.0.113.0/33", "203.0.113.0/-1", "203.0.113.0/24/24", "fe80::1%eth0/64", "::ffff:10.0.0.0/104",
		"10.0.0.0/8 192.168.0.0/16", "10.0.0.0/8\nenabled = false", "%(known/ignoreip)s", "<ip>", "example.com/24"} {
		if got, ok := parsePrefix(text); ok {
			t.Errorf("parsePrefix(%q) = %v, want it refused", text, got)
		}
	}
}
