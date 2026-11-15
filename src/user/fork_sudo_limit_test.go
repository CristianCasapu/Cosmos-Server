package user

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
	"golang.org/x/crypto/bcrypt"
)

// Fork addition: tests of fork_sudo_limit.go.

const sudoTestPassword = "Right-Passw0rd!"

func sudoTestSetup(t *testing.T) *fakeClock {
	t.Helper()
	clock := mfaTestSetup(t)
	prev := sudoAttempts
	sudoAttempts = newMFAAttemptLimiter(clock.now)
	t.Cleanup(func() { sudoAttempts = prev })
	hash, err := bcrypt.GenerateFromPassword([]byte(sudoTestPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := utils.CreateUser(utils.User{Nickname: "boss", Password: string(hash), Role: utils.ADMIN}); err != nil {
		t.Fatal(err)
	}
	return clock
}

func sudoAttempt(password string) int {
	w := httptest.NewRecorder()
	req := userRequest("POST", "boss", `{"password":"`+password+`"}`, sessionOf("boss", utils.ADMIN, false))
	req.Host = "localhost"
	UserSudo(w, req)
	if w.Code == http.StatusOK && len(w.Result().Cookies()) == 0 {
		return http.StatusInternalServerError // the confirmation gives a new session cookie
	}
	return w.Code
}

func TestWrongSudoPasswordsCloseTheConfirmationForAWhile(t *testing.T) {
	clock := sudoTestSetup(t)
	for i := 0; i < mfaMaxFailures; i++ {
		if code := sudoAttempt("Wrong-Passw0rd!"); code == http.StatusOK || code == http.StatusTooManyRequests {
			t.Fatalf("wrong password %d: %d", i+1, code)
		}
	}
	if code := sudoAttempt(sudoTestPassword); code != http.StatusTooManyRequests {
		t.Errorf("the right password after %d wrong ones: %d, want 429", mfaMaxFailures, code)
	}
	clock.advance(mfaLockDuration + time.Second)
	if code := sudoAttempt(sudoTestPassword); code != http.StatusOK {
		t.Errorf("the right password once the lock is over: %d, want 200", code)
	}
}

func TestARightSudoPasswordClearsTheCount(t *testing.T) {
	sudoTestSetup(t)
	for round := 0; round < 2; round++ {
		for i := 0; i < mfaMaxFailures-1; i++ {
			sudoAttempt("Wrong-Passw0rd!")
		}
		if code := sudoAttempt(sudoTestPassword); code != http.StatusOK {
			t.Fatalf("round %d: the right password after %d wrong ones: %d, want 200", round, mfaMaxFailures-1, code)
		}
	}
}
