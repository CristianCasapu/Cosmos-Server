package docker

// Fork addition (plan 47, I2c, second review S3): a container made again by
// an edit keeps its network settings. Upstream created the new container
// without its endpoint settings and connected the other networks with empty
// ones, so every save of a tab lost the network aliases (a compose stack's
// service names), a static address (a macvlan app with a fixed LAN address
// moved), the MAC address and the network's links and driver options, and on
// the default bridge it replaced a hostname given with --hostname by the
// container's name. Here the new container is created on its main network
// with the endpoint the old one had, the other networks are connected with
// theirs, and a hostname of the container's own is kept. A container that
// never had such settings is treated as upstream treats it (the name as
// hostname on the bridge, no alias, a dynamic address).

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/docker/docker/api/types"
	containerType "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
)

// primaryNetwork is the network a network mode creates the container on, "" for none.
func primaryNetwork(mode containerType.NetworkMode) string {
	m := string(mode)
	switch {
	case m == "" || m == "default" || m == "bridge":
		return "bridge"
	case m == "host" || m == "none" || strings.HasPrefix(m, "container:") || strings.HasPrefix(m, "service:"):
		return ""
	}
	return m
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// keptEndpoint is what a container had on a network that the new one keeps:
// its own aliases (without the short ID Docker adds), static addresses,
// links, driver options and MAC address. nil when it was not on that network.
func keptEndpoint(old types.ContainerJSON, name string) *network.EndpointSettings {
	if old.NetworkSettings == nil || old.NetworkSettings.Networks == nil {
		return nil
	}
	stored, ok := old.NetworkSettings.Networks[name]
	if !ok || stored == nil {
		return nil
	}
	// No MAC address: inspect reports the one a running container got, chosen by Docker or not; one
	// its owner set is in Config.MacAddress, which the edit keeps (third review m-1: a plain container
	// gets a new one at each creation, as upstream)
	ep := &network.EndpointSettings{}
	if name == "bridge" {
		// the default bridge takes no aliases, links by network or static addresses
		return ep
	}
	id := ""
	if old.ContainerJSONBase != nil {
		id = old.ID
	}
	for _, alias := range stored.Aliases {
		if alias != "" && alias != shortID(id) && alias != id {
			ep.Aliases = append(ep.Aliases, alias)
		}
	}
	if c := stored.IPAMConfig; c != nil && (c.IPv4Address != "" || c.IPv6Address != "" || len(c.LinkLocalIPs) > 0) {
		copied := *c
		ep.IPAMConfig = &copied
	}
	ep.Links = append(ep.Links, stored.Links...)
	if len(stored.DriverOpts) > 0 {
		ep.DriverOpts = map[string]string{}
		for k, v := range stored.DriverOpts {
			ep.DriverOpts[k] = v
		}
	}
	return ep
}

func emptyEndpoint(ep *network.EndpointSettings) bool {
	return len(ep.Aliases) == 0 && ep.IPAMConfig == nil && len(ep.Links) == 0 && len(ep.DriverOpts) == 0 && ep.MacAddress == ""
}

// forkNetworkingConfig is what the new container is created with on its main network.
func forkNetworkingConfig(old types.ContainerJSON, config *containerType.Config, mode containerType.NetworkMode) *network.NetworkingConfig {
	name := primaryNetwork(mode)
	if name == "" {
		return nil
	}
	ep := keptEndpoint(old, name)
	if ep != nil {
		fitAddresses(old.Name, name, ep)
	}
	if ep == nil || emptyEndpoint(ep) {
		return nil // nothing of its own: exactly as upstream creates it (a MAC address its owner set stays in Config)
	}
	// with an endpoint given, Docker reads the MAC address from it, not from the configuration:
	// the one its owner set moves there (Docker refuses two)
	if config != nil && config.MacAddress != "" { //nolint:staticcheck
		ep.MacAddress = config.MacAddress //nolint:staticcheck
		config.MacAddress = ""            //nolint:staticcheck
	}
	return &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{name: ep}}
}

// forkHostname is the hostname upstream gives a container on the bridge (its
// name), unless it had one of its own (not Docker's default, its short ID).
func forkHostname(current string, newName string, old types.ContainerJSON) string {
	id := ""
	if old.ContainerJSONBase != nil {
		id = old.ID
	}
	if current == "" || current == shortID(id) || current == strings.TrimPrefix(old.Name, "/") || current == strings.TrimPrefix(newName, "/") {
		return strings.TrimPrefix(newName, "/")
	}
	return current
}

