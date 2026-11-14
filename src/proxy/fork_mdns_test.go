package proxy

// Fork addition: tests of fork_mdns*.go against a stand-in for Avahi's D-Bus
// interface and for the machine's interfaces. Nothing here talks to a real
// Avahi daemon or the network.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/holoplot/go-avahi"
	"github.com/miekg/dns"

	"github.com/azukaar/cosmos-server/src/utils"
)

// --- stand-in Avahi -------------------------------------------------------

type fakeRecord struct {
	iface   int32
	name    string
	address string
}

type fakeGroup struct {
	daemon    *fakeAvahi
	addresses []fakeRecord
	services  []fakeRecord
	committed bool
	state     int32
	freed     bool
	resets    int
}

type fakeAvahi struct {
	host       string
	generation int // a restart makes the old groups unknown
	groups     []*fakeGroup
	gen        map[*fakeGroup]int
	closed     int
	taken      map[string]bool // names another machine answers for (collision once committed)
	localTaken map[string]bool // names another program on this machine has (AddAddress fails)
	down       bool
}

func (a *fakeAvahi) HostFqdn() (string, error) {
	if a.down {
		return "", errors.New("org.freedesktop.DBus.Error.ServiceUnknown")
	}
	return a.host, nil
}

func (a *fakeAvahi) NewGroup() (mdnsGroup, error) {
	g := &fakeGroup{daemon: a}
	a.groups = append(a.groups, g)
	a.gen[g] = a.generation
	return g, nil
}

func (a *fakeAvahi) Close() { a.closed++ }

func (g *fakeGroup) known() bool {
	return !g.daemon.down && g.daemon.gen[g] == g.daemon.generation && !g.freed
}

func (g *fakeGroup) AddAddress(iface int32, name, address string) error {
	if g.daemon.localTaken[name] {
		return errors.New("Local name collision")
	}
	g.addresses = append(g.addresses, fakeRecord{iface, name, address})
	return nil
}
func (g *fakeGroup) AddService(iface int32, name string, port uint16) error {
	g.services = append(g.services, fakeRecord{iface, name, fmt.Sprint(port)})
	return nil
}
func (g *fakeGroup) Commit() error {
	if !g.known() {
		return errors.New("gone")
	}
	g.committed = true
	g.state = avahi.EntryGroupEstablished
	for _, record := range g.addresses {
		if g.daemon.taken[record.name] {
			g.state = avahi.EntryGroupCollision
		}
	}
	return nil
}
func (g *fakeGroup) Reset() error {
	g.addresses, g.services, g.committed, g.state = nil, nil, false, avahi.EntryGroupUncommited
	g.resets++
	return nil
}
func (g *fakeGroup) State() (int32, error) {
	if !g.known() {
		return 0, errors.New("org.freedesktop.Avahi.NotFoundError")
	}
	return g.state, nil
}
func (g *fakeGroup) Free() { g.freed = true }

// live returns the records Avahi publishes now: name -> "iface=address" list.
func (a *fakeAvahi) live() map[string][]string {
	out := map[string][]string{}
	for _, g := range a.groups {
		if !g.known() || !g.committed || g.state != avahi.EntryGroupEstablished {
			continue
		}
		for _, record := range g.addresses {
			out[record.name] = append(out[record.name], fmt.Sprintf("%d=%s", record.iface, record.address))
		}
	}
	for name := range out {
		sort.Strings(out[name])
	}
	return out
}

// --- stand-in machine -----------------------------------------------------

const (
	upMulticast = net.FlagUp | net.FlagMulticast | net.FlagBroadcast
)

func homeServer() []systemInterface {
	return []systemInterface{
		{Name: "lo", Index: 1, Flags: net.FlagUp | net.FlagLoopback, IPv4: []net.IP{net.IPv4(127, 0, 0, 1)}},
		{Name: "eno1", Index: 2, Flags: upMulticast, IPv4: []net.IP{net.IPv4(172, 16, 0, 14)}},
		{Name: "eno2", Index: 3, Flags: net.FlagBroadcast | net.FlagMulticast}, // down
		{Name: "tailscale0", Index: 6, Flags: net.FlagUp | net.FlagPointToPoint | net.FlagMulticast, IPv4: []net.IP{net.IPv4(100, 64, 0, 2)}},
		{Name: "br-90a4ac8c00e0", Index: 7, Flags: upMulticast, IPv4: []net.IP{net.IPv4(172, 18, 0, 1)}},
		{Name: "docker0", Index: 8, Flags: upMulticast, IPv4: []net.IP{net.IPv4(172, 17, 0, 1)}},
		{Name: "vethf29df6a", Index: 9, Flags: upMulticast},
		{Name: "wg0", Index: 10, Flags: upMulticast, IPv4: []net.IP{net.IPv4(10, 8, 0, 1)}},
	}
}

