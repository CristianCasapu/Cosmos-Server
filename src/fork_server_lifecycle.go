package main

// Fork addition (plan 46): the restarts of the web server, one at a time.
//
// Upstream's RestartHTTPServer reloaded the configuration and then shut the
// package's servers down from a goroutine, without a lock, while the loop of
// StartServer built the next ones and started the plain-HTTP one from a
// goroutine of its own. Two restarts close together could therefore shut
// down a server that was not listening yet (its ListenAndServe then
// returned at once: the plain-HTTP listener stayed dead until the next
// restart), shut down the old servers again while the new router was built
// from a configuration older than the last save (the change was not live),
// or leave a stray plain-HTTP server of an older round bound to the port so
// that the new one ended the process.
//
// Here a restart has two parts:
//
//   - the reload (upstream's RestartHTTPServer without the shutdown) runs
//     in the caller, as before, so that the configuration in memory is the
//     new one when RestartHTTPServer returns. Reloads run one at a time,
//     and one reload covers every request made before it began: callers
//     that arrive while one runs wait for the next, which then serves all
//     of them.
//   - then a new generation of the servers is asked for, without waiting.
//     The serving loop (StartServer -> startHTTP(S)Server -> serve) is the
//     only one that starts or stops servers. When a restart was asked for,
//     it ends exactly the servers of the generation that serves (each
//     Shutdown in a goroutine of its own: their requests finish in the
//     background, as upstream's did), waits until they no longer take
//     connections, and returns, so that StartServer builds the router from
//     the configuration as it is then. Requests made before that point are
//     covered by the new router; requests made later are remembered (one is
//     enough) and give one more generation.
//   - the listening sockets are not closed between two generations: each
//     generation accepts on them through a listener of its own whose Close
//     only stops its accepting. Connections that arrive while the next
//     router is built wait in the accept queue and are served by the new
//     generation, instead of being refused as they were upstream. A socket
//     is closed when no server uses its address any more (ports changed),
//     and between two generations when a certificate order needs the ports
//     for its own challenge servers (utils.ForkFreeServerPorts); it is
//     bound again, retrying for a while if the port is still held, instead
//     of ending the process at once. Queueing is bounded: when the next
//     generation does not serve within gapLimit of the last one's end (a
//     slow router build), the sockets are closed and visitors are refused,
//     as they were upstream, until it does.
//   - the router of a generation is served through a gate (routerOf): once
//     the generation has ended, a request that still reaches it (a stream
//     of an HTTP/2 connection it accepted, the first request of a
//     connection it took in its last instant) is served by the next
//     generation's router, not by the old one. Only requests already being
//     handled, and WebSockets already handed over, finish on the old router.
//
// RestartHTTPServer never waits for the servers: it is called from request
// handlers whose own request the old server's Shutdown waits for.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

type restarts struct {
	mu        sync.Mutex
	done      *sync.Cond
	asked     uint64 // reloads asked for
	covered   uint64 // every one up to this one is covered by a finished reload
	reloading bool

	// a new generation of the servers is wanted; one is enough for any
	// number of requests
	wanted chan struct{}

	// how long a listener whose port is still held is tried again
	bindFor   time.Duration
	bindEvery time.Duration
	listen    func(addr string) (net.Listener, error)
	log       func(string)

	// the listening sockets, kept from one generation to the next
	keptMu  sync.Mutex
	kept    map[string]*keptSocket
	serving bool

	// connections taken whose first request has not been read yet (the
	// servers' ConnState), and how long an ending generation waits for them
	freshMu   sync.Mutex
	fresh     map[net.Conn]chan struct{}
	firstWait time.Duration

	// between two generations: whether one is being bound, and the timer
	// that stops the queueing after gapLimit
	starting bool
	gapTimer *time.Timer
	gapLimit time.Duration

	// the routers: the one being built, the one serving, and a channel
	// closed whenever another serves
	gateMu   sync.Mutex
	building *generationRouter
	current  *generationRouter
	changed  chan struct{}

	// a restart asked for from inside the reload: one more reload follows
	again      bool
	reentered  int
	loopLogged bool
}

// maxReloadsAskedFromInside bounds the reloads that requests made from
// inside a reload add to one restart.
const maxReloadsAskedFromInside = 3

