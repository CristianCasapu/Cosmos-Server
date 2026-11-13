package utils

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"io"
	"math/big"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// mailScript is a mail server whose answers the test chooses. It writes
// down every line it receives.
type mailScript struct {
	auth     string // the methods named after AUTH in EHLO, "" for none
	startTLS bool   // offer STARTTLS, with a self-signed certificate
	user     string // the account LOGIN and PLAIN accept
	password string
	authSays string // answer to AUTH instead of checking the account
	mailSays string // answer to MAIL FROM
	rcptSays string // answer to RCPT TO

	mu   sync.Mutex
	got  []string
	body string
}

func (s *mailScript) heard() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.got, "\n")
}

func (s *mailScript) message() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.body
}

func selfSigned(t *testing.T, name string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:   time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func (s *mailScript) start(t *testing.T) EmailConfig {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var config *tls.Config
	if s.startTLS {
		cert := selfSigned(t, "mail.example.test")
		config = &tls.Config{Certificates: []tls.Certificate{cert}}
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				conn.Write([]byte("220 mail.example.test ESMTP\r\n"))
				s.session(conn, config)
			}()
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	return EmailConfig{Host: host, Port: port, From: "cosmos@example.test"}
}

func (s *mailScript) session(conn net.Conn, upgrade *tls.Config) {
	reader := bufio.NewReader(conn)
	say := func(line string) { conn.Write([]byte(line + "\r\n")) }
	read := func() (string, bool) {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", false
		}
		line = strings.TrimRight(line, "\r\n")
		s.mu.Lock()
		s.got = append(s.got, line)
		s.mu.Unlock()
		return line, true
	}
	decode := func(text string) string {
		raw, _ := base64.StdEncoding.DecodeString(text)
		return string(raw)
	}
	accept := func(user, password string) {
		if user == s.user && password == s.password {
			say("235 2.7.0 accepted")
		} else {
			say("535 5.7.8 Username and Password not accepted")
		}
	}
	for {
		line, ok := read()
		if !ok {
			return
		}
		command := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(command, "EHLO"):
			say("250-mail.example.test")
			if upgrade != nil {
				say("250-STARTTLS")
			} else if s.auth != "" {
				say("250-AUTH " + s.auth)
			}
			say("250 SIZE 1000000")
		case command == "STARTTLS" && upgrade != nil:
			say("220 go ahead")
			secure := tls.Server(conn, upgrade)
			if secure.Handshake() != nil {
				return
			}
			s.session(secure, nil)
			return
		case strings.HasPrefix(command, "AUTH") && s.authSays != "":
			say(s.authSays)
		case command == "AUTH LOGIN":
			say("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
			user, _ := read()
			say("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
			password, _ := read()
			accept(decode(user), decode(password))
		case strings.HasPrefix(command, "AUTH PLAIN "):
			parts := strings.Split(decode(line[len("AUTH PLAIN "):]), "\x00")
			if len(parts) == 3 {
				accept(parts[1], parts[2])
			} else {
				say("501 malformed")
			}
		case strings.HasPrefix(command, "MAIL FROM"):
			if s.mailSays != "" {
				say(s.mailSays)
			} else {
				say("250 ok")
			}
		case strings.HasPrefix(command, "RCPT TO"):
			if s.rcptSays != "" {
				say(s.rcptSays)
			} else {
				say("250 ok")
			}
		case command == "DATA":
			say("354 go ahead")
			var body strings.Builder
			for {
				l, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				body.WriteString(l)
			}
			s.mu.Lock()
			s.body = body.String()
			s.mu.Unlock()
			say("250 queued")
		case command == "QUIT":
			say("221 bye")
			return
		default:
			say("502 not implemented")
		}
	}
}

func lastStep(t *testing.T, steps []EmailStep) EmailStep {
	t.Helper()
	if len(steps) == 0 {
		t.Fatal("no steps")
	}
	return steps[len(steps)-1]
}

func TestSignsInWithLOGINWhenPLAINIsNotOffered(t *testing.T) {
	// what Microsoft 365 offers: LOGIN and OAuth, after STARTTLS
	server := &mailScript{auth: "LOGIN XOAUTH2", startTLS: true, user: "me@example.test", password: "correct horse"}
	cfg := server.start(t)
	cfg.UseTLS, cfg.AllowInsecureTLS = true, true
	cfg.Username, cfg.Password = "me@example.test", "correct horse"

	steps, err := SendTestEmail(cfg, "admin@example.test")
	if err != nil {
		t.Fatalf("delivery failed: %v (%+v)", err, steps)
	}
	heard := server.heard()
	if !strings.Contains(heard, "AUTH LOGIN") || strings.Contains(heard, "AUTH PLAIN") {
		t.Errorf("the server heard:\n%s", heard)
	}
	if got := stepNames(steps); got != "connect,tls,auth,sender,recipient,send" {
		t.Errorf("steps = %s", got)
	}

	cfg.Password = "wrong"
	steps, err = SendTestEmail(cfg, "admin@example.test")
	if last := lastStep(t, steps); err == nil || last.Name != "auth" || last.Code != EmailFailAuthRejected {
		t.Errorf("wrong password: %v %+v", err, last)
	}
}

func TestPLAINIsStillPreferred(t *testing.T) {
	server := &mailScript{auth: "LOGIN PLAIN", user: "me", password: "pw"}
	cfg := server.start(t)
	cfg.Username, cfg.Password = "me", "pw"
	if steps, err := SendTestEmail(cfg, "admin@example.test"); err != nil {
		t.Fatalf("delivery failed: %v (%+v)", err, steps)
	}
	if heard := server.heard(); !strings.Contains(heard, "AUTH PLAIN") || strings.Contains(heard, "AUTH LOGIN") {
		t.Errorf("the server heard:\n%s", heard)
	}
}

func TestNoPasswordIsSentToAServerThatOnlyOffersOAuth(t *testing.T) {
	server := &mailScript{auth: "XOAUTH2 OAUTHBEARER"}
	cfg := server.start(t)
	cfg.Username, cfg.Password = "me@example.test", "the-secret-password"

	steps, err := SendTestEmail(cfg, "admin@example.test")
	last := lastStep(t, steps)
	if err == nil || last.Name != "auth" || last.Code != EmailFailAuthNoMethod {
		t.Fatalf("got %v %+v", err, last)
	}
	secret := base64.StdEncoding.EncodeToString([]byte("the-secret-password"))
	if heard := server.heard(); strings.Contains(heard, "AUTH") || strings.Contains(heard, secret) {
		t.Errorf("the server heard:\n%s", heard)
	}
}

func TestLOGINOnlyOverAnEncryptedConnection(t *testing.T) {
	auth := &loginAuth{username: "me", password: "pw", host: "mail.example.org"}
	if _, _, err := auth.Start(&smtp.ServerInfo{Name: "mail.example.org", TLS: false}); err == nil {
		t.Error("LOGIN was started on an unencrypted connection to another machine")
	}
	if _, _, err := auth.Start(&smtp.ServerInfo{Name: "other.example.org", TLS: true}); err == nil {
		t.Error("LOGIN was started for another server than the one configured")
	}
	if method, _, err := auth.Start(&smtp.ServerInfo{Name: "mail.example.org", TLS: true}); err != nil || method != "LOGIN" {
		t.Errorf("encrypted: %q %v", method, err)
	}
	local := &loginAuth{host: "127.0.0.1"}
	if _, _, err := local.Start(&smtp.ServerInfo{Name: "127.0.0.1", TLS: false}); err != nil {
		t.Errorf("this machine: %v", err)
	}
	if answer, err := auth.Next([]byte("Username:"), true); err != nil || string(answer) != "me" {
		t.Errorf("user name prompt: %q %v", answer, err)
	}
	if answer, err := auth.Next([]byte("password:"), true); err != nil || string(answer) != "pw" {
		t.Errorf("password prompt: %q %v", answer, err)
	}
	if _, err := auth.Next([]byte("Send me your secrets:"), true); err == nil {
		t.Error("an unknown prompt was answered")
	}
}

func TestFailuresAreNamed(t *testing.T) {
	account := func(s *mailScript) *mailScript { s.auth, s.user, s.password = "PLAIN", "me", "pw"; return s }
	cases := []struct {
		name     string
		server   *mailScript
		signIn   bool
		useTLS   bool
		wantStep string
		wantCode string
	}{
		{"wrong password", account(&mailScript{}), true, false, "auth", EmailFailAuthRejected},
		{"app password wanted", &mailScript{auth: "PLAIN", authSays: "534 5.7.9 Application-specific password required"}, true, false, "auth", EmailFailAuthAppPassword},
		{"sign-in switched off", &mailScript{auth: "LOGIN", authSays: "535 5.7.139 Authentication unsuccessful, SmtpClientAuthentication is disabled for the Tenant."}, true, false, "auth", EmailFailAuthDisabled},
		{"busy", &mailScript{auth: "PLAIN", authSays: "454 4.7.0 Temporary authentication failure"}, true, false, "auth", EmailFailTryLater},
		{"encryption first", &mailScript{auth: "PLAIN", authSays: "530 5.7.0 Must issue a STARTTLS command first"}, true, false, "auth", EmailFailAuthNeedsTLS},
		{"no sign-in given", &mailScript{mailSays: "530 5.7.0 Authentication Required"}, false, false, "sender", EmailFailNeedsSignIn},
		{"sender not yours", &mailScript{auth: "PLAIN", authSays: "235 ok", mailSays: "553 5.7.1 Sender address rejected: not owned by user"}, true, false, "sender", EmailFailSenderRejected},
		{"starttls first", &mailScript{mailSays: "530 5.7.0 Must issue a STARTTLS command first"}, false, false, "sender", EmailFailNeedsTLS},
		{"relay denied", &mailScript{rcptSays: "554 5.7.1 Relay access denied"}, false, false, "recipient", EmailFailNeedsSignIn},
		{"no such recipient", &mailScript{rcptSays: "550 5.1.1 no such user"}, false, false, "recipient", EmailFailRecipientRefused},
		{"no STARTTLS", &mailScript{}, false, true, "tls", EmailFailNoStartTLS},
		{"certificate not trusted", &mailScript{startTLS: true}, false, true, "tls", EmailFailCertUntrusted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.server.start(t)
			cfg.UseTLS = tc.useTLS
			if tc.signIn {
				cfg.Username, cfg.Password = "me", "wrong"
			}
			steps, err := SendTestEmail(cfg, "admin@example.test")
			last := lastStep(t, steps)
			if err == nil || last.OK || last.Name != tc.wantStep || last.Code != tc.wantCode {
				t.Errorf("got %v, step %+v; want %s / %s", err, last, tc.wantStep, tc.wantCode)
			}
			for _, step := range steps[:len(steps)-1] {
				if step.Code != "" {
					t.Errorf("a step that passed carries a code: %+v", step)
				}
			}
		})
	}

	// nothing listens: the port of a listener that was closed
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()
	steps, err := SendTestEmail(EmailConfig{Host: host, Port: port, From: "cosmos@example.test"}, "admin@example.test")
	if last := lastStep(t, steps); err == nil || last.Name != "connect" || last.Code != EmailFailRefused {
		t.Errorf("closed port: %v %+v", err, last)
	}

	// the reasons that need a network to happen for real
	timeout := &net.OpError{Op: "dial", Err: timeoutError{}}
	for _, tc := range []struct {
		step string
		err  error
		cfg  EmailConfig
		want string
	}{
		{"connect", &net.DNSError{Err: "no such host", IsNotFound: true}, EmailConfig{}, EmailFailHostNotFound},
		{"connect", timeout, EmailConfig{Port: "587"}, EmailFailTimeout},
		{"connect", timeout, EmailConfig{Port: "465"}, EmailFailTLSExpected},
		{"connect", io.EOF, EmailConfig{Port: "465"}, EmailFailTLSExpected},
		{"connect", timeout, EmailConfig{Port: "465", UseTLS: true}, EmailFailTimeout},
		{"connect", tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}, EmailConfig{Port: "465", UseTLS: true}, EmailFailNotTLS},
		{"connect", &net.OpError{Op: "dial", Err: syscall.ENETUNREACH}, EmailConfig{}, EmailFailUnreachable},
		{"connect", x509.HostnameError{Certificate: &x509.Certificate{}, Host: "mail.example.test"}, EmailConfig{Port: "465", UseTLS: true}, EmailFailCertName},
		{"connect", x509.CertificateInvalidError{Reason: x509.Expired}, EmailConfig{Port: "465", UseTLS: true}, EmailFailCertExpired},
		{"connect", x509.UnknownAuthorityError{}, EmailConfig{Port: "465", UseTLS: true}, EmailFailCertUntrusted},
		{"connect", errors.New("something else"), EmailConfig{}, EmailFailConnect},
		{"tls", &tls.CertificateVerificationError{Err: x509.HostnameError{Certificate: &x509.Certificate{}, Host: "mail.example.test"}}, EmailConfig{}, EmailFailCertName},
		{"tls", errors.New("handshake failure"), EmailConfig{}, EmailFailTLS},
		{"auth", errors.New("unencrypted connection"), EmailConfig{}, EmailFailAuthNeedsTLS},
		{"auth", &textproto.Error{Code: 504, Msg: "5.7.4 Unrecognized authentication type"}, EmailConfig{}, EmailFailAuthNoMethod},
		{"auth", &textproto.Error{Code: 535, Msg: "5.7.8 Username and Password not accepted"}, EmailConfig{}, EmailFailAuthRejected},
		{"send", &textproto.Error{Code: 552, Msg: "message too large"}, EmailConfig{}, EmailFailSend},
		{"send", &textproto.Error{Code: 451, Msg: "try again"}, EmailConfig{}, EmailFailTryLater},
	} {
		if got := emailFailureCode(tc.step, tc.err, tc.cfg); got != tc.want {
			t.Errorf("%s / %v: %q, want %q", tc.step, tc.err, got, tc.want)
		}
	}
	if emailFailureCode("connect", nil, EmailConfig{}) != "" {
		t.Error("no error, no code")
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestSenderWithAName(t *testing.T) {
	for from, want := range map[string][2]string{
		"cosmos@example.test":                  {"cosmos@example.test", "cosmos@example.test"},
		"  <cosmos@example.test> ":             {"cosmos@example.test", "cosmos@example.test"},
		"Cosmos at home <cosmos@example.test>": {"cosmos@example.test", `"Cosmos at home" <cosmos@example.test>`},
		"Căsuța mea <cosmos@example.test>":     {"cosmos@example.test", "=?utf-8?q?C=C4=83su=C8=9Ba_mea?= <cosmos@example.test>"},
		"Evil\r\nBcc: v@example.test <a@b.c>":  {"Evil  Bcc: v@example.test <a@b.c>", "Evil  Bcc: v@example.test <a@b.c>"},
		"Evil\nBcc <a@b.c>":                    {"a@b.c", `"Evil Bcc" <a@b.c>`},
		"not an address":                       {"not an address", "not an address"},
	} {
		address, header := EmailSender(from)
		if address != want[0] || header != want[1] {
			t.Errorf("EmailSender(%q) = %q, %q; want %q, %q", from, address, header, want[0], want[1])
		}
		if strings.ContainsAny(address+header, "\r\n") {
			t.Errorf("EmailSender(%q) keeps a line break", from)
		}
	}

	server := &mailScript{}
	cfg := server.start(t)
	cfg.From = "Cosmos at home <cosmos@example.test>"
	steps, err := SendTestEmail(cfg, "admin@example.test")
	if err != nil {
		t.Fatalf("delivery failed: %v (%+v)", err, steps)
	}
	if heard := server.heard(); !strings.Contains(heard, "MAIL FROM:<cosmos@example.test>") {
		t.Errorf("the envelope sender must be the bare address:\n%s", heard)
	}
	if body := server.message(); !strings.HasPrefix(body, `From: "Cosmos at home" <cosmos@example.test>`) {
		t.Errorf("the From header:\n%.120s", body)
	}
}
