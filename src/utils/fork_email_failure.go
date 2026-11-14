package utils

// Fork addition: why a mail delivery failed, as a fixed word. The settings
// form turns it into a next step ("this provider wants an app password",
// "try port 587"); the server's own text stays in the step's detail.

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/textproto"
	"strings"
	"syscall"
)

// Codes of a failed step.
const (
	EmailFailHostNotFound     = "host-not-found"     // the server name does not exist
	EmailFailTimeout          = "timeout"            // nothing answered: wrong port, or the port is blocked on the way
	EmailFailRefused          = "refused"            // the machine answered that nothing listens on this port
	EmailFailUnreachable      = "unreachable"        // no route to the server
	EmailFailTLSExpected      = "tls-expected"       // the port wants TLS from the start and got a plain connection
	EmailFailNotTLS           = "not-tls"            // TLS from the start on a port that does not speak it
	EmailFailConnect          = "connect-failed"     // any other failure to get the greeting
	EmailFailNoStartTLS       = "no-starttls"        // the server does not offer STARTTLS
	EmailFailCertUntrusted    = "cert-untrusted"     // certificate self-signed or from an unknown authority
	EmailFailCertName         = "cert-name"          // certificate issued for another name
	EmailFailCertExpired      = "cert-expired"       // certificate out of date
	EmailFailTLS              = "tls-failed"         // any other TLS failure
	EmailFailAuthRejected     = "auth-rejected"      // user name or password not accepted
	EmailFailAuthAppPassword  = "auth-app-password"  // the provider asks for an app password
	EmailFailAuthDisabled     = "auth-disabled"      // sign-in with a password is switched off for this mailbox (Microsoft 365)
	EmailFailAuthNeedsTLS     = "auth-needs-tls"     // the password is only taken over an encrypted connection
	EmailFailAuthNoMethod     = "auth-no-method"     // the server offers no sign-in with a password
	EmailFailAuth             = "auth-failed"        // any other sign-in failure
	EmailFailTryLater         = "try-later"          // the server says: temporary, try again
	EmailFailNeedsTLS         = "needs-tls"          // the server wants STARTTLS before taking mail
	EmailFailNeedsSignIn      = "needs-sign-in"      // the server takes mail only from signed-in senders
	EmailFailSenderRejected   = "sender-rejected"    // the sender address was refused
	EmailFailRecipientRefused = "recipient-rejected" // the recipient was refused
	EmailFailSend             = "send-rejected"      // the message itself was refused
)

func certificateFailure(err error) string {
	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		return EmailFailCertName
	}
	var invalid x509.CertificateInvalidError
	if errors.As(err, &invalid) {
		if invalid.Reason == x509.Expired {
			return EmailFailCertExpired
		}
		return EmailFailCertUntrusted
	}
	var unknown x509.UnknownAuthorityError
	var verification *tls.CertificateVerificationError
	if errors.As(err, &unknown) || errors.As(err, &verification) {
		return EmailFailCertUntrusted
	}
	return ""
}

// emailFailureCode names the reason step failed with err.
func emailFailureCode(step string, err error, cfg EmailConfig) string {
	if err == nil {
		return ""
	}
	var reply *textproto.Error
	isReply := errors.As(err, &reply)
	text := strings.ToLower(err.Error())
	temporary := isReply && reply.Code >= 400 && reply.Code < 500
	plain465 := !cfg.UseTLS && strings.TrimSpace(cfg.Port) == "465"

	switch step {
	case "connect":
		if code := certificateFailure(err); code != "" {
			return code
		}
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) {
			if dnsErr.IsNotFound {
				return EmailFailHostNotFound
			}
			return EmailFailTimeout
		}
		var header tls.RecordHeaderError
		if errors.As(err, &header) {
			return EmailFailNotTLS
		}
		if plain465 {
			return EmailFailTLSExpected
		}
		var netErr net.Error
		switch {
		case errors.Is(err, syscall.ECONNREFUSED):
			return EmailFailRefused
		case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
			return EmailFailUnreachable
		case errors.As(err, &netErr) && netErr.Timeout():
			return EmailFailTimeout
		case temporary:
			return EmailFailTryLater
		case errors.Is(err, io.EOF) && usesImplicitTLS(cfg):
			return EmailFailNotTLS
		}
		return EmailFailConnect
	case "tls":
		if errors.Is(err, errNoStartTLS) {
			return EmailFailNoStartTLS
		}
		if code := certificateFailure(err); code != "" {
			return code
		}
		return EmailFailTLS
	case "auth":
		switch {
		case errors.Is(err, errNoPasswordSignIn):
			return EmailFailAuthNoMethod
		case !isReply && strings.Contains(text, "unencrypted connection"):
			return EmailFailAuthNeedsTLS
		case !isReply:
			return EmailFailAuth
		case strings.Contains(text, "5.7.139") || strings.Contains(text, "smtpclientauthentication") || strings.Contains(text, "basic authentication is disabled"):
			return EmailFailAuthDisabled
		case reply.Code == 534 || strings.Contains(text, "application-specific password") || strings.Contains(text, "app password"):
			return EmailFailAuthAppPassword
		case reply.Code == 530 || reply.Code == 538 || strings.Contains(text, "starttls"):
			return EmailFailAuthNeedsTLS
		case reply.Code == 504:
			return EmailFailAuthNoMethod
		case temporary:
			return EmailFailTryLater
		case reply.Code == 535:
			return EmailFailAuthRejected
		}
		return EmailFailAuth
	case "sender", "recipient":
		switch {
		case isReply && strings.Contains(text, "starttls"):
			return EmailFailNeedsTLS
		case isReply && (reply.Code == 530 || strings.Contains(text, "authentication required") || strings.Contains(text, "relay")) && cfg.Username == "" && cfg.Password == "":
			return EmailFailNeedsSignIn
		case temporary:
			return EmailFailTryLater
		case step == "sender":
			return EmailFailSenderRejected
		}
		return EmailFailRecipientRefused
	}
	if temporary {
		return EmailFailTryLater
	}
	return EmailFailSend
}
