package main

// Fork addition: the host terminal and the remote storage settings answer
// only where the rest of the administration does.
//
// "Admin only from these addresses" (and "admin only through Constellation")
// is applied to the admin router. The host terminal and the remote storage
// settings (which can hand out every remote's credentials) are registered on
// the router of the ordinary API, so that restriction did not reach them. It
// is applied to their handlers here; nothing else about them changes.

import (
	"net/http"
	"strings"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/utils"
)

// adminElsewhere reports the routes of the ordinary API that are
// administration all the same.
func adminElsewhere(template string) bool {
	return template == "/cosmos/api/terminal/{route}" || strings.HasPrefix(template, "/cosmos/rclone/")
}

// restrictAdminElsewhere is the hook of InitServer; it returns how many
// routes it restricted.
func restrictAdminElsewhere(router *mux.Router, restriction func(http.Handler) http.Handler) int {
	count := 0
	router.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		template, err := route.GetPathTemplate()
		if err != nil || !adminElsewhere(template) || route.GetHandler() == nil {
			return nil
		}
		route.Handler(restriction(route.GetHandler()))
		count++
		return nil
	})
	if count == 0 {
		utils.Warn("Fork: the host terminal and remote storage routes were not found; the admin address restriction is not applied to them")
	}
	return count
}
