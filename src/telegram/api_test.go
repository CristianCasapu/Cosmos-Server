package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// apiRequest builds a request from nobody ("none"), from someone who may
// only read the configuration ("reader"), from an admin who has not
// confirmed their password ("admin"), from one who has ("sudo"), or from an
// API token that may only read ("token").
func apiRequest(method string, path string, who string, body string) *http.Request {
	req := httptest.NewRequest(method, "/cosmos"+path, strings.NewReader(body))
	var auth *utils.AuthContext
	switch who {
	case "none":
		return req
	case "token":
		auth = &utils.AuthContext{APIToken: &utils.APITokenContext{Permissions: []utils.Permission{utils.PERM_CONFIGURATION_READ}}}
	default:
		auth = &utils.AuthContext{Nickname: "someone", Permissions: []utils.Permission{utils.PERM_CONFIGURATION_READ}, IsSudoed: who == "sudo"}
		if who != "reader" {
			auth.Permissions = append(auth.Permissions, utils.PERM_ADMIN)
		}
	}
	return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
}

func call(handler http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// useConfig makes a configuration the one in use for the test.
func useConfig(t *testing.T, config utils.Config) {
	t.Helper()
	previous := utils.GetBaseMainConfig()
	utils.LoadBaseMainConfig(config)
	t.Cleanup(func() { utils.LoadBaseMainConfig(previous) })
}

// useFake points the direct sends (test message, farewell) at the fake and
// lets tests follow each other without the pause between test messages.
func useFake(t *testing.T, fake *fakeTelegram) {
	t.Helper()
	previous := directClient
	directClient = fake.client()
	testMu.Lock()
	lastTest = time.Time{}
	testMu.Unlock()
	t.Cleanup(func() { directClient = previous })
}

func testBody(cfg utils.TelegramConfig) string {
	raw, _ := json.Marshal(testRequest{Telegram: cfg})
	return string(raw)
}

func resultOf(t *testing.T, rec *httptest.ResponseRecorder) TestResult {
	t.Helper()
	var answer struct {
		Status string     `json:"status"`
		Data   TestResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil || answer.Status != "OK" {
		t.Fatalf("answer %d %s", rec.Code, rec.Body.String())
	}
	return answer.Data
}

func TestRoutesNeedASessionTheRightMethodAndForTheTestThePassword(t *testing.T) {
	fake := newFakeTelegram(t)
	useFake(t, fake)
	useConfig(t, testConfig())

	routes := []struct {
		path    string
		handler http.HandlerFunc
		method  string
	}{
		{"/api/telegram", StatusRoute, "GET"},
		{"/api/telegram/test", TestRoute, "POST"},
	}
	body := testBody(utils.TelegramConfig{ChatID: testChat})
	for _, route := range routes {
		// nobody: refused whatever the method, before anything is read or sent
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			if rec := call(route.handler, apiRequest(method, route.path, "none", body)); rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s without a session: status %d, want 401", method, route.path, rec.Code)
			}
		}
		// the other methods are not served
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"} {
			if method == route.method {
				continue
			}
			if rec := call(route.handler, apiRequest(method, route.path, "sudo", body)); rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: status %d, want 405", method, route.path, rec.Code)
			}
		}
	}

	// sending takes an admin who confirmed their password
	for who, status := range map[string]int{"reader": http.StatusUnauthorized, "admin": http.StatusUnauthorized, "token": http.StatusForbidden} {
		if rec := call(TestRoute, apiRequest("POST", "/api/telegram/test", who, body)); rec.Code != status {
			t.Errorf("POST /api/telegram/test as %s: status %d, want %d", who, rec.Code, status)
		}
	}
	if got := len(fake.received()); got != 0 {
		t.Fatalf("%d messages were sent by requests that were refused", got)
	}

	// reading the status does not
	for _, who := range []string{"reader", "admin", "sudo", "token"} {
		rec := call(StatusRoute, apiRequest("GET", "/api/telegram", who, ""))
		if rec.Code != http.StatusOK {
			t.Errorf("GET /api/telegram as %s: status %d: %s", who, rec.Code, rec.Body.String())
		}
		// and it holds neither the token nor the chat
		mustNotContain(t, "the status", rec.Body.String(), testToken)
		mustNotContain(t, "the status", rec.Body.String(), testChat)
	}
}

