package fail2ban

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// useConfig gives the server a configuration for the test.
func useConfig(t *testing.T, config utils.Config) {
	t.Helper()
	previous := utils.GetBaseMainConfig()
	utils.LoadBaseMainConfig(config)
	t.Cleanup(func() { utils.LoadBaseMainConfig(previous) })
}

func useOwnAddresses(t *testing.T, texts ...string) {
	t.Helper()
	previous := ownAddresses
	ownAddresses = func() []netip.Addr { return addresses(texts...) }
	t.Cleanup(func() { ownAddresses = previous })
}

func TestStatusWithoutFail2ban(t *testing.T) {
	useJailDir(t, false)
	useConfig(t, utils.Config{})
	client := useFake(t, false, realAnswers(t))

	status := GetStatus("203.0.113.50")
	if status.Installed || status.Running || status.ConfigInstalled {
		t.Errorf("status = %+v, want nothing installed", status)
	}
	if len(client.called()) != 0 {
		t.Errorf("the client was run although it is not installed: %v", client.called())
	}
	// the jails are listed with the thresholds of their file, not loaded
	if len(status.Jails) != len(jails) {
		t.Fatalf("%d jails, want %d", len(status.Jails), len(jails))
	}
	for position, known := range jails {
		if entry := status.Jails[position]; entry.Name != known.name || entry.Loaded || entry.Thresholds != known.defaults {
			t.Errorf("jail %d = %+v, want %s with its defaults, not loaded", position, entry, known.name)
		}
	}
	if status.Bans == nil || len(status.Bans) != 0 {
		t.Errorf("bans = %v, want an empty list", status.Bans)
	}
}

func TestStatusOfAStoppedFail2ban(t *testing.T) {
	useJailDir(t, true)
	useConfig(t, utils.Config{})
	client := useFake(t, true, func(args []string) (string, error) { return "", errClient })

	status := GetStatus("")
	if !status.Installed || status.Running || !status.ConfigInstalled {
		t.Errorf("status = %+v, want installed, not running", status)
	}
	if got := client.called(); len(got) != 1 || got[0][0] != "ping" {
		t.Errorf("calls = %v, want one ping", got)
	}
}

func TestStatusShowsJailsBansAndWhatIsNeverBanned(t *testing.T) {
	useJailDir(t, true)
	useConfig(t, utils.Config{})
	useOwnAddresses(t, "192.0.2.1")
	client := useFake(t, true, realAnswers(t))

	status := GetStatus("203.0.113.50")
	if !status.Installed || !status.Running || !status.ConfigInstalled || status.Incomplete || status.OtherJails != 0 {
		t.Fatalf("status = %+v", status)
	}
	auth := status.Jails[0]
	if !auth.Loaded || auth.CurrentlyBanned != 3 || auth.TotalFailed != 17 || auth.Thresholds != (Thresholds{MaxRetry: 5, FindTime: 3600, BanTime: 14400}) {
		t.Errorf("cosmos-auth = %+v", auth)
	}
	for _, entry := range status.Jails {
		if !entry.Loaded {
			t.Errorf("%s is not shown as loaded", entry.Name)
		}
	}

	// newest first; the same address in two jails is two bans
	got := []string{}
	for _, ban := range status.Bans {
		if ban.Since == nil || ban.Until == nil || !ban.Until.After(*ban.Since) {
			t.Errorf("ban without its times: %+v", ban)
		}
		got = append(got, ban.Jail+" "+ban.IP)
	}
	want := []string{"cosmos-probe 203.0.113.7", "cosmos-auth 198.51.100.23", "cosmos-auth 2001:db8::7", "cosmos-auth 203.0.113.7"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bans = %v, want %v", got, want)
	}

	if want := []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "100.64.0.0/10", "fc00::/7", "fe80::/10", "::1"}; !reflect.DeepEqual(status.Settings.IgnoreIP, want) {
		t.Errorf("never banned = %v, want %v", status.Settings.IgnoreIP, want)
	}
	if !status.PrivateIgnored {
		t.Error("the private networks are on the list but PrivateIgnored is false")
	}
	if status.ClientIP != "203.0.113.50" || status.ClientIgnored {
		t.Errorf("client %q ignored %v, want 203.0.113.50 not ignored", status.ClientIP, status.ClientIgnored)
	}

	// jails without bans are not asked for their bans
	if client.count("get", "sshd", "banip") != 0 || client.count("get", "cosmos-auth", "banip") != 1 {
		t.Errorf("banip calls: sshd %d, cosmos-auth %d", client.count("get", "sshd", "banip"), client.count("get", "cosmos-auth", "banip"))
	}

	// a second look right away is answered from what was read; who asks
	// still decides the warning
	calls := len(client.called())
	for address, ignored := range map[string]bool{
		"192.168.1.20":        true,  // on the list
		"172.20.0.3":          true,  // on the list
		"127.0.0.1":           true,  // loopback
		"192.0.2.1":           true,  // the server's own
		"fd00::5":             true,  // on the list
		"::ffff:192.168.1.20": true,  // IPv4 written as IPv6
		"2001:db8::50":        false, // public
		"198.51.100.1":        false,
	} {
		if got := GetStatus(address); got.ClientIgnored != ignored {
			t.Errorf("%s: ignored = %v, want %v", address, got.ClientIgnored, ignored)
		}
	}
	for _, text := range []string{"", "not an address", "<script>", "fe80::1%eth0"} {
		if got := GetStatus(text); got.ClientIP != "" || got.ClientIgnored {
			t.Errorf("client %q: shown as %q, ignored %v", text, got.ClientIP, got.ClientIgnored)
		}
	}
	if len(client.called()) != calls {
		t.Errorf("%d more client calls for looks within the cache time", len(client.called())-calls)
	}
}

