package docker

import (
	"encoding/json"
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
	if strings.Join(ep.Aliases, ",") != "db,database" || ep.IPAMConfig.IPv4Address != "172.30.0.50" || ep.MacAddress != "02:42:ac:1e:00:32" || len(ep.Links) != 1 {
		t.Fatalf("stack: %+v", ep)
	}
	// a container that never had settings of its own: nothing but its MAC address
	if ep := keptEndpoint(old, "plain"); ep.IPAMConfig != nil || len(ep.Aliases) != 0 || ep.MacAddress == "" {
		t.Fatalf("plain: %+v", ep)
	}
	// the default bridge takes no aliases or static address
	if ep := keptEndpoint(old, "bridge"); ep.IPAMConfig != nil || len(ep.Aliases) != 0 || ep.MacAddress != "02:42:ac:11:00:02" {
		t.Fatalf("bridge: %+v", ep)
	}
	if keptEndpoint(old, "elsewhere") != nil {
		t.Fatal("a network it was not on")
	}
	config := &containerType.Config{MacAddress: "02:42:ac:1e:00:32"} //nolint:staticcheck
	nc := forkNetworkingConfig(old, config, "stack")
	if nc == nil || nc.EndpointsConfig["stack"] == nil || config.MacAddress != "" { //nolint:staticcheck
		t.Fatalf("networking config: %+v %q", nc, config.MacAddress) //nolint:staticcheck
	}
	for _, mode := range []containerType.NetworkMode{"host", "none", "container:vpn"} {
		if forkNetworkingConfig(old, config, mode) != nil {
			t.Errorf("%s: a network config", mode)
		}
	}
	if nc := forkNetworkingConfig(old, config, "default"); nc == nil || nc.EndpointsConfig["bridge"] == nil {
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
	if strings.Join(body.EndpointConfig.Aliases, ",") != "shape-other" || body.EndpointConfig.MacAddress != "02:42:ac:1f:00:09" {
		t.Fatalf("other: %+v", body.EndpointConfig)
	}
}