type harness struct {
	t       *testing.T
	daemon  *fakeAvahi
	system  []systemInterface
	def     string
	onBus   bool
	now     time.Time
	asked   [][]string
	answers func(iface lanInterface, names []string) map[string][]mdnsAnswer
}

func newHarness(t *testing.T, hosts ...string) *harness {
	h := &harness{
		t:      t,
		daemon: &fakeAvahi{host: "server.local", gen: map[*fakeGroup]int{}, taken: map[string]bool{}, localTaken: map[string]bool{}},
		system: homeServer(),
		def:    "eno1",
		onBus:  true,
		now:    time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC),
	}
	saved := []interface{}{mdnsConnect, mdnsAvahiOnBus, mdnsNow, mdnsSystemInterfaces, mdnsDefaultRoute, mdnsAsk, mdnsAvahiConfPath}
	mdnsConnect = func() (mdnsAvahi, error) {
		if !h.onBus {
			return nil, errors.New("no Avahi")
		}
		h.daemon.down = false
		return h.daemon, nil
	}
	mdnsAvahiOnBus = func() bool { return h.onBus }
	mdnsNow = func() time.Time { return h.now }
	mdnsSystemInterfaces = func() ([]systemInterface, error) { return h.system, nil }
	mdnsDefaultRoute = func() string { return h.def }
	mdnsAsk = func(iface lanInterface, names []string, timeout time.Duration) map[string][]mdnsAnswer {
		h.asked = append(h.asked, append([]string{iface.Name}, names...))
		if h.answers != nil {
			return h.answers(iface, names)
		}
		return map[string][]mdnsAnswer{}
	}
	mdnsAvahiConfPath = filepath.Join(t.TempDir(), "missing.conf")
	localNames = &localNamesPublisher{entries: map[string]*mdnsEntry{}}
	forgetLocalNamesCheck()
	h.routes(hosts...)
	t.Cleanup(func() {
		mdnsConnect = saved[0].(func() (mdnsAvahi, error))
		mdnsAvahiOnBus = saved[1].(func() bool)
		mdnsNow = saved[2].(func() time.Time)
		mdnsSystemInterfaces = saved[3].(func() ([]systemInterface, error))
		mdnsDefaultRoute = saved[4].(func() string)
		mdnsAsk = saved[5].(func(lanInterface, []string, time.Duration) map[string][]mdnsAnswer)
		mdnsAvahiConfPath = saved[6].(string)
		localNames = &localNamesPublisher{entries: map[string]*mdnsEntry{}}
		forgetLocalNamesCheck()
		utils.LoadBaseMainConfig(utils.Config{})
	})
	return h
}

func (h *harness) config(edit func(*utils.Config), hosts ...string) {
	config := utils.Config{}
	config.HTTPConfig.Hostname = "cosmos.example.org"
	config.HTTPConfig.HTTPPort, config.HTTPConfig.HTTPSPort = "2052", "2053"
	config.HTTPConfig.PublishMDNS = true
	for _, host := range hosts {
		config.HTTPConfig.ProxyConfig.Routes = append(config.HTTPConfig.ProxyConfig.Routes,
			utils.ProxyRouteConfig{Name: host, UseHost: true, Host: host})
	}
	if edit != nil {
		edit(&config)
	}
	utils.LoadBaseMainConfig(config)
}

func (h *harness) routes(hosts ...string) { h.config(nil, hosts...) }

func (h *harness) expectLive(want map[string][]string) {
	h.t.Helper()
	got := h.daemon.live()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		h.t.Fatalf("published records:\n got  %v\n want %v", got, want)
	}
}

