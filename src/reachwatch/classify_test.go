package reachwatch

import (
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

const cloudflarePage = "<html><head><title>example.org | 522: Connection timed out</title></head><body>cloudflare Ray ID: 8a1b</body></html>"

// TestEveryOutcomeHasItsCause is the table of the plan, section 3.3.
func TestEveryOutcomeHasItsCause(t *testing.T) {
	cf := func(status int, body string) Outcome {
		return Outcome{Status: status, CloudflareMark: true, Body: body}
	}
	cases := []struct {
		name   string
		in     Outcome
		status string
		cause  string
		code   string
	}{
		{"arrived", Outcome{Status: 200, CloudflareMark: true, Body: "OK", Arrived: true}, StatusOK, "seen", ""},
		{"arrived, whatever the status", Outcome{Status: 503, Arrived: true}, StatusOK, "seen", ""},
		{"no such name", Outcome{NotFound: true}, StatusProblem, "dnsNotFound", ""},
		{"lookup failed", Outcome{LookupFailed: true}, StatusUnknown, "offline", ""},
		{"resolves around Cloudflare", Outcome{NoCloudflare: true}, StatusProblem, "dnsNotCloudflare", ""},
		{"local DNS", Outcome{OnlyPrivate: true}, StatusUnknown, "dnsLocal", ""},
		{"no way out", Outcome{DialFailed: true}, StatusUnknown, "offline", ""},
		{"edge certificate", Outcome{EdgeCertBad: true}, StatusProblem, "edgeCert", ""},
		{"TLS broken", Outcome{TLSFailed: true}, StatusUnknown, "noAnswer", ""},
		{"no answer", Outcome{NoAnswer: true}, StatusUnknown, "noAnswer", ""},
		{"520", cf(520, cloudflarePage), StatusProblem, "unexpected", "520"},
		{"521", cf(521, cloudflarePage), StatusProblem, "refused", "521"},
		{"522", cf(522, cloudflarePage), StatusProblem, "connect", "522"},
		{"523", cf(523, cloudflarePage), StatusProblem, "unreachable", "523"},
		{"524", cf(524, cloudflarePage), StatusProblem, "timeout", "524"},
		{"525", cf(525, cloudflarePage), StatusProblem, "tlsHandshake", "525"},
		{"526", cf(526, cloudflarePage), StatusProblem, "tlsCertificate", "526"},
		{"530", cf(530, "error code: 1016"), StatusProblem, "originDNS", "530"},
		{"522 without Cloudflare's marks", Outcome{Status: 522, Body: cloudflarePage}, StatusProblem, "foreign", "522"},
		{"origin guard", cf(403, "This address is available through Cloudflare's proxy only.\n"), StatusProblem, "originRefused", "403"},
		{"challenge", Outcome{Status: 403, CloudflareMark: true, Challenge: true, Body: "<html>Just a moment...</html>"}, StatusUnknown, "challenged", "403"},
		{"edge block", cf(403, "<title>Attention Required! | Cloudflare</title> error code: 1006"), StatusUnknown, "edgeBlocked", "403"},
		{"rate limited at the edge", cf(429, "<title>Cloudflare</title> error code: 1015"), StatusUnknown, "edgeBlocked", "429"},
		{"Cloudflare's 502", cf(502, "<title>502 Bad gateway</title> cloudflare"), StatusProblem, "cloudflare", "502"},
		{"Cloudflare's 503", cf(503, "cloudflare"), StatusProblem, "cloudflare", "503"},
		{"another server's OK", cf(200, "OK"), StatusProblem, "foreign", "200"},
		{"another server's 404", cf(404, "not found"), StatusProblem, "foreign", "404"},
		{"a redirect", cf(301, ""), StatusProblem, "foreign", "301"},
		{"a 502 of the origin", cf(502, "Bad Gateway"), StatusProblem, "foreign", "502"},
		{"no answer at all", Outcome{}, StatusUnknown, "noAnswer", ""},
	}
	for _, c := range cases {
		got := Classify(c.in)
		if got.Status != c.status || got.Cause != c.cause || got.Code != c.code {
			t.Errorf("%s: got %+v, want %s %s %s", c.name, got, c.status, c.cause, c.code)
		}
	}
}

// TestEveryCauseHasItsSentences: each problem has what, means and a step in
// English; the router causes have the variant that says the router was
// asked; each cause has its few words.
func TestEveryCauseHasItsSentences(t *testing.T) {
	for _, cause := range utils.ReachWatchProblemCauses {
		for _, key := range []string{"notif.reach.down." + cause + ".what", "notif.reach.down." + cause + ".means", "notif.step.reach." + cause, "notif.reach.cause." + cause} {
			if utils.NotificationEnglish(key) == "" {
				t.Errorf("%s has no English", key)
			}
		}
	}
	for _, cause := range utils.ReachWatchPathCauses {
		if utils.NotificationEnglish("notif.reach.down."+cause+".asked.what") == "" {
			t.Errorf("%s: no sentence for an asked router", cause)
		}
	}
	for _, cause := range append(utils.ReachWatchUnknownCauses, "seen") {
		if utils.NotificationEnglish("notif.reach.cause."+cause) == "" {
			t.Errorf("cause %s has no words", cause)
		}
	}
	if Level("cloudflare") != "warning" || Level("connect") != "error" {
		t.Error("levels")
	}
}
