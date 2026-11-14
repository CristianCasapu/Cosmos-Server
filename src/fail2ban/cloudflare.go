package fail2ban

import (
	"net/netip"
	"os"
	"path/filepath"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Cloudflare and the firewall.
//
// When the administrator serves a hostname through Cloudflare's proxy
// (utils/fork_cloudflare.go), every visitor of that hostname reaches the
// server from one of Cloudflare's addresses. A ban of such an address would
// cut all of them off, and it would stop nobody: the visitor comes back
// through another one. So while a hostname is served through Cloudflare,
// Cloudflare's ranges are on the never-ban list of the fork's jails. They
// are not the administrator's entries: Cosmos adds them to the file it
// writes, replaces them when Cloudflare's list changes, and takes them out
// when nothing is served through Cloudflare any more.
//
// (The security log has no line for such an address in the first place, nor
// for an address Cloudflare reported: see security/logfile.go. The list is
// the second lock, for whatever else writes to that log or bans by hand.)

// cloudflareRanges returns the ranges that must never be banned now: the
// list Cosmos has in force (validated when it was fetched, else the
// built-in one) while a hostname is served through Cloudflare, nothing
// otherwise. Tests replace it.
var cloudflareRanges = func() []netip.Prefix {
	cf := utils.GetMainConfig().HTTPConfig.Cloudflare
	if len(cf.Shielded) == 0 {
		return nil
	}
	v4, v6, _ := utils.CloudflareRangeLists(cf)
	ranges := make([]netip.Prefix, 0, len(v4)+len(v6))
	for _, text := range append(v4, v6...) {
		// parsed again here: only a parsed network is ever written
		if prefix, ok := parsePrefix(text); ok && prefix.Bits() > 0 {
			ranges = append(ranges, prefix)
		}
	}
	return ranges
}

// cloudflareHostnames is how many hostnames are served through Cloudflare.
func cloudflareHostnames() int {
	return len(utils.GetMainConfig().HTTPConfig.Cloudflare.Shielded)
}

// syncBase is what the panel's file is rendered from when Cosmos writes it
// by itself: what was last saved in the panel, else what the jails run with
// now (so that nothing but Cloudflare's ranges changes). ok is false when
// that cannot be known for sure yet.
func syncBase() (Settings, bool) {
	if len(utils.GetMainConfig().Fail2ban.Jails) > 0 {
		return savedSettings(), true
	}
	if !ping() {
		return Settings{}, false
	}
	read := readShared()
	status := read.status
	loaded := false
	for _, entry := range status.Jails {
		loaded = loaded || entry.Loaded
	}
	// a list that could not be read whole must not be written back shorter
	if status.Incomplete || !loaded || status.OtherIgnored > 0 ||
		len(read.ignored)-status.CloudflareIgnored > bounds.MaxIgnoreIP {
		return Settings{}, false
	}
	return status.Settings, true
}

// SyncCloudflare brings the panel's file in line with whether hostnames are
// served through Cloudflare and with Cloudflare's ranges as Cosmos has them.
// It writes only when the file would differ, and never on a server where
// the fork's Fail2ban files are not installed. It reports whether it wrote.
func SyncCloudflare() bool {
	if !clientInstalled() || !configInstalled() {
		return false
	}
	want := cloudflareRanges()

	applyLock.Lock()
	defer applyLock.Unlock()

	current, readErr := os.ReadFile(filepath.Join(jailDir, panelFile))
	if readErr != nil && len(want) == 0 {
		// nothing to add, and nothing was ever written from the panel: the
		// installer's files stand as they are
		return false
	}
	base, ok := syncBase()
	if !ok {
		return false
	}
	clean, err := check(base)
	if err != nil {
		utils.Warn("Fail2ban: the saved settings are not valid any more, Cloudflare's address ranges were not added to the never-ban list: " + err.Error())
		return false
	}
	clean.cloudflare = want
	if readErr == nil && string(current) == render(clean) {
		return false
	}
	if err := write(clean); err != nil {
		utils.Warn("Fail2ban: Cloudflare's address ranges could not be put on the never-ban list: " + err.Error())
		return false
	}
	if len(utils.GetMainConfig().Fail2ban.Jails) == 0 {
		// from now on the file is rendered from this
		saveSettings(clean.settings())
	}
	if len(want) > 0 {
		utils.Log("Fail2ban: Cloudflare's address ranges are on the never-ban list of the jails")
	} else {
		utils.Log("Fail2ban: nothing is served through Cloudflare any more, its address ranges were taken off the never-ban list")
	}
	return true
}

// CloudflareChanged is called when the hostnames served through Cloudflare
// or Cloudflare's ranges changed. It returns at once.
func CloudflareChanged() {
	go func() {
		defer func() { recover() }()
		SyncCloudflare()
	}()
}
