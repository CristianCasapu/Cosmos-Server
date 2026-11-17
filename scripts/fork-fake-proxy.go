//go:build ignore

// Fork addition (plan 49): a stand-in reverse proxy in front of a test
// server, for scripts/fork-ui-check-app-links.sh: the "public address" a
// visitor from the internet comes in through. It passes every request on to
// the origin as a reverse proxy the administrator runs would: the Host as the
// visitor sent it, X-Forwarded-For with the visitor's address appended,
// X-Forwarded-Proto. The test server lists this proxy as trusted, so the
// visitor it sees is the browser's own address behind the proxy.
//
//	go run scripts/fork-fake-proxy.go <listen address> <origin http://address:port>
//
// Used on Docker networks made with --internal only. It is not part of any
// build.
package main

import (
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		log.Fatal("usage: fork-fake-proxy.go <listen> <origin URL>")
	}
	origin, err := url.Parse(os.Args[2])
	if err != nil || origin.Host == "" {
		log.Fatal("origin: ", err)
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(origin)
			r.Out.Host = r.In.Host // the name the visitor asked for
			visitor, _, _ := net.SplitHostPort(r.In.RemoteAddr)
			prior := strings.TrimSpace(r.In.Header.Get("X-Forwarded-For"))
			if prior != "" {
				r.Out.Header.Set("X-Forwarded-For", prior+", "+visitor)
			} else {
				r.Out.Header.Set("X-Forwarded-For", visitor)
			}
			r.Out.Header.Set("X-Forwarded-Proto", "http")
			log.Printf("%s %s %s (from %s)", r.In.Method, r.In.Host, r.In.URL.Path, visitor)
		},
	}
	log.Fatal(http.ListenAndServe(os.Args[1], proxy))
}
