package upnp

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// fakeRouter is an in-process UPnP IGD: a description document and a SOAP
// control endpoint backed by a table of port forwards. It behaves like
// miniupnpd: a mapping asked for again by its own client is updated, one
// asked for by another client is refused with 718.
type fakeRouter struct {
	mu            sync.Mutex
	server        *httptest.Server
	table         map[string]*fakeForward // "TCP/8443"
	onlyPermanent bool                    // refuse leased mappings, like some routers do
	noLookup      bool                    // GetSpecificPortMappingEntry is not implemented
	noList        bool                    // GetGenericPortMappingEntry is not implemented
	sameForever   bool                    // answers every index with the first entry
	// hidden: forwards made in the router's own pages, not shown to UPnP.
	// The value is the error the router answers a request for that port
	// with (718, 729), or 0: it says "done" and the manual forward wins.
	hidden     map[string]int
	refuse     map[string]int // other refusals by port key (606)
	controlURL string         // what the description advertises
	calls      []string       // "AddPortMapping TCP/8443"
}

type fakeForward struct {
	client      string
	port        string
	description string
	lease       string
}

func newFakeRouter(t *testing.T) *fakeRouter {
	t.Helper()
	r := &fakeRouter{table: map[string]*fakeForward{}, hidden: map[string]int{}, refuse: map[string]int{}, controlURL: "/ctl/ipconn"}
	mux := http.NewServeMux()
	mux.HandleFunc("/desc.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
 <device>
  <friendlyName>Test Router</friendlyName>
  <serviceList><service><serviceType>urn:schemas-upnp-org:service:Layer3Forwarding:1</serviceType><controlURL>/ctl/l3f</controlURL></service></serviceList>
  <deviceList><device><deviceList><device>
   <serviceList><service>
    <serviceType>urn:schemas-upnp-org:service:WANIPConnection:1</serviceType>
    <controlURL>%s</controlURL>
   </service></serviceList>
  </device></deviceList></device></deviceList>
 </device>
</root>`, r.controlURL)
	})
	mux.HandleFunc("/ctl/ipconn", r.control)
	r.server = httptest.NewServer(mux)
	t.Cleanup(r.server.Close)
	return r
}

// put places a forward in the table as another client would have.
func (r *fakeRouter) put(key string, client string, port int, description string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.table[key] = &fakeForward{client: client, port: strconv.Itoa(port), description: description, lease: "0"}
}

func (r *fakeRouter) get(key string) (fakeForward, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, ok := r.table[key]
	if !ok {
		return fakeForward{}, false
	}
	return *f, true
}

// reboot is what a power cut does to a router: every UPnP mapping is gone.
func (r *fakeRouter) reboot() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.table = map[string]*fakeForward{}
}

// expire ends the lease of one mapping.
func (r *fakeRouter) expire(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.table, key)
}

// count tells how often an action was asked for a port ("" for any port).
func (r *fakeRouter) count(action string, key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, call := range r.calls {
		if call == action+" "+key || (key == "" && strings.HasPrefix(call, action+" ")) {
			n++
		}
	}
	return n
}

func (r *fakeRouter) keys() []string {
	keys := make([]string, 0, len(r.table))
	for key := range r.table {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (r *fakeRouter) control(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	body, _ := io.ReadAll(req.Body)
	args := leafValues(body)
	key := args["NewProtocol"] + "/" + args["NewExternalPort"]
	fault := func(code int, text string) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>%d</errorCode><errorDescription>%s</errorDescription></UPnPError></detail></s:Fault></s:Body></s:Envelope>`, code, text)
	}
	reply := func(inner string) {
		fmt.Fprintf(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:R xmlns:u="x">%s</u:R></s:Body></s:Envelope>`, inner)
	}
	describe := func(key string, f *fakeForward) string {
		var text bytes.Buffer
		xml.EscapeText(&text, []byte(f.description))
		parts := strings.SplitN(key, "/", 2)
		return "<NewRemoteHost></NewRemoteHost><NewExternalPort>" + parts[1] + "</NewExternalPort><NewProtocol>" + parts[0] + "</NewProtocol>" +
			"<NewInternalPort>" + f.port + "</NewInternalPort><NewInternalClient>" + f.client + "</NewInternalClient><NewEnabled>1</NewEnabled>" +
			"<NewPortMappingDescription>" + text.String() + "</NewPortMappingDescription><NewLeaseDuration>" + f.lease + "</NewLeaseDuration>"
	}

	action := strings.Trim(req.Header.Get("SOAPAction"), `"`)
	action = action[strings.Index(action, "#")+1:]
	r.calls = append(r.calls, action+" "+key)
	switch action {
	case "GetExternalIPAddress":
		reply("<NewExternalIPAddress>203.0.113.7</NewExternalIPAddress>")
	case "GetSpecificPortMappingEntry":
		if r.noLookup {
			fault(401, "Invalid Action")
			return
		}
		if f, ok := r.table[key]; ok {
			reply(describe(key, f))
		} else {
			fault(714, "NoSuchEntryInArray")
		}
	case "GetGenericPortMappingEntry":
		if r.noList {
			fault(401, "Invalid Action")
			return
		}
		index, _ := strconv.Atoi(args["NewPortMappingIndex"])
		keys := r.keys()
		if r.sameForever && len(keys) > 0 {
			index = 0
		}
		if index < 0 || index >= len(keys) {
			fault(713, "SpecifiedArrayIndexInvalid")
			return
		}
		reply(describe(keys[index], r.table[keys[index]]))
	case "AddPortMapping":
		if code, ok := r.refuse[key]; ok {
			fault(code, "Refused")
			return
		}
		if code, ok := r.hidden[key]; ok {
			if code != 0 {
				fault(code, "Conflict")
			} else {
				reply("") // "done", but the forward made by hand wins
			}
			return
		}
		if r.onlyPermanent && args["NewLeaseDuration"] != "0" {
			fault(725, "OnlyPermanentLeasesSupported")
			return
		}
		if f, ok := r.table[key]; ok && f.client != args["NewInternalClient"] {
			fault(718, "ConflictInMappingEntry")
			return
		}
		r.table[key] = &fakeForward{client: args["NewInternalClient"], port: args["NewInternalPort"], description: args["NewPortMappingDescription"], lease: args["NewLeaseDuration"]}
		reply("")
	case "DeletePortMapping":
		if _, ok := r.table[key]; !ok {
			fault(714, "NoSuchEntryInArray")
			return
		}
		delete(r.table, key)
		reply("")
	default:
		fault(401, "Invalid Action")
	}
}

var loopback = net.ParseIP("127.0.0.1")

const serverIP = "192.168.1.50"

func (r *fakeRouter) gateway(t *testing.T) *gateway {
	t.Helper()
	g, err := loadGateway(r.server.URL+"/desc.xml", loopback)
	if err != nil {
		t.Fatalf("loadGateway: %v", err)
	}
	g.localIP = net.ParseIP(serverIP)
	return g
}

func TestLoadGatewayFindsTheNestedWANService(t *testing.T) {
	r := newFakeRouter(t)
	g := r.gateway(t)
	if g.serviceType != "urn:schemas-upnp-org:service:WANIPConnection:1" {
		t.Errorf("service = %q", g.serviceType)
	}
	if g.controlURL != r.server.URL+"/ctl/ipconn" {
		t.Errorf("control URL = %q", g.controlURL)
	}
	if g.name != "Test Router" {
		t.Errorf("name = %q", g.name)
	}
}

func TestOnlyTheGatewayIsTrusted(t *testing.T) {
	r := newFakeRouter(t)

	// A description that lives somewhere other than the gateway.
	if _, err := loadGateway(r.server.URL+"/desc.xml", net.ParseIP("192.168.1.1")); err == nil {
		t.Error("accepted a description from a host that is not the gateway")
	}
	// A gateway description that points its control URL at another host.
	r.controlURL = "http://198.51.100.9/ctl"
	if _, err := loadGateway(r.server.URL+"/desc.xml", loopback); err == nil {
		t.Error("accepted a control URL on another host")
	}
	// Anything but plain HTTP.
	for _, u := range []string{"https://127.0.0.1/desc.xml", "file:///etc/passwd", "http://localhost/desc.xml"} {
		if _, err := onGateway(u, loopback); err == nil {
			t.Errorf("onGateway accepted %q", u)
		}
	}
}

func TestClaimRenewAndRelease(t *testing.T) {
	r := newFakeRouter(t)
	g := r.gateway(t)

	for i := 0; i < 2; i++ { // the second call is a renewal of our own mapping
		if err := g.claim("TCP", 8443, 2053, "Cosmos HTTPS", 3600, i == 1); err != nil {
			t.Fatalf("claim (call %d): %v", i+1, err)
		}
	}
	got, _ := r.get("TCP/8443")
	if got.client != serverIP || got.port != "2053" || got.lease != "3600" || got.description != "Cosmos HTTPS" {
		t.Fatalf("router has %+v", got)
	}

	if deleted, err := g.release("TCP", 8443, 2053, true); err != nil || !deleted {
		t.Fatalf("release: %v, %v", deleted, err)
	}
	if _, still := r.get("TCP/8443"); still {
		t.Error("mapping still present after release")
	}
	if deleted, err := g.release("TCP", 8443, 2053, true); err != nil || deleted {
		t.Errorf("releasing a mapping that is already gone: %v, %v", deleted, err)
	}
}

// What Cosmos did not create is never written over and never deleted: not
// another device's mapping, and not one another program on this server made.
func TestForeignMappingsAreLeftAlone(t *testing.T) {
	r := newFakeRouter(t)
	g := r.gateway(t)
	r.put("TCP/8443", "192.168.1.99", 443, "NAS")
	r.put("TCP/6881", serverIP, 6881, "qBittorrent")

	var conflict *conflictError
	err := g.claim("TCP", 8443, 2053, "Cosmos HTTPS", 3600, false)
	if !errors.As(err, &conflict) || conflict.Holder == nil || conflict.Holder.InternalClient != "192.168.1.99" || conflict.Holder.Description != "NAS" {
		t.Errorf("another device's port: %v", err)
	}
	// same server, another program, another internal port: the router would
	// update it without a word, so Cosmos must not ask
	err = g.claim("TCP", 6881, 51413, "Cosmos app torrent", 3600, false)
	if !errors.As(err, &conflict) || r.count("AddPortMapping", "TCP/6881") != 0 {
		t.Errorf("another program's port on this server: %v, asked %d times", err, r.count("AddPortMapping", "TCP/6881"))
	}
	for _, port := range []int{8443, 6881} {
		if deleted, err := g.release("TCP", port, 2053, false); err != nil || deleted {
			t.Errorf("release of a foreign mapping on %d: %v, %v", port, deleted, err)
		}
		if deleted, _ := g.release("TCP", port, 2053, true); deleted {
			t.Errorf("deleted the foreign mapping on %d because the port was once Cosmos'", port)
		}
	}
	if got, _ := r.get("TCP/8443"); got.client != "192.168.1.99" || got.port != "443" {
		t.Errorf("another device's mapping was changed: %+v", got)
	}
	if got, _ := r.get("TCP/6881"); got.port != "6881" || got.description != "qBittorrent" {
		t.Errorf("another program's mapping was changed: %+v", got)
	}
	if r.count("DeletePortMapping", "") != 0 {
		t.Error("a delete was sent to the router")
	}
}

func TestOwnOrEquivalentMappingsAreTakenOver(t *testing.T) {
	r := newFakeRouter(t)
	g := r.gateway(t)
	// Cosmos' own from an earlier run, pointing at the host port the app had then
	r.put("TCP/8096", serverIP, 8000, "Cosmos app jellyfin")
	if err := g.claim("TCP", 8096, 8096, "Cosmos app jellyfin", 3600, false); err != nil {
		t.Errorf("our own stale mapping: %v", err)
	}
	// already what is wanted, whoever wrote it
	r.put("UDP/9000", serverIP, 9000, "")
	if err := g.claim("UDP", 9000, 9000, "Cosmos app game", 3600, false); err != nil {
		t.Errorf("a mapping that already leads to the wanted port: %v", err)
	}
	// only Cosmos' own are deleted
	if deleted, _ := g.release("TCP", 8096, 8096, false); !deleted {
		t.Error("our own mapping was not released")
	}
}

// A forward made in the router's own pages is often invisible to UPnP: the
// lookup says the port is free.
func TestHiddenManualForwards(t *testing.T) {
	r := newFakeRouter(t)
	g := r.gateway(t)
	var conflict *conflictError
	for key, code := range map[string]int{"TCP/443": 718, "TCP/25": 729} {
		r.hidden[key] = code
		port, _ := strconv.Atoi(strings.TrimPrefix(key, "TCP/"))
		if err := g.claim("TCP", port, 2053, "Cosmos HTTPS", 3600, false); !errors.As(err, &conflict) || conflict.Code != code {
			t.Errorf("%s refused with %d: %v", key, code, err)
		}
	}
	// the router says "done" and shows nothing: Cosmos cannot know better
	r.hidden["TCP/81"] = 0
	if err := g.claim("TCP", 81, 2052, "Cosmos HTTP", 3600, false); err != nil {
		t.Errorf("accepted and not shown: %v", err)
	}
}

func TestReadBackCatchesAMappingThatWentElsewhere(t *testing.T) {
	r := newFakeRouter(t)
	g := r.gateway(t)
	// the router accepts, then shows the manual forward's target
	r.hidden["TCP/8080"] = 0
	original := r.server.Config.Handler
	r.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		original.ServeHTTP(w, req)
		if r.count("AddPortMapping", "TCP/8080") == 1 {
			r.put("TCP/8080", "192.168.1.10", 80, "manual")
		}
	})
	var conflict *conflictError
	if err := g.claim("TCP", 8080, 2052, "Cosmos HTTP", 3600, false); !errors.As(err, &conflict) || conflict.Holder == nil {
		t.Errorf("claim = %v, want a conflict naming the holder", err)
	}
}

