package fail2ban

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func golden(t *testing.T, name string, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs.\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func TestPanelFileIsRenderedAsTheGoldenFilesSay(t *testing.T) {
	clean, err := check(defaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "panel-default.golden", render(clean))

	custom := Settings{
		Jails: map[string]Thresholds{
			"cosmos-auth":  {MaxRetry: 3, FindTime: 7200, BanTime: 86400},
			"cosmos-probe": {MaxRetry: 1, FindTime: 600, BanTime: 2678400},
			"cosmos-scan":  {MaxRetry: 10, FindTime: 604800, BanTime: 300},
			"cosmos-abuse": {MaxRetry: 2, FindTime: 86400, BanTime: 43200},
			"sshd":         {MaxRetry: 4, FindTime: 900, BanTime: 7200},
			"recidive":     {MaxRetry: 2, FindTime: 172800, BanTime: 1209600},
		},
		// not canonical, repeated, with loopback given again
		IgnoreIP: []string{" 192.168.1.77/24 ", "2001:DB8:5::1/48", "203.0.113.7/32", "::ffff:198.51.100.9", "192.168.1.0/24", "::1", "127.0.0.0/8"},
	}
	clean, err = check(custom)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "panel-custom.golden", render(clean))

	wantList := []string{"127.0.0.0/8", "::1", "192.168.1.0/24", "2001:db8:5::/48", "203.0.113.7", "198.51.100.9"}
	if got := clean.settings().IgnoreIP; !reflect.DeepEqual(got, wantList) {
		t.Errorf("never banned = %v, want %v", got, wantList)
	}
}

// Whatever is accepted, the file consists of comment lines, the six section
// headers and "key = value" lines whose values are digits or addresses.
var panelLine = regexp.MustCompile(`^(?:|# [A-Za-z0-9 ,.:"'-]+|\[(?:cosmos-auth|cosmos-probe|cosmos-scan|cosmos-abuse|sshd|recidive)\]|(?:maxretry|findtime|bantime) = [0-9]{1,8}|ignoreip = [0-9a-f.:/ ]+)$`)

func assertOnlySafeLines(t *testing.T, content string) {
	t.Helper()
	sections := 0
	for _, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		if !panelLine.MatchString(line) {
			t.Errorf("unexpected line in the panel's file: %q", line)
		}
		if strings.HasPrefix(line, "[") {
			sections++
		}
	}
	if sections != len(jails) {
		t.Errorf("%d sections, want %d", sections, len(jails))
	}
}

func TestPanelFileHoldsNothingButNumbersAndAddresses(t *testing.T) {
	for _, list := range [][]string{
		nil,
		{},
		{"10.0.0.0/8"},
		{"2001:db8::/32", "fc00::/7", "fe80::/10", "100.64.0.0/10"},
		{"203.0.113.7", "::ffff:203.0.113.8", "2001:DB8::ABCD"},
	} {
		settings := defaultSettings()
		settings.IgnoreIP = list
		clean, err := check(settings)
		if err != nil {
			t.Fatalf("%v: %v", list, err)
		}
		assertOnlySafeLines(t, render(clean))
	}
}

