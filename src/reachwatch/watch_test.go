package reachwatch

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/upnp"
	"github.com/azukaar/cosmos-server/src/utils"
)

// rig replaces everything the watch talks to: the clock, the tests of the
// names (by a script of verdicts per name), the router module and the
// notifications.
type rig struct {
	mu      sync.Mutex
	now     time.Time
	verdict map[string]Verdict
	tested  []string
	downs   []Outage
	backs   []Outage
	quiet   []string
	asked   int
	upnpOn  bool
	plan    Plan
	primary []*NameState
}

func newRig(t *testing.T, names ...string) *rig {
	t.Helper()
	r := &rig{now: time.Date(2026, 11, 15, 9, 0, 0, 0, time.Local), verdict: map[string]Verdict{}}
	r.plan = Plan{Interval: 5 * time.Minute, Port: 2053}
	for i, name := range names {
		r.plan.Names = append(r.plan.Names, candidate{host: name, panel: i == 0})
		r.verdict[name] = Verdict{Status: StatusOK, Cause: "seen"}
	}
	saved := struct {
		clock      func() time.Time
		tester     func(context.Context, string, int) Result
		upnpStatus func() upnp.Status
		upnpCheck  func() upnp.Status
		jitter     func(time.Duration) time.Duration
		plan       func() Plan
		down       func(Outage, *NameState, Plan, time.Time)
		back       func(Outage, Plan)
		quiet      func(string)
		store      func() string
	}{clock, tester, upnpStatus, upnpCheck, jitter, currentPlan, notifyDown, notifyRestored, closeQuietly, storePath}
	w = newWatcher()
	clock = func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now }
	jitter = func(d time.Duration) time.Duration { return d }
	currentPlan = func() Plan { r.mu.Lock(); defer r.mu.Unlock(); return r.plan }
	tester = func(_ context.Context, host string, port int) Result {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.tested = append(r.tested, host)
		v := r.verdict[host]
		return Result{Verdict: v, Raw: "GET https://" + host + ":" + strconv.Itoa(port) + "/_health: answer " + v.Code}
	}
	held := func() upnp.Status {
		r.mu.Lock()
		defer r.mu.Unlock()
		return upnp.Status{Enabled: r.upnpOn, Mappings: []upnp.Mapping{{Name: "HTTPS", InternalPort: 2053, ExternalPort: 2053, WantedPort: 2053, Active: true}}}
	}
	upnpStatus = held
	upnpCheck = func() upnp.Status { r.mu.Lock(); r.asked++; r.mu.Unlock(); return held() }
	notifyDown = func(o Outage, p *NameState, _ Plan, _ time.Time) {
		r.downs = append(r.downs, o)
		copy := *p
		r.primary = append(r.primary, &copy)
	}
	notifyRestored = func(o Outage, _ Plan) { r.backs = append(r.backs, o) }
	closeQuietly = func(thread string) { r.quiet = append(r.quiet, thread) }
	dir := t.TempDir()
	storePath = func() string { return filepath.Join(dir, "reach-watch.json") }
	t.Cleanup(func() {
		clock, tester, upnpStatus, upnpCheck, jitter = saved.clock, saved.tester, saved.upnpStatus, saved.upnpCheck, saved.jitter
		currentPlan, notifyDown, notifyRestored, closeQuietly, storePath = saved.plan, saved.down, saved.back, saved.quiet, saved.store
		w = newWatcher()
	})
	return r
}

func (r *rig) advance(d time.Duration) {
	r.mu.Lock()
	r.now = r.now.Add(d)
	r.mu.Unlock()
	w.step(false)
}

func (r *rig) set(host string, status, cause, code string) {
	r.mu.Lock()
	r.verdict[host] = Verdict{Status: status, Cause: cause, Code: code}
	r.mu.Unlock()
}

func (r *rig) testedNow() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.tested
	r.tested = nil
	return out
}

func state(host string) NameState {
	w.mu.Lock()
	defer w.mu.Unlock()
	if st, ok := w.names[host]; ok {
		return *st
	}
	return NameState{}
}

// start: the first round comes a minute after the start, then every
// interval.
func (r *rig) start() {
	w.step(false)
	r.advance(time.Minute)
}

