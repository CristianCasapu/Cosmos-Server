package proxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

// bareMetal makes the package behave as on a server where Cosmos runs outside Docker.
func bareMetal(t *testing.T) {
	prevInside, prevHost := utils.IsInsideContainer, utils.IsHostNetwork
	utils.IsInsideContainer, utils.IsHostNetwork = false, false
	t.Cleanup(func() { utils.IsInsideContainer, utils.IsHostNetwork = prevInside, prevHost })
}

func stubTarget(t *testing.T, answer func(name string) (string, docker.TargetState, error)) *int32 {
	var asked int32
	prev := dockerContainerTarget
	dockerContainerTarget = func(name string) (string, docker.TargetState, error) {
		atomic.AddInt32(&asked, 1)
		return answer(name)
	}
	t.Cleanup(func() { dockerContainerTarget = prev })
	return &asked
}

func countLogs(t *testing.T) *[]string {
	lines := []string{}
	prev := logUnreachable
	logUnreachable = func(text string) { lines = append(lines, text) }
	notesMu.Lock()
	notes = map[string]*noteState{}
	notesMu.Unlock()
	reaskMu.Lock()
	reaskAt = map[string]time.Time{}
	reaskMu.Unlock()
	t.Cleanup(func() { logUnreachable = prev })
	return &lines
}

// the test's own listeners count as "Cosmos' ports" only where a test says so
func stubOwnPorts(t *testing.T, ports ...string) {
	prev := ownPorts
	ownPorts = func() []string { return ports }
	t.Cleanup(func() { ownPorts = prev })
}

func details(t *testing.T, on bool) {
	prev := mayKnowDetails
	mayKnowDetails = func(*http.Request) bool { return on }
	t.Cleanup(func() { mayKnowDetails = prev })
}

// A URL whose container is restarting answers 503 at once, and the proxy
// behind the guard is never reached (it would have dialed ":<port>").
func TestGuardAnswersForAContainerThatIsDown(t *testing.T) {
	bareMetal(t)
	details(t, true)
	logs := countLogs(t)
	for _, state := range []docker.TargetState{docker.TargetRestarting, docker.TargetStopped, docker.TargetNoAddress, docker.TargetUnknown} {
		stubTarget(t, func(string) (string, docker.TargetState, error) { return "", state, docker.ErrTargetDown })
		var reached int32
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&reached, 1) })
		h := forkTargetGuard(utils.ProxyRouteConfig{Name: "pg-" + string(state), Mode: "SERVAPP", Target: "http://postgresql:"})(next)
		req := httptest.NewRequest("GET", "http://postgresql.local/", nil)
		req.Header.Set("Accept", "text/html")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable || reached != 0 || rec.Header().Get("Retry-After") == "" {
			t.Fatalf("%s: status %d, proxy reached %d times", state, rec.Code, reached)
		}
		if !strings.Contains(rec.Body.String(), "<code>postgresql</code>") {
			t.Errorf("%s: an administrator reads which app: %q", state, rec.Body.String())
		}
	}
	if len(*logs) != 4 {
		t.Errorf("one line per URL: %v", *logs)
	}
}

// A stranger reads neither the container's name nor its state (review A7).
func TestAStrangerSeesNoDetails(t *testing.T) {
	bareMetal(t)
	countLogs(t)
	details(t, false)
	stubTarget(t, func(string) (string, docker.TargetState, error) {
		return "", docker.TargetRestarting, docker.ErrTargetDown
	})
	h := forkTargetGuard(utils.ProxyRouteConfig{Name: "secret-db-url", Mode: "SERVAPP", Target: "http://internal-billing-db:5432"})(http.NotFoundHandler())
	for _, accept := range []string{"text/html", "*/*"} {
		req := httptest.NewRequest("GET", "http://x.example.org/", nil)
		req.Header.Set("Accept", accept)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		body := rec.Body.String()
		if rec.Code != 503 || strings.Contains(body, "internal-billing-db") || strings.Contains(body, "secret-db-url") || strings.Contains(strings.ToLower(body), "restart") || !strings.Contains(body, "not available") {
			t.Fatalf("%s: %d %q %v", accept, rec.Code, body, rec.Header())
		}
	}
}

// A thousand requests to a stopped app: one log line.
func TestGuardLogsOncePerURLAndMinute(t *testing.T) {
	bareMetal(t)
	logs := countLogs(t)
	stubTarget(t, func(string) (string, docker.TargetState, error) {
		return "", docker.TargetRestarting, docker.ErrTargetDown
	})
	h := forkTargetGuard(utils.ProxyRouteConfig{Name: "pg", Mode: "SERVAPP", Target: "http://postgresql:5432"})(http.NotFoundHandler())
	for i := 0; i < 1000; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://pg.local/", nil))
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "restarting") {
		t.Fatalf("lines %v", *logs)
	}
}

