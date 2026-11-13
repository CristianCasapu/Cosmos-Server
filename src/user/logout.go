package user

import (
	"net/http"
	"encoding/json"
	"github.com/azukaar/cosmos-server/src/utils"
)

// UserLogout godoc
// @Summary User logout
// @Description Logs out the current user by clearing authentication cookies
// @Tags auth
// @Produce json
// @Success 200 {object} utils.APIResponse
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/logout [get]
func UserLogout(w http.ResponseWriter, req *http.Request) {
	if(req.Method == "GET") {
		utils.Debug("UserLogout: Logging out user")

		recordLogout(req) // Fork addition: security record
		// Fork addition: logging out ends every session of the account, on every device, see fork_session_life.go
		if !endSessionsAtLogout(w, req) {
			return
		}
		logOutUser(w, req);

		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "OK",
		})
	} else {
		utils.Error("UserLogin: Method not allowed" + req.Method, nil)
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
}