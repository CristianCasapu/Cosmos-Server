package utils

// Fork addition (plan 45): the sign-in details proposed for new apps. A
// username and an e-mail address that every installation proposes for the
// app's own account, the switch of the e-mail sent when an app is created, and
// the length of generated passwords. Never a password: the passwords are
// generated in the browser at each installation and live only in the app's
// container settings. Read by src/appsignin and the panel's review.

import (
	"errors"
	"net/mail"
	"strings"
	"unicode"
)

// ForkAppSignInConfig is Config.AppSignIn.
type ForkAppSignInConfig struct {
	// User is the username proposed to apps; empty: the installing
	// administrator's own Cosmos nickname
	User string `json:"User,omitempty"`
	// Email is the address proposed to apps that sign in with one; empty:
	// the installing administrator's own e-mail address
	Email string `json:"Email,omitempty"`
	// MailOff: no e-mail when an app is installed (stored inverted: the
	// e-mail is sent by default)
	MailOff bool `json:"MailOff,omitempty"`
	// Length of generated passwords: 0 means AppSignInDefaultLength
	Length int `json:"Length,omitempty"`
}

const (
	AppSignInDefaultLength = 24
	AppSignInMinLength     = 16
	AppSignInMaxLength     = 64
	appSignInMaxUser       = 64
	appSignInMaxEmail      = 254
	// AppSignInMasked is what a reader without credentials-read gets
	AppSignInMasked = "***"
)

// PasswordLength is the length to generate, the default when unset.
func (c ForkAppSignInConfig) PasswordLength() int {
	if c.Length == 0 {
		return AppSignInDefaultLength
	}
	return c.Length
}

// ValidateAppSignIn checks the settings as they are about to be saved; the
// error is a sentence for the administrator.
func ValidateAppSignIn(c ForkAppSignInConfig) error {
	if len(c.User) > appSignInMaxUser {
		return errors.New("The username for new apps is longer than 64 characters.")
	}
	for _, r := range c.User {
		if !unicode.IsPrint(r) || r == ' ' {
			return errors.New("The username for new apps holds a character that is not printable.")
		}
	}
	if strings.TrimSpace(c.User) != c.User {
		return errors.New("The username for new apps starts or ends with a space.")
	}
	if c.Email != "" {
		if len(c.Email) > appSignInMaxEmail || strings.ContainsAny(c.Email, " \t\r\n<>,;\"") {
			return errors.New("The e-mail address for new apps is not an address.")
		}
		parsed, err := mail.ParseAddress(c.Email)
		if err != nil || parsed.Address != c.Email || !strings.Contains(c.Email[strings.LastIndex(c.Email, "@")+1:], ".") {
			return errors.New("The e-mail address for new apps is not an address.")
		}
	}
	if c.Length != 0 && (c.Length < AppSignInMinLength || c.Length > AppSignInMaxLength) {
		return errors.New("The length of generated passwords must be between 16 and 64.")
	}
	return nil
}
