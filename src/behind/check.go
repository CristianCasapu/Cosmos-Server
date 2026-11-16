package behind

// The URL check: does the app accept the address it is reached at?
//
// Asked for one URL of this server by an administrator (or by the panel after
// it made or changed a URL), Cosmos sends the app the request a visitor of
// that URL would bring, exactly as the proxy would compose it
// (proxy.AppHeadersFor), and, when the app refuses, the same once more with
// the other way of telling it its address (its own address as Host, or the
// public one). What it answers is compared and recognised: the signatures of
// the known apps (apps.json), and the generic one "refused under the public
// name, accepted under its own".
//
// Bounds: only a URL that leads to a container of this server, asked at the
// container's own address as Docker gives it (never the host's network,
// never this server's own addresses, never a name looked up anywhere), on
// the URL's own port; at most two requests, GET of the URL's own path, no
// body, no cookie, no credentials, no sign-in; redirects are read, never
// followed; 4 s and 8 KiB per answer; one check at a time on the server. The
// only input is the URL's name: nothing of the request reaches the app.

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/proxy"
	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	askWait   = 4 * time.Second
	askRead   = 8 << 10
	firstMax  = 160
	logKept   = 40
	cacheFor  = 30 * time.Minute
	cacheSize = 512
)

// Attempt is one request and what the app answered.
type Attempt struct {
	AppOwnHost    bool   `json:"appOwnHost"`
	Host          string `json:"host"`
	ForwardedHost string `json:"forwardedHost"`
	Status        int    `json:"status,omitempty"`
	Location      string `json:"location,omitempty"`
	First         string `json:"first,omitempty"`
	// Error: refused (nothing listens), unreachable, timeout, tls
	Error string `json:"error,omitempty"`
}

func (a Attempt) ok() bool { return a.Error == "" && a.Status > 0 && a.Status < 400 }

// Repair is something that makes the app accept its address.
type Repair struct {
	Kind string `json:"kind"` // route, env, file
	// AppOwnHost: the value of the URL's setting (kind route)
	AppOwnHost bool `json:"appOwnHost,omitempty"`
	// Seen: Cosmos tried it and the app then answered
	Seen  bool     `json:"seen"`
	Name  string   `json:"name,omitempty"`  // env
	Value string   `json:"value,omitempty"` // env
	Now   string   `json:"now,omitempty"`   // env: its value today, "" when not set
	Path    string `json:"path,omitempty"`    // file
	Section string `json:"section,omitempty"` // file
	Lines []string `json:"lines,omitempty"` // file
	When  string   `json:"when,omitempty"`
	Why   string   `json:"why,omitempty"`
}

// Result is the check of one URL.
type Result struct {
	Route     string `json:"route"`
	URL       string `json:"url,omitempty"`
	Container string `json:"container,omitempty"`
	Image     string `json:"image,omitempty"`
	App       string `json:"app,omitempty"`
	AppName   string `json:"appName,omitempty"`
	// Verdict: works, answers (the same under every name), warning, refused, wrongAddress, unreachable, notRunning, notChecked
	Verdict string `json:"verdict"`
	// Reason: why (hostPort, hostNotAllowed, untrustedDomain, untrustedProxy, csrfOrigin, wrongAddress, ownHostBreaks, refused, notContainer, noName, hostNetwork, missing, ...)
	Reason string `json:"reason,omitempty"`
	// Line: the answer or log line the reason was recognised in
	Line       string    `json:"line,omitempty"`
	Attempts   []Attempt `json:"attempts"`
	Repairs    []Repair  `json:"repairs"`
	Notes      []string  `json:"notes,omitempty"`
	AppOwnHost bool      `json:"appOwnHost"`
	CheckedAt  time.Time `json:"checkedAt"`
	key        string
}

// variables so that tests answer for Docker and dial their own listener
var (
	// the inspect of the container, with the values of the variables its known app names
	factsOf = func(ctx context.Context, name string) docker.URLCheckFacts {
		facts := docker.URLCheckFactsOf(ctx, name, nil)
		if app := AppFor(facts.Image); app != nil && len(app.Env) > 0 {
			names := []string{}
			for _, e := range app.Env {
				names = append(names, e.Name)
			}
			facts = docker.URLCheckFactsOf(ctx, name, names)
		}
		return facts
	}
	logSince = docker.URLCheckLogSince
	dialApp  = func(ctx context.Context, address string) (net.Conn, error) {
		d := net.Dialer{Timeout: askWait}
		return d.DialContext(ctx, "tcp", address)
	}
	// ownAddresses: this server's own (a container's address is never one of them)
	ownAddresses = net.InterfaceAddrs
)

