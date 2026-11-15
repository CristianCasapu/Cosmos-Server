package turnstile

// Fork addition: tests of the Turnstile check. Nothing here reaches the
// network: siteverify is the stand-in of cffake (behind the transport), and
// a call to any other address fails the test.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/cloudflare"
	"github.com/azukaar/cosmos-server/src/cloudflare/cffake"
	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	siteKey   = "0x4AAAAAAAsiteKEYfortests"
	secretKey = "0x4AAAAAAAsecretKEYfortests0123456"
	panelName = "cosmos.example.org"
	stranger  = "203.0.113.7"
)

type toStandIn struct {
	t      *testing.T
	target *url.URL
}

func (f *toStandIn) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.String() != siteverifyURL {
		f.t.Errorf("a request left for %s, not for siteverify", req.URL.String())
	}
	clone := req.Clone(req.Context())
	clone.URL.Scheme, clone.URL.Host = f.target.Scheme, f.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

type env struct {
	sv     *cffake.Siteverify
	server *httptest.Server
	mu     sync.Mutex
	events []utils.SecurityEvent
	notes  []string
}

func (e *env) recorded() []utils.SecurityEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]utils.SecurityEvent(nil), e.events...)
}

func (e *env) kinds() []string {
	out := []string{}
	for _, event := range e.recorded() {
		out = append(out, event.Kind)
	}
	return out
}

// setup: a configuration whose panel is cosmos.example.org, Turnstile on
// with keys the stand-in knows, events and notifications captured.
func setup(t *testing.T, change func(config *utils.Config)) *env {
	t.Helper()
	e := &env{sv: cffake.NewSiteverify(secretKey)}
	e.server = httptest.NewServer(e.sv)
	target, _ := url.Parse(e.server.URL)
	previousTransport := transport
	transport = &toStandIn{t: t, target: target}

	previousFolder := utils.CONFIGFOLDER
	previousConfig := utils.GetBaseMainConfig()
	utils.CONFIGFOLDER = t.TempDir() + "/"
	config := utils.Config{}
	config.HTTPConfig.Hostname = panelName
	config.HTTPConfig.HTTPSPort = "2053"
	config.Turnstile = utils.ForkTurnstileConfig{Enabled: true, SiteKey: siteKey, SecretKey: secretKey,
		Confirmed: utils.ForkTurnstileConfirmed{Keys: keysHash(siteKey, secretKey)}}
	if change != nil {
		change(&config)
	}
	utils.SetBaseMainConfig(config)

	utils.SetSecurityRecorder(func(event utils.SecurityEvent) {
		e.mu.Lock()
		e.events = append(e.events, event)
		e.mu.Unlock()
	}, func() {})
	previousNotify := notifyAdmins
	notifyAdmins = func(title string, message string, level string) {
		e.mu.Lock()
		e.notes = append(e.notes, title+" "+message)
		e.mu.Unlock()
	}
	previousLocal := IsLocal
	IsLocal = func(req *http.Request) bool { return req.Header.Get("X-Test-Local") == "1" }
	previousTimes := []time.Duration{attemptTimeout, overallTimeout, retryNeeds}
	checks.reset()
	spentMu.Lock()
	spent = map[[32]byte]time.Time{}
	spentMu.Unlock()
	clearProblems()
	resetConsoleCache()

	t.Cleanup(func() {
		e.server.Close()
		transport = previousTransport
		notifyAdmins = previousNotify
		IsLocal = previousLocal
		attemptTimeout, overallTimeout, retryNeeds = previousTimes[0], previousTimes[1], previousTimes[2]
		utils.SetSecurityRecorder(func(utils.SecurityEvent) {}, func() {})
		utils.CONFIGFOLDER = previousFolder
		utils.LoadBaseMainConfig(previousConfig)
		checks.reset()
		clearProblems()
		resetConsoleCache()
	})
	return e
}

// router is the sign-in router with the check, and handlers that say they ran.
func router(reached *int) *mux.Router {
	r := mux.NewRouter()
	sub := r.PathPrefix("/cosmos").Subrouter()
	for _, path := range []string{"/api/login", "/api/password-reset", "/api/register", "/api/mfa"} {
		sub.HandleFunc(path, func(w http.ResponseWriter, req *http.Request) {
			*reached++
			w.Write([]byte(`{"status":"OK"}`))
		})
	}
	sub.Use(Middleware)
	return r
}

