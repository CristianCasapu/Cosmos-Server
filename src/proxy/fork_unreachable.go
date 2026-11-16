package proxy

// Fork addition (plan 43): a URL whose app is not running answers at once,
// in words, and a request is never dialled back into Cosmos itself.
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
//   - forkTargetGuard resolves a SERVAPP URL's container before the proxy
//     runs; a container that is restarting, stopped or without an address
//     gets a 503 page, and the proxy never dials. A target that is not a
//     container at all (localhost:8123, host.docker.internal, an address, a
//     name of the network) is dialled as written, as a PROXY URL would be:
//     upstream's Director reached such targets by accident, and they must
//     keep working.
//   - forkGuardDial, the proxy's own dialer, refuses an address that is this
//     server (loopback, unspecified, one of its interfaces' addresses) on a
//     port Cosmos itself listens on (its HTTP and HTTPS ports, the ports of
//     its port routes): a request can never be sent back into Cosmos, for
//     whatever reason, in every route mode. This is decided on the address
//     about to be dialled, so it needs no header (an earlier version added
//     X-Cosmos-Hop to every request, which gave every backend a fingerprint of
//     this process and cut an app that called its own URL back), and an app
//     calling its own URL is never mistaken for a loop. What it cannot see is
//     a loop through another machine (a router that sends the request back);
//     the shield bounds that one, as it bounds any client.
//   - after a failed dial, Docker is asked again whether the container still
//     runs, at most once per container every reaskEvery;
//   - a visitor sees only "not available"; the container's name and state are
//     for users who may read the containers' state, and for the log;
//   - each of these is logged once per URL and minute, with a count.

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

// dockerContainerTarget is a variable so that tests answer for Docker.
var dockerContainerTarget = docker.ContainerTarget

// forgetTarget is a variable so that tests keep their stub.
var forgetTarget = docker.ForgetTarget

type resolvedKey struct{}

// resolvedTarget is what forkTargetGuard found: a container's address, or
// "dial the target as written" (it is not a container).
type resolvedTarget struct {
	ip     string
	direct bool
}

// forkDirectorHost is what the Director dials for a SERVAPP URL when Cosmos
// runs outside Docker. "" when there is nothing to dial, which makes the
// transport fail without dialing anything ("no Host in request URL").
func forkDirectorHost(req *http.Request, target *url.URL) string {
	found, ok := req.Context().Value(resolvedKey{}).(resolvedTarget)
	if !ok {
		ip, state, _ := dockerContainerTarget(target.Hostname())
		found = resolvedTarget{ip: ip, direct: state == docker.TargetMissing}
	}
	if found.direct {
		return target.Host
	}
	if found.ip == "" {
		return ""
	}
	return net.JoinHostPort(found.ip, target.Port())
}

// forkTargetGuard runs in front of the reverse proxy of a SERVAPP or PROXY URL.
func forkTargetGuard(route utils.ProxyRouteConfig) func(http.Handler) http.Handler {
	target, err := url.Parse(route.Target)
	resolve := err == nil && route.Mode == "SERVAPP" && (!utils.IsInsideContainer || utils.IsHostNetwork)
	if !resolve {
		return func(next http.Handler) http.Handler { return next }
	}
	container := target.Hostname()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, state, err := dockerContainerTarget(container)
			found := resolvedTarget{ip: ip}
			switch {
			case err == nil:
			case state == docker.TargetMissing:
				// not a container: dialled as written (forkGuardDial still refuses Cosmos itself)
				found = resolvedTarget{direct: true}
			default:
				unreachableNote(route.Name, string(state), "container "+container+" is "+stateWords(state)+": answered 503")
				writeUnreachable(w, r, http.StatusServiceUnavailable, string(state), container)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), resolvedKey{}, found)))
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
	return "unknown: Docker did not answer"
}

// ---- never dial Cosmos itself --------------------------------------------------

// errDialsCosmos is the dial's answer for an address that is Cosmos itself.
var errDialsCosmos = errors.New("the target is this Cosmos itself")

// ownPorts are the ports this Cosmos listens on: its web server and its port routes.
var ownPorts = func() []string {
	config := utils.GetMainConfig()
	ports := []string{config.HTTPConfig.HTTPPort, config.HTTPConfig.HTTPSPort}
	for _, from := range GetActiveProxies() {
		if i := strings.LastIndex(from, ":"); i >= 0 {
			ports = append(ports, from[i+1:])
		}
	}
	return ports
}

var (
	ownAddrMu    sync.Mutex
	ownAddrAt    time.Time
	ownAddrCache []net.IP
	// interfaceAddrs is a variable so that tests give the server's addresses
	interfaceAddrs = net.InterfaceAddrs
)

// ownAddress says whether an address is this server's: loopback, unspecified,
// or one of its interfaces' addresses (read again every 30 s).
func ownAddress(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsUnspecified() {
		return true
	}
	ownAddrMu.Lock()
	if time.Since(ownAddrAt) > 30*time.Second {
		ownAddrCache = ownAddrCache[:0]
		if addrs, err := interfaceAddrs(); err == nil {
			for _, a := range addrs {
				if n, ok := a.(*net.IPNet); ok {
					ownAddrCache = append(ownAddrCache, n.IP)
				}
			}
		}
		ownAddrAt = time.Now()
	}
	list := ownAddrCache
	ownAddrMu.Unlock()
	for _, own := range list {
		if own.Equal(ip) {
			return true
		}
	}
	return false
}

