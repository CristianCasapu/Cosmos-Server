package main

// Fork addition (plan 46): the restarts of the web server, see
// fork_server_lifecycle.go. The serving loop below is StartServer's, with
// servers that answer which generation they are instead of InitServer's
// router (which needs Docker and the databases).

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func testRestarts() *restarts {
	r := newRestarts()
	r.log = func(string) {}
	r.bindFor = 2 * time.Second
	r.bindEvery = 10 * time.Millisecond
	return r
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// loop is StartServer: a generation is built (InitServer: build, when set),
// served until a restart ends it, and built again. It reports the error
// that would have ended the process.
type loop struct {
	r           *restarts
	main, plain string
	generation  atomic.Int64
	ended       chan error
	tls         bool
}

type loopSetup struct {
	handler func(gen int64) http.Handler
	build   func(gen int64) // what InitServer does, before the generation listens
	tls     bool            // main over TLS, with the handoff's Serve started first as startHTTPSServer does
}

func generationHandler(gen int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/slow" {
			time.Sleep(700 * time.Millisecond)
		}
		fmt.Fprintf(w, "%d", gen)
	})
}

func startLoop(t *testing.T, r *restarts) *loop {
	return startLoopWith(t, r, loopSetup{})
}

func startLoopWith(t *testing.T, r *restarts, setup loopSetup) *loop {
	if setup.handler == nil {
		setup.handler = generationHandler
	}
	var tlsConfig *tls.Config
	if setup.tls {
		certPEM, keyPEM := testCertPEM(t, "server.local")
		cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
		if err != nil {
			t.Fatal(err)
		}
		tlsConfig = &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &cert, nil }}
	}
	l := &loop{r: r, main: freePort(t), plain: freePort(t), ended: make(chan error, 1), tls: setup.tls}
	go func() {
		for {
			gen := l.generation.Add(1)
			if setup.build != nil {
				setup.build(gen)
			}
			gated := r.routerOf(setup.handler(gen)) // as startHTTP(S)Server do
			main := &http.Server{Addr: l.main, Handler: gated, TLSConfig: tlsConfig.Clone(), ConnState: r.connState}
			plain := &http.Server{Addr: l.plain, Handler: gated, ConnState: r.connState}
			if setup.tls {
				// as startHTTPSServer and proxy.StartHTTPHandoff do: ServeTLS
				// and Serve on listeners of their own, before the server listens
				nameHTTP2(main)
				go main.ServeTLS(&brokenListener{block: make(chan struct{})}, "", "")
				go main.Serve(&brokenListener{block: make(chan struct{})})
			}
			if err := r.serve(main, setup.tls, plain); !errors.Is(err, http.ErrServerClosed) {
				l.ended <- err
				return
			}
		}
	}()
	return l
}

func askGen(addr string, path string) (string, error) {
	return askOver("http", addr, path)
}

func askOver(scheme, addr string, path string) (string, error) {
	c := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DisableKeepAlives: true, ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // the test's own certificate, HTTP/2 as browsers
	res, err := c.Get(scheme + "://" + addr + path)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	return string(body), err
}

