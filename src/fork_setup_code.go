package main

// Fork addition (plan 47, the owner's decision of 2026-11-17): a one-time
// setup code guards the first-run wizard.
//
// While the installation is new, Cosmos answers its setup routes without a
// session: whoever reached the port first became the administrator, and an
// administrator of Cosmos is root on the host. Now, at the first start of a
// new installation, the server makes a code, writes it to <config
// folder>/setup-code (0600, created exclusively, never through a link) and
// prints it once to the journal (the owner's choice); install.sh prints it in
// the terminal. During the new-install state every route that works without
// a session (newInstall, setup, setup-join, dns: see setupRoutes) must carry
// the code in the header X-Cosmos-Setup-Code. /cosmos/api/status, which the
// panel's first page reads, is left as it was. Without the file, or with a
// file that is not a plain 0600 file of this user, setup is refused: never
// open. The clean-install step of the wizard keeps the file (newInstall.go).
//
// The code: 20 symbols of a 30-symbol alphabet without look-alikes (no 0, 1,
// I, L, O, U), shown as five groups of four; 98 bits. That alone makes
// guessing hopeless: a million guesses a second for 10^16 years. So nothing
// is shared between addresses (a flood from many addresses cannot keep the
// owner out), the right code is always accepted, and a wrong one from an
// address that already made five earns a pause that doubles with each try,
// at most 15 minutes, which only keeps the logs readable. An address is the
// connection's own (X-Forwarded-For is never read here); IPv6 counts by /64,
// since one machine holds a whole /64.
//
// Where it answers: when install.sh decides the wizard must not be reached
// over the network in clear (a cloud server, the router's exposed host, a
// public address; see install.sh, --wizard-on-lan), it writes the marker
// <config folder>/setup-loopback before the first start. While the
// installation is new and the marker is there (a plain 0600 file of this
// user), the server listens on 127.0.0.1 only; the owner reaches the wizard
// through an SSH tunnel. Once set up, the marker goes and the server listens
// as configured.
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
	"syscall"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	setupCodeHeader   = "X-Cosmos-Setup-Code"
	setupCodeFile     = "setup-code"
	setupLoopbackFile = "setup-loopback"
	setupCodeAlphabet = "ABCDEFGHJKMNPQRSTVWXYZ23456789"
	setupCodeLength   = 20

	setupFreeTries     = 5
	setupMaxPause      = 15 * time.Minute
	setupMaxAddresses  = 4096
	setupForgetAddress = time.Hour
)

func setupCodePath() string     { return path.Join(utils.CONFIGFOLDER, setupCodeFile) }
func setupLoopbackPath() string { return path.Join(utils.CONFIGFOLDER, setupLoopbackFile) }

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

// privateFile tells whether file is a plain file (not a link), of this
// process's user, readable and writable by it only, with one name: the only
// kind of setup-code or setup-loopback file that is trusted.
func privateFile(file string) error {
	info, err := os.Lstat(file)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New(file + " is not a plain file (a link or something else)")
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("%s has the mode %o, not 600", file, info.Mode().Perm())
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		if int(st.Uid) != os.Geteuid() {
			return fmt.Errorf("%s belongs to another user (%d)", file, st.Uid)
		}
		if st.Nlink != 1 {
			return errors.New(file + " has more than one name (a hard link)")
		}
	}
	return nil
}

// The start: a new installation gets its code (once); one that is set up
// loses the file and the loopback marker.
var setupCodeOut io.Writer = os.Stderr

func forkSetupCodeAtStart() {
	newInstall := utils.GetMainConfig().NewInstall
	ensureSetupCode(newInstall, setupCodePath(), rand.Reader)
	if !newInstall {
		os.Remove(setupLoopbackPath())
	}
}

