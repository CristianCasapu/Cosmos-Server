package main

// Fork addition (plan 47, the owner's decision of 2026-11-17): a one-time
// setup code guards the first-run wizard.
//
// While the installation is new, Cosmos answers its setup routes without a
// session: whoever reached the port first became the administrator, and an
// administrator of Cosmos is root on the host. Now, at the first start of a
// new installation, the server makes a code, writes it to <config
// folder>/setup-code (0600, created exclusively) and prints it once to the
// journal; install.sh prints it in the terminal. During the new-install
// state every route that works without a session (newInstall, setup,
// setup-join, dns: see setupRoutes) must carry the code in the header
// X-Cosmos-Setup-Code. /cosmos/api/status, which the panel's first page
// reads, is left as it was. Without the file, or with a file that cannot be
// read, setup is refused: never open.
//
// The code: 20 symbols of a 30-symbol alphabet without look-alikes (no 0, 1,
// I, L, O, U), shown as five groups of four; 98 bits. Guessing is bounded per
// address (five free tries, then a pause that doubles with each wrong try,
// at most 15 minutes) and overall (a bucket of 30 checks refilled at 2 a
// second, for every check right or wrong). At 2 guesses a second for a year
// an attacker has about one chance in 10^21. The bounds only slow down:
// nobody is locked out for good, and the code can always be read again on
// the server by root (sudo cat <config folder>/setup-code).
//
// The code is never written to cosmos.log, never in a URL (so never in the
// request log), never returned by a route; the file is removed when the
// installation is no longer new.

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	setupCodeHeader   = "X-Cosmos-Setup-Code"
	setupCodeFile     = "setup-code"
	setupCodeAlphabet = "ABCDEFGHJKMNPQRSTVWXYZ23456789"
	setupCodeLength   = 20

	setupFreeTries     = 5
	setupMaxPause      = 15 * time.Minute
	setupBucketSize    = 30
	setupBucketRefill  = 2 // checks a second
	setupMaxAddresses  = 4096
	setupForgetAddress = time.Hour
)

func setupCodePath() string {
	return path.Join(utils.CONFIGFOLDER, setupCodeFile)
}

// newSetupCode returns a fresh code in its display form (XXXX-XXXX-...).
func newSetupCode(random io.Reader) (string, error) {
	var b strings.Builder
	max := big.NewInt(int64(len(setupCodeAlphabet)))
	for i := 0; i < setupCodeLength; i++ {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		n, err := rand.Int(random, max)
		if err != nil {
			return "", err
		}
		b.WriteByte(setupCodeAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// normalizeSetupCode turns what someone typed or pasted into the bare code:
// upper case, without spaces, dashes or underscores. "" when it cannot be a
// code at all.
func normalizeSetupCode(s string) string {
	if len(s) > 200 {
		return ""
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch {
		case r == ' ' || r == '-' || r == '_' || r == '\t' || r == '\n' || r == '\r':
			continue
		case strings.ContainsRune(setupCodeAlphabet, r):
			b.WriteRune(r)
		default:
			return ""
		}
	}
	if b.Len() != setupCodeLength {
		return ""
	}
	return b.String()
}

// The start: a new installation gets its code (once); one that is set up
// loses the file.
var setupCodeOut io.Writer = os.Stderr

func forkSetupCodeAtStart() {
	ensureSetupCode(utils.GetMainConfig().NewInstall, setupCodePath(), rand.Reader)
}

func ensureSetupCode(newInstall bool, file string, random io.Reader) {
	if !newInstall {
		if err := os.Remove(file); err == nil {
			utils.Log("Setup: the installation is set up, its setup code was removed")
		}
		return
	}
	code, err := newSetupCode(random)
	if err != nil {
		utils.MajorError("Setup: cannot make a setup code; the first-run wizard stays closed", err)
		return
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return // made at an earlier start: kept, not printed again
	}
	if err != nil {
		utils.MajorError("Setup: cannot write the setup code to "+file+"; the first-run wizard stays closed", err)
		return
	}
	_, werr := f.WriteString(code + "\n")
	cerr := f.Close()
	if werr != nil || cerr != nil {
		os.Remove(file)
		utils.MajorError("Setup: cannot write the setup code to "+file+"; the first-run wizard stays closed", errors.Join(werr, cerr))
		return
	}
	// the journal only: not cosmos.log, not the request log
	fmt.Fprintf(setupCodeOut, "\n==================== COSMOS FIRST-RUN SETUP CODE ====================\n"+
		"  %s\n"+
		"  The first-run wizard asks for it. Also in %s (root only).\n"+
		"=====================================================================\n\n", code, file)
}

// readSetupCode is the expected code, or an error: a missing or unreadable
// file refuses setup.
func readSetupCode(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 256))
	if err != nil {
		return "", err
	}
	code := normalizeSetupCode(strings.TrimSpace(string(data)))
	if code == "" {
		return "", errors.New("the setup code file does not hold a setup code")
	}
	return code, nil
}

// The limiter.
type setupTries struct {
	fails int
	until time.Time
	last  time.Time
}

type setupLimiter struct {
	mu      sync.Mutex
	now     func() time.Time
	tries   map[string]*setupTries
	tokens  float64
	refills time.Time
}

func newSetupLimiter(now func() time.Time) *setupLimiter {
	return &setupLimiter{now: now, tries: map[string]*setupTries{}, tokens: setupBucketSize, refills: now()}
}

// allow says whether a check from address may be made now, and if not for
// how long to wait.
func (l *setupLimiter) allow(address string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.tokens += now.Sub(l.refills).Seconds() * setupBucketRefill
	if l.tokens > setupBucketSize {
		l.tokens = setupBucketSize
	}
	l.refills = now
	if t := l.tries[address]; t != nil && now.Before(t.until) {
		return false, t.until.Sub(now)
	}
	if l.tokens < 1 {
		return false, time.Duration((1-l.tokens)/setupBucketRefill*float64(time.Second)) + time.Millisecond
	}
	l.tokens--
	return true, 0
}

// failed counts a wrong code from address.
func (l *setupLimiter) failed(address string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.tries) >= setupMaxAddresses {
		for a, t := range l.tries {
			if now.Sub(t.last) > setupForgetAddress || now.After(t.until) {
				delete(l.tries, a)
			}
		}
	}
	t := l.tries[address]
	if t == nil {
		if len(l.tries) >= setupMaxAddresses {
			return // the overall bucket still bounds this address
		}
		t = &setupTries{}
		l.tries[address] = t
	}
	if now.Sub(t.last) > setupForgetAddress {
		t.fails = 0
	}
	t.fails++
	t.last = now
	if t.fails > setupFreeTries {
		pause := time.Second << uint(min(t.fails-setupFreeTries-1, 20))
		if pause > setupMaxPause {
			pause = setupMaxPause
		}
		t.until = now.Add(pause)
	}
}

