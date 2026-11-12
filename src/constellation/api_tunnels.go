package constellation

import (
	"net/http"
	"encoding/json"

	"github.com/azukaar/cosmos-server/src/utils"
)

// TunnelList godoc
// @Summary List all active Constellation tunnels
// @Tags constellation
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 403 {object} utils.HTTPErrorResult
// @Router /api/constellation/tunnels [get]
func TunnelList(w http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP002")
		return
	}

	if utils.CheckPermissions(w, req, utils.PERM_RESOURCES) != nil {
		return
	}

	// Const_IsTunneled never crosses JSON; send explicit Tunneled + Advertisers fields (see routes_op.go).
	tunnels := GetLocalTunnelCache()
	out := make([]map[string]interface{}, 0, len(tunnels))
	for _, t := range tunnels {
		out = append(out, map[string]interface{}{
			"Route":       t.Route,
			"Targets":     t.Targets,
			"Tunneled":    true,
			"Advertisers": TunnelAdvertisers(t),
		})
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
		"data":   out,
	})
}
