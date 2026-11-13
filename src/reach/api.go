package reach

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/azukaar/cosmos-server/src/utils"
)

const maxBody = 1024

// Route godoc
// @Summary Can the apps be reached from the internet?
// @Description Per app route: whether it is open to visitors from outside and what could be checked from the server (the name in DNS, the router's port forwarding as far as UPnP tells, the certificate). Nothing is tested from outside. GET answers from a cache of ten minutes; POST checks again (at most every 15 seconds) and, with {"askPublicIP": true}, asks the services DynDNS uses for the server's public address. The addresses are returned to admins only.
// @Tags config
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/reachability [get]
// @Router /api/reachability [post]
func StatusRoute(w http.ResponseWriter, req *http.Request) {
	// who may read the routes may read what they are worth; the addresses
	// themselves only an admin
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION_READ) != nil {
		return
	}

	var resp Response
	switch req.Method {
	case "GET":
		resp = Run(Options{})
	case "POST":
		var body struct {
			AskPublicIP bool `json:"askPublicIP"`
		}
		raw, err := io.ReadAll(io.LimitReader(req.Body, maxBody+1))
		if err != nil || len(raw) > maxBody {
			utils.HTTPError(w, "Request too large", http.StatusBadRequest, "RCH001")
			return
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				utils.HTTPError(w, "Invalid request", http.StatusBadRequest, "RCH002")
				return
			}
		}
		// asking an outside service is for admins
		if body.AskPublicIP && utils.CheckPermissions(w, req, utils.PERM_ADMIN_READ) != nil {
			return
		}
		resp = Run(Options{Force: true, AskPublicIP: body.AskPublicIP})
	default:
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}

	if !utils.HasPermission(req, utils.PERM_ADMIN_READ) {
		resp = hideAddresses(resp)
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
		"data":   resp,
	})
}
