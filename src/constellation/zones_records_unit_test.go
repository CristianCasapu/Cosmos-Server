package constellation

import (
	"reflect"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func seedRecordsView(t *testing.T, devices map[string]utils.ConstellationDevice, nodes map[string]recordsNodeView, complete bool) {
	t.Helper()

	deviceCacheMux.Lock()
	prevDevices := CachedDevices
	CachedDevices = devices
	deviceCacheMux.Unlock()

	recordsViewLock.Lock()
	recordsNodes = nodes
	recordsViewComplete = complete
	recordsViewUpdated = time.Now()
	recordsViewLock.Unlock()

	prevProbe := publicAnswers
	publicAnswers = func(string) bool { return false }

	t.Cleanup(func() {
		deviceCacheMux.Lock()
		CachedDevices = prevDevices
		deviceCacheMux.Unlock()
		recordsViewLock.Lock()
		recordsNodes = map[string]recordsNodeView{}
		recordsViewLock.Unlock()
		publicAnswers = prevProbe
	})
}

func recordsNode(name string, ip string, public string, cosmosNode int, hostnames []string, tunnels []utils.ProxyRouteConfig, seen time.Time) recordsNodeView {
	return recordsNodeView{seen: seen, heartbeat: NodeHeartbeat{
		DeviceName: name, IP: ip, PublicAddr: public, CosmosNode: cosmosNode, Hostnames: hostnames, Tunnels: tunnels,
	}}
}

func TestUnitClusterHostAddresses(t *testing.T) {
	now := time.Now()
	devices := map[string]utils.ConstellationDevice{
		"node-1": {DeviceName: "node-1", IP: "192.168.201.1/24", CosmosNode: 2, IsLoadBalancer: true},
		"node-2": {DeviceName: "node-2", IP: "192.168.201.2/24", CosmosNode: 2, IsLoadBalancer: true},
		"node-3": {DeviceName: "node-3", IP: "192.168.201.3/24", CosmosNode: 1},
	}
	tunnel := []utils.ProxyRouteConfig{{Name: "app", UseHost: true, Host: "app.example.com", Tunnel: "any"}}
	nodes := map[string]recordsNodeView{
		"node-1": recordsNode("node-1", "192.168.201.1", "1.1.1.1", 2, []string{"node-1.example.com"}, nil, now),
		"node-2": recordsNode("node-2", "192.168.201.2", "2.2.2.2", 2, []string{"node-2.example.com"}, nil, now),
		"node-3": recordsNode("node-3", "192.168.201.3", "3.3.3.3", 1, []string{"node-3.example.com", "plain.example.com"}, tunnel, now),
	}
	seedRecordsView(t, devices, nodes, true)

	hosts, wildcard, ok := clusterHostAddresses()
	if !ok {
		t.Fatal("a complete view must be usable")
	}

	want := map[string][]string{
		"node-1.example.com": {"1.1.1.1"},
		"node-2.example.com": {"2.2.2.2"},
		"node-3.example.com": {"3.3.3.3"},
		"plain.example.com":  {"3.3.3.3"},
		// tunneled: the load balancers, not the node hosting it
		"app.example.com": {"1.1.1.1", "2.2.2.2"},
	}
	if !reflect.DeepEqual(hosts, want) {
		t.Errorf("hosts = %v, want %v", hosts, want)
	}
	if !reflect.DeepEqual(wildcard, []string{"1.1.1.1", "2.2.2.2"}) {
		t.Errorf("wildcard = %v, want the load balancers", wildcard)
	}
}

func TestUnitClusterHostAddressesDeadNodeAndPartialView(t *testing.T) {
	now := time.Now()
	long := now.Add(-10 * time.Minute)
	devices := map[string]utils.ConstellationDevice{
		"node-1": {DeviceName: "node-1", IP: "192.168.201.1/24", CosmosNode: 2, IsLoadBalancer: true},
		"node-2": {DeviceName: "node-2", IP: "192.168.201.2/24", CosmosNode: 2, IsLoadBalancer: true},
	}
	tunnel := []utils.ProxyRouteConfig{{Name: "app", UseHost: true, Host: "app.example.com", Tunnel: "any"}}
	nodes := map[string]recordsNodeView{
		"node-1":  recordsNode("node-1", "192.168.201.1", "1.1.1.1", 2, []string{"node-1.example.com"}, tunnel, now),
		"node-2":  recordsNode("node-2", "192.168.201.2", "2.2.2.2", 2, []string{"node-2.example.com"}, nil, long),
		"removed": recordsNode("removed", "192.168.201.9", "9.9.9.9", 1, []string{"removed.example.com"}, nil, now),
	}
	seedRecordsView(t, devices, nodes, true)

	hosts, wildcard, ok := clusterHostAddresses()
	if !ok {
		t.Fatal("a complete view must be usable")
	}

	want := map[string][]string{
		"node-1.example.com": {"1.1.1.1"},
		// dead, but the only one serving the name: a set is never emptied
		"node-2.example.com": {"2.2.2.2"},
		// dead load balancer withdrawn from the shared set
		"app.example.com": {"1.1.1.1"},
	}
	if !reflect.DeepEqual(hosts, want) {
		t.Errorf("hosts = %v, want %v", hosts, want)
	}
	if !reflect.DeepEqual(wildcard, []string{"1.1.1.1"}) {
		t.Errorf("wildcard = %v, want the live load balancer only", wildcard)
	}

	// still answering from the internet: losing the mesh is not an outage
	publicAnswers = func(address string) bool { return address == "2.2.2.2" }
	hosts, _, _ = clusterHostAddresses()
	if !reflect.DeepEqual(hosts["app.example.com"], []string{"1.1.1.1", "2.2.2.2"}) {
		t.Errorf("app = %v, a node that still answers publicly must stay", hosts["app.example.com"])
	}

	recordsViewLock.Lock()
	recordsViewComplete = false
	recordsViewLock.Unlock()
	if _, _, ok := clusterHostAddresses(); ok {
		t.Error("a partial view must never be used")
	}
}
