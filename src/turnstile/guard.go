package turnstile

// The check on the sign-in router, and what the sign-in pages are told.

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The answers of a refused form. The codes are what the page turns into a
// sentence; the message is fixed text, never Cloudflare's.
const (
	CodeMissing     = "TS001"
	CodeRefused     = "TS002"
	CodeTooMany     = "TS003"
	CodeUnavailable = "TS004"
)

func refuse(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"status": "error", "code": code, "message": message})
}

// formOf names the form a request posts to, "" for any other route.
func formOf(req *http.Request) string {
	route := mux.CurrentRoute(req)
	if route == nil {
		return ""
	}
	template, err := route.GetPathTemplate()
	if err != nil {
		return ""
	}
	return routeForms[template]
}

// visitorAddress is the visitor's address as the fork determines it (the
// ClientRealIP middleware), never from a header read here.
func visitorAddress(req *http.Request) string {
	if address, _ := req.Context().Value("ClientID").(string); address != "" {
		return address
	}
	return utils.GetClientIP(req)
}

// applies says whether a request to a form must carry a good token.
func applies(config utils.ForkTurnstileConfig, form string, req *http.Request) bool {
	if form == "" || !active(config) || !formOn(config, form) {
		return false
	}
	if auth := utils.GetAuthContext(req); auth != nil && auth.APIToken != nil {
		// a machine with a valid API token (the token middleware ran before)
		return false
	}
	return !IsLocal(req)
}

// Middleware is the check, on the sign-in router after its shield and
// token middlewares. Only POST of the three forms is concerned; everything
// else passes untouched (the handlers answer other methods themselves).
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "POST" {
			next.ServeHTTP(w, req)
			return
		}
		form := formOf(req)
		config := settings()
		if !applies(config, form, req) {
			next.ServeHTTP(w, req)
			return
		}

		token := req.Header.Get(HeaderName)
		if token == "" {
			utils.RecordSecurity(req, utils.SecTurnstileMissing, "", "form", form) // security record
			refuse(w, http.StatusForbidden, CodeMissing, "The sign-in check of Cloudflare is missing")
			return
		}
		if !validToken(token) {
			utils.RecordSecurity(req, utils.SecTurnstileFailed, "", "form", form, "reason", ReasonMalformed) // security record
			refuse(w, http.StatusForbidden, CodeRefused, "The sign-in check of Cloudflare was refused")
			return
		}
		address := visitorAddress(req)
		if !checks.allow(address, now()) {
			utils.RecordSecurity(req, utils.SecTurnstileFailed, "", "form", form, "reason", ReasonTooMany) // security record
			refuse(w, http.StatusTooManyRequests, CodeTooMany, "Too many sign-in checks: wait a minute")
			return
		}

		result := Verify(req.Context(), config.SecretKey, token, address, hostOf(req.Host), form)
		switch result.Kind {
		case ResultOK:
			clearProblem(ResultUnreachable)
			next.ServeHTTP(w, req)
		case ResultRefused:
			utils.RecordSecurity(req, utils.SecTurnstileFailed, "", "form", form, "reason", result.Reason) // security record
			refuse(w, http.StatusForbidden, CodeRefused, "The sign-in check of Cloudflare was refused")
		default:
			utils.RecordSecurity(req, utils.SecTurnstileUnavailable, "", "form", form, "reason", result.Kind) // security record
			noteProblem(result.Kind)
			refuse(w, http.StatusServiceUnavailable, CodeUnavailable, "The sign-in check of Cloudflare cannot be made right now")
		}
	})
}

// Public is what the sign-in pages learn through the anonymous read they
// already make (GET /api/can-send-email): the site key and the forms that
// ask, only when the check applies to this visitor; nil otherwise. A site
// key is public by design: it is in every page that shows the widget.
func Public(req *http.Request) map[string]interface{} {
	config := settings()
	if !active(config) || IsLocal(req) {
		return nil
	}
	forms := config.FormsInForce()
	if !forms.Login && !forms.Reset && !forms.Register {
		return nil
	}
	appearance := config.Appearance
	if appearance != "interaction-only" {
		appearance = "always"
	}
	return map[string]interface{}{
		"siteKey":    config.SiteKey,
		"forms":      map[string]bool{FormLogin: forms.Login, FormReset: forms.Reset, FormRegister: forms.Register},
		"appearance": appearance,
	}
}
