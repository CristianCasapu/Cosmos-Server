package proxy

// Fork addition: the interfaces the .local names are published on, see
// fork_mdns.go.
//
// Automatically: the interfaces that face the local network. Up, multicast,
// not loopback nor point-to-point, not one of the virtual families (Docker
// bridges, veth pairs, VPNs, VM bridges), with a private IPv4 address. The
// interface of the default route comes first and qualifies with any IPv4
// address. HTTPConfig.MDNSInterfaces overrides the choice.

import (
	"bufio"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
)

type lanInterface struct {
	Name    string `json:"name"`
	Index   int    `json:"-"`
	Address string `json:"address"`
	Auto    bool   `json:"auto"` // chosen automatically, not named in the settings
	Default bool   `json:"defaultRoute"`
}

// A problem with the interfaces, for the panel to put in words.
type mdnsProblem struct {
	Code string `json:"code"` // noInterface, notFound, down, noIPv4
	Name string `json:"name,omitempty"`
}

// What the selection needs to know about an interface; the tests replace
// the system's.
type systemInterface struct {
	Name  string
	Index int
	Flags net.Flags
	IPv4  []net.IP
}

var (
	mdnsSystemInterfaces = readSystemInterfaces
	mdnsDefaultRoute     = defaultRouteInterface
)

func readSystemInterfaces() ([]systemInterface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := []systemInterface{}
	for _, iface := range ifaces {
		entry := systemInterface{Name: iface.Name, Index: iface.Index, Flags: iface.Flags}
		if addrs, err := iface.Addrs(); err == nil {
			for _, addr := range addrs {
				if network, ok := addr.(*net.IPNet); ok {
					if ip := network.IP.To4(); ip != nil {
						entry.IPv4 = append(entry.IPv4, ip)
					}
				}
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

// defaultRouteInterface reads the interface of the IPv4 default route from
// /proc/net/route ("" when there is none or the file cannot be read).
func defaultRouteInterface() string {
	file, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer file.Close()
	return parseRouteTable(file)
}

// parseRouteTable picks the interface of the lowest-metric default route.
func parseRouteTable(table io.Reader) string {
	best, bestMetric := "", -1
	scanner := bufio.NewScanner(io.LimitReader(table, 1<<20))
	for lines := 0; scanner.Scan() && lines < 4096; lines++ {
		fields := strings.Fields(scanner.Text())
		// Iface Destination Gateway Flags RefCnt Use Metric Mask ...
		if len(fields) < 8 || fields[1] != "00000000" || fields[7] != "00000000" {
			continue
		}
		metric := 0
		for _, c := range fields[6] {
			if c < '0' || c > '9' {
				metric = -1
				break
			}
			metric = metric*10 + int(c-'0')
			if metric > 1<<20 {
				break
			}
		}
		if metric < 0 {
			continue
		}
		if bestMetric == -1 || metric < bestMetric {
			best, bestMetric = fields[0], metric
		}
	}
	return best
}

// Interfaces of containers, VPNs and virtual machines: never the local network.
var virtualInterface = regexp.MustCompile(`^(docker|br-[0-9a-f]{12}$|veth|virbr|vnet|tailscale|wg|tun|tap|zt|cni|flannel|cali|cilium|vxlan|lxcbr|lxdbr|podman|kube|vboxnet|vmnet|nebula|ztun|utun|ipsec|ppp)`)

var (
	linkLocalRange = mustCIDR("169.254.0.0/16")
)

// usableIPv4 returns the first address of an interface that can be published
// (not link-local, not the carrier-NAT range VPNs use); private ones only
// unless anyPrivate is false.
func usableIPv4(ips []net.IP, privateOnly bool) net.IP {
	for _, ip := range ips {
		if ip.IsLoopback() || linkLocalRange.Contains(ip) || sharedAddresses.Contains(ip) || ip.IsUnspecified() {
			continue
		}
		if privateOnly && !ip.IsPrivate() {
			continue
		}
		return ip
	}
	return nil
}

// validMDNSInterfaceName is what the settings accept as an interface name.
var validMDNSInterfaceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@-]{0,14}$`)

const mdnsMaxOverride = 8

// chooseLANInterfaces returns the interfaces to publish on, the default
// route's first, and what keeps named ones from being used.
func chooseLANInterfaces(override []string) ([]lanInterface, []mdnsProblem) {
	system, err := mdnsSystemInterfaces()
	if err != nil {
		return nil, []mdnsProblem{{Code: "noInterface"}}
	}
	byName := map[string]systemInterface{}
	for _, iface := range system {
		byName[iface.Name] = iface
	}
	defaultIface := mdnsDefaultRoute()

	chosen := []lanInterface{}
	problems := []mdnsProblem{}
	up := func(iface systemInterface) bool {
		return iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0
	}

	if len(override) > 0 {
		seen := map[string]bool{}
		for i, name := range override {
			if i >= mdnsMaxOverride || !validMDNSInterfaceName.MatchString(name) || seen[name] {
				continue
			}
			seen[name] = true
			iface, ok := byName[name]
			switch {
			case !ok:
				problems = append(problems, mdnsProblem{Code: "notFound", Name: name})
			case !up(iface):
				problems = append(problems, mdnsProblem{Code: "down", Name: name})
			default:
				ip := usableIPv4(iface.IPv4, false)
				if ip == nil {
					problems = append(problems, mdnsProblem{Code: "noIPv4", Name: name})
					continue
				}
				chosen = append(chosen, lanInterface{Name: name, Index: iface.Index, Address: ip.String(), Default: name == defaultIface})
			}
		}
	} else {
		candidate := func(iface systemInterface) bool {
			return up(iface) && iface.Flags&net.FlagMulticast != 0 && iface.Flags&net.FlagPointToPoint == 0 && !virtualInterface.MatchString(iface.Name)
		}
		if iface, ok := byName[defaultIface]; ok && candidate(iface) {
			if ip := usableIPv4(iface.IPv4, false); ip != nil {
				chosen = append(chosen, lanInterface{Name: iface.Name, Index: iface.Index, Address: ip.String(), Auto: true, Default: true})
			}
		}
		for _, iface := range system {
			if iface.Name == defaultIface || !candidate(iface) {
				continue
			}
			if ip := usableIPv4(iface.IPv4, true); ip != nil {
				chosen = append(chosen, lanInterface{Name: iface.Name, Index: iface.Index, Address: ip.String(), Auto: true})
			}
		}
	}
	if len(chosen) == 0 {
		problems = append(problems, mdnsProblem{Code: "noInterface"})
	}
	return chosen, problems
}
