package reachwatch

// The machine: which names are watched, when they are tested, when a
// failure counts, when an outage opens and closes, and what is told.

import (
	"context"
	"math/rand"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/upnp"
	"github.com/azukaar/cosmos-server/src/utils"
)

// The states of a name.
const (
	StatePending   = "pending"   // not tested yet
	StateReachable = "reachable" // the last test came back in
	StateSuspect   = "suspect"   // failed, being confirmed
	StateDown      = "down"      // failed three times in a row
	StateUnknown   = "unknown"   // the last test could not conclude
)

// Bounds. Variables for the tests.
var (
	maxWatched   = 50
	perRound     = 8
	parallel     = 2
	confirmGaps  = []time.Duration{30 * time.Second, time.Minute} // the second try 30 s after the first, the third 60 s after that
	downPauses   = []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute}
	downPauseMin = 10 * time.Minute // after downPauses: max(interval, this)
	firstRound   = time.Minute
	forceGap     = 15 * time.Second
	upnpGap      = 5 * time.Minute
	jitterShare  = 0.1
	maxHistory   = 50
)

// Replaced by the tests.
var (
	clock      = time.Now
	tester     = testName
	upnpStatus = upnp.GetStatus
	upnpCheck  = upnp.CheckNow
	jitter     = func(d time.Duration) time.Duration {
		span := int64(float64(d) * jitterShare)
		if span <= 0 {
			return d
		}
		return d - time.Duration(span) + time.Duration(rand.Int63n(2*span+1))
	}
)

// NameState is what is known about one watched name.
type NameState struct {
	Host     string    `json:"host"`
	Panel    bool      `json:"panel,omitempty"`
	State    string    `json:"state"`
	Cause    string    `json:"cause,omitempty"`
	Code     string    `json:"code,omitempty"`
	LastTest time.Time `json:"lastTest,omitzero"`
	SeenAt   time.Time `json:"seenAt,omitzero"`
	Since    time.Time `json:"since,omitzero"` // the first failure of the series
	Fails    int       `json:"fails,omitempty"`
	// Note: while down, a test that could not conclude says why here
	Note    string `json:"note,omitempty"`
	Raw     string `json:"raw,omitempty"`
	retryAt time.Time
}

// Outage is a time the server could not be reached.
type Outage struct {
	Since  time.Time  `json:"since"`
	End    *time.Time `json:"end,omitempty"`
	Cause  string     `json:"cause"`
	Code   string     `json:"code,omitempty"`
	Names  []string   `json:"names"`
	Asked  bool       `json:"asked,omitempty"`  // the router module was asked to check
	Router string     `json:"router,omitempty"` // what it said
	// Stopped: the watch was switched off (or nothing was left to test)
	// while it lasted; it did not end by being seen again
	Stopped bool   `json:"stopped,omitempty"`
	Thread  string `json:"thread,omitempty"`
}

// watcher holds everything; one per process.
type watcher struct {
	mu        sync.Mutex
	run       sync.Mutex // one round at a time
	names     map[string]*NameState
	order     []string // the app names, in the order of the rotation
	rotation  int
	nextRound time.Time
	downRound int // rounds since the outage opened, for the back-off
	outage    *Outage
	history   []Outage
	lastForce time.Time
	upnpAsked time.Time
	router    string // what the router module said when last asked
	seenAs    string // the server's public address as Cloudflare saw it
	lastRound time.Time
	running   bool
	started   bool
	dirty     bool
}

var w = newWatcher()

func newWatcher() *watcher {
	return &watcher{names: map[string]*NameState{}}
}

// candidate is a name that can be tested, with whether it is the panel's.
type candidate struct {
	host  string
	panel bool
}

// Plan is what the configuration allows to be tested.
type Plan struct {
	Off      bool
	Interval time.Duration
	Port     int
	Names    []candidate
	TooMany  []string
	// Direct: names served directly, which cannot be tested from inside
	Direct []string
	// Why nothing can be tested, when nothing can: "", "noHTTPS", "port"
	Reason string
}

