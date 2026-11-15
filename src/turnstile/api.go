package turnstile

// The routes of the settings card, on the admin router:
//
//	GET  /cosmos/api/turnstile   the state (configuration readers)
//	POST /cosmos/api/turnstile   {action, ...}  changes (configuration permission)
//
// Actions: keys {siteKey, secretKey}, create {mode}, try {token},
// settings {forms, appearance, mode}, enable, disable, remove. Every body is
// bounded, unknown fields are refused, every value is checked here whatever
// the form checked. The secret key is never in an answer: readers see that
// one is stored.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/cloudflare"
	"github.com/azukaar/cosmos-server/src/utils"
)

const maxBody = 4 << 10

// WidgetName is the name of a widget Cosmos makes.
const WidgetName = "Cosmos sign-in"

// replaced by the tests
var (
	createWidget = cloudflare.CreateTurnstileWidget
	zoneOf       = cloudflare.TurnstileZone
)

type request struct {
	Action     string `json:"action"`
	SiteKey    string `json:"siteKey"`
	SecretKey  string `json:"secretKey"`
	Token      string `json:"token"`
	Mode       string `json:"mode"`
	Appearance string `json:"appearance"`
	Forms      *struct {
		Login    bool `json:"login"`
		Reset    bool `json:"reset"`
		Register bool `json:"register"`
	} `json:"forms"`
}

func answer(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": data})
}

// failure answers with a fixed reason the card turns into a sentence.
func failure(w http.ResponseWriter, status int, reason string) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "error", "code": "TS010", "message": "Turnstile: " + reason, "reason": reason})
}

func validMode(mode string) bool {
	return mode == "managed" || mode == "non-interactive" || mode == "invisible"
}

func modeOf(mode string) string {
	if validMode(mode) {
		return mode
	}
	return "managed"
}

// Status is the state for the card.
func Status(req *http.Request) map[string]interface{} {
	config := settings()
	panel := PanelPublicName()
	_, zone, hasToken := zoneOf(panel)
	if panel == "" {
		hasToken, zone = false, ""
	}
	appearance := config.Appearance
	if appearance != "interaction-only" {
		appearance = "always"
	}
	secret := ""
	if config.SecretKey != "" {
		secret = utils.MaskedSecret
	}
	confirmed := config.SiteKey != "" && config.SecretKey != "" && config.Confirmed.Keys == keysHash(config.SiteKey, config.SecretKey)
	hostnames := config.Hostnames
	if hostnames == nil {
		hostnames = []string{}
	}
	return map[string]interface{}{
		"enabled":         config.Enabled,
		"active":          active(config),
		"consoleOff":      ConsoleOff(),
		"consoleFile":     ConsolePath(),
		"siteKey":         config.SiteKey,
		"secretKey":       secret,
		"testKeys":        isTestKey(config.SiteKey) || isTestKey(config.SecretKey),
		"forms":           config.FormsInForce(),
		"appearance":      appearance,
		"mode":            modeOf(config.Mode),
		"source":          config.Source,
		"widgetName":      config.WidgetName,
		"hostnames":       hostnames,
		"keysSaved":       config.KeysSaved,
		"confirmed":       confirmed,
		"confirmedAt":     map[bool]interface{}{true: config.Confirmed.At, false: nil}[confirmed],
		"confirmedHost":   map[bool]string{true: config.Confirmed.Hostname, false: ""}[confirmed],
		"panelHost":       panel,
		"requestHost":     hostOf(req.Host),
		"zone":            zone,
		"cloudflareToken": hasToken,
		"problems":        Problems(),
		"testMode":        testMode,
	}
}

// StatusRoute answers GET /api/turnstile.
func StatusRoute(w http.ResponseWriter, req *http.Request) {
	if req.Method == "POST" {
		ChangeRoute(w, req)
		return
	}
	if req.Method != "GET" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION_READ) != nil {
		return
	}
	answer(w, Status(req))
}

// updateConfig changes the stored block under the lock; change returns
// false to leave everything as it is.
func updateConfig(change func(config *utils.ForkTurnstileConfig) bool) bool {
	utils.ConfigLock.Lock()
	defer utils.ConfigLock.Unlock()
	config := utils.ReadConfigFromFile()
	if !change(&config.Turnstile) {
		return false
	}
	utils.SetBaseMainConfig(config)
	resetConsoleCache()
	return true
}

