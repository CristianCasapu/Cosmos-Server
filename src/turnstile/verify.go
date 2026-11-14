package turnstile

// The call to Cloudflare's siteverify. One fixed address, TLS verified with
// the system's authorities, no proxy from the environment, redirects
// refused, a time limit per attempt and overall, the answer read through a
// size limit. One retry with the same idempotency key after a connection
// failure or Cloudflare's "internal-error", as its documentation advises.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const siteverifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// the time limits (variables: the tests shorten them)
var (
	attemptTimeout = 8 * time.Second
	overallTimeout = 10 * time.Second
	retryNeeds     = time.Second
)

const (
	maxAnswer = 16 << 10
	// a token is valid for 300 seconds (Cloudflare); some skew between clocks
	tokenLifetime = 300 * time.Second
	clockSkew     = 30 * time.Second
)

// endpoint is where siteverify is asked. In test mode only, the environment
// may name a stand-in.
var endpoint = func() string {
	if testMode {
		if stand := strings.TrimSpace(os.Getenv("COSMOS_FORK_TURNSTILE_SITEVERIFY")); stand != "" {
			return stand
		}
	}
	return siteverifyURL
}()

// transport carries the calls; the tests put a stand-in here.
var transport http.RoundTripper = &http.Transport{
	Proxy:                 nil,
	DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
	TLSHandshakeTimeout:   5 * time.Second,
	ResponseHeaderTimeout: attemptTimeout,
	MaxIdleConns:          4,
	IdleConnTimeout:       60 * time.Second,
	ForceAttemptHTTP2:     true,
}

type transportFunc struct{}

func (transportFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport.RoundTrip(req)
}

var client = &http.Client{
	Timeout:   attemptTimeout,
	Transport: transportFunc{},
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// now is the clock (tests move it).
var now = time.Now

// What a verification ended in.
const (
	ResultOK          = "ok"
	ResultRefused     = "refused"     // the token is not good: the visitor's side
	ResultUnreachable = "unreachable" // Cloudflare could not be asked or failed
	ResultKeyRefused  = "key_refused" // Cloudflare refuses the secret key
)

// Reasons of a refused token (fixed words, for the record and the tests).
const (
	ReasonInvalid   = "invalid"
	ReasonUsed      = "used" // expired or used before
	ReasonHostname  = "hostname"
	ReasonAction    = "action"
	ReasonMalformed = "malformed"
	ReasonTooMany   = "too_many"
)

// Result is the outcome of one verification. Nothing in it comes from
// Cloudflare as text, except Hostname after it was compared.
type Result struct {
	Kind     string
	Reason   string
	Hostname string
}

type verdict struct {
	Success     bool     `json:"success"`
	ChallengeTS string   `json:"challenge_ts"`
	Hostname    string   `json:"hostname"`
	ErrorCodes  []string `json:"error-codes"`
	Action      string   `json:"action"`
}

// --- tokens verified before: refused at once, whatever Cloudflare would say
// (its reuse detection is eventually consistent)

const (
	spentKeep = tokenLifetime + time.Minute
	spentMax  = 10000
)

var (
	spentMu sync.Mutex
	spent   = map[[32]byte]time.Time{}
)

// spend marks a token used and reports whether it was used before.
func spend(token string) bool {
	key := sha256.Sum256([]byte(token))
	t := now()
	spentMu.Lock()
	defer spentMu.Unlock()
	if at, seen := spent[key]; seen && t.Sub(at) < spentKeep {
		return true
	}
	if len(spent) >= spentMax {
		for k, at := range spent {
			if t.Sub(at) >= spentKeep {
				delete(spent, k)
			}
		}
		if len(spent) >= spentMax {
			// still full: of a flood of verified tokens, forget the oldest half
			// would need a sort; forgetting all is safe because Cloudflare
			// refuses repeats too
			spent = map[[32]byte]time.Time{}
		}
	}
	spent[key] = t
	return false
}

func newIdempotencyKey() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32])
}

// ask makes one call. A nil answer means Cloudflare could not be asked.
func ask(ctx context.Context, form url.Values) *verdict {
	ctx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer+1))
	if err != nil || len(raw) > maxAnswer {
		return nil
	}
	var parsed verdict
	if json.Unmarshal(raw, &parsed) != nil {
		return nil
	}
	return &parsed
}

