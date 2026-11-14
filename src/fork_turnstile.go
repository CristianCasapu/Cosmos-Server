package main

// Fork addition: wires Cloudflare Turnstile on the public forms
// (src/turnstile) into the server: the check on the sign-in router, the
// settings routes, the public part of the settings for the sign-in pages,
// and the local network rule.

import (
	"net/http"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/proxy"
	"github.com/azukaar/cosmos-server/src/turnstile"
)

func init() {
	turnstile.IsLocal = proxy.IsLocalVisitorRequest
}

// forkTurnstile puts the check on the sign-in router. Called after
// SecureAPI, so the SmartShield and the token check run before it.
func forkTurnstile(strict *mux.Router) {
	strict.Use(turnstile.Middleware)
}

// registerTurnstileRoutes adds the settings routes to the admin router
// (same-origin check, token and session checks, the admin address
// restriction, like their neighbours); each checks its permission itself.
func registerTurnstileRoutes(admin *mux.Router) {
	admin.HandleFunc("/api/turnstile", turnstile.StatusRoute)
	turnstile.Start()
}

// forkTurnstilePublic is what the sign-in pages read to show the widget:
// nil when the check does not apply to this visitor.
func forkTurnstilePublic(req *http.Request) map[string]interface{} {
	return turnstile.Public(req)
}