func TestTheFirstRoundWaitsAMinute(t *testing.T) {
	r := newRig(t, "example.org")
	w.step(false)
	if got := r.testedNow(); len(got) != 0 {
		t.Fatalf("tested at once: %v", got)
	}
	r.advance(59 * time.Second)
	if got := r.testedNow(); len(got) != 0 {
		t.Fatalf("tested too early: %v", got)
	}
	r.advance(time.Second)
	if got := r.testedNow(); len(got) != 1 || state("example.org").State != StateReachable {
		t.Fatalf("not tested after a minute: %v %+v", got, state("example.org"))
	}
}

func TestOneFailureIsNotAnAlarm(t *testing.T) {
	r := newRig(t, "example.org")
	r.start()
	r.set("example.org", StatusProblem, "connect", "522")
	r.advance(5 * time.Minute)
	if st := state("example.org"); st.State != StateSuspect || st.Fails != 1 || len(r.downs) != 0 {
		t.Fatalf("after one failure: %+v, %d notifications", st, len(r.downs))
	}
	r.testedNow()
	r.advance(29 * time.Second)
	if got := r.testedNow(); len(got) != 0 {
		t.Fatalf("the second try came too early: %v", got)
	}
	r.advance(time.Second) // 30 s after the first
	if st := state("example.org"); st.State != StateSuspect || st.Fails != 2 || len(r.downs) != 0 {
		t.Fatalf("after two failures: %+v, %d notifications", st, len(r.downs))
	}
	r.advance(time.Minute) // the third try
	st := state("example.org")
	if st.State != StateDown || len(r.downs) != 1 {
		t.Fatalf("after three failures: %+v, %d notifications", st, len(r.downs))
	}
	o := r.downs[0]
	if o.Cause != "connect" || o.Code != "522" || len(o.Names) != 1 || o.Names[0] != "example.org" {
		t.Errorf("outage: %+v", o)
	}
	if !o.Since.Equal(st.Since) || st.Since.IsZero() {
		t.Errorf("the outage starts at the first failure: %v / %v", o.Since, st.Since)
	}
	if o.Thread != "reach:connect" {
		t.Errorf("thread %q", o.Thread)
	}
}

func TestAFailureThatHealsIsNotTold(t *testing.T) {
	r := newRig(t, "example.org")
	r.start()
	r.set("example.org", StatusProblem, "connect", "522")
	r.advance(5 * time.Minute)
	r.set("example.org", StatusOK, "seen", "")
	r.advance(30 * time.Second)
	if st := state("example.org"); st.State != StateReachable || st.Fails != 0 || len(r.downs)+len(r.backs) != 0 {
		t.Fatalf("%+v, %d %d", st, len(r.downs), len(r.backs))
	}
}

func TestUnknownIsNeitherUpNorDown(t *testing.T) {
	r := newRig(t, "example.org")
	r.start()
	r.set("example.org", StatusProblem, "connect", "522")
	r.advance(5 * time.Minute)
	// the server lost its own internet: nothing can be concluded, and the
	// count starts again
	r.set("example.org", StatusUnknown, "offline", "")
	for i := 0; i < 20; i++ {
		r.advance(5 * time.Minute)
	}
	if st := state("example.org"); st.State != StateUnknown || st.Fails != 0 || st.Cause != "offline" {
		t.Fatalf("%+v", st)
	}
	if len(r.downs) != 0 {
		t.Fatalf("an unknown made an alarm: %+v", r.downs)
	}
	// one failure after that is one, not the third
	r.set("example.org", StatusProblem, "connect", "522")
	r.advance(5 * time.Minute)
	if st := state("example.org"); st.State != StateSuspect || st.Fails != 1 {
		t.Fatalf("%+v", st)
	}
}

func down(r *rig, host string, cause, code string) {
	r.set(host, StatusProblem, cause, code)
	r.advance(5 * time.Minute)
	r.advance(30 * time.Second)
	r.advance(time.Minute)
}

func TestUnknownWhileDownKeepsTheOutage(t *testing.T) {
	r := newRig(t, "example.org")
	r.start()
	down(r, "example.org", "connect", "522")
	r.set("example.org", StatusUnknown, "offline", "")
	for i := 0; i < 10; i++ {
		r.advance(10 * time.Minute)
	}
	if st := state("example.org"); st.State != StateDown || st.Note != "offline" {
		t.Fatalf("%+v", st)
	}
	if len(r.backs) != 0 || w.outage == nil {
		t.Fatal("an unknown closed the outage")
	}
}

