package fail2ban

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The routes. All are registered on the admin router (behind the token
// check, the SmartShield and the admin address restriction). Reading needs
// the permission of the logs page; lifting a ban and changing the settings
// need the administrator's permission, which a session only has after the
// password was confirmed.

const maxBody = 16 << 10

// statusFor is the status for the one who asks: with the address the
// request counts as coming from, and whether a proxy reported it.
func statusFor(req *http.Request) Status {
	client := utils.GetClientIP(req)
	status := GetStatus(client)
	status.ClientForwarded = utils.IsForwardedClient(req, client)
	return status
}

func answer(w http.ResponseWriter, data any) {
	json.NewEncoder(w).Encode(utils.APIResponse{Status: "OK", Data: data})
}

// StatusRoute godoc
// @Summary Fail2ban: state, jails and banned addresses
// @Description Whether Fail2ban is installed and running on the server, which of the fork's jails are loaded, their counters and thresholds, the addresses they have banned and until when, and the addresses that are never banned.
// @Tags fail2ban
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse{data=Status}
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/fail2ban [get]
func StatusRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN_READ) != nil {
		return
	}
	if req.Method != "GET" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	answer(w, statusFor(req))
}

// unbanRequest names the address to unban and the jail to unban it from;
// an empty jail means every jail of the fork.
type unbanRequest struct {
	IP   string `json:"ip"`
	Jail string `json:"jail"`
}

// UnbanRoute godoc
// @Summary Fail2ban: lift the ban of an address
// @Description Lifts the ban of one address in one of the fork's jails, or in all of them when no jail is named.
// @Tags fail2ban
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body unbanRequest true "The address, and the jail or an empty string for all"
// @Success 200 {object} utils.APIResponse{data=Status}
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Failure 409 {object} utils.HTTPErrorResult
// @Router /api/fail2ban/unban [post]
func UnbanRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}

	var request unbanRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxBody)).Decode(&request); err != nil {
		utils.HTTPError(w, "The request could not be read", http.StatusBadRequest, "F2B001")
		return
	}
	// The address is parsed here and only the parsed value goes on; the
	// jail selects one of the fixed list, its text is not used.
	address, ok := parseAddress(request.IP)
	if !ok {
		utils.HTTPError(w, "That is not an IP address", http.StatusBadRequest, "F2B002")
		return
	}
	targets := jails
	label := "all"
	if request.Jail != "" {
		known, ok := jailByName(request.Jail)
		if !ok {
			utils.HTTPError(w, "That is not one of the jails of this server", http.StatusBadRequest, "F2B003")
			return
		}
		targets = []jail{known}
		label = known.name
	}

	utils.Log("API: Lift a Fail2ban ban")
	utils.AuditSecurity(req, utils.SecFail2banUnban, address.String(), "jail", label) // security record

	if !clientInstalled() || !ping() {
		utils.HTTPError(w, "Fail2ban is not running on this server", http.StatusConflict, "F2B004")
		return
	}
	loaded, err := loadedJails()
	if err != nil {
		utils.HTTPError(w, "Fail2ban did not answer", http.StatusConflict, "F2B004")
		return
	}
	isLoaded := map[string]bool{}
	for _, name := range loaded {
		isLoaded[name] = true
	}
	failed := false
	for _, target := range targets {
		if !isLoaded[target.name] {
			continue
		}
		if _, err := unban(target, address); err != nil {
			failed = true
		}
	}
	forgetStatus()
	if failed {
		utils.HTTPError(w, "Fail2ban did not lift the ban", http.StatusConflict, "F2B005")
		return
	}
	answer(w, statusFor(req))
}

// SettingsRoute godoc
// @Summary Fail2ban: set the thresholds and the addresses that are never banned
// @Description Writes the thresholds of the fork's jails and the list of addresses that are never banned to one generated file of Fail2ban's configuration and makes Fail2ban read it. Every jail of the fork must be given; numbers must be within the bounds the status reports; list entries must be addresses or networks.
// @Tags fail2ban
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body Settings true "Thresholds per jail and the never-ban list"
// @Success 200 {object} utils.APIResponse{data=Status}
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Failure 409 {object} utils.HTTPErrorResult
// @Router /api/fail2ban/settings [post]
func SettingsRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}

	var request Settings
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		utils.HTTPError(w, "The request could not be read", http.StatusBadRequest, "F2B001")
		return
	}

	utils.Log("API: Save the Fail2ban settings")
	utils.AuditSecurity(req, utils.SecFail2banSettings, "") // security record

	_, err := Apply(request)
	var invalid *SettingsError
	switch {
	case errors.As(err, &invalid):
		utils.HTTPError(w, invalid.Error(), http.StatusBadRequest, "F2B006")
		return
	case errors.Is(err, errNotInstalled), errors.Is(err, errNoInstall):
		utils.HTTPError(w, "Fail2ban, or the fork's files for it, are not installed on this server", http.StatusConflict, "F2B004")
		return
	case err != nil:
		utils.HTTPError(w, "The settings could not be applied", http.StatusConflict, "F2B007")
		return
	}
	answer(w, statusFor(req))
}
