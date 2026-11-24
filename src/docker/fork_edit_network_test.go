package docker

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	containerType "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
)

func TestKeptEndpoint(t *testing.T) {
	old := types.ContainerJSON{ContainerJSONBase: &types.ContainerJSONBase{ID: "0123456789ab" + strings.Repeat("c", 52), Name: "/db"},
		NetworkSettings: &types.NetworkSettings{Networks: map[string]*network.EndpointSettings{
			"stack":  {Aliases: []string{"db", "0123456789ab", "database"}, IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: "172.30.0.50"}, MacAddress: "02:42:ac:1e:00:32", Links: []string{"web:web"}},
			"plain":  {MacAddress: "02:42:ac:1f:00:02", IPAMConfig: &network.EndpointIPAMConfig{}},
			"bridge": {Aliases: []string{"x"}, MacAddress: "02:42:ac:11:00:02", IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: "172.17.0.9"}},
		}}}
	ep := keptEndpoint(old, "stack")
	if strings.Join(ep.Aliases, ",") != "db,database" || ep.IPAMConfig.IPv4Address != "172.30.0.50" || ep.MacAddress != "" || len(ep.Links) != 1 {
		t.Fatalf("stack: %+v", ep)
	}
	// a container that never had settings of its own: nothing, not even the MAC address Docker gave it
	// (third review m-1); created exactly as upstream creates it (no networking config)
	if ep := keptEndpoint(old, "plain"); ep.IPAMConfig != nil || len(ep.Aliases) != 0 || ep.MacAddress != "" {
		t.Fatalf("plain: %+v", ep)
	}
	if nc := forkNetworkingConfig(old, &containerType.Config{}, "plain"); nc != nil {
		t.Fatalf("plain: a networking config %+v", nc)
	}
	// the default bridge takes no aliases or static address, and keeps no MAC address Docker chose
	if ep := keptEndpoint(old, "bridge"); ep.IPAMConfig != nil || len(ep.Aliases) != 0 || ep.MacAddress != "" {
		t.Fatalf("bridge: %+v", ep)
	}
	if keptEndpoint(old, "elsewhere") != nil {
		t.Fatal("a network it was not on")
	}
	config := &containerType.Config{MacAddress: "02:42:ac:1e:00:32"} //nolint:staticcheck
	nc := forkNetworkingConfig(old, config, "stack")
	// a MAC address its owner set moves to the endpoint, where Docker reads it when an endpoint is given
	if nc == nil || nc.EndpointsConfig["stack"] == nil || nc.EndpointsConfig["stack"].MacAddress != "02:42:ac:1e:00:32" || config.MacAddress != "" { //nolint:staticcheck
		t.Fatalf("networking config: %+v %q", nc, config.MacAddress) //nolint:staticcheck
	}
	for _, mode := range []containerType.NetworkMode{"host", "none", "container:vpn"} {
		if forkNetworkingConfig(old, config, mode) != nil {
			t.Errorf("%s: a network config", mode)
		}
	}
	// on the bridge nothing is kept: created as upstream creates it
	if nc := forkNetworkingConfig(old, config, "default"); nc != nil {
		t.Errorf("default (the bridge): %+v", nc)
	}
	if primaryNetwork("default") != "bridge" || primaryNetwork("") != "bridge" {
		t.Error("default is the bridge")
	}
}

func TestForkHostname(t *testing.T) {
	old := types.ContainerJSON{ContainerJSONBase: &types.ContainerJSONBase{ID: "0123456789ab" + strings.Repeat("c", 52), Name: "/pihole"}}
	cases := map[string]string{
		"":             "pihole", // nothing: upstream's
		"0123456789ab": "pihole", // Docker's default: upstream's
		"pihole":       "pihole",
		"dns-box":      "dns-box", // of its own: kept
	}
	for current, want := range cases {
		if got := forkHostname(current, "/pihole", old); got != want {
			t.Errorf("%q: %q, want %q", current, got, want)
		}
	}
}

// The other networks are connected with the endpoint each had; the main one is not connected twice.
func TestTheOtherNetworksAreConnectedAsTheyWere(t *testing.T) {
	withServer(t)
	old := networkWait
	networkWait = time.Millisecond
	defer func() { networkWait = old }()
	c := shapeContainer(t, false)
	fake := withFakeDocker(t, c)
	fake.lenient, fake.createOK = true, true
	updateThroughRoute(t, `{"dns":["192.0.2.53"]}`)
	if len(fake.connected) != 1 || !strings.HasPrefix(fake.connected[0], "other ") {
		t.Fatalf("connected: %v", fake.connected)
	}
	var body struct {
		EndpointConfig network.EndpointSettings `json:"EndpointConfig"`
	}
	json.Unmarshal([]byte(strings.TrimPrefix(fake.connected[0], "other ")), &body)
	if strings.Join(body.EndpointConfig.Aliases, ",") != "shape-other" || body.EndpointConfig.MacAddress != "" {
		t.Fatalf("other: %+v", body.EndpointConfig)
	}
}

// A fixed address that no longer fits its network (made again with another range) is dropped, and said.
func TestAFixedAddressThatNoLongerFitsIsDropped(t *testing.T) {
	old := networkSubnets
	defer func() { networkSubnets = old }()
	networkSubnets = func(name string) ([]*net.IPNet, bool) {
		_, v4, _ := net.ParseCIDR("172.31.6.0/24")
		_, v6, _ := net.ParseCIDR("fd00:6::/64")
		return []*net.IPNet{v4, v6}, name == "net"
	}
	ep := &network.EndpointSettings{Aliases: []string{"db"}, IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: "172.31.5.9", IPv6Address: "fd00:5::9"}}
	fitAddresses("/app", "net", ep)
	if ep.IPAMConfig != nil {
		t.Fatalf("kept: %+v", ep.IPAMConfig)
	}
	notes := takeEditNotes("app")
	if len(notes) != 2 || !strings.Contains(notes[0], "172.31.5.9 no longer fits the network net") {
		t.Fatalf("notes: %v", notes)
	}
	if len(takeEditNotes("app")) != 0 {
		t.Fatal("said twice")
	}
	fits := &network.EndpointSettings{IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: "172.31.6.9", IPv6Address: "fd00:6::9"}}
	fitAddresses("/app", "net", fits)
	if fits.IPAMConfig == nil || fits.IPAMConfig.IPv4Address != "172.31.6.9" || fits.IPAMConfig.IPv6Address != "fd00:6::9" {
		t.Fatalf("a fitting address dropped: %+v", fits.IPAMConfig)
	}
	// a network that cannot be read: left to Docker
	unknown := &network.EndpointSettings{IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: "10.9.9.9"}}
	fitAddresses("/app", "gone", unknown)
	if unknown.IPAMConfig == nil {
		t.Fatal("dropped without knowing the network")
	}
}
