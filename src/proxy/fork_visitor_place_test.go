package proxy

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func cidrs(t *testing.T, list ...string) []*net.IPNet {
	t.Helper()
	out := []*net.IPNet{}
	for _, c := range list {
		ip, network, err := net.ParseCIDR(c)
		if err != nil {
			t.Fatal(err)
		}
		network.IP = ip // an interface address carries its own IP, as net.Interface.Addrs gives it
		out = append(out, network)
	}
	return out
}

// A server shaped like a home server: a LAN with an IPv4 and a global IPv6
// prefix, a Docker bridge, and a WireGuard interface on a range of its own.
func testPlaceNets(t *testing.T) placeNetworks {
	return placeNetworks{
		home: cidrs(t, "192.168.1.10/24", "2001:db8:1:2::10/64", "172.17.0.1/16"),
		vpn:  cidrs(t, "10.99.0.1/24", "fd00:99::1/64"),
	}
}

func TestClassifyPlace(t *testing.T) {
	nets := testPlaceNets(t)
	public := []string{"203.0.113.7", ""}
	cases := []struct {
		address string
		where   string
		sure    bool
	}{
		{"127.0.0.1", PlaceHome, true},
		{"::1", PlaceHome, true},
		{"192.168.1.42", PlaceHome, true},
		{"10.1.2.3", PlaceHome, true},             // another private range: still at home
		{"172.17.0.5", PlaceHome, true},           // a container on the server
		{"169.254.10.1", PlaceHome, true},         // link-local
		{"fe80::1", PlaceHome, true},              // link-local IPv6
		{"fe80::1%eth0", PlaceHome, true},         // with a zone
		{"fd12:3456::9", PlaceHome, true},         // unique-local IPv6
		{"2001:db8:1:2::abcd", PlaceHome, true},   // the home's global IPv6 prefix
		{"[2001:db8:1:2::abcd]", PlaceHome, true}, // in brackets
		{"::ffff:192.168.1.42", PlaceHome, true},  // mapped IPv4
		{"100.64.0.1", PlaceVPN, true},            // carrier-NAT range (Tailscale)
		{"100.101.102.103", PlaceVPN, true},
		{"100.127.255.254", PlaceVPN, true},
		{"fd7a:115c:a1e0::1234", PlaceVPN, true}, // Tailscale IPv6, though unique-local
		{"10.99.0.7", PlaceVPN, true},            // a WireGuard interface's range, though private
		{"fd00:99::7", PlaceVPN, true},
		{"100.128.0.1", PlaceAway, true}, // just outside the carrier-NAT range
		{"100.63.255.255", PlaceAway, true},
		{"203.0.113.7", PlaceHome, false}, // the home's own public address: hairpin or Cloudflare
		{"198.51.100.20", PlaceAway, true},
		{"2001:db8:9::1", PlaceAway, true},   // another IPv6 prefix
		{"2001:db8:1:3::1", PlaceAway, true}, // the next /64 is not the home's
		{"", PlaceAway, false},
		{"not an address", PlaceAway, false},
		{"192.168.1.42, 10.0.0.1", PlaceAway, false},
	}
	for _, c := range cases {
		got := classifyPlace(c.address, nets, public)
		if got.Where != c.where || got.Sure != c.sure {
			t.Errorf("classifyPlace(%q) = %+v, want %s sure=%v", c.address, got, c.where, c.sure)
		}
	}
}

func TestClassifyPlaceIgnoresPrivatePublicAddresses(t *testing.T) {
	// an "advertised address" that is private (iface:) is not a public one
	if got := classifyPlace("198.51.100.20", placeNetworks{}, []string{"192.168.1.10", "198.51.100.21"}); got.Where != PlaceAway {
		t.Errorf("got %+v, want away", got)
	}
}

