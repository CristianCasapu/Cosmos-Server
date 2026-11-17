package proxy

// Fork addition (plan 50): whether the proxy itself keeps a URL with a given
// host to the home network (a .local name, a private or VPN address: the rule
// of routerGen.go, isLocalAddress). The URL form asks it before it proposes a
// storage API without Cosmos' limits: only an address the proxy confines may
// be recommended without them. GET /cosmos/api/host-home-only?host=…
// (configuration-read, like the form); one bounded value in, one boolean out.

import (
	"encoding/json"
	"net/http"

	"github.com/azukaar/cosmos-server/src/utils"
)

// HostHomeOnly is the proxy's rule for a URL whose host is used (UseHost):
// true when its visitors from outside the home network are refused.
func HostHomeOnly(host string) bool {
	if len(host) == 0 || len(host) > 260 {
		return false
	}
	return isLocalAddress(host)
}

func HostHomeOnlyRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION_READ) != nil {
		return
	}
	if req.Method != http.MethodGet {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	host := req.URL.Query().Get("host")
	if len(host) > 260 {
		utils.HTTPError(w, "The host is too long", http.StatusBadRequest, "HTTP004")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": map[string]bool{"homeOnly": HostHomeOnly(host)}})
}
