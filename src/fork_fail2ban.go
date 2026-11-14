package main

// Fork addition: where the server meets the Fail2ban integration (package
// fail2ban): its routes and the watcher that records new bans.

import (
	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/cloudflare"
	"github.com/azukaar/cosmos-server/src/fail2ban"
)

func init() {
	// the never-ban list follows the hostnames served through Cloudflare
	// and Cloudflare's address ranges
	cloudflare.AfterChange = fail2ban.CloudflareChanged
}

// registerFail2banRoutes adds the routes to the admin router: behind the
// token check, the SmartShield and the admin address restriction, like
// their neighbours. Each checks its permission itself: reading takes
// PERM_ADMIN_READ, lifting a ban and saving settings PERM_ADMIN.
func registerFail2banRoutes(admin *mux.Router) {
	admin.HandleFunc("/api/fail2ban", fail2ban.StatusRoute)
	admin.HandleFunc("/api/fail2ban/unban", fail2ban.UnbanRoute)
	admin.HandleFunc("/api/fail2ban/settings", fail2ban.SettingsRoute)
}

// startFail2ban starts the watcher. It does nothing on a server without
// Fail2ban.
func startFail2ban() {
	fail2ban.Start()
}
