package user

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/pquerna/otp/totp"
)

// Fork addition: tests of the limit on wrong two-factor codes (fork_mfa_limit.go).

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)}
}

func TestMFALimiterLocksAfterFiveWrongCodes(t *testing.T) {
	clock := newFakeClock()
	l := newMFAAttemptLimiter(clock.now)

	for i := 1; i <= mfaMaxFailures; i++ {
		if ok, _ := l.begin("alice"); !ok {
			t.Fatalf("attempt %d refused, the first %d must be let through", i, mfaMaxFailures)
		}
		clock.advance(time.Second)
	}

	ok, wait := l.begin("alice")
	if ok {
		t.Fatalf("attempt %d was let through", mfaMaxFailures+1)
	}
	if wait <= mfaLockDuration-time.Minute || wait > mfaLockDuration {
		t.Fatalf("retry after %s, want about %s", wait, mfaLockDuration)
	}

	// refused attempts do not push the end of the lock further away
	clock.advance(mfaLockDuration - 2*time.Second)
	if ok, wait := l.begin("alice"); ok || wait > 2*time.Second {
		t.Fatalf("just before the end of the lock: allowed=%v wait=%s", ok, wait)
	}

	// once it is over the count starts again from nothing
	clock.advance(2 * time.Second)
	for i := 1; i <= mfaMaxFailures; i++ {
		if ok, _ := l.begin("alice"); !ok {
			t.Fatalf("attempt %d after the lock was refused", i)
		}
	}
	if ok, _ := l.begin("alice"); ok {
		t.Fatal("a second series of wrong codes did not lock the account again")
	}
}

func TestMFALimiterSuccessResetsTheCount(t *testing.T) {
	clock := newFakeClock()
	l := newMFAAttemptLimiter(clock.now)

	for round := 0; round < 3; round++ {
		for i := 0; i < mfaMaxFailures-1; i++ {
			if ok, _ := l.begin("alice"); !ok {
				t.Fatalf("round %d: wrong code %d refused", round, i+1)
			}
		}
		// the fifth one is right
		if ok, _ := l.begin("alice"); !ok {
			t.Fatalf("round %d: the right code was refused", round)
		}
		l.succeeded("alice")
	}
	if len(l.accounts) != 0 {
		t.Fatalf("a successful verification left %d counter(s) behind", len(l.accounts))
	}
}

func TestMFALimiterOnlyCountsTheWindow(t *testing.T) {
	clock := newFakeClock()
	l := newMFAAttemptLimiter(clock.now)

	// four wrong codes, then four more once the first ones are older than the window: never five at once
	for round := 0; round < 4; round++ {
		for i := 0; i < mfaMaxFailures-1; i++ {
			if ok, _ := l.begin("alice"); !ok {
				t.Fatalf("round %d: attempt %d refused although the earlier ones are too old to count", round, i+1)
			}
		}
		clock.advance(mfaFailureWindow + time.Second)
	}
}

func TestMFALimiterIsPerAccount(t *testing.T) {
	clock := newFakeClock()
	l := newMFAAttemptLimiter(clock.now)

	for i := 0; i < mfaMaxFailures; i++ {
		l.begin("alice")
	}
	if ok, _ := l.begin("alice"); ok {
		t.Fatal("alice is not locked")
	}
	if ok, _ := l.begin("bob"); !ok {
		t.Fatal("bob is locked by alice's wrong codes")
	}
}

func TestMFALimiterForgetsOldCounters(t *testing.T) {
	clock := newFakeClock()
	l := newMFAAttemptLimiter(clock.now)

	for _, name := range []string{"a", "b", "c", "d"} {
		l.begin(name)
	}
	for i := 0; i < mfaMaxFailures; i++ {
		l.begin("locked")
	}
	if len(l.accounts) != 5 {
		t.Fatalf("%d counters, want 5", len(l.accounts))
	}

	// the window is over for the four, the lock still holds for the fifth
	clock.advance(mfaFailureWindow + time.Second)
	l.begin("someone-else")
	if _, kept := l.accounts["locked"]; !kept || len(l.accounts) != 2 {
		t.Fatalf("after the window: %d counters (locked kept: %v), want the locked one and the new one", len(l.accounts), kept)
	}

	clock.advance(mfaLockDuration)
	l.succeeded("nobody")
	l.begin("last")
	if len(l.accounts) != 1 {
		t.Fatalf("%d counters left after everything expired, want only the new one", len(l.accounts))
	}
}

