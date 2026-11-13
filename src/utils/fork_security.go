package utils

// Fork addition: the entry point of the security record.
//
// The record itself (catalogue of kinds, limits, table, Fail2ban file,
// subscribers, read API) is the package src/security. It imports utils, so
// utils cannot import it: what is here is only what the rest of the server
// needs in order to say "this happened", and a function variable that
// security.Start fills in. Until then, and in the tests of other packages,
// recording does nothing.
//
// A hook in upstream's code is one line:
//
//	utils.RecordSecurity(req, utils.SecLoginFailed, nickname, "reason", "wrong_password")
//
// Never pass a secret (password, token, cookie, register key, request body)
// as target or detail, and never a query string.

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// The kinds of security events. The identifiers are stable: they are stored,
// written to the Fail2ban file and matched by filters. Class and severity of
// each are in the catalogue of the security package; a kind that is not in it
// is refused.
const (
	// who logs in, and who fails to
	SecLoginSuccess    = "auth.login.success"
	SecLoginFailed     = "auth.login.failed"
	SecLogout          = "auth.logout"
	SecMFASuccess      = "auth.mfa.success"
	SecMFAFailed       = "auth.mfa.failed"
	SecMFALocked       = "auth.mfa.locked"
	SecMFASetup        = "auth.mfa.setup"
	SecSudoSuccess     = "auth.sudo.success"
	SecSudoFailed      = "auth.sudo.failed"
	SecSessionForged   = "auth.session.forged"
	SecSessionRejected = "auth.session.rejected"
	SecResetRequested  = "auth.reset.requested"
	SecResetRejected   = "auth.reset.rejected"
	SecPasswordChanged = "auth.password.changed"
	SecRegisterFailed  = "auth.register.failed"

	// accounts
	SecUserCreated       = "user.created"
	SecUserDeleted       = "user.deleted"
	SecUserEdited        = "user.edited"
	SecUserRoleChanged   = "user.role.changed"
	SecUserInviteCreated = "user.invite.created"
	SecUserInviteUsed    = "user.invite.used"
	SecUserResetCreated  = "user.reset.created"
	SecUserMFAReset      = "user.mfa.reset"

	// API tokens
	SecTokenCreated  = "token.created"
	SecTokenUpdated  = "token.updated"
	SecTokenDeleted  = "token.deleted"
	SecTokenInvalid  = "token.invalid"
	SecTokenExpired  = "token.expired"
	SecTokenIPDenied = "token.ip.denied"

	// refusals
	SecAccessDenied      = "access.denied"
	SecAccessGrantDenied = "access.grant.denied"
	SecAccessIPDenied    = "access.ip.denied"
	SecAccessLocalDenied = "access.local.denied"
	// Fork addition (Cloudflare): a hostname served through Cloudflare was asked for around it
	SecAccessOriginDenied = "access.origin.denied"

	// the shields
	SecShieldBlocked     = "shield.blocked"
	SecShieldStrike      = "shield.strike"
	SecShieldBanTemp     = "shield.ban.temporary"
	SecShieldBanPerm     = "shield.ban.permanent"
	SecShieldUnban       = "shield.unban"
	SecShieldGeo         = "shield.geo.blocked"
	SecShieldBot         = "shield.bot.blocked"
	SecShieldHostname    = "shield.hostname.unknown"
	SecShieldRateLimited = "shield.ratelimited"
	SecScanProbe         = "scan.probe"

	// what administrators do
	SecConfigSaved          = "config.saved"
	SecRouteCreated         = "route.created"
	SecRouteUpdated         = "route.updated"
	SecRouteDeleted         = "route.deleted"
	SecContainerAction      = "container.action"
	SecContainerDeleted     = "container.deleted"
	SecContainerCreated     = "container.created"
	SecContainerEdited      = "container.edited"
	SecContainerTerminal    = "container.terminal.opened"
	SecHostTerminal         = "host.terminal.opened"
	SecServerUpdate         = "server.update.started"
	SecServerUpdateRejected = "server.update.rejected"
	SecServerRestart        = "server.restart"
	SecSnapshotRestore      = "snapshot.restore.started"
	SecAPIChange            = "api.change"

	// Fail2ban (src/fail2ban). A kind is lower-case letters and dots, hence "firewall".
	SecFail2banBan      = "firewall.ban"
	SecFail2banUnban    = "firewall.unban"
	SecFail2banSettings = "firewall.settings.saved"

	// the record about itself
	SecRecorderDropped = "recorder.dropped"
)

