package user

import (
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: tests of the reporting of mails that could not be sent
// (fork_mail_report.go).

// resetHarness replaces the mail server, the database and the notifications
// by recorders, and runs the background work at once.
type resetHarness struct {
	sendErr  error
	saveErr  error
	sent     []string // links mailed
	savedKey []string
	reports  []string // message and reason of what the administrators were told
	events   int
	clock    *fakeClock
}

func newResetHarness(t *testing.T) *resetHarness {
	t.Helper()
	h := &resetHarness{clock: newFakeClock()}

	oldSend, oldSave, oldReport, oldEvent, oldBackground, oldNow := sendResetMail, saveResetKey, reportToAdmins, recordResetEvent, inBackground, resetNow
	t.Cleanup(func() {
		sendResetMail, saveResetKey, reportToAdmins, recordResetEvent, inBackground, resetNow = oldSend, oldSave, oldReport, oldEvent, oldBackground, oldNow
	})

	resetLock.Lock()
	resetFailureNotice = map[string]time.Time{}
	resetMailSent = map[string]time.Time{}
	resetLock.Unlock()

	sendResetMail = func(nickname string, email string, link string) error {
		h.sent = append(h.sent, link)
		return h.sendErr
	}
	saveResetKey = func(nickname string, fields map[string]interface{}) error {
		key, _ := fields["RegisterKey"].(string)
		h.savedKey = append(h.savedKey, key)
		return h.saveErr
	}
	reportToAdmins = func(message string, err error) {
		h.reports = append(h.reports, message+" : "+err.Error())
	}
	recordResetEvent = func(string, string, string, string, map[string]interface{}) { h.events++ }
	inBackground = func(work func()) { work() }
	resetNow = h.clock.now
	return h
}

// ask sends one "forgot password" request through and returns what the
// caller sees.
func (h *resetHarness) ask(user utils.User, lookupErr error) (int, string) {
	recorder := httptest.NewRecorder()
	if !answerPasswordReset(recorder, user.Nickname, user, lookupErr) {
		return 0, "not answered"
	}
	return recorder.Code, recorder.Body.String()
}

var bob = utils.User{Nickname: "bob", Email: "bob@example.com"}

func TestPasswordResetAnswersTheSameWhateverHappens(t *testing.T) {
	type outcome struct {
		code int
		body string
	}
	seen := map[string]outcome{}

	h := newResetHarness(t)
	code, body := h.ask(bob, nil)
	seen["mail sent"] = outcome{code, body}

	h = newResetHarness(t)
	h.sendErr = errors.New("dial tcp 10.0.0.5:587: connect: connection refused")
	code, body = h.ask(bob, nil)
	seen["mail failed"] = outcome{code, body}

	h = newResetHarness(t)
	h.saveErr = errors.New("database is locked")
	code, body = h.ask(bob, nil)
	seen["link not saved"] = outcome{code, body}

	h = newResetHarness(t)
	code, body = h.ask(utils.User{}, utils.ErrNotFound)
	seen["unknown account"] = outcome{code, body}

	h = newResetHarness(t)
	code, body = h.ask(utils.User{}, errors.New("database is locked"))
	seen["lookup failed"] = outcome{code, body}

	want := seen["mail sent"]
	if want.code != 200 || !strings.Contains(want.body, `"status":"OK"`) {
		t.Fatalf("a reset that worked must answer OK, got %d %s", want.code, want.body)
	}
	for name, got := range seen {
		if got != want {
			t.Errorf("%s answers %d %q, a sent mail answers %d %q: the difference tells a stranger about the account", name, got.code, got.body, want.code, want.body)
		}
	}
}

func TestPasswordResetMailsTheLinkItSaved(t *testing.T) {
	h := newResetHarness(t)
	h.ask(bob, nil)

	if len(h.savedKey) != 1 || len(h.savedKey[0]) != 48 {
		t.Fatalf("a new key must be saved for the account, got %v", h.savedKey)
	}
	if len(h.sent) != 1 || !strings.Contains(h.sent[0], "nickname=bob&key="+h.savedKey[0]) {
		t.Errorf("the link mailed must carry the key that was saved, got %v", h.sent)
	}
	if h.events != 1 || len(h.reports) != 0 {
		t.Errorf("a mail that was sent is recorded and not reported as a failure: %d events, reports %v", h.events, h.reports)
	}
}

func TestPasswordResetDoesNothingForAnUnknownAccount(t *testing.T) {
	h := newResetHarness(t)
	h.ask(utils.User{}, utils.ErrNotFound)
	h.ask(utils.User{Nickname: "nomail"}, nil) // an account without an email cannot be written to

	if len(h.savedKey) != 0 || len(h.sent) != 0 || len(h.reports) != 0 {
		t.Errorf("nothing may be saved, sent or reported: saved %v, sent %v, reports %v", h.savedKey, h.sent, h.reports)
	}
}

func TestPasswordResetFailureGoesToTheAdministrators(t *testing.T) {
	h := newResetHarness(t)
	h.sendErr = errors.New("535 5.7.8 Username and Password\nnot accepted")
	h.ask(bob, nil)

	if len(h.reports) != 1 {
		t.Fatalf("the administrators must be told that the mail was not sent, got %v", h.reports)
	}
	report := h.reports[0]
	if !strings.Contains(report, "bob") || !strings.Contains(report, "535 5.7.8 Username and Password not accepted") {
		t.Errorf("the report must name the account and the reason, got %q", report)
	}
	if strings.Contains(report, h.savedKey[0]) {
		t.Error("the report must not carry the reset key")
	}
	if h.events != 0 {
		t.Error("a mail that was not sent must not be recorded as sent")
	}

	// the form is open to anyone: repeating it must not fill the notifications
	for i := 0; i < 5; i++ {
		h.clock.advance(2 * time.Minute)
		h.ask(bob, nil)
	}
	if len(h.reports) != 1 {
		t.Errorf("one notification per account and hour, got %d", len(h.reports))
	}
	h.clock.advance(time.Hour)
	h.ask(bob, nil)
	if len(h.reports) != 2 {
		t.Errorf("after an hour a failure is reported again, got %d", len(h.reports))
	}
}

func TestPasswordResetLinkNotSavedIsReported(t *testing.T) {
	h := newResetHarness(t)
	h.saveErr = errors.New("database is locked")
	h.ask(bob, nil)

	if len(h.sent) != 0 {
		t.Error("a link that could not be saved must not be mailed")
	}
	if len(h.reports) != 1 || !strings.Contains(h.reports[0], "database is locked") {
		t.Errorf("the administrators must be told, got %v", h.reports)
	}
}

func TestPasswordResetSendsOneLinkAMinute(t *testing.T) {
	h := newResetHarness(t)
	for i := 0; i < 10; i++ {
		h.ask(bob, nil)
	}
	if len(h.sent) != 1 {
		t.Fatalf("repeating the request must not send a mail each time, got %d", len(h.sent))
	}

	h.ask(utils.User{Nickname: "alice", Email: "alice@example.com"}, nil)
	if len(h.sent) != 2 {
		t.Error("another account is not held back")
	}

	h.clock.advance(resetMailEvery)
	h.ask(bob, nil)
	if len(h.sent) != 3 {
		t.Errorf("a minute later the account can ask again, got %d", len(h.sent))
	}
}

func TestMailOutcome(t *testing.T) {
	if sent, reason := mailOutcome(nil); !sent || reason != "" {
		t.Errorf("no error is a sent mail, got %v %q", sent, reason)
	}
	sent, reason := mailOutcome(errors.New("dial tcp 10.0.0.5:587:\n connect: connection refused"))
	if sent || reason != "dial tcp 10.0.0.5:587: connect: connection refused" {
		t.Errorf("a failed mail is not sent and says why on one line, got %v %q", sent, reason)
	}
	_, reason = mailOutcome(errors.New("a" + strings.Repeat("é", 1000)))
	if len(reason) > maxMailReason+len("…") || !strings.HasSuffix(reason, "…") || strings.ContainsRune(reason, '�') {
		t.Errorf("a long reason must be cut cleanly, got %d bytes", len(reason))
	}
}

// The two handlers must go through the functions above: an upstream merge
// could put "sent, whatever happened" back without any other test noticing.
func TestMailHandlersReportFailures(t *testing.T) {
	invite, err := os.ReadFile("resend.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(invite), "emailWasSent = true") {
		t.Error("the invitation must not say the mail was sent without looking at the result")
	}
	if !strings.Contains(string(invite), "emailWasSent, emailError = mailOutcome(errEm)") || !strings.Contains(string(invite), `"emailError": emailError`) {
		t.Error("the invitation must tell the administrator when the mail was not sent, and why")
	}

	reset, err := os.ReadFile("password_reset.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(reset)
	hook := strings.Index(text, "answerPasswordReset(w, nickname, user, err)")
	firstDifference := strings.Index(text, `"PasswordReset: Error while finding user"`)
	if hook < 0 || firstDifference < 0 || hook > firstDifference {
		t.Error("the reset form must give its one answer before anything that depends on the account")
	}
}
