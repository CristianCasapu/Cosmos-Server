package fail2ban

import (
	"os"
	"path/filepath"
	"time"
)

// The short state of Fail2ban for the protection cards of the security page
// (src/protection): whether it is there, running and set up by the fork,
// without the jails and bans the full status asks for.

// Summary is that short state.
type Summary struct {
	// Installed: fail2ban-client is on this server.
	Installed bool
	// Running: the Fail2ban server answers.
	Running bool
	// ConfigInstalled: the fork's jail file is in Fail2ban's configuration.
	ConfigInstalled bool
	// ConfigSince is the modification time of the fork's jail file (zero
	// when it is not there).
	ConfigSince time.Time
}

// GetSummary returns the short state. A full status read less than
// statusTTL ago is reused; otherwise it costs one "ping" of the client at
// most (none when the client is not installed).
func GetSummary() Summary {
	summary := Summary{}
	statusLock.Lock()
	cached := lastStatus
	statusLock.Unlock()
	if cached != nil && time.Since(cached.read) <= statusTTL {
		summary.Installed = cached.status.Installed
		summary.Running = cached.status.Running
		summary.ConfigInstalled = cached.status.ConfigInstalled
	} else {
		summary.Installed = clientInstalled()
		summary.ConfigInstalled = configInstalled()
		summary.Running = summary.Installed && ping()
	}
	if info, err := os.Lstat(filepath.Join(jailDir, installedFile)); err == nil && info.Mode().IsRegular() {
		summary.ConfigSince = info.ModTime().UTC()
	} else {
		summary.ConfigInstalled = false
	}
	return summary
}
