package main

// Fork addition: where the server meets the protection cards of the security
// page (package protection): their one route, read only.

import (
	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/protection"
)

// registerProtectionRoutes adds the route to the admin router: behind the
// token check, the SmartShield and the admin address restriction, like its
// neighbours. It checks PERM_ADMIN_READ itself, as the Fail2ban status does,
// and answers GET only.
func registerProtectionRoutes(admin *mux.Router) {
	admin.HandleFunc("/api/protection", protection.StatusRoute)
}
