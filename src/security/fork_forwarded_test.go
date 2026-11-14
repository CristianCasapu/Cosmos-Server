package security

// Fork addition: which incidents get a line in the Fail2ban file when the
// server is reached through Cloudflare (or another proxy) for some hostnames
// and directly for others.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestLogFileLeavesOutAddressesAProxyReportedAndCloudflaresOwn(t *testing.T) {
	previous := utils.GetBaseMainConfig()
	t.Cleanup(func() { utils.LoadBaseMainConfig(previous) })
	config := utils.Config{}
	config.HTTPConfig.Cloudflare = utils.ForkCloudflareConfig{Shielded: []string{"example.org"}, TrustRanges: true}
	utils.LoadBaseMainConfig(config)

	folder := openDatabase(t)
	clock := newTestClock()
	recorder := startRecorder(t, folder, clock.now)

	// a visitor who connects to the server: acted on, as before
	recorder.Record(Event{Kind: utils.SecLoginFailed, IP: "203.0.113.7"})
	recorder.Record(Event{Kind: utils.SecAccessOriginDenied, IP: "203.0.113.8", Details: map[string]string{"reason": "not_cloudflare"}})
	// the same kind of event about an address Cloudflare reported: recorded, no line
	recorder.Record(Event{Kind: utils.SecLoginFailed, IP: "198.51.100.77", Forwarded: true})
	recorder.Record(Event{Kind: utils.SecScanProbe, IP: "2001:db8::77", Forwarded: true})
	recorder.Record(Event{Kind: utils.SecShieldStrike, IP: "198.51.100.77", Forwarded: true})
	// events whose address is one of Cloudflare's own (a request from there for a
	// name that is not served through it, or one refused for lack of a certificate)
	recorder.Record(Event{Kind: utils.SecShieldHostname, IP: "173.245.48.9"})
	recorder.Record(Event{Kind: utils.SecAccessOriginDenied, IP: "2606:4700::9", Details: map[string]string{"reason": "no_certificate"}})
	recorder.Record(Event{Kind: utils.SecLoginFailed, IP: "104.16.5.5"})
	recorder.Flush()

	stamp := clock.now().Format(logTimeFormat)
	want := stamp + " cosmos-security kind=auth.login.failed ip=203.0.113.7 sev=medium n=1\n" +
		stamp + " cosmos-security kind=access.origin.denied ip=203.0.113.8 sev=low n=1\n"
	if got := readLog(t, folder); got != want {
		t.Fatalf("log file:\n%s\nwant:\n%s", got, want)
	}
	// the record itself has all of them
	if events := all(t); len(events) != 8 {
		t.Errorf("%d events stored, want 8", len(events))
	}

	// nothing is served through Cloudflare: its addresses are addresses like any other
	config.HTTPConfig.Cloudflare = utils.ForkCloudflareConfig{}
	utils.LoadBaseMainConfig(config)
	recorder.Record(Event{Kind: utils.SecLoginFailed, IP: "173.245.48.9"})
	recorder.Flush()
	if got := readLog(t, folder); !strings.HasSuffix(got, " kind=auth.login.failed ip=173.245.48.9 sev=medium n=1\n") {
		t.Errorf("with nothing served through Cloudflare, an event from its addresses has no line:\n%s", got)
	}
}

// A flood is summed up per source. The same address as a proxy's report and
// as a connection of its own are two sources: only the second one's summary
// is a line.
func TestLogFileSummaryOfAReportedAddressHasNoLine(t *testing.T) {
	folder := openDatabase(t)
	clock := newTestClock()
	recorder := newRecorder(func() string { return filepath.Join(folder, LogFileName) }, clock.now)
	go recorder.run()

	for i := 0; i < 30; i++ {
		recorder.Record(Event{Kind: utils.SecLoginFailed, IP: "198.51.100.77", Forwarded: true})
	}
	for i := 0; i < 25; i++ {
		recorder.Record(Event{Kind: utils.SecLoginFailed, IP: "198.51.100.77"})
	}
	recorder.Stop() // the pending summaries are written on the way out

	lines := strings.Split(strings.TrimSuffix(readLog(t, folder), "\n"), "\n")
	if len(lines) != 11 || !strings.HasSuffix(lines[10], " kind=auth.login.failed ip=198.51.100.77 sev=medium n=15") {
		t.Fatalf("lines for 25 direct and 30 reported events from one address, want the 10 direct ones and their summary:\n%s", strings.Join(lines, "\n"))
	}
	summaries := 0
	for _, event := range all(t) {
		if event.Details["summary"] == "1" {
			summaries++
		}
	}
	if summaries != 2 {
		t.Errorf("%d summary rows in the record, want one for each way the address came", summaries)
	}
}