func newRestarts() *restarts {
	r := &restarts{
		wanted:    make(chan struct{}, 1),
		bindFor:   10 * time.Second,
		bindEvery: 50 * time.Millisecond,
		listen:    func(addr string) (net.Listener, error) { return net.Listen("tcp", setupListenAddr(addr)) }, // the wizard on 127.0.0.1 only when install.sh asked for it, see fork_setup_code.go
		log:       utils.Log,
		kept:      map[string]*keptSocket{},
		fresh:     map[net.Conn]chan struct{}{},
		firstWait: time.Second,
		gapLimit:  5 * time.Second,
		changed:   make(chan struct{}),
	}
	r.done = sync.NewCond(&r.mu)
	return r
}

var forkRestarts = newRestarts()

func init() {
	utils.ForkFreeServerPorts = forkRestarts.release
}

// forkConnState is the ConnState of the web servers (hook in
// startHTTP(S)Server): it follows the connections that have not sent their
// first request yet.
func forkConnState(c net.Conn, state http.ConnState) { forkRestarts.connState(c, state) }

func (r *restarts) connState(c net.Conn, state http.ConnState) {
	r.freshMu.Lock()
	defer r.freshMu.Unlock()
	if state == http.StateNew {
		r.fresh[c] = make(chan struct{})
		return
	}
	if first, ok := r.fresh[c]; ok {
		close(first)
		delete(r.fresh, c)
	}
}

// waitForFirstRequests waits, for firstWait at most, until every connection
// taken so far has sent its first request (or closed): that request is then
// answered (by the next router, through the gate). Shutdown alone does not
// do it: it leaves a new connection 5 s before closing it as idle, but a
// connection that reads its first request after Shutdown began is closed
// unanswered (net/http conn.serve: "if c.server.shuttingDown() return"
// right after readRequest, Go 1.27). Without the wait about one connection
// per restart under load was closed so (TestRestartRefusesNoConnection).
// A connection that sends nothing costs the restart firstWait, no more;
// usually there is none and the wait is nil.
func (r *restarts) waitForFirstRequests() time.Duration {
	start := time.Now()
	r.freshMu.Lock()
	waiting := make([]chan struct{}, 0, len(r.fresh))
	for _, first := range r.fresh {
		waiting = append(waiting, first)
	}
	r.freshMu.Unlock()
	deadline := time.NewTimer(r.firstWait)
	defer deadline.Stop()
	for _, first := range waiting {
		select {
		case <-first:
		case <-deadline.C:
			return time.Since(start)
		}
	}
	if len(waiting) > 0 {
		// net/http marks the connection active just before it checks
		// whether the server is shutting down: give its goroutine the
		// moment it needs to pass that check
		time.Sleep(firstRequestGrace)
	}
	return time.Since(start)
}

// firstRequestGrace: see waitForFirstRequests.
const firstRequestGrace = 10 * time.Millisecond

// restart runs reload unless a reload that began after this call has
// finished meanwhile, then asks for a new generation of the servers. A panic
// of reload goes to the caller, as it did upstream; the next caller reloads.
// Called from inside the reload itself (a callee of reloadForRestart), it
// only records the request: one more reload follows this one, nothing waits.
func (r *restarts) restart(reload func()) {
	r.mu.Lock()
	r.asked++
	mine := r.asked
	extra := 0
	if r.reloading && insideReload() {
		r.again = true
		r.reentered++
		r.mu.Unlock()
		r.log("A restart was asked for from inside the reload: one more reload follows it")
		return
	}
	for r.covered < mine {
		if r.reloading {
			r.done.Wait()
			continue
		}
		r.reloading = true
		covers := r.asked
		r.mu.Unlock()
		r.reloadOnce(reload, covers)
		r.mu.Lock()
		if r.again {
			r.again = false
			if extra < maxReloadsAskedFromInside {
				extra++
				mine = r.asked // what was asked for from inside: covered by the next one
			} else if !r.loopLogged {
				// a callee asks for a restart on every reload: stop here
				// rather than reload for ever (the caller would never return)
				r.loopLogged = true
				r.log("The reload keeps asking for a restart from inside itself: stopped after " + strconv.Itoa(maxReloadsAskedFromInside) + " more reloads")
			}
		}
	}
	r.mu.Unlock()
	r.ask()
}

// insideReload reports whether the calling goroutine runs a reload
// (reloadOnce is on its stack).
func insideReload() bool {
	pcs := make([]uintptr, 512)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, ".(*restarts).reloadOnce") {
			return true
		}
		if !more {
			return false
		}
	}
}

// reloadOnce runs reload for the requests up to covers; whatever happens,
// the next caller is let through.
func (r *restarts) reloadOnce(reload func(), covers uint64) {
	finished := false
	defer func() {
		r.mu.Lock()
		r.reloading = false
		if finished && covers > r.covered {
			r.covered = covers
		}
		r.done.Broadcast()
		r.mu.Unlock()
	}()
	reload()
	finished = true
}

