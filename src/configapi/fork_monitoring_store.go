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

// monitoringStoreState is utils.GetMonitoringStoreState; replaced by the tests.
var monitoringStoreState = utils.GetMonitoringStoreState

// MonitoringStoreRoute godoc
// @Summary Monitoring database: the store in use
// @Description Says whether monitoring records into the local file or PostgreSQL, and while a configured PostgreSQL does not answer since when, why and when Cosmos tries again. The server, the database, the sentences and the advice only for readers of the configuration (they name the server, the user and the containers); never a password.
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
	state := monitoringStoreState()
	if !utils.HasPermission(req, utils.PERM_CONFIGURATION_READ) {
		// the sentences name the server, the user, the database and the
		// containers (review S3): the reason only
		state.Host, state.Database, state.Message, state.Advice = "", "", "", ""
	}
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": state})
}