// Without an address the Director gives the transport no host at all, which
// fails without dialing (upstream gave ":8080": this server itself).
func TestDirectorNeverDialsAnEmptyAddress(t *testing.T) {
	bareMetal(t)
	stubTarget(t, func(string) (string, docker.TargetState, error) {
		return "", docker.TargetRestarting, docker.ErrTargetDown
	})
	proxy, err := NewProxy("http://web:8080", false, false, utils.ProxyRouteConfig{Name: "web", Mode: "SERVAPP", Target: "http://web:8080"})
	if err != nil {
		t.Fatal(err)
	}
	out := httptest.NewRequest("GET", "http://web.local/", nil)
	proxy.Director(out)
	if out.URL.Host != "" {
		t.Fatalf("the Director dials %q", out.URL.Host)
	}
}

// listener answers "ok" and counts its requests.
func listener(t *testing.T) (*httptest.Server, *int32) {
	var hits int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write([]byte("ok"))
	}))
	t.Cleanup(s.Close)
	return s, &hits
}

// Review A3: a SERVAPP URL whose target is not a container keeps working,
// dialled as written (localhost:port, an address); a name that resolves
// nowhere is a plain 502, logged once.
func TestServappTargetsThatAreNotContainers(t *testing.T) {
	bareMetal(t)
	logs := countLogs(t)
	stubOwnPorts(t, "1") // not the listener's port
	stubTarget(t, func(name string) (string, docker.TargetState, error) {
		return "", docker.TargetMissing, docker.ErrTargetDown
	})
	app, hits := listener(t)
	port := app.URL[strings.LastIndex(app.URL, ":")+1:]
	for _, target := range []string{"http://localhost:" + port, "http://127.0.0.1:" + port} {
		route := utils.ProxyRouteConfig{Name: "nc", Mode: "SERVAPP", Target: target}
		rec := httptest.NewRecorder()
		routeHandlerForTarget(target, route).ServeHTTP(rec, httptest.NewRequest("GET", "http://nc.local/", nil))
		if rec.Code != 200 || rec.Body.String() != "ok" {
			t.Errorf("%s: %d %q", target, rec.Code, rec.Body.String())
		}
	}
	if atomic.LoadInt32(hits) != 2 {
		t.Fatalf("reached %d times", *hits)
	}
	route := utils.ProxyRouteConfig{Name: "nowhere", Mode: "SERVAPP", Target: "http://name-that-resolves-nowhere.invalid:80"}
	h := routeHandlerForTarget(route.Target, route)
	for i := 0; i < 20; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "http://nowhere.local/", nil))
		if rec.Code != 502 {
			t.Fatalf("a name that resolves nowhere: %d", rec.Code)
		}
	}
	n := 0
	for _, l := range *logs {
		if strings.Contains(l, "URL nowhere") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("logged %d times: %v", n, *logs)
	}
}

// The dial refuses Cosmos itself, whatever the name used for it (loopback,
// unspecified, empty, one of the server's own addresses); another port of
// the same server is fine.
func TestTheDialRefusesCosmosItself(t *testing.T) {
	countLogs(t)
	app, hits := listener(t)
	port := app.URL[strings.LastIndex(app.URL, ":")+1:]
	stubOwnPorts(t, port)
	prevAddrs := interfaceAddrs
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("192.0.2.44"), Mask: net.CIDRMask(24, 32)}}, nil
	}
	ownAddrMu.Lock()
	ownAddrAt = time.Time{}
	ownAddrMu.Unlock()
	t.Cleanup(func() {
		interfaceAddrs = prevAddrs
		ownAddrMu.Lock()
		ownAddrAt = time.Time{}
		ownAddrMu.Unlock()
	})
	dial := forkGuardDial(&net.Dialer{Timeout: time.Second})
	for _, address := range []string{"127.0.0.1:" + port, "localhost:" + port, "0.0.0.0:" + port, ":" + port, "192.0.2.44:" + port} {
		if _, err := dial(context.Background(), "tcp", address); !errors.Is(err, errDialsCosmos) {
			t.Errorf("%s: %v", address, err)
		}
	}
	stubOwnPorts(t, "1")
	conn, err := dial(context.Background(), "tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatalf("another port of this server: %v", err)
	}
	conn.Close()
	// a PROXY URL to Cosmos' own port, as a whole request: 508, nothing reached
	stubOwnPorts(t, port)
	before := atomic.LoadInt32(hits)
	route := utils.ProxyRouteConfig{Name: "self", Mode: "PROXY", Target: app.URL}
	rec := httptest.NewRecorder()
	routeHandlerForTarget(app.URL, route).ServeHTTP(rec, httptest.NewRequest("GET", "http://self.local/", nil))
	if rec.Code != http.StatusLoopDetected || atomic.LoadInt32(hits) != before {
		t.Fatalf("status %d, reached %d", rec.Code, atomic.LoadInt32(hits)-before)
	}
}

// The owner's storm: the Cosmos port is a real listener that serves the same
// URL; one request is answered at once, nothing comes back.
func TestALoopThroughCosmosIsNeverStarted(t *testing.T) {
	countLogs(t)
	var hits int32
	var handler http.Handler
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	stubOwnPorts(t, server.URL[strings.LastIndex(server.URL, ":")+1:])
	route := utils.ProxyRouteConfig{Name: "self", Mode: "PROXY", Target: server.URL}
	handler = routeHandlerForTarget(server.URL, route)
	client := &http.Client{Timeout: 10 * time.Second} // without the guard this never ends
	res, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusLoopDetected || atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("status %d after %d requests", res.StatusCode, hits)
	}
}

