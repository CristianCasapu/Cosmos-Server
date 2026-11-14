package fail2ban

import (
	"fmt"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// useCloudflare makes the package see these ranges as Cloudflare's, as it
// does while a hostname is served through Cloudflare (none: nothing is).
func useCloudflare(t *testing.T, ranges ...string) {
	t.Helper()
	previous := cloudflareRanges
	set := func(ranges ...string) {
		parsed := []netip.Prefix{}
		for _, text := range ranges {
			parsed = append(parsed, netip.MustParsePrefix(text))
		}
		cloudflareRanges = func() []netip.Prefix { return parsed }
	}
	set(ranges...)
	t.Cleanup(func() { cloudflareRanges = previous })
}

func useConfigWith(t *testing.T, change func(config *utils.Config)) {
	t.Helper()
	previous := utils.GetBaseMainConfig()
	config := utils.Config{}
	change(&config)
	utils.LoadBaseMainConfig(config)
	t.Cleanup(func() { utils.LoadBaseMainConfig(previous) })
}

func panelContent(t *testing.T, folder string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(folder, panelFile))
	if err != nil {
		t.Fatal(err)
	}
	assertOnlySafeLines(t, string(content))
	return string(content)
}

// The ranges come from what Cosmos has in force, only while a hostname is
// served through Cloudflare.
func TestCloudflareRangesFollowTheShieldedHostnames(t *testing.T) {
	useConfigWith(t, func(config *utils.Config) {})
	if got := cloudflareRanges(); len(got) != 0 {
		t.Errorf("nothing is served through Cloudflare: %v", got)
	}

	useConfigWith(t, func(config *utils.Config) {
		config.HTTPConfig.Cloudflare.Shielded = []string{"example.org"}
	})
	builtin := cloudflareRanges()
	if len(builtin) != 22 || !builtin[0].IsValid() {
		t.Fatalf("with a shielded hostname: %d ranges, want Cloudflare's 22 built-in ones", len(builtin))
	}
	for _, address := range []string{"173.245.48.9", "104.16.1.1", "2606:4700::9"} {
		covered := false
		for _, prefix := range builtin {
			covered = covered || prefix.Contains(netip.MustParseAddr(address))
		}
		if !covered {
			t.Errorf("%s is not covered", address)
		}
	}

	// a list fetched from Cloudflare replaces the built-in one; one that is no list of Cloudflare's does not
	useConfigWith(t, func(config *utils.Config) {
		config.HTTPConfig.Cloudflare.Shielded = []string{"example.org"}
		config.HTTPConfig.Cloudflare.RangesV4 = []string{"173.245.48.0/20"}
		config.HTTPConfig.Cloudflare.RangesV6 = []string{"2606:4700::/32"}
	})
	if got := cloudflareRanges(); len(got) != 2 {
		t.Errorf("a fetched list: %v", got)
	}
	useConfigWith(t, func(config *utils.Config) {
		config.HTTPConfig.Cloudflare.Shielded = []string{"example.org"}
		config.HTTPConfig.Cloudflare.RangesV4 = []string{"0.0.0.0/0"}
		config.HTTPConfig.Cloudflare.RangesV6 = []string{"::/0"}
	})
	if got := cloudflareRanges(); len(got) != 22 {
		t.Errorf("a list that covers everything was used: %d ranges", len(got))
	}
	for _, prefix := range cloudflareRanges() {
		if prefix.Bits() == 0 {
			t.Error("a range that covers every address is on the list")
		}
	}
}

