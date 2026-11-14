package main

// Fork addition: the TLS listener asks Cloudflare's proxy for a client
// certificate ("Authenticated Origin Pulls", utils/fork_origin_pulls.go).
//
// The listener serves everyone: browsers at home, visitors of applications
// that are served directly, programs that talk to an application. None of
// them has a certificate to present, a browser that holds one for something
// else would show a chooser if it were asked, and a handshake that verified
// what it is given would fail for whoever presents something unexpected. So:
//
//   - nobody is asked unless the admin switched the option on, and then only
//     a peer whose address is inside Cloudflare's ranges. For every other
//     connection GetConfigForClient returns nil and the handshake is the one
//     it always was;
//   - the handshake asks (tls.RequestClientCert) and names the expected
//     issuers, but verifies nothing: what was presented is verified per
//     request by utils.OriginPullCheck against exactly those issuers, and a
//     certificate that does not fit ends in the guard's refusal with its
//     reason recorded (proxy/fork_cloudflare.go), not in a failed handshake;
//   - connections that are asked get no session tickets: a session opened
//     before the option was switched on would otherwise be resumed, without a
//     certificate, for as long as its ticket lives.

import (
	"crypto/tls"
	"crypto/x509"
	"sync"

	"github.com/azukaar/cosmos-server/src/utils"
)

// originPullTLS is the hook of startHTTPSServer: it returns the
// configuration it was given, with the choice per connection added.
func originPullTLS(base *tls.Config) *tls.Config {
	var mu sync.Mutex
	var asking *tls.Config
	var askingFor *x509.CertPool

	base.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		if hello == nil || hello.Conn == nil {
			return nil, nil
		}
		issuers := utils.OriginPullAsk(hello.Conn.RemoteAddr().String())
		if issuers == nil {
			return nil, nil
		}
		mu.Lock()
		defer mu.Unlock()
		if asking == nil || askingFor != issuers {
			// The session ticket keys stay the original's.
			asking = base.Clone()
			asking.GetConfigForClient = nil
			if len(asking.NextProtos) == 0 {
				// the HTTP server adds the protocols it speaks to a copy of
				// its configuration that cannot be seen from here: name
				// the same two (its defaults), or these connections alone
				// would fall back to HTTP/1.1. A test compares.
				asking.NextProtos = []string{"h2", "http/1.1"}
			}
			asking.ClientAuth = tls.RequestClientCert
			asking.ClientCAs = issuers
			asking.SessionTicketsDisabled = true
			askingFor = issuers
		}
		return asking, nil
	}
	return base
}