// Review A6: an app that calls its own URL back (another path) through
// Cosmos is not a loop, and nothing of Cosmos' marks its requests.
func TestAnAppCallingItsOwnURLIsNotALoop(t *testing.T) {
	countLogs(t)
	stubOwnPorts(t, "1")
	var front http.Handler
	frontServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { front.ServeHTTP(w, r) }))
	defer frontServer.Close()
	seen := []string{}
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name := range r.Header {
			if strings.Contains(strings.ToLower(name), "hop") {
				seen = append(seen, name)
			}
		}
		if r.URL.Path == "/page" {
			res, err := http.Get(frontServer.URL + "/api")
			if err != nil || res.StatusCode != 200 {
				w.WriteHeader(500)
				return
			}
			res.Body.Close()
		}
		w.Write([]byte("ok"))
	}))
	defer app.Close()
	route := utils.ProxyRouteConfig{Name: "app", Mode: "PROXY", Target: app.URL}
	front = routeHandlerForTarget(app.URL, route)
	res, err := http.Get(frontServer.URL + "/page")
	if err != nil || res.StatusCode != 200 || len(seen) != 0 {
		t.Fatalf("%v %v headers %v", err, res, seen)
	}
}

// Review A1: a running container whose port refuses connections: Docker is
// asked again at most once per container every reaskEvery, not per request.
func TestFailedDialsDoNotHammerDocker(t *testing.T) {
	bareMetal(t)
	countLogs(t)
	stubOwnPorts(t, "2")
	forgot := 0
	prevForget := forgetTarget
	forgetTarget = func(string) { forgot++ }
	t.Cleanup(func() { forgetTarget = prevForget })
	asked := stubTarget(t, func(string) (string, docker.TargetState, error) { return "127.0.0.1", docker.TargetRunning, nil })
	route := utils.ProxyRouteConfig{Name: "refusing", Mode: "SERVAPP", Target: "http://refusing:1"}
	h := routeHandlerForTarget(route.Target, route)
	for i := 0; i < 100; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "http://r.local/", nil))
		if rec.Code != 502 {
			t.Fatalf("status %d", rec.Code)
		}
	}
	// 100 by the guard (answered by Docker's own 10 s cache in real life, stubbed here), 1 more after the first failure
	if *asked != 101 || forgot != 1 {
		t.Fatalf("asked %d, forgot %d", *asked, forgot)
	}
}

// An address cached while the app ran a moment: the dial fails, Docker is
// asked again, and the visitor gets the page, not a bare 502.
func TestAProxyErrorAsksDockerAgain(t *testing.T) {
	bareMetal(t)
	countLogs(t)
	stubOwnPorts(t, "2")
	prevForget := forgetTarget
	forgetTarget = func(string) {}
	t.Cleanup(func() { forgetTarget = prevForget })
	asked := 0
	stubTarget(t, func(string) (string, docker.TargetState, error) {
		asked++
		if asked%2 == 1 {
			return "127.0.0.1", docker.TargetRunning, nil
		}
		return "", docker.TargetRestarting, docker.ErrTargetDown
	})
	route := utils.ProxyRouteConfig{Name: "pg", Mode: "SERVAPP", Target: "http://crashing:1"}
	rec := httptest.NewRecorder()
	routeHandlerForTarget(route.Target, route).ServeHTTP(rec, httptest.NewRequest("GET", "http://pg.local/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rec.Code)
	}
}

// Review A2: a route's own timeout is not "the visitor left": the answer is
// upstream's 502; the visitor who left is told as such in the log.
func TestTimeoutIsNotAVisitorWhoLeft(t *testing.T) {
	logs := countLogs(t)
	target, _ := url.Parse("http://192.0.2.1:80")
	handler := forkProxyError(utils.ProxyRouteConfig{Name: "slow", Mode: "PROXY", Target: target.String()}, target)
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	<-ctx.Done()
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest("GET", "/", nil).WithContext(ctx), context.DeadlineExceeded)
	if rec.Code != http.StatusBadGateway || strings.Contains(strings.Join(*logs, ""), "visitor left") {
		t.Fatalf("%d %v", rec.Code, *logs)
	}
	gone, stop := context.WithCancel(context.Background())
	stop()
	rec = httptest.NewRecorder()
	handler(rec, httptest.NewRequest("GET", "/", nil).WithContext(gone), context.Canceled)
	if !strings.Contains(strings.Join(*logs, ""), "visitor left") {
		t.Fatalf("%v", *logs)
	}
}

// The page is plain: a hostile URL name is escaped.
func TestUnreachablePageEscapes(t *testing.T) {
	details(t, true)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept", "text/html")
	writeUnreachable(rec, req, 508, "loop", `<script>x</script>`)
	if strings.Contains(rec.Body.String(), "<script>") {
		t.Fatal(rec.Body.String())
	}
}
