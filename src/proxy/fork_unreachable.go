package proxy

// Fork addition (plan 43): a URL whose app is not running answers at once,
// in words, and a request can never come back to Cosmos for the same URL.
//
// What happened before: on a server where Cosmos runs outside Docker, the
// Director asked Docker for the container's address; for a container that
// was restarting or stopped the answer was "" (an error, logged), and the
// request was sent to ":<port>", that is to this server itself (to port 80
// when the URL's target names no port). When that port is Cosmos' own, the
// request arrived again from 127.0.0.1 for the same URL, was sent again, and
// so on: thousands of requests a minute from one visit, two error lines
// each, until the visitor gave up ("context canceled") or the shield struck
// 127.0.0.1.
//
// Now:
//   - the container is resolved before the proxy runs (forkTargetGuard); a
//     container that is restarting, stopped, missing or without an address
//     gets a 503 page that says so, and the proxy never dials;
//   - an address that would be this server's own Cosmos port is refused;
//   - every request Cosmos forwards carries X-Cosmos-Hop with a marker of
//     this process and this URL's target: a request that arrives carrying
//     the marker of the URL it is for is a loop and is answered 508, and so
//     is one that went through Cosmos more than maxHops times;
//   - each of these is logged once per URL and minute, with a count.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

// HopHeader is added to every request Cosmos forwards to an app.
const HopHeader = "X-Cosmos-Hop"

const maxHops = 8

// hopToken tells this process' markers from anyone else's.
var hopToken = func() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}()

func hopMarker(routeName, target string) string {
	sum := sha256.Sum256([]byte(routeName + "\x00" + target))
	return hopToken + "." + hex.EncodeToString(sum[:5])
}

// hopsOf answers whether the request already carries the marker, and how
// many markers of this process it carries.
func hopsOf(header http.Header, marker string) (seen bool, ours int) {
	for _, value := range header.Values(HopHeader) {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == marker {
				seen = true
			}
			if strings.HasPrefix(part, hopToken+".") {
				ours++
			}
		}
	}
	return seen, ours
}

// dockerContainerTarget is a variable so that tests answer for Docker.
var dockerContainerTarget = docker.ContainerTarget

type resolvedKey struct{}

// forkDirectorHost is what the Director dials for a SERVAPP URL when Cosmos
// runs outside Docker: the address resolved by forkTargetGuard, else asked
// now. "" when there is none, which makes the transport fail without
// dialing anything ("no Host in request URL").
func forkDirectorHost(req *http.Request, container string, port string) string {
	ip, _ := req.Context().Value(resolvedKey{}).(string)
	if ip == "" {
		ip, _, _ = dockerContainerTarget(container)
	}
	if ip == "" {
		return ""
	}
	return net.JoinHostPort(ip, port)
}

// ownPorts are the ports this Cosmos listens on for its web server.
var ownPorts = func() []string {
	config := utils.GetMainConfig()
	return []string{config.HTTPConfig.HTTPPort, config.HTTPConfig.HTTPSPort}
}

func portOrDefault(target *url.URL) string {
	if p := target.Port(); p != "" {
		return p
	}
	if target.Scheme == "https" {
		return "443"
	}
	return "80"
}

// pointsAtCosmos says whether an address is this server's own Cosmos port.
func pointsAtCosmos(ip string, port string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil || !(parsed.IsLoopback() || parsed.IsUnspecified()) {
		return false
	}
	for _, own := range ownPorts() {
		if own != "" && own == port {
			return true
		}
	}
	return false
}

// forkTargetGuard runs in front of the reverse proxy of a SERVAPP or PROXY URL.
func forkTargetGuard(route utils.ProxyRouteConfig) func(http.Handler) http.Handler {
	target, err := url.Parse(route.Target)
	if err != nil {
		return func(next http.Handler) http.Handler { return next }
	}
	marker := hopMarker(route.Name, route.Target)
	resolve := route.Mode == "SERVAPP" && (!utils.IsInsideContainer || utils.IsHostNetwork)
	container := target.Hostname()
	port := portOrDefault(target)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if seen, ours := hopsOf(r.Header, marker); seen || ours >= maxHops {
				unreachableNote(route.Name, "loop", "a request for this URL came back to Cosmos: cut, answered 508")
				writeUnreachable(w, r, http.StatusLoopDetected, "loop", route.Name)
				return
			}
			if resolve {
				ip, state, err := dockerContainerTarget(container)
				if err != nil || ip == "" {
					unreachableNote(route.Name, string(state), "container "+container+" is "+stateWords(state)+": answered 503")
					writeUnreachable(w, r, http.StatusServiceUnavailable, string(state), container)
					return
				}
				if pointsAtCosmos(ip, port) {
					unreachableNote(route.Name, "self", "the target "+ip+":"+port+" is Cosmos itself: answered 508")
					writeUnreachable(w, r, http.StatusLoopDetected, "loop", route.Name)
					return
				}
				r = r.WithContext(context.WithValue(r.Context(), resolvedKey{}, ip))
			}
			r.Header.Add(HopHeader, marker)
			next.ServeHTTP(w, r)
		})
	}
}

