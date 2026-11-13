package proxy

// Fork addition: what an application behind a route is told about the
// visitor comes from this server, not from the visitor.
//
// The proxy adds headers the application relies on: who is logged in
// (x-cosmos-user and the like), the scheme and host that were asked for, the
// headers set on the route, and the visitor's address. Three things let a
// visitor interfere with them:
//
//   - A request may name headers in "Connection" that a proxy has to remove
//     before passing it on, and that removal happens after this server has
//     set its own headers. Only the tokens a connection needs (upgrade,
//     keep-alive, close) are kept, so a visitor cannot have this server's
//     headers taken out again.
//   - Many applications read a header written with underscores the same way
//     as with dashes (X_Cosmos_User, X_Forwarded_For). Such spellings of the
//     headers this server sets, or of the route's own headers, are dropped.
//   - X-Forwarded-For, X-Real-IP and Forwarded sent by the visitor were
//     passed on (the first with the real address merely added at the end). The
//     application now gets X-Forwarded-For and X-Real-IP with the address
//     this server itself works with: the peer of the connection, or what a
//     proxy listed as trusted reports. Forwarded is dropped.

import (
	"net/http"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

// headers this server sets for the application, or that speak about the
// visitor's address
var guardedHeaders = map[string]bool{
	"x-cosmos-user":      true,
	"x-cosmos-role":      true,
	"x-cosmos-user-role": true,
	"x-cosmos-mfa":       true,
	"x-cstln-auth":       true,
	"x-forwarded-for":    true,
	"x-forwarded-proto":  true,
	"x-forwarded-host":   true,
	"x-forwarded-port":   true,
	"x-forwarded-ssl":    true,
	"x-real-ip":          true,
	"forwarded":          true,
}

// forwardedHeaders is the hook of the proxy's director, called before it sets
// anything.
func forwardedHeaders(req *http.Request, route utils.ProxyRouteConfig) {
	keepConnectionTokens(req.Header)
	dropUnderscoreSpellings(req.Header, route)

	peer, _ := utils.SplitIP(req.RemoteAddr)
	client := GetClientID(req, route)

	req.Header.Del("Forwarded")
	if client == "" || client == peer {
		// nobody in front is trusted: the reverse proxy writes the peer's address
		req.Header.Del("X-Forwarded-For")
	} else {
		// a trusted proxy reported the visitor; the reverse proxy adds that proxy's address
		req.Header.Set("X-Forwarded-For", client)
	}
	if client == "" {
		client = peer
	}
	if client != "" {
		req.Header.Set("X-Real-IP", client)
	} else {
		req.Header.Del("X-Real-IP")
	}
}

// keepConnectionTokens leaves in Connection only what a connection needs.
func keepConnectionTokens(header http.Header) {
	values := header.Values("Connection")
	if len(values) == 0 {
		return
	}
	kept := []string{}
	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			token = strings.TrimSpace(token)
			switch strings.ToLower(token) {
			case "upgrade", "keep-alive", "close":
				kept = append(kept, token)
			}
		}
	}
	header.Del("Connection")
	if len(kept) > 0 {
		header.Set("Connection", strings.Join(kept, ", "))
	}
}

// dropUnderscoreSpellings removes headers that only differ from a guarded
// one, or from a header the route sets, by underscores (with or without the
// HTTP_ prefix some servers add).
func dropUnderscoreSpellings(header http.Header, route utils.ProxyRouteConfig) {
	for name := range header {
		if !strings.Contains(name, "_") {
			continue
		}
		plain := strings.TrimPrefix(strings.ToLower(strings.ReplaceAll(name, "_", "-")), "http-")
		if guardedHeaders[plain] {
			delete(header, name)
			continue
		}
		for extra := range route.ExtraHeaders {
			if strings.ToLower(strings.ReplaceAll(extra, "_", "-")) == plain {
				delete(header, name)
				break
			}
		}
	}
}