var setupCodeLimiter = newSetupLimiter(time.Now)

// setupRoutes are the routes the new-install state lets through without a
// session (src/user/token.go, RefreshUserToken), but for /cosmos/api/status:
// every other route answers {"status":"NEW_INSTALL"} there and does nothing.
var setupRoutes = map[string]bool{
	"/cosmos/api/newinstall": true, // every step: -1 (wipe the config), 2, 3 (HTTPS), 4 (the administrator), 5
	"/cosmos/api/setup":      true, // everything in one call, an API token included
	"/cosmos/api/setup-join": true, // joins a Constellation
	"/cosmos/api/dns":        true, // a DNS lookup for the wizard
}

// setupPathGuarded: compared cleaned and in lower case, so that no spelling
// of a path the router would still take gets past.
func setupPathGuarded(p string) bool {
	return setupRoutes[strings.ToLower(path.Clean("/"+p))]
}

type setupGuard struct {
	newInstall func() bool
	file       func() string
	limiter    *setupLimiter
	removed    sync.Once
}

var setupCodeGuard = &setupGuard{
	newInstall: func() bool { return utils.GetMainConfig().NewInstall },
	file:       setupCodePath,
	limiter:    setupCodeLimiter,
}

// forkSetupCodeGuard is the middleware hooked into the router.
func forkSetupCodeGuard(next http.Handler) http.Handler {
	return setupCodeGuard.wrap(next)
}

func setupClient(r *http.Request) string {
	if ip, ok := r.Context().Value("ClientID").(string); ok && ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (g *setupGuard) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.newInstall() {
			// set up: the code has served; it goes with the first request
			g.removed.Do(func() { os.Remove(g.file()) })
			next.ServeHTTP(w, r)
			return
		}
		if !setupPathGuarded(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		given := r.Header.Get(setupCodeHeader)
		r.Header.Del(setupCodeHeader) // nothing after this sees it
		if strings.TrimSpace(given) == "" {
			utils.HTTPError(w, "The setup code is required. It was printed by install.sh and is in the setup-code file of Cosmos' data folder (root only).", http.StatusForbidden, "SC001")
			return
		}
		expected, err := readSetupCode(g.file())
		if err != nil {
			utils.Error("Setup: the setup code file cannot be read, setup is refused", err)
			utils.HTTPError(w, "Setup is closed: the setup code file cannot be read on the server. Restart Cosmos to make a new one.", http.StatusServiceUnavailable, "SC004")
			return
		}
		client := setupClient(r)
		if ok, wait := g.limiter.allow(client); !ok {
			seconds := int(wait/time.Second) + 1
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			utils.HTTPError(w, "Too many setup code attempts: try again in "+strconv.Itoa(seconds)+" seconds.", http.StatusTooManyRequests, "SC003")
			return
		}
		got := normalizeSetupCode(given)
		if subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			g.limiter.failed(client)
			utils.Warn("Setup: a wrong setup code was given from " + client)
			utils.HTTPError(w, "This is not the setup code.", http.StatusForbidden, "SC002")
			return
		}
		next.ServeHTTP(w, r)
	})
}
