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
//
// Cloudflare's addresses are shared by all its customers. While the admin
// enforces Authenticated Origin Pulls (utils/fork_origin_pulls.go), a peer
// from those addresses must also have presented the expected client
// certificate on its connection: that is what shows Cloudflare's proxy made
// it (and, with a certificate of the server's own, that it made it for this
// server's zone). In "test" such requests are counted and let through.
//
// A peer of the local network passes without a certificate. The guard exists
// to make visitors from the internet pass Cloudflare; a device at home can
// already reach the panel and every application at the server's local
// address, behind the same logins, and nothing it says about another address
// is believed: its address is its own. Local is decided by the peer of the
// connection alone, and an address of Cloudflare's is never local.

import (
	"net/http"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Why a request for a shielded hostname is refused: one of these, or one of
// the utils.OriginPull... reasons. Recorded with the event.
const originNotCloudflare = "not_cloudflare"

// originRefusal says why a request for a shielded hostname may not be
// answered, "" when it may. peer is the peer of its connection.
func originRefusal(cf utils.ForkCloudflareConfig, peer string, req *http.Request) string {
	if utils.IsCloudflareAddress(cf, peer) {
		mode := utils.OriginPullMode(cf)
		if mode == utils.OriginPullOff {
			return ""
		}
		problem, issuer := utils.OriginPullCheck(cf, req.TLS)
		utils.CountOriginPull(utils.CloudflareHostname(req.Host), problem, issuer)
		if mode == utils.OriginPullEnforce {
			return problem
		}
		return ""
	}
	if isLocalVisitor(peer, serverNetworks()) {
		return ""
	}
	return originNotCloudflare
}

// CloudflareOriginGuard is placed on the main router, in front of the panel
// and of every URL.
func CloudflareOriginGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		cf := utils.GetMainConfig().HTTPConfig.Cloudflare
		if len(cf.Shielded) > 0 && utils.ShieldedIn(cf.Shielded, req.Host) {
			peer, _ := utils.SplitIP(req.RemoteAddr)
			if reason := originRefusal(cf, peer, req); reason != "" {
				utils.RecordSecurity(req, utils.SecAccessOriginDenied, "", "reason", reason) // security record
				if reason == originNotCloudflare {
					utils.Warn("An address served through Cloudflare was requested around it (" + utils.CloudflareHostname(req.Host) + " from " + peer + "): refused")
					http.Error(w, "This address is available through Cloudflare only.", http.StatusForbidden)
				} else {
					utils.Warn("A request for " + utils.CloudflareHostname(req.Host) + " came from Cloudflare's addresses (" + peer + ") without proof that Cloudflare's proxy made it (" + reason + "): refused")
					http.Error(w, "This address is available through Cloudflare's proxy only.", http.StatusForbidden)
				}
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
