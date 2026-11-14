package utils

// Fork addition: signing in to a mail server that does not offer AUTH PLAIN,
// and a sender written as "Name <address>".
//
// Go's net/smtp only brings PLAIN and CRAM-MD5. Microsoft 365 offers LOGIN
// (and OAuth, which Cosmos cannot do): without LOGIN such a mailbox can
// never be used, whatever the settings say.

import (
	"errors"
	"net/mail"
	"net/smtp"
	"strings"
)

var (
	errNoPasswordSignIn = errors.New("the server offers no sign-in with a password here (only methods such as OAuth, which Cosmos cannot use)")
	errNoStartTLS       = errors.New("the server does not offer STARTTLS on this port")
)

// loginAuth is the AUTH LOGIN mechanism: user name and password, each
// answering a prompt of the server.
type loginAuth struct {
	username, password, host string
}

func isLocalMailHost(name string) bool {
	return name == "localhost" || name == "127.0.0.1" || name == "::1"
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	// The rule of Go's PLAIN: a password goes over an encrypted connection,
	// or to this machine, and to the server it is meant for.
	if !server.TLS && !isLocalMailHost(server.Name) {
		return "", nil, errors.New("unencrypted connection")
	}
	if server.Name != a.host {
		return "", nil, errors.New("wrong host name")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSuffix(strings.TrimSpace(string(fromServer)), ":")) {
	case "username", "user name":
		return []byte(a.username), nil
	case "password":
		return []byte(a.password), nil
	}
	return nil, errors.New("unexpected question from the server during sign-in")
}

// chooseEmailAuth picks the sign-in method from what the server offers:
// PLAIN, else LOGIN. A server that names only other methods gets none. One
// that names nothing (typically before encryption) is tried with PLAIN, as
// before, and answers for itself.
func chooseEmailAuth(c *smtp.Client, cfg EmailConfig) (smtp.Auth, error) {
	offered, methods := c.Extension("AUTH")
	has := func(name string) bool {
		for _, method := range strings.Fields(strings.ToUpper(methods)) {
			if method == name {
				return true
			}
		}
		return false
	}
	switch {
	case !offered || strings.TrimSpace(methods) == "" || has("PLAIN"):
		return smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host), nil
	case has("LOGIN"):
		return &loginAuth{username: cfg.Username, password: cfg.Password, host: cfg.Host}, nil
	}
	return nil, errNoPasswordSignIn
}

// oneLine removes line breaks: nothing from the settings may start a new
// header or a new command.
func oneLine(text string) string {
	return strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(text))
}

// EmailSender splits the configured sender, "address" or "Name <address>",
// into the address for the envelope and the text of the From header (the
// name encoded as mail headers need it). A sender that is neither is used
// as it is, as upstream did.
func EmailSender(from string) (address string, header string) {
	from = oneLine(from)
	parsed, err := mail.ParseAddress(from)
	if err != nil {
		return from, from
	}
	if parsed.Name == "" {
		return parsed.Address, parsed.Address
	}
	return parsed.Address, parsed.String()
}