func ownPort(port string) bool {
	for _, p := range ownPorts() {
		if p != "" && p == port {
			return true
		}
	}
	return false
}

// forkGuardDial dials as the proxy's dialer does, but resolves the name
// first and refuses an address that is this Cosmos itself.
func forkGuardDial(dialer *net.Dialer) func(ctx context.Context, network, address string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		var ips []net.IP
		if ip := net.ParseIP(host); ip != nil {
			ips = []net.IP{ip}
		} else if host == "" {
			ips = []net.IP{net.IPv4zero}
		} else {
			resolver := dialer.Resolver
			if resolver == nil {
				resolver = net.DefaultResolver
			}
			addrs, err := resolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, a := range addrs {
				ips = append(ips, a.IP)
			}
		}
		if ownPort(port) {
			for _, ip := range ips {
				if ownAddress(ip) {
					return nil, errDialsCosmos
				}
			}
		}
		var last error = errors.New("no address for " + host)
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
			if ctx.Err() != nil {
				break
			}
		}
		return nil, last
	}
}

// ---- after a failed dial ---------------------------------------------------------

const reaskEvery = 2 * time.Second

var (
	reaskMu sync.Mutex
	reaskAt = map[string]time.Time{}
)

// reaskDue says whether Docker may be asked again about a container now.
func reaskDue(name string) bool {
	reaskMu.Lock()
	defer reaskMu.Unlock()
	now := time.Now()
	if now.Sub(reaskAt[name]) < reaskEvery {
		return false
	}
	if len(reaskAt) >= 512 {
		reaskAt = map[string]time.Time{}
	}
	reaskAt[name] = now
	return true
}

// forkProxyError answers a request the proxy could not deliver.
//   - Cosmos itself as target: 508, said once per minute;
//   - the visitor went away: nothing more to say (upstream answered 502);
//   - a container whose address was cached and which stopped since: the
//     same page as the guard gives (Docker is asked again at most once per
//     container every reaskEvery);
//   - anything else, a timeout included: upstream's 502, logged once per
//     URL and minute instead of once per request.
func forkProxyError(route utils.ProxyRouteConfig, target *url.URL) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		if errors.Is(err, errDialsCosmos) {
			unreachableNote(route.Name, "self", "the target "+printable(route.Target, 120)+" is this Cosmos itself: not dialled, answered 508")
			writeUnreachable(w, r, http.StatusLoopDetected, "loop", route.Name)
			return
		}
		if errors.Is(r.Context().Err(), context.Canceled) {
			unreachableNote(route.Name, "canceled", "the visitor left before the app answered")
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		found, _ := r.Context().Value(resolvedKey{}).(resolvedTarget)
		if route.Mode == "SERVAPP" && target != nil && found.ip != "" && reaskDue(target.Hostname()) {
			name := target.Hostname()
			forgetTarget(name)
			if _, state, terr := dockerContainerTarget(name); terr != nil && state != docker.TargetMissing {
				unreachableNote(route.Name, string(state), "container "+name+" is "+stateWords(state)+": answered 503")
				writeUnreachable(w, r, http.StatusServiceUnavailable, string(state), name)
				return
			}
		}
		unreachableNote(route.Name, "error", "the app did not answer: "+printable(err.Error(), 200))
		w.WriteHeader(http.StatusBadGateway)
	}
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

// mayKnowDetails: the container's name and state are said to users who may
// read the containers' state; a visitor reads only "not available".
var mayKnowDetails = func(r *http.Request) bool {
	return utils.HasPermission(r, utils.PERM_RESOURCES_READ)
}

func writeUnreachable(w http.ResponseWriter, r *http.Request, status int, state string, name string) {
	title, line, shown := "This application is not available right now", "Please try again later.", ""
	if mayKnowDetails(r) {
		shown = name
		switch state {
		case string(docker.TargetRestarting):
			title, line = "The application is restarting", "It keeps stopping and Docker starts it again. Its tile in ServApps says why."
		case string(docker.TargetStopped):
			title, line = "The application is stopped", "It can be started in ServApps."
		case string(docker.TargetNoAddress):
			title, line = "The application has no network address", "Docker gave it none yet: it may be starting."
		case "loop":
			title, line = "This URL points at Cosmos itself", "Its target is this server's own Cosmos port, so the request was not sent. Check where this URL points."
		default:
			title, line = "The application cannot be reached", "Docker did not say whether it runs."
		}
	}
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
		nameLine := ""
		if shown != "" {
			nameLine = "<p><code>" + html.EscapeString(shown) + "</code></p>"
		}
		w.Write([]byte(strings.NewReplacer("{title}", html.EscapeString(title), "{name}", nameLine, "{line}", html.EscapeString(line)).Replace(unreachablePage)))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	if shown != "" {
		w.Write([]byte(title + " (" + shown + "). " + line + "\n"))
		return
	}
	w.Write([]byte(title + ". " + line + "\n"))
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
{name}
<p>{line}</p>
</div>
</body>
</html>
`
