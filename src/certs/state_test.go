package certs

import (
	"crypto/x509"
	"os"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func parseLeaf(t *testing.T, certPEM string) *x509.Certificate {
	t.Helper()
	leaf := utils.ForkLeaf(certPEM)
	if leaf == nil {
		t.Fatal("not a certificate")
	}
	return leaf
}

func cert0(until time.Time) utils.ZoneCert {
	return utils.ZoneCert{TLSCert: "not a certificate", ValidUntil: until}
}

var pair = []string{"example.org", "*.example.org"}

func fail(key string, now time.Time, code string, retryAfter time.Duration) {
	store.started(key, now)
	store.finished(key, now, Current(), pair, "", &Failure{Code: code, Detail: "detail", RetryAfter: retryAfter})
}

func succeed(key string, now time.Time, names []string) {
	store.started(key, now)
	store.finished(key, now, Current(), names, "1234", nil)
}

func TestBackoffDoublesUpToADay(t *testing.T) {
	want := []time.Duration{0, time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 16 * time.Hour, 24 * time.Hour, 24 * time.Hour}
	for failures, wait := range want {
		if got := backoff(failures); got != wait {
			t.Errorf("after %d failures: wait %v, want %v", failures, got, wait)
		}
	}
	if got := backoff(500); got != 24*time.Hour {
		t.Errorf("after many failures: %v", got)
	}
}

func TestAFailureHoldsBackAutomaticOrdersOnly(t *testing.T) {
	env := setup(t, nil)
	key := "zone:example.org"
	if ok, _, _ := store.mayOrder(key, pair, false, env.now); !ok {
		t.Fatal("a certificate nothing is known about may be ordered")
	}

	fail(key, env.now, FailDNSCredentials, 0)
	ok, why, until := store.mayOrder(key, pair, false, env.now.Add(59*time.Minute))
	if ok || why != waitBackoff || !until.Equal(env.now.Add(time.Hour)) {
		t.Errorf("59 minutes after a failure: allowed %v, %q until %v", ok, why, until)
	}
	if ok, _, _ := store.mayOrder(key, pair, false, env.now.Add(61*time.Minute)); !ok {
		t.Error("an hour after the first failure the order may be tried again")
	}
	// an admin's order does not wait
	if ok, _, _ := store.mayOrder(key, pair, true, env.now.Add(time.Minute)); !ok {
		t.Error("a forced order must not wait for the back-off")
	}
	store.force(key)
	if ok, _, _ := store.mayOrder(key, pair, false, env.now.Add(time.Minute)); !ok {
		t.Error("\"Renew now\" lifts the back-off for the next order")
	}
	store.unforce(key)

	// the second failure in a row doubles the wait
	second := env.now.Add(2 * time.Hour)
	fail(key, second, FailDNSCredentials, 0)
	if ok, _, until := store.mayOrder(key, pair, false, second.Add(90*time.Minute)); ok || !until.Equal(second.Add(2*time.Hour)) {
		t.Errorf("after the second failure the wait is two hours, until %v", until)
	}

	// a success forgets the failures
	succeed(key, second.Add(3*time.Hour), pair)
	state := store.get(key)
	if state.Failures != 0 || state.WaitUntil != 0 || !state.LastOK || state.LastCode != "" {
		t.Errorf("after a success: %+v", state)
	}
}

func TestTheCAsRetryAfterStopsEveryOrder(t *testing.T) {
	env := setup(t, nil)
	key := "zone:example.org"
	fail(key, env.now, FailRateLimited, 36*time.Hour)
	for _, force := range []bool{false, true} {
		ok, why, until := store.mayOrder(key, pair, force, env.now.Add(35*time.Hour))
		if ok || why != waitRateLimited || !until.Equal(env.now.Add(36*time.Hour)) {
			t.Errorf("force=%v: allowed %v, %q until %v", force, ok, why, until)
		}
	}
	if ok, _, _ := store.mayOrder(key, pair, true, env.now.Add(37*time.Hour)); !ok {
		t.Error("after the CA's wait the order may go")
	}

	// a rate limit without a time waits an hour; an absurd time is cut at a week
	fail("zone:a.example", env.now, FailRateLimited, 0)
	if _, _, until := store.mayOrder("zone:a.example", pair, true, env.now); !until.Equal(env.now.Add(time.Hour)) {
		t.Errorf("no Retry-After: until %v", until)
	}
	fail("zone:b.example", env.now, FailRateLimited, 400*24*time.Hour)
	if _, _, until := store.mayOrder("zone:b.example", pair, true, env.now); !until.Equal(env.now.Add(7 * 24 * time.Hour)) {
		t.Errorf("a Retry-After of a year: until %v", until)
	}
}

func TestOrdersAreBoundedPerDayWeekAndNames(t *testing.T) {
	env := setup(t, nil)
	key := "zone:example.org"

	// ten orders in a day, whoever asks
	for i := 0; i < maxOrdersPerDay; i++ {
		if ok, why, _ := store.mayOrder(key, []string{"example.org", string(rune('a'+i)) + ".b.example.org"}, true, env.now); !ok {
			t.Fatalf("order %d refused: %s", i+1, why)
		}
		store.started(key, env.now.Add(time.Duration(i)*time.Minute))
	}
	ok, why, until := store.mayOrder(key, pair, true, env.now.Add(time.Hour))
	if ok || why != waitDaily || !until.Equal(env.now.Add(24*time.Hour)) {
		t.Errorf("the eleventh order of the day: allowed %v, %q until %v", ok, why, until)
	}
	if ok, _, _ := store.mayOrder(key, pair, false, env.now.Add(24*time.Hour+time.Minute)); !ok {
		t.Error("a day later an order may go again")
	}
	// another certificate is not affected
	if ok, _, _ := store.mayOrder("zone:example.com", pair, false, env.now.Add(time.Hour)); !ok {
		t.Error("the bound is per certificate")
	}

	// three certificates for the same names in a week: the fourth waits,
	// a different set of names does not
	key = "zone:example.net"
	names := []string{"example.net", "*.example.net"}
	for i := 0; i < maxSameNamesPerWeek; i++ {
		succeed(key, env.now.Add(time.Duration(i)*25*time.Hour), names)
	}
	at := env.now.Add(4 * 24 * time.Hour)
	ok, why, until = store.mayOrder(key, []string{"*.example.net", "example.net"}, true, at)
	if ok || why != waitSameNames || !until.Equal(env.now.Add(7*24*time.Hour)) {
		t.Errorf("the fourth certificate for the same names: allowed %v, %q until %v", ok, why, until)
	}
	if ok, why, _ := store.mayOrder(key, append([]string{"a.b.example.net"}, names...), false, at); !ok {
		t.Errorf("an added name is another certificate to the CA: %s", why)
	}
	if ok, _, _ := store.mayOrder(key, names, false, env.now.Add(7*24*time.Hour+time.Minute)); !ok {
		t.Error("a week after the first of the three the same names may be ordered again")
	}

	// thirty orders in a week
	key = "zone:busy.example"
	for i := 0; i < maxOrdersPerWeek; i++ {
		store.started(key, env.now.Add(time.Duration(i)*5*time.Hour))
	}
	if ok, why, _ := store.mayOrder(key, pair, true, env.now.Add(6*24*time.Hour+12*time.Hour)); ok || why != waitWeekly {
		t.Errorf("the thirty-first order of the week: allowed %v, %q", ok, why)
	}
}

func TestWhatIsRememberedSurvivesARestart(t *testing.T) {
	env := setup(t, nil)
	key := "zone:example.org"
	fail(key, env.now, FailDNSCredentials, 0)
	store.force(key)

	info, err := os.Stat(statePath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("the state file has mode %v, want 0600", info.Mode().Perm())
	}
	content, _ := os.ReadFile(statePath())
	if len(content) == 0 || string(content[0]) != "{" {
		t.Fatalf("the state file is not JSON: %q", content)
	}

	store.reset() // what a restart does: memory gone, the file stays
	if ok, why, _ := store.mayOrder(key, pair, false, env.now.Add(10*time.Minute)); ok || why != waitBackoff {
		t.Errorf("after a restart the failure is forgotten: allowed %v, %q", ok, why)
	}
	state := store.get(key)
	if state.LastCode != FailDNSCredentials || state.Failures != 1 || len(state.Attempts) != 1 {
		t.Errorf("after a restart: %+v", state)
	}
	if store.isForced(key) {
		t.Error("\"Renew now\" is a request for now: it is not kept across a restart")
	}

	// a file that cannot be read starts over instead of stopping the server
	os.WriteFile(statePath(), []byte("{not json"), 0600)
	store.reset()
	if ok, _, _ := store.mayOrder(key, pair, false, env.now); !ok {
		t.Error("an unreadable state file must not block orders")
	}
	succeed(key, env.now, pair)
	store.reset()
	if !store.get(key).LastOK {
		t.Error("the state file is written again after it was unreadable")
	}
}

func TestOldEntriesAreDropped(t *testing.T) {
	env := setup(t, nil)
	fail("zone:gone.example", env.now, FailOther, 0)
	later := env.now.Add(forgetAfter + 24*time.Hour)
	store.started("zone:example.org", later)
	store.reset()
	if state := store.get("zone:gone.example"); state.LastAt != 0 {
		t.Errorf("a certificate nothing happened to for months is still remembered: %+v", state)
	}
	if got := len(store.get("zone:example.org").Attempts); got != 1 {
		t.Errorf("the recent one is kept, attempts %d", got)
	}
}