func stateOf(name string) string {
	localNames.mu.Lock()
	defer localNames.mu.Unlock()
	if entry := localNames.entries[name]; entry != nil {
		return entry.state
	}
	return ""
}

// --- tests ----------------------------------------------------------------

func TestLocalNamesAreAddressRecordsOnTheLANOnly(t *testing.T) {
	h := newHarness(t, "radarr.local", "Sonarr.local", "app.example.org", "radarr.local")
	h.system = append(h.system, systemInterface{Name: "wlan0", Index: 11, Flags: upMulticast, IPv4: []net.IP{net.IPv4(192, 168, 1, 5)}})
	ForkPublishLocalNames()

	h.expectLive(map[string][]string{
		"radarr.local": {"11=192.168.1.5", "2=172.16.0.14"},
		"sonarr.local": {"11=192.168.1.5", "2=172.16.0.14"},
	})
	// one group per name, so a collision names its name; the DNS-SD entry stays, on the same interfaces
	committed := 0
	for _, g := range h.daemon.groups {
		if g.committed {
			committed++
			if len(g.services) != 2 || g.services[0].address != "2052" {
				t.Fatalf("service entries: %v", g.services)
			}
		}
	}
	if committed != 2 {
		t.Fatalf("%d groups committed, want one per name", committed)
	}
	if stateOf("radarr.local") != namePublished && stateOf("radarr.local") != nameRegistering {
		t.Fatalf("state: %q", stateOf("radarr.local"))
	}
}

func TestDefaultRouteInterfaceFirstAndAnyAddress(t *testing.T) {
	h := newHarness(t)
	// a LAN with a public IPv4 on the default route's interface still qualifies; a public one elsewhere does not
	h.system = []systemInterface{
		{Name: "eth1", Index: 3, Flags: upMulticast, IPv4: []net.IP{net.IPv4(192, 168, 50, 2)}},
		{Name: "eth0", Index: 2, Flags: upMulticast, IPv4: []net.IP{net.IPv4(169, 254, 3, 3), net.IPv4(198, 51, 100, 7)}},
		{Name: "eth2", Index: 4, Flags: upMulticast, IPv4: []net.IP{net.IPv4(203, 0, 113, 9)}},
		{Name: "br0", Index: 5, Flags: upMulticast, IPv4: []net.IP{net.IPv4(10, 0, 0, 2)}}, // a host bridge is a LAN
	}
	h.def = "eth0"
	got, problems := chooseLANInterfaces(nil)
	if len(problems) != 0 || len(got) != 3 || got[0].Name != "eth0" || got[0].Address != "198.51.100.7" || !got[0].Default || got[1].Name != "eth1" || got[2].Name != "br0" {
		t.Fatalf("chosen %+v problems %v", got, problems)
	}

	h.system = homeServer()
	h.def = "eno1"
	got, _ = chooseLANInterfaces(nil)
	if len(got) != 1 || got[0].Name != "eno1" {
		t.Fatalf("on the home server: %+v (Docker, veth, Tailscale and WireGuard must be left out)", got)
	}
}

func TestOverrideInterfaces(t *testing.T) {
	h := newHarness(t)
	h.config(func(c *utils.Config) {
		c.HTTPConfig.MDNSInterfaces = []string{"eno2", "eth9", "br-90a4ac8c00e0", "x y", "vethf29df6a"}
	}, "radarr.local")
	ForkPublishLocalNames()
	// named explicitly, even a bridge is used; the others are said why not
	h.expectLive(map[string][]string{"radarr.local": {"7=172.18.0.1"}})
	resp := localNames.snapshot()
	want := []mdnsProblem{{Code: "down", Name: "eno2"}, {Code: "notFound", Name: "eth9"}, {Code: "noIPv4", Name: "vethf29df6a"}}
	if fmt.Sprint(resp.Problems) != fmt.Sprint(want) {
		t.Fatalf("problems %v, want %v", resp.Problems, want)
	}

	h.config(func(c *utils.Config) { c.HTTPConfig.MDNSInterfaces = []string{"eth9"} }, "radarr.local")
	ForkPublishLocalNames()
	h.expectLive(map[string][]string{})
	if stateOf("radarr.local") != nameNoInterface {
		t.Fatalf("state %q", stateOf("radarr.local"))
	}
}

