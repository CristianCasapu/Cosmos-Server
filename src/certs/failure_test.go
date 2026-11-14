package certs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-acme/lego/v5/acme"
)

func problem(kind string, status int, detail string) error {
	return &acme.ProblemDetails{Type: "urn:ietf:params:acme:error:" + kind, HTTPStatus: status, Detail: detail}
}

func TestFailuresGetAFixedReason(t *testing.T) {
	cases := []struct {
		name  string
		stage string
		err   error
		code  string
	}{
		{"the CA's rate limit", stageOrder, &acme.RateLimitedError{ProblemDetails: problem("rateLimited", 429, "too many certificates").(*acme.ProblemDetails), RetryAfter: 2 * time.Hour}, FailRateLimited},
		{"a rate limit without the typed error", stageOrder, problem("rateLimited", 429, "too many"), FailRateLimited},
		{"CAA forbids", stageOrder, fmt.Errorf("obtain: %w", problem("caa", 403, "CAA record for example.org prevents issuance")), FailCAA},
		{"the CA did not find the record", stageOrder, problem("unauthorized", 403, "Incorrect TXT record"), FailValidation},
		{"the CA's DNS lookup failed", stageOrder, problem("dns", 400, "DNS problem: NXDOMAIN looking up TXT"), FailValidation},
		{"the CA does not know the account", stageOrder, problem("accountDoesNotExist", 400, "no account"), FailAccount},
		{"registration refused", stageAccount, problem("unauthorized", 403, "blocked"), FailAccount},
		{"a name the CA will not certify", stageOrder, problem("rejectedIdentifier", 400, "policy forbids issuing"), FailCARefused},
		{"the CA has a problem", stageOrder, problem("serverInternal", 500, "oops"), FailCAUnreachable},
		{"an unknown problem with a 503", stageOrder, problem("somethingNew", 503, "busy"), FailCAUnreachable},
		{"the directory cannot be fetched", stageDirectory, errors.New(`get directory at 'https://acme-v02.api.letsencrypt.org/directory': Get "…": dial tcp: lookup acme-v02.api.letsencrypt.org: no such host`), FailCAUnreachable},
		{"registration cannot reach the CA", stageAccount, errors.New("dial tcp 203.0.113.1:443: i/o timeout"), FailCAUnreachable},
		{"Cloudflare refuses the token", stageOrder, errors.New("obtain: one or more domains had a problem: [example.org: dns01: error presenting token (example.org): cloudflare: failed to find zone example.org.: zones: unexpected status code: [status code: 403] body: {\"success\":false,\"errors\":[{\"code\":9109,\"message\":\"Invalid access token\"}]}]"), FailDNSCredentials},
		{"the provider does not have the domain", stageOrder, errors.New("dns01: error presenting token (example.org): cloudflare: failed to find zone example.org.: zone could not be found"), FailDNSZone},
		{"the provider fails otherwise", stageOrder, errors.New("dns01: error presenting token (example.org): hetzner: failed to create TXT record: unexpected status code: [status code: 500]"), FailDNSProvider},
		{"credentials are missing", stageProvider, errors.New("hetzner: some credentials information are missing: HETZNER_API_TOKEN"), FailDNSCredentials},
		{"the provider cannot be built", stageProvider, errors.New("ovh: unknown endpoint"), FailDNSProvider},
		{"the record did not show up", stageOrder, errors.New("dns01: time limit exceeded: last error: NS ns1.example.org.:53 returned NXDOMAIN for _acme-challenge.example.org."), FailDNSPropagation},
		{"the order was stopped", stageOrder, fmt.Errorf("order: %w", context.DeadlineExceeded), FailTimeout},
		{"something else", stageOrder, errors.New("an unexpected thing"), FailOther},
	}
	known := map[string]bool{}
	for _, code := range FailureCodes {
		known[code] = true
	}
	for _, c := range cases {
		failure := classify(c.stage, c.err, nil)
		if failure.Code != c.code {
			t.Errorf("%s: %q, want %q", c.name, failure.Code, c.code)
		}
		if !known[failure.Code] {
			t.Errorf("%s: %q is not in the list of codes", c.name, failure.Code)
		}
		if failure.Detail == "" || strings.ContainsAny(failure.Detail, "\n\r\t") {
			t.Errorf("%s: the detail is not one line: %q", c.name, failure.Detail)
		}
	}
	limited := classify(stageOrder, cases[0].err, nil)
	if limited.RetryAfter != 2*time.Hour {
		t.Errorf("the CA's Retry-After is lost: %v", limited.RetryAfter)
	}
	if classify(stageOrder, nil, nil) != nil {
		t.Error("no error is no failure")
	}
	// a failure this package made itself passes through
	own := &Failure{Code: FailDelegationMissing, Detail: "x"}
	if got := classify(stageOrder, fmt.Errorf("wrapped: %w", own), nil); got != own {
		t.Errorf("an own failure was reclassified: %+v", got)
	}
	// every code has words of its own for the log and the notices
	fallback := FailureSentence("no such code")
	for _, code := range FailureCodes {
		sentence := FailureSentence(code)
		if sentence == "" || (sentence == fallback && code != FailOther) {
			t.Errorf("%s has no sentence", code)
		}
	}
}

func TestNothingSecretOrOddReachesTheDetail(t *testing.T) {
	secret := "s3cr3t-value-of-a-provider"
	err := errors.New("provider said: 403 for request with Authorization: Bearer " + testToken +
		"\n\x1b[31mX-Auth-Key: " + secret + "\r\nand an unknown token 0123456789abcdef0123456789abcdef01234567 at https://api.example/v4")
	failure := classify(stageOrder, err, []string{secret, "  ", "abc"})
	for _, hidden := range []string{testToken, secret, "0123456789abcdef0123456789abcdef01234567"} {
		if strings.Contains(failure.Detail, hidden) {
			t.Errorf("the detail carries %q: %s", hidden, failure.Detail)
		}
	}
	if strings.ContainsAny(failure.Detail, "\n\r\x1b") {
		t.Errorf("control characters in the detail: %q", failure.Detail)
	}
	if !strings.Contains(failure.Detail, "provider said: 403") || !strings.Contains(failure.Detail, "https://api.example/v4") {
		t.Errorf("the useful part is gone: %q", failure.Detail)
	}

	long := classify(stageOrder, errors.New(strings.Repeat("é", 4000)), nil)
	if len(long.Detail) > maxDetail+len("…") || !strings.HasSuffix(long.Detail, "…") || strings.ContainsRune(long.Detail, '�') {
		t.Errorf("a long detail is not cut cleanly: %d bytes", len(long.Detail))
	}
	if got := clean("a b\x00c  d\n", 100); got != "a b c d" {
		t.Errorf("clean: %q", got)
	}
	// a short secret is not worth hiding (it would blank out ordinary words)
	if got := scrub("the zone abc was not found", []string{"abc"}); got != "the zone abc was not found" {
		t.Errorf("scrub: %q", got)
	}
}
