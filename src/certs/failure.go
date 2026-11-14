package certs

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/go-acme/lego/v5/acme"
)

// The reasons an order can fail for, as the panel knows them. The panel
// turns a code into what to do (client/src/utils/certificates.js); a test
// on each side keeps the two lists the same.
const (
	FailRateLimited       = "rateLimited"       // the CA says: too many requests
	FailCAUnreachable     = "caUnreachable"     // the CA did not answer, or had a problem of its own
	FailCARefused         = "caRefused"         // the CA will not issue for these names
	FailAccount           = "account"           // the CA does not know (or refuses) the account
	FailDNSCredentials    = "dnsCredentials"    // the DNS provider refused the token or key
	FailDNSZone           = "dnsZone"           // the DNS provider does not have the domain under these credentials
	FailDNSProvider       = "dnsProvider"       // the DNS provider failed otherwise
	FailDNSPropagation    = "dnsPropagation"    // the record did not show up in time
	FailValidation        = "validation"        // the CA looked and did not find what it expected
	FailCAA               = "caa"               // a CAA record forbids the CA
	FailDelegationMissing = "delegationMissing" // the CNAME of a delegated challenge is not there
	FailNoProvider        = "noProvider"        // nothing to answer the challenge with
	FailTimeout           = "timeout"           // the order took too long
	FailOther             = "other"
)

// FailureCodes lists every code, for the tests.
var FailureCodes = []string{
	FailRateLimited, FailCAUnreachable, FailCARefused, FailAccount, FailDNSCredentials, FailDNSZone,
	FailDNSProvider, FailDNSPropagation, FailValidation, FailCAA, FailDelegationMissing, FailNoProvider,
	FailTimeout, FailOther,
}

// Failure is why an order did not give a certificate.
type Failure struct {
	Code string
	// Detail is the other side's own words: one line, bounded, with
	// nothing secret in it.
	Detail string
	// RetryAfter is how long the CA asked to wait, zero when it did not.
	RetryAfter time.Duration
}

func (f *Failure) Error() string { return f.Code + ": " + f.Detail }

const maxDetail = 300

// clean makes a text safe to log, store and show: one line, no control
// characters, valid UTF-8, at most max bytes.
func clean(text string, max int) string {
	text = strings.ToValidUTF8(text, "")
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return ' '
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > max {
		text = strings.ToValidUTF8(text[:max], "") + "…"
	}
	return text
}

// what looks like a credential whatever it is: a bearer value, or a long
// run of key characters (API tokens are 37 characters and more)
var (
	bearerShape = regexp.MustCompile(`(?i)(bearer|(?:token|key|secret|password)"?\s*[=:])(\s*"?)[^\s"',;]{6,}`)
	tokenShape  = regexp.MustCompile(`[A-Za-z0-9_\-]{37,}`)
)

// scrub takes out of a text every secret given, and anything shaped like
// one. What a DNS provider or a CA writes into an error can quote the
// request it refused.
func scrub(text string, secrets []string) string {
	for _, secret := range secrets {
		if secret = strings.TrimSpace(secret); len(secret) >= 6 {
			text = strings.ReplaceAll(text, secret, "***")
		}
	}
	text = bearerShape.ReplaceAllString(text, "$1$2***")
	return tokenShape.ReplaceAllString(text, "***")
}

// The stages of an order, for classify.
const (
	stageDirectory = "directory"
	stageAccount   = "account"
	stageProvider  = "provider"
	stageOrder     = "order"
)

// classify turns an error of the ACME library into a Failure. secrets are
// the values that must not appear in the detail.
func classify(stage string, err error, secrets []string) *Failure {
	if err == nil {
		return nil
	}
	var known *Failure
	if errors.As(err, &known) {
		return known
	}
	failure := &Failure{Code: FailOther, Detail: clean(scrub(err.Error(), secrets), maxDetail)}
	text := strings.ToLower(err.Error())

	var limited *acme.RateLimitedError
	if errors.As(err, &limited) {
		failure.Code = FailRateLimited
		failure.RetryAfter = limited.RetryAfter
		return failure
	}

	var problem *acme.ProblemDetails
	if errors.As(err, &problem) {
		kind := strings.TrimPrefix(problem.Type, "urn:ietf:params:acme:error:")
		switch kind {
		case "rateLimited":
			failure.Code = FailRateLimited
		case "accountDoesNotExist":
			failure.Code = FailAccount
		case "caa":
			failure.Code = FailCAA
		case "dns", "connection", "tls", "incorrectResponse":
			failure.Code = FailValidation
		case "unauthorized":
			// during a challenge: the CA did not find the record; anywhere
			// else: the account may not do this
			failure.Code = FailValidation
			if stage == stageAccount {
				failure.Code = FailAccount
			}
		case "rejectedIdentifier", "unsupportedIdentifier", "badCSR", "malformed", "orderNotReady", "invalidProfile":
			failure.Code = FailCARefused
		case "serverInternal":
			failure.Code = FailCAUnreachable
		default:
			if problem.HTTPStatus >= 500 {
				failure.Code = FailCAUnreachable
			}
		}
		if failure.Code != FailOther {
			return failure
		}
	}

	switch {
	case errors.Is(err, context.DeadlineExceeded) && stage == stageOrder && !strings.Contains(text, "propagation"):
		failure.Code = FailTimeout
	case strings.Contains(text, "time limit exceeded") || strings.Contains(text, "propagation"):
		failure.Code = FailDNSPropagation
	case stage == stageProvider || strings.Contains(text, "error presenting token"):
		failure.Code = FailDNSProvider
		switch {
		case containsAny(text, "401", "403", "unauthorized", "forbidden", "authentication", "invalid access token",
			"invalid credentials", "invalid request headers", "permission", "credentials information are missing",
			"missing credentials", "access denied", "not allowed"):
			failure.Code = FailDNSCredentials
		case containsAny(text, "could not find zone", "failed to find zone", "zone not found", "no zone found",
			"could not determine zone", "could not find the start of authority", "zone could not be found"):
			failure.Code = FailDNSZone
		}
	case stage == stageDirectory:
		failure.Code = FailCAUnreachable
	case stage == stageAccount:
		failure.Code = FailAccount
		if isNetworkError(err, text) {
			failure.Code = FailCAUnreachable
		}
	case isNetworkError(err, text) && strings.Contains(text, "acme"):
		failure.Code = FailCAUnreachable
	}
	return failure
}

func containsAny(text string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(text, part) {
			return true
		}
	}
	return false
}

func isNetworkError(err error, text string) bool {
	var netErr net.Error
	return errors.As(err, &netErr) || containsAny(text, "dial tcp", "no such host", "connection refused",
		"i/o timeout", "tls: ", "x509: ", "deadline exceeded")
}
