package main

// Fork addition: wires the blocks at Cloudflare's edge (src/edgeblock) into
// the server: the routes of the card, the start of its worker, and the note
// of the addresses administrators use (never blocked at the edge).

import (
	"net/http"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/dnsrecords"
	"github.com/azukaar/cosmos-server/src/edgeblock"
	"github.com/azukaar/cosmos-server/src/proxy"
	"github.com/azukaar/cosmos-server/src/utils"
)

// registerEdgeBlockRoutes adds the routes to the admin router (same-origin
// check, token and session checks, the admin address restriction, like their
// neighbours); each checks its permission and method itself.
func registerEdgeBlockRoutes(admin *mux.Router) {
	admin.HandleFunc("/api/cloudflare/edge-blocks", edgeblock.StatusRoute)
	admin.HandleFunc("/api/cloudflare/edge-blocks/keys", edgeblock.BlockedRoute)
	edgeblock.Start(dnsrecords.CurrentAddress, proxy.IsLocalAddress)
}

// noteEdgeAdmin remembers the address of a request made by an administrator's
// full session (password and second factor done; not an API token).
func noteEdgeAdmin(req *http.Request) {
	auth := utils.GetAuthContext(req)
	if auth.APIToken != nil || auth.Nickname == "" || auth.UserRole != utils.ADMIN || auth.MFAState != 0 {
		return
	}
	edgeblock.NoteAdmin(utils.SecurityClientIP(req))
}