// answering waits until both listeners answer, and returns their generations.
func (l *loop) answering(t *testing.T) (string, string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		scheme := "http"
		if l.tls {
			scheme = "https"
		}
		a, errA := askOver(scheme, l.main, "/")
		b, errB := askGen(l.plain, "/")
		if errA == nil && errB == nil && a == b && a == fmt.Sprint(l.generation.Load()) {
			return a, b
		}
		if time.Now().After(deadline) {
			t.Fatalf("the listeners do not both answer with the newest generation %d: main %q %v, plain %q %v", l.generation.Load(), a, errA, b, errB)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (l *loop) quiet(t *testing.T) {
	t.Helper()
	select {
	case err := <-l.ended:
		t.Fatalf("the serving loop ended (the process would have): %v", err)
	default:
	}
}

func TestRestartReloadsCoalesce(t *testing.T) {
	r := testRestarts()
	var mu sync.Mutex
	type span struct{ from, to time.Time }
	reloads := []span{}
	reload := func() {
		from := time.Now()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		reloads = append(reloads, span{from, time.Now()})
		mu.Unlock()
	}
	// one reload under way, then 40 callers at once
	go r.restart(reload)
	time.Sleep(10 * time.Millisecond)
	var wg sync.WaitGroup
	calls := make([]span, 40)
	for i := range calls {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			calls[i].from = time.Now()
			r.restart(reload)
			calls[i].to = time.Now()
		}(i)
	}
	wg.Wait()
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(reloads) != 2 {
		t.Fatalf("41 restarts, the last 40 while the first reloaded: want 2 reloads, got %d", len(reloads))
	}
	// the contract of every caller: when it returns, a reload that began
	// after its call has finished
	for i, c := range calls {
		covered := false
		for _, rl := range reloads {
			if !rl.from.Before(c.from) && !rl.to.After(c.to) {
				covered = true
			}
		}
		if !covered {
			t.Errorf("caller %d returned without a reload that began after its call", i)
		}
	}
	if len(r.wanted) != 1 {
		t.Errorf("41 restarts must want exactly one more generation, %d wanted", len(r.wanted))
	}
}

func TestRestartReloadPanicLetsTheNextOneThrough(t *testing.T) {
	r := testRestarts()
	func() {
		defer func() {
			if recover() == nil {
				t.Error("a panic of the reload must reach the caller, as upstream's did")
			}
		}()
		r.restart(func() { panic("reload failed") })
	}()
	done := make(chan struct{})
	go func() {
		r.restart(func() {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the restart after a panicking one never returned")
	}
}

func TestRestartWithoutServingLoopDoesNotWait(t *testing.T) {
	// before the first start, during the process's exit, after the loop
	// ended: nobody serves, a restart still returns
	r := testRestarts()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			r.restart(func() {})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("restarts with no serving loop blocked")
	}
}

func TestRestartAskedBeforeTheFirstStart(t *testing.T) {
	r := testRestarts()
	r.restart(func() {})
	listened := make(chan string, 10)
	listen := r.listen
	r.listen = func(addr string) (net.Listener, error) {
		listened <- addr
		return listen(addr)
	}
	l := startLoop(t, r)
	// the first generation listens, then gives way to one more
	l.answering(t)
	if got := l.generation.Load(); got != 2 {
		t.Errorf("a restart asked before the first start: want 2 generations, got %d", got)
	}
	if len(listened) != 2 {
		t.Errorf("the two sockets are bound once and kept for the next generation: %d binds", len(listened))
	}
	l.quiet(t)
}

func TestRestartsCloseTogetherKeepBothListeners(t *testing.T) {
	r := testRestarts()
	l := startLoop(t, r)
	l.answering(t)
	for round := 0; round < 30; round++ {
		before := l.generation.Load()
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r.restart(func() {})
			}()
			if round%3 == 0 {
				time.Sleep(time.Duration(i%4) * time.Millisecond)
			}
		}
		wg.Wait()
		l.answering(t)
		// 20 restarts: the one under way and at most one more per restart
		// that came in while a generation was being built
		if after := l.generation.Load(); after <= before {
			t.Fatalf("round %d: 20 restarts and no new generation", round)
		}
	}
	l.quiet(t)
}

func TestRestartCoalescesWhileAGenerationStarts(t *testing.T) {
	r := testRestarts()
	// hold the next generation while its router is built: every restart
	// asked meanwhile gives one generation more
	gate := make(chan struct{})
	building := make(chan struct{})
	l := startLoopWith(t, r, loopSetup{build: func(gen int64) {
		if gen == 2 {
			close(building)
			<-gate
		}
	}})
	l.answering(t)
	r.restart(func() {})
	<-building
	for i := 0; i < 50; i++ {
		r.restart(func() {})
	}
	close(gate)
	l.answering(t)
	time.Sleep(100 * time.Millisecond)
	if got := l.generation.Load(); got != 3 {
		t.Errorf("one restart, then 50 while its router was built: want 3 generations in all, got %d", got)
	}
	l.quiet(t)
}

