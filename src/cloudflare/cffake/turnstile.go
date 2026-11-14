package cffake

// Fork addition: the Turnstile parts of the stand-in.
//
//   - Server answers POST /client/v4/accounts/{id}/challenges/widgets (making
//     a widget) like api.cloudflare.com, for a token with EditTurnstile.
//   - Siteverify answers POST /turnstile/v0/siteverify like
//     challenges.cloudflare.com, as Cloudflare's documentation describes it
//     (turnstile/get-started/server-side-validation and
//     turnstile/troubleshooting/testing): form or JSON body; secret checked
//     first; a token is good for 300 seconds and once; Cloudflare's test
//     secrets behave as documented (always pass with the dummy token and
//     hostname example.com, always fail, always "already spent").

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// AccountID is the account every zone of the stand-in belongs to.
const AccountID = "0123456789abcdef0123456789abcdef"

var widgetsPath = regexp.MustCompile(`^/client/v4/accounts/([0-9a-f]{32})/challenges/widgets$`)

// Widget is a Turnstile widget made through the API.
type Widget struct {
	Name    string   `json:"name"`
	Domains []string `json:"domains"`
	Mode    string   `json:"mode"`
	SiteKey string   `json:"sitekey"`
	Secret  string   `json:"secret"`
}

// Widgets lists the widgets made so far.
func (s *Server) Widgets() []Widget {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Widget(nil), s.widgets...)
}

