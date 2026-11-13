package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	maxTestBody = 16 << 10
	testGap     = 3 * time.Second
)

var (
	testMu   sync.Mutex
	lastTest time.Time
)

// StatusRoute godoc
// @Summary Telegram notifications: state of deliveries
// @Description Tells when the last message was sent, the last failure in plain words, and how many messages wait. Holds no setting: neither the token nor the chat.
// @Tags telegram
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/telegram [get]
func StatusRoute(w http.ResponseWriter, req *http.Request) {
	// For anyone who may read the configuration: the settings page loads it.
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION_READ) != nil {
		return
	}
	if req.Method != "GET" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}

	status := Status{}
	if n := std.Load(); n != nil {
		status = n.Status()
	} else {
		st := currentSettings()
		status.Enabled, status.Ready = st.enabled, st.usable
	}
	json.NewEncoder(w).Encode(utils.APIResponse{Status: "OK", Data: status})
}

type testRequest struct {
	Telegram utils.TelegramConfig
}

// TestResult is the answer of the test route.
type TestResult struct {
	Success bool `json:"success"`
	// Reason is an identifier ("wrong_token", "chat_not_found", …), Message
	// the same in plain words.
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message"`
}

// TestRoute godoc
// @Summary Telegram notifications: send a test message
// @Description Sends one test message with the settings in the request (they do not need to be saved; an empty or masked token or chat means the saved one) and reports Telegram's answer in plain words
// @Tags telegram
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Failure 429 {object} utils.HTTPErrorResult
// @Router /api/telegram/test [post]
func TestRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}

	var request testRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxTestBody)).Decode(&request); err != nil {
		utils.HTTPError(w, "Invalid request", http.StatusBadRequest, "TG001")
		return
	}

	// What the form does not hold is taken from what is saved. The saved
	// token only ever goes to Telegram's own address, so using it with
	// another chat gives nothing away.
	saved := utils.GetMainConfig()
	cfg := request.Telegram
	if cfg.BotToken == "" || cfg.BotToken == MaskedValue {
		cfg.BotToken = saved.Telegram.BotToken
	}
	if cfg.ChatID == MaskedValue {
		cfg.ChatID = saved.Telegram.ChatID
	}
	cfg.Enabled = true
	if err := ValidateConfig(&cfg); err != nil {
		utils.HTTPError(w, err.Error(), http.StatusBadRequest, "TG002")
		return
	}

	// one test at a time, and not in a row: each is a request to Telegram
	testMu.Lock()
	if wait := testGap - time.Since(lastTest); wait > 0 {
		testMu.Unlock()
		utils.HTTPError(w, "Wait a few seconds before sending another test message", http.StatusTooManyRequests, "TG003")
		return
	}
	lastTest = time.Now()
	testMu.Unlock()

	config := saved
	config.Telegram = cfg
	st := settingsOf(config)

	c := &composer{}
	header(c, st, "Test")
	c.fixed("This is a test message: notifications from this server arrive in this chat.").line()
	c.fixed(time.Now().Format(stampLayout))
	text, entities := c.result()

	ctx, cancel := context.WithTimeout(req.Context(), requestTimeout+5*time.Second)
	defer cancel()
	failure := directClient.send(ctx, st, text, entities, false)

	result := TestResult{Success: failure == nil}
	if failure != nil {
		result.Reason, result.Message = failure.Reason, failure.Message
		utils.Warn("Telegram: the test message was not accepted: " + failure.Message)
	} else {
		result.Message = "Telegram accepted the test message."
		utils.Log("Telegram: test message sent")
		// the saved settings were just shown to work: the status says so,
		// and nothing is held back any longer
		if n := std.Load(); n != nil && st.fingerprint == currentSettings().fingerprint {
			n.delivered()
		}
	}
	json.NewEncoder(w).Encode(utils.APIResponse{Status: "OK", Data: result})
}