func send(r http.Handler, method string, path string, token string, change func(req *http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "https://"+panelName+":2053"+path, strings.NewReader(`{"nickname":"admin","password":"Secret-123"}`))
	req.Host = panelName + ":2053"
	req.RemoteAddr = stranger + ":40000"
	if token != "" {
		req.Header.Set(HeaderName, token)
	}
	if change != nil {
		change(req)
	}
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, req)
	return recorder
}

func code(recorder *httptest.ResponseRecorder) string {
	var answer map[string]interface{}
	json.Unmarshal(recorder.Body.Bytes(), &answer)
	text, _ := answer["code"].(string)
	return text
}

func TestAGoodTokenLetsTheFormThrough(t *testing.T) {
	e := setup(t, nil)
	reached := 0
	r := router(&reached)
	e.sv.Issue("good-token-1", panelName, "cosmos-login")
	if rec := send(r, "POST", "/cosmos/api/login", "good-token-1", nil); rec.Code != 200 || reached != 1 {
		t.Fatalf("good token: status %d, reached %d: %s", rec.Code, reached, rec.Body.String())
	}
	calls := e.sv.Calls(false)
	if len(calls) != 1 || calls[0].RemoteIP != stranger || calls[0].Response != "good-token-1" || !calls[0].SecretKnown || calls[0].IdempotencyKey == "" {
		t.Errorf("siteverify was asked %+v", calls)
	}
	// once only: a second use is refused here, before Cloudflare is asked again
	if rec := send(r, "POST", "/cosmos/api/login", "good-token-1", nil); rec.Code != 403 || code(rec) != CodeRefused || reached != 1 {
		t.Errorf("second use: status %d code %s, reached %d", rec.Code, code(rec), reached)
	}
	if len(e.sv.Calls(false)) != 1 {
		t.Errorf("a repeated token was sent to Cloudflare again")
	}
	if got := e.recorded(); len(got) != 1 || got[0].Kind != utils.SecTurnstileFailed || got[0].Details["reason"] != ReasonUsed || got[0].Details["form"] != FormLogin {
		t.Errorf("recorded %+v", got)
	}
}

func TestEveryRefusalStopsBeforeTheHandler(t *testing.T) {
	cases := []struct {
		name, path, token, wantCode, wantKind, wantReason string
		issue                                             func(sv *cffake.Siteverify)
		status                                            int
	}{
		{"no token", "/cosmos/api/login", "", CodeMissing, utils.SecTurnstileMissing, "", nil, 403},
		{"unknown token", "/cosmos/api/login", "nope", CodeRefused, utils.SecTurnstileFailed, ReasonInvalid, nil, 403},
		{"other host", "/cosmos/api/login", "t-host", CodeRefused, utils.SecTurnstileFailed, ReasonHostname, func(sv *cffake.Siteverify) { sv.Issue("t-host", "evil.example.net", "cosmos-login") }, 403},
		{"other form", "/cosmos/api/login", "t-form", CodeRefused, utils.SecTurnstileFailed, ReasonAction, func(sv *cffake.Siteverify) { sv.Issue("t-form", panelName, "cosmos-reset") }, 403},
		{"no action", "/cosmos/api/register", "t-noaction", CodeRefused, utils.SecTurnstileFailed, ReasonAction, func(sv *cffake.Siteverify) { sv.Issue("t-noaction", panelName, "") }, 403},
		{"control characters", "/cosmos/api/login", "a\tb", CodeRefused, utils.SecTurnstileFailed, ReasonMalformed, nil, 403},
		{"too long", "/cosmos/api/login", strings.Repeat("x", 2049), CodeRefused, utils.SecTurnstileFailed, ReasonMalformed, nil, 403},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t, nil)
			if c.issue != nil {
				c.issue(e.sv)
			}
			reached := 0
			rec := send(router(&reached), "POST", c.path, c.token, nil)
			if rec.Code != c.status || code(rec) != c.wantCode || reached != 0 {
				t.Fatalf("status %d code %q reached %d: %s", rec.Code, code(rec), reached, rec.Body.String())
			}
			got := e.recorded()
			if len(got) != 1 || got[0].Kind != c.wantKind || got[0].Details["reason"] != c.wantReason || got[0].IP != stranger {
				t.Errorf("recorded %+v", got)
			}
			if strings.Contains(rec.Body.String(), secretKey) {
				t.Errorf("the answer names the secret")
			}
		})
	}
}

