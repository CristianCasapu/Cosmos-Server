package monitorcopy

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/azukaar/cosmos-server/src/utils"
)

// StatusRoute godoc
// @Summary Monitoring history copy: status and offer
// @Description What a copy of the monitoring history between the local file and PostgreSQL can do now (the source's records per table, its size and first and last dates), the running copy's progress per table and how the last one ended. Changes nothing.
// @Tags monitoring
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/monitoring-copy [get]
func StatusRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN_READ) != nil {
		return
	}
	if req.Method != "GET" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	json.NewEncoder(w).Encode(utils.APIResponse{Status: "OK", Data: Status()})
}

type startRequest struct {
	Direction string `json:"direction"`
}

const maxBody = 1024

// StartRoute godoc
// @Summary Monitoring history copy: start or resume
// @Description Starts copying the monitoring history in the background: "to-postgres" copies the local file (opened read-only, never changed) into PostgreSQL, the store in use; "to-local" copies PostgreSQL's records into the local file. A copy that was interrupted continues where it stopped; what is already in the target is not copied again. One copy at a time; recorded in the security record.
// @Tags monitoring
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body startRequest true "direction: to-postgres or to-local"
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Failure 409 {object} utils.HTTPErrorResult
// @Router /api/monitoring-copy/start [post]
func StartRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	var body startRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || (body.Direction != ToPostgres && body.Direction != ToLocal) {
		utils.HTTPError(w, "Invalid request: direction is to-postgres or to-local", http.StatusBadRequest, "MC001")
		return
	}
	if err := Start(body.Direction); err != nil {
		code := "MC002"
		if errors.Is(err, ErrRunning) {
			code = "MC003"
		}
		utils.HTTPError(w, err.Error(), http.StatusConflict, code)
		return
	}
	utils.RecordSecurity(req, utils.SecMonitoringCopy, "", "action", "start", "direction", body.Direction)
	json.NewEncoder(w).Encode(utils.APIResponse{Status: "OK", Data: Status()})
}

// CancelRoute godoc
// @Summary Monitoring history copy: cancel
// @Description Stops the running copy after its current batch; what was copied stays, and starting it again resumes. Takes no input.
// @Tags monitoring
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Failure 409 {object} utils.HTTPErrorResult
// @Router /api/monitoring-copy/cancel [post]
func CancelRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	if !Cancel() {
		utils.HTTPError(w, "No copy is running.", http.StatusConflict, "MC004")
		return
	}
	state.mu.Lock()
	direction := state.direction
	state.mu.Unlock()
	utils.RecordSecurity(req, utils.SecMonitoringCopy, "", "action", "cancel", "direction", direction)
	json.NewEncoder(w).Encode(utils.APIResponse{Status: "OK", Data: Status()})
}