func TestMFALimiterHoldsUnderParallelRequests(t *testing.T) {
	l := newMFAAttemptLimiter(time.Now)

	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := l.begin("alice"); ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if allowed != mfaMaxFailures {
		t.Fatalf("%d parallel attempts were let through, want %d", allowed, mfaMaxFailures)
	}
}

func TestMFARetryMessage(t *testing.T) {
	for wait, want := range map[time.Duration]string{
		15 * time.Minute:               "15 minutes",
		14*time.Minute + time.Second:   "15 minutes",
		61 * time.Second:               "2 minutes",
		time.Minute:                    "1 minute",
		time.Second:                    "1 minute",
		59*time.Second + 999999999:     "1 minute",
		9*time.Minute + 30*time.Second: "10 minutes",
	} {
		if got := mfaRetryMessage(wait); !strings.HasSuffix(got, " "+want+".") {
			t.Errorf("mfaRetryMessage(%s) = %q, want it to end with %q", wait, got, want)
		}
	}
}

// ---- the handlers, against a real user store ----

func mfaTestSetup(t *testing.T) *fakeClock {
	t.Helper()

	prevFolder := utils.CONFIGFOLDER
	utils.CONFIGFOLDER = t.TempDir() + "/"
	if err := utils.InitStore(); err != nil {
		t.Fatal("InitStore:", err)
	}

	prevConfig := utils.GetBaseMainConfig()
	pub, priv := utils.GenerateEd25519Certificates()
	config := utils.Config{}
	config.HTTPConfig.Hostname = "localhost"
	config.HTTPConfig.AuthPublicKey = pub
	config.HTTPConfig.AuthPrivateKey = priv
	utils.LoadBaseMainConfig(config)

	clock := newFakeClock()
	prevLimiter := mfaAttempts
	mfaAttempts = newMFAAttemptLimiter(clock.now)

	t.Cleanup(func() {
		mfaAttempts = prevLimiter
		utils.LoadBaseMainConfig(prevConfig)
		utils.CloseStore()
		utils.CONFIGFOLDER = prevFolder
	})
	return clock
}

func mfaTestUser(t *testing.T, nickname string, verified bool) string {
	t.Helper()
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "test", AccountName: nickname})
	if err != nil {
		t.Fatal(err)
	}
	if err := utils.CreateUser(utils.User{
		Nickname:       nickname,
		Password:       "not-used",
		Role:           utils.USER,
		MFAKey:         key.Secret(),
		Was2FAVerified: verified,
	}); err != nil {
		t.Fatal("CreateUser:", err)
	}
	return key.Secret()
}

// mfaRequest builds a request as the token middleware hands it to the handler
func mfaRequest(method string, body string, ctx utils.AuthContext) *http.Request {
	req := httptest.NewRequest(method, "http://localhost/cosmos/api/mfa", strings.NewReader(body))
	return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &ctx))
}

// a session after the password, before the code
func halfLoggedIn(nickname string) utils.AuthContext {
	return utils.AuthContext{Nickname: nickname, Role: utils.USER, UserRole: utils.USER,
		Permissions: utils.GetRolePermissions(utils.USER), MFAState: 1}
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var res utils.HTTPErrorResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("response is not JSON: %q", w.Body.String())
	}
	return res.Code
}

func wrongCode(t *testing.T, secret string) string {
	t.Helper()
	// none of the codes the validator accepts around now
	accepted := map[string]bool{}
	for _, d := range []time.Duration{-90 * time.Second, -60 * time.Second, -30 * time.Second, 0, 30 * time.Second, 60 * time.Second, 90 * time.Second} {
		code, err := totp.GenerateCode(secret, time.Now().Add(d))
		if err != nil {
			t.Fatal(err)
		}
		accepted[code] = true
	}
	for _, candidate := range []string{"000000", "111111", "222222", "333333", "444444", "555555", "666666", "777777", "888888"} {
		if !accepted[candidate] {
			return candidate
		}
	}
	t.Fatal("no wrong code found")
	return ""
}