func TestAnOldTokenIsRefused(t *testing.T) {
	e := setup(t, nil)
	previous := now
	defer func() { now = previous }()
	e.sv.Issue("old", panelName, "cosmos-login")
	// Cloudflare would still say yes (its clock): the age is checked here too
	now = func() time.Time { return time.Now().Add(6 * time.Minute) }
	reached := 0
	if rec := send(router(&reached), "POST", "/cosmos/api/login", "old", nil); rec.Code != 403 || reached != 0 {
		t.Errorf("an old token: status %d reached %d", rec.Code, reached)
	}
}

func TestWhoIsNeverAsked(t *testing.T) {
	cases := []struct {
		name   string
		config func(*utils.Config)
		req    func(*http.Request)
		method string
		path   string
	}{
		{"switched off", func(c *utils.Config) { c.Turnstile.Enabled = false }, nil, "POST", "/cosmos/api/login"},
		{"no secret", func(c *utils.Config) { c.Turnstile.SecretKey = "" }, nil, "POST", "/cosmos/api/login"},
		{"form switched off", func(c *utils.Config) {
			c.Turnstile.FormsSet = true
			c.Turnstile.Forms = utils.ForkTurnstileForms{Reset: true, Register: true}
		}, nil, "POST", "/cosmos/api/login"},
		{"local network", nil, func(r *http.Request) { r.Header.Set("X-Test-Local", "1") }, "POST", "/cosmos/api/login"},
		{"API token", nil, func(r *http.Request) {
			*r = *r.WithContext(context.WithValue(r.Context(), utils.AuthCtxKey, &utils.AuthContext{Nickname: "bot", APIToken: &utils.APITokenContext{Name: "ci"}}))
		}, "POST", "/cosmos/api/login"},
		{"two-factor step", nil, nil, "POST", "/cosmos/api/mfa"},
		{"not a POST", nil, nil, "GET", "/cosmos/api/login"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t, c.config)
			reached := 0
			if rec := send(router(&reached), c.method, c.path, "", c.req); rec.Code != 200 || reached != 1 {
				t.Errorf("status %d reached %d: %s", rec.Code, reached, rec.Body.String())
			}
			if len(e.sv.Calls(false)) != 0 || len(e.recorded()) != 0 {
				t.Errorf("asked Cloudflare %v, recorded %v", e.sv.Calls(false), e.kinds())
			}
		})
	}
}

func TestTheThreeFormsAskByDefault(t *testing.T) {
	e := setup(t, nil)
	reached := 0
	r := router(&reached)
	for _, path := range []string{"/cosmos/api/login", "/cosmos/api/password-reset", "/cosmos/api/register"} {
		if rec := send(r, "POST", path, "", nil); rec.Code != 403 {
			t.Errorf("%s: status %d without a token", path, rec.Code)
		}
	}
	// a path that only looks like one is not routed to the form at all
	if rec := send(r, "POST", "/cosmos/api/login/", "", nil); rec.Code == 200 && reached > 0 {
		t.Errorf("a trailing slash reached a handler without the check")
	}
	if reached != 0 {
		t.Errorf("%d handlers ran", reached)
	}
	_ = e
}

func TestTheConsoleFileSwitchesTheCheckOff(t *testing.T) {
	setup(t, nil)
	reached := 0
	r := router(&reached)
	if rec := send(r, "POST", "/cosmos/api/login", "", nil); rec.Code != 403 {
		t.Fatalf("before: %d", rec.Code)
	}
	if err := os.WriteFile(filepath.Join(utils.CONFIGFOLDER, ConsoleFile), nil, 0600); err != nil {
		t.Fatal(err)
	}
	resetConsoleCache()
	if rec := send(r, "POST", "/cosmos/api/login", "", nil); rec.Code != 200 || reached != 1 {
		t.Errorf("with the file: %d", rec.Code)
	}
	if ScriptAllowed() || Public(httptest.NewRequest("GET", "/", nil)) != nil {
		t.Errorf("the pages are still told to show the widget")
	}
}