func TestRepublishAfterAvahiRestart(t *testing.T) {
	h := newHarness(t, "radarr.local")
	ForkPublishLocalNames()
	h.daemon.generation++ // the daemon restarted: it no longer knows the groups
	h.expectLive(map[string][]string{})
	checkMDNS()
	h.expectLive(map[string][]string{"radarr.local": {"2=172.16.0.14"}})
	if h.daemon.closed != 1 {
		t.Fatalf("the old connection was closed %d times", h.daemon.closed)
	}
}

func TestRepublishOnAddressChange(t *testing.T) {
	h := newHarness(t, "radarr.local")
	ForkPublishLocalNames()
	created := len(h.daemon.groups)
	checkMDNS() // nothing changed: nothing is redone
	if g := h.daemon.groups[0]; g.resets != 0 || len(h.daemon.groups) != created {
		t.Fatal("republished although nothing changed")
	}
	h.system[1].IPv4 = []net.IP{net.IPv4(172, 16, 0, 99)} // DHCP gave another address
	checkMDNS()
	h.expectLive(map[string][]string{"radarr.local": {"2=172.16.0.99"}})
}

func TestConflictIsReportedNotRenamed(t *testing.T) {
	h := newHarness(t, "radarr.local", "sonarr.local")
	h.daemon.taken["radarr.local"] = true
	ForkPublishLocalNames()
	checkMDNS() // the collision is seen on the next look
	if stateOf("radarr.local") != nameConflict || stateOf("sonarr.local") != namePublished {
		t.Fatalf("states %q %q", stateOf("radarr.local"), stateOf("sonarr.local"))
	}
	for _, g := range h.daemon.groups {
		for _, record := range g.addresses {
			if strings.HasPrefix(record.name, "radarr-") || strings.Contains(record.name, "#") {
				t.Fatalf("renamed: %v", record)
			}
		}
	}
	resets := h.daemon.groups[0].resets
	h.now = h.now.Add(5 * time.Minute)
	checkMDNS()
	if h.daemon.groups[0].resets != resets {
		t.Fatal("tried again before ten minutes")
	}
	h.now = h.now.Add(6 * time.Minute)
	delete(h.daemon.taken, "radarr.local") // the other machine is gone
	checkMDNS()
	checkMDNS()
	if stateOf("radarr.local") != namePublished {
		t.Fatalf("not published after the retry: %q", stateOf("radarr.local"))
	}

	// a settings save tries again at once
	h.daemon.taken["sonarr.local"] = true
	h.daemon.groups[1].state = avahi.EntryGroupCollision
	checkMDNS()
	delete(h.daemon.taken, "sonarr.local")
	ForkPublishLocalNames()
	checkMDNS()
	if stateOf("sonarr.local") != namePublished {
		t.Fatalf("a save did not retry: %q", stateOf("sonarr.local"))
	}
}

func TestLocalCollisionWithAnotherProgram(t *testing.T) {
	h := newHarness(t, "radarr.local")
	h.daemon.localTaken["radarr.local"] = true
	ForkPublishLocalNames()
	if stateOf("radarr.local") != nameConflict {
		t.Fatalf("state %q", stateOf("radarr.local"))
	}
	resp := localNames.snapshot()
	if resp.Names[0].Detail == "" {
		t.Fatal("Avahi's words are not passed on")
	}
}

func TestOwnHostNameIsLeftToAvahi(t *testing.T) {
	h := newHarness(t, "server.local", "radarr.local")
	ForkPublishLocalNames()
	h.expectLive(map[string][]string{"radarr.local": {"2=172.16.0.14"}})
	if stateOf("server.local") != nameHostName {
		t.Fatalf("state %q", stateOf("server.local"))
	}
}

func TestAvahiAbsentAndAppearingLater(t *testing.T) {
	h := newHarness(t, "radarr.local")
	h.onBus = false
	ForkPublishLocalNames()
	resp := localNamesStatus(false)
	if resp.Avahi != "absent" || resp.Names[0].State != nameFailed || resp.Names[0].Answered {
		t.Fatalf("%+v", resp)
	}
	if len(h.asked) != 0 {
		t.Fatal("asked the network without Avahi")
	}
	h.onBus = true
	checkMDNS()
	h.expectLive(map[string][]string{"radarr.local": {"2=172.16.0.14"}})
}

