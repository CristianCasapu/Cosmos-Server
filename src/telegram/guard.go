package telegram

// Fork addition (plan 48): the one gate every value of a message passes
// (composer.value and composer.pre call guardValue), and the last check of a
// finished message (leaks, in render). Telegram is a third party: no
// password, token, key, cookie, one-time code or sign-in detail may reach
// it, whatever an error text or an event quotes.

import (
	"regexp"
	"strings"
)

var (
	// key=value, key: value, "key":"value" with a key that names a secret
	secretPairPattern = regexp.MustCompile(`(?i)((?:pass(?:word|wd|phrase)?|pwd|secret|token|api[_-]?key|apikey|access[_-]?key|private[_-]?key|client[_-]?secret|auth(?:orization)?|cookie|session(?:id)?|otp|totp|mfa[_-]?code|credentials?)["']?\s*[=:]\s*["']?)([^\s"',;&]+)`)
	// scheme://user:password@host
	urlPasswordPattern = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^/\s:@]*:)[^/\s@]+@`)
	// Authorization: Bearer xxx / Basic xxx
	bearerPattern = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	// a JSON Web Token
	jwtPattern = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}`)
	// a private key, whole or cut
	privateKeyPattern = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(-----END [A-Z ]*PRIVATE KEY-----|$)`)
	// the reset and invitation links carry their secret in the query
	linkSecretPattern = regexp.MustCompile(`(?i)([?&](?:t|token|code|key|nonce|invite|reset)=)[^&\s]+`)
)

// guardValue cuts every secret out of a text from outside.
func guardValue(text string, secrets []string) string {
	for _, secret := range secrets {
		if worthHiding(secret) {
			text = strings.ReplaceAll(text, secret, hiddenSecret)
		}
	}
	text = privateKeyPattern.ReplaceAllString(text, hiddenSecret)
	text = tokenShapePattern.ReplaceAllString(text, hiddenToken)
	text = jwtPattern.ReplaceAllString(text, hiddenSecret)
	text = bearerPattern.ReplaceAllString(text, "$1 "+hiddenSecret)
	text = urlPasswordPattern.ReplaceAllString(text, "${1}"+hiddenSecret+"@")
	text = linkSecretPattern.ReplaceAllString(text, "${1}"+hiddenSecret)
	text = secretPairPattern.ReplaceAllString(text, "${1}"+hiddenSecret)
	return text
}

// leaks reports whether a finished text still holds a secret.
func leaks(text string, secrets []string) bool {
	for _, secret := range secrets {
		if worthHiding(secret) && strings.Contains(text, secret) {
			return true
		}
	}
	return tokenShapePattern.MatchString(text) || jwtPattern.MatchString(text) || privateKeyPattern.MatchString(text)
}

// secretsOf lists the secrets of a configuration a message must never
// hold: the bot token, the mail and database passwords, the server's own
// token and licence, and the ones RememberSecrets noted from the maps.
func secretsOf(config secretSource) []string {
	secrets := []string{config.botToken, config.mailPassword, config.postgresPassword, config.serverToken, config.licence}
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
	botToken, mailPassword, postgresPassword, serverToken, licence string
}