func TestCloudflareDownFailsClosedAndTells(t *testing.T) {
	for _, mode := range []string{"error", "500", "redirect", "big", "slow", "closed"} {
		t.Run(mode, func(t *testing.T) {
			e := setup(t, nil)
			attemptTimeout, overallTimeout, retryNeeds = 300*time.Millisecond, 700*time.Millisecond, 100*time.Millisecond
			e.sv.Delay = 2 * time.Second
			e.sv.SetDown(mode)
			if mode == "closed" {
				e.server.Close()
			}
			e.sv.Issue("tok", panelName, "cosmos-login")
			reached := 0
			started := time.Now()
			rec := send(router(&reached), "POST", "/cosmos/api/login", "tok", nil)
			if rec.Code != 503 || code(rec) != CodeUnavailable || reached != 0 {
				t.Fatalf("status %d code %s reached %d", rec.Code, code(rec), reached)
			}
			if time.Since(started) > 1500*time.Millisecond {
				t.Errorf("took %s", time.Since(started))
			}
			if got := e.recorded(); len(got) != 1 || got[0].Kind != utils.SecTurnstileUnavailable || got[0].Details["reason"] != ResultUnreachable {
				t.Errorf("recorded %+v", got)
			}
			if p := Problems(); len(p) != 1 || p[0].Reason != ResultUnreachable {
				t.Errorf("problems %+v", p)
			}
			time.Sleep(50 * time.Millisecond)
			e.mu.Lock()
			notes := append([]string(nil), e.notes...)
			e.mu.Unlock()
			if len(notes) != 1 || !strings.Contains(notes[0], MessageUnreachable) {
				t.Errorf("notifications %v", notes)
			}
			if mode == "error" {
				// one retry, with the same idempotency key
				calls := e.sv.Calls(false)
				if len(calls) != 2 || calls[0].IdempotencyKey == "" || calls[0].IdempotencyKey != calls[1].IdempotencyKey {
					t.Errorf("calls %+v", calls)
				}
			}
		})
	}
}

func TestARefusedSecretIsAProblemNotTheVisitors(t *testing.T) {
	e := setup(t, nil)
	e.sv.RemoveSecret(secretKey)
	e.sv.Issue("tok", panelName, "cosmos-login")
	reached := 0
	rec := send(router(&reached), "POST", "/cosmos/api/login", "tok", nil)
	if rec.Code != 503 || reached != 0 {
		t.Fatalf("status %d", rec.Code)
	}
	if got := e.recorded(); len(got) != 1 || got[0].Details["reason"] != ResultKeyRefused {
		t.Errorf("recorded %+v", got)
	}
	if p := Problems(); len(p) != 1 || p[0].Reason != ResultKeyRefused {
		t.Errorf("problems %+v", p)
	}
	// the watch finds it too, and a key that works again clears it
	checkStored(context.Background())
	e.sv.AddSecret(secretKey)
	checkStored(context.Background())
	if p := Problems(); len(p) != 0 {
		t.Errorf("problems after the key works: %+v", p)
	}
}

func TestTheVerificationIsRateLimited(t *testing.T) {
	e := setup(t, nil)
	reached := 0
	r := router(&reached)
	refused := 0
	for i := 0; i < visitorBurst+3; i++ {
		rec := send(r, "POST", "/cosmos/api/login", "bad-"+strings.Repeat("x", i+1), nil)
		if rec.Code == 429 {
			refused++
			if code(rec) != CodeTooMany {
				t.Errorf("code %s", code(rec))
			}
		}
	}
	if refused != 3 || len(e.sv.Calls(false)) != visitorBurst {
		t.Errorf("%d refused, %d calls to Cloudflare", refused, len(e.sv.Calls(false)))
	}
	// another visitor is not concerned
	other := send(r, "POST", "/cosmos/api/login", "other-visitor", func(req *http.Request) { req.RemoteAddr = "198.51.100.9:1" })
	if other.Code == 429 {
		t.Errorf("another visitor was limited")
	}
	// an IPv6 visitor is counted by its /64
	if visitorKey("2001:db8:1:2:aaaa::1") != visitorKey("2001:db8:1:2:bbbb::9") || visitorKey("2001:db8:1:2::1") == visitorKey("2001:db8:1:3::1") {
		t.Errorf("IPv6 keys")
	}
}

