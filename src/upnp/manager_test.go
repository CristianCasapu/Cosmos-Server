package upnp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// world is a server with a router, containers and a clock, none of them real.
type world struct {
	t          *testing.T
	router     *fakeRouter
	containers []Container
	dockerErr  error
	routerErr  error
	discovered int
	notices    []string
	now        time.Time
	mu         sync.Mutex
	events     []utils.SecurityEvent
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, router: newFakeRouter(t), now: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)}

	realDiscover, realNotify, realList, realUpdate, realClock := discoverGateway, notifyFailure, ListContainers, updateUPnPConfig, clock
	discoverGateway = func() (*gateway, error) {
		w.discovered++
		if w.routerErr != nil {
			return nil, w.routerErr
		}
		return w.router.gateway(t), nil
	}
	notifyFailure = func(reason string) { w.notices = append(w.notices, reason) }
	ListContainers = func() ([]Container, error) { return w.containers, w.dockerErr }
	updateUPnPConfig = func(change func(*utils.UPnPConfig)) {
		config := utils.GetBaseMainConfig()
		change(&config.HTTPConfig.UPnP)
		utils.LoadBaseMainConfig(config)
	}
	clock = func() time.Time { return w.now }
	utils.SetSecurityRecorder(func(event utils.SecurityEvent) {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.events = append(w.events, event)
	}, func() {})

	reset := func() {
		runMu.Lock()
		held, failed, missing = map[string]heldMapping{}, map[string]string{}, map[string]time.Time{}
		paused, lastPlan, lastConfigErr, lastRouterTalk = false, "", "", time.Time{}
		runMu.Unlock()
		statusMu.Lock()
		status = Status{Mappings: []Mapping{}, Apps: []Mapping{}, Foreign: []Foreign{}}
		statusMu.Unlock()
	}
	reset()
	t.Cleanup(func() {
		discoverGateway, notifyFailure, ListContainers, updateUPnPConfig, clock = realDiscover, realNotify, realList, realUpdate, realClock
		utils.SetSecurityRecorder(nil, nil)
		reset()
		utils.LoadBaseMainConfig(utils.Config{})
	})

	config := utils.Config{}
	config.HTTPConfig.HTTPPort, config.HTTPConfig.HTTPSPort = "2052", "2053"
	config.HTTPConfig.HTTPSCertificateMode = utils.HTTPSCertModeList["SELFSIGNED"]
	utils.LoadBaseMainConfig(config)
	return w
}

func (w *world) config(change func(*utils.Config)) {
	config := utils.GetBaseMainConfig()
	change(&config)
	utils.LoadBaseMainConfig(config)
}

// restart is Cosmos starting again: what it held is only on the router.
func (w *world) restart() {
	runMu.Lock()
	held, failed, missing, paused, lastPlan = map[string]heldMapping{}, map[string]string{}, map[string]time.Time{}, false, ""
	runMu.Unlock()
}

func (w *world) after(d time.Duration) { w.now = w.now.Add(d) }

// minutes lets the periodic check run once a minute for a while.
func (w *world) minutes(n int) {
	for i := 0; i < n; i++ {
		w.after(time.Minute)
		tick()
	}
}

func (w *world) kinds() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	kinds := []string{}
	for _, event := range w.events {
		text := event.Kind + " " + event.Target
		if event.Details["external"] != "" {
			text += " " + event.Details["external"]
		}
		if event.Details["reason"] != "" {
			text += " " + event.Details["reason"]
		}
		kinds = append(kinds, text)
	}
	w.events = nil
	return kinds
}

func container(name string, running bool, ports ...Published) Container {
	return Container{Name: name, Running: running, Ports: ports}
}

func tcp(containerPort int, hostPort int) Published {
	return Published{ContainerPort: containerPort, Protocol: "tcp", HostPort: hostPort}
}

func udp(containerPort int, hostPort int) Published {
	return Published{ContainerPort: containerPort, Protocol: "udp", HostPort: hostPort}
}

func choose(container string, port int, protocol string) utils.UPnPAppPort {
	return utils.UPnPAppPort{Container: container, Port: port, Protocol: protocol}
}

func (w *world) choices(list ...utils.UPnPAppPort) {
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.AppPorts = list })
}

func app(st Status, name string) Mapping {
	for _, m := range st.Apps {
		if m.Name == name {
			return m
		}
	}
	return Mapping{}
}

func own(st Status, name string) Mapping {
	for _, m := range st.Mappings {
		if m.Name == name {
			return m
		}
	}
	return Mapping{}
}