func audit(req *http.Request, what string) {
	utils.AuditSecurity(req, utils.SecConfigSaved, "", "sections", "turnstile", "change", what)
}

// ChangeRoute answers POST /api/turnstile.
func ChangeRoute(w http.ResponseWriter, req *http.Request) {
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION) != nil {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(req.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		utils.HTTPError(w, "Request too large", http.StatusRequestEntityTooLarge, "TS011")
		return
	}
	var body request
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil {
		utils.HTTPError(w, "Invalid request", http.StatusBadRequest, "TS012")
		return
	}

	switch body.Action {
	case "keys":
		saveKeys(w, req, body)
	case "create":
		create(w, req, body)
	case "try":
		try(w, req, body)
	case "settings":
		saveSettings(w, req, body)
	case "enable":
		enable(w, req)
	case "disable":
		updateConfig(func(c *utils.ForkTurnstileConfig) bool { c.Enabled = false; return true })
		audit(req, "disabled")
		answer(w, Status(req))
	case "remove":
		updateConfig(func(c *utils.ForkTurnstileConfig) bool {
			*c = utils.ForkTurnstileConfig{Forms: c.Forms, FormsSet: c.FormsSet, Appearance: c.Appearance, Mode: c.Mode}
			return true
		})
		clearProblems()
		audit(req, "removed")
		answer(w, Status(req))
	default:
		utils.HTTPError(w, "Unknown action", http.StatusBadRequest, "TS013")
	}
}

// testSecret runs the test of a secret before it is kept; false when the
// answer was sent.
func testSecret(w http.ResponseWriter, req *http.Request, secret string) bool {
	ctx, cancel := context.WithTimeout(req.Context(), 15*time.Second)
	defer cancel()
	switch Secret(ctx, secret) {
	case "ok":
		return true
	case "test":
		if testMode {
			return true
		}
		failure(w, http.StatusBadRequest, "test_keys")
	case ResultKeyRefused:
		failure(w, http.StatusBadRequest, "secret_refused")
	default:
		failure(w, http.StatusBadGateway, "unreachable")
	}
	return false
}

func saveKeys(w http.ResponseWriter, req *http.Request, body request) {
	siteKey := strings.TrimSpace(body.SiteKey)
	secret := strings.TrimSpace(body.SecretKey)
	stored := settings()
	if secret == utils.MaskedSecret {
		secret = stored.SecretKey
	}
	if !validKey(siteKey) {
		failure(w, http.StatusBadRequest, "site_key_format")
		return
	}
	if !validKey(secret) {
		failure(w, http.StatusBadRequest, "secret_format")
		return
	}
	if (isTestKey(siteKey) || isTestKey(secret)) && !testMode {
		failure(w, http.StatusBadRequest, "test_keys")
		return
	}
	if !testSecret(w, req, secret) {
		return
	}
	changed := siteKey != stored.SiteKey || secret != stored.SecretKey
	updateConfig(func(c *utils.ForkTurnstileConfig) bool {
		if changed {
			// new keys: off until they were tried from a browser
			c.Enabled = false
			c.Confirmed = utils.ForkTurnstileConfirmed{}
			c.Source, c.WidgetName, c.Hostnames = "manual", "", nil
		}
		c.SiteKey, c.SecretKey, c.KeysSaved = siteKey, secret, now()
		return true
	})
	clearProblems()
	audit(req, "keys")
	answer(w, Status(req))
}