func TestRestartFreesThePortsForAChallengeAndWaitsForThem(t *testing.T) {
	r := testRestarts()
	var l *loop
	challenge := make(chan error, 1)
	l = startLoopWith(t, r, loopSetup{build: func(gen int64) {
		if gen != 2 {
			return
		}
		// a certificate order while the router is built: lego's own
		// challenge server takes the port for a moment
		r.release()
		ln, err := net.Listen("tcp", l.main)
		challenge <- err
		if err == nil {
			go func() { time.Sleep(300 * time.Millisecond); ln.Close() }()
		}
	}})
	l.answering(t)
	r.release() // while a generation serves: nothing is freed
	if _, err := askGen(l.main, "/"); err != nil {
		t.Fatalf("freeing the ports while a generation serves must do nothing: %v", err)
	}
	r.restart(func() {})
	if err := <-challenge; err != nil {
		t.Fatalf("between two generations the ports must be freed for a challenge: %v", err)
	}
	l.answering(t) // bound again once the challenge let go
	l.quiet(t)
}

func TestRestartRefusesNoConnection(t *testing.T) {
	for _, overTLS := range []bool{false, true} {
		t.Run(map[bool]string{false: "http", true: "https"}[overTLS], func(t *testing.T) {
			r := testRestarts()
			// building a router takes a while: the gap a visitor would meet
			l := startLoopWith(t, r, loopSetup{tls: overTLS, build: func(int64) { time.Sleep(15 * time.Millisecond) }})
			l.answering(t)
			scheme := map[bool]string{false: "http", true: "https"}[overTLS]
			stop := make(chan struct{})
			var mu sync.Mutex
			failures := map[string]int{}
			asked := 0
			var wg sync.WaitGroup
			for w := 0; w < 4; w++ {
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					for {
						select {
						case <-stop:
							return
						default:
						}
						addr, sch := l.main, scheme
						if w%2 == 1 {
							addr, sch = l.plain, "http"
						}
						_, err := askOver(sch, addr, "/")
						mu.Lock()
						asked++
						if err != nil {
							kind := "other: " + err.Error()
							if errors.Is(err, syscall.ECONNREFUSED) {
								kind = "refused"
							} else if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "EOF") || errors.Is(err, syscall.ECONNRESET) ||
								strings.Contains(err.Error(), "client conn could not be established") {
								kind = "closed before an answer"
							}
							failures[kind]++
						}
						mu.Unlock()
					}
				}(w)
			}
			for i := 0; i < 100; i++ {
				r.restart(func() {})
				time.Sleep(time.Duration(i%5) * 10 * time.Millisecond)
			}
			l.answering(t)
			close(stop)
			wg.Wait()
			t.Logf("%d requests on new connections across %d generations, failed: %v", asked, l.generation.Load(), failures)
			for kind, n := range failures {
				// Go's Shutdown closes a connection whose request it reads
				// in the very instant it begins (HTTP/1), or that has not
				// opened its first stream yet (HTTP/2, which browsers retry):
				// rare, and upstream's too
				if kind != "closed before an answer" || n > 3 {
					t.Errorf("%d connections %s across the restarts", n, kind)
				}
			}
			l.quiet(t)
		})
	}
}

func TestRestartIsHeldAtMostBrieflyByASilentConnection(t *testing.T) {
	r := testRestarts()
	r.firstWait = 300 * time.Millisecond
	l := startLoop(t, r)
	l.answering(t)
	// connects and never sends a request
	silent, err := net.Dial("tcp", l.main)
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	r.restart(func() {})
	l.answering(t)
	if took := time.Since(start); took > time.Second {
		t.Errorf("a silent connection held the restart for %s, want about %s", took, r.firstWait)
	}
	l.quiet(t)
}

