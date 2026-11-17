package configapi

// Fork addition (plan 51): which monitoring store is in use, for the
// External page and the monitoring page.
//
//	GET /cosmos/api/monitoring-store
//
// Answers the state of utils.GetMonitoringStoreState: the store configured
// and the one open, and while PostgreSQL does not answer since when, why
// (the fixed sentences of utils.CheckPostgresStore) and when Cosmos tries
// again. The database server and name only for a reader of the
// configuration; never a password.

import (
	"encoding/json"
	"net/http"

	"github.com/azukaar/cosmos-server/src/utils"
)

// MonitoringStoreRoute godoc
// @Summary Monitoring database: the store in use
// @Description Says whether monitoring records into the local file or PostgreSQL, and while a configured PostgreSQL does not answer since when, why and when Cosmos tries again. The server and database names only for readers of the configuration; never a password.
// @Tags config
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/monitoring-store [get]
func MonitoringStoreRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_RESOURCES_READ) != nil {
		return
	}
	if req.Method != "GET" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	state := utils.GetMonitoringStoreState()
	if !utils.HasPermission(req, utils.PERM_CONFIGURATION_READ) {
		state.Host, state.Database = "", ""
	}
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": state})
}
