// Package turnstile is a fork addition: Cloudflare Turnstile on the panel's
// public forms (sign-in, forgot password, invitation and reset links).
//
// What it does, in the order a request meets it:
//
//   - Middleware sits on the sign-in router. For a POST of one of the three
//     forms from a visitor outside the local network, while the check is on
//     for that form, it verifies the token the page sent in the header
//     X-Cosmos-Turnstile with Cloudflare's siteverify (verify.go) before the
//     handler reads anything. No valid token, no handler.
//   - Public gives the sign-in pages what they need to show the widget (the
//     site key, which forms ask), through the anonymous read they already make.
//   - The routes of api.go are the settings card: keys pasted or made through
//     Cloudflare's API, a round trip from the administrator's browser before
//     the check can be switched on, the switches.
//   - watch.go tests the secret key every six hours and tells the
//     administrators when Cloudflare refuses it or cannot be reached.
//
// Never a lock-out: the local network is never asked (the fork's own rule,
// IsLocal); a file turnstile.off in the configuration folder switches the
// check off from the server's console, within seconds, without a restart.
// When Cloudflare cannot be asked the check fails closed for visitors from
// the internet (failing open would let anyone who can make the check fail
// skip it); they are told that the home network works.
//
// The secret key leaves the server only in the body of the request to
// siteverify. It is in no log, event, notification, error or answer.
package turnstile

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// HeaderName carries the widget's token from the page to the server.
const HeaderName = "X-Cosmos-Turnstile"

// The forms that can ask, and the action name each widget is rendered with
// (Cloudflare: up to 32 letters, digits, "_" and "-"). The action comes
// back from siteverify: a token solved for one form is refused by another.
const (
	FormLogin    = "login"
	FormReset    = "reset"
	FormRegister = "register"
	FormTest     = "test" // the settings card's round trip
)

var actions = map[string]string{
	FormLogin:    "cosmos-login",
	FormReset:    "cosmos-reset",
	FormRegister: "cosmos-register",
	FormTest:     "cosmos-test",
}

// routeForms maps the route templates of the sign-in router to their form.
var routeForms = map[string]string{
	"/cosmos/api/login":          FormLogin,
	"/cosmos/api/password-reset": FormReset,
	"/cosmos/api/register":       FormRegister,
}

// ConsoleFile is the name of the file that switches the check off.
const ConsoleFile = "turnstile.off"

// IsLocal says whether a request comes from the local network, by the rule
// the fork uses for .local names and home names. The server sets it
// (src/fork_turnstile.go); until then everyone is asked.
var IsLocal = func(*http.Request) bool { return false }

// testMode allows Cloudflare's test keys and a stand-in siteverify. Only
// from the environment of the process: never from the panel.
var testMode = os.Getenv("COSMOS_FORK_TURNSTILE_TEST") == "1"

// TestMode says whether the process runs with test keys allowed.
func TestMode() bool { return testMode }

var (
	keyPattern     = regexp.MustCompile(`^[0-9A-Za-z_-]{10,100}$`)
	testKeyPattern = regexp.MustCompile(`^[123]x0{20,40}(AA|AB|BB|FF)$`)
)

// maxToken is the longest token Cloudflare hands out.
const maxToken = 2048

// validToken checks the shape of a token: 1 to 2048 printable ASCII
// characters, no space.
func validToken(token string) bool {
	if token == "" || len(token) > maxToken {
		return false
	}
	for i := 0; i < len(token); i++ {
		if token[i] < 0x21 || token[i] > 0x7e {
			return false
		}
	}
	return true
}

// validKey checks the shape of a site key or a secret key.
func validKey(key string) bool { return keyPattern.MatchString(key) }

// isTestKey reports whether a key is one of Cloudflare's test keys, which
// accept a token anyone can make.
func isTestKey(key string) bool { return testKeyPattern.MatchString(key) }

// keysHash ties a round trip to the keys it was made with.
func keysHash(siteKey string, secret string) string {
	sum := sha256.Sum256([]byte("cosmos-turnstile\x00" + siteKey + "\x00" + secret))
	return hex.EncodeToString(sum[:16])
}

// --- the console switch

var (
	consoleMu      sync.Mutex
	consoleChecked time.Time
	consoleState   bool
)

// consoleCheckEvery bounds how often the file is looked for.
const consoleCheckEvery = 5 * time.Second

// ConsolePath is where the file that switches the check off is looked for.
func ConsolePath() string { return filepath.Join(utils.CONFIGFOLDER, ConsoleFile) }

// ConsoleOff reports whether the check was switched off from the console.
func ConsoleOff() bool {
	consoleMu.Lock()
	defer consoleMu.Unlock()
	now := time.Now()
	if !consoleChecked.IsZero() && now.Sub(consoleChecked) < consoleCheckEvery {
		return consoleState
	}
	_, err := os.Stat(ConsolePath())
	consoleState = err == nil
	consoleChecked = now
	return consoleState
}

// resetConsoleCache makes the next ConsoleOff look again (tests, and after
// a change of the settings).
func resetConsoleCache() {
	consoleMu.Lock()
	consoleChecked = time.Time{}
	consoleMu.Unlock()
}

// --- what is in force

// settings is the stored block with the keys trimmed.
func settings() utils.ForkTurnstileConfig {
	config := utils.GetMainConfig().Turnstile
	config.SiteKey = strings.TrimSpace(config.SiteKey)
	config.SecretKey = strings.TrimSpace(config.SecretKey)
	return config
}

// active reports whether the check is in force at all.
func active(config utils.ForkTurnstileConfig) bool {
	return config.Enabled && config.SiteKey != "" && config.SecretKey != "" && !ConsoleOff()
}

// ScriptAllowed reports whether the panel's pages may load Turnstile's
// script and frame: keys are saved (the check is on, or being tried) and the
// console switch is not set. The script policy follows it (fork_turnstile_csp.go).
func ScriptAllowed() bool {
	config := settings()
	return config.SiteKey != "" && !ConsoleOff()
}

// formOn says whether a form asks.
func formOn(config utils.ForkTurnstileConfig, form string) bool {
	forms := config.FormsInForce()
	switch form {
	case FormLogin:
		return forms.Login
	case FormReset:
		return forms.Reset
	case FormRegister:
		return forms.Register
	}
	return false
}

// hostOf is a request's host as siteverify names it: lower case, without
// port and trailing dot.
func hostOf(hostHeader string) string {
	host := strings.ToLower(strings.TrimSpace(utils.HostWithoutPort(hostHeader)))
	host = strings.Trim(host, "[]")
	return strings.TrimSuffix(host, ".")
}

// PanelPublicName is the panel's hostname when it is a name a widget can be
// made for: not an address, not localhost, not a .local name. "" otherwise.
func PanelPublicName() string {
	host := hostOf(utils.GetMainConfig().HTTPConfig.Hostname)
	if host == "" || net.ParseIP(host) != nil || !strings.Contains(host, ".") || host == "localhost" ||
		strings.HasSuffix(host, ".local") || !utils.IsValidHostnameSyntax(host) {
		return ""
	}
	return host
}
