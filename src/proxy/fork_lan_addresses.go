package proxy

// Fork addition (plan 43, review F1): the server's addresses on the home
// network, for "Reach it from my home network": a service's port is
// published on these, never on every address of the server. The same
// interfaces the .local names are published on (fork_mdns_ifaces.go): the
// default route's first, then the other physical ones; private IPv4 only.

import (
	"encoding/json"
	"net"
	"net/http"

	"github.com/azukaar/cosmos-server/src/utils"
)

// LANAddress is one address of the server on a home network.
type LANAddress struct {
	Interface string `json:"interface"`
	Address   string `json:"address"`
	Default   bool   `json:"defaultRoute"`
}

// chooseLANForAddresses is a variable so that tests give the interfaces.
var chooseLANForAddresses = func() []lanInterface {
	chosen, _ := chooseLANInterfaces(utils.GetMainConfig().HTTPConfig.MDNSInterfaces)
	return chosen
}

// LANAddresses are the server's private IPv4 addresses on its home networks.
func LANAddresses() []LANAddress {
	out := []LANAddress{}
	seen := map[string]bool{}
	for _, iface := range chooseLANForAddresses() {
		ip := net.ParseIP(iface.Address)
		if ip == nil || ip.To4() == nil || !ip.IsPrivate() || seen[ip.String()] || len(out) >= 8 {
			continue
		}
		seen[ip.String()] = true
		out = append(out, LANAddress{Interface: iface.Name, Address: ip.To4().String(), Default: iface.Default})
	}
	return out
}

// LANAddressesRoute answers GET /cosmos/api/lan-addresses.
func LANAddressesRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_RESOURCES_READ) != nil {
		return
	}
	if req.Method != http.MethodGet {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": LANAddresses()})
}