func TestAppPortIsForwardedRenewedAndWithdrawn(t *testing.T) {
	w := newWorld(t)
	w.containers = []Container{container("jellyfin", true, tcp(8096, 8096), udp(7359, 7359))}
	w.choices(choose("jellyfin", 8096, "tcp"), choose("jellyfin", 7359, "udp"))

	st := reconcile(true)
	m := app(st, "jellyfin 8096/tcp")
	if !m.Active || m.State != StateActive || m.ExternalPort != 8096 || m.InternalPort != 8096 || m.Kind != "app" || m.Since == nil {
		t.Fatalf("mapping = %+v", m)
	}
	if got, _ := w.router.get("TCP/8096"); got.client != serverIP || got.port != "8096" || got.lease != "3600" || got.description != "Cosmos app jellyfin" {
		t.Errorf("router has %+v", got)
	}
	if got, _ := w.router.get("UDP/7359"); got.client != serverIP || got.port != "7359" {
		t.Errorf("router has %+v for the UDP port", got)
	}
	if st.Enabled || len(st.Mappings) != 0 || st.Paused || st.ExternalIP != "203.0.113.7" {
		t.Errorf("Cosmos' own ports were not asked for: %+v", st)
	}
	if got := strings.Join(w.kinds(), "; "); got != "upnp.mapping.created jellyfin 8096; upnp.mapping.created jellyfin 7359" {
		t.Errorf("events: %s", got)
	}

	// nothing changes: the router is left in peace until a renewal is due
	asked := w.router.count("AddPortMapping", "")
	w.minutes(19)
	if w.router.count("AddPortMapping", "") != asked {
		t.Error("the router was asked again before a renewal was due")
	}
	w.minutes(2)
	if w.router.count("AddPortMapping", "TCP/8096") != 2 || len(w.kinds()) != 0 {
		t.Errorf("renewal: asked %d times, events %v", w.router.count("AddPortMapping", "TCP/8096"), w.kinds())
	}
	if since := app(GetStatus(), "jellyfin 8096/tcp").Since; since == nil || !since.Equal(*m.Since) {
		t.Error("a renewal changed since when the port is forwarded")
	}

	// stopped: withdrawn within a minute, the choice stays
	w.containers = []Container{container("jellyfin", false)}
	w.minutes(1)
	if _, still := w.router.get("TCP/8096"); still {
		t.Error("the mapping of a stopped container was left on the router")
	}
	if m := app(GetStatus(), "jellyfin 8096/tcp"); m.Active || m.State != StateWaiting || m.Code != "stopped" {
		t.Errorf("stopped: %+v", m)
	}
	if got := strings.Join(w.kinds(), "; "); got != "upnp.mapping.removed jellyfin 7359 stopped; upnp.mapping.removed jellyfin 8096 stopped" {
		t.Errorf("events: %s", got)
	}
	w.minutes(60)
	if len(loadChoices()) != 2 || GetStatus().Paused {
		t.Errorf("a stopped container lost its choice or paused the forwarding: %d choices", len(loadChoices()))
	}

	// started again, on another host port: the mapping follows
	w.containers = []Container{container("jellyfin", true, tcp(8096, 18096), udp(7359, 7359))}
	w.minutes(1)
	if got, _ := w.router.get("TCP/18096"); got.port != "18096" {
		t.Errorf("after the host port changed the router has %+v", got)
	}
	w.containers = []Container{container("jellyfin", true, tcp(8096, 18097), udp(7359, 7359))}
	w.minutes(1)
	if _, old := w.router.get("TCP/18096"); old {
		t.Error("the mapping of the old host port was left on the router")
	}
	if got, _ := w.router.get("TCP/18097"); got.port != "18097" {
		t.Errorf("the mapping did not follow the host port: %+v", got)
	}
	w.kinds()

	// the port is no longer published: withdrawn at once, forgotten after a while
	w.containers = []Container{container("jellyfin", true, udp(7359, 7359))}
	w.minutes(1)
	if _, still := w.router.get("TCP/18097"); still {
		t.Error("a port that is no longer published stayed open")
	}
	if len(loadChoices()) != 2 {
		t.Error("the choice was forgotten at once")
	}
	w.minutes(16)
	if got := loadChoices(); len(got) != 1 || got[0].Port != 7359 {
		t.Errorf("choices after the port was gone for 16 minutes: %+v", got)
	}

	// the container is removed: the same
	w.containers = nil
	w.minutes(1)
	if _, still := w.router.get("UDP/7359"); still {
		t.Error("the mapping of a removed container stayed open")
	}
	w.minutes(16)
	if len(loadChoices()) != 0 || len(GetStatus().Apps) != 0 {
		t.Errorf("a removed container kept its choice: %+v", loadChoices())
	}
	// a new container of the same name does not inherit it
	w.containers = []Container{container("jellyfin", true, tcp(8096, 8096), udp(7359, 7359))}
	w.minutes(3)
	if len(w.router.keys()) != 0 {
		t.Errorf("a new container of the same name got ports opened: %v", w.router.keys())
	}
}