func ensureSetupCode(newInstall bool, file string, random io.Reader) {
	if !newInstall {
		if err := os.Remove(file); err == nil {
			utils.Log("Setup: the installation is set up, its setup code was removed")
		}
		return
	}
	if _, err := os.Lstat(file); err == nil {
		if err := privateFile(file); err == nil {
			if _, err := readSetupCode(file); err == nil {
				return // made at an earlier start: kept, not printed again
			}
		} else {
			utils.Warn("Setup: " + err.Error() + ": it is not trusted, a new code is made")
		}
		// not one this server made: never trusted. Removing a link removes the
		// link, not what it points to.
		if err := os.Remove(file); err != nil {
			utils.MajorError("Setup: cannot remove "+file+", which is not a setup code this server made; the first-run wizard stays closed", err)
			return
		}
	}
	code, err := newSetupCode(random)
	if err != nil {
		utils.MajorError("Setup: cannot make a setup code; the first-run wizard stays closed", err)
		return
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
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

// readSetupCode is the expected code, or an error: a missing file, or one
// that is not a private plain file, refuses setup.
func readSetupCode(file string) (string, error) {
	if err := privateFile(file); err != nil {
		return "", err
	}
	f, err := os.OpenFile(file, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
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

// setupListenAddr is the address a listener takes: while the installation is
// new and install.sh asked for it, 127.0.0.1 instead of every address.
// Hooked in fork_server_lifecycle.go.
func setupListenAddr(addr string) string {
	return setupListenAddrFor(addr, utils.GetMainConfig().NewInstall, setupLoopbackPath())
}

func setupListenAddrFor(addr string, newInstall bool, marker string) string {
	if !newInstall || privateFile(marker) != nil {
		return addr
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return net.JoinHostPort("127.0.0.1", port)
}

// The limiter: per address only.
type setupTries struct {
	fails int
	until time.Time
	last  time.Time
}

type setupLimiter struct {
	mu    sync.Mutex
	now   func() time.Time
	tries map[string]*setupTries
}

func newSetupLimiter(now func() time.Time) *setupLimiter {
	return &setupLimiter{now: now, tries: map[string]*setupTries{}}
}

// paused says whether address must wait before its next wrong guess is
// even counted, and for how long.
func (l *setupLimiter) paused(address string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if t := l.tries[address]; t != nil && now.Before(t.until) {
		return true, t.until.Sub(now)
	}
	return false, 0
}

// failed counts a wrong code from address.
func (l *setupLimiter) failed(address string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	t := l.tries[address]
	if t == nil {
		if len(l.tries) >= setupMaxAddresses {
			l.forgetOldest(now)
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

// succeeded forgets address's wrong tries.
func (l *setupLimiter) succeeded(address string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.tries, address)
}

// size is the number of addresses held (for the tests).
func (l *setupLimiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.tries)
}

// forgetOldest makes room: first the addresses no longer paused, else the
// one seen longest ago. The table never holds more than setupMaxAddresses.
func (l *setupLimiter) forgetOldest(now time.Time) {
	for a, t := range l.tries {
		if !now.Before(t.until) {
			delete(l.tries, a)
		}
	}
	for len(l.tries) >= setupMaxAddresses {
		oldest, when := "", now
		for a, t := range l.tries {
			if oldest == "" || t.last.Before(when) {
				oldest, when = a, t.last
			}
		}
		delete(l.tries, oldest)
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

// setupClient is the address a guess is counted against: the connection's
// own, never a header (no proxy is trusted before setup); IPv6 by /64.
func setupClient(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return (&net.IPNet{IP: ip.Mask(net.CIDRMask(64, 128)), Mask: net.CIDRMask(64, 128)}).String()
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
			utils.Error("Setup: the setup code file cannot be used, setup is refused", err)
			utils.HTTPError(w, "Setup is closed: the setup code file cannot be read on the server. Restart Cosmos to make a new one.", http.StatusServiceUnavailable, "SC004")
			return
		}
		client := setupClient(r)
		// the right code is always accepted, whatever came before
		if subtle.ConstantTimeCompare([]byte(normalizeSetupCode(given)), []byte(expected)) == 1 {
			g.limiter.succeeded(client)
			next.ServeHTTP(w, r)
			return
		}
		if paused, wait := g.limiter.paused(client); paused {
			seconds := int(wait/time.Second) + 1
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			utils.HTTPError(w, "Too many wrong setup codes from here: try again in "+strconv.Itoa(seconds)+" seconds.", http.StatusTooManyRequests, "SC003")
			return
		}
		g.limiter.failed(client)
		utils.Warn("Setup: a wrong setup code was given from " + client)
		utils.HTTPError(w, "This is not the setup code.", http.StatusForbidden, "SC002")
	})
}
