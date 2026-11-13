package utils

// Fork addition: tests of the entry point of the security record
// (fork_security.go) and of the hooks in this package.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// captureSecurity collects what is recorded during a test.
func captureSecurity(t *testing.T) func() []SecurityEvent {
	t.Helper()
	var mu sync.Mutex
	events := []SecurityEvent{}
	SetSecurityRecorder(func(event SecurityEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}, func() {})
	t.Cleanup(func() { SetSecurityRecorder(nil, nil) })
	return func() []SecurityEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]SecurityEvent(nil), events...)
	}
}

func securityTestRequest(method string, target string, auth *AuthContext) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = "203.0.113.7:40000"
	if auth != nil {
		req = req.WithContext(context.WithValue(req.Context(), AuthCtxKey, auth))
	}
	return req
}

func TestSecurityRecordingWithoutARecorderDoesNothing(t *testing.T) {
	SetSecurityRecorder(nil, nil)
	req := securityTestRequest("POST", "http://cosmos.example/cosmos/api/login", nil)
	RecordSecurity(req, SecLoginFailed, "admin")
	AuditSecurity(req, SecUserCreated, "bob")
	RecordSecurityNow(req, SecServerRestart, "")
	RecordSecurityEvent(SecurityEvent{Kind: SecLoginFailed})
}

func TestSecurityRecordingSurvivesARecorderThatPanics(t *testing.T) {
	SetSecurityRecorder(func(SecurityEvent) { panic("recorder failure") }, func() { panic("flush failure") })
	t.Cleanup(func() { SetSecurityRecorder(nil, nil) })
	req := securityTestRequest("POST", "http://cosmos.example/cosmos/api/login", nil)
	RecordSecurity(req, SecLoginFailed, "admin")
	RecordSecurityNow(req, SecServerRestart, "")
}

func TestSecurityEventDescribesTheRequest(t *testing.T) {
	recorded := captureSecurity(t)

	// the address is the peer's: a forwarded-for header from a peer that is
	// not a trusted proxy is not believed, and the query string is not kept
	req := securityTestRequest("POST", "http://cosmos.example/cosmos/api/users?token=cosmos_secret&key=abc", &AuthContext{Nickname: "alice"})
	req.Header.Set("X-Forwarded-For", "198.51.100.99")
	RecordSecurity(req, SecUserCreated, "bob", "role", "Admin", "outcome", "failed", "dangling")

	events := recorded()
	if len(events) != 1 {
		t.Fatalf("%d events, want 1", len(events))
	}
	event := events[0]
	if event.Kind != SecUserCreated || event.Actor != "alice" || event.Target != "bob" || event.IP != "203.0.113.7" ||
		event.Host != "cosmos.example" || event.Path != "/cosmos/api/users" || event.Method != "POST" ||
		event.Outcome != "failed" || len(event.Details) != 1 || event.Details["role"] != "Admin" {
		t.Fatalf("event = %+v", event)
	}
	if strings.Contains(event.Path, "cosmos_secret") {
		t.Fatal("the query string, where tokens and keys travel, was kept")
	}

	// the address the first middleware worked out wins over a new computation
	req = req.WithContext(context.WithValue(req.Context(), "ClientID", "192.0.2.50"))
	RecordSecurity(req, SecUserCreated, "bob")
	if got := recorded()[1].IP; got != "192.0.2.50" {
		t.Fatalf("ip = %q, want the one of the request context", got)
	}

	// a request made with an API token names the token; its owner is the actor
	tokenReq := securityTestRequest("DELETE", "http://cosmos.example/cosmos/api/users/bob", &AuthContext{Nickname: "alice", APIToken: &APITokenContext{Name: "deploy", Owner: "alice"}})
	RecordSecurity(tokenReq, SecUserDeleted, "bob")
	ownerless := securityTestRequest("DELETE", "http://cosmos.example/cosmos/api/users/bob", &AuthContext{APIToken: &APITokenContext{Name: "deploy"}})
	RecordSecurity(ownerless, SecUserDeleted, "bob")
	events = recorded()
	if events[2].Actor != "alice" || events[2].Details["token"] != "deploy" || events[3].Actor != "token:deploy" {
		t.Fatalf("token events = %+v, %+v", events[2], events[3])
	}

	// a login names the user it just authenticated
	RecordSecurityAs(securityTestRequest("POST", "http://cosmos.example/cosmos/api/login", nil), "carol", SecLoginSuccess, "")
	if got := recorded()[4].Actor; got != "carol" {
		t.Fatalf("actor = %q, want carol", got)
	}
}