func TestARecreatedContainerKeepsItsPort(t *testing.T) {
	w := newWorld(t)
	w.containers = []Container{container("jellyfin", true, tcp(8096, 8096))}
	w.choices(choose("jellyfin", 8096, "tcp"))
	reconcile(true)

	// re-created outside Cosmos: gone for a few minutes
	w.containers = []Container{container("jellyfin-cosmos-old-abc", false)}
	w.minutes(5)
	if _, still := w.router.get("TCP/8096"); still {
		t.Error("the port stayed open while the container was gone")
	}
	w.containers = []Container{container("jellyfin", true, tcp(8096, 8096))}
	w.minutes(1)
	if got, ok := w.router.get("TCP/8096"); !ok || got.port != "8096" || len(loadChoices()) != 1 {
		t.Errorf("after the re-creation: %+v, %d choices", got, len(loadChoices()))
	}
}

// The wanted external port is taken: the next free one, and the same one
// from then on.
func TestATakenPortGivesAStableAlternative(t *testing.T) {
	w := newWorld(t)
	w.router.put("TCP/8096", "192.168.1.99", 8096, "Media box")
	w.router.put("TCP/8097", serverIP, 9999, "another program here")
	w.router.hidden["TCP/8098"] = 718 // forwarded by hand, not shown
	w.containers = []Container{container("jellyfin", true, tcp(8096, 8096))}
	w.choices(choose("jellyfin", 8096, "tcp"))

	st := reconcile(true)
	m := app(st, "jellyfin 8096/tcp")
	if !m.Active || m.ExternalPort != 8099 || m.WantedPort != 8096 || m.Code != "moved" || !strings.Contains(m.Holder, "192.168.1.99") || !strings.Contains(m.Holder, "Media box") {
		t.Fatalf("mapping = %+v", m)
	}
	if got := loadChoices()[0].AssignedPort; got != 8099 {
		t.Errorf("the port it got was not remembered: %d", got)
	}
	events := w.kinds()
	if len(events) != 1 || events[0] != "upnp.mapping.created jellyfin 8099" {
		t.Errorf("events: %v", events)
	}
	// nothing of anyone else's was touched
	if got, _ := w.router.get("TCP/8096"); got.client != "192.168.1.99" || got.description != "Media box" {
		t.Errorf("the other device's mapping: %+v", got)
	}
	if got, _ := w.router.get("TCP/8097"); got.port != "9999" || got.description != "another program here" {
		t.Errorf("the other program's mapping: %+v", got)
	}
	if w.router.count("AddPortMapping", "TCP/8096")+w.router.count("AddPortMapping", "TCP/8097") != 0 {
		t.Error("the router was asked for a port it showed as taken")
	}
	// they are listed, read only
	if len(st.Foreign) != 2 || st.Foreign[0].ExternalPort != 8096 || st.Foreign[0].ThisServer || !st.Foreign[1].ThisServer || !st.ForeignListed {
		t.Errorf("foreign = %+v", st.Foreign)
	}

	// the wanted port becomes free: the address that was handed out stays
	w.router.expire("TCP/8096")
	w.minutes(21)
	if m := app(GetStatus(), "jellyfin 8096/tcp"); m.ExternalPort != 8099 || !m.Active {
		t.Errorf("after a renewal: %+v", m)
	}
	// Cosmos restarts
	w.restart()
	if m := app(reconcile(true), "jellyfin 8096/tcp"); m.ExternalPort != 8099 || !m.Active {
		t.Errorf("after a restart: %+v", m)
	}
	// the router reboots and loses everything
	w.router.reboot()
	w.minutes(21)
	if m := app(GetStatus(), "jellyfin 8096/tcp"); m.ExternalPort != 8099 || !m.Active {
		t.Errorf("after the router's reboot: %+v", m)
	}
	if _, wanted := w.router.get("TCP/8096"); wanted {
		t.Error("the wanted port was taken behind the admin's back")
	}
	// while Cosmos was not looking, something else took the port it had
	w.router.expire("TCP/8099")
	w.router.put("TCP/8099", "192.168.1.77", 1, "console")
	w.kinds()
	w.minutes(21)
	m = app(GetStatus(), "jellyfin 8096/tcp")
	if !m.Active || m.ExternalPort != 8096 {
		t.Errorf("after its port was taken: %+v", m)
	}
	if got, _ := w.router.get("TCP/8099"); got.client != "192.168.1.77" {
		t.Errorf("the console's mapping was touched: %+v", got)
	}

	// switched off and on again starts from the wanted port; every candidate taken: it fails
	for port := 8096; port <= 8105; port++ {
		w.router.put("TCP/"+strconv.Itoa(port), "192.168.1.99", port, "busy")
	}
	w.restart()
	w.choices(choose("jellyfin", 8096, "tcp"))
	st = reconcile(true)
	m = app(st, "jellyfin 8096/tcp")
	if m.Active || m.State != StateFailed || m.Code != "conflict" || !m.Paused || m.Holder == "" {
		t.Errorf("every candidate taken: %+v", m)
	}
	if w.router.count("AddPortMapping", "TCP/8106") != 0 {
		t.Error("more than ten ports were tried")
	}
}

