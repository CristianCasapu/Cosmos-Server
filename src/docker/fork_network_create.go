package docker

// Fork addition: a new network is checked before Docker is asked.
//
// The dialog offered drivers Docker cannot create here ("host" exists once,
// "overlay" needs a swarm, "none" is not a driver to create), took any text
// as a subnet and as the parent network card of a macvlan network, and a
// macvlan network without its card was created without one. Docker's own
// refusal came back as a raw error. Now: a name Docker accepts, the driver
// bridge (or empty, Docker's default bridge), macvlan or ipvlan, a subnet in
// CIDR form or none, and for macvlan/ipvlan the name of a network card.

import (
	"net"
	"regexp"
	"strings"
)

var (
	networkNamePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)
	interfaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@-]{0,14}$`)
)

// checkNetworkPayload says what is wrong with a request to create a network,
// "" when Docker may be asked. It trims the fields it keeps.
func checkNetworkPayload(p *createNetworkPayload) string {
	p.Name, p.Driver = strings.TrimSpace(p.Name), strings.TrimSpace(p.Driver)
	p.Subnet, p.ParentInterface = strings.TrimSpace(p.Subnet), strings.TrimSpace(p.ParentInterface)
	if !networkNamePattern.MatchString(p.Name) {
		return "A network name is 1 to 63 letters, digits, dots, dashes or underscores, starting with a letter or a digit, for example media-net"
	}
	switch p.Driver {
	case "", "bridge":
		if p.ParentInterface != "" {
			return "Only a macvlan or ipvlan network has a parent network card"
		}
	case "macvlan", "ipvlan":
		if !interfaceNamePattern.MatchString(p.ParentInterface) {
			return "A " + p.Driver + " network needs the name of the server's network card it sits on, for example eth0 or eno1"
		}
	default:
		return "The driver must be bridge, macvlan or ipvlan: Docker has only one host network, and overlay networks need a swarm"
	}
	if p.Subnet != "" {
		ip, network, err := net.ParseCIDR(p.Subnet)
		if err != nil || ip.To4() == nil || !ip.Equal(network.IP) {
			return "A subnet is written like 172.30.0.0/24 (an IPv4 network address and its size), or left empty for Docker to pick a free one"
		}
	}
	return ""
}