func TestRestartLetsALongDownloadFinish(t *testing.T) {
	r := testRestarts()
	l := startLoopWith(t, r, loopSetup{tls: true, handler: func(gen int64) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path == "/big" {
				for i := 0; i < 30; i++ {
					w.Write(make([]byte, 32<<10))
					w.(http.Flusher).Flush()
					time.Sleep(50 * time.Millisecond)
				}
				return
			}
			fmt.Fprintf(w, "%d", gen)
		})
	}})
	l.answering(t)
	c := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	res, err := c.Get("https://" + l.main + "/big")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10; i++ {
			r.restart(func() {})
			time.Sleep(80 * time.Millisecond)
		}
		close(done)
	}()
	n, err := io.Copy(io.Discard, res.Body)
	<-done
	if err != nil || n != 30*32<<10 {
		t.Errorf("a download over %s across 10 restarts: %d bytes of %d, %v", res.Proto, n, 30*32<<10, err)
	}
	l.answering(t)
	l.quiet(t)
}

func TestRestartOntoOtherPortsClosesTheOldOnes(t *testing.T) {
	r := testRestarts()
	first, second := freePort(t), freePort(t)
	ended := make(chan error, 1)
	go func() {
		for _, addr := range []string{first, second} {
			if err := r.serve(&http.Server{Addr: addr, Handler: generationHandler(1)}, false, nil); !errors.Is(err, http.ErrServerClosed) {
				ended <- err
				return
			}
		}
		ended <- nil
	}()
	waitAnswer := func(addr string) {
		for i := 0; i < 500; i++ {
			if _, err := askGen(addr, "/"); err == nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("%s does not answer", addr)
	}
	waitAnswer(first)
	r.restart(func() {}) // the settings moved the port
	waitAnswer(second)
	if _, err := askGen(first, "/"); !errors.Is(err, syscall.ECONNREFUSED) {
		t.Errorf("the socket of the port no longer configured must be closed: %v", err)
	}
	r.restart(func() {})
	if err := <-ended; err != nil {
		t.Fatal(err)
	}
}

func TestRestartEndsWhenThePortStaysTaken(t *testing.T) {
	r := testRestarts()
	r.bindFor = 200 * time.Millisecond
	addr := freePort(t)
	taken, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	start := time.Now()
	err = r.serve(&http.Server{Addr: addr}, false, nil)
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("a port that stays taken must end the start as upstream did, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("gave up after %s, want about 200ms", time.Since(start))
	}
}

func TestRestartLetsRequestsInFlightFinish(t *testing.T) {
	r := testRestarts()
	l := startLoop(t, r)
	l.answering(t)
	old := l.generation.Load()
	slow := make(chan string, 1)
	go func() {
		body, err := askGen(l.main, "/slow")
		if err != nil {
			body = err.Error()
		}
		slow <- body
	}()
	time.Sleep(100 * time.Millisecond)
	// restarts while the old generation drains
	for i := 0; i < 5; i++ {
		r.restart(func() {})
		time.Sleep(20 * time.Millisecond)
	}
	l.answering(t)
	if got := <-slow; got != fmt.Sprint(old) {
		t.Errorf("an ordinary request in flight across the restart: want the answer of generation %d, got %q", old, got)
	}
	l.quiet(t)
}

func TestRestartRequestedFromAHandlerAnswersIt(t *testing.T) {
	r := testRestarts()
	l := startLoopWith(t, r, loopSetup{handler: func(gen int64) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path == "/save" {
				// as the configuration routes do: the restart in the handler, then the answer
				r.restart(func() {})
				time.Sleep(50 * time.Millisecond)
				io.WriteString(w, "saved")
				return
			}
			fmt.Fprintf(w, "%d", gen)
		})
	}})
	l.answering(t)
	for i := 0; i < 20; i++ {
		got, err := askGen(l.main, "/save")
		if err != nil || got != "saved" {
			t.Fatalf("save %d: the answer must reach the client although the server restarts: %q %v", i, got, err)
		}
	}
	l.answering(t)
	l.quiet(t)
}