func TestStatusCountsTrustedProxiesAndMissingPrivateNetworks(t *testing.T) {
	useJailDir(t, true)
	config := utils.Config{}
	config.HTTPConfig.TrustedProxies = []string{"10.0.0.5", "", "https://example.org/ranges"}
	useConfig(t, config)
	answers := realAnswers(t)
	useFake(t, true, func(args []string) (string, error) {
		if len(args) == 3 && args[2] == "ignoreip" {
			return "These IP addresses/networks are ignored:\n|- 127.0.0.0/8\n|- 192.168.0.0/16\n|- home.example.org\n`- 172.16.0.0/16\n", nil
		}
		if len(args) == 1 && args[0] == "status" {
			return "Status\n|- Number of jail:\t4\n`- Jail list:\tapache-auth, cosmos-auth, postfix, sshd\n", nil
		}
		return answers(args)
	})

	status := GetStatus("192.168.1.20")
	if status.TrustedProxies != 2 {
		t.Errorf("trusted proxies = %d, want 2", status.TrustedProxies)
	}
	if status.PrivateIgnored {
		t.Error("PrivateIgnored although 10.0.0.0/8 and most of 172.16.0.0/12 are not on the list")
	}
	if status.OtherIgnored != 1 || status.OtherJails != 2 {
		t.Errorf("other ignored = %d, other jails = %d; want 1 and 2", status.OtherIgnored, status.OtherJails)
	}
	loaded := []string{}
	for _, entry := range status.Jails {
		if entry.Loaded {
			loaded = append(loaded, entry.Name)
		}
	}
	if !reflect.DeepEqual(loaded, []string{"cosmos-auth", "sshd"}) {
		t.Errorf("loaded = %v, want cosmos-auth and sshd", loaded)
	}
	if !status.ClientIgnored {
		t.Error("192.168.1.20 is on the list")
	}
}

// A Fail2ban that answers nonsense makes the status incomplete; it never
// puts its text into the answer.
func TestStatusOfAFail2banThatAnswersNonsense(t *testing.T) {
	useJailDir(t, true)
	useConfig(t, utils.Config{})
	noise := "<script>alert(1)</script> $(reboot) \x1b[31m" + strings.Repeat("x", 4096)
	useFake(t, true, func(args []string) (string, error) {
		switch strings.Join(args, " ") {
		case "ping":
			return "Server replied: pong\n", nil
		case "status":
			return "`- Jail list:\tcosmos-auth, sshd, " + noise + "\n", nil
		}
		return noise, nil
	})

	status := GetStatus("203.0.113.50")
	if !status.Running || !status.Incomplete {
		t.Errorf("running %v, incomplete %v; want both", status.Running, status.Incomplete)
	}
	if len(status.Bans) != 0 {
		t.Errorf("bans from nonsense: %v", status.Bans)
	}
	for _, entry := range status.Jails {
		if strings.ContainsAny(entry.Name, "<$\x1b") {
			t.Errorf("a jail is named %q", entry.Name)
		}
	}
	// thresholds that could not be read stay those of the jail's file
	if status.Jails[0].Thresholds != jails[0].defaults {
		t.Errorf("thresholds = %+v, want the defaults", status.Jails[0].Thresholds)
	}
}

// What the configuration file holds is checked before it is shown.
func TestStatusChecksWhatWasSaved(t *testing.T) {
	useJailDir(t, false)
	config := utils.Config{}
	config.Fail2ban = utils.Fail2banConfig{
		Jails: map[string]utils.Fail2banJailConfig{
			"cosmos-auth": {MaxRetry: 3, FindTime: 7200, BanTime: 86400},
			"sshd":        {MaxRetry: 500, FindTime: 1, BanTime: -1},
			"made-up":     {MaxRetry: 3, FindTime: 7200, BanTime: 86400},
		},
		IgnoreIP: []string{"192.168.1.77/24", "<script>", "10.0.0.0/8\nenabled = false"},
	}
	useConfig(t, config)
	useFake(t, false, realAnswers(t))

	settings := GetStatus("").Settings
	if got := settings.Jails["cosmos-auth"]; got != (Thresholds{MaxRetry: 3, FindTime: 7200, BanTime: 86400}) {
		t.Errorf("cosmos-auth = %+v, want what was saved", got)
	}
	sshd, _ := jailByName("sshd")
	if got := settings.Jails["sshd"]; got != sshd.defaults {
		t.Errorf("sshd = %+v, want the defaults in place of values out of bounds", got)
	}
	if _, found := settings.Jails["made-up"]; found || len(settings.Jails) != len(jails) {
		t.Errorf("jails = %v", settings.Jails)
	}
	if !reflect.DeepEqual(settings.IgnoreIP, []string{"192.168.1.0/24"}) {
		t.Errorf("never banned = %v, want only the entry that is a network", settings.IgnoreIP)
	}
}