// Values that must never reach the file. Each is refused as a whole: there
// is no "cleaning" that could let part of one through.
func TestSettingsRefuseEverythingThatIsNotANumberInBoundsOrAnAddress(t *testing.T) {
	hostileEntries := []string{
		"",
		" ",
		"10.0.0.0/8\nenabled = false",
		"10.0.0.0/8\r\n[sshd]\r\nenabled = false",
		"10.0.0.0/8 0.0.0.0/0",
		"10.0.0.0/8,192.168.0.0/16",
		"%(known/ignoreip)s",
		"%(__name__)s",
		"<ip>",
		"<known/ignoreip>",
		"[DEFAULT]",
		"# comment",
		"10.0.0.0/8 # comment",
		"; rm -rf /",
		"$(reboot)",
		"`reboot`",
		"home.example.org",
		"localhost",
		"fe80::1%eth0",
		"fe80::1%eth0/64",
		"1.2.3.4/33",
		"1.2.3.4/-1",
		"1.2.3.4/",
		"0.0.0.0/0",
		"::/0",
		"::ffff:0.0.0.0/96",
		"10.0.0.0/8\x00",
		"１.２.３.４",
		strings.Repeat("1", 500),
	}
	for _, entry := range hostileEntries {
		settings := defaultSettings()
		settings.IgnoreIP = []string{"192.168.0.0/16", entry}
		_, err := check(settings)
		var invalid *SettingsError
		if !errors.As(err, &invalid) {
			t.Errorf("entry %q was accepted", entry)
			continue
		}
		// the message says which entry, without repeating it
		if trimmed := strings.TrimSpace(entry); len(trimmed) > 3 && strings.Contains(err.Error(), trimmed) {
			t.Errorf("the error repeats the value: %q", err.Error())
		}
		if !strings.Contains(err.Error(), "Entry 2") {
			t.Errorf("the error does not say which entry: %q", err.Error())
		}
	}

	tooMany := defaultSettings()
	tooMany.IgnoreIP = nil
	for i := 0; i <= bounds.MaxIgnoreIP; i++ {
		tooMany.IgnoreIP = append(tooMany.IgnoreIP, fmt.Sprintf("203.0.113.%d", i))
	}
	if _, err := check(tooMany); err == nil {
		t.Errorf("%d entries were accepted, the bound is %d", len(tooMany.IgnoreIP), bounds.MaxIgnoreIP)
	}

	thresholds := map[string]Thresholds{
		"no tries":           {MaxRetry: 0, FindTime: 3600, BanTime: 3600},
		"negative tries":     {MaxRetry: -1, FindTime: 3600, BanTime: 3600},
		"more than ten":      {MaxRetry: 11, FindTime: 3600, BanTime: 3600},
		"short find time":    {MaxRetry: 5, FindTime: 599, BanTime: 3600},
		"long find time":     {MaxRetry: 5, FindTime: 7*86400 + 1, BanTime: 3600},
		"short ban":          {MaxRetry: 5, FindTime: 3600, BanTime: 299},
		"permanent ban":      {MaxRetry: 5, FindTime: 3600, BanTime: -1},
		"ban over 31 days":   {MaxRetry: 5, FindTime: 3600, BanTime: 31*86400 + 1},
		"everything is zero": {},
	}
	for name, hostile := range thresholds {
		for _, known := range jails {
			settings := defaultSettings()
			settings.Jails[known.name] = hostile
			if _, err := check(settings); err == nil {
				t.Errorf("%s in %s: accepted", name, known.name)
			}
		}
	}

	// the jails are the fork's six, no more, no fewer, no other
	missing := defaultSettings()
	delete(missing.Jails, "sshd")
	if _, err := check(missing); err == nil {
		t.Error("settings without sshd were accepted")
	}
	for _, name := range []string{"apache-auth", "sshd]\nenabled = false\n[x", "DEFAULT", "", "cosmos-auth "} {
		extra := defaultSettings()
		extra.Jails[name] = jails[0].defaults
		if _, err := check(extra); err == nil {
			t.Errorf("settings with a jail named %q were accepted", name)
		}
		swapped := defaultSettings()
		delete(swapped.Jails, "sshd")
		swapped.Jails[name] = jails[0].defaults
		if _, err := check(swapped); err == nil {
			t.Errorf("settings with %q in place of sshd were accepted", name)
		}
	}
	if _, err := check(Settings{}); err == nil {
		t.Error("empty settings were accepted")
	}
}

