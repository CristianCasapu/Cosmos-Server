// Package reachwatch is a fork addition: it tests by itself, every few
// minutes, whether the server can be reached from the internet.
//
// For a name served through Cloudflare's proxy the server can test the
// whole outside path alone: a request to https://<name>:<public port>/
// resolves to Cloudflare, leaves the house, and Cloudflare connects back to
// the public address through the router like it does for any visitor. If the
// forward is gone Cloudflare answers 522, if the connection is refused 521,
// if the certificate does not suit it 526; if all is well the request
// arrives at this very process, which is the proof (utils.NoteWatchProbe).
// No outside machine and nobody new is involved. A name served directly
// cannot be tested this way (the request would only be turned around by the
// router) and is not.
//
// A failure is tried twice more before it counts; a round that could not run
// (no way out from the server) concludes nothing; what is announced is the
// change: down, and back with how long it lasted.
package reachwatch

import (
	"strconv"
	"strings"
)

// The status of one test.
const (
	StatusOK      = "ok"
	StatusProblem = "problem"
	StatusUnknown = "unknown"
)

// Outcome is everything one test of a name gave.
type Outcome struct {
	// DNS: the lookup of the name
	NotFound       bool // the name does not exist
	LookupFailed   bool // no answer, or the resolver failed
	NoCloudflare   bool // it resolves, to public addresses that are not Cloudflare's
	OnlyPrivate    bool // it resolves to private addresses only (a local DNS)
	DialFailed     bool // no connection to Cloudflare's address could be made
	EdgeCertBad    bool // Cloudflare's certificate for the name did not verify
	TLSFailed      bool // the TLS connection to Cloudflare broke otherwise
	NoAnswer       bool // no answer in time
	Status         int  // the HTTP status, 0 without an answer
	CloudflareMark bool // the answer carries Cloudflare's marks (CF-Ray, Server: cloudflare)
	Challenge      bool // cf-mitigated: challenge
	Body           string
	Arrived        bool // the request came in to this server
}

// Verdict is what one test means.
type Verdict struct {
	Status string `json:"status"`
	Cause  string `json:"cause"`
	// Code is Cloudflare's status when it is part of the cause ("522")
	Code string `json:"code,omitempty"`
}

// what the origin guard (proxy/fork_cloudflare.go) answers when it refuses
var guardSentences = []string{
	"This address is available through Cloudflare's proxy only.",
	"This address is available through Cloudflare only.",
}

// Classify says what an outcome means.
func Classify(o Outcome) Verdict {
	code := ""
	if o.Status > 0 {
		code = strconv.Itoa(o.Status)
	}
	v := func(status, cause string) Verdict { return Verdict{Status: status, Cause: cause} }
	switch {
	case o.Arrived:
		return v(StatusOK, "seen")
	case o.NotFound:
		return v(StatusProblem, "dnsNotFound")
	case o.LookupFailed:
		return v(StatusUnknown, "offline")
	case o.NoCloudflare:
		return v(StatusProblem, "dnsNotCloudflare")
	case o.OnlyPrivate:
		return v(StatusUnknown, "dnsLocal")
	case o.DialFailed:
		return v(StatusUnknown, "offline")
	case o.EdgeCertBad:
		return v(StatusProblem, "edgeCert")
	case o.TLSFailed, o.NoAnswer, o.Status == 0:
		return v(StatusUnknown, "noAnswer")
	}

	body := strings.ToLower(o.Body)
	cloudflarePage := o.CloudflareMark && strings.Contains(body, "cloudflare")
	if o.CloudflareMark {
		causes := map[int]string{520: "unexpected", 521: "refused", 522: "connect", 523: "unreachable", 524: "timeout",
			525: "tlsHandshake", 526: "tlsCertificate", 530: "originDNS"}
		if cause, ok := causes[o.Status]; ok {
			return Verdict{Status: StatusProblem, Cause: cause, Code: code}
		}
		if o.Challenge {
			return Verdict{Status: StatusUnknown, Cause: "challenged", Code: code}
		}
	}
	for _, sentence := range guardSentences {
		if o.Status == 403 && strings.Contains(o.Body, sentence) {
			return Verdict{Status: StatusProblem, Cause: "originRefused", Code: code}
		}
	}
	if cloudflarePage && (o.Status == 403 || o.Status == 429 || strings.Contains(body, "error code: 10")) {
		return Verdict{Status: StatusUnknown, Cause: "edgeBlocked", Code: code}
	}
	if cloudflarePage && (o.Status == 500 || o.Status == 502 || o.Status == 503 || o.Status == 504) {
		return Verdict{Status: StatusProblem, Cause: "cloudflare", Code: code}
	}
	return Verdict{Status: StatusProblem, Cause: "foreign", Code: code}
}

// Level is the level of a notification about a cause.
func Level(cause string) string {
	if cause == "cloudflare" {
		return "warning"
	}
	return "error"
}
