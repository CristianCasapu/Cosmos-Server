package telegram

// Fork addition (plan 48): the panel's preview of every kind of message,
// rendered here from the same table and the same code as the real ones,
// with sample values; and "send me this sample".
//
//	GET  /cosmos/api/telegram/kinds    (who may read the configuration)
//	POST /cosmos/api/telegram/sample   {"kind": "..."} (administrators)
//
// The samples hold the table's sample values and the server's name: no
// setting, no secret. A sample goes to the saved chat with the saved bot,
// at most one every few seconds and samplesPerWindow per samplesWindow.

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/azukaar/cosmos-server/src/security"
	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	maxSampleBody    = 1 << 10
	samplesPerWindow = 20
	samplesWindow    = 10 * time.Minute
)

var sampleTimes []time.Time // under testMu

// KindSample is one kind of message as the panel shows it.
type KindSample struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Category string `json:"category"`
	Label    string `json:"label"`
	Closing  bool   `json:"closing,omitempty"`
	// Closes: the opening kind a closing ends (a pair); Neutral: a closing
	// whose own severity is its delivery (review 5)
	Closes  string `json:"closes,omitempty"`
	Neutral bool   `json:"neutral,omitempty"`
	// Record is the severity the security record gives the kind (high,
	// medium, low): the minimum severity of the settings applies to it.
	Record string `json:"record,omitempty"`
	// Own: a message of the notifications themselves; it has no switch.
	Own bool `json:"own,omitempty"`
	// Off: not sent unless switched on by itself.
	Off bool `json:"off,omitempty"`
	// The message as it arrives: text, entities (UTF-16 offsets), button.
	Text     string   `json:"text"`
	Entities []entity `json:"entities"`
	Button   *button  `json:"button,omitempty"`
	// Preview is the first line: what the phone shows before it is opened.
	Preview string `json:"preview"`
}

// KindsAnswer is the answer of the kinds route.
type KindsAnswer struct {
	Kinds      []KindSample      `json:"kinds"`
	Severities []string          `json:"severities"`
	Delivery   map[string]string `json:"delivery"`
}

// samples renders every kind with the settings in use.
func samples(st settings) []KindSample {
	out := []KindSample{}
	for _, kind := range KindNames() {
		nt := sampleNotice(kind)
		m := render(st, nt)
		entities := m.entities
		if entities == nil {
			entities = []entity{}
		}
		sample := KindSample{
			Kind: kind, Severity: nt.severity.String(), Category: nt.category, Label: nt.label(),
			Closing: nt.closing, Own: ownKinds[kind], Off: nt.texts().Off, Closes: nt.texts().Closes, Neutral: nt.texts().Neutral,
			Text: m.text, Entities: entities, Button: m.button, Preview: PreviewLine(m.text),
		}
		if known, ok := security.KindOf(kind); ok {
			sample.Record = known.Severity
		} else if kind == "auth.login.new" {
			sample.Record = ""
		}
		out = append(out, sample)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := parseSeverity(out[i].Severity)
		b, _ := parseSeverity(out[j].Severity)
		return a > b
	})
	return out
}

// KindsRoute godoc
// @Summary Telegram notifications: every kind of message, as it arrives
// @Description Every kind of message with its severity and a sample rendered by the server exactly as it would be sent (text, entities, button), and its first line as a phone's preview shows it. Holds no setting and no secret.
// @Tags telegram
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/telegram/kinds [get]
func KindsRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION_READ) != nil {
		return
	}
	if req.Method != "GET" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	answer := KindsAnswer{Kinds: samples(currentSettings()), Severities: SeverityNames, Delivery: DefaultDelivery}
	json.NewEncoder(w).Encode(utils.APIResponse{Status: "OK", Data: answer})
}

type sampleRequest struct {
	Kind string `json:"kind"`
}

// SampleRoute godoc
// @Summary Telegram notifications: send the sample of one kind of message
// @Description Sends the sample of a kind (as the kinds route shows it) to the saved chat with the saved bot, with the delivery of its severity, and reports Telegram's answer in plain words. Rate-limited.
// @Tags telegram
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Failure 409 {object} utils.HTTPErrorResult
// @Failure 429 {object} utils.HTTPErrorResult
// @Router /api/telegram/sample [post]
func SampleRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	var request sampleRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxSampleBody)).Decode(&request); err != nil || !knownKind(request.Kind) {
		utils.HTTPError(w, "Invalid request", http.StatusBadRequest, "TG001")
		return
	}
	st := currentSettings()
	if !st.usable {
		utils.HTTPError(w, "Save working Telegram settings first: the sample goes to the saved chat", http.StatusConflict, "TG004")
		return
	}

	testMu.Lock()
	now := time.Now()
	if wait := testGap - now.Sub(lastTest); wait > 0 {
		testMu.Unlock()
		utils.HTTPError(w, "Wait a few seconds before sending another message", http.StatusTooManyRequests, "TG003")
		return
	}
	kept := sampleTimes[:0]
	for _, at := range sampleTimes {
		if now.Sub(at) < samplesWindow {
			kept = append(kept, at)
		}
	}
	sampleTimes = kept
	if len(sampleTimes) >= samplesPerWindow {
		testMu.Unlock()
		utils.HTTPError(w, "Enough samples for now: try again in a few minutes", http.StatusTooManyRequests, "TG005")
		return
	}
	sampleTimes = append(sampleTimes, now)
	lastTest = now
	testMu.Unlock()

	nt := sampleNotice(request.Kind)
	m := render(st, nt)
	silent := st.mode(nt) != DeliverSound

	ctx, cancel := context.WithTimeout(req.Context(), requestTimeout+5*time.Second)
	defer cancel()
	_, failure := directClient.sendMessage(ctx, st, outgoing{message: m, silent: silent})
	if failure != nil && failure.Reason == ReasonBadButton && m.button != nil {
		m.button = nil
		_, failure = directClient.sendMessage(ctx, st, outgoing{message: m, silent: silent})
	}

	result := TestResult{Success: failure == nil}
	if failure != nil {
		result.Reason, result.Message = failure.Reason, failure.Message
		utils.Warn("Telegram: the sample message was not accepted: " + failure.Message)
	} else {
		result.Message = "Telegram accepted the sample message."
	}
	json.NewEncoder(w).Encode(utils.APIResponse{Status: "OK", Data: result})
}
