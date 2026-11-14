package fail2ban

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/azukaar/cosmos-server/src/utils"
)

// This file is the only place where Cosmos writes Fail2ban configuration:
// one file, at a fixed path, rendered from a checked value. What is written
// consists of
//
//   - fixed text (the header, the keys),
//   - section names, which are jail names of the fixed list in jails.go,
//   - integers within the bounds of jails.go, printed by strconv,
//   - networks and addresses that were parsed by net/netip and are printed
//     by it: digits, a to f, dots, colons and one slash.
//
// None of these can contain a line break, a "%" (Fail2ban's interpolation),
// a "<" (its tags), a "[" or a "#", so nothing a request sends can add a
// line, a section or an option to Fail2ban's configuration.

var (
	// jailDir is where Fail2ban reads its jail files.
	jailDir = "/etc/fail2ban/jail.d"
	// installedFile is the fork's jail file, put there by the installer.
	// Cosmos writes its own file only next to it: it does not create
	// Fail2ban configuration on a host where the fork's was not installed.
	installedFile = "cosmos-10-jails.local"
	// panelFile is the file Cosmos writes. It sorts after the installer's
	// files (cosmos-10-..., cosmos-50-...), so its values win.
	panelFile = "cosmos-90-panel.local"
)

// Settings are what the panel sets: the thresholds of each of the fork's
// jails and the addresses that are never banned.
type Settings struct {
	Jails    map[string]Thresholds `json:"jails"`
	IgnoreIP []string              `json:"ignoreIP"`
}

// checked is Settings after validation: typed values only.
type checked struct {
	jails   map[string]Thresholds // one entry per jail of the fixed list
	ignored []netip.Prefix        // deduplicated, loopback first
}

var (
	loopback4 = netip.MustParsePrefix("127.0.0.0/8")
	loopback6 = netip.MustParsePrefix("::1/128")

	errNoInstall = errors.New("the Fail2ban files of the fork are not installed on this server")
)

// A SettingsError says what is wrong with settings in words for the
// administrator. Its text never repeats a value that was sent.
type SettingsError struct{ message string }

func (e *SettingsError) Error() string { return e.message }

func settingsError(format string, args ...any) error {
	return &SettingsError{message: fmt.Sprintf(format, args...)}
}

// check validates settings. Every jail of the fork must be there and
// nothing else; every threshold within the bounds; every entry of the list
// an address or a network. Loopback is always part of the result.
func check(settings Settings) (checked, error) {
	result := checked{jails: map[string]Thresholds{}}

	if len(settings.Jails) != len(jails) {
		return checked{}, settingsError("The settings must name each of the %d jails once", len(jails))
	}
	for _, known := range jails {
		thresholds, given := settings.Jails[known.name]
		if !given {
			return checked{}, settingsError("The settings of the jail %s are missing", known.name)
		}
		if thresholds.MaxRetry < bounds.MinMaxRetry || thresholds.MaxRetry > bounds.MaxMaxRetry {
			return checked{}, settingsError("%s: the number of tries must be between %d and %d", known.name, bounds.MinMaxRetry, bounds.MaxMaxRetry)
		}
		if thresholds.FindTime < bounds.MinFindTime || thresholds.FindTime > bounds.MaxFindTime {
			return checked{}, settingsError("%s: the time the tries are counted over must be between %d minutes and %d days", known.name, bounds.MinFindTime/60, bounds.MaxFindTime/86400)
		}
		if thresholds.BanTime < bounds.MinBanTime || thresholds.BanTime > bounds.MaxBanTime {
			return checked{}, settingsError("%s: the length of a ban must be between %d minutes and %d days", known.name, bounds.MinBanTime/60, bounds.MaxBanTime/86400)
		}
		result.jails[known.name] = thresholds
	}

	if len(settings.IgnoreIP) > bounds.MaxIgnoreIP {
		return checked{}, settingsError("At most %d addresses or networks can be listed as never banned", bounds.MaxIgnoreIP)
	}
	result.ignored = []netip.Prefix{loopback4, loopback6}
	seen := map[netip.Prefix]bool{loopback4: true, loopback6: true}
	for position, entry := range settings.IgnoreIP {
		prefix, ok := parsePrefix(strings.TrimSpace(entry))
		if !ok {
			return checked{}, settingsError("Entry %d of the addresses that are never banned is not an address (203.0.113.7) or a network (203.0.113.0/24)", position+1)
		}
		if prefix.Bits() == 0 {
			return checked{}, settingsError("Entry %d of the addresses that are never banned covers every address: nothing would ever be banned", position+1)
		}
		if !seen[prefix] {
			seen[prefix] = true
			result.ignored = append(result.ignored, prefix)
		}
	}
	return result, nil
}