// serveWidgets runs with the lock held.
func (s *Server) serveWidgets(w http.ResponseWriter, req *http.Request, account string, token Token, raw []byte) {
	if account != AccountID || !token.EditTurnstile {
		fail(w, http.StatusForbidden, 10000, "Authentication error")
		return
	}
	if req.Method != "POST" {
		fail(w, http.StatusMethodNotAllowed, 10405, "Method not allowed")
		return
	}
	var body struct {
		Name    string   `json:"name"`
		Domains []string `json:"domains"`
		Mode    string   `json:"mode"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		fail(w, http.StatusBadRequest, 10001, "Invalid request body")
		return
	}
	if body.Name == "" || len(body.Name) > 254 || len(body.Domains) == 0 || len(body.Domains) > 10 {
		fail(w, http.StatusBadRequest, 10002, "Invalid name or domains")
		return
	}
	switch body.Mode {
	case "managed", "non-interactive", "invisible":
	default:
		fail(w, http.StatusBadRequest, 10003, "Invalid mode")
		return
	}
	for _, domain := range body.Domains {
		if strings.ContainsAny(domain, ":/*") || !strings.Contains(domain, ".") {
			fail(w, http.StatusBadRequest, 10004, "Invalid domain")
			return
		}
	}
	n := len(s.widgets) + 1
	widget := Widget{
		Name: body.Name, Domains: body.Domains, Mode: body.Mode,
		SiteKey: fmt.Sprintf("0x4AAAAAAAfake%010d", n),
		Secret:  fmt.Sprintf("0x4AAAAAAAfakeSecret%015d", n),
	}
	s.widgets = append(s.widgets, widget)
	ok(w, map[string]interface{}{
		"sitekey": widget.SiteKey, "secret": widget.Secret, "name": widget.Name,
		"domains": widget.Domains, "mode": widget.Mode, "region": "world",
		"created_on": "2026-11-14T10:00:00Z", "modified_on": "2026-11-14T10:00:00Z",
	}, nil)
}

// Cloudflare's test keys (turnstile/troubleshooting/testing).
const (
	TestSecretPass  = "1x0000000000000000000000000000000AA"
	TestSecretFail  = "2x0000000000000000000000000000000AA"
	TestSecretSpent = "3x0000000000000000000000000000000AA"
	DummyToken      = "XXXX.DUMMY.TOKEN.XXXX"
)

// SiteverifyToken is a token a widget handed out.
type SiteverifyToken struct {
	Hostname string
	Action   string
	Made     time.Time
}

// SiteverifyCall is one request the stand-in got. The secret is not kept:
// only whether one was sent and whether it was a known one.
type SiteverifyCall struct {
	SecretSent     bool   `json:"secretSent"`
	SecretKnown    bool   `json:"secretKnown"`
	Response       string `json:"response"`
	RemoteIP       string `json:"remoteip"`
	IdempotencyKey string `json:"idempotencyKey"`
}

// Siteverify is the stand-in for challenges.cloudflare.com's siteverify.
type Siteverify struct {
	mu      sync.Mutex
	secrets map[string]bool
	tokens  map[string]SiteverifyToken
	spent   map[string]bool
	calls   []SiteverifyCall
	// Down: "" answers; "error" answers internal-error; "500" a gateway
	// page; "slow" waits Delay before answering; "redirect" a 302
	Down  string
	Delay time.Duration
	// Now is the clock (tests move it)
	Now func() time.Time
}

// NewSiteverify returns a stand-in that knows the given secrets.
func NewSiteverify(secrets ...string) *Siteverify {
	v := &Siteverify{secrets: map[string]bool{}, tokens: map[string]SiteverifyToken{}, spent: map[string]bool{}, Now: time.Now}
	for _, secret := range secrets {
		v.secrets[secret] = true
	}
	return v
}

// AddSecret makes a secret known (a widget made at Cloudflare).
func (v *Siteverify) AddSecret(secret string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.secrets[secret] = true
}

// RemoveSecret forgets a secret (the widget was deleted or its key rotated).
func (v *Siteverify) RemoveSecret(secret string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.secrets, secret)
}

// Issue makes a token as a widget on hostname with action would.
func (v *Siteverify) Issue(token string, hostname string, action string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.tokens[token] = SiteverifyToken{Hostname: hostname, Action: action, Made: v.Now()}
}

// SetDown changes how the stand-in fails ("" to answer again).
func (v *Siteverify) SetDown(mode string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.Down = mode
}

// Calls lists the requests received; clear empties the list.
func (v *Siteverify) Calls(clear bool) []SiteverifyCall {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := append([]SiteverifyCall(nil), v.calls...)
	if clear {
		v.calls = nil
	}
	return out
}

func (v *Siteverify) answer(w http.ResponseWriter, result map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (v *Siteverify) refuse(w http.ResponseWriter, codes ...string) {
	v.answer(w, map[string]interface{}{"success": false, "error-codes": codes, "messages": []string{}})
}

// ServeHTTP answers POST /turnstile/v0/siteverify.
func (v *Siteverify) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/turnstile/v0/siteverify" {
		http.NotFound(w, req)
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(req.Body, 1<<16))
	fields := map[string]string{}
	if strings.HasPrefix(req.Header.Get("Content-Type"), "application/json") {
		var body map[string]interface{}
		if json.Unmarshal(raw, &body) != nil {
			v.refuse(w, "bad-request")
			return
		}
		for key, value := range body {
			if text, isText := value.(string); isText {
				fields[key] = text
			}
		}
	} else {
		values, err := url.ParseQuery(string(raw))
		if err != nil {
			v.refuse(w, "bad-request")
			return
		}
		for key := range values {
			fields[key] = values.Get(key)
		}
	}

	v.mu.Lock()
	down, delay := v.Down, v.Delay
	secret, response := fields["secret"], fields["response"]
	known := v.secrets[secret] || secret == TestSecretPass || secret == TestSecretFail || secret == TestSecretSpent
	v.calls = append(v.calls, SiteverifyCall{SecretSent: secret != "", SecretKnown: known, Response: response, RemoteIP: fields["remoteip"], IdempotencyKey: fields["idempotency_key"]})
	v.mu.Unlock()

	switch down {
	case "error":
		v.refuse(w, "internal-error")
		return
	case "500":
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "<html>Bad Gateway</html>")
		return
	case "redirect":
		http.Redirect(w, req, "https://example.com/", http.StatusFound)
		return
	case "slow":
		select {
		case <-time.After(delay):
		case <-req.Context().Done():
			return
		}
	case "big":
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"hostname":"`+strings.Repeat("a", 1<<20)+`"}`)
		return
	}

	switch {
	case req.Method != "POST":
		v.refuse(w, "bad-request")
		return
	case secret == "":
		v.refuse(w, "missing-input-secret")
		return
	case !known:
		v.refuse(w, "invalid-input-secret")
		return
	case response == "":
		v.refuse(w, "missing-input-response")
		return
	case len(response) > 2048:
		v.refuse(w, "invalid-input-response")
		return
	}

	// Cloudflare's test secrets
	switch secret {
	case TestSecretFail:
		v.refuse(w, "invalid-input-response")
		return
	case TestSecretSpent:
		v.refuse(w, "timeout-or-duplicate")
		return
	case TestSecretPass:
		if response != DummyToken {
			v.refuse(w, "invalid-input-response")
			return
		}
		v.answer(w, map[string]interface{}{"success": true, "challenge_ts": v.Now().UTC().Format(time.RFC3339), "hostname": "example.com", "error-codes": []string{}})
		return
	}

	v.mu.Lock()
	issued, exists := v.tokens[response]
	spent := v.spent[response]
	if exists && !spent {
		v.spent[response] = true
	}
	now := v.Now()
	v.mu.Unlock()
	switch {
	case !exists:
		v.refuse(w, "invalid-input-response")
	case spent || now.Sub(issued.Made) > 300*time.Second:
		v.refuse(w, "timeout-or-duplicate")
	default:
		result := map[string]interface{}{"success": true, "challenge_ts": issued.Made.UTC().Format(time.RFC3339), "hostname": issued.Hostname, "error-codes": []string{}}
		if issued.Action != "" {
			result["action"] = issued.Action
		}
		v.answer(w, result)
	}
}
