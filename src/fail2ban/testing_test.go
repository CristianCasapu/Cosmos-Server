package fail2ban

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// fake stands in for fail2ban-client: it answers from a function and keeps
// every argument list it was called with.
type fake struct {
	mu    sync.Mutex
	calls [][]string
}

func (f *fake) called() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.calls...)
}

// count is how many calls began with these arguments.
func (f *fake) count(prefix ...string) int {
	found := 0
	for _, call := range f.called() {
		if len(call) >= len(prefix) && strings.Join(call[:len(prefix)], "\x00") == strings.Join(prefix, "\x00") {
			found++
		}
	}
	return found
}

// useFake replaces the client for a test. installed says whether the
// binary is "there".
func useFake(t *testing.T, installed bool, answer func(args []string) (string, error)) *fake {
	t.Helper()
	f := &fake{}
	previousRun, previousInstalled := runClient, clientInstalled
	runClient = func(args ...string) (string, error) {
		f.mu.Lock()
		f.calls = append(f.calls, append([]string(nil), args...))
		f.mu.Unlock()
		return answer(args)
	}
	clientInstalled = func() bool { return installed }
	forgetStatus()
	t.Cleanup(func() {
		runClient, clientInstalled = previousRun, previousInstalled
		forgetStatus()
	})
	return f
}

// realAnswers answers like the Fail2ban the fixtures were captured from:
// six jails, three addresses banned by cosmos-auth, one by cosmos-probe.
func realAnswers(t *testing.T) func(args []string) (string, error) {
	t.Helper()
	return func(args []string) (string, error) {
		switch strings.Join(args, " ") {
		case "ping":
			return fixture(t, "ping.txt"), nil
		case "status":
			return fixture(t, "status.txt"), nil
		case "status cosmos-auth":
			return fixture(t, "jail-banned.txt"), nil
		case "status cosmos-probe":
			return fixture(t, "jail-probe.txt"), nil
		case "get cosmos-auth banip --with-time":
			return fixture(t, "banip-with-time.txt"), nil
		case "get cosmos-probe banip --with-time":
			return "203.0.113.7 \t2026-11-13 14:02:55 + 86400 = 2026-11-14 14:02:55\n", nil
		case "banned":
			return fixture(t, "banned.txt"), nil
		case "reload":
			return fixture(t, "reload.txt"), nil
		}
		switch {
		case len(args) == 2 && args[0] == "status":
			return fixture(t, "jail-empty.txt"), nil
		case len(args) == 4 && args[0] == "get" && args[2] == "banip":
			return fixture(t, "banip-empty.txt"), nil
		case len(args) == 3 && args[0] == "get" && args[2] == "ignoreip":
			return fixture(t, "ignoreip.txt"), nil
		case len(args) == 3 && args[0] == "get" && args[2] == "maxretry":
			return fixture(t, "get-maxretry.txt"), nil
		case len(args) == 3 && args[0] == "get" && args[2] == "findtime":
			return fixture(t, "get-findtime.txt"), nil
		case len(args) == 3 && args[0] == "get" && args[2] == "bantime":
			return fixture(t, "get-bantime.txt"), nil
		case len(args) == 4 && args[0] == "set" && args[2] == "unbanip":
			if args[3] == "203.0.113.7" {
				return fixture(t, "unban-one.txt"), nil
			}
			return fixture(t, "unban-none.txt"), nil
		}
		return "", errClient
	}
}

// useJailDir points the package at a folder of the test's; installed says
// whether the fork's jail file is in it.
func useJailDir(t *testing.T, installed bool) string {
	t.Helper()
	previous := jailDir
	jailDir = t.TempDir()
	if installed {
		if err := os.WriteFile(filepath.Join(jailDir, installedFile), []byte("[DEFAULT]\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { jailDir = previous })
	return jailDir
}

// captureSaved replaces the saving of settings into Cosmos' configuration.
func captureSaved(t *testing.T) *[]Settings {
	t.Helper()
	saved := &[]Settings{}
	previous := saveSettings
	saveSettings = func(settings Settings) { *saved = append(*saved, settings) }
	t.Cleanup(func() { saveSettings = previous })
	return saved
}

// captureSecurity collects what is recorded in the security record.
func captureSecurity(t *testing.T) func() []utils.SecurityEvent {
	t.Helper()
	var mu sync.Mutex
	events := []utils.SecurityEvent{}
	utils.SetSecurityRecorder(func(event utils.SecurityEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}, func() {})
	t.Cleanup(func() { utils.SetSecurityRecorder(nil, nil) })
	return func() []utils.SecurityEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]utils.SecurityEvent(nil), events...)
	}
}

func defaultSettings() Settings {
	settings := Settings{Jails: map[string]Thresholds{}, IgnoreIP: []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}}
	for _, known := range jails {
		settings.Jails[known.name] = known.defaults
	}
	return settings
}
