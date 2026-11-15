package proxy

// Fork addition: the local network rule of fork_local.go for other
// packages. The Turnstile check of the public forms (src/turnstile) never
// asks a visitor this rule calls local, so that the owner at home always
// signs in, with or without the internet.

import (
	"net/http"

	"github.com/azukaar/cosmos-server/src/utils"
)

// IsLocalVisitorRequest reports whether a request comes from the local
// network: the visitor's address as the other restrictions use it (set by
// utils.ClientRealIP: the peer, or what Cloudflare or a trusted proxy
// reported under the fork's rules), private, loopback, link-local, the
// carrier-NAT range or a network the server is attached to.
func IsLocalVisitorRequest(req *http.Request) bool {
	visitor, _ := req.Context().Value("ClientID").(string)
	if visitor == "" {
		visitor = utils.GetClientIP(req)
	}
	return isLocalVisitor(visitor, serverNetworks())
}