func TestReservedPortsAreNeverAskedFor(t *testing.T) {
	w := newWorld(t)
	w.containers = []Container{container("web", true, tcp(80, 80), tcp(443, 441)), container("mail", true, tcp(25, 25))}
	w.choices(choose("web", 80, "tcp"), utils.UPnPAppPort{Container: "web", Port: 443, Protocol: "tcp"}, choose("mail", 25, "tcp"))
	w.router.put("TCP/442", "192.168.1.99", 442, "x")
	w.config(func(c *utils.Config) {
		c.HTTPConfig.UPnP.Enabled = true
		c.HTTPConfig.UPnP.ExternalHTTPSPort = "443"
	})

	st := reconcile(true)
	for _, key := range []string{"TCP/80", "TCP/443", "TCP/25"} {
		if w.router.count("AddPortMapping", key)+w.router.count("GetSpecificPortMappingEntry", key) != 0 {
			t.Errorf("the router was asked about %s", key)
		}
	}
	https := own(st, "HTTPS")
	if https.Active || https.Code != "reserved" || https.State != StateFailed || https.Suggested != 2053 {
		t.Errorf("Cosmos' own port on a reserved one: %+v", https)
	}
	if m := app(st, "web 80/tcp"); !m.Active || m.ExternalPort != 81 || m.Code != "movedReserved" {
		t.Errorf("an app's port 80: %+v", m)
	}
	// 441 is wanted (the host port), 442 is taken, 443 reserved: 444
	w.router.put("TCP/441", "192.168.1.99", 441, "x")
	w.restart()
	w.choices(choose("web", 443, "tcp"))
	st = reconcile(true)
	if m := app(st, "web 443/tcp"); !m.Active || m.ExternalPort != 444 {
		t.Errorf("an alternative skipped the reserved port: %+v", m)
	}

	// the list is a setting
	set, list := reservedPorts(" 8080, 9000 ;x 70000")
	if len(list) != 2 || !set[8080] || !set[9000] || set[443] {
		t.Errorf("reservedPorts = %v", list)
	}
	if _, list := reservedPorts("none"); len(list) != 0 {
		t.Errorf("none = %v", list)
	}
	if set, _ := reservedPorts(""); !set[80] || !set[443] || !set[25] || !set[993] {
		t.Error("the default list misses a port")
	}
}

// One refused mapping is paused alone; the others go on being renewed.
func TestARefusedMappingIsPausedAlone(t *testing.T) {
	w := newWorld(t)
	w.router.put("TCP/2053", "192.168.1.99", 443, "NAS")
	w.router.refuse["UDP/27015"] = 606
	w.containers = []Container{container("game", true, udp(27015, 27015), tcp(27015, 27015))}
	w.choices(choose("game", 27015, "udp"), choose("game", 27015, "tcp"))
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.Enabled = true })

	st := reconcile(true)
	https := own(st, "HTTPS")
	if https.Active || !https.Paused || https.Code != "conflict" || !strings.Contains(https.Holder, "192.168.1.99") || !strings.Contains(https.Error, "already forwarded") {
		t.Fatalf("HTTPS = %+v", https)
	}
	if https.Suggested == 0 || https.Suggested == 80 || https.Suggested == 443 || https.Suggested == 2053 {
		t.Errorf("the port proposed for HTTPS: %d", https.Suggested)
	}
	if got, _ := w.router.get("TCP/2053"); got.client != "192.168.1.99" || w.router.count("AddPortMapping", "TCP/2053") != 0 {
		t.Errorf("the NAS' mapping was touched: %+v", got)
	}
	udpPort := app(st, "game 27015/udp")
	if udpPort.Active || udpPort.Code != "refused" || !udpPort.Paused || w.router.count("AddPortMapping", "UDP/27016") != 0 {
		t.Errorf("a refusal that is not a conflict: %+v", udpPort)
	}
	if m := app(st, "game 27015/tcp"); !m.Active {
		t.Errorf("the port the router accepts: %+v", m)
	}
	if st.Paused {
		t.Error("one refused mapping paused everything")
	}
	if len(w.notices) != 2 {
		t.Errorf("notices: %v", w.notices)
	}
	if ExternalHTTPSPort() != 2053 {
		t.Errorf("ExternalHTTPSPort = %d", ExternalHTTPSPort())
	}

	// by itself: the good one is renewed, the refused ones are not asked again
	w.minutes(41)
	if n := w.router.count("AddPortMapping", "TCP/27015"); n != 3 {
		t.Errorf("the working mapping was asked for %d times in 41 minutes, want 3", n)
	}
	if n := w.router.count("AddPortMapping", "UDP/27015"); n != 1 {
		t.Errorf("the refused mapping was asked for %d times", n)
	}
	if m := app(GetStatus(), "game 27015/udp"); !m.Paused || m.State != StateFailed || m.Error == "" {
		t.Errorf("the refused mapping lost its state: %+v", m)
	}
	if len(w.notices) != 2 {
		t.Errorf("told again: %v", w.notices)
	}

	// "Check now": everything is tried again
	delete(w.router.refuse, "UDP/27015")
	w.router.expire("TCP/2053")
	st = reconcile(true)
	if !own(st, "HTTPS").Active || !app(st, "game 27015/udp").Active || own(st, "HTTPS").Paused {
		t.Errorf("after Check now: %+v / %+v", own(st, "HTTPS"), app(st, "game 27015/udp"))
	}
}

