package main

// Fork addition: the places where the server itself meets the security
// record (package security): its start, its read routes, the middleware
// that gives audit entries their outcome, and the requests nothing answers.

import (
	"net/http"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/proxy"
	"github.com/azukaar/cosmos-server/src/security"
	"github.com/azukaar/cosmos-server/src/utils"
)

// startSecurityRecord starts the recorder; from then on utils.RecordSecurity
// and utils.AuditSecurity write to it.
func startSecurityRecord() {
	security.Start()
}

// registerSecurityRoutes adds the read routes of the record to the admin
// router: behind the token check, the SmartShield and the admin address
// restriction, like their neighbours. Each checks PERM_ADMIN_READ itself.
func registerSecurityRoutes(admin *mux.Router) {
	admin.HandleFunc("/api/security/events", security.EventsRoute)
	admin.HandleFunc("/api/security/counts", security.CountsRoute)
	admin.HandleFunc("/api/security/top-ips", security.TopIPsRoute)
	admin.HandleFunc("/api/security/kinds", security.KindsRoute)
	admin.HandleFunc("/api/security/overview", security.OverviewRoute)   // the dashboard
	admin.HandleFunc("/api/security/incidents", security.IncidentsRoute) // the dashboard
}

// securityAudit is utils.SecurityAudit reading the status of the answer
// from the SmartShield's response wrapper, which every router that
// authenticates already has around its handlers: nothing more is wrapped
// around the response, so streaming and websockets behave as before.
var securityAudit = utils.SecurityAudit(func(w http.ResponseWriter) int {
	if wrapper, ok := w.(*proxy.SmartResponseWriterWrapper); ok {
		return wrapper.Status
	}
	return 0
})

// recordNotFound looks at a request that no route answers. Two things are
// worth a row: a path that scanners ask for, and a hostname this server
// does not serve (a scan of the address). Everything else is an ordinary
// 404 and is not recorded. No middleware has run for such a request, so
// the client address is worked out here, the same way.
func recordNotFound(r *http.Request) {
	defer func() { recover() }() // a 404 must stay a 404 whatever happens here
	if family := security.ProbeFamily(r.URL.Path); family != "" {
		utils.RecordSecurity(r, utils.SecScanProbe, "", "probe", family)
		return
	}
	if !utils.IsValidHostname(r.Host) {
		utils.RecordSecurity(r, utils.SecShieldHostname, "")
	}
}