func stateWords(state docker.TargetState) string {
	switch state {
	case docker.TargetRestarting:
		return "restarting"
	case docker.TargetStopped:
		return "not running"
	case docker.TargetMissing:
		return "missing"
	case docker.TargetNoAddress:
		return "without a network address"
	}
	return "not answering Docker's questions"
}

// ---- one log line per URL and reason and minute ------------------------------

const unreachableEvery = time.Minute

type noteState struct {
	at    time.Time
	count int
}

var (
	notesMu sync.Mutex
	notes   = map[string]*noteState{}
	// logUnreachable is a variable so that tests count the lines
	logUnreachable = func(text string) { utils.Warn(text) }
)

func unreachableNote(routeName, reason, text string) {
	key := routeName + "\x00" + reason
	now := time.Now()
	notesMu.Lock()
	n := notes[key]
	if n == nil {
		if len(notes) >= 256 {
			notes = map[string]*noteState{}
		}
		n = &noteState{}
		notes[key] = n
	}
	n.count++
	if !n.at.IsZero() && now.Sub(n.at) < unreachableEvery {
		notesMu.Unlock()
		return
	}
	count := n.count
	n.at, n.count = now, 0
	notesMu.Unlock()
	logUnreachable(fmt.Sprintf("[proxy] URL %s: %s (%d request(s) since the last message)", printable(routeName, 120), text, count))
}

// ---- the answer ----------------------------------------------------------------

func writeUnreachable(w http.ResponseWriter, r *http.Request, status int, state string, name string) {
	title, line := "The application is not running", "Cosmos cannot reach it right now."
	switch state {
	case string(docker.TargetRestarting):
		title, line = "The application is restarting", "It keeps stopping and Docker starts it again. An administrator can see why in its log, in Cosmos."
	case string(docker.TargetStopped):
		title, line = "The application is stopped", "An administrator can start it in Cosmos."
	case string(docker.TargetMissing):
		title, line = "The application does not exist", "This address points at a container that is no longer there."
	case "loop":
		title, line = "This address points back at itself", "The request came back to Cosmos for the same address, so it was stopped. An administrator should check where this URL points."
	}
	w.Header().Set(ProbeHeader, state)
	w.Header().Set("Cache-Control", "no-store")
	if status == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "10")
	}
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if status == http.StatusServiceUnavailable {
			w.Header().Set("Refresh", "10")
		}
		w.WriteHeader(status)
		w.Write([]byte(strings.NewReplacer("{title}", html.EscapeString(title), "{name}", html.EscapeString(name), "{line}", html.EscapeString(line)).Replace(unreachablePage)))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	w.Write([]byte(title + " (" + name + "). " + line + "\n"))
}

// Self-contained, like the lazy start page: nothing loaded from elsewhere.
const unreachablePage = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{title}</title>
<style>
html,body{height:100%;margin:0}
body{display:flex;align-items:center;justify-content:center;background:#101418;color:#e6e9ee;font-family:system-ui,-apple-system,"Segoe UI",Roboto,Helvetica,Arial,sans-serif}
.box{max-width:28rem;padding:2rem;text-align:center}
h1{margin:0 0 .75rem;font-size:1.25rem;font-weight:600}
p{margin:0 0 .75rem;font-size:.9rem;line-height:1.5;color:#a7b0bd}
code{color:#e6e9ee}
</style>
</head>
<body>
<div class="box">
<h1>{title}</h1>
<p><code>{name}</code></p>
<p>{line}</p>
</div>
</body>
</html>
`

// forkProxyError answers a request the proxy could not deliver. A container
// that stopped or restarts since its address was cached gets the same page
// as the guard gives (its address is forgotten, so the next request asks
// Docker again); anything else is upstream's 502, logged once per URL and
// minute instead of once per request.
func forkProxyError(route utils.ProxyRouteConfig, target *url.URL) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		if r.Context().Err() != nil {
			// the visitor went away: nothing to answer
			unreachableNote(route.Name, "canceled", "the visitor left before the app answered")
			w.WriteHeader(499)
			return
		}
		if route.Mode == "SERVAPP" && (!utils.IsInsideContainer || utils.IsHostNetwork) && target != nil {
			name := target.Hostname()
			forgetTarget(name)
			if _, state, terr := dockerContainerTarget(name); terr != nil {
				unreachableNote(route.Name, string(state), "container "+name+" is "+stateWords(state)+": answered 503")
				writeUnreachable(w, r, http.StatusServiceUnavailable, string(state), name)
				return
			}
		}
		unreachableNote(route.Name, "error", "the app did not answer: "+printable(err.Error(), 200))
		w.WriteHeader(http.StatusBadGateway)
	}
}

// forgetTarget is a variable so that tests keep their stub.
var forgetTarget = docker.ForgetTarget