func TestSecurityCheckPermissionsRecordsRefusals(t *testing.T) {
	recorded := captureSecurity(t)

	user := &AuthContext{Nickname: "bob", Permissions: []Permission{PERM_LOGIN, PERM_RESOURCES_READ}}
	admin := &AuthContext{Nickname: "alice", Permissions: []Permission{PERM_LOGIN, PERM_CONFIGURATION, PERM_ADMIN_READ}, IsSudoed: true}
	notSudoed := &AuthContext{Nickname: "alice", Permissions: []Permission{PERM_LOGIN, PERM_CONFIGURATION}}
	token := &AuthContext{Nickname: "alice", APIToken: &APITokenContext{Name: "reader", Owner: "alice", Permissions: []Permission{PERM_RESOURCES_READ}}}

	for name, c := range map[string]struct {
		auth       *AuthContext
		permission Permission
		status     int
		recorded   bool
	}{
		"nobody":                    {nil, PERM_CONFIGURATION, http.StatusUnauthorized, false},
		"a user without it":         {user, PERM_CONFIGURATION, http.StatusUnauthorized, true},
		"a user with it":            {user, PERM_RESOURCES_READ, http.StatusOK, false},
		"an admin":                  {admin, PERM_CONFIGURATION, http.StatusOK, false},
		"an admin who must confirm": {notSudoed, PERM_CONFIGURATION, http.StatusUnauthorized, false},
		"a token without it":        {token, PERM_CONFIGURATION, http.StatusForbidden, true},
		"a token with it":           {token, PERM_RESOURCES_READ, http.StatusOK, false},
	} {
		before := len(recorded())
		w := httptest.NewRecorder()
		err := CheckPermissions(w, securityTestRequest("GET", "http://cosmos.example/cosmos/api/config", c.auth), c.permission)
		if (err == nil) != (c.status == http.StatusOK) || (err != nil && w.Code != c.status) {
			t.Errorf("%s: error %v, status %d, want %d", name, err, w.Code, c.status)
		}
		events := recorded()[before:]
		if c.recorded != (len(events) == 1) {
			t.Errorf("%s: %d events recorded: %+v", name, len(events), events)
			continue
		}
		if c.recorded {
			event := events[0]
			if event.Kind != SecAccessDenied || event.Actor != c.auth.Nickname || event.IP != "203.0.113.7" ||
				event.Path != "/cosmos/api/config" || event.Details["permission"] != PermissionLabel(c.permission) {
				t.Errorf("%s: event = %+v", name, event)
			}
		}
	}
}

// The audit middleware: what a handler announces is written when it
// returns, with the outcome its answer shows.
func TestSecurityAuditGivesAnnouncedEntriesTheirOutcome(t *testing.T) {
	recorded := captureSecurity(t)
	alice := &AuthContext{Nickname: "alice"}

	for status, want := range map[int]string{0: "ok", 200: "ok", 204: "ok", 307: "ok", 400: "failed", 401: "denied", 403: "denied", 404: "failed", 409: "failed", 500: "failed"} {
		before := len(recorded())
		handler := SecurityAudit(func(http.ResponseWriter) int { return status })(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			AuditSecurity(req, SecContainerAction, "old", "action", "start")
			AuditSecurity(req, SecContainerAction, "jellyfin", "action", "stop") // the later call counts
			if len(recorded()) != before {
				t.Errorf("status %d: the entry was written before the handler returned", status)
			}
		}))
		handler.ServeHTTP(httptest.NewRecorder(), securityTestRequest("GET", "http://cosmos.example/cosmos/api/servapps/jellyfin/manage/stop", alice))

		events := recorded()[before:]
		if len(events) != 1 {
			t.Fatalf("status %d: %d events, want 1: %+v", status, len(events), events)
		}
		event := events[0]
		if event.Kind != SecContainerAction || event.Target != "jellyfin" || event.Details["action"] != "stop" ||
			event.Actor != "alice" || event.Outcome != want {
			t.Errorf("status %d: event = %+v, want outcome %q", status, event, want)
		}
	}

	// an outcome the handler gave is kept
	before := len(recorded())
	SecurityAudit(func(http.ResponseWriter) int { return 200 })(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		AuditSecurity(req, SecContainerAction, "jellyfin", "outcome", "failed")
	})).ServeHTTP(httptest.NewRecorder(), securityTestRequest("GET", "http://cosmos.example/x", alice))
	if got := recorded()[before].Outcome; got != "failed" {
		t.Errorf("outcome = %q, want the one the handler gave", got)
	}

	// outside the middleware an announcement is recorded at once
	before = len(recorded())
	AuditSecurity(securityTestRequest("POST", "http://cosmos.example/cosmos/api/users", alice), SecUserCreated, "bob")
	if events := recorded()[before:]; len(events) != 1 || events[0].Target != "bob" || events[0].Outcome != "" {
		t.Errorf("outside the middleware: %+v", events)
	}

	// a handler that panics still leaves its entry
	before = len(recorded())
	func() {
		defer func() { recover() }()
		SecurityAudit(nil)(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			AuditSecurity(req, SecSnapshotRestore, "/data")
			panic("handler failure")
		})).ServeHTTP(httptest.NewRecorder(), securityTestRequest("POST", "http://cosmos.example/x", alice))
	}()
	if events := recorded()[before:]; len(events) != 1 || events[0].Kind != SecSnapshotRestore {
		t.Errorf("after a panic: %+v", events)
	}
}