// planFor works out what is tested from a configuration.
func planFor(config utils.Config, https bool) Plan {
	http := config.HTTPConfig
	p := Plan{Off: http.Cloudflare.Watch.Off, Interval: utils.ReachWatchInterval(http.Cloudflare.Watch), Port: utils.PublicHTTPSPort(http)}
	seen := map[string]bool{}
	add := func(host string, panel bool) {
		host = utils.CloudflareHostname(host)
		if host == "" || seen[host] || net.ParseIP(host) != nil || strings.HasSuffix(host, ".local") ||
			!utils.IsValidHostnameSyntax(host) || !strings.Contains(host, ".") || utils.IsHomeHost(host) {
			return
		}
		seen[host] = true
		if !utils.ShieldedIn(http.Cloudflare.Shielded, host) {
			p.Direct = append(p.Direct, host)
			return
		}
		if len(p.Names) >= maxWatched {
			p.TooMany = append(p.TooMany, host)
			return
		}
		p.Names = append(p.Names, candidate{host: host, panel: panel})
	}
	add(http.Hostname, true)
	for _, route := range http.ProxyConfig.Routes {
		if route.Mode != "SERVAPP" || route.Disabled || !route.UseHost {
			continue
		}
		host := strings.TrimSpace(route.Host)
		if _, port, err := net.SplitHostPort(host); err == nil && port != "" {
			continue // a port of its own: not on Cosmos' HTTPS port
		}
		add(host, false)
	}
	switch {
	case !https:
		p.Reason, p.Names = "noHTTPS", nil
	case !utils.IsCloudflareHTTPSPort(p.Port):
		p.Reason, p.Names = "port", nil
	}
	return p
}

// currentPlan is the plan of the running configuration.
var currentPlan = func() Plan { return planFor(utils.GetMainConfig(), utils.IsHTTPS) }

// sync brings the names in line with the plan. The caller holds mu.
func (w *watcher) sync(p Plan) {
	wanted := map[string]bool{}
	order := []string{}
	for _, c := range p.Names {
		wanted[c.host] = true
		st, ok := w.names[c.host]
		if !ok {
			st = &NameState{Host: c.host, State: StatePending}
			w.names[c.host] = st
		}
		st.Panel = c.panel
		if !c.panel {
			order = append(order, c.host)
		}
	}
	for host := range w.names {
		if !wanted[host] {
			delete(w.names, host)
		}
	}
	w.order = order
	if w.rotation >= len(order) {
		w.rotation = 0
	}
}

// due returns the names to test now: the confirmation tries that are due,
// and, when a round is due, the panel and the next names of the rotation.
// It moves the schedule. The caller holds mu.
func (w *watcher) due(now time.Time, p Plan, force bool) []string {
	pick := map[string]bool{}
	list := []string{}
	take := func(host string) {
		if !pick[host] {
			pick[host] = true
			list = append(list, host)
		}
	}
	for _, host := range w.sortedHosts() {
		st := w.names[host]
		if !st.retryAt.IsZero() && !now.Before(st.retryAt) {
			take(host)
		}
	}
	if force || !now.Before(w.nextRound) {
		for _, host := range w.sortedHosts() {
			if w.names[host].Panel {
				take(host)
			}
		}
		// while down, the names that are down are watched first
		for _, host := range w.sortedHosts() {
			if w.names[host].State == StateDown && len(list) < perRound {
				take(host)
			}
		}
		moved := 0
		for i := 0; i < len(w.order) && len(list) < perRound; i++ {
			take(w.order[(w.rotation+i)%len(w.order)])
			moved++
		}
		if len(w.order) > 0 {
			w.rotation = (w.rotation + moved) % len(w.order)
		}
		if w.outage != nil {
			w.downRound++
		}
		w.nextRound = now.Add(jitter(w.pause(p)))
		w.lastRound = now
	}
	return list
}

// pause is the time to the next round.
func (w *watcher) pause(p Plan) time.Duration {
	if w.outage == nil {
		return p.Interval
	}
	if w.downRound < len(downPauses) {
		return downPauses[w.downRound]
	}
	if p.Interval > downPauseMin {
		return p.Interval
	}
	return downPauseMin
}