func rightCode(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func postCode(nickname string, code string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	Check2FA(w, mfaRequest("POST", `{"token":"`+code+`"}`, halfLoggedIn(nickname)))
	return w
}

func TestCheck2FALocksTheAccountAfterFiveWrongCodes(t *testing.T) {
	clock := mfaTestSetup(t)
	secret := mfaTestUser(t, "alice", true)
	bobSecret := mfaTestUser(t, "bob", true)
	wrong := wrongCode(t, secret)

	// the wrong codes themselves are answered as they always were
	for i := 1; i <= mfaMaxFailures; i++ {
		w := postCode("alice", wrong)
		if w.Code != http.StatusInternalServerError || errorCode(t, w) != "2FA005" {
			t.Fatalf("wrong code %d: %d %s, want 500 2FA005", i, w.Code, w.Body.String())
		}
	}

	// from the sixth on nothing is checked any more, not even the right code
	w := postCode("alice", rightCode(t, secret))
	if w.Code != http.StatusTooManyRequests || errorCode(t, w) != "2FA006" {
		t.Fatalf("right code while locked: %d %s, want 429 2FA006", w.Code, w.Body.String())
	}
	if w.Header().Get("Retry-After") != "900" {
		t.Errorf("Retry-After = %q, want 900", w.Header().Get("Retry-After"))
	}
	if len(w.Header().Values("Set-Cookie")) != 0 {
		t.Fatal("a session cookie was sent while the account is locked")
	}
	if strings.Contains(w.Body.String(), secret) {
		t.Fatal("the answer contains the secret")
	}

	// by account, not by source: another user is not affected
	if w := postCode("bob", rightCode(t, bobSecret)); w.Code != http.StatusOK {
		t.Fatalf("bob: %d %s, want 200", w.Code, w.Body.String())
	}

	// still locked a minute before the end
	clock.advance(mfaLockDuration - time.Minute)
	if w := postCode("alice", rightCode(t, secret)); w.Code != http.StatusTooManyRequests {
		t.Fatalf("one minute before the end of the lock: %d, want 429", w.Code)
	}

	// and usable again after it
	clock.advance(time.Minute)
	w = postCode("alice", rightCode(t, secret))
	if w.Code != http.StatusOK {
		t.Fatalf("right code after the lock: %d %s, want 200", w.Code, w.Body.String())
	}
	if len(w.Header().Values("Set-Cookie")) == 0 {
		t.Fatal("no session cookie after the right code")
	}
}

func TestCheck2FARightCodeResetsTheCount(t *testing.T) {
	mfaTestSetup(t)
	secret := mfaTestUser(t, "alice", true)
	wrong := wrongCode(t, secret)

	for round := 0; round < 3; round++ {
		for i := 0; i < mfaMaxFailures-1; i++ {
			if w := postCode("alice", wrong); w.Code != http.StatusInternalServerError {
				t.Fatalf("round %d, wrong code %d: %d, want 500", round, i+1, w.Code)
			}
		}
		if w := postCode("alice", rightCode(t, secret)); w.Code != http.StatusOK {
			t.Fatalf("round %d: right code after four wrong ones: %d %s, want 200", round, w.Code, w.Body.String())
		}
	}
}

func TestCheck2FADoesNotCountRequestsWithoutACodeToCheck(t *testing.T) {
	mfaTestSetup(t)
	secret := mfaTestUser(t, "alice", true)

	// a body that cannot be read is refused before any code is compared
	for i := 0; i < 20; i++ {
		w := httptest.NewRecorder()
		Check2FA(w, mfaRequest("POST", `not json`, halfLoggedIn("alice")))
		if w.Code != http.StatusInternalServerError || errorCode(t, w) != "2FA001" {
			t.Fatalf("unreadable body: %d %s, want 500 2FA001", w.Code, w.Body.String())
		}
	}
	// nobody logged in: refused as before, nothing counted
	w := httptest.NewRecorder()
	Check2FA(w, mfaRequest("POST", `{"token":"000000"}`, utils.AuthContext{}))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d, want 401", w.Code)
	}

	if w := postCode("alice", rightCode(t, secret)); w.Code != http.StatusOK {
		t.Fatalf("right code after requests that compared nothing: %d %s, want 200", w.Code, w.Body.String())
	}
}
