package user

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: wrong two-factor codes are limited per account.
//
// Upstream leaves the code check to the SmartShield of the login router:
// about twenty failed attempts an hour before it strikes, and no limit at all
// for a session whose role can read the admin pages (the shield's privileged
// group: it looks at the role, not at whether the code was given) or that
// comes from a whitelisted address. Whoever has the password of an
// administrator could try the million possible codes without being stopped.
//
// The rule here: mfaMaxFailures wrong codes within mfaFailureWindow close the
// code check of that account for mfaLockDuration, the right code included; a
// right code before that clears the count. Counting is by nickname, taken from
// the session, so only existing accounts ever get a counter and nothing is
// said about an account to someone who is not logged into it.
//
// The counters are kept in memory only (a restart forgets them) and are
// dropped as soon as they no longer matter, see prune.

const (
	mfaMaxFailures   = 5
	mfaFailureWindow = 10 * time.Minute
	mfaLockDuration  = 15 * time.Minute
)

type mfaAccountAttempts struct {
	// attempts of the last mfaFailureWindow that were not followed by a right code
	failures    []time.Time
	lockedUntil time.Time
}

type mfaAttemptLimiter struct {
	mu       sync.Mutex
	accounts map[string]*mfaAccountAttempts
	now      func() time.Time
}

func newMFAAttemptLimiter(now func() time.Time) *mfaAttemptLimiter {
	return &mfaAttemptLimiter{accounts: map[string]*mfaAccountAttempts{}, now: now}
}

var mfaAttempts = newMFAAttemptLimiter(time.Now)

// begin is called before a code is compared. It answers whether the account
// may try one, and if not for how long. The attempt is counted as a wrong one
// right away, under the lock, so that requests sent in parallel cannot all
// slip in before the first of them is known to be wrong; succeeded takes it
// back.
func (l *mfaAttemptLimiter) begin(nickname string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.prune(now)

	account := l.accounts[nickname]
	if account == nil {
		account = &mfaAccountAttempts{}
		l.accounts[nickname] = account
	}
	if now.Before(account.lockedUntil) {
		return false, account.lockedUntil.Sub(now)
	}

	account.failures = append(account.failures, now)
	if len(account.failures) >= mfaMaxFailures {
		account.lockedUntil = now.Add(mfaLockDuration)
	}
	return true, 0
}

// succeeded forgets the wrong codes of an account: its code was right
func (l *mfaAttemptLimiter) succeeded(nickname string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.accounts, nickname)
	l.prune(l.now())
}

// prune drops what is too old to count: the attempts older than the window,
// then every account left with neither an attempt nor a lock. The map holds
// at most the accounts that gave a wrong code in the last mfaLockDuration.
// Called with the lock held.
func (l *mfaAttemptLimiter) prune(now time.Time) {
	oldest := now.Add(-mfaFailureWindow)
	for nickname, account := range l.accounts {
		kept := account.failures[:0]
		for _, at := range account.failures {
			if at.After(oldest) {
				kept = append(kept, at)
			}
		}
		account.failures = kept
		if len(kept) == 0 && !now.Before(account.lockedUntil) {
			delete(l.accounts, nickname)
		}
	}
}

// mfaRetryMessage is what a locked out user is told, in whole minutes rounded up
func mfaRetryMessage(wait time.Duration) string {
	minutes := int((wait + time.Minute - 1) / time.Minute)
	if minutes <= 1 {
		return "Too many wrong codes. Try again in 1 minute."
	}
	return fmt.Sprintf("Too many wrong codes. Try again in %d minutes.", minutes)
}

// mfaAttemptRefused is the hook of Check2FA, called once there is a code to
// compare. It returns true, with the answer written (429), when the account
// gave too many wrong codes.
func mfaAttemptRefused(w http.ResponseWriter, nickname string) bool {
	allowed, wait := mfaAttempts.begin(nickname)
	if allowed {
		return false
	}

	seconds := int((wait + time.Second - 1) / time.Second)
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	utils.Error("2FA: User "+nickname+" gave too many wrong codes, the code check is closed for this account", nil)
	utils.HTTPError(w, mfaRetryMessage(wait), http.StatusTooManyRequests, "2FA006")
	return true
}