func TestSwitchingOffWithdraws(t *testing.T) {
	h := newHarness(t, "radarr.local")
	ForkPublishLocalNames()
	h.config(func(c *utils.Config) { c.HTTPConfig.PublishMDNS = false }, "radarr.local")
	checkMDNS()
	h.expectLive(map[string][]string{})
	if resp := localNamesStatus(false); resp.Avahi != "off" || resp.Enabled || len(resp.Names) != 0 {
		t.Fatalf("%+v", resp)
	}
}

func TestRemovedNameIsWithdrawn(t *testing.T) {
	h := newHarness(t, "radarr.local", "sonarr.local")
	ForkPublishLocalNames()
	h.routes("sonarr.local")
	ForkPublishLocalNames()
	h.expectLive(map[string][]string{"sonarr.local": {"2=172.16.0.14"}})
}

func TestBounds(t *testing.T) {
	hosts := []string{}
	for i := 0; i < 300; i++ {
		hosts = append(hosts, fmt.Sprintf("app%03d.local", i))
	}
	h := newHarness(t, hosts...)
	ForkPublishLocalNames()
	if got := len(h.daemon.live()); got != mdnsMaxNames {
		t.Fatalf("%d names published", got)
	}
	if stateOf("app299.local") != nameTooMany {
		t.Fatalf("state %q", stateOf("app299.local"))
	}
	localNamesStatus(false)
	if len(h.asked) != 1 || len(h.asked[0])-1 != mdnsMaxChecked {
		t.Fatalf("asked %d names", len(h.asked[0])-1)
	}
}

func TestStatusAnswersAndCache(t *testing.T) {
	h := newHarness(t, "radarr.local", "sonarr.local")
	h.answers = func(iface lanInterface, names []string) map[string][]mdnsAnswer {
		return map[string][]mdnsAnswer{
			"radarr.local": {{Interface: iface.Name, Address: iface.Address, From: iface.Address, Own: true}},
			"sonarr.local": {{Interface: iface.Name, Address: "172.16.0.50", From: "172.16.0.50"}},
		}
	}
	ForkPublishLocalNames()
	resp := localNamesStatus(false)
	if !resp.Names[0].Answered || resp.Names[1].Answered || len(resp.Names[1].Answers) != 1 || !resp.Names[1].Checked {
		t.Fatalf("%+v", resp.Names)
	}
	if resp.HostName != "server.local" || len(resp.Interfaces) != 1 || resp.Interfaces[0].Address != "172.16.0.14" || resp.HTTPSPort != "2053" {
		t.Fatalf("%+v", resp)
	}
	localNamesStatus(false)
	localNamesStatus(true) // forced, but the last check is younger than five seconds
	if len(h.asked) != 1 {
		t.Fatalf("the network was asked %d times", len(h.asked))
	}
	checkCache.Lock()
	checkCache.at = checkCache.at.Add(-10 * time.Second)
	checkCache.Unlock()
	localNamesStatus(false) // kept 30 s
	if len(h.asked) != 1 {
		t.Fatal("not cached")
	}
	localNamesStatus(true)
	if len(h.asked) != 2 {
		t.Fatal("a forced check older than five seconds did not ask again")
	}
}

func TestAvahiConfIsRead(t *testing.T) {
	h := newHarness(t, "radarr.local")
	_ = h
	path := filepath.Join(t.TempDir(), "avahi-daemon.conf")
	os.WriteFile(path, []byte("[server]\n#allow-interfaces=eth0\nallow-interfaces= eno1, bad name ,eno2\n[publish]\ndeny-interfaces=docker0\n"), 0600)
	mdnsAvahiConfPath = path
	info := readAvahiConf()
	if !info.Read || fmt.Sprint(info.AllowInterfaces) != "[eno1 eno2]" || len(info.DenyInterfaces) != 0 {
		t.Fatalf("%+v", info)
	}
}