func TestFallsBackToAPermanentLease(t *testing.T) {
	r := newFakeRouter(t)
	r.onlyPermanent = true
	g := r.gateway(t)

	if err := g.claim("TCP", 8443, 2053, "Cosmos HTTPS", 3600, false); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if got, _ := r.get("TCP/8443"); got.lease != "0" {
		t.Errorf("lease = %q, want 0 after the router refused a leased mapping", got.lease)
	}
}

func TestListIsBoundedAndCleaned(t *testing.T) {
	r := newFakeRouter(t)
	g := r.gateway(t)
	r.put("UDP/5000", "192.168.1.20", 5000, "evil\r\nname <b>\u202e"+strings.Repeat("x", 200))
	entries, listed, truncated := g.list()
	if !listed || truncated || len(entries) != 1 {
		t.Fatalf("list = %d entries, listed %v, truncated %v", len(entries), listed, truncated)
	}
	e := entries[0]
	if e.Protocol != "UDP" || e.ExternalPort != 5000 || e.InternalClient != "192.168.1.20" || e.InternalPort != 5000 {
		t.Errorf("entry = %+v", e)
	}
	if len([]rune(e.Description)) > maxRouterText || strings.ContainsAny(e.Description, "\r\n\u202e") || !strings.HasPrefix(e.Description, "evil  name <b>") {
		t.Errorf("description not bounded and cleaned: %q", e.Description)
	}

	for port := 20000; port < 20000+maxListed+40; port++ {
		r.put("TCP/"+strconv.Itoa(port), "192.168.1.20", port, "many")
	}
	entries, listed, truncated = g.list()
	if !listed || !truncated || len(entries) != maxListed {
		t.Errorf("a long table: %d entries, listed %v, truncated %v", len(entries), listed, truncated)
	}

	r.sameForever = true
	before := r.count("GetGenericPortMappingEntry", "")
	entries, _, truncated = g.list()
	if len(entries) != 1 || !truncated || r.count("GetGenericPortMappingEntry", "")-before > 2 {
		t.Errorf("a router that repeats itself: %d entries, truncated %v", len(entries), truncated)
	}

	r.sameForever, r.noList = false, true
	if entries, listed, _ := g.list(); listed || len(entries) != 0 {
		t.Errorf("a router without the listing: %d entries, listed %v", len(entries), listed)
	}
}

