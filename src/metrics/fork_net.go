package metrics

// Fork addition: the server's network traffic is what its own network cards
// carried, and it never goes below zero.
//
// Upstream took the sum of the counters of every interface (loopback, each
// container's veth pair, every Docker bridge, VPN tunnels) and charted the
// difference between two readings. A container's traffic was counted up to
// three times (its veth, its bridge and the network card), and whenever an
// interface went away (a container or a network removed) the sum dropped and
// the chart showed negative traffic, by as much as that interface had ever
// carried.
//
// Here only the interfaces that are a device of the machine are counted (a
// network card has /sys/class/net/<name>/device; veth pairs, bridges, tunnels
// and loopback have not), or, where none is found (inside a container),
// everything but loopback. Each interface is followed by itself: what it
// carried since its previous reading is added to a total that only grows, an
// interface seen for the first time starts from its current reading, and one
// whose counter went back (replugged, driver reloaded) starts again.

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/shirou/gopsutil/v3/net"
)

type netTotals struct {
	sync.Mutex
	started bool
	seen    map[string]net.IOCountersStat
	total   net.IOCountersStat
}

var forkNet = &netTotals{}

// isNetworkCard reports whether an interface is a device of the machine.
var isNetworkCard = func(name string) bool {
	if name == "" || strings.ContainsAny(name, "/\x00") || name == "." || name == ".." {
		return false
	}
	_, err := os.Stat("/sys/class/net/" + name + "/device")
	return err == nil
}

// countedInterfaces returns the readings that make up the server's traffic.
func countedInterfaces(all []net.IOCountersStat, card func(string) bool) []net.IOCountersStat {
	var cards, others []net.IOCountersStat
	for _, one := range all {
		switch {
		case card(one.Name):
			cards = append(cards, one)
		case one.Name != "lo":
			others = append(others, one)
		}
	}
	if len(cards) > 0 {
		return cards
	}
	return others
}

// grown returns what a counter gained since its previous reading: nothing
// when it went back.
func grown(now uint64, before uint64) uint64 {
	if now < before {
		return 0
	}
	return now - before
}

// add takes one reading of every interface and returns the totals so far.
func (t *netTotals) add(all []net.IOCountersStat, card func(string) bool) net.IOCountersStat {
	t.Lock()
	defer t.Unlock()
	counted := countedInterfaces(all, card)
	now := make(map[string]net.IOCountersStat, len(counted))
	for _, one := range counted {
		now[one.Name] = one
		before, known := t.seen[one.Name]
		switch {
		case !t.started:
			// the first reading: the totals start where the counters are, as upstream's did
			t.total.BytesRecv += one.BytesRecv
			t.total.BytesSent += one.BytesSent
			t.total.Errin += one.Errin
			t.total.Errout += one.Errout
			t.total.Dropin += one.Dropin
			t.total.Dropout += one.Dropout
		case known:
			t.total.BytesRecv += grown(one.BytesRecv, before.BytesRecv)
			t.total.BytesSent += grown(one.BytesSent, before.BytesSent)
			t.total.Errin += grown(one.Errin, before.Errin)
			t.total.Errout += grown(one.Errout, before.Errout)
			t.total.Dropin += grown(one.Dropin, before.Dropin)
			t.total.Dropout += grown(one.Dropout, before.Dropout)
		}
		// an interface seen for the first time later on only sets its starting point
	}
	t.seen = now
	t.started = true
	total := t.total
	total.Name = "all"
	return total
}

// forkNetCounters replaces upstream's net.IOCountersWithContext(ctx, false)
// in system.go: one entry, the totals of the server's network cards. read is
// that function, asked for every interface by itself.
func forkNetCounters(ctx context.Context, read func(context.Context, bool) ([]net.IOCountersStat, error)) ([]net.IOCountersStat, error) {
	all, err := read(ctx, true)
	if err != nil {
		return nil, err
	}
	return []net.IOCountersStat{forkNet.add(all, isNetworkCard)}, nil
}

// trafficKey reports whether a metric is one of the server's traffic totals.
func trafficKey(key string) bool {
	for _, name := range []string{"system.netRx", "system.netTx", "system.netErr", "system.netDrop"} {
		if key == name || strings.HasSuffix(key, "."+name) {
			return true
		}
	}
	return false
}

// forkNoNegativeTraffic shows the negative amounts recorded before this fix
// as zero: traffic below zero means nothing, and the chart's scale would
// otherwise stay bent around them until they age out.
func forkNoNegativeTraffic(metrics []DataDefDB) []DataDefDB {
	for m := range metrics {
		if !trafficKey(metrics[m].Key) {
			continue
		}
		for v := range metrics[m].Values {
			if metrics[m].Values[v].Value < 0 {
				metrics[m].Values[v].Value = 0
			}
		}
		for name, entry := range metrics[m].ValuesAggl {
			if entry.Value < 0 {
				entry.Value = 0
				metrics[m].ValuesAggl[name] = entry
			}
		}
	}
	return metrics
}