// A router that cannot be reached is reported once and not asked again
// until someone asks for it.
func TestAnUnreachableRouterPausesEverything(t *testing.T) {
	w := newWorld(t)
	w.routerErr = errors.New("no UPnP router answered")
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.Enabled = true })

	st := reconcile(true)
	if !st.Paused || st.Error == "" || w.discovered != 1 || len(w.notices) != 1 {
		t.Fatalf("first failure: paused=%v error=%q attempts=%d notices=%d", st.Paused, st.Error, w.discovered, len(w.notices))
	}
	if !GetStatus().Paused || own(st, "HTTPS").Active {
		t.Error("the published status does not say that retries are paused")
	}
	w.minutes(90)
	if w.discovered != 1 {
		t.Errorf("the router was asked again by itself: %d", w.discovered)
	}

	// asked again by hand: tried again, but the admins are not told twice
	st = reconcile(true)
	if !st.Paused || w.discovered != 2 || len(w.notices) != 1 {
		t.Errorf("manual retry: paused=%v attempts=%d notices=%d", st.Paused, w.discovered, len(w.notices))
	}

	// switched off: nothing to do, nothing paused
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.Enabled = false })
	st = reconcile(true)
	if st.Paused || st.Error != "" || w.discovered != 2 {
		t.Errorf("after switching off: paused=%v error=%q attempts=%d", st.Paused, st.Error, w.discovered)
	}

	// back, with something held: a failure in between pauses, a manual retry heals
	w.routerErr = nil
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.Enabled = true })
	if st = reconcile(true); !own(st, "HTTPS").Active || st.Paused {
		t.Fatalf("with the router back: %+v", st)
	}
	w.routerErr = errors.New("gone")
	w.minutes(21)
	if !GetStatus().Paused || len(w.notices) != 2 {
		t.Errorf("a renewal that found no router: paused=%v notices=%d", GetStatus().Paused, len(w.notices))
	}
}

func TestCosmosOwnPortsAndLeftovers(t *testing.T) {
	w := newWorld(t)
	// left by an earlier run of Cosmos, wanted by nothing now
	w.router.put("TCP/9443", serverIP, 2053, "Cosmos HTTPS")
	w.router.put("TCP/7000", serverIP, 7000, "Cosmos app gone")
	w.router.put("TCP/7001", "192.168.1.60", 7001, "Cosmos app elsewhere") // another server's Cosmos
	w.config(func(c *utils.Config) {
		c.HTTPConfig.UPnP.Enabled = true
		c.HTTPConfig.UPnP.ExternalHTTPSPort = "8443"
		c.HTTPConfig.UPnP.ForwardHTTP = true
	})

	st := reconcile(true)
	if https := own(st, "HTTPS"); !https.Active || https.ExternalPort != 8443 || https.InternalPort != 2053 || https.Kind != "cosmos" {
		t.Fatalf("HTTPS = %+v", https)
	}
	if st.ExternalHTTPSPort != 8443 || st.ExternalHTTPPort != 2052 || st.HTTPSPort != 2053 || ExternalHTTPSPort() != 8443 {
		t.Errorf("external ports: %d / %d", st.ExternalHTTPSPort, st.ExternalHTTPPort)
	}
	if _, left := w.router.get("TCP/9443"); left {
		t.Error("Cosmos' own leftover stayed on the router")
	}
	if _, left := w.router.get("TCP/7000"); left {
		t.Error("a leftover app mapping stayed on the router")
	}
	if got, ok := w.router.get("TCP/7001"); !ok || got.client != "192.168.1.60" {
		t.Error("another machine's mapping was deleted because of its description")
	}
	if len(st.Foreign) != 1 || st.Foreign[0].ExternalPort != 7001 {
		t.Errorf("foreign = %+v", st.Foreign)
	}

	// an app never gets a port Cosmos itself uses or wants
	w.containers = []Container{container("proxy", true, tcp(443, 8443), tcp(80, 2051))}
	w.choices(choose("proxy", 443, "tcp"), choose("proxy", 80, "tcp"))
	st = reconcile(true)
	if m := app(st, "proxy 443/tcp"); !m.Active || m.ExternalPort != 8444 {
		t.Errorf("an app wanting Cosmos' external port: %+v", m)
	}
	// 2051 is free, 2052 and 2053 are Cosmos' own
	if m := app(st, "proxy 80/tcp"); !m.Active || m.ExternalPort != 2051 {
		t.Errorf("proxy 80: %+v", m)
	}
	if got, _ := w.router.get("TCP/8443"); got.port != "2053" {
		t.Errorf("Cosmos' HTTPS mapping: %+v", got)
	}

	// as configured, when nothing is mapped
	w.restart()
	statusMu.Lock()
	status = Status{}
	statusMu.Unlock()
	if ExternalHTTPSPort() != 8443 || ExternalHTTPPort() != 2052 {
		t.Errorf("as configured: %d / %d", ExternalHTTPSPort(), ExternalHTTPPort())
	}
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.Enabled = false })
	if ExternalHTTPSPort() != 2053 {
		t.Errorf("with forwarding off: %d", ExternalHTTPSPort())
	}
}