func TestExternalIP(t *testing.T) {
	g := newFakeRouter(t).gateway(t)
	ip, err := g.externalIP()
	if err != nil || ip != "203.0.113.7" {
		t.Errorf("externalIP = %q, %v", ip, err)
	}
}

func TestParseDefaultGateway(t *testing.T) {
	routes := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"eno1\t000010AC\t00000000\t0001\t0\t0\t1002\t0000FFFF\t0\t0\t0\n" +
		"eno1\t00000000\t010010AC\t0003\t0\t0\t1002\t00000000\t0\t0\t0\n"
	gw, err := parseDefaultGateway(routes)
	if err != nil || gw.String() != "172.16.0.1" {
		t.Errorf("gateway = %v, %v; want 172.16.0.1", gw, err)
	}
	if _, err := parseDefaultGateway("Iface\tDestination\n"); err == nil {
		t.Error("expected an error when there is no default route")
	}
}

func TestParseSSDPLocation(t *testing.T) {
	ok := "HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=120\r\nST: upnp:rootdevice\r\nLocation: http://172.16.0.1:2828/gateway.xml\r\n\r\n"
	if loc, err := parseSSDPLocation([]byte(ok)); err != nil || loc != "http://172.16.0.1:2828/gateway.xml" {
		t.Errorf("location = %q, %v", loc, err)
	}
	for _, bad := range []string{"", "garbage", "HTTP/1.1 200 OK\r\n\r\n", "NOTIFY * HTTP/1.1\r\nLocation: http://x/\r\n\r\n"} {
		if _, err := parseSSDPLocation([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestDesiredMappings(t *testing.T) {
	selfSigned := utils.HTTPSCertModeList["SELFSIGNED"]
	disabled := utils.HTTPSCertModeList["DISABLED"]
	base := func(mode string, u utils.UPnPConfig) utils.HTTPConfig {
		return utils.HTTPConfig{HTTPPort: "2052", HTTPSPort: "2053", HTTPSCertificateMode: mode, UPnP: u}
	}
	describe := func(ms []Mapping) string {
		parts := []string{}
		for _, m := range ms {
			parts = append(parts, fmt.Sprintf("%s:%d>%d", m.Name, m.ExternalPort, m.InternalPort))
		}
		return strings.Join(parts, " ")
	}

	cases := []struct {
		name    string
		cfg     utils.HTTPConfig
		want    string
		wantErr bool
	}{
		{"off", base(selfSigned, utils.UPnPConfig{}), "", false},
		{"off ignores other fields", base(selfSigned, utils.UPnPConfig{ForwardHTTP: true}), "", false},
		{"https only by default", base(selfSigned, utils.UPnPConfig{Enabled: true}), "HTTPS:2053>2053", false},
		{"http only when asked", base(selfSigned, utils.UPnPConfig{Enabled: true, ForwardHTTP: true}), "HTTPS:2053>2053 HTTP:2052>2052", false},
		{"custom external port", base(selfSigned, utils.UPnPConfig{Enabled: true, ExternalHTTPSPort: " 443 "}), "HTTPS:443>2053", false},
		{"https disabled forwards nothing", base(disabled, utils.UPnPConfig{Enabled: true}), "", true},
		{"https disabled, http asked", base(disabled, utils.UPnPConfig{Enabled: true, ForwardHTTP: true}), "HTTP:2052>2052", false},
		{"invalid external port", base(selfSigned, utils.UPnPConfig{Enabled: true, ExternalHTTPSPort: "70000"}), "", true},
		{"not a number", base(selfSigned, utils.UPnPConfig{Enabled: true, ExternalHTTPSPort: "443; rm"}), "", true},
		{"same external port twice", base(selfSigned, utils.UPnPConfig{Enabled: true, ForwardHTTP: true, ExternalHTTPSPort: "8443", ExternalHTTPPort: "8443"}), "", true},
	}
	for _, c := range cases {
		got, err := desiredMappings(c.cfg)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", c.name, err, c.wantErr)
		}
		if describe(got) != c.want {
			t.Errorf("%s: got %q, want %q", c.name, describe(got), c.want)
		}
	}
}

func TestPrivateAddressNote(t *testing.T) {
	for _, ip := range []string{"192.168.1.2", "10.0.0.1", "172.16.5.5", "100.64.1.1"} {
		if privateAddressNote(ip) == "" {
			t.Errorf("%s should be flagged as not public", ip)
		}
	}
	for _, ip := range []string{"203.0.113.7", "8.8.8.8", "", "not an ip"} {
		if privateAddressNote(ip) != "" {
			t.Errorf("%q should not be flagged", ip)
		}
	}
}
