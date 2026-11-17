package main

// Fork addition (plan 46): see fork_http2.go.

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// waitingListener accepts nothing until it is closed, as the handoff's
// in-memory listeners do while no port route hands a connection over.
type waitingListener struct{ closed chan struct{} }

func (w waitingListener) Accept() (net.Conn, error) { <-w.closed; return nil, net.ErrClosed }
func (w waitingListener) Close() error {
	select {
	case <-w.closed:
	default:
		close(w.closed)
	}
	return nil
}
func (waitingListener) Addr() net.Addr { return &net.TCPAddr{} }

// The handoff's Serve may set the server up before ServeTLS does: HTTP/2
// must still be served (it was offered in the handshake and then the
// connection closed, for the server's whole life).
func TestHTTP2IsServedWhicheverServeComesFirst(t *testing.T) {
	certPEM, keyPEM := testCertPEM(t, "server.local")
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, r.Proto) }),
		TLSConfig: &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &cert, nil }}}
	nameHTTP2(srv)
	go srv.Serve(waitingListener{make(chan struct{})}) // the plain handoff first
	time.Sleep(50 * time.Millisecond)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.ServeTLS(ln, "", "")
	defer srv.Close()
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // the test's own certificate
	res, err := client.Get("https://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatalf("an HTTP/2 client after the handoff's Serve came first: %v", err)
	}
	defer res.Body.Close()
	if got, _ := io.ReadAll(res.Body); string(got) != "HTTP/2.0" {
		t.Fatalf("served over %q, want HTTP/2.0", got)
	}
}

func TestHTTP2IsNamedBeforeTheHandoff(t *testing.T) {
	raw, err := os.ReadFile("httpServer.go")
	if err != nil {
		t.Fatal(err)
	}
	server := string(raw)
	hook, handoff := strings.Index(server, "nameHTTP2(HTTPServer)"), strings.Index(server, "proxy.StartHTTPHandoff(HTTPServer, true)")
	if hook < 0 || handoff < 0 || hook > handoff {
		t.Error("startHTTPSServer must name HTTP/2 before the handoff serves on the server")
	}
}
