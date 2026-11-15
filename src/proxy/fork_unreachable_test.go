package proxy

import (
	"net/http"
	"net/http/httptest"
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
	t.Cleanup(func() { logUnreachable = prev })
	return &lines
}

// A URL whose container is restarting answers 503 at once, in words, and the
// proxy behind the guard is never reached (it would have dialed ":<port>").
func TestGuardAnswersForAContainerThatIsDown(t *testing.T) {
	bareMetal(t)
	logs := countLogs(t)
	for _, state := range []docker.TargetState{docker.TargetRestarting, docker.TargetStopped, docker.TargetMissing, docker.TargetNoAddress} {
		stubTarget(t, func(string) (string, docker.TargetState, error) { return "", state, docker.ErrTargetDown })
		var reached int32
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&reached, 1) })
		route := utils.ProxyRouteConfig{Name: "pg-" + string(state), Mode: "SERVAPP", Target: "http://postgresql:"}
		h := forkTargetGuard(route)(next)

		req := httptest.NewRequest("GET", "http://postgresql.local/", nil)
		req.Header.Set("Accept", "text/html")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable || reached != 0 {
			t.Fatalf("%s: status %d, proxy reached %d times", state, rec.Code, reached)
		}
		if rec.Header().Get(ProbeHeader) != string(state) || rec.Header().Get("Retry-After") == "" {
			t.Errorf("%s: headers %v", state, rec.Header())
		}
		if !strings.Contains(rec.Body.String(), "<code>postgresql</code>") {
			t.Errorf("%s: page %q", state, rec.Body.String())
		}
	}
	if len(*logs) != 4 {
		t.Errorf("one line per URL: %v", *logs)
	}
}

// A thousand requests to a stopped app: one log line, the next one says how many.
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
	notesMu.Lock()
	n := notes["pg\x00restarting"].count
	notesMu.Unlock()
	if n != 999 {
		t.Errorf("counted %d", n)
	}
}

// A running container: the request goes on to the proxy with the address
// resolved, and carries this URL's marker.
func TestGuardPassesTheAddressToTheDirector(t *testing.T) {
	bareMetal(t)
	asked := stubTarget(t, func(name string) (string, docker.TargetState, error) {
		if name != "web" {
			t.Errorf("asked for %q", name)
		}
		return "192.0.2.7", docker.TargetRunning, nil
	})
	var host, hop string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host = forkDirectorHost(r, "web", "8080")
		hop = r.Header.Get(HopHeader)
	})
	route := utils.ProxyRouteConfig{Name: "web", Mode: "SERVAPP", Target: "http://web:8080"}
	forkTargetGuard(route)(next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://web.local/", nil))
	if host != "192.0.2.7:8080" || hop != hopMarker("web", "http://web:8080") || *asked != 1 {
		t.Fatalf("host %q hop %q asked %d", host, hop, *asked)
	}
}

// Without an address the Director gives the transport no host at all, which
// fails without dialing (upstream gave ":8080": this server itself).
func TestDirectorNeverDialsAnEmptyAddress(t *testing.T) {
	bareMetal(t)
	stubTarget(t, func(string) (string, docker.TargetState, error) {
		return "", docker.TargetRestarting, docker.ErrTargetDown
	})
	if got := forkDirectorHost(httptest.NewRequest("GET", "/", nil), "web", "8080"); got != "" {
		t.Fatalf("host %q", got)
	}
	proxy, err := NewProxy("http://web:8080", false, false, utils.ProxyRouteConfig{Name: "web", Mode: "SERVAPP", Target: "http://web:8080"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "http://web.local/", nil)
	out := req.Clone(req.Context())
	proxy.Director(out)
	if out.URL.Host != "" {
		t.Fatalf("the Director dials %q", out.URL.Host)
	}
}

// A host-network container on Cosmos' own port would be Cosmos itself.
func TestGuardRefusesCosmosOwnPort(t *testing.T) {
	bareMetal(t)
	countLogs(t)
	prev := ownPorts
	ownPorts = func() []string { return []string{"2052", "2053"} }
	t.Cleanup(func() { ownPorts = prev })
	stubTarget(t, func(string) (string, docker.TargetState, error) { return "127.0.0.1", docker.TargetRunning, nil })
	var reached int32
	h := forkTargetGuard(utils.ProxyRouteConfig{Name: "x", Mode: "SERVAPP", Target: "http://hostapp:2052"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached++ }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://x.local/", nil))
	if rec.Code != http.StatusLoopDetected || reached != 0 {
		t.Fatalf("status %d reached %d", rec.Code, reached)
	}
	// another port of a host-network app is fine
	h = forkTargetGuard(utils.ProxyRouteConfig{Name: "y", Mode: "SERVAPP", Target: "http://hostapp:8096"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached++ }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://y.local/", nil))
	if reached != 1 {
		t.Fatalf("reached %d", reached)
	}
}

// The real thing: a URL whose target is the server it is served by. One
// request is forwarded once, comes back carrying its marker, and is cut:
// two requests in all, not thousands.
func TestALoopThroughCosmosIsCut(t *testing.T) {
	countLogs(t)
	var hits int32
	var handler http.Handler
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	route := utils.ProxyRouteConfig{Name: "self", Mode: "PROXY", Target: server.URL}
	handler = routeHandlerForTarget(server.URL, route)

	client := &http.Client{Timeout: 10 * time.Second} // without the guard this never ends
	res, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusLoopDetected || atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("status %d after %d requests", res.StatusCode, hits)
	}
}

// Two different URLs in a row (Cosmos → app → Cosmos → another app) are not a loop,
// and a request that went through Cosmos too many times is cut anyway.
func TestHopsBetweenURLsAreAllowedUpToABound(t *testing.T) {
	countLogs(t)
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Add(HopHeader, hopMarker("a", "http://a:80"))
	rec := httptest.NewRecorder()
	forkTargetGuard(utils.ProxyRouteConfig{Name: "b", Mode: "PROXY", Target: "http://192.0.2.1:80"})(ok).ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("status %d", rec.Code)
	}
	req = httptest.NewRequest("GET", "/", nil)
	for i := 0; i < maxHops; i++ {
		req.Header.Add(HopHeader, hopMarker("r"+string(rune('a'+i)), "http://x"))
	}
	rec = httptest.NewRecorder()
	forkTargetGuard(utils.ProxyRouteConfig{Name: "b", Mode: "PROXY", Target: "http://192.0.2.1:80"})(ok).ServeHTTP(rec, req)
	if rec.Code != http.StatusLoopDetected {
		t.Fatalf("status %d", rec.Code)
	}
	// someone else's markers are not counted, nor taken for ours
	req = httptest.NewRequest("GET", "/", nil)
	for i := 0; i < 20; i++ {
		req.Header.Add(HopHeader, "0123456789ab.0000000000")
	}
	rec = httptest.NewRecorder()
	forkTargetGuard(utils.ProxyRouteConfig{Name: "b", Mode: "PROXY", Target: "http://192.0.2.1:80"})(ok).ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("status %d", rec.Code)
	}
}

// The page is plain: a hostile URL name is escaped.
func TestUnreachablePageEscapes(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept", "text/html")
	writeUnreachable(rec, req, 508, "loop", `<script>x</script>`)
	if strings.Contains(rec.Body.String(), "<script>") {
		t.Fatal(rec.Body.String())
	}
}
