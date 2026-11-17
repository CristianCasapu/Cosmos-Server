package proxy

// Fork addition (plan 49): where the visitor is, as advice for the links the
// panel shows: at home, through a VPN, or away.
//
// An app usually has several addresses: a .local name, a name for use at
// home, a public name. Which of them opens depends on where the visitor is,
// not on the name the page was opened under (the installed app on a phone
// keeps the public name at home too). The answer is advice for the screens
// only: nothing is allowed or refused on it, the guards of fork_local.go and
// fork_home_names.go decide access as before.
//
// The visitor is the address every restriction uses (ClientID, set by
// utils.ClientRealIP: the peer, or what a trusted proxy or Cloudflare
// reported under the fork's rules). A header the visitor writes is never
// read here. In order:
//
//  1. loopback: home, sure;
//  2. a VPN: 100.64.0.0/10 (Tailscale and other carrier-NAT VPNs),
//     Tailscale's IPv6 range, or a network of one of the server's VPN
//     interfaces: vpn, sure (the server's addresses answer, .local names do
//     not resolve there);
//  3. private IPv4, IPv6 unique-local, link-local, or inside a network of
//     one of the server's other interfaces (the home's global IPv6 prefix):
//     home, sure;
//  4. the home's own public address (the router's or the DNS records'): a
//     device in the house that came in through the public name, by the
//     router's loopback or through Cloudflare: home, not sure (other
//     subscribers may share that address behind a carrier NAT);
//  5. anything else: away.

import (
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// VisitorPlace is what /api/status tells the panel about its visitor: two
// values, nothing about the networks themselves.
type VisitorPlace struct {
	Where string `json:"where"` // "home", "vpn" or "away"
	Sure  bool   `json:"sure"`
}

const (
	PlaceHome = "home"
	PlaceVPN  = "vpn"
	PlaceAway = "away"
)

// vpnInterfaceName matches the names VPN software gives its interfaces
// (the VPN part of virtualInterface in fork_mdns_ifaces.go). Not ppp: a
// server that dials PPPoE itself has its way out there. A tunN interface
// (OpenVPN) counts only when it does not carry the default route (a server
// whose internet goes through a tunnel would otherwise call the internet a
// VPN); tunl0 (IP-in-IP) is not one.
var vpnInterfaceName = regexp.MustCompile(`^(tailscale|wg|tun[0-9]|tap[0-9]|zt|nebula|ztun|utun|ipsec)`)

// isVPNInterface is the rule above; defaultRoutes are the interfaces that
// carry a default route (fork_default_routes.go).
func isVPNInterface(name string, defaultRoutes map[string]bool) bool {
	name = strings.ToLower(name)
	if !vpnInterfaceName.MatchString(name) {
		return false
	}
	if strings.HasPrefix(name, "tun") {
		for iface := range defaultRoutes {
			if strings.ToLower(iface) == name {
				return false
			}
		}
	}
	return true
}

var tailscaleIPv6 = mustCIDR("fd7a:115c:a1e0::/48")

// placeNetworks are the server's networks, split into VPN and the others.
type placeNetworks struct {
	vpn  []*net.IPNet
	home []*net.IPNet
}

var (
	placeNetsMu   sync.Mutex
	placeNets     *placeNetworks
	placeNetsRead time.Time
)

// readPlaceNetworks is a variable so that tests give the interfaces.
var readPlaceNetworks = func() placeNetworks {
	out := placeNetworks{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	defaultRoutes := defaultRouteInterfaces()
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		vpn := isVPNInterface(iface.Name, defaultRoutes)
		for _, addr := range addrs {
			network, ok := addr.(*net.IPNet)
			if !ok || network.IP.IsLoopback() {
				continue
			}
			if vpn {
				out.vpn = append(out.vpn, network)
			} else {
				out.home = append(out.home, network)
			}
		}
	}
	return out
}

// serverPlaceNetworks reads the interfaces again at most once a minute.
func serverPlaceNetworks() placeNetworks {
	placeNetsMu.Lock()
	defer placeNetsMu.Unlock()
	if placeNets != nil && time.Since(placeNetsRead) < time.Minute {
		return *placeNets
	}
	nets := readPlaceNetworks()
	placeNets, placeNetsRead = &nets, time.Now()
	return nets
}

func inAny(ip net.IP, networks []*net.IPNet) bool {
	for _, network := range networks {
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

// classifyPlace is the rule above for one address. publicIPs are the home's
// own public addresses ("" entries are ignored).
func classifyPlace(address string, nets placeNetworks, publicIPs []string) VisitorPlace {
	address = strings.TrimSpace(address)
	if i := strings.IndexByte(address, '%'); i >= 0 {
		address = address[:i] // an IPv6 zone
	}
	ip := net.ParseIP(strings.Trim(address, "[]"))
	if ip == nil {
		return VisitorPlace{Where: PlaceAway, Sure: false}
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() {
		return VisitorPlace{Where: PlaceHome, Sure: true}
	}
	if sharedAddresses.Contains(ip) || tailscaleIPv6.Contains(ip) || inAny(ip, nets.vpn) {
		return VisitorPlace{Where: PlaceVPN, Sure: true}
	}
	if ip.IsPrivate() || ip.IsLinkLocalUnicast() || inAny(ip, nets.home) {
		return VisitorPlace{Where: PlaceHome, Sure: true}
	}
	for _, public := range publicIPs {
		if p := net.ParseIP(strings.TrimSpace(public)); p != nil && p.Equal(ip) && !p.IsPrivate() && !p.IsLoopback() {
			return VisitorPlace{Where: PlaceHome, Sure: false}
		}
	}
	return VisitorPlace{Where: PlaceAway, Sure: true}
}

// PlaceOf is where the visitor of a request is. publicIPs are the home's own
// public addresses as the server knows them.
func PlaceOf(req *http.Request, publicIPs []string) VisitorPlace {
	visitor, _ := req.Context().Value("ClientID").(string)
	if visitor == "" {
		visitor = utils.GetClientIP(req)
	}
	return classifyPlace(visitor, serverPlaceNetworks(), publicIPs)
}