func TestTheTestMessage(t *testing.T) {
	fake := newFakeTelegram(t)
	useFake(t, fake)
	useConfig(t, testConfig())

	// the form's values, the token left empty: the saved one is used
	rec := call(TestRoute, apiRequest("POST", "/api/telegram/test", "sudo", testBody(utils.TelegramConfig{ChatID: "4242", TopicID: 7, ServerName: "Cellar"})))
	if result := resultOf(t, rec); !result.Success || result.Reason != "" || result.Message == "" {
		t.Fatalf("result %+v", result)
	}
	got := fake.received()
	if len(got) != 1 || got[0].path != "/bot"+testToken+"/sendMessage" {
		t.Fatalf("requests %+v", got)
	}
	text, _ := got[0].body["text"].(string)
	if !strings.HasPrefix(text, "Cosmos · Cellar · Test\nThis is a test message") || got[0].body["chat_id"] != float64(4242) || got[0].body["message_thread_id"] != float64(7) {
		t.Errorf("the test message: %s", got[0].raw)
	}
	// nothing of the request is echoed, and nothing of the token
	mustNotContain(t, "the answer", rec.Body.String(), testToken)
	mustNotContain(t, "the answer", rec.Body.String(), "4242")

	// a masked token and a masked chat are the saved ones; a typed token is used as typed
	useFake(t, fake)
	rec = call(TestRoute, apiRequest("POST", "/api/telegram/test", "sudo", testBody(utils.TelegramConfig{BotToken: "***", ChatID: "***"})))
	if !resultOf(t, rec).Success || !strings.Contains(fake.received()[1].raw, `"chat_id":`+testChat) {
		t.Errorf("masked values: %s", rec.Body.String())
	}
	useFake(t, fake)
	other := "987654321:BBotherOtherOtherOtherOtherOtherOther"
	rec = call(TestRoute, apiRequest("POST", "/api/telegram/test", "sudo", testBody(utils.TelegramConfig{BotToken: other, ChatID: testChat})))
	if !resultOf(t, rec).Success || fake.received()[2].path != "/bot"+other+"/sendMessage" {
		t.Errorf("a typed token: %s", rec.Body.String())
	}

	// what Telegram answers comes back in plain words
	for description, reason := range map[string]string{
		"Unauthorized":                                           ReasonWrongToken,
		"Bad Request: chat not found":                            ReasonChatNotFound,
		"Forbidden: bot was blocked by the user":                 ReasonBlocked,
		"Forbidden: bot can't initiate conversation with a user": ReasonNotStarted,
		"Forbidden: bot is not a member of the supergroup chat":  ReasonNotMember,
		"Bad Request: message thread not found":                  ReasonTopicNotFound,
	} {
		status := 400
		switch {
		case strings.HasPrefix(description, "Unauthorized"):
			status = 401
		case strings.HasPrefix(description, "Forbidden"):
			status = 403
		}
		fake.always(refusal(status, description))
		useFake(t, fake)
		rec := call(TestRoute, apiRequest("POST", "/api/telegram/test", "sudo", testBody(utils.TelegramConfig{ChatID: testChat})))
		result := resultOf(t, rec)
		if rec.Code != http.StatusOK || result.Success || result.Reason != reason || len(result.Message) < 20 {
			t.Errorf("%q: status %d, result %+v, want reason %s", description, rec.Code, result, reason)
		}
		mustNotContain(t, "the answer", rec.Body.String(), testToken)
	}
}

func TestTheTestRouteChecksAndBoundsItsInput(t *testing.T) {
	fake := newFakeTelegram(t)
	useFake(t, fake)
	useConfig(t, testConfig())

	bad := map[string]string{
		"not JSON":             `nonsense`,
		"a chat that is none":  testBody(utils.TelegramConfig{ChatID: "https://evil.example"}),
		"a token that is none": testBody(utils.TelegramConfig{BotToken: testToken + "/../getUpdates", ChatID: testChat}),
		"no chat at all":       testBody(utils.TelegramConfig{}),
		"a name of two lines":  testBody(utils.TelegramConfig{ChatID: testChat, ServerName: "a\nb"}),
		// a request that would be a fine one if all of it were read
		"far too much": `{"Telegram":{"ChatID":"` + testChat + `"}` + strings.Repeat(" ", maxTestBody) + `}`,
	}
	for name, body := range bad {
		rec := call(TestRoute, apiRequest("POST", "/api/telegram/test", "sudo", body))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", name, rec.Code, rec.Body.String())
		}
		mustNotContain(t, "the answer", rec.Body.String(), "AAFake")
	}
	if got := len(fake.received()); got != 0 {
		t.Fatalf("%d messages were sent for input that was refused", got)
	}

	// without a saved token and without one in the form there is nothing to send with
	config := testConfig()
	config.Telegram.BotToken = ""
	useConfig(t, config)
	if rec := call(TestRoute, apiRequest("POST", "/api/telegram/test", "sudo", testBody(utils.TelegramConfig{ChatID: testChat}))); rec.Code != http.StatusBadRequest {
		t.Errorf("no token anywhere: status %d", rec.Code)
	}
	useConfig(t, testConfig())

	// one test, then a pause: the route cannot be used to hammer Telegram
	ok := testBody(utils.TelegramConfig{ChatID: testChat})
	if rec := call(TestRoute, apiRequest("POST", "/api/telegram/test", "sudo", ok)); rec.Code != http.StatusOK {
		t.Fatalf("the first test: %d %s", rec.Code, rec.Body.String())
	}
	for i := 0; i < 20; i++ {
		if rec := call(TestRoute, apiRequest("POST", "/api/telegram/test", "sudo", ok)); rec.Code != http.StatusTooManyRequests {
			t.Fatalf("test %d right after another: status %d, want 429", i+2, rec.Code)
		}
	}
	if got := len(fake.received()); got != 1 {
		t.Errorf("%d messages for 21 tests in a row, want 1", got)
	}
}

