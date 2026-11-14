package main

import (
	"errors"
	"strings"
)

// Fork addition: the notice of a failed Let's Encrypt order says why.
//
// When an order fails the server keeps serving HTTPS with the certificate it
// has (the self-signed one when there is no other) and tells the admins
// "Couldn't get the TLS certificate of example.com", but the reason was left
// out of that notice: it was only in the log and on the home page until the
// next restart. This hands the reason of the order that just failed
// (utils.LetsEncryptErrors, filled by utils.DoLetsEncrypt) to the notice.
//
// The reasons come scrubbed from src/certs (no credential, one line), for
// the orders over HTTP-01 (issueLocalCerts) and through a DNS provider
// (issueZoneCerts) alike.

// maxLetsEncryptReason bounds the reason shown in a notice.
const maxLetsEncryptReason = 400

// letsEncryptFailure returns the reason of the last failed order as an error,
// nil when none was recorded.
func letsEncryptFailure(recorded []string) error {
	for i := len(recorded) - 1; i >= 0; i-- {
		reason := strings.Join(strings.Fields(recorded[i]), " ")
		if reason == "" {
			continue
		}
		if len(reason) > maxLetsEncryptReason {
			reason = strings.ToValidUTF8(reason[:maxLetsEncryptReason], "") + "…"
		}
		return errors.New(reason)
	}
	return nil
}
