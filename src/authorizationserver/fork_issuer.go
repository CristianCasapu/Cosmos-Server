package authorizationserver

// Fork addition: the issuer of the panel's OpenID provider names the port the
// panel is reached on.
//
// Upstream built the issuer of the discovery document from the request's
// host with the port dropped when it was Cosmos' HTTPS port, and the "iss" of
// the ID tokens from the hostname setting alone. On a server whose HTTPS port
// is not 443 (2053, say) an app that reads
// https://cosmos.example.com:2053/.well-known/openid-configuration was told
// that the issuer and the token endpoint were https://cosmos.example.com,
// which is port 443, another machine or nothing: OpenID libraries that check
// the issuer against the address they asked refuse it, the others call the
// wrong machine.
//
// For a request on the panel's own name both now come from
// utils.PanelOriginFor (the hostname from the settings, the port the request
// used), so the document and the tokens always agree. Requests on an app's
// name keep upstream's behaviour.

import (
	"net/http"

	"github.com/azukaar/cosmos-server/src/utils"
)

// panelIssuer returns the issuer for a request made on the panel's hostname.
func panelIssuer(req *http.Request) (string, bool) {
	if req == nil {
		return "", false
	}
	if utils.HostWithoutPort(req.Host) != utils.GetMainConfig().HTTPConfig.Hostname {
		return "", false
	}
	return utils.PanelOriginFor(req), true
}
