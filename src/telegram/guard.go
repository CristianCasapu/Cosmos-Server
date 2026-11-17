package telegram

// Fork addition (plan 48): the one gate every value of a message passes
// (composer.value and composer.pre call firstPass, which is guardValue),
// and the second line, leaks, which looks at the finished message's values
// again with the same shapes: a message that still holds one is sent
// without any value (render). Telegram is a third party: no password,
// token, key, cookie, one-time code or sign-in detail may reach it,
// whatever an error text or an event quotes. When in doubt, too much is cut.

import (
	"math"
	"regexp"
	"strings"
	"unicode"
)

const secretNames = `pass(?:word|wd|phrase)?|pwd|secret|token|api[_-]?key|apikey|access[_-]?key|private[_-]?key|client[_-]?secret|auth(?:orization)?|cookie|session(?:id)?|otp|totp|mfa[_-]?code|credentials?|signature|sig`

// a value: quoted whole (escaped quotes inside), else up to a space or what
// closes a structure, so that cutting twice gives what cutting once gave
const secretValue = `(\[(?:hidden|token)\]|"(?:[^"\\]|\\.)*"?|'[^']*'?|[^\s"',;&}\])]+)`

var (
	// key=value, key: value, "key":"value", key = "two words", with the name
	// anywhere in a longer one (db_password, secret_access_key, SECRET_KEY_BASE)
	secretPairPattern = regexp.MustCompile(`(?i)((?:^|[^A-Za-z0-9])(?:[A-Za-z0-9]+[_-])*(?:` + secretNames + `)(?:[_-][A-Za-z0-9]+)*["']?\s*[=:]\s*)` + secretValue)
	// --password value, --db-password value, -password value (a flag: one or two dashes)
	secretFlagPattern = regexp.MustCompile(`(?i)((?:^|\s)--?(?:[a-z0-9]+-)*(?:password|passwd|pass|pwd|token|secret|api-?key)\s+)` + `("[^"]*"?|'[^']*'?|[^\s=:\[-]\S*)`)
	// commands that take a password after them: sshpass -p X, passwd X
	commandPasswordPattern = regexp.MustCompile(`(?i)(\b(?:sshpass\s+-p\s*|passwd\s+))(\S+)`)
	// -e NAME_PASSWORD value, -e NAME_PASSWORD=value
	envFlagPattern = regexp.MustCompile(`((?:^|\s)(?:-e|--env)\s+[A-Za-z0-9_]*(?i:pass|secret|token|key)[A-Za-z0-9_]*[\s=]+)(\S+)`)
	// FOO_SECRET_KEY_BASE=…, ENCRYPTION_KEY: …, DB_PASS=…
	envSecretPattern = regexp.MustCompile(`\b([A-Z0-9_]*(?:SECRET|KEY|TOKEN|PASS|PASSWORD|PASSWD|PWD|CREDENTIALS?|SALT)[A-Z0-9_]*\s*[=:]\s*)` + secretValue)
	// mysql -pSECRET, mysqldump -u root -pSECRET
	mysqlPasswordPattern = regexp.MustCompile(`(?i)(\b(?:mysql\w*|mariadb\w*)\b[^\n]*?\s-p)(\S+)`)
	// curl -u user:password, --user user:password
	curlUserPattern = regexp.MustCompile(`(?i)((?:^|\s)(?:-u|--user)[=\s]+[^\s:]*:)(\S+)`)
	// scheme://user:password@host: the password runs to the last "@" of the address
	urlPasswordPattern = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^\s:/@]*:)\S*@([^\s@/?#]+)`)
	// scheme://TOKEN@host: a long user is a token
	urlTokenPattern = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[A-Za-z0-9_.~-]{20,}@`)
	// the Go MySQL address user:password@tcp(host)
	dsnPattern = regexp.MustCompile(`([A-Za-z0-9_.-]+:)[^\s@]+@((?:tcp|unix)\()`)
	// .htpasswd lines: user:$apr1$…, user:$2y$…, user:{SHA}…
	htpasswdPattern = regexp.MustCompile(`([A-Za-z0-9_.-]+:)(\$(?:apr1|2[aby]|1|5|6)\$\S+|\{SHA\}\S+)`)
	// Authorization: Bearer xxx / Basic xxx
	bearerPattern = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	// a JSON Web Token
	jwtPattern = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}`)
	// a private key, whole or cut
	privateKeyPattern = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(-----END [A-Z ]*PRIVATE KEY-----|$)`)
	// query or fragment parameters that carry a secret
	linkSecretPattern = regexp.MustCompile(`(?i)([?&#](?:t|token|code|key|nonce|invite|reset|secret|signature|sig|password|apikey|api_key|access_token|id_token|auth)=)[^&\s#]+`)
	// tokens of well-known services
	serviceTokenPattern = regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9_-]{16,}|xox[abprs]-[A-Za-z0-9-]{10,}|(?:AKIA|ASIA)[A-Z0-9]{16}|glpat-[A-Za-z0-9_-]{20,})`)
	// webhook addresses: whoever has one can post
	webhookPattern = regexp.MustCompile(`(?i)https?://[^\s/]*(?:hooks\.slack\.com/services|discord(?:app)?\.com/api/webhooks|/hooks?/|/webhooks?/)\S*`)
	// a word of 32 or more letters, digits and + _ = - (no "/" nor "." : a path
	// or a name is not one word): judged in error texts only
	wordPattern = regexp.MustCompile(`[A-Za-z0-9+_=-]{32,}`)
	// a key of 40 characters with "/" and "+" (an AWS secret key), judged
	// in error texts only
	keyPattern = regexp.MustCompile(`(?:^|[\s"'=:])([A-Za-z0-9+][A-Za-z0-9+/]{35,63})(?:$|[\s"',;])`)
	// in error texts: an address's password up to its last "@" on the line
	urlPasswordLinePattern = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^\s:/@]*:)[^\n]*@([A-Za-z0-9.-]+)`)
	secretEnvWord          = regexp.MustCompile(`(?:^|_)(?:SECRET|KEY|TOKEN|PASS|PASSWORD|PASSWD|PWD|CREDENTIALS?|SALT)(?:_|$)`)
	placeSuffix            = regexp.MustCompile(`_(?:URL|URI|FILE|PATH|HOST|NAME|USER|USERNAME|ID|TYPE|LENGTH|SIZE|DIR|ENDPOINT|EXPIRY|EXPIRES|TTL)$`)
	plainWord              = regexp.MustCompile(`^[a-z]{1,12}[.!]?$`)
)

// maxGuarded is how much of a text the guard looks at: the values are cut
// far shorter afterwards.
const maxGuarded = 16 << 10

// firstPass is the guard of the values. A variable so that a test can make
// it miss something and see the second line catch it.
var firstPass = guardValue

// guardValue cuts every secret out of a value: a name, an address, a
// sentence's value. A "key: value" is cut there only when the value looks
// like a secret (not "invalid token: expired"). Applying it twice gives
// what applying it once gave.
func guardValue(text string, secrets []string) string {
	return guard(text, secrets, false)
}

// guardRaw cuts every secret out of an error text: every "key: value",
// the shapes of guardValue, an address's password up to its last "@", and
// words that look made by a machine.
func guardRaw(text string, secrets []string) string {
	text = guard(text, secrets, true)
	text = urlPasswordLinePattern.ReplaceAllString(text, "${1}"+hiddenSecret+"@${2}")
	text = wordPattern.ReplaceAllStringFunc(text, func(word string) string {
		if generated(word) {
			return hiddenSecret
		}
		return word
	})
	// only the key itself is replaced, not what stands around it
	matches := keyPattern.FindAllStringSubmatchIndex(text, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		start, end := matches[i][2], matches[i][3]
		if keyLike(text[start:end]) {
			text = text[:start] + hiddenSecret + text[end:]
		}
	}
	return text
}

func guard(text string, secrets []string, raw bool) string {
	if len(text) > maxGuarded {
		text = text[:maxGuarded]
	}
	for _, secret := range secrets {
		if worthHiding(secret) {
			text = strings.ReplaceAll(text, secret, hiddenSecret)
		}
	}
	text = privateKeyPattern.ReplaceAllString(text, hiddenSecret)
	text = tokenShapePattern.ReplaceAllString(text, hiddenToken)
	text = jwtPattern.ReplaceAllString(text, hiddenSecret)
	text = serviceTokenPattern.ReplaceAllString(text, hiddenSecret)
	text = webhookPattern.ReplaceAllString(text, hiddenSecret)
	text = bearerPattern.ReplaceAllString(text, "$1 "+hiddenSecret)
	text = urlPasswordPattern.ReplaceAllString(text, "${1}"+hiddenSecret+"@${2}")
	text = urlTokenPattern.ReplaceAllString(text, "${1}"+hiddenSecret+"@")
	text = dsnPattern.ReplaceAllString(text, "${1}"+hiddenSecret+"@${2}")
	text = htpasswdPattern.ReplaceAllString(text, "${1}"+hiddenSecret)
	text = linkSecretPattern.ReplaceAllString(text, "${1}"+hiddenSecret)
	text = mysqlPasswordPattern.ReplaceAllString(text, "${1}"+hiddenSecret)
	text = curlUserPattern.ReplaceAllString(text, "${1}"+hiddenSecret)
	text = commandPasswordPattern.ReplaceAllString(text, "${1}"+hiddenSecret)
	text = envFlagPattern.ReplaceAllString(text, "${1}"+hiddenSecret)
	cut := func(pattern *regexp.Regexp, nameCheck bool) {
		text = pattern.ReplaceAllStringFunc(text, func(m string) string {
			parts := pattern.FindStringSubmatch(m)
			name, value := parts[1], parts[2]
			if nameCheck && !secretEnvName(name) {
				return m
			}
			if placeSuffix.MatchString(strings.ToUpper(strings.TrimRight(strings.TrimSpace(name), "=: \"'"))) {
				return m // the name of a place (TOKEN_URL, password_file), not of a secret
			}
			if !raw && !secretish(value) {
				return m
			}
			return name + hiddenSecret
		})
	}
	cut(envSecretPattern, true)
	cut(secretFlagPattern, false)
	cut(secretPairPattern, false)
	return text
}

// keyLike: a key with "/" and "+" (an AWS secret key): upper and lower
// case and digits, many different characters; a path starts with "/" and
// is not one.
func keyLike(word string) bool {
	var upper, lower, digit bool
	counts := map[rune]int{}
	for _, r := range word {
		counts[r]++
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		}
	}
	if !(upper && lower && digit) {
		return false
	}
	n := float64(len(word))
	entropy := 0.0
	for _, c := range counts {
		p := float64(c) / n
		entropy -= p * math.Log2(p)
	}
	return entropy >= 4.2
}

// secretEnvName: the secret word is a whole part of the name (DB_PASSWORD,
// SECRET_KEY_BASE, not KEYBOARD), and the name is not that of a place
// (TOKEN_URL, PASSWORD_FILE).
func secretEnvName(name string) bool {
	name = strings.TrimRight(strings.TrimSpace(name), "=: ")
	if !secretEnvWord.MatchString(name) {
		return false
	}
	return !placeSuffix.MatchString(name)
}

// secretish: a value that can be a secret, not a word that says how
// something is ("expired", "none", "started") nor an address.
func secretish(value string) bool {
	if value == hiddenSecret || value == hiddenToken {
		return true
	}
	if strings.HasPrefix(value, `"`) || strings.HasPrefix(value, "'") {
		return true
	}
	if strings.Contains(value, "://") {
		return false
	}
	return !plainWord.MatchString(value)
}