func TestPortsThatCannotBeForwarded(t *testing.T) {
	w := newWorld(t)
	w.containers = []Container{
		container("local", true, Published{ContainerPort: 80, Protocol: "tcp", HostPort: 8080, HostIP: "127.0.0.1"}),
		container("lan", true, Published{ContainerPort: 80, Protocol: "tcp", HostPort: 8081, HostIP: serverIP}),
		container("other", true, Published{ContainerPort: 80, Protocol: "tcp", HostPort: 8082, HostIP: "10.9.9.9"}),
		container("both", true, Published{ContainerPort: 80, Protocol: "tcp", HostPort: 8083, HostIP: "::"}, Published{ContainerPort: 80, Protocol: "tcp", HostPort: 8083, HostIP: "0.0.0.0"}),
		{Name: "cosmos", Running: true, IsCosmos: true, Ports: []Published{tcp(443, 443)}},
		container("panel", true, tcp(443, 2053)),
	}
	w.choices(choose("local", 80, "tcp"), choose("lan", 80, "tcp"), choose("other", 80, "tcp"), choose("both", 80, "tcp"), choose("cosmos", 443, "tcp"), choose("panel", 443, "tcp"))
	st := reconcile(true)
	want := map[string]string{"local 80/tcp": "localOnly", "other 80/tcp": "otherAddress", "cosmos 443/tcp": "cosmosPort", "panel 443/tcp": "cosmosPort"}
	for name, code := range want {
		if m := app(st, name); m.Active || m.Code != code {
			t.Errorf("%s: %+v, want code %s", name, m, code)
		}
	}
	if !app(st, "lan 80/tcp").Active || !app(st, "both 80/tcp").Active {
		t.Errorf("a port on the server's LAN address or on every address: %+v / %+v", app(st, "lan 80/tcp"), app(st, "both 80/tcp"))
	}
	if keys := strings.Join(w.router.keys(), " "); keys != "TCP/8081 TCP/8083" {
		t.Errorf("the router has %s", keys)
	}

	// Docker does not answer: what is held is kept, nothing is forgotten
	w.dockerErr = errors.New("docker is restarting")
	w.minutes(30)
	if keys := strings.Join(w.router.keys(), " "); keys != "TCP/8081 TCP/8083" || len(loadChoices()) != 6 {
		t.Errorf("while Docker was away: router %s, %d choices", keys, len(loadChoices()))
	}
}

// --- the routes

func call(t *testing.T, handler http.HandlerFunc, method string, body any, auth *utils.AuthContext) (int, map[string]any) {
	t.Helper()
	var raw []byte
	switch b := body.(type) {
	case string:
		raw = []byte(b)
	case nil:
	default:
		raw, _ = json.Marshal(b)
	}
	req := httptest.NewRequest(method, "https://cosmos.example/cosmos/api/upnp/apps", bytes.NewReader(raw))
	if auth != nil {
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	answer := map[string]any{}
	json.Unmarshal(rec.Body.Bytes(), &answer)
	return rec.Code, answer
}

var admin = &utils.AuthContext{Nickname: "admin", IsSudoed: true, Permissions: []utils.Permission{
	utils.PERM_LOGIN, utils.PERM_ADMIN, utils.PERM_ADMIN_READ, utils.PERM_CONFIGURATION, utils.PERM_CONFIGURATION_READ,
}}

func open(container string, port int, protocol string) appPortRequest {
	return appPortRequest{Container: container, Port: port, Protocol: protocol, Enabled: true, Confirm: fmt.Sprintf("%s:%d/%s", container, port, protocol)}
}

func TestAppPortRouteOpensAndCloses(t *testing.T) {
	w := newWorld(t)
	w.containers = []Container{container("jellyfin", true, tcp(8096, 8096))}
	w.router.put("UDP/1900", "192.168.1.20", 1900, "tv")

	request := open("jellyfin", 8096, "tcp")
	request.Scheme = "http"
	code, answer := call(t, AppPortRoute, "POST", request, admin)
	if code != http.StatusOK {
		t.Fatalf("open: %d %v", code, answer)
	}
	data := answer["data"].(map[string]any)
	apps := data["apps"].([]any)
	if len(apps) != 1 || apps[0].(map[string]any)["active"] != true || apps[0].(map[string]any)["scheme"] != "http" {
		t.Errorf("answer: %v", apps)
	}
	if len(data["foreign"].([]any)) != 1 {
		t.Errorf("an admin sees what else is open: %v", data["foreign"])
	}
	if got, ok := w.router.get("TCP/8096"); !ok || got.client != serverIP {
		t.Errorf("router: %+v", got)
	}
	choices := loadChoices()
	if len(choices) != 1 || choices[0].Scheme != "http" || choices[0].Since.IsZero() || choices[0].AssignedPort != 8096 {
		t.Errorf("choices: %+v", choices)
	}
	w.mu.Lock()
	events := append([]utils.SecurityEvent(nil), w.events...)
	w.mu.Unlock()
	if len(events) != 2 || events[0].Kind != utils.SecUPnPPortOpened || events[0].Actor != "admin" || events[0].Target != "jellyfin" || events[0].Details["port"] != "8096/tcp" ||
		events[1].Kind != utils.SecUPnPMappingCreated || events[1].Actor != "" || events[1].Details["external"] != "8096" {
		t.Errorf("events: %+v", events)
	}
	w.kinds()

	// someone who may read the settings sees the status, without the other devices
	reader := &utils.AuthContext{Nickname: "reader", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ}}
	code, answer = call(t, StatusRoute, "GET", nil, reader)
	if code != http.StatusOK || len(answer["data"].(map[string]any)["foreign"].([]any)) != 0 || len(answer["data"].(map[string]any)["apps"].([]any)) != 1 {
		t.Errorf("a reader: %d %v", code, answer)
	}

	// closing needs no confirmation, and works for a container that is gone
	w.containers = nil
	code, _ = call(t, AppPortRoute, "POST", appPortRequest{Container: "jellyfin", Port: 8096, Protocol: "TCP"}, admin)
	if code != http.StatusOK || len(loadChoices()) != 0 {
		t.Errorf("close: %d, %d choices", code, len(loadChoices()))
	}
	if _, still := w.router.get("TCP/8096"); still {
		t.Error("the port stayed open on the router")
	}
	if got := strings.Join(w.kinds(), "; "); got != "upnp.port.closed jellyfin; upnp.mapping.removed jellyfin 8096 off" {
		t.Errorf("events: %s", got)
	}
	if got, ok := w.router.get("UDP/1900"); !ok || got.client != "192.168.1.20" {
		t.Error("the TV's mapping was touched")
	}
}