// addressAllowed is a variable so that tests use documentation addresses
var addressAllowed = usableAddress

var checkMu sync.Mutex // one check at a time on the server

// containerOf is the container a SERVAPP URL leads to, its port and scheme.
func containerOf(route utils.ProxyRouteConfig) (string, string, string, string) {
	if route.Mode != "SERVAPP" {
		return "", "", "", "notContainer"
	}
	target, err := url.Parse(strings.TrimSpace(route.Target))
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") {
		return "", "", "", "notWeb"
	}
	name := target.Hostname()
	// a container's name: one word, as Docker allows (never an address or a name with a dot)
	if name == "" || strings.ContainsAny(name, ".:") || net.ParseIP(name) != nil || strings.EqualFold(name, "localhost") {
		return "", "", "", "notContainer"
	}
	port := target.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[target.Scheme]
	}
	path := target.EscapedPath()
	if route.UsePathPrefix && route.PathPrefix != "" && !route.StripPathPrefix {
		path = strings.TrimSuffix(path, "/") + route.PathPrefix
	}
	if path == "" {
		path = "/"
	}
	return name, port, target.Scheme, path
}

// usableAddress: an address the check may connect to.
func usableAddress(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.IsLoopback() || parsed.IsUnspecified() || parsed.IsMulticast() || parsed.IsLinkLocalUnicast() || parsed.IsLinkLocalMulticast() {
		return false
	}
	if addrs, err := ownAddresses(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.Equal(parsed) {
				return false
			}
		}
	}
	return true
}

// ask sends one request to the app: GET path at ip:port, as headers say.
func ask(ctx context.Context, ip, port, targetScheme, path string, h proxy.AppHeaders, ownHost bool) (Attempt, string) {
	attempt := Attempt{AppOwnHost: ownHost, Host: h.Host, ForwardedHost: h.ForwardedHost}
	ctx, cancel := context.WithTimeout(ctx, askWait)
	defer cancel()
	conn, err := dialApp(ctx, net.JoinHostPort(ip, port))
	if err != nil {
		attempt.Error = "unreachable"
		if strings.Contains(err.Error(), "refused") {
			attempt.Error = "refused"
		} else if errors.Is(err, context.DeadlineExceeded) {
			attempt.Error = "timeout"
		}
		return attempt, ""
	}
	// the address the app sees Cosmos at: what a trusted-proxy setting must name
	peer, _, _ := net.SplitHostPort(conn.LocalAddr().String())
	used := false
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			if used {
				return nil, errors.New("one connection only")
			}
			used = true
			return conn, nil
		},
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: askWait,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, // an app's own certificate is its own, as the proxy's AcceptInsecureHTTPSTarget
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetScheme+"://"+net.JoinHostPort(ip, port)+path, nil)
	if err != nil {
		conn.Close()
		attempt.Error = "unreachable"
		return attempt, peer
	}
	req.Host = h.Host
	req.Header.Set("X-Forwarded-Host", h.ForwardedHost)
	req.Header.Set("X-Forwarded-Port", h.ForwardedPort)
	req.Header.Set("X-Forwarded-Proto", h.ForwardedProto)
	if h.ForwardedProto == "https" {
		req.Header.Set("X-Forwarded-Ssl", "on")
	}
	req.Header.Set("X-Forwarded-For", peer)
	req.Header.Set("X-Real-IP", peer)
	req.Header.Set("User-Agent", "Cosmos-URL-check")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
	resp, err := client.Do(req)
	if err != nil {
		attempt.Error = "unreachable"
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timeout") {
			attempt.Error = "timeout"
		}
		return attempt, peer
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, askRead))
	attempt.Status = resp.StatusCode
	attempt.Location = clean(resp.Header.Get("Location"), 300)
	attempt.First = firstText(body)
	return attempt, peer
}

// clean keeps printable text, cut.
func clean(text string, max int) string {
	out := []rune{}
	for _, r := range text {
		if len(out) >= max {
			out = append(out, '…')
			break
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || (r >= 0x200b && r <= 0x200f) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			r = ' '
		}
		out = append(out, r)
	}
	return strings.TrimSpace(string(out))
}