func TestSavingFromThePanelKeepsCloudflareOnTheList(t *testing.T) {
	folder := useJailDir(t, true)
	useFake(t, true, realAnswers(t))
	saved := captureSaved(t)
	useCloudflare(t, "173.245.48.0/20", "2606:4700::/32")

	// the form sends back what the jails run with, Cloudflare's ranges
	// included, and the administrator's own entry
	settings := defaultSettings()
	settings.IgnoreIP = []string{"192.168.0.0/16", "173.245.48.0/20", "203.0.113.7", "2606:4700::/32"}
	written, err := Apply(settings)
	if err != nil {
		t.Fatal(err)
	}
	// the administrator's list is theirs alone: stored and shown without Cloudflare's
	if want := []string{"127.0.0.0/8", "::1", "192.168.0.0/16", "203.0.113.7"}; !reflect.DeepEqual(written.IgnoreIP, want) || !reflect.DeepEqual((*saved)[0].IgnoreIP, want) {
		t.Errorf("the administrator's list = %v (saved %v), want %v", written.IgnoreIP, (*saved)[0].IgnoreIP, want)
	}
	content := panelContent(t, folder)
	line := "ignoreip = 127.0.0.0/8 ::1 192.168.0.0/16 203.0.113.7 173.245.48.0/20 2606:4700::/32\n"
	if strings.Count(content, line) != len(jails) {
		t.Errorf("not every jail has the list with Cloudflare's ranges at its end:\n%s", content)
	}
	if !strings.Contains(content, "# The last 2 entries of each list are Cloudflare's address ranges.") {
		t.Errorf("the file does not say whose the last entries are:\n%s", content)
	}

	// Cloudflare's ranges do not use up the administrator's places
	full := defaultSettings()
	full.IgnoreIP = []string{"173.245.48.0/20", "2606:4700::/32"}
	for i := 0; i < bounds.MaxIgnoreIP; i++ {
		full.IgnoreIP = append(full.IgnoreIP, fmt.Sprintf("203.0.113.%d", i))
	}
	if _, err := Apply(full); err != nil {
		t.Errorf("a full list plus Cloudflare's ranges was refused: %v", err)
	}

	// nothing served through Cloudflare: no trace of it
	useCloudflare(t)
	if _, err := Apply(defaultSettings()); err != nil {
		t.Fatal(err)
	}
	if content := panelContent(t, folder); strings.Contains(content, "Cloudflare") || strings.Contains(content, "173.245.48.0") {
		t.Errorf("without a shielded hostname the file still mentions Cloudflare:\n%s", content)
	}
}

// Cosmos keeps the list current by itself: when the first hostname goes
// through Cloudflare, when Cloudflare's ranges change, when the last one is
// served directly again. And only then.
func TestTheNeverBanListFollowsCloudflareByItself(t *testing.T) {
	folder := useJailDir(t, true)
	client := useFake(t, true, realAnswers(t))
	saved := captureSaved(t)
	useConfigWith(t, func(config *utils.Config) {})
	path := filepath.Join(folder, panelFile)

	// nothing shielded, nothing saved in the panel: the installer's files stand
	useCloudflare(t)
	if SyncCloudflare() {
		t.Error("with nothing to add, something was written")
	}
	if _, err := os.Stat(path); err == nil || client.count("reload") != 0 {
		t.Fatal("a file was written, or Fail2ban reloaded, without need")
	}

	// the first hostname is served through Cloudflare
	useCloudflare(t, "173.245.48.0/20", "104.16.0.0/13", "2606:4700::/32")
	if !SyncCloudflare() {
		t.Fatal("Cloudflare's ranges were not written")
	}
	content := panelContent(t, folder)
	// what the jails ran with (the fixtures: the installer's list, the default thresholds), plus Cloudflare
	line := "ignoreip = 127.0.0.0/8 ::1 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 169.254.0.0/16 100.64.0.0/10 fc00::/7 fe80::/10 173.245.48.0/20 104.16.0.0/13 2606:4700::/32\n"
	if strings.Count(content, line) != len(jails) {
		t.Fatalf("the file does not keep the list the jails ran with and add Cloudflare's ranges:\n%s", content)
	}
	if client.count("reload") != 1 {
		t.Errorf("Fail2ban was told to reload %d times, want once", client.count("reload"))
	}
	if len(*saved) != 1 || len((*saved)[0].IgnoreIP) != 9 || len((*saved)[0].Jails) != len(jails) {
		t.Errorf("what the file is rendered from was not kept: %+v", *saved)
	}
	// from here on the kept settings are the base (as after a save in the panel)
	keep := (*saved)[0]
	useConfigWith(t, func(config *utils.Config) {
		config.Fail2ban.IgnoreIP = keep.IgnoreIP
		config.Fail2ban.Jails = map[string]utils.Fail2banJailConfig{}
		for name, thresholds := range keep.Jails {
			config.Fail2ban.Jails[name] = utils.Fail2banJailConfig{MaxRetry: thresholds.MaxRetry, FindTime: thresholds.FindTime, BanTime: thresholds.BanTime}
		}
	})

	// nothing changed: nothing is written, Fail2ban is left alone
	for i := 0; i < 3; i++ {
		if SyncCloudflare() {
			t.Error("the file was written again although nothing changed")
		}
	}
	if client.count("reload") != 1 || len(*saved) != 1 {
		t.Errorf("reloads %d, saves %d after three passes without a change", client.count("reload"), len(*saved))
	}

	// Cloudflare publishes another list
	useCloudflare(t, "173.245.48.0/20", "198.41.128.0/17")
	if !SyncCloudflare() {
		t.Fatal("the new ranges were not written")
	}
	content = panelContent(t, folder)
	if !strings.Contains(content, " fe80::/10 173.245.48.0/20 198.41.128.0/17\n") || strings.Contains(content, "104.16.0.0/13") || strings.Contains(content, "2606:4700::/32") {
		t.Errorf("after Cloudflare's list changed:\n%s", content)
	}

	// the last hostname is served directly again
	useCloudflare(t)
	if !SyncCloudflare() {
		t.Fatal("Cloudflare's ranges were not taken off")
	}
	content = panelContent(t, folder)
	if strings.Contains(content, "173.245.48.0") || strings.Contains(content, "Cloudflare") || !strings.Contains(content, "ignoreip = 127.0.0.0/8 ::1 10.0.0.0/8 ") {
		t.Errorf("after the last shield went:\n%s", content)
	}
	if client.count("reload") != 3 || len(*saved) != 1 {
		t.Errorf("reloads %d (want 3), saves %d (want 1: the administrator's list never changed)", client.count("reload"), len(*saved))
	}
}