// The visitor is the address the restrictions use; a header the visitor
// writes never moves it.
func TestPlaceOfUsesTheVisitorAddress(t *testing.T) {
	saved := readPlaceNetworks
	placeNetsMu.Lock()
	placeNets = nil
	placeNetsMu.Unlock()
	readPlaceNetworks = func() placeNetworks { return testPlaceNets(t) }
	t.Cleanup(func() {
		readPlaceNetworks = saved
		placeNetsMu.Lock()
		placeNets = nil
		placeNetsMu.Unlock()
		utils.LoadBaseMainConfig(utils.Config{})
	})
	utils.LoadBaseMainConfig(utils.Config{})

	place := func(remote string, headers map[string]string) VisitorPlace {
		var got VisitorPlace
		handler := utils.ClientRealIP(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			got = PlaceOf(req, []string{"203.0.113.7"})
		}))
		req := httptest.NewRequest("GET", "https://example.org/cosmos/api/status", nil)
		req.RemoteAddr = remote
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		handler.ServeHTTP(httptest.NewRecorder(), req)
		return got
	}

	forged := map[string]string{
		"X-Forwarded-For":  "192.168.1.5",
		"X-Real-IP":        "192.168.1.5",
		"CF-Connecting-IP": "192.168.1.5",
		"Forwarded":        "for=192.168.1.5",
		"True-Client-IP":   "192.168.1.5",
	}
	if got := place("198.51.100.20:4000", forged); got.Where != PlaceAway {
		t.Errorf("a visitor from the internet with forged headers is %+v, want away", got)
	}
	if got := place("198.51.100.20:4000", map[string]string{"X-Forwarded-For": "100.64.0.9"}); got.Where != PlaceAway {
		t.Errorf("a forged VPN address counted: %+v", got)
	}
	if got := place("192.168.1.42:4000", nil); got.Where != PlaceHome || !got.Sure {
		t.Errorf("a home visitor is %+v", got)
	}
	if got := place("[2001:db8:1:2::abcd]:4000", nil); got.Where != PlaceHome {
		t.Errorf("a home IPv6 visitor is %+v", got)
	}
	if got := place("100.70.1.2:4000", nil); got.Where != PlaceVPN {
		t.Errorf("a Tailscale visitor is %+v", got)
	}
	// a peer at home that is not a trusted proxy: its own address counts, not what it says
	if got := place("192.168.1.2:4000", map[string]string{"X-Forwarded-For": "198.51.100.20"}); got.Where != PlaceHome {
		t.Errorf("an untrusted home peer's header counted: %+v", got)
	}

	// behind a trusted proxy at home, the visitor that proxy saw
	config := utils.Config{}
	config.HTTPConfig.TrustedProxies = []string{"192.168.1.2"}
	utils.LoadBaseMainConfig(config)
	if got := place("192.168.1.2:4000", map[string]string{"X-Forwarded-For": "198.51.100.20"}); got.Where != PlaceAway {
		t.Errorf("the visitor behind a trusted proxy is %+v, want away", got)
	}
	if got := place("192.168.1.2:4000", map[string]string{"X-Forwarded-For": "203.0.113.7"}); got.Where != PlaceHome || got.Sure {
		t.Errorf("the home's own public address behind the proxy is %+v, want home, not sure", got)
	}
	// a forged entry on the left of the proxy's own is not the visitor
	if got := place("192.168.1.2:4000", map[string]string{"X-Forwarded-For": "192.168.1.5, 198.51.100.20"}); got.Where != PlaceAway {
		t.Errorf("a forged left entry counted: %+v", got)
	}
}

func TestVPNInterfaceNames(t *testing.T) {
	eth := map[string]bool{"eth0": true}
	for _, name := range []string{"tailscale0", "wg0", "tun0", "tun12", "zt12345", "nebula1", "utun3"} {
		if !isVPNInterface(name, eth) {
			t.Errorf("%s is not taken for a VPN", name)
		}
	}
	for _, name := range []string{"eth0", "enp3s0", "eno1", "br-0123456789ab", "docker0", "wlan0", "bond0", "ppp0", "pppoe-wan", "tunl0"} {
		if isVPNInterface(name, eth) {
			t.Errorf("%s is taken for a VPN", name)
		}
	}
	// a server whose way out is the tunnel: that tunnel is not a VPN of visitors
	if isVPNInterface("tun0", map[string]bool{"tun0": true}) {
		t.Errorf("the default route's tunnel is taken for a VPN")
	}
	if !isVPNInterface("wg0", map[string]bool{"wg0": true}) {
		t.Errorf("WireGuard on the default route is still a VPN interface")
	}
	if !isVPNInterface("tun0", nil) {
		t.Errorf("without default routes known, tun0 is a VPN interface")
	}
}