func TestBackIsToldWithHowLongItLasted(t *testing.T) {
	r := newRig(t, "example.org", "app.example.org")
	r.start()
	r.set("example.org", StatusProblem, "connect", "522")
	r.set("app.example.org", StatusProblem, "connect", "522")
	first := r.now.Add(5 * time.Minute)
	r.advance(5 * time.Minute)
	r.advance(30 * time.Second)
	r.advance(time.Minute)
	if len(r.downs) != 1 || len(r.downs[0].Names) != 2 || r.downs[0].Names[0] != "example.org" {
		t.Fatalf("one outage for both names, the panel first: %+v", r.downs)
	}
	r.set("example.org", StatusOK, "seen", "")
	r.advance(10 * time.Minute)
	if len(r.backs) != 0 {
		t.Fatal("back while a name is still down")
	}
	r.set("app.example.org", StatusOK, "seen", "")
	for i := 0; i < 5 && len(r.backs) == 0; i++ {
		r.advance(10 * time.Minute)
	}
	if len(r.backs) != 1 {
		t.Fatalf("back not told: %+v", r.backs)
	}
	o := r.backs[0]
	if o.End == nil || !o.Since.Equal(first) {
		t.Fatalf("outage %+v, since want %v", o, first)
	}
	n := RestoredNotification(o, r.plan, *o.End)
	what := utils.RenderNotificationText(*n.Fork.What)
	want := utils.NotificationDuration(o.End.Sub(o.Since))
	if !strings.Contains(what, want) || n.Fork.Resolves != "reach:connect" || n.Level != "success" {
		t.Errorf("follow-up: %q (want %q), %+v", what, want, n.Fork)
	}
	if len(w.history) != 1 || w.history[0].End == nil {
		t.Errorf("history: %+v", w.history)
	}
}

func TestACauseChangeIsToldAgain(t *testing.T) {
	r := newRig(t, "example.org")
	r.start()
	down(r, "example.org", "connect", "522")
	r.set("example.org", StatusProblem, "tlsCertificate", "526")
	r.advance(time.Minute)
	if len(r.downs) != 2 || r.downs[1].Cause != "tlsCertificate" || r.downs[1].Thread != "reach:tlsCertificate" {
		t.Fatalf("%+v", r.downs)
	}
	if len(r.quiet) != 1 || r.quiet[0] != "reach:connect" {
		t.Fatalf("the old thread was not closed quietly: %v", r.quiet)
	}
	if len(r.backs) != 0 {
		t.Fatal("a cause change is not a recovery")
	}
	if !r.downs[1].Since.Equal(r.downs[0].Since) {
		t.Error("the outage keeps its start")
	}
}

func TestARoundIsBounded(t *testing.T) {
	names := []string{"example.org"}
	for i := 0; i < 20; i++ {
		names = append(names, "app"+strconv.Itoa(i)+".example.org")
	}
	r := newRig(t, names...)
	r.start()
	seen := map[string]int{}
	for round := 0; round < 3; round++ {
		got := r.testedNow()
		if len(got) > perRound {
			t.Fatalf("round %d tested %d names", round, len(got))
		}
		if !inList("example.org", got) {
			t.Fatalf("the panel is not in every round: %v", got)
		}
		for _, host := range got {
			seen[host]++
		}
		r.advance(5 * time.Minute)
	}
	if len(seen) != len(names) {
		t.Fatalf("the rotation left names out: %d of %d", len(seen), len(names))
	}
}