// forkReconnect connects the new container to a network the old one was on,
// with the endpoint it had there; the main network is already connected.
func forkReconnect(networkName string, containerID string, old types.ContainerJSON, mode containerType.NetworkMode) error {
	if networkName == primaryNetwork(mode) {
		return nil
	}
	ep := keptEndpoint(old, networkName)
	if ep == nil {
		ep = &network.EndpointSettings{}
	}
	fitAddresses(old.Name, networkName, ep)
	if err := DockerClient.NetworkConnect(DockerContext, networkName, containerID, ep); err != nil {
		utils.Error("EditContainer: connecting a network", err)
		return err
	}
	for retries := 10; retries > 0; retries-- {
		c, err := DockerClient.ContainerInspect(DockerContext, containerID)
		if err != nil {
			return err
		}
		if IsConnectedToNetwork(c, networkName) {
			return nil
		}
		time.Sleep(networkWait)
	}
	return errors.New("connecting the network " + networkName + ": timeout")
}

// networkWait is replaced by the tests.
var networkWait = 500 * time.Millisecond

// ---- a fixed address that no longer fits its network (third review m-2) ----

// networkSubnets are a network's current subnets; found is false when they
// cannot be read (the address is then left to Docker). Replaced by the tests.
var networkSubnets = func(name string) (subnets []*net.IPNet, found bool) {
	if DockerClient == nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(DockerContext, 5*time.Second)
	defer cancel()
	n, err := DockerClient.NetworkInspect(ctx, name, types.NetworkInspectOptions{})
	if err != nil {
		return nil, false
	}
	for i, c := range n.IPAM.Config {
		if i >= 32 {
			break
		}
		if _, subnet, err := net.ParseCIDR(c.Subnet); err == nil {
			subnets = append(subnets, subnet)
		}
	}
	return subnets, true
}

func fits(address string, subnets []*net.IPNet) bool {
	ip := net.ParseIP(address)
	if ip == nil {
		return false
	}
	for _, s := range subnets {
		if s.Contains(ip) {
			return true
		}
	}
	return false
}

var (
	editNotesMu sync.Mutex
	editNotes   = map[string][]string{}
)

func noteEdit(container, text string) {
	utils.Warn("EditContainer: " + text)
	editNotesMu.Lock()
	defer editNotesMu.Unlock()
	if len(editNotes) >= 64 {
		editNotes = map[string][]string{}
	}
	key := strings.TrimPrefix(container, "/")
	editNotes[key] = append(editNotes[key], text)
}

// takeEditNotes are what the last edit of a container has to say, said once.
func takeEditNotes(container string) []string {
	editNotesMu.Lock()
	defer editNotesMu.Unlock()
	key := strings.TrimPrefix(container, "/")
	notes := editNotes[key]
	delete(editNotes, key)
	return notes
}

// fitAddresses drops a kept fixed address that the network's current subnets
// no longer hold (the network was made again with another range: Docker would
// refuse the container), and says so.
func fitAddresses(container, networkName string, ep *network.EndpointSettings) {
	if ep == nil || ep.IPAMConfig == nil {
		return
	}
	subnets, found := networkSubnets(networkName)
	if !found {
		return
	}
	var v4, v6 []*net.IPNet
	for _, s := range subnets {
		if s.IP.To4() != nil {
			v4 = append(v4, s)
		} else {
			v6 = append(v6, s)
		}
	}
	if a := ep.IPAMConfig.IPv4Address; a != "" && !fits(a, v4) {
		ep.IPAMConfig.IPv4Address = ""
		noteEdit(container, "its fixed address "+a+" no longer fits the network "+networkName+" (made again with another range): it now gets one from the network")
	}
	if a := ep.IPAMConfig.IPv6Address; a != "" && !fits(a, v6) {
		ep.IPAMConfig.IPv6Address = ""
		noteEdit(container, "its fixed address "+a+" no longer fits the network "+networkName+" (made again with another range): it now gets one from the network")
	}
	if ep.IPAMConfig.IPv4Address == "" && ep.IPAMConfig.IPv6Address == "" && len(ep.IPAMConfig.LinkLocalIPs) == 0 {
		ep.IPAMConfig = nil
	}
}