func TestDefaultRoutes(t *testing.T) {
	v4 := strings.NewReader(`Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	00000000	0101A8C0	0003	0	0	100	00000000	0	0	0
eth0	0001A8C0	00000000	0001	0	0	100	00FFFFFF	0	0	0
tun0	00000000	0100080A	0003	0	0	0	00000080	0	0	0
tun0	00000080	0100080A	0003	0	0	0	00000080	0	0	0
tun1	00000000	0100090A	0003	0	0	0	00000080	0	0	0
`)
	v6 := strings.NewReader(`00000000000000000000000000000000 00 00000000000000000000000000000000 00 fe800000000000000000000000000001 00000400 00000001 00000000 00000003 wg6
00000000000000000000000000000000 01 00000000000000000000000000000000 00 00000000000000000000000000000000 00000400 00000001 00000000 00000001 tun5
80000000000000000000000000000000 01 00000000000000000000000000000000 00 00000000000000000000000000000000 00000400 00000001 00000000 00000001 tun5
00000000000000000000000000000000 00 00000000000000000000000000000000 00 00000000000000000000000000000000 ffffffff 00000001 00000000 00200200 lo
20010db8000100020000000000000000 40 00000000000000000000000000000000 00 00000000000000000000000000000000 00000100 00000001 00000000 00000001 eth0
`)
	got := parseDefaultRoutes(v4, v6)
	want := map[string]bool{"eth0": true, "tun0": true, "wg6": true, "tun5": true}
	if len(got) != len(want) {
		t.Errorf("default routes %v, want %v", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("%s carries a default route and was not seen (%v)", k, got)
		}
	}
	if got["tun1"] {
		t.Errorf("one half of def1 is not a default route")
	}
	if len(parseDefaultRoutes(nil, nil)) != 0 {
		t.Errorf("nothing to read gives routes")
	}
	if isVPNInterface("tun0", got) || !isVPNInterface("tun1", got) || isVPNInterface("tun5", got) {
		t.Errorf("def1 (IPv4 or IPv6) on a tunnel makes it the way out, one half does not")
	}
}

// Through Cloudflare (a shielded name, origin pulls off): the visitor is the
// one public address Cloudflare names; anything else is Cloudflare's own
// address, away.
func TestPlaceOfThroughCloudflare(t *testing.T) {
	saved := readPlaceNetworks
	placeNetsMu.Lock()
	placeNets = nil
	placeNetsMu.Unlock()
	readPlaceNetworks = func() placeNetworks { return testPlaceNets(t) }
	t.Cleanup(func() {
		readPlaceNetworks = saved
		placeNetsMu.Lock()
		placeNets = nil
		placeNetsMu.Unlock()
		utils.LoadBaseMainConfig(utils.Config{})
	})
	config := utils.Config{}
	config.HTTPConfig.Cloudflare.Shielded = []string{"app.example.org"}
	config.HTTPConfig.Cloudflare.TrustRanges = true
	utils.LoadBaseMainConfig(config)

	place := func(host, peer string, cf ...string) VisitorPlace {
		var got VisitorPlace
		handler := utils.ClientRealIP(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			got = PlaceOf(req, []string{"203.0.113.7"})
		}))
		req := httptest.NewRequest("GET", "https://"+host+"/cosmos/api/status", nil)
		req.RemoteAddr = peer
		for _, v := range cf {
			req.Header.Add("CF-Connecting-IP", v)
		}
		handler.ServeHTTP(httptest.NewRecorder(), req)
		return got
	}
	edge := "173.245.48.5:4000"
	cases := []struct {
		what  string
		host  string
		peer  string
		cf    []string
		where string
		sure  bool
	}{
		{"a visitor from the internet", "app.example.org", edge, []string{"198.51.100.20"}, PlaceAway, true},
		{"the home's own public address (a phone at home through Cloudflare)", "app.example.org", edge, []string{"203.0.113.7"}, PlaceHome, false},
		{"the home's IPv6 prefix", "app.example.org", edge, []string{"2001:db8:1:2::abcd"}, PlaceHome, true},
		{"Cloudflare claiming a private visitor", "app.example.org", edge, []string{"192.168.1.42"}, PlaceAway, true},
		{"Cloudflare claiming a carrier-NAT visitor", "app.example.org", edge, []string{"100.64.0.9"}, PlaceAway, true},
		{"two values", "app.example.org", edge, []string{"203.0.113.7", "198.51.100.20"}, PlaceAway, true},
		{"a name that is not shielded", "direct.example.org", edge, []string{"203.0.113.7"}, PlaceAway, true},
		{"an IPv4-mapped IPv6 peer at home", "app.example.org", "[::ffff:192.168.1.42]:4000", nil, PlaceHome, true},
		{"an IPv4-mapped IPv6 peer from the internet", "app.example.org", "[::ffff:198.51.100.20]:4000", nil, PlaceAway, true},
	}
	for _, c := range cases {
		got := place(c.host, c.peer, c.cf...)
		if got.Where != c.where || got.Sure != c.sure {
			t.Errorf("%s: %+v, want %s sure=%v", c.what, got, c.where, c.sure)
		}
	}
}