// hideKnown cuts only the configuration's own secrets out of a text.
func hideKnown(text string, secrets []string) string {
	for _, secret := range secrets {
		if worthHiding(secret) {
			text = strings.ReplaceAll(text, secret, hiddenSecret)
		}
	}
	return text
}

// generated reports whether a long word looks made by a machine: upper
// case, lower case and digits all in it, and many different characters
// (at least 4 bits of entropy per character). Hexadecimal identifiers (one
// case), words of a language and names stay.
func generated(word string) bool {
	var upper, lower, digit bool
	counts := map[rune]int{}
	for _, r := range word {
		counts[r]++
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		}
	}
	if !(upper && lower && digit) {
		return false
	}
	// every class well represented: a name has few digits, an identifier
	// of a disk few lower-case letters
	var nu, nl, nd int
	for _, r := range word {
		switch {
		case unicode.IsUpper(r):
			nu++
		case unicode.IsLower(r):
			nl++
		case unicode.IsDigit(r):
			nd++
		}
	}
	if min(nu, nl, nd)*100 < 15*len(word) {
		return false
	}
	n := float64(len(word))
	entropy := 0.0
	for _, c := range counts {
		p := float64(c) / n
		entropy -= p * math.Log2(p)
	}
	return entropy >= 4
}

// leaks reports whether a finished message still holds a secret: a secret
// of the configuration anywhere, or a value (the parts covered by entities)
// in which the guard would still find something to cut.
func leaks(text string, entities []entity, secrets []string) bool {
	for _, secret := range secrets {
		if worthHiding(secret) && strings.Contains(text, secret) {
			return true
		}
	}
	if tokenShapePattern.MatchString(text) || jwtPattern.MatchString(text) || privateKeyPattern.MatchString(text) {
		return true
	}
	for _, e := range entities {
		part := entityText(text, e)
		// a value cut at its length may end in the middle of what the
		// guard put in: that last word is not judged
		if strings.HasSuffix(part, cutMark) {
			if i := strings.LastIndexAny(part, " :="); i >= 0 {
				part = part[:i]
			} else {
				continue
			}
		}
		judge := guardValue
		if e.Type == "pre" {
			judge = guardRaw
		}
		if judge(part, nil) != part {
			return true
		}
	}
	return false
}

// secretsOf lists the secrets of a configuration a message must never
// hold: the bot token, the mail and database passwords, the server's own
// token and licence, the Turnstile secret, and the ones RememberSecrets
// noted from the maps.
func secretsOf(config secretSource) []string {
	secrets := []string{config.botToken, config.mailPassword, config.postgresPassword, config.serverToken, config.licence, config.turnstile}
	if remembered := knownSecrets.Load(); remembered != nil {
		secrets = append(secrets, *remembered...)
	}
	kept := secrets[:0]
	for _, secret := range secrets {
		if worthHiding(secret) {
			kept = append(kept, secret)
		}
	}
	return kept
}

type secretSource struct {
	botToken, mailPassword, postgresPassword, serverToken, licence, turnstile string
}
