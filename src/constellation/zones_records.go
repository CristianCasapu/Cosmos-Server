package constellation

import (
	"net"
	"sort"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/dnsrecords"
	"github.com/azukaar/cosmos-server/src/utils"
)

// a node missing from the heartbeats is still counted in for this long: a
// couple of missed beats is not an outage
const recordsNodeGrace = 2 * time.Minute

type recordsNodeView struct {
	heartbeat NodeHeartbeat
	seen      time.Time
}

var recordsViewLock sync.Mutex
var recordsNodes = map[string]recordsNodeView{}
var recordsViewComplete bool
var recordsViewUpdated time.Time

// updateRecordsView feeds the DynDNS writer with the cluster view of this
// refresh. complete is false when any heartbeat could not be read: a partial
// view must never be mistaken for nodes being gone.
func updateRecordsView(heartbeats []NodeHeartbeat, complete bool) {
	recordsViewLock.Lock()
	recordsViewComplete = complete
	now := time.Now()
	recordsViewUpdated = now
	for _, hb := range heartbeats {
		recordsNodes[hb.DeviceName] = recordsNodeView{heartbeat: hb, seen: now}
	}
	recordsViewLock.Unlock()

	dnsrecords.Trigger()
}

// publicAnswers is the hand-off a dead node's address needs before it is taken
// out of a shared record set: losing the mesh is not being unreachable from
// the internet. Advisory only, a home network often cannot reach its own
// public IP, and then the node is simply treated as gone.
var publicAnswers = func(address string) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(address, utils.GetMainConfig().HTTPConfig.HTTPSPort), 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// clusterHostAddresses is the public side of buildClusterDNS: every hostname
// of the cluster with the advertised addresses of the nodes serving it. A
// tunneled hostname resolves to the load balancers, whoever hosts it.
func clusterHostAddresses() (map[string][]string, []string, bool) {
	recordsViewLock.Lock()
	// no refresh for a while means the store is unreachable, not that every node died
	complete := recordsViewComplete && time.Since(recordsViewUpdated) < recordsNodeGrace/2
	nodes := make(map[string]recordsNodeView, len(recordsNodes))
	for name, view := range recordsNodes {
		nodes[name] = view
	}
	recordsViewLock.Unlock()

	if !complete {
		return nil, nil, false
	}

	devices, _ := deviceCacheSnapshot()

	// nebula IP -> advertised address, for the nodes that count
	addressOf := map[string]string{}
	alive := []NodeHeartbeat{}
	gone := []NodeHeartbeat{}

	for name, view := range nodes {
		device, registered := devices[name]
		if !registered || device.Blocked {
			// removed from the constellation: it leaves the records right away
			recordsViewLock.Lock()
			delete(recordsNodes, name)
			recordsViewLock.Unlock()
			continue
		}
		if view.heartbeat.PublicAddr == "" {
			continue
		}

		if time.Since(view.seen) < recordsNodeGrace || publicAnswers(view.heartbeat.PublicAddr) {
			alive = append(alive, view.heartbeat)
		} else {
			gone = append(gone, view.heartbeat)
		}
		addressOf[cleanIp(view.heartbeat.IP)] = view.heartbeat.PublicAddr
	}

	toPublic := func(clusterDNS map[string]clusterHostname) map[string][]string {
		out := map[string][]string{}
		for host, entry := range clusterDNS {
			for _, ip := range entry.IPs {
				if address := addressOf[ip]; address != "" {
					out[host] = appendUniqueIP(out[host], address)
				}
			}
		}
		return out
	}

	lbIPs := loadBalancerIPs()
	hosts := toPublic(buildClusterDNS(alive, aliveOnly(lbIPs, alive)))

	// a node that is gone only keeps the names nobody else serves: a set is never emptied
	for host, addresses := range toPublic(buildClusterDNS(gone, aliveOnly(lbIPs, gone))) {
		if len(hosts[host]) == 0 {
			hosts[host] = addresses
		}
	}

	// the wildcard goes to the load balancers, or to the first manager without any
	wildcard := []string{}
	for _, ip := range aliveOnly(lbIPs, alive) {
		wildcard = appendUniqueIP(wildcard, addressOf[ip])
	}
	if len(wildcard) == 0 {
		managers := []NodeHeartbeat{}
		for _, hb := range alive {
			if hb.CosmosNode == 2 {
				managers = append(managers, hb)
			}
		}
		sort.Slice(managers, func(i, j int) bool { return managers[i].DeviceName < managers[j].DeviceName })
		if len(managers) > 0 {
			wildcard = []string{managers[0].PublicAddr}
		}
	}

	return hosts, wildcard, true
}

// aliveOnly keeps the load balancers found among the given nodes
func aliveOnly(lbIPs []string, nodes []NodeHeartbeat) []string {
	present := map[string]bool{}
	for _, hb := range nodes {
		present[cleanIp(hb.IP)] = true
	}
	out := []string{}
	for _, ip := range lbIPs {
		if present[ip] {
			out = append(out, ip)
		}
	}
	return out
}

func initRecords() {
	dnsrecords.IsWriter = isZoneIssuer
	dnsrecords.HostAddresses = func() (map[string][]string, []string, bool) {
		if !utils.GetMainConfig().ConstellationConfig.Enabled {
			return standaloneHostAddresses()
		}
		return clusterHostAddresses()
	}
}

var standaloneHostAddresses = dnsrecords.HostAddresses