// The bot token must not show up anywhere but in the request to Telegram:
// not in the log, not in an answer, not in a notification, not in the
// status, whatever goes wrong.
func TestTheTokenStaysInTheRequest(t *testing.T) {
	var logged bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&logged)
	defer log.SetOutput(previousOutput)

	fake := newFakeTelegram(t)
	useConfig(t, testConfig())
	answers := []string{}

	failures := []fakeAnswer{
		refusal(401, "Unauthorized"),
		refusal(404, "Not Found"),
		refusal(400, "Bad Request: chat not found"),
		refusal(403, "Forbidden: bot was blocked by the user"),
		// a server that echoes the request back, as a proxy's error page might
		refusal(400, "Bad Request: cannot handle /bot"+testToken+"/sendMessage for "+testToken),
		refusal(500, "Internal error at https://api.telegram.org/bot"+testToken+"/sendMessage"),
		{status: 502, body: "<html>upstream /bot" + testToken + "/sendMessage failed</html>"},
		{status: 302, body: "", header: map[string]string{"Location": "https://elsewhere.example/bot" + testToken + "/sendMessage"}},
		{status: 429, body: `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 1 for ` + testToken + `","parameters":{"retry_after":1}}`},
	}
	for _, failure := range failures {
		fake.always(failure)
		useFake(t, fake)
		rec := call(TestRoute, apiRequest("POST", "/api/telegram/test", "sudo", testBody(utils.TelegramConfig{ChatID: testChat})))
		answers = append(answers, rec.Body.String())
	}

	// the same through the queue, with the server's own log function and a
	// captured notification
	h := newHarness(t, fake, false, func(h *harness) {
		h.logf = utils.Warn
		h.limits.settingsPause = time.Millisecond // every failure is met, not only the first
		h.limits.criticalBurst = 100
	})
	for i, failure := range failures {
		fake.always(failure)
		before := len(fake.received())
		h.Notify(plainNotice("kind", priorityCritical))
		for deadline := time.Now().Add(5 * time.Second); len(fake.received()) == before && time.Now().Before(deadline); {
			time.Sleep(5 * time.Millisecond)
		}
		if len(fake.received()) == before {
			t.Fatalf("failure %d was never met through the queue", i)
		}
		time.Sleep(30 * time.Millisecond)
		status, _ := json.Marshal(h.Status())
		answers = append(answers, string(status))
	}

	// and when nothing listens at all
	fake.server.Close()
	useFake(t, fake)
	rec := call(TestRoute, apiRequest("POST", "/api/telegram/test", "sudo", testBody(utils.TelegramConfig{ChatID: testChat})))
	answers = append(answers, rec.Body.String())
	if resultOf(t, rec).Success {
		t.Error("a test message to a closed server counted as sent")
	}
	h.Notify(plainNotice("other", priorityCritical))
	time.Sleep(100 * time.Millisecond)
	status, _ := json.Marshal(h.Status())
	answers = append(answers, string(status))
	rec = call(StatusRoute, apiRequest("GET", "/api/telegram", "reader", ""))
	answers = append(answers, rec.Body.String())

	// a refused save names no token either
	cfg := utils.TelegramConfig{Enabled: true, BotToken: testToken + "!", ChatID: testChat}
	if err := ValidateConfig(&cfg); err != nil {
		answers = append(answers, err.Error())
	}

	everything := map[string]string{
		"the log":           logged.String(),
		"the answers":       strings.Join(answers, "\n"),
		"the notifications": strings.Join(h.raisedNow(), "\n"),
	}
	if len(logged.String()) < 100 || len(h.raisedNow()) == 0 {
		t.Fatalf("nothing was logged or raised: the test looks at nothing (%d bytes logged, %d raised)", logged.Len(), len(h.raisedNow()))
	}
	secret := strings.SplitN(testToken, ":", 2)[1]
	for where, text := range everything {
		mustNotContain(t, where, text, testToken)
		mustNotContain(t, where, text, secret)
		mustNotContain(t, where, text, "elsewhere.example")
	}
}