// ask wants a new generation of the servers, without waiting for it.
func (r *restarts) ask() {
	select {
	case r.wanted <- struct{}{}:
	default: // one is already wanted: it will see this configuration too
	}
}

// serve serves one generation: main (over TLS when mainTLS) and plain when
// it is not nil, on the kept sockets of their addresses (bound when there
// are none), until a restart is asked for. It returns http.ErrServerClosed
// once both have stopped taking connections. Any other error ends the
// process in the caller, as upstream's did.
func (r *restarts) serve(main *http.Server, mainTLS bool, plain *http.Server) error {
	servers := []*http.Server{main}
	if plain != nil {
		servers = append(servers, plain)
	}
	listeners, err := r.sockets(servers)
	if err != nil {
		if errors.Is(err, errPlainBind) {
			return errors.New("Listening to HTTP (Redirecting to HTTPS): " + err.Error())
		}
		return err
	}
	router := r.publish()

	stopped := make(chan error, len(servers))
	for i, srv := range servers {
		go func(srv *http.Server, ln net.Listener, overTLS bool) {
			if overTLS {
				stopped <- srv.ServeTLS(ln, "", "")
			} else {
				stopped <- srv.Serve(ln)
			}
		}(srv, listeners[i], srv == main && mainTLS)
	}

	running := len(servers)
	var failed error
	select {
	case <-r.wanted:
	case err := <-stopped:
		// not asked for: upstream ended the process unless it was a Shutdown
		running--
		if !errors.Is(err, http.ErrServerClosed) {
			failed = err
		}
	}

	// The generation ends. First it stops taking connections (the sockets
	// stay open: what arrives from now on waits for the next generation)
	// and keeps no connection open after its current request; the
	// connections it took in its last instant are given the time to send
	// their first request (Go's Shutdown would close them unanswered);
	// then its requests finish in the background, as long as they take
	// (upstream's Shutdown without a deadline; a WebSocket is not waited
	// for: it was handed over and goes on).
	for _, ln := range listeners {
		ln.Close()
	}
	for ; running > 0; running-- {
		if err := <-stopped; failed == nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
			failed = err
		}
	}
	if router != nil {
		close(router.ended) // from now on its requests go to the next router
	}
	for _, srv := range servers {
		srv.SetKeepAlivesEnabled(false)
	}
	waited := r.waitForFirstRequests()
	for _, srv := range servers {
		go srv.Shutdown(context.Background())
	}
	r.keptMu.Lock()
	r.serving = false
	r.gapTimer = time.AfterFunc(r.gapLimit, r.release) // a slow build: refused, not queued, from then on
	r.keptMu.Unlock()
	if failed != nil {
		r.release()
		return failed
	}

	// the router about to be built reads the configuration as it is now:
	// what was asked for until here is in it
	select {
	case <-r.wanted:
	default:
	}
	r.log("HTTPServer shutdown. Waited " + waited.Round(time.Microsecond).String() + " for the first requests of new connections")
	return http.ErrServerClosed
}

var errPlainBind = errors.New("plain")

// sockets returns a listener of this generation for each server: a view of
// the socket kept for its address, bound first when there is none. Sockets
// of addresses no server uses any more (ports changed) are closed.
func (r *restarts) sockets(servers []*http.Server) ([]net.Listener, error) {
	for i := range servers {
		for _, other := range servers[:i] {
			if other.Addr == servers[i].Addr {
				// upstream's second listener failed to bind: same end, said plainly
				return nil, fmt.Errorf("the HTTP and the HTTPS server are both set to listen on %s: they cannot share one port", servers[i].Addr)
			}
		}
	}
	r.keptMu.Lock()
	defer r.keptMu.Unlock()
	if r.gapTimer != nil {
		r.gapTimer.Stop()
	}
	r.starting = true
	defer func() { r.starting = false }()
	used := map[string]bool{}
	listeners := []net.Listener{}
	for i, srv := range servers {
		used[srv.Addr] = true
		k := r.kept[srv.Addr]
		if k == nil {
			r.keptMu.Unlock()
			ln, err := r.bind(srv.Addr)
			r.keptMu.Lock()
			if err != nil {
				if i > 0 {
					err = fmt.Errorf("%w: %v", errPlainBind, err)
				}
				return nil, err
			}
			k = &keptSocket{ln: ln, spill: make(chan net.Conn, 64)}
			r.kept[srv.Addr] = k
		}
		if d, ok := k.ln.(interface{ SetDeadline(time.Time) error }); ok {
			d.SetDeadline(time.Time{}) // the last generation's end
		}
		listeners = append(listeners, &generationListener{k: k, done: make(chan struct{})})
	}
	for addr, k := range r.kept {
		if !used[addr] {
			k.close()
			delete(r.kept, addr)
		}
	}
	r.serving = true
	return listeners, nil
}