func TestTheNeverBanListIsLeftAloneWhenItCannotBeKnown(t *testing.T) {
	useCloudflare(t, "173.245.48.0/20")
	useConfigWith(t, func(config *utils.Config) {})

	// no Fail2ban, or not the fork's files: Cosmos writes nothing
	folder := useJailDir(t, false)
	useFake(t, true, realAnswers(t))
	if SyncCloudflare() {
		t.Error("written on a server without the fork's Fail2ban files")
	}
	folder = useJailDir(t, true)
	useFake(t, false, realAnswers(t))
	if SyncCloudflare() {
		t.Error("written on a server without Fail2ban")
	}

	// Fail2ban does not answer and nothing was ever saved: what the jails run with is not known
	useFake(t, true, func(args []string) (string, error) { return "", errClient })
	if SyncCloudflare() {
		t.Error("written without knowing what the jails run with")
	}
	// the list could not be read: writing would shorten it
	answers := realAnswers(t)
	useFake(t, true, func(args []string) (string, error) {
		if len(args) == 3 && args[2] == "ignoreip" {
			return "", errClient
		}
		return answers(args)
	})
	if SyncCloudflare() {
		t.Error("written although the never-ban list could not be read")
	}
	// the list holds a host name, which the file cannot carry: not dropped silently
	useFake(t, true, func(args []string) (string, error) {
		if len(args) == 3 && args[2] == "ignoreip" {
			return "These IP addresses/networks are ignored:\n|- 10.0.0.0/8\n`- home.example.org\n", nil
		}
		return answers(args)
	})
	if SyncCloudflare() {
		t.Error("written although the list holds an entry that would be lost")
	}
	if entries, _ := os.ReadDir(folder); len(entries) != 1 {
		t.Errorf("%d files in the folder, want the fork's alone", len(entries))
	}
}

func TestTheNeverBanListIsPutBackWhenFail2banRefusesIt(t *testing.T) {
	folder := useJailDir(t, true)
	path := filepath.Join(folder, panelFile)
	captureSaved(t)
	useConfigWith(t, func(config *utils.Config) {})
	useCloudflare(t)
	useFake(t, true, realAnswers(t))
	if _, err := Apply(defaultSettings()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)

	answers := realAnswers(t)
	reloads := 0
	useFake(t, true, func(args []string) (string, error) {
		if args[0] == "reload" {
			reloads++
			if reloads == 1 {
				return "", errClient
			}
		}
		return answers(args)
	})
	useConfigWith(t, func(config *utils.Config) {
		config.Fail2ban.Jails = map[string]utils.Fail2banJailConfig{}
		for _, known := range jails {
			config.Fail2ban.Jails[known.name] = utils.Fail2banJailConfig{MaxRetry: known.defaults.MaxRetry, FindTime: known.defaults.FindTime, BanTime: known.defaults.BanTime}
		}
		config.Fail2ban.IgnoreIP = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
	})
	useCloudflare(t, "173.245.48.0/20")
	if SyncCloudflare() {
		t.Error("reported as written although Fail2ban refused")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) || reloads != 2 {
		t.Errorf("the file was not put back (reloads: %d):\n%s", reloads, after)
	}
}

