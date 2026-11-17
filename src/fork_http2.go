package main

// Fork addition (plan 46): HTTP/2 on the HTTPS server, whatever starts first.

import "net/http"

// nameHTTP2 names HTTP/2 in the HTTPS server's protocols before anything
// serves on it (hook in startHTTPSServer). The handoff of the port routes
// (proxy.StartHTTPHandoff) runs Serve on a plain listener of the same
// server while ServeTLS starts on others, and net/http sets HTTP/2 up once,
// by whichever comes first: when Serve came first it left HTTP/2 out (a
// TLS configuration that does not name "h2"), and ServeTLS still offered it
// in the handshake, so every HTTP/2 connection (every browser) was closed
// right after the handshake, for as long as that server lived. Named here,
// both ways set it up. Upstream's race, seen as "the HTTPS listener closes
// every connection until Cosmos restarts" (plan 44, plan 46).
func nameHTTP2(srv *http.Server) {
	if len(srv.TLSConfig.NextProtos) == 0 {
		srv.TLSConfig.NextProtos = []string{"h2", "http/1.1"} // what net/http offers by default
	}
}