func TestApplyWritesTheFileReloadsAndSaves(t *testing.T) {
	folder := useJailDir(t, true)
	client := useFake(t, true, realAnswers(t))
	saved := captureSaved(t)

	settings := defaultSettings()
	settings.Jails["cosmos-auth"] = Thresholds{MaxRetry: 3, FindTime: 7200, BanTime: 86400}
	settings.IgnoreIP = []string{"192.168.1.77/24"}
	written, err := Apply(settings)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"127.0.0.0/8", "::1", "192.168.1.0/24"}; !reflect.DeepEqual(written.IgnoreIP, want) {
		t.Errorf("written list = %v, want %v", written.IgnoreIP, want)
	}

	path := filepath.Join(folder, panelFile)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertOnlySafeLines(t, string(content))
	if !strings.Contains(string(content), "[cosmos-auth]\nmaxretry = 3\nfindtime = 7200\nbantime = 86400\nignoreip = 127.0.0.0/8 ::1 192.168.1.0/24\n") {
		t.Errorf("the file does not hold the settings:\n%s", content)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0644 {
		t.Errorf("mode %o, want 0644", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(folder)
	if len(entries) != 2 {
		t.Errorf("%d files in the folder, want the fork's and the panel's only", len(entries))
	}
	if client.count("reload") != 1 {
		t.Errorf("reload was called %d times, want once", client.count("reload"))
	}
	if len(*saved) != 1 || !reflect.DeepEqual((*saved)[0], written) {
		t.Errorf("saved = %+v, want what was written once", *saved)
	}
}

func TestApplyPutsTheFileBackWhenFail2banRefusesIt(t *testing.T) {
	folder := useJailDir(t, true)
	path := filepath.Join(folder, panelFile)
	saved := captureSaved(t)

	answers := realAnswers(t)
	reloads := 0
	useFake(t, true, func(args []string) (string, error) {
		if args[0] == "reload" {
			reloads++
			if reloads == 1 {
				return "", errClient // the first reload, with the new file, fails
			}
		}
		return answers(args)
	})

	// no file before: none after
	if _, err := Apply(defaultSettings()); err == nil {
		t.Fatal("Apply succeeded although the reload failed")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the refused file was left in place (%v)", err)
	}
	if reloads != 2 {
		t.Errorf("%d reloads, want a second one after the file was taken back", reloads)
	}

	// a file before: the same file after
	before := "# what Fail2ban last accepted\n"
	if err := os.WriteFile(path, []byte(before), 0644); err != nil {
		t.Fatal(err)
	}
	reloads = 0
	if _, err := Apply(defaultSettings()); err == nil {
		t.Fatal("Apply succeeded although the reload failed")
	}
	if after, _ := os.ReadFile(path); string(after) != before {
		t.Errorf("the file is now %q, want what it was", after)
	}
	if len(*saved) != 0 {
		t.Errorf("refused settings were saved: %+v", *saved)
	}
}

func TestApplyWritesNothingWithoutTheForksInstallation(t *testing.T) {
	saved := captureSaved(t)

	// Fail2ban is there, the fork's jail file is not
	folder := useJailDir(t, false)
	client := useFake(t, true, realAnswers(t))
	if _, err := Apply(defaultSettings()); !errors.Is(err, errNoInstall) {
		t.Errorf("err = %v, want errNoInstall", err)
	}
	// Fail2ban is not there at all
	useFake(t, false, realAnswers(t))
	if _, err := Apply(defaultSettings()); !errors.Is(err, errNotInstalled) {
		t.Errorf("err = %v, want errNotInstalled", err)
	}
	// bad settings: refused before anything is looked at
	useJailDir(t, true)
	useFake(t, true, realAnswers(t))
	bad := defaultSettings()
	bad.IgnoreIP = []string{"10.0.0.0/8\nenabled = false"}
	if _, err := Apply(bad); err == nil {
		t.Error("hostile settings were applied")
	}
	if _, err := os.Stat(filepath.Join(jailDir, panelFile)); !os.IsNotExist(err) {
		t.Error("a file was written for refused settings")
	}

	if entries, _ := os.ReadDir(folder); len(entries) != 0 {
		t.Errorf("files were written without the fork's installation: %v", entries)
	}
	if len(client.called()) != 0 || len(*saved) != 0 {
		t.Errorf("client calls %v, saved %v: want neither", client.called(), *saved)
	}
}

func TestApplyDoesNotWriteThroughALink(t *testing.T) {
	folder := useJailDir(t, true)
	useFake(t, true, realAnswers(t))
	captureSaved(t)

	elsewhere := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(elsewhere, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(folder, panelFile)); err != nil {
		t.Skip("no symbolic links here:", err)
	}
	if _, err := Apply(defaultSettings()); err == nil {
		t.Error("Apply wrote although its file is a link")
	}
	if content, _ := os.ReadFile(elsewhere); string(content) != "untouched" {
		t.Errorf("the link's target was changed: %q", content)
	}
}
