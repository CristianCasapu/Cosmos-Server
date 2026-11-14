package metrics

import (
	"testing"

	"github.com/shirou/gopsutil/v3/net"
)

func reading(name string, recv uint64, sent uint64) net.IOCountersStat {
	return net.IOCountersStat{Name: name, BytesRecv: recv, BytesSent: sent, Errin: recv / 1000, Dropout: sent / 1000}
}

func cards(names ...string) func(string) bool {
	set := map[string]bool{}
	for _, name := range names {
		set[name] = true
	}
	return func(name string) bool { return set[name] }
}

func TestTrafficCountsNetworkCardsOnly(t *testing.T) {
	totals := &netTotals{}
	card := cards("lan0")
	first := totals.add([]net.IOCountersStat{
		reading("lo", 9_000_000, 9_000_000), reading("lan0", 1_000_000, 500_000),
		reading("veth1", 700_000, 700_000), reading("br-1", 700_000, 700_000), reading("tun0", 300_000, 300_000),
	}, card)
	if first.BytesRecv != 1_000_000 || first.BytesSent != 500_000 {
		t.Fatalf("the first reading counts %d received and %d sent, want the network card's own 1000000 and 500000", first.BytesRecv, first.BytesSent)
	}
	second := totals.add([]net.IOCountersStat{
		reading("lo", 9_500_000, 9_500_000), reading("lan0", 1_200_000, 600_000),
		reading("veth1", 900_000, 900_000), reading("br-1", 900_000, 900_000), reading("tun0", 400_000, 400_000),
	}, card)
	if got := second.BytesRecv - first.BytesRecv; got != 200_000 {
		t.Errorf("received between two readings: %d, want the card's 200000 (a container's traffic is not counted again on its veth and bridge)", got)
	}
	if got := second.BytesSent - first.BytesSent; got != 100_000 {
		t.Errorf("sent between two readings: %d, want 100000", got)
	}
}

// The defect: removing a container or a network took its interfaces away,
// the sum of all counters dropped and the chart showed negative traffic.
func TestTrafficNeverGoesBackWhenInterfacesGoAway(t *testing.T) {
	for name, card := range map[string]func(string) bool{
		"with a network card":          cards("lan0"),
		"without one (in a container)": cards(),
	} {
		totals := &netTotals{}
		previous := totals.add([]net.IOCountersStat{reading("lan0", 5_000, 5_000), reading("veth1", 800_000_000, 900_000_000), reading("br-1", 800_000_000, 900_000_000)}, card)
		steps := [][]net.IOCountersStat{
			{reading("lan0", 6_000, 5_500), reading("br-1", 800_000_100, 900_000_100)},                  // the container is gone
			{reading("lan0", 7_000, 6_000)},                                                             // and its network
			{reading("lan0", 8_000, 6_500), reading("veth2", 50, 60)},                                   // another one appears
			{reading("lan0", 100, 100), reading("veth2", 150, 160)},                                     // the card's counter starts again
			{reading("lan0", 300, 200), reading("veth2", 250, 260), reading("lan1", 40_000_000, 1_000)}, // a card is plugged in
			{reading("lan0", 400, 300), reading("veth2", 350, 360), reading("lan1", 40_000_500, 1_200)},
		}
		for i, step := range steps {
			now := totals.add(step, card)
			if now.BytesRecv < previous.BytesRecv || now.BytesSent < previous.BytesSent || now.Errin < previous.Errin || now.Dropout < previous.Dropout {
				t.Errorf("%s, step %d: the totals went back from %d/%d to %d/%d", name, i, previous.BytesRecv, previous.BytesSent, now.BytesRecv, now.BytesSent)
			}
			previous = now
		}
	}
}

func TestTrafficOfANewInterfaceStartsAtItsCurrentReading(t *testing.T) {
	totals := &netTotals{}
	card := cards("lan0", "lan1")
	start := totals.add([]net.IOCountersStat{reading("lan0", 1_000, 1_000)}, card)
	plugged := totals.add([]net.IOCountersStat{reading("lan0", 1_000, 1_000), reading("lan1", 70_000_000, 80_000_000)}, card)
	if plugged.BytesRecv != start.BytesRecv || plugged.BytesSent != start.BytesSent {
		t.Errorf("a card that appears adds what it carried before: %d/%d, want %d/%d", plugged.BytesRecv, plugged.BytesSent, start.BytesRecv, start.BytesSent)
	}
	later := totals.add([]net.IOCountersStat{reading("lan0", 1_500, 1_000), reading("lan1", 70_000_300, 80_000_000)}, card)
	if got := later.BytesRecv - plugged.BytesRecv; got != 800 {
		t.Errorf("received afterwards: %d, want 800 (500 on one card, 300 on the other)", got)
	}
}

func TestTrafficFallsBackToEverythingButLoopback(t *testing.T) {
	counted := countedInterfaces([]net.IOCountersStat{reading("lo", 1, 1), reading("eth0", 2, 2), reading("tun0", 3, 3)}, cards())
	if len(counted) != 2 || counted[0].Name != "eth0" || counted[1].Name != "tun0" {
		t.Errorf("without a network card: %v, want eth0 and tun0", counted)
	}
}

func TestIsNetworkCardRefusesNamesThatAreNotOne(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../../etc", "a/b", "lo\x00"} {
		if isNetworkCard(name) {
			t.Errorf("%q is taken for a network card", name)
		}
	}
}

func TestNegativeTrafficOfThePastIsShownAsZero(t *testing.T) {
	metrics := forkNoNegativeTraffic([]DataDefDB{
		{Key: "cosmos.system.netRx", Values: []DataDefDBEntry{{Value: 500}, {Value: -1_400_000_000}, {Value: 0}},
			ValuesAggl: map[string]DataDefDBEntry{"hour": {Value: -7}, "day": {Value: 9}}},
		{Key: "cosmos.system.netTx", Values: []DataDefDBEntry{{Value: -3}}},
		{Key: "cosmos.system.cpu.0", Values: []DataDefDBEntry{{Value: -1}}},
		{Key: "cosmos.system.docker.netRx.app", Values: []DataDefDBEntry{{Value: -2}}},
	})
	if got := metrics[0].Values; got[0].Value != 500 || got[1].Value != 0 || got[2].Value != 0 {
		t.Errorf("received: %v", got)
	}
	if metrics[0].ValuesAggl["hour"].Value != 0 || metrics[0].ValuesAggl["day"].Value != 9 {
		t.Errorf("aggregated: %v", metrics[0].ValuesAggl)
	}
	if metrics[1].Values[0].Value != 0 {
		t.Errorf("sent: %v", metrics[1].Values)
	}
	if metrics[2].Values[0].Value != -1 || metrics[3].Values[0].Value != -2 {
		t.Error("a metric that is not the server's traffic was changed")
	}
}
