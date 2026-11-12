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

	// A caller that may not read credentials gets host, username and password
	// masked as "***" and sends the mask back. Such a caller can only test the
	// saved settings, exactly as saved, and the reply must not reveal them.
	masked := cfg.Host == "***" || cfg.Username == "***"
	if masked {
		cfg = saved
	} else if cfg.Password == "***" {
		// Reuse the saved password only for the very connection it was saved
		// for: another host or port, or a weaker TLS setting, would hand it over.
		sameConnection := cfg.Host == saved.Host && cfg.Port == saved.Port &&
			cfg.Username == saved.Username && cfg.UseTLS == saved.UseTLS &&
			(!cfg.AllowInsecureTLS || saved.AllowInsecureTLS)
		if !sameConnection {
			utils.HTTPError(w, "Enter the SMTP password to test changed connection settings", http.StatusBadRequest, "TE003")
			return
		}
		cfg.Password = saved.Password
	}

	// Values are used as they would be saved: a test must not pass on input
	// that real delivery would then fail on.
	if strings.TrimSpace(cfg.Host) == "" || strings.TrimSpace(cfg.Port) == "" || strings.TrimSpace(cfg.From) == "" {
		utils.HTTPError(w, "SMTP host, port and sender are required", http.StatusBadRequest, "TE002")
		return
	}

	recipient := strings.TrimSpace(request.Recipient)
	if recipient == "" {
		recipient = cfg.From
	}

	steps, err := utils.SendTestEmail(cfg, recipient)

	failure := ""
	if err != nil {
		failure = err.Error()
	}
	if masked {
		// Step details and errors name the host and the account.
		for i := range steps {
			steps[i].Detail = ""
		}
		if err != nil {
			failure = "the test email could not be sent"
			if len(steps) > 0 {
				failure = "failed at step: " + steps[len(steps)-1].Name
			}
		}
	}

	data := map[string]interface{}{
		"success":   err == nil,
		"steps":     steps,
		"recipient": recipient,
	}
	if err != nil {
		utils.Error("TestEmail: sending failed", err)
		data["error"] = failure
	} else {
		utils.Log("TestEmail: test email sent to " + recipient)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
		"data":   data,
	})
}