func TestPublicIsForVisitorsItAppliesTo(t *testing.T) {
	setup(t, nil)
	req := httptest.NewRequest("GET", "/cosmos/api/can-send-email", nil)
	got := Public(req)
	if got == nil || got["siteKey"] != siteKey || got["appearance"] != "always" {
		t.Fatalf("public %v", got)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), secretKey) {
		t.Errorf("the secret is public: %s", raw)
	}
	req.Header.Set("X-Test-Local", "1")
	if Public(req) != nil {
		t.Errorf("a local visitor is told to show the widget")
	}
}

// --- the routes

func admin(req *http.Request, permissions ...utils.Permission) *http.Request {
	if len(permissions) == 0 {
		permissions = []utils.Permission{utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ, utils.PERM_CONFIGURATION}
	}
	return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{Nickname: "admin", Permissions: permissions, IsSudoed: true}))
}

func call(t *testing.T, body interface{}, host string, permissions ...utils.Permission) (int, map[string]interface{}) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := admin(httptest.NewRequest("POST", "/cosmos/api/turnstile", bytes.NewReader(raw)), permissions...)
	req.Host = host
	req.RemoteAddr = stranger + ":1"
	rec := httptest.NewRecorder()
	StatusRoute(rec, req)
	if strings.Contains(rec.Body.String(), secretKey) || strings.Contains(rec.Body.String(), "0x4AAAAAAAnewSECRETkey") {
		t.Errorf("an answer names a secret: %s", rec.Body.String())
	}
	var answer map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &answer)
	return rec.Code, answer
}

func reason(answer map[string]interface{}) string {
	text, _ := answer["reason"].(string)
	return text
}

func TestRoutesNeedThePermissionAndBoundTheBody(t *testing.T) {
	setup(t, nil)
	req := admin(httptest.NewRequest("GET", "/cosmos/api/turnstile", nil), utils.PERM_LOGIN)
	rec := httptest.NewRecorder()
	StatusRoute(rec, req)
	if rec.Code == 200 {
		t.Errorf("read without the permission: %d", rec.Code)
	}
	req = admin(httptest.NewRequest("GET", "/cosmos/api/turnstile", nil), utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ)
	rec = httptest.NewRecorder()
	StatusRoute(rec, req)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), secretKey) || !strings.Contains(rec.Body.String(), `"secretKey":"***"`) {
		t.Errorf("read: %d %s", rec.Code, rec.Body.String())
	}
	if status, _ := call(t, map[string]string{"action": "disable"}, panelName, utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ); status == 200 {
		t.Errorf("a change by a reader: %d", status)
	}
	if status, _ := call(t, map[string]interface{}{"action": "disable", "extra": 1}, panelName); status != 400 {
		t.Errorf("unknown field: %d", status)
	}
	if status, _ := call(t, map[string]string{"action": "keys", "siteKey": strings.Repeat("a", 5000)}, panelName); status != 413 {
		t.Errorf("large body: %d", status)
	}
	put := admin(httptest.NewRequest("PUT", "/cosmos/api/turnstile", nil))
	rec = httptest.NewRecorder()
	StatusRoute(rec, put)
	if rec.Code != 405 {
		t.Errorf("PUT: %d", rec.Code)
	}
}

