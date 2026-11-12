package proxy

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// API_ShieldBans godoc
// @Summary List SmartShield strikes and bans
// @Description Returns every client with a strike or ban history on this node (the cluster's union when the constellation is up)
// @Tags shield
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 403 {object} utils.HTTPErrorResult
// @Router /api/shield/bans [get]
func API_ShieldBans(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION_READ) != nil {
		return
	}
	if req.Method != "GET" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
		"data":   globalShieldState.statuses(time.Now()),
	})
}

// ShieldUnbanRequest is the body of the unban endpoint.
type ShieldUnbanRequest struct {
	ClientID string `json:"clientID"`
}

// API_ShieldUnban godoc
// @Summary Unban a SmartShield client
// @Description Clears a client's strikes and bans everywhere, and the abuse counter that drops its TCP/UDP connections
// @Tags shield
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body ShieldUnbanRequest true "Client to unban"
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Router /api/shield/unban [post]
func API_ShieldUnban(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION) != nil {
		return
	}
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	var body ShieldUnbanRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.ClientID == "" {
		utils.HTTPError(w, "clientID is required", http.StatusBadRequest, "SH001")
		return
	}

	removed := globalShieldState.unban(body.ClientID)

	utils.TriggerEvent(
		"cosmos.proxy.shield.unban",
		"Proxy Shield "+body.ClientID+" unbanned",
		"info",
		"",
		map[string]interface{}{
			"clientID": body.ClientID,
			"removed":  removed,
			"by":       utils.GetAuthContext(req).Nickname,
		})
	utils.Log("SmartShield: " + body.ClientID + " unbanned by " + utils.GetAuthContext(req).Nickname)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
		"data":   map[string]interface{}{"removed": removed},
	})
}