// settings turns a checked value back into what the API and the saved
// configuration hold.
func (c checked) settings() Settings {
	result := Settings{Jails: map[string]Thresholds{}, IgnoreIP: []string{}}
	for name, thresholds := range c.jails {
		result.Jails[name] = thresholds
	}
	for _, prefix := range c.ignored {
		result.IgnoreIP = append(result.IgnoreIP, formatPrefix(prefix))
	}
	return result
}

// render produces the content of the panel's file.
func render(c checked) string {
	ignored := make([]string, 0, len(c.ignored))
	for _, prefix := range c.ignored {
		ignored = append(ignored, formatPrefix(prefix))
	}
	ignoreLine := strings.Join(ignored, " ")

	var b strings.Builder
	b.WriteString("# Written by Cosmos when the Fail2ban settings are saved in its panel.\n")
	b.WriteString("# Do not edit: it is replaced on the next save. It is read after\n")
	b.WriteString("# cosmos-10-jails.local and cosmos-50-host.local, so what it says wins.\n")
	b.WriteString("# Delete it and run \"fail2ban-client reload\" to go back to those files.\n")
	for _, known := range jails {
		thresholds := c.jails[known.name]
		b.WriteString("\n[" + known.name + "]\n")
		b.WriteString("maxretry = " + strconv.Itoa(thresholds.MaxRetry) + "\n")
		b.WriteString("findtime = " + strconv.Itoa(thresholds.FindTime) + "\n")
		b.WriteString("bantime = " + strconv.Itoa(thresholds.BanTime) + "\n")
		b.WriteString("ignoreip = " + ignoreLine + "\n")
	}
	return b.String()
}

// configInstalled reports whether the fork's jail file is on this host.
func configInstalled() bool {
	info, err := os.Lstat(filepath.Join(jailDir, installedFile))
	return err == nil && info.Mode().IsRegular()
}

// panelFileExists reports whether settings were written from the panel.
func panelFileExists() bool {
	info, err := os.Lstat(filepath.Join(jailDir, panelFile))
	return err == nil && info.Mode().IsRegular()
}

// replaceFile writes content to the panel's file through a temporary file
// in the same folder, so that Fail2ban never reads half of it. The folder
// is root's; the temporary name is not predictable and is created
// exclusively.
func replaceFile(content []byte) error {
	target := filepath.Join(jailDir, panelFile)
	// never write through a link somebody put in its place
	if info, err := os.Lstat(target); err == nil && !info.Mode().IsRegular() {
		return errors.New("the panel's file is not a regular file")
	}
	temporary, err := os.CreateTemp(jailDir, ".cosmos-panel-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	keep := false
	defer func() {
		if !keep {
			os.Remove(name)
		}
	}()
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(0644); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, target); err != nil {
		return err
	}
	keep = true
	return nil
}

var applyLock sync.Mutex

// Apply checks settings, writes them to the panel's file, makes Fail2ban
// read its configuration again and saves them in Cosmos' configuration. If
// Fail2ban refuses the reload, the file is put back as it was. It returns
// the settings as they were written.
func Apply(settings Settings) (Settings, error) {
	clean, err := check(settings)
	if err != nil {
		return Settings{}, err
	}
	if !clientInstalled() {
		return Settings{}, errNotInstalled
	}
	if !configInstalled() {
		return Settings{}, errNoInstall
	}

	applyLock.Lock()
	defer applyLock.Unlock()

	target := filepath.Join(jailDir, panelFile)
	previous, readErr := os.ReadFile(target)
	hadPrevious := readErr == nil

	if err := replaceFile([]byte(render(clean))); err != nil {
		utils.Error("Fail2ban: could not write the panel's settings file", err)
		return Settings{}, errors.New("the settings file could not be written")
	}
	if ping() {
		if err := reload(); err != nil {
			// back to what Fail2ban last accepted
			if hadPrevious {
				replaceFile(previous)
			} else {
				os.Remove(target)
			}
			reload()
			forgetStatus()
			return Settings{}, errors.New("Fail2ban did not accept the settings: they were not applied")
		}
	}
	forgetStatus()

	written := clean.settings()
	saveSettings(written)
	return written, nil
}

// saveSettings stores what was applied in Cosmos' configuration. Tests
// replace it.
var saveSettings = func(settings Settings) {
	utils.ConfigLock.Lock()
	defer utils.ConfigLock.Unlock()
	config := utils.ReadConfigFromFile()
	stored := utils.Fail2banConfig{Jails: map[string]utils.Fail2banJailConfig{}, IgnoreIP: settings.IgnoreIP}
	for name, thresholds := range settings.Jails {
		stored.Jails[name] = utils.Fail2banJailConfig{MaxRetry: thresholds.MaxRetry, FindTime: thresholds.FindTime, BanTime: thresholds.BanTime}
	}
	config.Fail2ban = stored
	utils.SetBaseMainConfig(config)
}
