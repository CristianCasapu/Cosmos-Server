package constellation

import (
	"sync"

	"github.com/miekg/dns"
	"github.com/azukaar/cosmos-server/src/utils"
)

// the tun device of nebula, as nebula_default.go names it
const nebulaAdapter = "nebula1"

var hostDNSMux sync.Mutex
var hostDNSSet = false
var hostDNSExitOnce sync.Once

// applyHostDNS points the resolver of this server at the Constellation DNS,
// once it answers. Servers then reach the registries and services of the
// cluster by name, like any device of the Constellation. Unlike a device, a
// server ignores the DNS port: the resolver of the system only takes an address.
func applyHostDNS(server *dns.Server, ip string) {
	if !utils.UseConstellationDNSOnHost() {
		return
	}
	hostDNSMux.Lock()
	defer hostDNSMux.Unlock()

	if hostDNSSet {
		return
	}
	if !waitForAdapter(nebulaAdapter) {
		utils.Warn("Host DNS: the " + nebulaAdapter + " interface did not show up, the DNS of this server is left as it is")
		return
	}
	// StopDNS reverts under hostDNSMux after it dropped the server, so a server
	// that is still the current one here cannot be left behind by a stop
	if !isCurrentDNSServer(server) {
		return
	}
	if err := setHostDNS(nebulaAdapter, ip); err != nil {
		utils.Warn("Host DNS: cannot point this server at the Constellation DNS, its DNS is left as it is: " + err.Error())
		return
	}
	hostDNSSet = true
	utils.Log("Host DNS: this server now resolves through the Constellation DNS at " + ip)
}

// ReapplyHostDNS follows a change of the setting while the DNS runs
func ReapplyHostDNS() {
	dnsMux.Lock()
	server := dnsServer
	started := DNSStarted
	dnsMux.Unlock()
	if !started || server == nil {
		return
	}
	if !utils.UseConstellationDNSOnHost() {
		revertHostDNS()
		return
	}
	if ip, err := GetCurrentDeviceIP(); err == nil {
		applyHostDNS(server, ip)
	}
}

// revertHostDNS gives the server its resolver back when the DNS stops or the process exits
func revertHostDNS() {
	hostDNSMux.Lock()
	defer hostDNSMux.Unlock()

	if !hostDNSSet {
		return
	}
	hostDNSSet = false
	if err := resetHostDNS(nebulaAdapter); err != nil {
		utils.Warn("Host DNS: cannot give this server its DNS back: " + err.Error())
		return
	}
	utils.Log("Host DNS: this server resolves on its own again")
}
