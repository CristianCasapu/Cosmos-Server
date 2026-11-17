package appsignin

// What never leaves through a channel that promises "no secret" (review F1).
//
// Three layers:
//  1. The labels that name the sign-in are the fork's own: an image's are not
//     taken (docker/fork_image_labels.go, read through docker.ForkAdminLabel),
//     and a creation whose label is not the one the review writes is refused
//     (check.go).
//  2. When the sign-in is worked out, whatever its source: a variable whose
//     NAME says it is a secret is never a user, e-mail, database or host; a
//     value equal to a secret of the container, or holding one, or holding an
//     address with credentials, is dropped; values must look like what they
//     claim (an identifier, an e-mail address, a container name).
//  3. At each exit (the card's answer, the e-mail, the notification), a last
//     check that no secret value of the container is in what leaves; a field
//     that would carry one is left out and the fact logged, without the value.

import (
	"regexp"
	"sort"
	"strings"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

// What counts as a secret value for this check (second review R2): the value
// of a secret-named variable, or the password of an address with credentials,
// when it is at least minSecret characters long and not a word that only
// switches something (true, false, yes, no, on, off …). Values are compared
// without regard to case. Only the fields that come from the container's
// variables are compared with them (`clean`), never fixed words, field names,
// the container's name or its image.
const minSecret = 4

var switchWords = map[string]bool{"true": true, "false": true, "yes": true, "no": true, "on": true, "off": true,
	"enabled": true, "disabled": true, "enable": true, "disable": true, "none": true, "null": true}

var (
	credentialsURL = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://([^/\s@:]*):([^/\s@]*)@`)
	credentialsDSN = regexp.MustCompile(`(?i)(?:^|[\s;])([^:\s/@;]+):([^@\s/;]+)@(?:tcp|unix|udp)\(`)
	identifier     = regexp.MustCompile(`^[^\s\x00-\x1f\x7f/:@'"\x60\\]{1,64}$`)
	emailShape     = regexp.MustCompile(`^[^\s@<>()",;:\\]{1,64}@[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)+$`)
	databaseShape  = regexp.MustCompile(`^[A-Za-z0-9_.$-]{1,64}$`)
	// names the start check's masking rule misses (second review, minor)
	moreSecretNames = regexp.MustCompile(`(?i)((^|_)(PIN|PW|AUTHKEY|APIKEY|PRIVATEKEY|JWT|SALT|SEED|DSN|CONNECTION_STRING|COOKIE|COOKIES|AUTH|KEY_ID|PASSWORDS|HASH)(_|$))`)
)

// secretName: the masking rule of the start check (and PWD).
func secretName(name string) bool {
	return docker.IsSecretName(name) || moreSecretNames.MatchString(name)
}

// secretsOf are the secret values of a container: those of its secret-named
// variables and the passwords of addresses with credentials in any variable.
func secretsOf(env map[string]string) []string {
	out := []string{}
	seen := map[string]bool{}
	add := func(v string) {
		v = strings.ToLower(strings.TrimSpace(v))
		if len(v) >= minSecret && !switchWords[v] && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for name, value := range env {
		if secretName(name) {
			add(value)
		}
		for _, m := range credentialsURL.FindAllStringSubmatch(value, -1) {
			add(m[2])
		}
		for _, m := range credentialsDSN.FindAllStringSubmatch(value, -1) {
			add(m[2])
		}
	}
	return out
}

// holdsSecret tells whether a value from a variable equals or carries a secret value.
func holdsSecret(text string, secrets []string) bool {
	if text == "" {
		return false
	}
	if (credentialsURL.MatchString(text) && !strings.Contains(text, ":<password>@")) || credentialsDSN.MatchString(text) {
		return true
	}
	lower := strings.ToLower(text)
	for _, s := range secrets {
		if lower == s || strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// field is a value of a variable as a user, e-mail or database may show it:
// "" when the variable is a secret by its name, when the value is or holds a
// secret, or when it does not look like what it claims.
func field(env map[string]string, variable string, shape *regexp.Regexp, secrets []string) string {
	if variable == "" || secretName(variable) {
		return ""
	}
	value := shown(env[variable])
	if value == "" || !shape.MatchString(value) || holdsSecret(value, secrets) {
		return ""
	}
	return value
}

// clean is the last check, run on the details before every exit (the card's
// answer, the e-mail, the notification): every field that comes from the
// container's variables and holds a secret value is left out, and said in
// Withheld (the card tells it) and in the log (never the value).
func clean(d *Details, secrets []string) {
	withheld := map[string]bool{}
	drop := func(s *string, what string) {
		if holdsSecret(*s, secrets) {
			utils.Warn("Sign-in details of " + d.Container + ": the " + what + " held a secret value of the app and is left out")
			*s = ""
			withheld[what] = true
		}
	}
	drop(&d.ViaContainer, "database container")
	drop(&d.ManagerOf, "database container")
	if d.FirstRun != nil {
		drop(&d.FirstRun.User, "proposed username")
	}
	for i := range d.Accounts {
		a := &d.Accounts[i]
		drop(&a.User, "username")
		drop(&a.Email, "e-mail")
		drop(&a.Database, "database")
		// the connection string is built from these fields (already checked) and the container's name
	}
	for _, what := range d.Withheld {
		withheld[what] = true
	}
	d.Withheld = []string{}
	for what := range withheld {
		d.Withheld = append(d.Withheld, what)
	}
	sort.Strings(d.Withheld)
	if len(d.Withheld) == 0 {
		d.Withheld = nil
	}
}