func TestPlanBounds(t *testing.T) {
	config := utils.Config{}
	config.HTTPConfig.Hostname = "example.org"
	config.HTTPConfig.HTTPSPort = "2053"
	config.HTTPConfig.Cloudflare.Shielded = []string{"example.org"}
	for i := 0; i < 60; i++ {
		host := "app" + strconv.Itoa(i) + ".example.org"
		config.HTTPConfig.Cloudflare.Shielded = append(config.HTTPConfig.Cloudflare.Shielded, host)
		config.HTTPConfig.ProxyConfig.Routes = append(config.HTTPConfig.ProxyConfig.Routes,
			utils.ProxyRouteConfig{Name: host, Mode: "SERVAPP", UseHost: true, Host: host})
	}
	config.HTTPConfig.ProxyConfig.Routes = append(config.HTTPConfig.ProxyConfig.Routes,
		utils.ProxyRouteConfig{Name: "direct", Mode: "SERVAPP", UseHost: true, Host: "direct.example.org"},
		utils.ProxyRouteConfig{Name: "own port", Mode: "SERVAPP", UseHost: true, Host: "app0.example.org:7201"},
		utils.ProxyRouteConfig{Name: "local", Mode: "SERVAPP", UseHost: true, Host: "nas.local"},
		utils.ProxyRouteConfig{Name: "ip", Mode: "SERVAPP", UseHost: true, Host: "192.0.2.10"},
		utils.ProxyRouteConfig{Name: "off", Mode: "SERVAPP", UseHost: true, Host: "off.example.org", Disabled: true})
	p := planFor(config, true)
	if len(p.Names) != maxWatched || len(p.TooMany) != 61-maxWatched || !p.Names[0].panel || p.Names[0].host != "example.org" {
		t.Fatalf("names %d, too many %d, first %+v", len(p.Names), len(p.TooMany), p.Names[0])
	}
	if len(p.Direct) != 1 || p.Direct[0] != "direct.example.org" {
		t.Fatalf("direct %v", p.Direct)
	}
	if p.Interval != 5*time.Minute || p.Port != 2053 || p.Reason != "" {
		t.Fatalf("%+v", p)
	}
	if q := planFor(config, false); len(q.Names) != 0 || q.Reason != "noHTTPS" {
		t.Fatalf("without HTTPS: %+v", q.Reason)
	}
	config.HTTPConfig.HTTPSPort = "8444"
	if q := planFor(config, true); len(q.Names) != 0 || q.Reason != "port" {
		t.Fatalf("a port Cloudflare does not proxy: %+v", q.Reason)
	}
	config.HTTPConfig.HTTPSPort = "2053"
	for _, c := range []struct{ minutes, want int }{{0, 5}, {1, 2}, {2, 2}, {30, 30}, {90, 60}} {
		config.HTTPConfig.Cloudflare.Watch.Minutes = c.minutes
		if got := planFor(config, true).Interval; got != time.Duration(c.want)*time.Minute {
			t.Errorf("minutes %d: interval %v", c.minutes, got)
		}
	}
	config.HTTPConfig.Cloudflare.Watch.Off = true
	if !planFor(config, true).Off {
		t.Error("off")
	}
}

func TestNothingIsSentWhenOffOrNothingToTest(t *testing.T) {
	r := newRig(t, "example.org")
	r.plan.Off = true
	for i := 0; i < 10; i++ {
		r.advance(5 * time.Minute)
	}
	r.plan = Plan{Interval: 5 * time.Minute, Port: 2053}
	for i := 0; i < 10; i++ {
		r.advance(5 * time.Minute)
	}
	if got := r.testedNow(); len(got) != 0 {
		t.Fatalf("tested: %v", got)
	}
}

func TestBackOffWhileDown(t *testing.T) {
	r := newRig(t, "example.org")
	r.start()
	down(r, "example.org", "connect", "522")
	gaps := []time.Duration{}
	last := r.now
	r.testedNow()
	for len(gaps) < 6 {
		r.advance(10 * time.Second)
		if len(r.testedNow()) > 0 {
			gaps = append(gaps, r.now.Sub(last).Round(10*time.Second))
			last = r.now
		}
	}
	want := []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 10 * time.Minute, 10 * time.Minute}
	for i := range want {
		if gaps[i] != want[i] {
			t.Fatalf("gaps %v, want %v", gaps, want)
		}
	}
}

func TestJitterStaysWithinATenth(t *testing.T) {
	for i := 0; i < 1000; i++ {
		d := jitter(5 * time.Minute)
		if d < 270*time.Second || d > 330*time.Second {
			t.Fatalf("jitter %v", d)
		}
	}
}

func TestTheRouterModuleIsAskedOnTheWayToTheServer(t *testing.T) {
	r := newRig(t, "example.org")
	r.upnpOn = true
	r.start()
	r.set("example.org", StatusProblem, "connect", "522")
	r.advance(5 * time.Minute)
	if r.asked != 1 {
		t.Fatalf("asked %d times at the first failure", r.asked)
	}
	r.advance(30 * time.Second)
	r.advance(time.Minute)
	if r.asked != 1 {
		t.Fatalf("asked again within five minutes: %d", r.asked)
	}
	if len(r.downs) != 1 || !r.downs[0].Asked || r.downs[0].Router == "" {
		t.Fatalf("the notification does not say the router was asked: %+v", r.downs)
	}
	n := DownNotification(r.downs[0], r.primary[0], r.plan, r.now.Add(time.Minute), r.now)
	what := utils.RenderNotificationText(*n.Fork.What)
	for _, part := range []string{"Since 09:06", "522", "port 2053", "asked the router"} {
		if !strings.Contains(what, part) {
			t.Errorf("the sentence Telegram tells lacks %q: %s", part, what)
		}
	}

	// not for a certificate, not without UPnP
	r2 := newRig(t, "example.org")
	r2.upnpOn = true
	r2.start()
	down(r2, "example.org", "tlsCertificate", "526")
	if r2.asked != 0 {
		t.Fatal("asked for a certificate problem")
	}
	r3 := newRig(t, "example.org")
	r3.start()
	down(r3, "example.org", "connect", "522")
	if r3.asked != 0 {
		t.Fatal("asked without automatic forwarding")
	}
	n = DownNotification(r3.downs[0], r3.primary[0], r3.plan, r3.now, r3.now)
	if what := utils.RenderNotificationText(*n.Fork.What); strings.Contains(what, "asked the router") || !strings.Contains(what, "keeps testing") {
		t.Errorf("without UPnP: %s", what)
	}
}