func (w *watcher) sortedHosts() []string {
	hosts := make([]string, 0, len(w.names))
	for host := range w.names {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}

// step is one look: test what is due and take in the results.
func (w *watcher) step(force bool) {
	w.run.Lock()
	defer w.run.Unlock()

	p := currentPlan()
	now := clock()
	w.mu.Lock()
	if p.Off || len(p.Names) == 0 {
		w.sync(Plan{})
		w.stopOutage(now)
		w.mu.Unlock()
		w.save()
		return
	}
	w.sync(p)
	if w.nextRound.IsZero() && !force {
		w.nextRound = now.Add(firstRound)
	}
	hosts := w.due(now, p, force)
	if len(hosts) == 0 {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.mu.Unlock()

	results := make([]Result, len(hosts))
	var wg sync.WaitGroup
	slots := make(chan struct{}, parallel)
	for i, host := range hosts {
		wg.Add(1)
		go func(i int, host string) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			defer func() {
				if r := recover(); r != nil {
					results[i] = Result{Verdict: Verdict{Status: StatusUnknown, Cause: "noAnswer"}}
				}
			}()
			results[i] = tester(context.Background(), host, p.Port)
		}(i, host)
	}
	wg.Wait()

	now = clock()
	w.mu.Lock()
	w.running = false
	path := false
	for i, host := range hosts {
		results[i].At = now
		if w.take(host, results[i], now) {
			path = true
		}
	}
	ask := path && w.mayAskRouter(now)
	w.mu.Unlock()

	if ask {
		w.askRouter(now)
	}

	w.mu.Lock()
	w.settle(now, p)
	w.mu.Unlock()
	w.save()
}

// take applies the result of one test. It reports whether the failure is
// one on the way to this server (the router module may help).
func (w *watcher) take(host string, r Result, now time.Time) bool {
	st, ok := w.names[host]
	if !ok {
		return false
	}
	st.LastTest, st.Raw = now, r.Raw
	if r.Visitor != "" {
		if ip := net.ParseIP(r.Visitor); ip != nil {
			w.seenAs = ip.String()
		}
	}
	v := r.Verdict
	switch v.Status {
	case StatusOK:
		if st.State != StateReachable {
			utils.Log("Reachability watch: " + host + " can be reached from the internet")
		}
		st.State, st.Cause, st.Code, st.SeenAt, st.Since, st.Fails, st.Note = StateReachable, "", "", now, time.Time{}, 0, ""
		st.retryAt = time.Time{}
		w.dirty = true
		return false
	case StatusUnknown:
		if st.State == StateDown {
			st.Note = v.Cause
			return false
		}
		st.State, st.Cause, st.Code, st.Fails, st.Since = StateUnknown, v.Cause, v.Code, 0, time.Time{}
		st.retryAt = time.Time{}
		return false
	}
	st.Note = ""
	if st.State == StateDown {
		st.Cause, st.Code = v.Cause, v.Code
		return inList(v.Cause, utils.ReachWatchPathCauses)
	}
	st.Fails++
	if st.Fails == 1 {
		st.Since = now
	}
	st.Cause, st.Code = v.Cause, v.Code
	if st.Fails >= 1+len(confirmGaps) {
		st.State = StateDown
		st.retryAt = time.Time{}
		utils.Warn("Reachability watch: " + host + " cannot be reached from the internet (" + v.Cause + " " + v.Code + ")")
	} else {
		st.State = StateSuspect
		st.retryAt = now.Add(confirmGaps[st.Fails-1])
		utils.Debug("Reachability watch: " + host + " failed (" + v.Cause + " " + v.Code + "), trying again")
	}
	return inList(v.Cause, utils.ReachWatchPathCauses)
}

func inList(value string, list []string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// mayAskRouter: the router module keeps a forward of Cosmos' HTTPS port and
// was not asked in the last upnpGap. The caller holds mu.
func (w *watcher) mayAskRouter(now time.Time) bool {
	if !w.upnpAsked.IsZero() && now.Sub(w.upnpAsked) < upnpGap {
		return false
	}
	_, holds := upnp.HoldsCosmosForward(upnpStatus())
	return holds
}

// askRouter asks the router module to check its forwards now, and keeps
// what it said.
func (w *watcher) askRouter(now time.Time) {
	w.mu.Lock()
	w.upnpAsked = now
	if w.outage != nil {
		w.outage.Asked = true
	}
	w.mu.Unlock()
	utils.Log("Reachability watch: Cloudflare cannot reach this server: asking the router module to check its forwards now")
	st := upnpCheck()
	says := routerSays(st)
	w.mu.Lock()
	w.router = says
	if w.outage != nil {
		w.outage.Router = says
	}
	w.mu.Unlock()
}

// routerSays puts what the router module answered into a few words.
func routerSays(st upnp.Status) string {
	m, holds := upnp.HoldsCosmosForward(st)
	switch {
	case !holds:
		return "automatic port forwarding is off"
	case m.Active:
		return "the router forwards port " + itoa(m.ExternalPort) + " to this server"
	case m.Error != "":
		return "the router did not make the forward of port " + itoa(m.WantedPort) + ": " + oneLine(m.Error)
	case st.Error != "":
		return "the router could not be asked: " + oneLine(st.Error)
	default:
		return "the forward of port " + itoa(m.WantedPort) + " is not in place"
	}
}

// settle opens, changes and closes the outage. The caller holds mu.
func (w *watcher) settle(now time.Time, p Plan) {
	down := []*NameState{}
	for _, host := range w.sortedHosts() {
		if st := w.names[host]; st.State == StateDown {
			down = append(down, st)
		}
	}
	if len(down) == 0 {
		if w.outage != nil {
			o := *w.outage
			end := now
			o.End = &end
			w.closeOutage(o)
			w.outage, w.downRound = nil, 0
			w.nextRound = now.Add(jitter(p.Interval))
			notifyRestored(o, p)
		}
		return
	}
	primary := down[0]
	for _, st := range down {
		if st.Panel {
			primary = st
		}
	}
	names := []string{}
	since := primary.Since
	for _, st := range down {
		names = append(names, st.Host)
		if !st.Since.IsZero() && st.Since.Before(since) {
			since = st.Since
		}
	}
	if since.IsZero() {
		since = now
	}
	sort.SliceStable(names, func(i, j int) bool { return names[i] == primary.Host && names[j] != primary.Host })
	switch {
	case w.outage == nil:
		asked := !w.upnpAsked.IsZero() && !w.upnpAsked.Before(since)
		w.outage = &Outage{Since: since, Cause: primary.Cause, Code: primary.Code, Names: names, Asked: asked}
		if asked {
			w.outage.Router = w.router // what the router module said about this outage, not an earlier one
		}
		w.outage.Thread = threadFor(primary.Cause)
		w.downRound = 0
		w.nextRound = now.Add(jitter(w.pause(p)))
		w.dirty = true
		notifyDown(*w.outage, primary, p, w.nextRound)
	case w.outage.Cause != primary.Cause:
		previous := w.outage.Thread
		w.outage.Cause, w.outage.Code, w.outage.Names = primary.Cause, primary.Code, names
		w.outage.Thread = threadFor(primary.Cause)
		w.dirty = true
		closeQuietly(previous)
		notifyDown(*w.outage, primary, p, w.nextRound)
	case strings.Join(w.outage.Names, ",") != strings.Join(names, ",") || w.outage.Code != primary.Code:
		w.outage.Names, w.outage.Code = names, primary.Code
		w.dirty = true
		notifyDown(*w.outage, primary, p, w.nextRound)
	}
}

func threadFor(cause string) string { return "reach:" + cause }

// stopOutage ends an open outage without a "back" when nothing is watched
// any more. The caller holds mu.
func (w *watcher) stopOutage(now time.Time) {
	if w.outage == nil {
		return
	}
	o := *w.outage
	end := now
	o.End, o.Stopped = &end, true
	w.closeOutage(o)
	closeQuietly(o.Thread)
	w.outage, w.downRound = nil, 0
}

func (w *watcher) closeOutage(o Outage) {
	w.history = append(w.history, o)
	if len(w.history) > maxHistory {
		w.history = w.history[len(w.history)-maxHistory:]
	}
	w.dirty = true
}

// forceRound starts a round now in the background. It reports false when
// the last forced one was less than forceGap ago, or one is running.
func (w *watcher) forceRound() bool {
	w.mu.Lock()
	now := clock()
	if w.running || (!w.lastForce.IsZero() && now.Sub(w.lastForce) < forceGap) {
		w.mu.Unlock()
		return false
	}
	w.lastForce = now
	w.mu.Unlock()
	go w.step(true)
	return true
}

// loop is the background look.
var tick = 10 * time.Second

func (w *watcher) loop() {
	for {
		func() {
			defer func() {
				if r := recover(); r != nil {
					utils.Error("Reachability watch: a round failed", nil)
				}
			}()
			w.step(false)
		}()
		time.Sleep(tick)
	}
}

var startOnce sync.Once

// Start runs the watch in the background, once per process.
func Start() {
	startOnce.Do(func() {
		w.load()
		go w.loop()
	})
}

func itoa(n int) string {
	if n == 0 {
		return "?"
	}
	return strconv.Itoa(n)
}