func TestKeysAreTestedBeforeTheyAreKept(t *testing.T) {
	e := setup(t, func(c *utils.Config) { c.Turnstile = utils.ForkTurnstileConfig{} })
	if status, a := call(t, map[string]string{"action": "keys", "siteKey": siteKey, "secretKey": "0x4AAAAAAAnewSECRETkey"}, panelName); status != 400 || reason(a) != "secret_refused" {
		t.Errorf("an unknown secret: %d %v", status, a)
	}
	if utils.GetMainConfig().Turnstile.SecretKey != "" {
		t.Errorf("a refused secret was kept")
	}
	if status, a := call(t, map[string]string{"action": "keys", "siteKey": "1x00000000000000000000AA", "secretKey": cffake.TestSecretPass}, panelName); status != 400 || reason(a) != "test_keys" {
		t.Errorf("test keys outside test mode: %d %v", status, a)
	}
	if status, a := call(t, map[string]string{"action": "keys", "siteKey": "bad key", "secretKey": secretKey}, panelName); status != 400 || reason(a) != "site_key_format" {
		t.Errorf("a malformed site key: %d %v", status, a)
	}
	e.sv.SetDown("500")
	if status, a := call(t, map[string]string{"action": "keys", "siteKey": siteKey, "secretKey": secretKey}, panelName); status != 502 || reason(a) != "unreachable" {
		t.Errorf("Cloudflare down: %d %v", status, a)
	}
	e.sv.SetDown("")
	status, a := call(t, map[string]string{"action": "keys", "siteKey": siteKey, "secretKey": secretKey}, panelName)
	if status != 200 {
		t.Fatalf("good keys: %d %v", status, a)
	}
	stored := utils.GetMainConfig().Turnstile
	if stored.SecretKey != secretKey || stored.Enabled || stored.Source != "manual" {
		t.Errorf("stored %+v", stored)
	}
	// the mask keeps the stored secret
	if status, _ := call(t, map[string]string{"action": "keys", "siteKey": siteKey, "secretKey": utils.MaskedSecret}, panelName); status != 200 || utils.GetMainConfig().Turnstile.SecretKey != secretKey {
		t.Errorf("the mask did not keep the secret")
	}
	for _, event := range e.recorded() {
		raw, _ := json.Marshal(event)
		if strings.Contains(string(raw), secretKey) {
			t.Errorf("an event names the secret: %s", raw)
		}
	}
}

func TestSwitchingOnNeedsARoundTripWithTheseKeys(t *testing.T) {
	e := setup(t, func(c *utils.Config) {
		c.Turnstile = utils.ForkTurnstileConfig{SiteKey: siteKey, SecretKey: secretKey}
	})
	if status, a := call(t, map[string]string{"action": "enable"}, panelName); status != 409 || reason(a) != "not_tried" {
		t.Errorf("enable before a try: %d %v", status, a)
	}
	// the try must come from the panel's public name
	e.sv.Issue("try-local", "cosmos.local", "cosmos-test")
	if status, a := call(t, map[string]string{"action": "try", "token": "try-local"}, "cosmos.local:2053"); status != 409 || reason(a) != "not_public_name" {
		t.Errorf("try on another name: %d %v", status, a)
	}
	// a token of the sign-in form is not a trial
	e.sv.Issue("try-form", panelName, "cosmos-login")
	if status, a := call(t, map[string]string{"action": "try", "token": "try-form"}, panelName+":2053"); status != 400 || reason(a) != "token_action" {
		t.Errorf("try with a login token: %d %v", status, a)
	}
	e.sv.Issue("try-ok", panelName, "cosmos-test")
	if status, a := call(t, map[string]string{"action": "try", "token": "try-ok"}, panelName+":2053"); status != 200 || data(a)["confirmed"] != true {
		t.Fatalf("try: %d %v", status, a)
	}
	if status, a := call(t, map[string]string{"action": "enable"}, panelName); status != 200 || data(a)["active"] != true {
		t.Fatalf("enable: %d %v", status, a)
	}
	// new keys switch it off until they are tried
	e.sv.AddSecret("0x4AAAAAAAnewSECRETkey")
	if status, a := call(t, map[string]string{"action": "keys", "siteKey": "0x4AAAAAAAnewSITEkey", "secretKey": "0x4AAAAAAAnewSECRETkey"}, panelName); status != 200 || data(a)["active"] != false || data(a)["confirmed"] != false {
		t.Errorf("new keys: %d %v", status, a)
	}
	// a revoked key is caught when switching on
	e.sv.Issue("try-2", panelName, "cosmos-test")
	call(t, map[string]string{"action": "try", "token": "try-2"}, panelName)
	e.sv.RemoveSecret("0x4AAAAAAAnewSECRETkey")
	if status, a := call(t, map[string]string{"action": "enable"}, panelName); status == 200 {
		t.Errorf("enabled with a revoked key: %v", a)
	}
}