func TestRestartLeavesAWebSocketOpen(t *testing.T) {
	r := testRestarts()
	l := startLoopWith(t, r, loopSetup{handler: func(gen int64) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path != "/ws" {
				fmt.Fprintf(w, "%d", gen)
				return
			}
			// handed over as a WebSocket is: echo lines
			conn, buf, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			buf.WriteString("HTTP/1.1 101 Switching Protocols\r\n\r\n")
			buf.Flush()
			for {
				line, err := buf.ReadString('\n')
				if err != nil {
					return
				}
				buf.WriteString(line)
				buf.Flush()
			}
		})
	}})
	l.answering(t)
	conn, err := net.Dial("tcp", l.main)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "GET /ws HTTP/1.1\r\nHost: x\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	rd := bufio.NewReader(conn)
	if status, _ := rd.ReadString('\n'); !strings.Contains(status, "101") {
		t.Fatalf("no upgrade: %q", status)
	}
	rd.ReadString('\n')
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10; i++ {
			r.restart(func() {})
			time.Sleep(30 * time.Millisecond)
		}
		close(done)
	}()
	for i := 0; i < 20; i++ {
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		fmt.Fprintf(conn, "ping %d\n", i)
		if got, err := rd.ReadString('\n'); err != nil || got != fmt.Sprintf("ping %d\n", i) {
			t.Fatalf("the WebSocket broke across restarts: %q %v", got, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	<-done
	l.answering(t)
	l.quiet(t)
}

func TestRestartUnexpectedStopEndsAsBefore(t *testing.T) {
	r := testRestarts()
	addr := freePort(t)
	ended := make(chan error, 1)
	broken := &brokenListener{}
	r.listen = func(string) (net.Listener, error) { return broken, nil }
	go func() { ended <- r.serve(&http.Server{Addr: addr}, false, nil) }()
	select {
	case err := <-ended:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			t.Errorf("a listener that fails must end the serving as upstream's Fatal did, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a failing listener left serve hanging")
	}
}

// brokenListener fails every Accept; with block, Accept waits until Close.
type brokenListener struct {
	block chan struct{}
	once  sync.Once
}

func (b *brokenListener) Accept() (net.Conn, error) {
	if b.block != nil {
		<-b.block
		return nil, net.ErrClosed
	}
	return nil, errors.New("accept: broken")
}
func (b *brokenListener) Close() error {
	if b.block != nil {
		b.once.Do(func() { close(b.block) })
	}
	return nil
}
func (*brokenListener) Addr() net.Addr { return &net.TCPAddr{} }

func TestRestartLeaksNoGoroutines(t *testing.T) {
	r := testRestarts()
	l := startLoop(t, r)
	l.answering(t)
	r.restart(func() {})
	l.answering(t)
	time.Sleep(100 * time.Millisecond)
	before := runtime.NumGoroutine()
	for i := 0; i < 300; i++ {
		r.restart(func() {})
		if i%10 == 0 {
			l.answering(t)
		}
	}
	l.answering(t)
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before+2 {
		t.Errorf("goroutines: %d before 300 restarts, %d after", before, after)
	}
	l.quiet(t)
}

// The hooks in upstream's file: the servers are started and stopped by the
// serving loop only, never from RestartHTTPServer or a goroutine of their own.
func TestRestartWiring(t *testing.T) {
	raw, err := os.ReadFile("httpServer.go")
	if err != nil {
		t.Fatal(err)
	}
	server := string(raw)
	between := func(from, to string) string {
		a := strings.Index(server, from)
		b := strings.Index(server[a+1:], to)
		if a < 0 || b < 0 {
			t.Fatalf("%q .. %q not found", from, to)
		}
		return server[a : a+1+b]
	}
	https := between("func startHTTPSServer(", "func tokenMiddleware(")
	plain := between("func startHTTPServer(", "var primaryCert")
	restart := server[strings.Index(server, "func RestartHTTPServer()"):]
	for name, body := range map[string]string{"startHTTPSServer": https, "startHTTPServer": plain, "RestartHTTPServer": restart} {
		for _, banned := range []string{"ListenAndServe", ".Shutdown(", "go (func", "go func"} {
			if strings.Contains(body, banned) {
				t.Errorf("%s must leave listening and stopping to the serving loop: it contains %q", name, banned)
			}
		}
	}
	if !strings.Contains(https, "return forkRestarts.serve(HTTPServer, true, HTTPServer2)") {
		t.Error("startHTTPSServer must serve both listeners as one generation")
	}
	if !strings.Contains(plain, "return forkRestarts.serve(HTTPServer, false, nil)") {
		t.Error("startHTTPServer must serve through the serving loop")
	}
	if strings.Count(https, "ConnState: forkConnState") != 2 || strings.Count(plain, "ConnState: forkConnState") != 1 {
		t.Error("every web server must report its connections to the restarts (ConnState)")
	}
	if !strings.Contains(restart, "forkRestarts.restart(reloadForRestart)") {
		t.Error("RestartHTTPServer must go through forkRestarts")
	}
}

func TestRestartRefusesTwoServersOnOnePort(t *testing.T) {
	r := testRestarts()
	addr := freePort(t)
	err := r.serve(&http.Server{Addr: addr}, false, &http.Server{Addr: addr})
	if err == nil || errors.Is(err, http.ErrServerClosed) || !strings.Contains(err.Error(), "cannot share one port") {
		t.Fatalf("the HTTP and HTTPS ports set to the same one must end the start, saying why: %v", err)
	}
	if len(r.kept) != 0 {
		t.Error("nothing may be bound for a generation that cannot start")
	}
}

func TestRestartRequestedFromInsideTheReloadNeverWaits(t *testing.T) {
	r := testRestarts()
	reloads := 0
	var reload func()
	reload = func() {
		reloads++
		if reloads == 1 {
			r.restart(reload) // a callee of the reload asks for a restart
		}
	}
	done := make(chan struct{})
	go func() { r.restart(reload); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a restart asked for from inside the reload waited for itself")
	}
	if reloads != 2 || r.reentered != 1 {
		t.Errorf("want the reload and one more for the request made inside it: %d reloads, %d inside", reloads, r.reentered)
	}
}

// The old generation's connections after a restart: a request on a
// keep-alive or HTTP/2 connection it accepted reaches the old router only
// until the generation ends; from then on the next router answers it.
func TestRestartOldConnectionsReachTheNewRouterOnly(t *testing.T) {
	for _, overTLS := range []bool{false, true} {
		t.Run(map[bool]string{false: "http keep-alive", true: "https http2"}[overTLS], func(t *testing.T) {
			r := testRestarts()
			l := startLoopWith(t, r, loopSetup{tls: overTLS, build: func(int64) { time.Sleep(50 * time.Millisecond) }})
			l.answering(t)
			scheme := map[bool]string{false: "http", true: "https"}[overTLS]
			// one connection, kept
			c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{ForceAttemptHTTP2: true, MaxConnsPerHost: 1,
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
			get := func() string {
				res, err := c.Get(scheme + "://" + l.main + "/")
				if err != nil {
					return "error: " + err.Error()
				}
				defer res.Body.Close()
				b, _ := io.ReadAll(res.Body)
				return string(b)
			}
			if got := get(); got != "1" {
				t.Fatalf("before: %q", got)
			}
			asked := time.Now()
			r.restart(func() {})
			lastOld := time.Duration(0)
			for i := 0; i < 200; i++ {
				got := get()
				if got == "1" {
					lastOld = time.Since(asked)
				} else if got != "2" {
					t.Fatalf("request %d after the restart: %q", i, got)
				}
				time.Sleep(time.Millisecond)
			}
			t.Logf("the old router answered the old connection until %s after the restart was asked for", lastOld)
			if lastOld > 20*time.Millisecond {
				t.Errorf("an old connection reached the old router %s after the restart was asked for", lastOld)
			}
			l.quiet(t)
		})
	}
}

// A slow router build: connections wait for the next generation for at
// most gapLimit, then they are refused (as upstream) until it serves.
func TestRestartStopsQueueingWhenTheNextGenerationIsSlow(t *testing.T) {
	r := testRestarts()
	r.gapLimit = 300 * time.Millisecond
	slow := make(chan struct{})
	l := startLoopWith(t, r, loopSetup{build: func(gen int64) {
		if gen == 2 {
			<-slow
		}
	}})
	l.answering(t)
	r.restart(func() {})
	// within the limit: the connection waits and is answered by generation 2
	waited := make(chan string, 1)
	go func() {
		got, err := askGen(l.main, "/")
		if err != nil {
			got = err.Error()
		}
		waited <- got
	}()
	time.Sleep(600 * time.Millisecond)
	if _, err := askGen(l.main, "/"); !errors.Is(err, syscall.ECONNREFUSED) {
		t.Errorf("past the limit a new connection must be refused, got %v", err)
	}
	close(slow)
	l.answering(t)
	if got := <-waited; got != "2" && !strings.Contains(got, "reset") && !strings.Contains(got, "EOF") {
		t.Errorf("a connection queued before the limit: %q", got)
	}
	l.quiet(t)
}

// A certificate order while the router is built (a slow ACME client): the
// ports are freed first, so visitors are refused at once, not queued for
// the length of the order.
func TestRestartRefusesDuringASlowOrder(t *testing.T) {
	r := testRestarts()
	ordering := make(chan struct{})
	done := make(chan struct{})
	l := startLoopWith(t, r, loopSetup{build: func(gen int64) {
		if gen == 2 {
			r.release() // what certs' order does first for HTTP-01
			close(ordering)
			<-done
		}
	}})
	l.answering(t)
	r.restart(func() {})
	<-ordering
	start := time.Now()
	_, err := askGen(l.main, "/")
	if !errors.Is(err, syscall.ECONNREFUSED) || time.Since(start) > time.Second {
		t.Errorf("during an order a visitor must be refused at once: %v after %s", err, time.Since(start))
	}
	close(done)
	l.answering(t)
	l.quiet(t)
}

// From HTTP only to HTTPS and back: the plain port's socket goes from the
// main server to the plain one, the HTTPS port is bound and later closed.
func TestRestartSwitchesBetweenHTTPAndHTTPS(t *testing.T) {
	r := testRestarts()
	certPEM, keyPEM := testCertPEM(t, "server.local")
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		t.Fatal(err)
	}
	httpPort, httpsPort := freePort(t), freePort(t)
	modes := []bool{false, true, false}
	ended := make(chan error, 1)
	go func() {
		for gen, overTLS := range modes {
			handler := r.routerOf(generationHandler(int64(gen + 1)))
			var err error
			if overTLS {
				main := &http.Server{Addr: httpsPort, Handler: handler, ConnState: r.connState,
					TLSConfig: &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &cert, nil }}}
				nameHTTP2(main)
				err = r.serve(main, true, &http.Server{Addr: httpPort, Handler: handler, ConnState: r.connState})
			} else {
				err = r.serve(&http.Server{Addr: httpPort, Handler: handler, ConnState: r.connState}, false, nil)
			}
			if !errors.Is(err, http.ErrServerClosed) {
				ended <- err
				return
			}
		}
		ended <- nil
	}()
	want := func(scheme, addr, gen string) {
		t.Helper()
		for i := 0; i < 400; i++ {
			if got, err := askOver(scheme, addr, "/"); err == nil && got == gen {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		got, err := askOver(scheme, addr, "/")
		t.Fatalf("%s://%s: want generation %s, got %q %v", scheme, addr, gen, got, err)
	}
	want("http", httpPort, "1")
	r.restart(func() {})
	want("https", httpsPort, "2")
	want("http", httpPort, "2")
	r.restart(func() {})
	want("http", httpPort, "3")
	if _, err := askOver("https", httpsPort, "/"); !errors.Is(err, syscall.ECONNREFUSED) {
		t.Errorf("back to HTTP only, the HTTPS port must be closed: %v", err)
	}
	r.restart(func() {})
	if err := <-ended; err != nil {
		t.Fatal(err)
	}
}