// What no hook records still leaves a trace when it changes something.
func TestSecurityAuditRecordsChangesWithoutAHook(t *testing.T) {
	recorded := captureSecurity(t)
	alice := &AuthContext{Nickname: "alice"}
	token := &AuthContext{APIToken: &APITokenContext{Name: "deploy"}}
	nothing := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	hooked := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { RecordSecurity(req, SecSudoFailed, "") })

	for name, c := range map[string]struct {
		method  string
		path    string
		auth    *AuthContext
		handler http.Handler
		status  int
		want    string // the kind recorded, "" for none
		outcome string
	}{
		"a change":                  {"POST", "/cosmos/api/cron", alice, nothing, 200, SecAPIChange, "ok"},
		"a change that failed":      {"PUT", "/cosmos/api/alerts/x", alice, nothing, 400, SecAPIChange, "failed"},
		"a change that was refused": {"DELETE", "/cosmos/api/volume/data", alice, nothing, 401, SecAPIChange, "denied"},
		"a change by a token":       {"PATCH", "/cosmos/api/users/bob", token, nothing, 200, SecAPIChange, "ok"},
		"a read":                    {"GET", "/cosmos/api/cron", alice, nothing, 200, "", ""},
		"a read sent as POST":       {"POST", "/cosmos/api/jobs/get", alice, nothing, 200, "", ""},
		"a change by nobody":        {"POST", "/cosmos/api/cron", nil, nothing, 401, "", ""},
		"a change a hook recorded":  {"POST", "/cosmos/api/sudo", alice, hooked, 500, SecSudoFailed, ""},
		"a preflight":               {"OPTIONS", "/cosmos/api/cron", alice, nothing, 200, "", ""},
	} {
		before := len(recorded())
		SecurityAudit(func(http.ResponseWriter) int { return c.status })(c.handler).ServeHTTP(
			httptest.NewRecorder(), securityTestRequest(c.method, "http://cosmos.example"+c.path+"?secret=1", c.auth))
		events := recorded()[before:]
		if c.want == "" {
			if len(events) != 0 {
				t.Errorf("%s: recorded %+v, want nothing", name, events)
			}
			continue
		}
		if len(events) != 1 || events[0].Kind != c.want {
			t.Errorf("%s: recorded %+v, want one %s", name, events, c.want)
			continue
		}
		if c.want == SecAPIChange {
			event := events[0]
			if event.Outcome != c.outcome || event.Path != c.path || event.Method != c.method || event.Actor == "" || event.IP != "203.0.113.7" {
				t.Errorf("%s: event = %+v", name, event)
			}
		}
	}
}

func TestSecuritySessionErrors(t *testing.T) {
	recorded := captureSecurity(t)
	req := securityTestRequest("GET", "http://cosmos.example/cosmos/api/me", nil)

	for message, want := range map[string][2]string{
		"Token likely falsified":                   {SecSessionForged, "bad_token"},
		"ed25519: verification error":              {SecSessionForged, "bad_token"},
		"illegal base64 data at input byte 4":      {SecSessionForged, "bad_token"},
		"Token not valid":                          {SecSessionRejected, "expired"},
		"JWT Token not valid for this domain":      {SecSessionRejected, "wrong_domain"},
		"User not found":                           {SecSessionRejected, "unknown_user"},
		"Password cycle changed, token is too old": {SecSessionRejected, "password_changed"},
		"No permissions for user role":             {SecSessionRejected, "no_role"},
		"something upstream added":                 {SecSessionRejected, "other"},
	} {
		before := len(recorded())
		RecordSecuritySessionError(req, errors.New(message))
		events := recorded()[before:]
		if len(events) != 1 || events[0].Kind != want[0] || events[0].Details["reason"] != want[1] || events[0].IP != "203.0.113.7" {
			t.Errorf("%q: recorded %+v, want %s (%s)", message, events, want[0], want[1])
		}
	}

	before := len(recorded())
	RecordSecuritySessionError(req, nil)
	RecordSecuritySessionError(req, errors.New("New install"))
	RecordSecuritySessionError(req, errors.New("Cannot read auth public key"))
	if events := recorded()[before:]; len(events) != 0 {
		t.Errorf("recorded %+v for errors that are not about the cookie", events)
	}
}