func (r *rig) waitTested(n int) []string {
	for i := 0; i < 200; i++ {
		r.mu.Lock()
		got := len(r.tested)
		r.mu.Unlock()
		if got >= n {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	w.run.Lock() // the round has finished
	w.run.Unlock()
	return r.testedNow()
}

func TestCheckNowIsBounded(t *testing.T) {
	r := newRig(t, "example.org")
	r.start()
	r.testedNow()
	if !w.forceRound() {
		t.Fatal("refused")
	}
	if got := r.waitTested(1); len(got) != 1 {
		t.Fatalf("the forced round tested %v", got)
	}
	if w.forceRound() {
		t.Fatal("a second forced round within 15 s")
	}
	r.mu.Lock()
	r.now = r.now.Add(16 * time.Second)
	r.mu.Unlock()
	if !w.forceRound() {
		t.Fatal("refused after 16 s")
	}
	if got := r.waitTested(1); len(got) != 1 {
		t.Fatalf("the forced round tested %v", got)
	}
}

func TestAnOutageSurvivesARestart(t *testing.T) {
	r := newRig(t, "example.org")
	r.start()
	down(r, "example.org", "connect", "522")
	since := r.downs[0].Since
	// a new process
	w = newWatcher()
	w.load()
	if w.outage == nil || !w.outage.Since.Equal(since) {
		t.Fatalf("outage after a restart: %+v", w.outage)
	}
	if st := state("example.org"); st.State != StateDown {
		t.Fatalf("the name is not down after a restart: %+v", st)
	}
	r.set("example.org", StatusOK, "seen", "")
	r.advance(time.Minute)
	r.advance(time.Minute)
	if len(r.backs) != 1 || !r.backs[0].Since.Equal(since) {
		t.Fatalf("back after a restart: %+v", r.backs)
	}
}

func TestStatusAndOutside(t *testing.T) {
	r := newRig(t, "example.org", "app.example.org")
	r.plan.Direct = []string{"direct.example.org"}
	r.start()
	st := GetStatus()
	if !st.Enabled || len(st.Names) != 2 || !st.Names[0].Panel || st.Names[0].State != StateReachable || len(st.Direct) != 1 {
		t.Fatalf("%+v", st)
	}
	if o := Outside("example.org"); !o.Watched || o.State != StateReachable || o.SeenAt.IsZero() {
		t.Errorf("%+v", o)
	}
	if o := Outside("direct.example.org"); !o.Direct || o.Watched {
		t.Errorf("%+v", o)
	}
	if o := Outside("other.example.org"); o.Watched || o.Direct {
		t.Errorf("%+v", o)
	}
}

// TestAnOutageCarriesOnlyItsOwnRouterAnswer: the router module's answer to
// an earlier outage is not told with a later one.
func TestAnOutageCarriesOnlyItsOwnRouterAnswer(t *testing.T) {
	r := newRig(t, "example.org")
	r.upnpOn = true
	r.start()
	down(r, "example.org", "connect", "522")
	r.set("example.org", StatusOK, "seen", "")
	r.advance(2 * time.Minute)
	if len(r.backs) != 1 {
		t.Fatalf("not back: %+v", r.backs)
	}
	r.upnpOn = false
	r.advance(10 * time.Minute)
	down(r, "example.org", "dnsNotFound", "")
	last := r.downs[len(r.downs)-1]
	if last.Cause != "dnsNotFound" || last.Asked || last.Router != "" {
		t.Fatalf("the second outage: %+v", last)
	}
}