// SecurityEvent is one entry of the security record. A caller fills in Kind
// and what it knows of Actor, IP, Target, Host, Path, Method, Outcome and
// Details; the recorder sets the rest (ID, Time when zero, Class, Severity,
// Count) and bounds and cleans every field.
type SecurityEvent struct {
	ID   int64     `json:"id"`
	Time time.Time `json:"time"`
	Kind string    `json:"kind"`
	// Class is "incident" (hostile or suspicious) or "audit" (an action of a
	// known user); Severity is "info", "low", "medium" or "high". Both come
	// from the catalogue.
	Class    string `json:"class"`
	Severity string `json:"severity"`
	// Actor is the authenticated user the request belongs to, "" when there
	// is none. A name somebody merely typed is a Target, never an Actor.
	Actor string `json:"actor"`
	// IP is the client address as utils.GetClientIP gives it, "" when the
	// event has none.
	IP string `json:"ip"`
	// Target is what the event is about: a nickname, a container, a route, a
	// token name.
	Target string `json:"target"`
	Host   string `json:"host"`
	// Path is the request path without its query string.
	Path   string `json:"path"`
	Method string `json:"method"`
	// Outcome is "ok", "failed", "denied" or "blocked".
	Outcome string `json:"outcome"`
	// Count is 1, or for a summary row the number of events it stands for.
	Count   int               `json:"count"`
	Details map[string]string `json:"details"`
}

var (
	securityRecorder atomic.Value // of func(SecurityEvent)
	securityFlusher  atomic.Value // of func()
)

// SetSecurityRecorder is called once by security.Start: record queues an
// event, flush waits until what is queued has been written.
func SetSecurityRecorder(record func(SecurityEvent), flush func()) {
	securityRecorder.Store(record)
	securityFlusher.Store(flush)
}

// RecordSecurityNow is RecordSecurity for what happens right before the
// process ends (a restart, an update): it waits, two seconds at most, until
// the event has been written.
func RecordSecurityNow(req *http.Request, kind string, target string, details ...string) {
	RecordSecurity(req, kind, target, details...)
	flush, _ := securityFlusher.Load().(func())
	if flush == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { recover() }()
		flush()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

// RecordSecurityEvent hands an event to the recorder. It returns at once
// (the recorder queues), never fails and never panics.
func RecordSecurityEvent(event SecurityEvent) {
	record, _ := securityRecorder.Load().(func(SecurityEvent))
	if record == nil {
		return
	}
	defer func() { recover() }()
	record(event)
}

type securityRequestKey struct{}

// securityRequest is what the audit middleware keeps about one request: the
// entry a handler announced with AuditSecurity, and whether anything was
// recorded for the request at all.
type securityRequest struct {
	recorded atomic.Bool
	pending  atomic.Pointer[SecurityEvent]
}

// SecurityClientIP is the client address of a request for the record: the one
// the ClientRealIP middleware worked out, else the same computation again. No
// header is read here.
func SecurityClientIP(req *http.Request) string {
	if ip, ok := req.Context().Value("ClientID").(string); ok && ip != "" {
		return ip
	}
	return GetClientIP(req)
}

// securityEventOf describes a request: who, from where, what was asked.
// details are key, value pairs; the key "outcome" sets the outcome.
func securityEventOf(req *http.Request, actor string, kind string, target string, details []string) SecurityEvent {
	event := SecurityEvent{Kind: kind, Actor: actor, Target: target}
	for i := 0; i+1 < len(details); i += 2 {
		if details[i] == "outcome" {
			event.Outcome = details[i+1]
			continue
		}
		if event.Details == nil {
			event.Details = map[string]string{}
		}
		event.Details[details[i]] = details[i+1]
	}
	if req == nil {
		return event
	}

	event.IP = SecurityClientIP(req)
	event.Host = req.Host
	event.Method = req.Method
	if req.URL != nil {
		event.Path = req.URL.Path
	}

	auth := GetAuthContext(req)
	if event.Actor == "" {
		event.Actor = auth.Nickname
	}
	if auth.APIToken != nil {
		if event.Actor == "" {
			event.Actor = "token:" + auth.APIToken.Name
		}
		if event.Details == nil {
			event.Details = map[string]string{}
		}
		event.Details["token"] = auth.APIToken.Name
	}
	return event
}

// RecordSecurity records an event about a request, now. The actor is the
// user the request is authenticated as.
func RecordSecurity(req *http.Request, kind string, target string, details ...string) {
	RecordSecurityAs(req, "", kind, target, details...)
}

// RecordSecurityAs is RecordSecurity for the places where the request is not
// (yet) authenticated as the user it is about: a login that just succeeded.
func RecordSecurityAs(req *http.Request, actor string, kind string, target string, details ...string) {
	if req != nil {
		if state, _ := req.Context().Value(securityRequestKey{}).(*securityRequest); state != nil {
			state.recorded.Store(true)
		}
	}
	RecordSecurityEvent(securityEventOf(req, actor, kind, target, details))
}

// AuditSecurity announces what a request is about to do. The entry is
// written when the handler returns, with the outcome its answer shows (see
// SecurityAudit). Called outside that middleware it is recorded at once,
// without an outcome. A later call for the same request replaces an earlier
// one.
func AuditSecurity(req *http.Request, kind string, target string, details ...string) {
	state, _ := req.Context().Value(securityRequestKey{}).(*securityRequest)
	if state == nil {
		RecordSecurityEvent(securityEventOf(req, "", kind, target, details))
		return
	}
	event := securityEventOf(req, "", kind, target, details)
	state.recorded.Store(true)
	state.pending.Store(&event)
}

// securityUnlistedReads are the POST routes that read: no entry for them.
var securityUnlistedReads = []string{
	"/cosmos/api/jobs/get",
	"/cosmos/api/jobs/running",
}

// SecurityAudit is the middleware behind AuditSecurity, placed innermost on
// the routers that authenticate. status reports the status of the answer
// once the handler returned (0 when nothing was written explicitly, which
// net/http sends as 200).
//
// It also records what no hook did: a request of a logged-in user that
// changes something (POST, PUT, PATCH, DELETE) becomes an "api.change" entry
// with its method, path and outcome, so that routes without a hook of their
// own, upstream's future ones included, still leave a trace.
func SecurityAudit(status func(w http.ResponseWriter) int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			state := &securityRequest{}
			req = req.WithContext(context.WithValue(req.Context(), securityRequestKey{}, state))

			defer func() {
				code := 0
				if status != nil {
					code = status(w)
				}
				if pending := state.pending.Load(); pending != nil {
					if pending.Outcome == "" {
						pending.Outcome = securityOutcomeOf(code)
					}
					RecordSecurityEvent(*pending)
					return
				}
				if state.recorded.Load() || !securityChanges(req) {
					return
				}
				event := securityEventOf(req, "", SecAPIChange, "", nil)
				if event.Actor == "" {
					return
				}
				event.Outcome = securityOutcomeOf(code)
				RecordSecurityEvent(event)
			}()

			next.ServeHTTP(w, req)
		})
	}
}

