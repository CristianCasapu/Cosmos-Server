package proxy

// Fork addition: publishes two .local names through a real Avahi daemon and
// keeps them up for a while, so that other containers can try to resolve
// them. Only for a container with its own Avahi and D-Bus, never the host's:
//
//	MDNS_E2E=new|old MDNS_E2E_HOLD=60s go test ./src/proxy/ -run MDNSE2E -v
//
// "old" publishes them the way upstream did (CNAME to the host name), "new"
// the fork's way (an address record per name). With "new" it also prints
// what the panel would be told, the network check included.

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestMDNSE2E(t *testing.T) {
	mode := os.Getenv("MDNS_E2E")
	if mode == "" {
		t.Skip("set MDNS_E2E=new or old inside a test container with its own Avahi")
	}
	hold, err := time.ParseDuration(os.Getenv("MDNS_E2E_HOLD"))
	if err != nil {
		hold = time.Minute
	}
	names := []string{"cosmos-e2e-one.local", "cosmos-e2e-two.local"}
	if extra := os.Getenv("MDNS_E2E_NAMES"); extra != "" {
		names = strings.Split(extra, ",")
	}

	config := utils.Config{}
	config.HTTPConfig.Hostname = "cosmos-e2e.invalid"
	config.HTTPConfig.HTTPPort, config.HTTPConfig.HTTPSPort = "2052", "2053"
	config.HTTPConfig.PublishMDNS = true
	for _, host := range names {
		config.HTTPConfig.ProxyConfig.Routes = append(config.HTTPConfig.ProxyConfig.Routes,
			utils.ProxyRouteConfig{Name: host, UseHost: true, Host: host})
	}
	utils.LoadBaseMainConfig(config)

	switch mode {
	case "old":
		p, err := NewPublisher()
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		if err := p.UpdateCNAMES(names, 60); err != nil {
			t.Fatal(err)
		}
		t.Logf("published as CNAMEs of %s", p.Fqdn())
	default:
		ForkPublishLocalNames()
		defer func() {
			localNames.mu.Lock()
			localNames.withdrawAll()
			localNames.mu.Unlock()
		}()
		time.Sleep(3 * time.Second) // Avahi probes before it answers
		out, _ := json.MarshalIndent(localNamesStatus(true), "", "  ")
		t.Logf("status: %s", out)
	}
	t.Logf("holding for %s", hold)
	time.Sleep(hold)
	if mode != "old" {
		out, _ := json.MarshalIndent(localNamesStatus(true), "", "  ")
		t.Logf("status at the end: %s", out)
	}
}
