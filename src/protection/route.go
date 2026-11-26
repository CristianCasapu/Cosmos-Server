package protection

import (
	"encoding/json"
	"net/http"

	"github.com/azukaar/cosmos-server/src/utils"
)

// StatusRoute godoc
// @Summary Protection: the state of the host guard, Fail2ban and the automatic security updates
// @Description Read only. For each of the three protection pieces the fork can install on the server: whether it is installed and active, since when, who set it up, and the command that installs or removes it (to run on the server; Cosmos never runs it). For the host guard also the live state of its nftables table (base chains and whether they hold their rules, drop counters, the ports it keeps open, the outside and home interfaces, the SSH allowances with their end, the networks named as home, whether IPv4 is filtered and on which interfaces it is not, the interfaces trusted with --trust-interface, new prefixes waiting for confirmation) with what its installer recorded (why IPv4 is filtered or not, other tables that filter, a detected cloud, the VPNs found, UDP ports that do not answer from outside, how SSH was left), whether its watcher and timer still run, and whether Cosmos' own public port is among the ports it keeps open. Readings are reused for ten seconds.
// @Tags protection
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse{data=Status}
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/protection [get]
func StatusRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN_READ) != nil {
		return
	}
	if req.Method != "GET" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	json.NewEncoder(w).Encode(utils.APIResponse{Status: "OK", Data: GetStatus()})
}