// release closes the kept sockets between two generations: so that
// something else can take the ports for a while (lego's own challenge
// servers during a certificate order made while the router is built:
// utils.ForkFreeServerPorts), and when the next generation is slow to come
// (gapLimit). While a generation serves or binds it does nothing (the ports
// are taken then, as they were upstream). The next generation binds again.
func (r *restarts) release() {
	r.keptMu.Lock()
	defer r.keptMu.Unlock()
	if r.serving || r.starting {
		return
	}
	for addr, k := range r.kept {
		k.close()
		delete(r.kept, addr)
	}
}

// bind listens on addr, trying again for a while when the port is still
// held (by a certificate challenge, the process before, a port check).
func (r *restarts) bind(addr string) (net.Listener, error) {
	deadline := time.Now().Add(r.bindFor)
	for {
		ln, err := r.listen(addr)
		if err == nil || time.Now().After(deadline) {
			return ln, err
		}
		time.Sleep(r.bindEvery)
	}
}

// keptSocket is a listening socket that outlives the generations of the
// servers. spill holds what was accepted in the instant a generation ended,
// for the next one.
type keptSocket struct {
	ln    net.Listener
	spill chan net.Conn
}

func (k *keptSocket) close() {
	k.ln.Close()
	for {
		select {
		case c := <-k.spill:
			c.Close()
		default:
			return
		}
	}
}

// generationListener is one generation's listener on a kept socket: closing
// it stops this generation's Accept (a deadline on the socket) and leaves
// the socket open.
type generationListener struct {
	k    *keptSocket
	done chan struct{}
	once sync.Once
}

func (g *generationListener) Accept() (net.Conn, error) {
	for {
		select {
		case <-g.done:
			return nil, net.ErrClosed
		case c := <-g.k.spill:
			return c, nil
		default:
		}
		c, err := g.k.ln.Accept()
		select {
		case <-g.done:
			if err == nil {
				select {
				case g.k.spill <- c: // for the next generation
				default:
					c.Close()
				}
			}
			return nil, net.ErrClosed
		default:
		}
		if err != nil {
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				// Close set the deadline and is about to say it is done
				time.Sleep(time.Millisecond)
				continue
			}
			return nil, err
		}
		return c, nil
	}
}

func (g *generationListener) Close() error {
	g.once.Do(func() {
		// the deadline first: once done is closed, Serve may return and the
		// next generation clear the deadline, which must then stay cleared
		if d, ok := g.k.ln.(interface{ SetDeadline(time.Time) error }); ok {
			d.SetDeadline(time.Now())
		}
		close(g.done)
	})
	return nil
}

func (g *generationListener) Addr() net.Addr { return g.k.ln.Addr() }

// generationRouter is a generation's router behind its gate (see the top).
type generationRouter struct {
	r       *restarts
	handler http.Handler
	ended   chan struct{}
}

// routerOf is the hook in startHTTP(S)Server: the router of the generation
// being started, behind its gate.
func (r *restarts) routerOf(handler http.Handler) http.Handler {
	g := &generationRouter{r: r, handler: handler, ended: make(chan struct{})}
	r.gateMu.Lock()
	r.building = g
	r.gateMu.Unlock()
	return g
}

// publish makes the router being built the one that serves.
func (r *restarts) publish() *generationRouter {
	r.gateMu.Lock()
	defer r.gateMu.Unlock()
	g := r.building
	r.building = nil
	if g != nil {
		r.current = g
		close(r.changed)
		r.changed = make(chan struct{})
	}
	return g
}

func (g *generationRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	select {
	case <-g.ended:
	default:
		g.handler.ServeHTTP(w, req)
		return
	}
	deadline := time.NewTimer(g.r.gapLimit)
	defer deadline.Stop()
	for {
		g.r.gateMu.Lock()
		next, changed := g.r.current, g.r.changed
		g.r.gateMu.Unlock()
		if next != g {
			next.ServeHTTP(w, req)
			return
		}
		select {
		case <-changed:
		case <-deadline.C:
			// the old router never answers once its generation ended
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Retry-After", "5")
			http.Error(w, "The server is restarting, try again in a moment", http.StatusServiceUnavailable)
			return
		case <-req.Context().Done():
			return
		}
	}
}
