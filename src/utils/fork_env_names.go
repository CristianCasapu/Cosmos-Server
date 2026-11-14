package utils

// Fork addition: the settings of a DNS provider never set a dangerous
// variable of the server's environment.
//
// The DNS providers of the certificate library read their settings from the
// process environment, so the settings of a domain are put there for the
// time of an order. The names come from the settings, which the
// configuration permission writes. A name such as LD_PRELOAD, PATH,
// HTTPS_PROXY or SSL_CERT_FILE would change how the server (running as root)
// and every program it starts behave. Such a name is never set, for anyone:
// it must look like a provider's variable (a capital letter, then letters,
// digits and underscores, 64 at most) and not be on the list below. A
// refused name is logged, without its value.

import (
	"os"
	"regexp"
	"strings"
)

var envNamePattern = regexp.MustCompile(`^[A-Z][A-Za-z0-9_]{0,63}$`)

// names that are refused as they are (compared in capitals)
var deniedEnvNames = map[string]bool{
	"PATH": true, "HOME": true, "SHELL": true, "IFS": true, "ENV": true, "CDPATH": true,
	"USER": true, "LOGNAME": true, "MAIL": true, "PWD": true, "OLDPWD": true,
	"TMPDIR": true, "TMP": true, "TEMP": true, "TZ": true, "TZDIR": true, "ZONEINFO": true,
	"HOSTNAME": true, "HOSTALIASES": true, "CONFIG_FILE": true, "ACME_STAGING": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true, "FTP_PROXY": true,
	"PS4": true, "PROMPT_COMMAND": true,
	"NOTIFY_SOCKET": true, "LISTEN_FDS": true, "LISTEN_PID": true, "LISTEN_FDNAMES": true,
	"INVOCATION_ID": true, "JOURNAL_STREAM": true, "CREDENTIALS_DIRECTORY": true,
}

// beginnings of names that are refused (compared in capitals)
var deniedEnvPrefixes = []string{
	"LD_", "DYLD_", "GCONV", "BASH_", "DOCKER_", "COSMOS_", "SSL_", "OPENSSL_",
	"NODE_", "PYTHON", "PERL", "RUBY", "GIT_", "MALLOC_", "LOC", "NLS", "RES_",
	"LANG", "LC_", "EXEC_", "CURL_", "REQUESTS_", "XDG_", "SSH_", "SUDO_",
	"KRB5", "GNUTLS", "NSS_", "GLIBC", "SYSTEMD_", "GODEBUG", "GOFLAGS",
}

// SafeEnvName reports whether a name from the settings may be set in the
// server's environment.
func SafeEnvName(name string) bool {
	if !envNamePattern.MatchString(name) {
		return false
	}
	upper := strings.ToUpper(name)
	if deniedEnvNames[upper] {
		return false
	}
	for _, prefix := range deniedEnvPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return false
		}
	}
	// Go's own settings (GOMAXPROCS, GOTRACEBACK, GOROOT...) have no
	// underscore; providers named GO... (GODADDY_API_KEY) have one
	if strings.HasPrefix(upper, "GO") && !strings.Contains(upper, "_") {
		return false
	}
	return true
}

// SetDNSChallengeEnv is the hook of the places that put a DNS provider's
// settings into the environment. It sets the variable and returns true, or
// logs the refused name and returns false.
func SetDNSChallengeEnv(name string, value string) bool {
	if !SafeEnvName(name) {
		LogRefusedEnvName(name)
		return false
	}
	os.Setenv(name, value)
	return true
}

// LogRefusedEnvName says in the log that a name was not set, without its value
func LogRefusedEnvName(name string) {
	Warn("Certificates: the DNS provider setting " + cleanEnvNameForLog(name) + " is not set: it is not a name a DNS provider uses, or it would change how the server runs")
}

// cleanEnvNameForLog keeps a refused name readable and short in the log
func cleanEnvNameForLog(name string) string {
	out := []rune{}
	for _, r := range name {
		if len(out) >= 64 {
			out = append(out, '…')
			break
		}
		if r < 0x20 || r == 0x7f || r > 0x7e {
			r = '?'
		}
		out = append(out, r)
	}
	return "\"" + string(out) + "\""
}