func create(w http.ResponseWriter, req *http.Request, body request) {
	panel := PanelPublicName()
	if panel == "" {
		failure(w, http.StatusConflict, "no_public_name")
		return
	}
	token, zone, ok := zoneOf(panel)
	if !ok {
		failure(w, http.StatusConflict, "no_cloudflare_token")
		return
	}
	mode := strings.TrimSpace(body.Mode)
	if mode == "" {
		mode = modeOf(settings().Mode)
	}
	if !validMode(mode) {
		failure(w, http.StatusBadRequest, "mode")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
	defer cancel()
	// exactly the panel's public name: never the domain itself (which would
	// cover every name under it) and never another of the owner's names
	widget, err := createWidget(ctx, token, zone, WidgetName, []string{panel}, mode)
	if err != nil {
		switch kind := cloudflare.TurnstileErrorKind(err); kind {
		case "forbidden", "invalid_token":
			failure(w, http.StatusForbidden, "permission")
		case "not_found":
			failure(w, http.StatusBadGateway, "account")
		case "rate_limited":
			failure(w, http.StatusBadGateway, "rate_limited")
		default:
			failure(w, http.StatusBadGateway, "cloudflare_"+kindWord(kind))
		}
		return
	}
	updateConfig(func(c *utils.ForkTurnstileConfig) bool {
		c.Enabled = false
		c.Confirmed = utils.ForkTurnstileConfirmed{}
		c.SiteKey, c.SecretKey, c.KeysSaved = widget.SiteKey, widget.Secret, now()
		c.Source, c.WidgetName, c.Hostnames, c.Mode = "api", WidgetName, []string{panel}, mode
		return true
	})
	clearProblems()
	audit(req, "created")
	answer(w, Status(req))
}

func kindWord(kind string) string {
	switch kind {
	case "unreachable", "server", "malformed_token", "other":
		return kind
	}
	return "other"
}

func try(w http.ResponseWriter, req *http.Request, body request) {
	config := settings()
	if config.SiteKey == "" || config.SecretKey == "" {
		failure(w, http.StatusConflict, "no_keys")
		return
	}
	host := hostOf(req.Host)
	panel := PanelPublicName()
	if panel == "" {
		failure(w, http.StatusConflict, "no_public_name")
		return
	}
	if host != panel {
		failure(w, http.StatusConflict, "not_public_name")
		return
	}
	token := strings.TrimSpace(body.Token)
	if token == "" {
		failure(w, http.StatusBadRequest, "no_token")
		return
	}
	if !checks.allow(visitorAddress(req), now()) {
		failure(w, http.StatusTooManyRequests, ReasonTooMany)
		return
	}
	result := Verify(req.Context(), config.SecretKey, token, visitorAddress(req), host, FormTest)
	switch result.Kind {
	case ResultOK:
	case ResultRefused:
		failure(w, http.StatusBadRequest, "token_"+result.Reason)
		return
	case ResultKeyRefused:
		noteProblem(ResultKeyRefused)
		failure(w, http.StatusBadRequest, "secret_refused")
		return
	default:
		failure(w, http.StatusBadGateway, "unreachable")
		return
	}
	hash := keysHash(config.SiteKey, config.SecretKey)
	updateConfig(func(c *utils.ForkTurnstileConfig) bool {
		if keysHash(strings.TrimSpace(c.SiteKey), strings.TrimSpace(c.SecretKey)) != hash {
			return false // the keys changed meanwhile
		}
		c.Confirmed = utils.ForkTurnstileConfirmed{Keys: hash, At: now(), Hostname: host}
		return true
	})
	clearProblems()
	audit(req, "tried")
	answer(w, Status(req))
}

func saveSettings(w http.ResponseWriter, req *http.Request, body request) {
	appearance := strings.TrimSpace(body.Appearance)
	if appearance != "" && appearance != "always" && appearance != "interaction-only" {
		failure(w, http.StatusBadRequest, "appearance")
		return
	}
	mode := strings.TrimSpace(body.Mode)
	if mode != "" && !validMode(mode) {
		failure(w, http.StatusBadRequest, "mode")
		return
	}
	updateConfig(func(c *utils.ForkTurnstileConfig) bool {
		if body.Forms != nil {
			c.FormsSet = true
			c.Forms = utils.ForkTurnstileForms{Login: body.Forms.Login, Reset: body.Forms.Reset, Register: body.Forms.Register}
		}
		if appearance != "" {
			c.Appearance = appearance
		}
		if mode != "" {
			c.Mode = mode
		}
		return true
	})
	audit(req, "settings")
	answer(w, Status(req))
}

func enable(w http.ResponseWriter, req *http.Request) {
	config := settings()
	if config.SiteKey == "" || config.SecretKey == "" {
		failure(w, http.StatusConflict, "no_keys")
		return
	}
	if config.Confirmed.Keys != keysHash(config.SiteKey, config.SecretKey) {
		failure(w, http.StatusConflict, "not_tried")
		return
	}
	// the key may have been revoked since the round trip
	if !testSecret(w, req, config.SecretKey) {
		return
	}
	updateConfig(func(c *utils.ForkTurnstileConfig) bool {
		if c.Confirmed.Keys != keysHash(strings.TrimSpace(c.SiteKey), strings.TrimSpace(c.SecretKey)) {
			return false
		}
		c.Enabled = true
		return true
	})
	clearProblems()
	audit(req, "enabled")
	answer(w, Status(req))
}
