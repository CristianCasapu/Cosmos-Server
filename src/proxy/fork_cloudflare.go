package proxy

// Fork addition: a hostname served through Cloudflare's proxy answers
// Cloudflare only.
//
// The admin chooses per application whether it is served through Cloudflare
// ("the shield") or directly (utils/fork_cloudflare.go, src/cloudflare). The
// record of a shielded name points at Cloudflare, but the server still
// listens on its own address: whoever knows that address can ask for the
// shielded name directly and go around everything Cloudflare filters. So a
// request for a shielded hostname is refused unless the connection itself
// comes from one of Cloudflare's published ranges, or from the local network
// (a device at home, the server itself): the way in for the owner that does
// not depend on Cloudflare.
//
// The decision reads the peer of the connection and the Host, never a header
// about the visitor: nothing a request says about itself opens the guard.

import (
	"net/http"

	"github.com/azukaar/cosmos-server/src/utils"
)

// originAllowed reports whether a request for a shielded hostname may be
// answered, given the peer of its connection.
func originAllowed(cf utils.ForkCloudflareConfig, peer string) bool {
	return utils.IsCloudflareAddress(cf, peer) || isLocalVisitor(peer, serverNetworks())
}

// CloudflareOriginGuard is placed on the main router, in front of the panel
// and of every URL.
func CloudflareOriginGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		cf := utils.GetMainConfig().HTTPConfig.Cloudflare
		if len(cf.Shielded) > 0 && utils.ShieldedIn(cf.Shielded, req.Host) {
			peer, _ := utils.SplitIP(req.RemoteAddr)
			if !originAllowed(cf, peer) {
				utils.RecordSecurity(req, utils.SecAccessOriginDenied, "") // security record
				utils.Warn("An address served through Cloudflare was requested around it (" + utils.CloudflareHostname(req.Host) + " from " + peer + "): refused")
				http.Error(w, "This address is available through Cloudflare only.", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, req)
	})
}

// cloudflareVisitorHeaders is called before a request is passed on to an
// application (fork_forwarded.go). The headers in which Cloudflare names the
// visitor are only left in place when this server itself believed them: the
// request came from Cloudflare for a hostname served through it. In every
// other case the visitor wrote them, and an application that reads them
// (many do, once told it is "behind Cloudflare") would take the visitor's
// word. peer is the peer of the connection, client the visitor's address as
// this server worked it out.
func cloudflareVisitorHeaders(req *http.Request, peer string, client string) {
	if client != "" && client != peer && utils.TrustedCloudflarePeer(peer) && utils.IsShieldedHost(req.Host) {
		req.Header.Set("CF-Connecting-IP", client)
		return
	}
	req.Header.Del("CF-Connecting-IP")
	req.Header.Del("CF-Connecting-IPv6")
	req.Header.Del("True-Client-IP")
}
