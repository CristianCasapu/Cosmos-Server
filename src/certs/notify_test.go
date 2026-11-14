package certs

import (
	"context"
	"crypto/x509"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestAFailedOrderIsToldOncePerReasonAndDay(t *testing.T) {
	env := setup(t, nil)
	key := "zone:example.org"
	message := "Zones: couldn't get the TLS certificate of example.org. Keeping the previous certificate if any"

	fail(key, env.now, FailDNSCredentials, 0)
	NoticeFailure(key, message, errors.New("The DNS provider refused the credentials (403)"))
	if len(env.errors) != 1 || !strings.HasPrefix(env.errors[0], "Zones: couldn't get the TLS certificate of example.org") ||
		!strings.Contains(env.errors[0], "The DNS provider refused the credentials") {
		t.Fatalf("the first failure: %v", env.errors)
	}
	// the same reason again within the day: the log only
	for _, later := range []time.Duration{time.Hour, 3 * time.Hour, 23 * time.Hour} {
		env.now = t0.Add(later)
		fail(key, env.now, FailDNSCredentials, 0)
		NoticeFailure(key, message, errors.New("again"))
	}
	if len(env.errors) != 1 {
		t.Errorf("the same reason was told %d times in a day", len(env.errors))
	}
	// another reason is news
	fail(key, env.now, FailDNSPropagation, 0)
	NoticeFailure(key, message, nil)
	if len(env.errors) != 2 || !strings.Contains(env.errors[1], FailureSentence(FailDNSPropagation)) {
		t.Errorf("a new reason: %v", env.errors)
	}
	// a day later the first reason is told again
	env.now = t0.Add(25 * time.Hour)
	fail(key, env.now, FailDNSCredentials, 0)
	NoticeFailure(key, message, errors.New("still refused"))
	if len(env.errors) != 3 {
		t.Errorf("a day later: %d notices", len(env.errors))
	}
	// another certificate is told on its own
	fail("zone:example.net", env.now, FailDNSCredentials, 0)
	NoticeFailure("zone:example.net", "Zones: couldn't get the TLS certificate of example.net", errors.New("refused"))
	if len(env.errors) != 4 {
		t.Errorf("another certificate: %d notices", len(env.errors))
	}
	// a success in between makes the next failure news again
	succeed(key, env.now, pair)
	fail(key, env.now.Add(time.Minute), FailDNSCredentials, 0)
	NoticeFailure(key, message, errors.New("refused"))
	if len(env.errors) != 5 {
		t.Errorf("after a success: %d notices", len(env.errors))
	}
}

func TestACertificateThatIsNotRenewedInTime(t *testing.T) {
	env := setup(t, nil)
	env.zoneCert(t, env.ca, "example.org", []string{"example.org", "*.example.org"}, t0.Add(-70*day), t0.Add(20*day), "app.example.org")

	// 20 days left of 90: due, but not yet worth a notice
	raiseNotices(GetStatus(), env.now)
	if len(env.notices) != 0 {
		t.Fatalf("20 days before the end: %+v", env.notices)
	}
	// 14 days left: half of the margin is gone
	env.now = t0.Add(6 * day)
	fail("zone:example.org", env.now, FailDNSCredentials, 0)
	raiseNotices(GetStatus(), env.now)
	if len(env.notices) != 1 {
		t.Fatalf("14 days before the end: %d notices", len(env.notices))
	}
	notice := env.notices[0]
	if notice.Recipient != "admin" || notice.Title != TitleExpiring || notice.Message != MessageExpiring || notice.Level != "warning" ||
		notice.Vars != "example.org (14 days left): The DNS provider refused the credentials" {
		t.Errorf("the notice: %+v", notice)
	}
	// not again the same day, again the next
	env.now = env.now.Add(5 * time.Hour)
	raiseNotices(GetStatus(), env.now)
	env.now = env.now.Add(20 * time.Hour)
	raiseNotices(GetStatus(), env.now)
	if len(env.notices) != 2 {
		t.Errorf("once a day: %d notices after 25 hours", len(env.notices))
	}
	// expired: an error, in hours near the end
	env.now = t0.Add(20*day - 20*time.Hour)
	raiseNotices(GetStatus(), env.now)
	if last := env.notices[len(env.notices)-1]; !strings.Contains(last.Vars, "(20 hours left)") {
		t.Errorf("near the end: %q", last.Vars)
	}
	env.now = t0.Add(21 * day)
	raiseNotices(GetStatus(), env.now)
	if last := env.notices[len(env.notices)-1]; last.Level != "error" || !strings.Contains(last.Vars, "(expired)") {
		t.Errorf("after the end: %+v", last)
	}

	// renewed: quiet
	before := len(env.notices)
	env.zoneCert(t, env.ca, "example.org", []string{"example.org", "*.example.org"}, env.now, env.now.Add(90*day), "app.example.org")
	succeed("zone:example.org", env.now, pair)
	env.now = env.now.Add(2 * day)
	raiseNotices(GetStatus(), env.now)
	if len(env.notices) != before {
		t.Errorf("after the renewal: %+v", env.notices[before:])
	}
}

func TestAddressesServedDirectlyWithoutATrustedCertificate(t *testing.T) {
	env := setup(t, func(config *utils.Config) {
		config.HTTPConfig.DNSZones = append(config.HTTPConfig.DNSZones, utils.DNSZoneConfig{Zone: "self.example.com", HTTPSCertificateMode: "SELFSIGNED"})
		for _, host := range []string{"films.example.org", "a.b.example.org", "a.self.example.com"} {
			config.HTTPConfig.ProxyConfig.Routes = append(config.HTTPConfig.ProxyConfig.Routes,
				utils.ProxyRouteConfig{Name: host, UseHost: true, Host: host, Mode: "PROXY", Target: "http://127.0.0.1:1"})
		}
		config.HTTPConfig.Cloudflare.Shielded = []string{"films.example.org"}
	})
	untrusted := func() []utils.Notification {
		out := []utils.Notification{}
		for _, notice := range env.notices {
			if notice.Title == TitleUntrusted {
				out = append(out, notice)
			}
		}
		return out
	}

	// a first order has an hour to end before anything is said
	raiseNotices(GetStatus(), env.now)
	env.now = t0.Add(59 * time.Minute)
	raiseNotices(GetStatus(), env.now)
	if len(untrusted()) != 0 {
		t.Fatalf("within the first hour: %+v", untrusted())
	}
	env.now = t0.Add(61 * time.Minute)
	raiseNotices(GetStatus(), env.now)
	if len(untrusted()) != 1 {
		t.Fatalf("after an hour: %d notices", len(untrusted()))
	}
	// the direct addresses this server should have a certificate for; not
	// the one through Cloudflare, not the one that is self-signed by choice
	if got := untrusted()[0]; got.Vars != "a.b.example.org, app.example.org" || got.Message != MessageUntrusted || got.Recipient != "admin" {
		t.Errorf("the notice: %+v", got)
	}
	// nothing new: quiet for a week
	env.now = t0.Add(3 * day)
	raiseNotices(GetStatus(), env.now)
	if len(untrusted()) != 1 {
		t.Errorf("the same list was told again after three days")
	}
	env.now = t0.Add(8 * day)
	raiseNotices(GetStatus(), env.now)
	if len(untrusted()) != 2 {
		t.Errorf("a week later it is told again: %d", len(untrusted()))
	}
	// the wildcard arrives: one address is left, which is news
	env.zoneCert(t, env.ca, "example.org", []string{"example.org", "*.example.org"}, env.now, env.now.Add(90*day), "app.example.org", "films.example.org")
	env.now = env.now.Add(time.Hour)
	raiseNotices(GetStatus(), env.now)
	if len(untrusted()) != 3 || untrusted()[2].Vars != "a.b.example.org" {
		t.Errorf("after the wildcard certificate: %+v", untrusted())
	}
	// everything covered: nothing is said, and the memory is cleared
	env.zoneCert(t, env.ca, "example.org", []string{"example.org", "*.example.org", "a.b.example.org"}, env.now, env.now.Add(90*day), "app.example.org", "films.example.org", "a.b.example.org")
	env.now = env.now.Add(time.Hour)
	raiseNotices(GetStatus(), env.now)
	if len(untrusted()) != 3 || len(store.snapshot().Untrusted) != 0 || len(store.snapshot().FirstSeen) != 0 {
		t.Errorf("all trusted: %d notices, remembered %+v", len(untrusted()), store.snapshot())
	}
}

func TestTheRenewalWindowOfTheCAIsAskedSparingly(t *testing.T) {
	env := setup(t, nil)
	stored := env.zoneCert(t, env.ca, "example.org", []string{"example.org", "*.example.org"}, t0.Add(-day), t0.Add(89*day))
	leaf := utils.ForkLeaf(stored.TLSCert)
	asked := 0
	var answer error
	renewalInfo = func(_ context.Context, _ CA, _ string, _ *x509.Certificate) (time.Time, time.Time, time.Duration, error) {
		asked++
		return t0.Add(55 * day), t0.Add(57 * day), 6 * time.Hour, answer
	}

	// no account yet: asking must not be what registers one
	refreshRenewalInfo(utils.GetMainConfig(), env.now)
	if asked != 0 {
		t.Fatal("the CA was asked without an account")
	}
	_, keyPEM, _ := utils.NewACMEAccountKey()
	if err := saveAccount(Current(), utils.ACMEAccount{Directory: Current().Directory, URL: "https://ca.example/acct/1", Key: keyPEM}); err != nil {
		t.Fatal(err)
	}
	// a certificate this server did not get from this CA is not asked about
	refreshRenewalInfo(utils.GetMainConfig(), env.now)
	if asked != 0 {
		t.Fatal("the CA was asked about a certificate it did not issue")
	}
	store.update("zone:example.org", func(state *certState) {
		state.IssuedSerial, state.IssuedDirectory = leaf.SerialNumber.String(), Current().Directory
	})

	refreshRenewalInfo(utils.GetMainConfig(), env.now)
	state := store.get("zone:example.org")
	if asked != 1 || state.ARI == nil || state.ARI.Start != t0.Add(55*day).Unix() || state.ARI.End != t0.Add(57*day).Unix() {
		t.Fatalf("asked %d times, kept %+v", asked, state.ARI)
	}
	pick := time.Unix(state.ARI.Pick, 0)
	if pick.Before(t0.Add(55*day)) || !pick.Before(t0.Add(57*day)) {
		t.Errorf("the moment drawn is outside the window: %v", pick)
	}
	// not again before the CA's Retry-After, and the moment stays
	env.now = t0.Add(5 * time.Hour)
	refreshRenewalInfo(utils.GetMainConfig(), env.now)
	env.now = t0.Add(7 * time.Hour)
	refreshRenewalInfo(utils.GetMainConfig(), env.now)
	if asked != 2 || store.get("zone:example.org").ARI.Pick != state.ARI.Pick {
		t.Errorf("asked %d times in 7 hours, the moment moved: %v", asked, store.get("zone:example.org").ARI.Pick != state.ARI.Pick)
	}
	// the CA does not answer: asked again after six hours, the window is kept
	answer = errors.New("timeout")
	env.now = t0.Add(14 * time.Hour)
	refreshRenewalInfo(utils.GetMainConfig(), env.now)
	env.now = t0.Add(19 * time.Hour)
	refreshRenewalInfo(utils.GetMainConfig(), env.now)
	if asked != 3 || store.get("zone:example.org").ARI.Pick != state.ARI.Pick {
		t.Errorf("after an error: asked %d times", asked)
	}
	if moment, source := renewMoment(leaf, store.get("zone:example.org").ARI); source != "ca" || !moment.Equal(pick) {
		t.Errorf("the renewal follows the CA: %v by %s", moment, source)
	}
}