func TestAppPortRouteRefuses(t *testing.T) {
	w := newWorld(t)
	w.containers = []Container{
		container("jellyfin", true, tcp(8096, 8096)),
		container("stopped", false),
		container("local", true, Published{ContainerPort: 80, Protocol: "tcp", HostPort: 8080, HostIP: "127.0.0.1"}),
		container("panel", true, tcp(443, 2053)),
		{Name: "cosmos", Running: true, IsCosmos: true, Ports: []Published{tcp(443, 8443)}},
	}
	with := func(request appPortRequest, change func(*appPortRequest)) appPortRequest {
		change(&request)
		return request
	}
	cases := []struct {
		name string
		body any
		code int
		id   string
	}{
		{"not JSON", "{", 400, "UPNP002"},
		{"too large", `{"container":"` + strings.Repeat("a", maxAPIBody) + `"}`, 400, "UPNP001"},
		{"no container", open("", 8096, "tcp"), 400, "UPNP003"},
		{"a name that is not a container's", open("../etc", 8096, "tcp"), 400, "UPNP003"},
		{"a name with a line break", open("jelly\nfin", 8096, "tcp"), 400, "UPNP003"},
		{"port 0", open("jellyfin", 0, "tcp"), 400, "UPNP003"},
		{"port too high", open("jellyfin", 65536, "tcp"), 400, "UPNP003"},
		{"protocol", open("jellyfin", 8096, "sctp"), 400, "UPNP003"},
		{"not confirmed", with(open("jellyfin", 8096, "tcp"), func(r *appPortRequest) { r.Confirm = "" }), 400, "UPNP004"},
		{"confirmed for another port", with(open("jellyfin", 8096, "tcp"), func(r *appPortRequest) { r.Confirm = "jellyfin:8097/tcp" }), 400, "UPNP004"},
		{"scheme", with(open("jellyfin", 8096, "tcp"), func(r *appPortRequest) { r.Scheme = "javascript" }), 400, "UPNP005"},
		{"external port out of range", with(open("jellyfin", 8096, "tcp"), func(r *appPortRequest) { r.ExternalPort = 70000 }), 400, "UPNP006"},
		{"external port negative", with(open("jellyfin", 8096, "tcp"), func(r *appPortRequest) { r.ExternalPort = -1 }), 400, "UPNP006"},
		{"external port reserved", with(open("jellyfin", 8096, "tcp"), func(r *appPortRequest) { r.ExternalPort = 443 }), 400, "UPNP007"},
		{"external port is Cosmos'", with(open("jellyfin", 8096, "tcp"), func(r *appPortRequest) { r.ExternalPort = 2053 }), 400, "UPNP008"},
		{"no such container", open("ghost", 8096, "tcp"), 409, "UPNP010"},
		{"Cosmos' own container", open("cosmos", 443, "tcp"), 409, "UPNP011"},
		{"not running", open("stopped", 80, "tcp"), 409, "UPNP012"},
		{"a port it does not publish", open("jellyfin", 8920, "tcp"), 409, "UPNP013"},
		{"the other protocol", open("jellyfin", 8096, "udp"), 409, "UPNP013"},
		{"published for the server only", open("local", 80, "tcp"), 409, "UPNP014"},
		{"Cosmos' port under an app's name", open("panel", 443, "tcp"), 409, "UPNP015"},
	}
	for _, c := range cases {
		code, answer := call(t, AppPortRoute, "POST", c.body, admin)
		if code != c.code || answer["code"] != c.id {
			t.Errorf("%s: %d %v, want %d %s", c.name, code, answer["code"], c.code, c.id)
		}
	}
	if len(loadChoices()) != 0 || w.discovered != 0 {
		t.Errorf("a refused request left a choice or reached the router: %+v", loadChoices())
	}

	// at most maxAppPorts
	many := []Published{}
	list := []utils.UPnPAppPort{}
	for i := 0; i < maxAppPorts+1; i++ {
		many = append(many, tcp(3000+i, 3000+i))
		if i < maxAppPorts {
			list = append(list, choose("many", 3000+i, "tcp"))
		}
	}
	w.containers = append(w.containers, container("many", true, many...))
	w.choices(list...)
	code, answer := call(t, AppPortRoute, "POST", open("many", 3000+maxAppPorts, "tcp"), admin)
	if code != 409 || answer["code"] != "UPNP016" {
		t.Errorf("one more than the limit: %d %v", code, answer["code"])
	}
	// one of the open ones may be asked for again
	if code, _ := call(t, AppPortRoute, "POST", open("many", 3000, "tcp"), admin); code != 200 {
		t.Errorf("an open port asked again: %d", code)
	}

	// Docker away: nothing can be checked, nothing is opened
	w.choices()
	w.dockerErr = errors.New("away")
	if code, answer := call(t, AppPortRoute, "POST", open("jellyfin", 8096, "tcp"), admin); code != 409 || answer["code"] != "UPNP009" || len(loadChoices()) != 0 {
		t.Errorf("with Docker away: %d %v", code, answer["code"])
	}
}

