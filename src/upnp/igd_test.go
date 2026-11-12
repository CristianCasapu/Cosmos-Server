package upnp

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// fakeRouter is an in-process UPnP IGD: a description document and a SOAP
// control endpoint backed by a table of port forwards.
type fakeRouter struct {
	server        *httptest.Server
	forwards      map[string]string // "TCP/8443" -> internal client
	leases        map[string]string // "TCP/8443" -> requested lease
	onlyPermanent bool              // refuse leased mappings, like some routers do
	controlURL    string            // what the description advertises
}

func newFakeRouter(t *testing.T) *fakeRouter {
	t.Helper()
	r := &fakeRouter{forwards: map[string]string{}, leases: map[string]string{}, controlURL: "/ctl/ipconn"}
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

func (r *fakeRouter) control(w http.ResponseWriter, req *http.Request) {
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

	action := strings.Trim(req.Header.Get("SOAPAction"), `"`)
	switch action[strings.Index(action, "#")+1:] {
	case "GetExternalIPAddress":
		reply("<NewExternalIPAddress>203.0.113.7</NewExternalIPAddress>")
	case "GetSpecificPortMappingEntry":
		if client, ok := r.forwards[key]; ok {
			reply("<NewInternalPort>1</NewInternalPort><NewInternalClient>" + client + "</NewInternalClient>")
		} else {
			fault(714, "NoSuchEntryInArray")
		}
	case "AddPortMapping":
		if r.onlyPermanent && args["NewLeaseDuration"] != "0" {
			fault(725, "OnlyPermanentLeasesSupported")
			return
		}
		r.forwards[key] = args["NewInternalClient"]
		r.leases[key] = args["NewLeaseDuration"]
		reply("")
	case "DeletePortMapping":
		if _, ok := r.forwards[key]; !ok {
			fault(714, "NoSuchEntryInArray")
			return
		}
		delete(r.forwards, key)
		reply("")
	default:
		fault(401, "Invalid Action")
	}
}

var loopback = net.ParseIP("127.0.0.1")

func (r *fakeRouter) gateway(t *testing.T) *gateway {
	t.Helper()
	g, err := loadGateway(r.server.URL+"/desc.xml", loopback)
	if err != nil {
		t.Fatalf("loadGateway: %v", err)
	}
	g.localIP = net.ParseIP("192.168.1.50")
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

func TestAddRenewAndRemoveMapping(t *testing.T) {
	r := newFakeRouter(t)
	g := r.gateway(t)

	for i := 0; i < 2; i++ { // the second call is a renewal of our own mapping
		if err := g.addMapping("TCP", 8443, 2053, "Cosmos HTTPS", 3600); err != nil {
			t.Fatalf("addMapping (call %d): %v", i+1, err)
		}
	}
	if got := r.forwards["TCP/8443"]; got != "192.168.1.50" {
		t.Fatalf("router forwards 8443 to %q", got)
	}
	if got := r.leases["TCP/8443"]; got != "3600" {
		t.Errorf("lease = %q, want a leased (non-permanent) mapping", got)
	}

	if err := g.removeMapping("TCP", 8443); err != nil {
		t.Fatalf("removeMapping: %v", err)
	}
	if _, still := r.forwards["TCP/8443"]; still {
		t.Error("mapping still present after removeMapping")
	}
	if err := g.removeMapping("TCP", 8443); err != nil {
		t.Errorf("removing a mapping that is already gone: %v", err)
	}
}

func TestAnotherDevicesMappingIsLeftAlone(t *testing.T) {
	r := newFakeRouter(t)
	r.forwards["TCP/8443"] = "192.168.1.99"
	g := r.gateway(t)

	if err := g.addMapping("TCP", 8443, 2053, "Cosmos HTTPS", 3600); err == nil {
		t.Error("took over a port that is forwarded to another device")
	}
	if err := g.removeMapping("TCP", 8443); err != nil {
		t.Errorf("removeMapping: %v", err)
	}
	if got := r.forwards["TCP/8443"]; got != "192.168.1.99" {
		t.Errorf("another device's mapping was changed: now %q", got)
	}
}

func TestFallsBackToAPermanentLease(t *testing.T) {
	r := newFakeRouter(t)
	r.onlyPermanent = true
	g := r.gateway(t)

	if err := g.addMapping("TCP", 8443, 2053, "Cosmos HTTPS", 3600); err != nil {
		t.Fatalf("addMapping: %v", err)
	}
	if got := r.leases["TCP/8443"]; got != "0" {
		t.Errorf("lease = %q, want 0 after the router refused a leased mapping", got)
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