// firstText is the start of an answer as text: tags removed, spaces folded.
func firstText(body []byte) string {
	text := string(body)
	var b strings.Builder
	inTag := false
	for _, r := range text {
		switch {
		case r == '<':
			inTag = true
			b.WriteRune(' ')
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return clean(strings.Join(strings.Fields(b.String()), " "), firstMax)
}

// matchSignature finds the first signature of an app (then the generic
// ones) that the answer and the log show.
func matchSignature(app *App, a Attempt, raw string, log []string) (*Signature, string, *App) {
	type list struct {
		app  *App
		sigs []Signature
	}
	lists := []list{}
	if app != nil {
		lists = append(lists, list{app, app.Signatures})
	} else {
		// an app without an image of its own (a dev server) is known by its answer
		for i := range known {
			if len(known[i].Images) == 0 {
				lists = append(lists, list{&known[i], known[i].Signatures})
			}
		}
	}
	lists = append(lists, list{app, generic})
	for _, l := range lists {
		for i := range l.sigs {
			s := &l.sigs[i]
			if len(s.Status) > 0 && !containsInt(s.Status, a.Status) {
				continue
			}
			if s.body == nil && s.log == nil {
				return s, "", l.app
			}
			if s.body != nil && s.body.MatchString(raw) {
				return s, a.First, l.app
			}
			if s.log != nil {
				for _, line := range log {
					if s.log.MatchString(line) {
						return s, clean(line, 300), l.app
					}
				}
			}
		}
	}
	return nil, "", app
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// generic are the refusals of apps Cosmos does not know, by what common
// servers and frameworks answer (sources in apps.json's about).
var generic = mustSignatures([]Signature{
	{Reason: "hostNotAllowed", Status: []int{400, 403}, Body: `(?i)invalid host header|blocked request\. this host|disallowedhost|invalid http_host header|host not allowed|not an allowed host`, Example: "Invalid Host header"},
	{Reason: "untrustedDomain", Status: []int{400}, Body: `(?i)access through untrusted domain`, Example: "Access through untrusted domain"},
	{Reason: "hostNotAllowed", Status: []int{421}, Example: "421 Misdirected Request"},
	{Reason: "untrustedProxy", Status: []int{400}, Log: `(?i)request from a reverse proxy was received from`, Example: "A request from a reverse proxy was received from 172.30.0.1, but your HTTP integration is not set-up for reverse proxies"},
})

func compile(pattern string) (*regexp.Regexp, error) { return regexp.Compile(pattern) }

func mustSignatures(list []Signature) []Signature {
	for i := range list {
		var err error
		if list[i].Body != "" {
			if list[i].body, err = compile(list[i].Body); err != nil {
				panic(err)
			}
		}
		if list[i].Log != "" {
			if list[i].log, err = compile(list[i].Log); err != nil {
				panic(err)
			}
		}
	}
	return list
}

// wrongLocation: a redirect that sends the visitor elsewhere than the URL
// (http for https, another name, a missing or other port).
func wrongLocation(location string, public proxy.PublicAddress, scheme string) bool {
	if location == "" {
		return false
	}
	u, err := url.Parse(location)
	if err != nil || u.Host == "" {
		return false // relative: on the same address
	}
	if !strings.EqualFold(u.Hostname(), public.Name) {
		return true
	}
	if u.Scheme != "" && u.Scheme != scheme {
		return true
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[strings.ToLower(u.Scheme)]
	}
	return port != public.Port
}

// facts for the values of the data: every public address of the container's URLs
func fillFor(config utils.Config, route utils.ProxyRouteConfig, container string, public proxy.PublicAddress, scheme, peer string) fillFacts {
	f := fillFacts{url: scheme + "://" + public.Host, host: public.Host, name: public.Name, port: public.Port, proxy: peer}
	seen := map[string]bool{}
	add := func(p proxy.PublicAddress, s string) {
		u := s + "://" + p.Host
		if seen[u] {
			return
		}
		seen[u] = true
		f.urls = append(f.urls, u)
		f.hosts = append(f.hosts, p.Host)
		f.names = append(f.names, p.Name)
	}
	add(public, scheme)
	for _, other := range config.HTTPConfig.ProxyConfig.Routes {
		if other.Name == route.Name || other.Disabled {
			continue
		}
		if name, _, _, _ := containerOf(other); name == container {
			if p, s, ok := proxy.PublicAddressFor(other); ok {
				add(p, s)
			}
		}
	}
	return f
}

// Check asks the app of one URL.
func Check(ctx context.Context, routeName string) Result {
	result := Result{Route: routeName, Attempts: []Attempt{}, Repairs: []Repair{}, CheckedAt: time.Now()}
	config := utils.GetMainConfig()
	var route *utils.ProxyRouteConfig
	for i := range config.HTTPConfig.ProxyConfig.Routes {
		if config.HTTPConfig.ProxyConfig.Routes[i].Name == routeName {
			route = &config.HTTPConfig.ProxyConfig.Routes[i]
			break
		}
	}
	if route == nil {
		result.Verdict, result.Reason = "notChecked", "noRoute"
		return result
	}
	result.AppOwnHost = route.ForkAppOwnHost
	result.key = fingerprint(*route)
	container, port, targetScheme, path := containerOf(*route)
	if container == "" {
		result.Verdict, result.Reason = "notChecked", path
		return result
	}
	result.Container = container
	public, scheme, ok := proxy.PublicAddressFor(*route)
	if !ok || route.SpoofHostname || route.OverwriteHostHeader != "" {
		result.Verdict, result.Reason = "notChecked", "noName"
		return result
	}
	result.URL = scheme + "://" + public.Host

	facts := factsOf(ctx, container)
	result.Image = facts.Image
	result.key += "|" + facts.ID
	app := AppFor(facts.Image)
	if app != nil {
		result.App, result.AppName = app.ID, app.Name
	}
	if facts.IP == "" {
		result.Verdict, result.Reason = "notRunning", facts.Why
		return result
	}
	if !addressAllowed(facts.IP) {
		result.Verdict, result.Reason = "notChecked", "hostNetwork"
		return result
	}

	checkMu.Lock()
	defer checkMu.Unlock()
	since := time.Now().Add(-time.Second)
	headers := func(own bool) proxy.AppHeaders {
		r := *route
		r.ForkAppOwnHost = own
		return proxy.AppHeadersFor(r, public, scheme, net.JoinHostPort(facts.IP, port), targetScheme)
	}
	first, peer := ask(ctx, facts.IP, port, targetScheme, path, headers(route.ForkAppOwnHost), route.ForkAppOwnHost)
	first.Location = proxy.FixLocation(first.Location, public, scheme) // as the visitor gets it
	result.Attempts = append(result.Attempts, first)
	if first.Error != "" {
		result.Verdict, result.Reason = "unreachable", first.Error
		return result
	}
	f := fillFor(config, *route, container, public, scheme, peer)
	f.image = facts.Image

	if first.ok() {
		result.Verdict = "works"
		if wrongLocation(first.Location, public, scheme) {
			result.Verdict, result.Reason, result.Line = "wrongAddress", "wrongAddress", first.Location
			result.Repairs = append(result.Repairs, appRepairs(app, facts, f, "refused")...)
		}
		if needs := appRepairs(app, facts, f, "always"); len(needs) > 0 {
			if result.Verdict == "works" {
				result.Verdict, result.Reason = "warning", "csrfOrigin"
			}
			result.Repairs = append(result.Repairs, needs...)
		}
		result.Repairs = append(result.Repairs, adviceOf(app, f)...)
		return result
	}

	// the other way of telling the app its address, unless the app's data says it breaks the app
	second := Attempt{Error: "notTried"}
	if route.ForkAppOwnHost || app == nil || !app.NoOwnHost {
		second, _ = ask(ctx, facts.IP, port, targetScheme, path, headers(!route.ForkAppOwnHost), !route.ForkAppOwnHost)
		second.Location = proxy.FixLocation(second.Location, public, scheme)
		result.Attempts = append(result.Attempts, second)
	}
	var log []string
	if facts.ID != "" {
		lines := logSince(ctx, facts.ID, facts.TTY, since)
		if len(lines) > logKept {
			lines = lines[len(lines)-logKept:]
		}
		log = lines
	}
	sig, line, matched := matchSignature(app, first, first.First, log)
	if app == nil && matched != nil {
		app = matched
		result.App, result.AppName = app.ID, app.Name
	}
	// an app known to break with its own address as Host is never told so, whatever one answer said
	ownBreaks := app != nil && app.NoOwnHost && !route.ForkAppOwnHost
	if second.ok() && !wrongLocation(second.Location, public, scheme) && !ownBreaks {
		result.Verdict = "refused"
		result.Reason = "hostPort"
		if route.ForkAppOwnHost {
			result.Reason = "ownHostBreaks"
		} else if sig != nil {
			result.Reason = sig.Reason
		}
		result.Line = line
		if result.Line == "" {
			result.Line = first.First
		}
		result.Repairs = append(result.Repairs, Repair{Kind: "route", AppOwnHost: !route.ForkAppOwnHost, Seen: true})
		result.Repairs = append(result.Repairs, appRepairs(app, facts, f, "refused")...)
		result.Repairs = append(result.Repairs, appRepairs(app, facts, f, "always")...)
		result.Repairs = append(result.Repairs, adviceOf(app, f)...)
		return result
	}
	if sig != nil {
		result.Verdict, result.Reason, result.Line = "refused", sig.Reason, line
		result.Repairs = append(result.Repairs, appRepairs(app, facts, f, "refused")...)
		result.Repairs = append(result.Repairs, appRepairs(app, facts, f, "always")...)
		result.Repairs = append(result.Repairs, filesOf(app, f, "refused")...)
		result.Repairs = append(result.Repairs, adviceOf(app, f)...)
		return result
	}
	if second.Error == "" && second.Status == first.Status {
		// the same under every name: what it answers is the app's own business
		result.Verdict, result.Reason = "answers", "sameAnswer"
		result.Repairs = append(result.Repairs, appRepairs(app, facts, f, "always")...)
		return result
	}
	result.Verdict, result.Reason, result.Line = "refused", "refused", first.First
	result.Repairs = append(result.Repairs, appRepairs(app, facts, f, "refused")...)
	result.Repairs = append(result.Repairs, filesOf(app, f, "refused")...)
	return result
}

// appRepairs are the variables of a known app that are missing or wrong.
func appRepairs(app *App, facts docker.URLCheckFacts, f fillFacts, when string) []Repair {
	out := []Repair{}
	if app == nil {
		return out
	}
	for _, e := range app.Env {
		if e.When != when && !(when == "always" && e.When == "ifSet") {
			continue
		}
		now, set := facts.Env[e.Name]
		if e.When == "ifSet" && (!set || now == "") {
			continue
		}
		if set && now != "" {
			want := fill(e.Contains, f)
			if e.Contains == "" || strings.Contains(strings.ToLower(now), strings.ToLower(want)) {
				continue
			}
		}
		out = append(out, Repair{Kind: "env", Name: e.Name, Value: fill(e.Value, f), Now: now, When: e.When, Why: e.Why})
	}
	return out
}

// filesOf are a known app's configuration lines for a refusal.
func filesOf(app *App, f fillFacts, when string) []Repair {
	out := []Repair{}
	if app == nil {
		return out
	}
	for _, file := range app.File {
		if file.When != when || (len(file.Images) > 0 && !imageIn(file.Images, f.image)) {
			continue
		}
		lines := []string{}
		for _, l := range file.Lines {
			lines = append(lines, fill(l, f))
		}
		out = append(out, Repair{Kind: "file", Path: file.Path, Section: file.Section, Lines: lines, When: file.When, Why: file.Why})
	}
	return out
}

// adviceOf are the lines a known app is better with (the visitor's address, for example).
func adviceOf(app *App, f fillFacts) []Repair {
	return filesOf(app, f, "advice")
}

// fingerprint: what of a URL changes the check's answer.
func fingerprint(route utils.ProxyRouteConfig) string {
	return strings.Join([]string{route.Target, route.Host, strconv.FormatBool(route.UseHost), strconv.FormatBool(route.ForkAppOwnHost),
		strconv.FormatBool(route.SpoofHostname), route.OverwriteHostHeader, route.PathPrefix, string(route.Mode)}, "|")
}

// ---- results kept for the panel

var (
	cacheMu sync.Mutex
	cache   = map[string]Result{}
)

func remember(r Result) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if len(cache) >= cacheSize {
		cache = map[string]Result{}
	}
	cache[r.Route] = r
}

// remembered is the last check of a URL while it still describes the URL as it is.
func remembered(route utils.ProxyRouteConfig) (Result, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	r, ok := cache[route.Name]
	if !ok || time.Since(r.CheckedAt) > cacheFor || !strings.HasPrefix(r.key, fingerprint(route)) {
		return Result{}, false
	}
	return r, true
}

func imageIn(list []string, image string) bool {
	key := imageKey(image)
	for _, name := range list {
		if name == key {
			return true
		}
	}
	return false
}