func securityChanges(req *http.Request) bool {
	switch req.Method {
	case "POST", "PUT", "PATCH", "DELETE":
	default:
		return false
	}
	for _, path := range securityUnlistedReads {
		if req.URL.Path == path {
			return false
		}
	}
	return true
}

func securityOutcomeOf(status int) string {
	switch {
	case status == 0 || status < 400:
		return "ok"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "denied"
	default:
		return "failed"
	}
}

// RecordSecuritySessionError records why a session cookie was refused, from
// the error user.RefreshUserToken returned. A cookie that was made up or
// altered is "auth.session.forged"; one that was valid once (expired,
// password changed since, account gone, another domain) is
// "auth.session.rejected". The texts are upstream's: one that is not known
// here is recorded as rejected with the reason "other".
func RecordSecuritySessionError(req *http.Request, err error) {
	if err == nil {
		return
	}
	message := err.Error()
	kind, reason := SecSessionRejected, "other"
	switch {
	case message == "New install", strings.Contains(message, "auth public key"):
		return // not about the cookie
	case message == "Token not valid":
		reason = "expired"
	case strings.Contains(message, "not valid for this domain"):
		reason = "wrong_domain"
	case message == "User not found":
		reason = "unknown_user"
	case strings.Contains(message, "Password cycle changed"):
		reason = "password_changed" // also: logged out since, on any device
	case strings.Contains(message, "older than the maximum session age"):
		reason = "session_too_old"
	case strings.Contains(message, "Session has no start"):
		reason = "session_unknown_start"
	case strings.Contains(message, "before the account was created"):
		reason = "account_recreated"
	case strings.Contains(message, "No permissions for user role"):
		reason = "no_role"
	case strings.Contains(message, "falsified"), strings.Contains(message, "verification error"),
		strings.Contains(message, "illegal base64"), strings.Contains(message, "invalid"):
		kind, reason = SecSessionForged, "bad_token"
	}
	RecordSecurity(req, kind, "", "reason", reason)
}

// SecurityAttemptedName is the name somebody typed into a login form, as the
// record may keep it: only if it has the shape of a nickname (3 to 32
// letters and digits, the rule accounts are created under). People type
// passwords into the name field; a password of this server never has that
// shape, and anything else is replaced.
func SecurityAttemptedName(name string) string {
	if len(name) < 3 || len(name) > 32 {
		return "(not a nickname)"
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') {
			return "(not a nickname)"
		}
	}
	return name
}
