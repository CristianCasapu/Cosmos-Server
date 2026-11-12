package proxy

// Fork addition: exercises the mDNS publisher against the Avahi daemon of the
// machine the test runs on. It announces two test names on the local network
// for a few seconds, so it only runs when asked to:
//
//	AVAHI_LIVE=1 go test ./src/proxy/ -run AvahiLive -v
//
// With AVAHI_LIVE_RESTART set to a shell command that restarts the Avahi
// daemon, it also checks that the names come back after a restart. Run that
// part against a daemon of its own (a container with dbus and avahi-daemon),
// not against the machine's.

import (
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/azukaar/cosmos-server/src/utils"
)

// mdnsQuery asks the local network for name and returns the records answered.
// Sent from an ordinary port, so responders reply to this socket directly.
func mdnsQuery(t *testing.T, name string, qtype uint16) []dns.RR {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	query := new(dns.Msg)
	query.SetQuestion(dns.Fqdn(name), qtype)
	query.RecursionDesired = false
	packed, err := query.Pack()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.WriteToUDP(packed, &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}); err != nil {
		t.Fatal(err)
	}

	records := []dns.RR{}
	buf := make([]byte, 9000)
	conn.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return records
		}
		reply := new(dns.Msg)
		if reply.Unpack(buf[:n]) != nil {
			continue
		}
		records = append(records, reply.Answer...)
		records = append(records, reply.Extra...)
	}
}

// resolves reports whether name is answered on the network with an address.
func resolves(t *testing.T, name string) bool {
	t.Helper()
	time.Sleep(2500 * time.Millisecond) // Avahi probes before it answers
	for _, rr := range mdnsQuery(t, name, dns.TypeA) {
		if _, ok := rr.(*dns.A); ok {
			return true
		}
	}
	return false
}

func TestAvahiLive(t *testing.T) {
	if os.Getenv("AVAHI_LIVE") == "" {
		t.Skip("set AVAHI_LIVE=1 to announce test names through the local Avahi daemon")
	}

	a, b := "cosmos-mdns-test-a.local", "cosmos-mdns-test-b.local"
	routes := func(hosts ...string) {
		config := utils.Config{}
		config.HTTPConfig.Hostname = "cosmos-mdns-test.invalid"
		config.HTTPConfig.PublishMDNS = true
		for _, host := range hosts {
			config.HTTPConfig.ProxyConfig.Routes = append(config.HTTPConfig.ProxyConfig.Routes,
				utils.ProxyRouteConfig{Name: host, UseHost: true, Host: host})
		}
		utils.LoadBaseMainConfig(config)
	}
	defer func() {
		publisherMu.Lock()
		if publisher != nil {
			publisher.Close()
			publisher = nil
		}
		publisherMu.Unlock()
	}()

	// Publishing, adding a name, removing a name.
	routes(a)
	PublishAllMDNSFromConfig()
	if publisher == nil {
		t.Fatal("no connection to Avahi")
	}
	t.Logf("host name: %s", publisher.Fqdn())
	if !resolves(t, a) {
		t.Fatalf("%s does not resolve after publishing", a)
	}

	routes(a, b)
	PublishAllMDNSFromConfig()
	if !resolves(t, a) || !resolves(t, b) {
		t.Fatalf("%s and %s do not both resolve after adding a name", a, b)
	}

	routes(b)
	PublishAllMDNSFromConfig()
	if resolves(t, a) || !resolves(t, b) {
		t.Fatalf("after removing %s: it must not resolve and %s must", a, b)
	}

	// Nothing changed, so the watch must leave the names alone.
	before := publisher
	checkMDNS()
	if publisher != before {
		t.Fatal("the watch republished although nothing was lost")
	}

	restart := os.Getenv("AVAHI_LIVE_RESTART")
	if restart == "" {
		return
	}

	// The Avahi daemon restarts and forgets the names.
	if out, err := exec.Command("sh", "-c", restart).CombinedOutput(); err != nil {
		t.Fatalf("restarting Avahi: %v: %s", err, out)
	}
	deadline := time.Now().Add(30 * time.Second)
	for publisher.lost() == "" {
		if time.Now().After(deadline) {
			t.Fatal("the restart of the Avahi daemon was not noticed")
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Logf("noticed: %s", publisher.lost())
	if resolves(t, b) {
		t.Fatalf("%s still resolves although Avahi lost it", b)
	}

	// What Cosmos did until now: publish the unchanged list again. Nothing happens.
	PublishAllMDNSFromConfig()
	if resolves(t, b) {
		t.Logf("%s resolves again without the watch", b)
	} else {
		t.Logf("without the watch %s stays unresolvable", b)
	}

	checkMDNS()
	if !resolves(t, b) {
		t.Fatalf("%s does not resolve after the watch republished it", b)
	}
	if publisher.lost() != "" {
		t.Fatalf("still lost after republishing: %s", publisher.lost())
	}
}
