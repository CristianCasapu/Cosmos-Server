package main

// Fork addition: two limits on what the API takes in.
//
//   - An API token in the address (?token=cosmos_...) is accepted when a
//     websocket is opened, which is what it exists for: a browser cannot send
//     a header there. Everywhere else a token belongs in the Authorization
//     header; an address ends up in histories, logs and the Referer of the
//     next page.
//   - The routes anyone can call without a session (login, register, password
//     reset, the second factor, sudo, the setup) read at most a few megabytes
//     of body. They read all of it into memory before looking at it.

import (
	"net/http"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/gorilla/mux"
)

// the largest body the sign-in routes read
const maxSignInBody = 4 << 20

// tokenFromQuery is the hook of tokenMiddleware: the API token given in the
// address of a websocket request, "" otherwise.
func tokenFromQuery(r *http.Request) string {
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return ""
	}
	if token := r.URL.Query().Get("token"); strings.HasPrefix(token, utils.APITokenPrefix) {
		return token
	}
	return ""
}

// limitRequestBodies is the hook of InitServer for the sign-in router.
func limitRequestBodies(limit int64, routers ...*mux.Router) {
	for _, router := range routers {
		router.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Body != nil {
					r.Body = http.MaxBytesReader(w, r.Body, limit)
				}
				next.ServeHTTP(w, r)
			})
		})
	}
}
