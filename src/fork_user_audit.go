package main

// Fork addition: the routes of the user audit panel (src/security/useraudit*.go).

import (
	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/security"
)

// registerUserAuditRoutes adds the trail and the per-user summary to the
// admin router (behind the admin address restriction, PERM_ADMIN_READ
// checked by each route), and the own-activity route to the router every
// logged-in user reaches (the route checks the session itself).
func registerUserAuditRoutes(admin *mux.Router, user *mux.Router) {
	admin.HandleFunc("/api/security/audit", security.AuditRoute)
	admin.HandleFunc("/api/security/user-summary", security.UserSummaryRoute)
	user.HandleFunc("/api/security/my-activity", security.MyActivityRoute)
}