// The status tells the administrator's entries from Cloudflare's, and
// whether the address the page is looked at from is a proxy's report.
func TestStatusTellsCloudflaresRangesFromTheAdministrators(t *testing.T) {
	useJailDir(t, true)
	useConfigWith(t, func(config *utils.Config) {
		config.HTTPConfig.Cloudflare.Shielded = []string{"example.org", "radarr.example.org"}
		config.HTTPConfig.Cloudflare.TrustRanges = true
	})
	useCloudflare(t, "173.245.48.0/20", "104.16.0.0/13", "2606:4700::/32")
	answers := realAnswers(t)
	useFake(t, true, func(args []string) (string, error) {
		if len(args) == 3 && args[2] == "ignoreip" {
			return "These IP addresses/networks are ignored:\n|- 127.0.0.0/8\n|- 192.168.0.0/16\n|- 173.245.48.0/20\n|- 2606:4700::/32\n`- ::1\n", nil
		}
		return answers(args)
	})

	status := GetStatus("203.0.113.7")
	if want := []string{"127.0.0.0/8", "192.168.0.0/16", "::1"}; !reflect.DeepEqual(status.Settings.IgnoreIP, want) {
		t.Errorf("the administrator's list = %v, want %v", status.Settings.IgnoreIP, want)
	}
	if status.CloudflareHostnames != 2 || status.CloudflareRanges != 3 || status.CloudflareIgnored != 2 {
		t.Errorf("Cloudflare: %d hostnames, %d ranges, %d of them on the list; want 2, 3, 2", status.CloudflareHostnames, status.CloudflareRanges, status.CloudflareIgnored)
	}
	// an address of Cloudflare's is never banned once the list holds it
	if cloudflare := GetStatus("173.245.48.9"); !cloudflare.ClientIgnored {
		t.Error("an address inside a range on the list counts as one that can be banned")
	}

	// who looks at the page through Cloudflare: the address is a report
	req := httptest.NewRequest("GET", "https://example.org:2053/cosmos/api/fail2ban", nil)
	req.Host = "example.org:2053"
	req.RemoteAddr = "173.245.48.9:40000"
	req.Header.Set("CF-Connecting-IP", "86.120.1.1")
	if got := statusFor(req); got.ClientIP != "86.120.1.1" || !got.ClientForwarded {
		t.Errorf("through Cloudflare: address %q, reported %v", got.ClientIP, got.ClientForwarded)
	}
	req = httptest.NewRequest("GET", "https://192.168.1.10:2053/cosmos/api/fail2ban", nil)
	req.RemoteAddr = "192.168.1.23:51000"
	if got := statusFor(req); got.ClientIP != "192.168.1.23" || got.ClientForwarded {
		t.Errorf("from home: address %q, reported %v", got.ClientIP, got.ClientForwarded)
	}
}

// The installer adds Cloudflare's ranges before Cosmos has written its own
// file. Its copy of the published list is the one built into the server.
func TestTheInstallersCloudflareListIsTheBuiltInOne(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "fork", "fail2ban", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, line := range strings.Split(string(script), "\n") {
		if text, found := strings.CutPrefix(line, `CLOUDFLARE_RANGES="`); found {
			listed = strings.Fields(strings.TrimSuffix(text, `"`))
		}
	}
	v4, v6, builtin := utils.CloudflareRangeLists(utils.ForkCloudflareConfig{})
	if !builtin {
		t.Fatal("an empty configuration does not give the built-in list")
	}
	want := append(append([]string{}, v4...), v6...)
	sort := func(list []string) []string {
		sorted := append([]string{}, list...)
		for i := range sorted {
			for j := i + 1; j < len(sorted); j++ {
				if sorted[j] < sorted[i] {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
		}
		return sorted
	}
	if !reflect.DeepEqual(sort(listed), sort(want)) {
		t.Errorf("fork/fail2ban/install.sh lists\n%v\nthe server has\n%v", sort(listed), sort(want))
	}
	if !strings.Contains(string(script), "last changed "+utils.CloudflareBuiltinDate) {
		t.Errorf("the installer does not give the date of the list (%s)", utils.CloudflareBuiltinDate)
	}
	// it reads Cloudflare from Cosmos' configuration, and refuses nothing for it
	for _, expected := range []string{`cloudflare.get("Shielded")`, `http.get("TrustedProxies")`} {
		if !strings.Contains(string(script), expected) {
			t.Errorf("the installer does not read %s", expected)
		}
	}
	if strings.Contains(string(script), "Run again with --no-cosmos-jails (SSH only) or, knowing this, with --behind-proxy") {
		t.Error("the installer still refuses when Cosmos stands behind a proxy")
	}
}