func TestRoutesNeedTheAdministrator(t *testing.T) {
	w := newWorld(t)
	w.containers = []Container{container("jellyfin", true, tcp(8096, 8096))}
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.Enabled = true })
	reconcile(true)

	withoutPassword := &utils.AuthContext{Nickname: "admin", Permissions: admin.Permissions}
	reader := &utils.AuthContext{Nickname: "reader", IsSudoed: true, Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ, utils.PERM_ADMIN_READ, utils.PERM_RESOURCES, utils.PERM_CONFIGURATION}}
	user := &utils.AuthContext{Nickname: "user", Permissions: []utils.Permission{utils.PERM_LOGIN}}
	for name, handler := range map[string]http.HandlerFunc{"apps": AppPortRoute, "close": CloseRoute} {
		body := any(open("jellyfin", 8096, "tcp"))
		if name == "close" {
			body = closeRequest{Name: "HTTPS"}
		}
		for who, auth := range map[string]*utils.AuthContext{"nobody": nil, "a user": user, "an admin who did not confirm the password": withoutPassword, "someone without the admin permission": reader} {
			if code, _ := call(t, handler, "POST", body, auth); code != http.StatusUnauthorized {
				t.Errorf("%s as %s: %d, want 401", name, who, code)
			}
		}
		for _, method := range []string{"GET", "PUT", "DELETE", "PATCH"} {
			if code, _ := call(t, handler, method, body, admin); code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: %d, want 405", method, name, code)
			}
		}
	}
	if code, _ := call(t, StatusRoute, "GET", nil, user); code != http.StatusUnauthorized {
		t.Errorf("the status as a user: %d", code)
	}
	if code, _ := call(t, StatusRoute, "POST", nil, reader); code != http.StatusUnauthorized {
		t.Errorf("Check now without the admin permission: %d", code)
	}
	if len(loadChoices()) != 0 || !utils.GetMainConfig().HTTPConfig.UPnP.Enabled {
		t.Error("a refused request changed something")
	}

	// close: Cosmos' own port
	w.kinds()
	if code, answer := call(t, CloseRoute, "POST", closeRequest{Name: "FTP"}, admin); code != 400 {
		t.Errorf("close of something else: %d %v", code, answer)
	}
	code, _ := call(t, CloseRoute, "POST", closeRequest{Name: "HTTPS"}, admin)
	if code != 200 || utils.GetMainConfig().HTTPConfig.UPnP.Enabled || len(w.router.keys()) != 0 {
		t.Errorf("close HTTPS: %d, enabled %v, router %v", code, utils.GetMainConfig().HTTPConfig.UPnP.Enabled, w.router.keys())
	}
	if got := strings.Join(w.kinds(), "; "); got != "upnp.port.closed Cosmos HTTPS; upnp.mapping.removed Cosmos HTTPS 2053 off" {
		t.Errorf("events: %s", got)
	}
}