func has(codes []string, wanted ...string) bool {
	for _, code := range codes {
		for _, w := range wanted {
			if code == w {
				return true
			}
		}
	}
	return false
}

// siteverify asks Cloudflare about a token, with one retry.
func siteverify(ctx context.Context, secret string, token string, remoteIP string) *verdict {
	ctx, cancel := context.WithTimeout(ctx, overallTimeout)
	defer cancel()
	form := url.Values{"secret": {secret}, "response": {token}}
	if ip := net.ParseIP(remoteIP); ip != nil {
		form.Set("remoteip", ip.String())
	}
	if key := newIdempotencyKey(); key != "" {
		form.Set("idempotency_key", key)
	}
	result := ask(ctx, form)
	if result == nil || has(result.ErrorCodes, "internal-error") {
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) > retryNeeds {
			if again := ask(ctx, form); again != nil {
				result = again
			}
		}
	}
	return result
}

// Verify checks a token for a form: Cloudflare must say yes, for the
// hostname the form was sent to and the form's action, solved less than
// five minutes ago, and the token must not have been used here before.
func Verify(ctx context.Context, secret string, token string, remoteIP string, wantHost string, form string) Result {
	if !validToken(token) {
		return Result{Kind: ResultRefused, Reason: ReasonMalformed}
	}
	if spend(token) {
		return Result{Kind: ResultRefused, Reason: ReasonUsed}
	}
	result := siteverify(ctx, secret, token, remoteIP)
	if result == nil {
		return Result{Kind: ResultUnreachable}
	}
	switch {
	case has(result.ErrorCodes, "invalid-input-secret", "missing-input-secret"):
		return Result{Kind: ResultKeyRefused}
	case has(result.ErrorCodes, "internal-error", "bad-request"):
		return Result{Kind: ResultUnreachable}
	case has(result.ErrorCodes, "timeout-or-duplicate"):
		return Result{Kind: ResultRefused, Reason: ReasonUsed}
	case !result.Success || len(result.ErrorCodes) > 0:
		return Result{Kind: ResultRefused, Reason: ReasonInvalid}
	}

	// Cloudflare's test secrets answer example.com without an action
	// (documented); only in test mode are they accepted at all
	if testMode && isTestKey(secret) {
		return Result{Kind: ResultOK, Hostname: wantHost}
	}
	if strings.ToLower(strings.TrimSuffix(result.Hostname, ".")) != wantHost || wantHost == "" {
		return Result{Kind: ResultRefused, Reason: ReasonHostname}
	}
	if result.Action != actions[form] {
		return Result{Kind: ResultRefused, Reason: ReasonAction}
	}
	solved, err := time.Parse(time.RFC3339, result.ChallengeTS)
	if err != nil || now().Sub(solved) > tokenLifetime+clockSkew || solved.Sub(now()) > clockSkew {
		return Result{Kind: ResultRefused, Reason: ReasonUsed}
	}
	return Result{Kind: ResultOK, Hostname: wantHost}
}

// dummyToken is what Cloudflare's test site keys hand out; a real secret
// key refuses it with "invalid-input-response", a wrong one is refused
// first with "invalid-input-secret".
const dummyToken = "XXXX.DUMMY.TOKEN.XXXX"

// Secret checks a secret key without a browser. "ok" (the secret is known
// to Cloudflare), "test" (one of Cloudflare's test secrets), ResultKeyRefused
// or ResultUnreachable.
func Secret(ctx context.Context, secret string) string {
	result := siteverify(ctx, secret, dummyToken, "")
	switch {
	case result == nil:
		return ResultUnreachable
	case has(result.ErrorCodes, "invalid-input-secret", "missing-input-secret"):
		return ResultKeyRefused
	case has(result.ErrorCodes, "invalid-input-response"):
		return "ok"
	case result.Success && isTestKey(secret):
		return "test"
	case has(result.ErrorCodes, "timeout-or-duplicate") && isTestKey(secret):
		return "test"
	}
	return ResultUnreachable
}
