package utils

// Fork addition: SMTP delivery with timeouts, implicit TLS on port 465 and a
// per-step report, shared by SendEmail and the "test email settings" API.

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

const (
	// A server that never answers (wrong port, or one expecting TLS from the
	// first byte) must fail well inside the 45s HTTP request timeout.
	emailConnectTimeout = 10 * time.Second
	emailSessionTimeout = 30 * time.Second
)

// EmailStep is one stage of an SMTP delivery attempt.
type EmailStep struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// usesImplicitTLS reports whether the connection must be encrypted from the
// first byte (SMTPS) instead of being upgraded with STARTTLS.
func usesImplicitTLS(cfg EmailConfig) bool {
	return cfg.UseTLS && strings.TrimSpace(cfg.Port) == "465"
}

// DeliverEmail sends msg using cfg and returns the stages it went through.
// On failure the last step is the one that failed and err is its cause.
func DeliverEmail(cfg EmailConfig, from string, to []string, msg []byte) ([]EmailStep, error) {
	steps := []EmailStep{}
	pass := func(name string, detail string) {
		steps = append(steps, EmailStep{Name: name, OK: true, Detail: detail})
	}
	fail := func(name string, err error) ([]EmailStep, error) {
		steps = append(steps, EmailStep{Name: name, OK: false, Detail: err.Error()})
		return steps, err
	}

	addr := net.JoinHostPort(cfg.Host, strings.TrimSpace(cfg.Port))
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.AllowInsecureTLS,
		ServerName:         cfg.Host,
	}
	implicitTLS := usesImplicitTLS(cfg)
	dialer := &net.Dialer{Timeout: emailConnectTimeout}

	var conn net.Conn
	var err error
	if implicitTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fail("connect", err)
	}

	// The greeting gets the short deadline: a plain connection to a TLS-only
	// port stays silent until one side gives up.
	conn.SetDeadline(time.Now().Add(emailConnectTimeout))
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		if !cfg.UseTLS && strings.TrimSpace(cfg.Port) == "465" {
			err = fmt.Errorf("%w (port 465 expects TLS from the start: enable TLS, or use port 587)", err)
		}
		return fail("connect", err)
	}
	defer c.Close()
	conn.SetDeadline(time.Now().Add(emailSessionTimeout))
	pass("connect", addr)

	if cfg.UseTLS {
		if !implicitTLS {
			if ok, _ := c.Extension("STARTTLS"); !ok {
				return fail("tls", errors.New("the server does not offer STARTTLS on this port"))
			}
			if err = c.StartTLS(tlsConfig); err != nil {
				return fail("tls", err)
			}
		}
		detail := "STARTTLS"
		if implicitTLS {
			detail = "implicit TLS"
		}
		if state, ok := c.TLSConnectionState(); ok {
			detail += ", " + tls.VersionName(state.Version)
		}
		if cfg.AllowInsecureTLS {
			detail += ", certificate not verified"
		}
		pass("tls", detail)
	}

	if cfg.Username != "" || cfg.Password != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err = c.Auth(auth); err != nil {
			return fail("auth", err)
		}
		pass("auth", cfg.Username)
	}

	if err = c.Mail(from); err != nil {
		return fail("sender", err)
	}
	pass("sender", from)

	for _, rcpt := range to {
		if err = c.Rcpt(rcpt); err != nil {
			return fail("recipient", fmt.Errorf("%s: %w", rcpt, err))
		}
	}
	pass("recipient", strings.Join(to, ", "))

	w, err := c.Data()
	if err != nil {
		return fail("send", err)
	}
	if _, err = w.Write(msg); err != nil {
		return fail("send", err)
	}
	if err = w.Close(); err != nil {
		return fail("send", err)
	}
	pass("send", "")

	// The message is accepted at this point; a failed QUIT changes nothing.
	c.Quit()
	return steps, nil
}

// BuildEmailMessage renders the standard Cosmos email around body.
func BuildEmailMessage(from string, recipients []string, subject string, body string) []byte {
	serverURL := GetServerURL("")
	return []byte(fmt.Sprintf(
		Template,
		from,
		strings.Join(recipients, ","),
		subject,
		serverURL+"logo",
		body,
		serverURL,
	))
}

// SendTestEmail sends a short test message with cfg, which does not have to be
// the saved configuration, so settings can be tried before they are saved.
func SendTestEmail(cfg EmailConfig, recipient string) ([]EmailStep, error) {
	body := "<p>This is a test email from Cosmos.</p>" +
		"<p>If you are reading it, the SMTP settings work.</p>"
	msg := BuildEmailMessage(cfg.From, []string{recipient}, "Cosmos test email", body)
	return DeliverEmail(cfg, cfg.From, []string{recipient}, msg)
}