func data(answer map[string]interface{}) map[string]interface{} {
	d, _ := answer["data"].(map[string]interface{})
	return d
}

func TestSettingsAndRemove(t *testing.T) {
	setup(t, nil)
	status, a := call(t, map[string]interface{}{"action": "settings", "forms": map[string]bool{"login": true}, "appearance": "interaction-only", "mode": "invisible"}, panelName)
	if status != 200 {
		t.Fatalf("%d %v", status, a)
	}
	stored := utils.GetMainConfig().Turnstile
	if stored.Forms.Reset || !stored.Forms.Login || stored.Appearance != "interaction-only" || stored.Mode != "invisible" {
		t.Errorf("stored %+v", stored)
	}
	if status, _ := call(t, map[string]interface{}{"action": "settings", "appearance": "sometimes"}, panelName); status != 400 {
		t.Errorf("an unknown appearance: %d", status)
	}
	call(t, map[string]string{"action": "remove"}, panelName)
	if c := utils.GetMainConfig().Turnstile; c.SecretKey != "" || c.SiteKey != "" || c.Enabled || !c.Forms.Login {
		t.Errorf("after remove %+v", c)
	}
}

func TestCreateMakesAWidgetForThePanelNameOnly(t *testing.T) {
	setup(t, func(c *utils.Config) { c.Turnstile = utils.ForkTurnstileConfig{} })
	fake := cffake.New()
	fake.AddZone("example.org")
	fake.AddToken("dnstoken0123456789abcdefghijklmnopqrstuv", cffake.Token{Zones: []string{"example.org"}, ReadDNS: true, EditDNS: true})
	fake.AddToken("turnstiletoken0123456789abcdefghijklmnopq", cffake.Token{Zones: []string{"example.org"}, ReadDNS: true, EditDNS: true, EditTurnstile: true})
	server := httptest.NewServer(fake)
	defer server.Close()
	target, _ := url.Parse(server.URL)
	restore := cloudflare.ForkTestTransport(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		clone.URL.Scheme, clone.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(clone)
	})
	defer restore()

	token := "dnstoken0123456789abcdefghijklmnopqrstuv"
	previous := zoneOf
	zoneOf = func(name string) (string, string, bool) { return token, "example.org", name == panelName }
	defer func() { zoneOf = previous }()

	if status, a := call(t, map[string]string{"action": "create", "mode": "managed"}, panelName); status != 403 || reason(a) != "permission" {
		t.Errorf("a DNS token: %d %v", status, a)
	}
	token = "turnstiletoken0123456789abcdefghijklmnopq"
	status, a := call(t, map[string]string{"action": "create", "mode": "non-interactive"}, panelName)
	if status != 200 {
		t.Fatalf("create: %d %v", status, a)
	}
	widgets := fake.Widgets()
	if len(widgets) != 1 || len(widgets[0].Domains) != 1 || widgets[0].Domains[0] != panelName || widgets[0].Mode != "non-interactive" {
		t.Errorf("made %+v", widgets)
	}
	stored := utils.GetMainConfig().Turnstile
	if stored.SiteKey != widgets[0].SiteKey || stored.SecretKey != widgets[0].Secret || stored.Enabled || stored.Source != "api" {
		t.Errorf("stored %+v", stored)
	}
	if strings.Contains(a["data"].(map[string]interface{})["secretKey"].(string), "Secret") {
		t.Errorf("the new secret is in the answer")
	}
	for _, c := range fake.Calls(false) {
		if strings.Contains(c.Body, "example.org\"") && !strings.Contains(c.Body, panelName) {
			t.Errorf("the domain itself was sent: %s", c.Body)
		}
	}
}

func TestPanelPublicName(t *testing.T) {
	for host, want := range map[string]string{
		"cosmos.example.org": "cosmos.example.org", "Cosmos.Example.org:2053": "cosmos.example.org",
		"192.168.1.10": "", "cosmos.local": "", "localhost": "", "0.0.0.0": "", "[2001:db8::1]": "",
	} {
		setup(t, func(c *utils.Config) { c.HTTPConfig.Hostname = host })
		if got := PanelPublicName(); got != want {
			t.Errorf("%s: %q, want %q", host, got, want)
		}
	}
}
