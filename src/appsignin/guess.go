package appsignin

// A guess, by the variables' names, of an image Cosmos knows nothing about:
// which variables are the app's own sign-in. The same rules are in the panel
// (client/src/utils/signIn.js `guessSignIn`); both are tested against
// testdata/guesses.json. A guess is said to be one wherever it is shown.
//
// A variable counts when its last word says user, e-mail or password, and
// none of its words names something else the app connects to (its database,
// a mail relay, a directory, a storage bucket, a proxy …): those credentials
// are the app's way into another service, not the way into the app. A
// password and a user (or e-mail) count together when they share the words
// before the last one (MOODLE_USERNAME, MOODLE_PASSWORD); a password alone
// only when those words say it is the administrator's (ADMIN_PASSWORD).

import (
	"sort"
	"strings"

	"github.com/azukaar/cosmos-server/src/anyimage"
)

var (
	guessUserWords     = map[string]bool{"USER": true, "USERNAME": true, "LOGIN": true}
	guessEmailWords    = map[string]bool{"EMAIL": true, "MAIL": true}
	guessPasswordWords = map[string]bool{"PASSWORD": true, "PASS": true, "PASSWD": true, "PWD": true}
	guessAdminWords    = map[string]bool{"ADMIN": true, "ROOT": true, "SUPERUSER": true, "INITIAL": true, "OWNER": true, "WEBUI": true, "UI": true, "WEB": true, "DEFAULT": true}
	guessForeignWords  = map[string]bool{
		"SMTP": true, "MAILER": true, "IMAP": true, "POP": true, "POP3": true, "LDAP": true, "OIDC": true, "OAUTH": true, "SSO": true, "SAML": true,
		"S3": true, "AWS": true, "B2": true, "MINIO": true, "PROXY": true, "FTP": true, "SFTP": true, "SSH": true, "VPN": true,
		"DB": true, "DATABASE": true, "POSTGRES": true, "POSTGRESQL": true, "PG": true, "MYSQL": true, "MARIADB": true, "MONGO": true, "MONGODB": true,
		"JDBC": true, "SQL": true, "REDIS": true, "VALKEY": true, "RABBITMQ": true, "AMQP": true, "KAFKA": true, "NATS": true, "ELASTIC": true,
		"REPL": true, "REPLICATION": true, "REGISTRY": true, "DOCKER": true, "GITHUB": true, "GITLAB": true, "ACME": true, "LETSENCRYPT": true,
		"CERT": true, "SSL": true, "TLS": true, "SENDER": true, "FROM": true, "NOTIFY": true, "NOTIFICATION": true, "ALERT": true, "CONTACT": true,
		"PLEX": true, "TRAKT": true, "API": true, "TOKEN": true, "KEY": true, "SECRET": true, "HASH": true, "FILE": true, "PUID": true, "PGID": true,
	}
)

// guessParts splits a variable's name into the words before the last and the last.
func guessParts(name string) (string, string, []string) {
	words := strings.Split(strings.ToUpper(name), "_")
	if len(words) == 0 {
		return "", "", nil
	}
	last := words[len(words)-1]
	return strings.Join(words[:len(words)-1], "_"), last, words[:len(words)-1]
}

func guessForeign(prefixWords []string) bool {
	for _, w := range prefixWords {
		if guessForeignWords[w] {
			return true
		}
	}
	return false
}

func guessAdmin(prefixWords []string) bool {
	for _, w := range prefixWords {
		if guessAdminWords[w] {
			return true
		}
	}
	return false
}

// GuessFromNames is the guess for a list of variable names: the account's
// user, e-mail and password variables, ok false when nothing counts.
func GuessFromNames(names []string) (user string, email string, password string, ok bool) {
	type group struct{ user, email, password string }
	groups := map[string]*group{}
	order := []string{}
	sorted := append([]string{}, names...)
	sort.Strings(sorted)
	for _, name := range sorted {
		if !envName.MatchString(name) || strings.HasSuffix(strings.ToUpper(name), "_FILE") {
			continue
		}
		prefix, last, words := guessParts(name)
		if guessForeign(words) {
			continue
		}
		g := groups[prefix]
		if g == nil {
			g = &group{}
			groups[prefix] = g
			order = append(order, prefix)
		}
		switch {
		case guessUserWords[last] && g.user == "":
			g.user = name
		case guessEmailWords[last] && g.email == "":
			g.email = name
		case guessPasswordWords[last] && g.password == "":
			g.password = name
		}
	}
	best := ""
	bestScore := 0
	for _, prefix := range order {
		g := groups[prefix]
		if g.password == "" {
			continue
		}
		_, _, words := guessParts(g.password)
		score := 0
		if g.user != "" || g.email != "" {
			score += 2
		}
		if guessAdmin(words) || prefix == "" {
			score++
		}
		if score == 0 {
			continue
		}
		if score > bestScore {
			best, bestScore = prefix, score
		}
	}
	if bestScore == 0 {
		return "", "", "", false
	}
	g := groups[best]
	return g.user, g.email, g.password, true
}

func guessAccount(env map[string]string) (anyimage.RecipeLoginAccount, bool) {
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	user, email, password, ok := GuessFromNames(names)
	if !ok {
		return anyimage.RecipeLoginAccount{}, false
	}
	return anyimage.RecipeLoginAccount{Role: "app", User: user, Email: email, Password: password}, true
}
