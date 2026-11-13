package maildetect

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	maxBody = 1024
	// detections the whole server runs in a minute, and at once
	perMinute = 12
)

// limiter lets one detection run at a time and a few per minute, for the
// whole process: the route is for one administrator filling in one form.
type limiter struct {
	mu      sync.Mutex
	running bool
	recent  []time.Time
}

func (l *limiter) start(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.recent[:0]
	for _, at := range l.recent {
		if now.Sub(at) < time.Minute {
			kept = append(kept, at)
		}
	}
	l.recent = kept
	if l.running || len(l.recent) >= perMinute {
		return false
	}
	l.running = true
	l.recent = append(l.recent, now)
	return true
}

func (l *limiter) done() {
	l.mu.Lock()
	l.running = false
	l.mu.Unlock()
}

var (
	routeLimiter  = &limiter{}
	routeDetector = NewDetector()
)

// Route godoc
// @Summary Propose SMTP settings for an email address
// @Description Takes an email address (or its domain) and returns SMTP servers to try, in order of confidence: a built-in table of providers, the domain's MX and SRV records, and the usual host names. Each is checked with one connection that stops after the greeting, EHLO and the TLS handshake; no credentials exist in this request and none are sent. Only ports 25, 465, 587 and 2525; never loopback, link-local or the server's own addresses; a few seconds and a handful of connections at most.
// @Tags config
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Failure 429 {object} utils.HTTPErrorResult
// @Router /api/config/email-detect [post]
func Route(w http.ResponseWriter, req *http.Request) {
	// the permission of the neighbouring "send a test email": an
	// administrator who confirmed the password
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}

	raw, err := io.ReadAll(io.LimitReader(req.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		utils.HTTPError(w, "Request too large", http.StatusBadRequest, "ED001")
		return
	}
	// The address and nothing else: a request that carries more (a password,
	// say) is a mistake of the caller and is refused, not ignored.
	var body struct {
		Address string `json:"address"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		utils.HTTPError(w, "Invalid request", http.StatusBadRequest, "ED002")
		return
	}
	domain, err := DomainOf(body.Address)
	if err != nil {
		utils.HTTPError(w, "Enter an email address such as name@example.com", http.StatusBadRequest, "ED003")
		return
	}

	if !routeLimiter.start(time.Now()) {
		utils.HTTPError(w, "Too many lookups at once: wait a moment and try again", http.StatusTooManyRequests, "ED004")
		return
	}
	defer routeLimiter.done()

	// the domain passed DomainOf: letters, digits, dots and hyphens only
	utils.Debug("EmailDetect: looking for the mail servers of " + domain)
	result := routeDetector.Detect(req.Context(), domain)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
		"data":   result,
	})
}
