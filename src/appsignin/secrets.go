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
	"encoding/json"
	"regexp"
	"strings"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

const minSecretInText = 4 // a shorter secret is only looked for as a whole field

var (
	credentialsURL = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://([^/\s@:]*):([^/\s@]*)@`)
	identifier     = regexp.MustCompile(`^[^\s\x00-\x1f\x7f/:@'"\x60\\]{1,64}$`)
	emailShape     = regexp.MustCompile(`^[^\s@<>()",;:\\]{1,64}@[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)+$`)
	databaseShape  = regexp.MustCompile(`^[A-Za-z0-9_.$-]{1,64}$`)
)

// secretName: the masking rule of the start check (and PWD).
func secretName(name string) bool { return docker.IsSecretName(name) }

// secretsOf are the secret values of a container: those of its secret-named
// variables and the passwords of addresses with credentials in any variable.
func secretsOf(env map[string]string) []string {
	out := []string{}
	seen := map[string]bool{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
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
	}
	return out
}

// holdsSecret tells whether a text equals or carries a secret value.
func holdsSecret(text string, secrets []string) bool {
	if text == "" {
		return false
	}
	if credentialsURL.MatchString(text) && !strings.Contains(text, ":<password>@") {
		return true
	}
	for _, s := range secrets {
		if text == s || (len(s) >= minSecretInText && strings.Contains(text, s)) {
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

// clean is the last check of an exit: every string of the details that holds
// a secret is left out, and that is logged (never the value).
func clean(d *Details, secrets []string) {
	drop := func(s *string, what string) {
		if holdsSecret(*s, secrets) {
			utils.Warn("Sign-in details of " + d.Container + ": the " + what + " held a secret value and is left out")
			*s = ""
		}
	}
	drop(&d.Name, "name")
	drop(&d.ViaContainer, "database name")
	drop(&d.ManagerOf, "database name")
	drop(&d.Host, "host")
	if d.FirstRun != nil {
		drop(&d.FirstRun.User, "proposed username")
	}
	for i := range d.Accounts {
		a := &d.Accounts[i]
		drop(&a.User, "username")
		drop(&a.Email, "e-mail")
		drop(&a.Database, "database")
		drop(&a.Connection, "connection string")
	}
	kept := []Address{}
	for _, a := range d.Addresses {
		if !holdsSecret(a.URL, secrets) {
			kept = append(kept, a)
		}
	}
	d.Addresses = kept
	places := []DataPlace{}
	for _, p := range d.Data {
		if !holdsSecret(p.Target, secrets) && !holdsSecret(p.Folder, secrets) && !holdsSecret(p.Volume, secrets) {
			places = append(places, p)
		}
	}
	d.Data = places
}

// leaks is the final check of a text about to leave.
func leaks(text string, secrets []string) bool {
	for _, s := range secrets {
		if len(s) >= minSecretInText && strings.Contains(text, s) {
			return true
		}
	}
	return false
}

// jsonLeaks checks what will be sent as JSON, as it will be written.
func jsonLeaks(v interface{}, secrets []string) bool {
	var buf strings.Builder
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return true
	}
	return leaks(buf.String(), secrets)
}