func TestSecurityAttemptedName(t *testing.T) {
	for in, want := range map[string]string{
		"admin":                            "admin",
		"Alice42":                          "Alice42",
		"ab":                               "(not a nickname)",
		"":                                 "(not a nickname)",
		"Tr0ub4dor&3":                      "(not a nickname)", // a password typed into the wrong field
		"correct horse battery":            "(not a nickname)",
		"admin'--":                         "(not a nickname)",
		"admin\nroot":                      "(not a nickname)",
		"élise":                            "(not a nickname)",
		strings.Repeat("a", 33):            "(not a nickname)",
		"<script>alert(1)</script>":        "(not a nickname)",
		"abcdefghijklmnopqrstuvwxyz012345": "abcdefghijklmnopqrstuvwxyz012345",
	} {
		if got := SecurityAttemptedName(in); got != want {
			t.Errorf("SecurityAttemptedName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSecurityMiddlewaresRecordWhatTheyRefuse(t *testing.T) {
	recorded := captureSecurity(t)

	previous := GetBaseMainConfig()
	config := Config{}
	config.HTTPConfig.Hostname = "cosmos.example"
	LoadBaseMainConfig(config)
	t.Cleanup(func() { LoadBaseMainConfig(previous) })
	previousMetrics := PushShieldMetrics
	PushShieldMetrics = func(string) {}
	t.Cleanup(func() { PushShieldMetrics = previousMetrics })

	reached := 0
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached++ })
	withClient := func(req *http.Request) *http.Request {
		return req.WithContext(context.WithValue(req.Context(), "ClientID", "203.0.113.7"))
	}

	// a hostname this server does not serve
	w := httptest.NewRecorder()
	EnsureHostname(next).ServeHTTP(w, withClient(securityTestRequest("GET", "http://198.51.100.5/cosmos-ui/", nil)))
	wAPI := httptest.NewRecorder()
	EnsureHostnameCosmosAPI(next).ServeHTTP(wAPI, withClient(securityTestRequest("GET", "http://198.51.100.5/cosmos/api/config", nil)))
	// its own
	EnsureHostname(next).ServeHTTP(httptest.NewRecorder(), withClient(securityTestRequest("GET", "http://cosmos.example/cosmos-ui/", nil)))

	// an address the admin whitelist does not have, and one it has
	restricted := Restrictions(false, []string{"192.0.2.0/24"})(next)
	wIP := httptest.NewRecorder()
	restricted.ServeHTTP(wIP, withClient(securityTestRequest("GET", "http://cosmos.example/cosmos/api/config", nil)))
	allowed := securityTestRequest("GET", "http://cosmos.example/cosmos/api/config", nil)
	restricted.ServeHTTP(httptest.NewRecorder(), allowed.WithContext(context.WithValue(allowed.Context(), "ClientID", "192.0.2.9")))

	if w.Code != http.StatusBadRequest || wAPI.Code != http.StatusBadRequest || wIP.Code != http.StatusForbidden || reached != 2 {
		t.Fatalf("answers %d, %d, %d and %d requests let through: the hooks changed what callers see", w.Code, wAPI.Code, wIP.Code, reached)
	}
	events := recorded()
	if len(events) != 3 {
		t.Fatalf("%d events, want 3: %+v", len(events), events)
	}
	for i, want := range []string{SecShieldHostname, SecShieldHostname, SecAccessIPDenied} {
		if events[i].Kind != want || events[i].IP != "203.0.113.7" {
			t.Errorf("event %d = %+v, want %s from 203.0.113.7", i, events[i], want)
		}
	}
	if events[0].Host != "198.51.100.5" {
		t.Errorf("the hostname that was asked for is %q", events[0].Host)
	}
}
