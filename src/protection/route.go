package protection

import (
	"encoding/json"
	"net/http"

	"github.com/azukaar/cosmos-server/src/utils"
)

// StatusRoute godoc
// @Summary Protection: the state of the host guard, Fail2ban and the automatic security updates
// @Description Read only. For each of the three protection pieces the fork can install on the server: whether it is installed and active, since when, who set it up, and the command that installs or removes it (to run on the server; Cosmos never runs it). For the host guard also the live state of its nftables table (base chains, drop counters, the ports it keeps open) and whether Cosmos' own public port is among them. Readings are reused for ten seconds.
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
