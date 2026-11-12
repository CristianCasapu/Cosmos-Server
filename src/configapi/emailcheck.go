package configapi

// Fork addition: lets an admin try SMTP settings, saved or not yet saved.

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

type testEmailRequest struct {
	EmailConfig utils.EmailConfig
	Recipient   string
}

// ConfigApiTestEmail godoc
// @Summary Send a test email
// @Description Sends a test email with the SMTP settings in the request (they do not need to be saved) and reports each step of the attempt
// @Tags config
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/config/test-email [post]
func ConfigApiTestEmail(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}

	if req.Method != "POST" {
		utils.Error("TestEmail: Method not allowed "+req.Method, nil)
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}

	var request testEmailRequest
	if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
		utils.Error("TestEmail: Invalid request", err)
		utils.HTTPError(w, "Invalid request", http.StatusBadRequest, "TE001")
		return
	}

	cfg := request.EmailConfig
	saved := utils.GetMainConfig().EmailConfig

	// The settings page receives these fields masked when the admin may not
	// read credentials, and sends the mask back untouched.
	if cfg.Host == "***" {
		cfg.Host = saved.Host
	}
	if cfg.Username == "***" {
		cfg.Username = saved.Username
	}
	if cfg.Password == "***" || cfg.Password == "" {
		// Only reuse the saved password for the server and account it belongs
		// to; otherwise a test against another host would hand it over.
		if cfg.Host == saved.Host && cfg.Username == saved.Username {
			cfg.Password = saved.Password
		} else {
			cfg.Password = ""
		}
	}

	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.Port = strings.TrimSpace(cfg.Port)
	cfg.From = strings.TrimSpace(cfg.From)
	if cfg.Host == "" || cfg.Port == "" || cfg.From == "" {
		utils.HTTPError(w, "SMTP host, port and sender are required", http.StatusBadRequest, "TE002")
		return
	}

	recipient := strings.TrimSpace(request.Recipient)
	if recipient == "" {
		recipient = cfg.From
	}

	steps, err := utils.SendTestEmail(cfg, recipient)

	data := map[string]interface{}{
		"success":   err == nil,
		"steps":     steps,
		"recipient": recipient,
	}
	if err != nil {
		utils.Error("TestEmail: sending failed", err)
		data["error"] = err.Error()
	} else {
		utils.Log("TestEmail: test email sent to " + recipient)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
		"data":   data,
	})
}