func TestParseMDNSAnswers(t *testing.T) {
	reply := new(dns.Msg)
	reply.Response = true
	reply.Answer = []dns.RR{
		&dns.A{Hdr: dns.RR_Header{Name: "Radarr.local.", Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.IPv4(172, 16, 0, 14)},
		&dns.A{Hdr: dns.RR_Header{Name: "other.local.", Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.IPv4(172, 16, 0, 15)},
		&dns.CNAME{Hdr: dns.RR_Header{Name: "radarr.local.", Rrtype: dns.TypeCNAME, Class: dns.ClassINET}, Target: "server.local."},
	}
	raw, _ := reply.Pack()
	got := parseMDNSAnswers(raw, map[string]bool{"radarr.local.": true})
	if len(got) != 1 || got[0].address != "172.16.0.14" {
		t.Fatalf("%v", got)
	}
	query := new(dns.Msg)
	query.SetQuestion("radarr.local.", dns.TypeA)
	raw, _ = query.Pack()
	if len(parseMDNSAnswers(raw, map[string]bool{"radarr.local.": true})) != 0 {
		t.Fatal("a question taken for an answer")
	}
	if parseMDNSAnswers([]byte{1, 2, 3}, map[string]bool{}) != nil {
		t.Fatal("garbage parsed")
	}
}

func TestParseRouteTable(t *testing.T) {
	table := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"wlan0\t00000000\t0101A8C0\t0003\t0\t0\t600\t00000000\t0\t0\t0\n" +
		"eno1\t00000000\t010010AC\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
		"eno1\t000010AC\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n"
	if got := parseRouteTable(strings.NewReader(table)); got != "eno1" {
		t.Fatalf("%q", got)
	}
	if got := parseRouteTable(strings.NewReader("garbage")); got != "" {
		t.Fatalf("%q", got)
	}
}

func TestPrintable(t *testing.T) {
	if got := printable("a\nb\x1bc\u0085d", 3); got != "abc" {
		t.Fatalf("%q", got)
	}
}

func localNamesRequest(method, who, body string) *http.Request {
	req := httptest.NewRequest(method, "/cosmos/api/local-names", strings.NewReader(body))
	if who != "none" {
		auth := &utils.AuthContext{Nickname: "someone"}
		switch who {
		case "user":
			auth.Permissions = []utils.Permission{utils.PERM_LOGIN}
		case "reader":
			auth.Permissions = []utils.Permission{utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ}
		case "admin":
			auth.Permissions = []utils.Permission{utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ, utils.PERM_ADMIN_READ}
		}
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
	}
	return req
}

func TestLocalNamesRoute(t *testing.T) {
	h := newHarness(t, "radarr.local")
	ForkPublishLocalNames()
	for _, c := range []struct {
		method, who, body string
		code              int
	}{
		{"GET", "none", "", http.StatusUnauthorized},
		{"GET", "user", "", http.StatusUnauthorized},
		{"GET", "reader", "", http.StatusUnauthorized},
		{"GET", "admin", "", http.StatusOK},
		{"POST", "admin", `{"check":true}`, http.StatusOK},
		{"POST", "admin", `{"check":`, http.StatusBadRequest},
		{"POST", "admin", `{"x":"` + strings.Repeat("a", 2000) + `"}`, http.StatusBadRequest},
		{"PUT", "admin", "", http.StatusMethodNotAllowed},
		{"DELETE", "admin", "", http.StatusMethodNotAllowed},
	} {
		rec := httptest.NewRecorder()
		LocalNamesRoute(rec, localNamesRequest(c.method, c.who, c.body))
		code := rec.Code
		if code == http.StatusForbidden {
			code = http.StatusUnauthorized // either refusal will do
		}
		if code != c.code {
			t.Fatalf("%s as %s: %d, want %d (%s)", c.method, c.who, rec.Code, c.code, rec.Body.String())
		}
		if c.code == http.StatusOK {
			var body struct {
				Data localNamesResponse `json:"data"`
			}
			if json.Unmarshal(rec.Body.Bytes(), &body) != nil || len(body.Data.Names) != 1 || body.Data.Names[0].Name != "radarr.local" {
				t.Fatalf("answer: %s", rec.Body.String())
			}
		}
	}
	if len(h.asked) == 0 {
		t.Fatal("the route did not check the network")
	}
}
