package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSendIsOneSendMessageRequestWithoutParseMode(t *testing.T) {
	fake := newFakeTelegram(t)
	st := testSettings()
	st.topicID = 42

	c := &composer{}
	c.fixed("Hello ").value("world", maxValue)
	text, entities := c.result()

	if failure := fake.client().send(context.Background(), st, text, entities, true); failure != nil {
		t.Fatalf("send: %v", failure)
	}
	got := fake.received()
	if len(got) != 1 {
		t.Fatalf("%d requests, want 1", len(got))
	}
	req := got[0]
	if req.method != "POST" || req.path != "/bot"+testToken+"/sendMessage" {
		t.Errorf("request %s %s", req.method, req.path)
	}
	if req.body["text"] != "Hello world" {
		t.Errorf("text %q", req.body["text"])
	}
	// the chat is a number in the JSON, the topic and the silence are there
	if !strings.Contains(req.raw, `"chat_id":`+testChat) {
		t.Errorf("chat_id is not the number: %s", req.raw)
	}
	if req.body["message_thread_id"] != float64(42) || req.body["disable_notification"] != true {
		t.Errorf("topic or silence missing: %s", req.raw)
	}
	// never a parse mode, always the preview switched off
	if _, has := req.body["parse_mode"]; has {
		t.Errorf("a parse_mode was sent: %s", req.raw)
	}
	preview, _ := req.body["link_preview_options"].(map[string]interface{})
	if preview["is_disabled"] != true {
		t.Errorf("link previews are not disabled: %s", req.raw)
	}
	if parts := covered(t, text, entitiesOf(t, req)); len(parts) != 1 || parts[0] != "world" {
		t.Errorf("entities cover %q, want the value", parts)
	}
}

func TestSendToAChannelByName(t *testing.T) {
	fake := newFakeTelegram(t)
	st := testSettings()
	st.chatID = "@my_channel"
	if failure := fake.client().send(context.Background(), st, "text", nil, false); failure != nil {
		t.Fatalf("send: %v", failure)
	}
	req := fake.received()[0]
	if req.body["chat_id"] != "@my_channel" {
		t.Errorf("chat_id %v", req.body["chat_id"])
	}
	if _, has := req.body["message_thread_id"]; has {
		t.Errorf("a topic was sent without one being set: %s", req.raw)
	}
	if _, has := req.body["disable_notification"]; has {
		t.Errorf("silence was sent without being asked for: %s", req.raw)
	}
}

func TestAnswersBecomeReasonsInPlainWords(t *testing.T) {
	cases := []struct {
		name     string
		answer   fakeAnswer
		reason   string
		settings bool
		retry    bool
		says     string
	}{
		{"wrong token", refusal(401, "Unauthorized"), ReasonWrongToken, true, false, "BotFather"},
		{"token of no bot", refusal(404, "Not Found"), ReasonWrongToken, true, false, "BotFather"},
		{"chat not found", refusal(400, "Bad Request: chat not found"), ReasonChatNotFound, true, false, "press Start"},
		{"never started", refusal(403, "Forbidden: bot can't initiate conversation with a user"), ReasonNotStarted, true, false, "press Start"},
		{"never started, old wording", refusal(401, "Unauthorized: bot can't initiate conversation with a user"), ReasonNotStarted, true, false, "press Start"},
		{"blocked", refusal(403, "Forbidden: bot was blocked by the user"), ReasonBlocked, true, false, "unblock"},
		{"kicked from the group", refusal(403, "Forbidden: bot was kicked from the supergroup chat"), ReasonNotMember, true, false, "Add the bot"},
		{"not a member", refusal(403, "Forbidden: bot is not a member of the channel chat"), ReasonNotMember, true, false, "Add the bot"},
		{"account deleted", refusal(403, "Forbidden: user is deactivated"), ReasonDeactivated, true, false, "no longer exists"},
		{"chat is a bot", refusal(403, "Forbidden: bot can't send messages to bots"), ReasonChatIsBot, true, false, "belongs to a bot"},
		{"no rights in the channel", refusal(400, "Bad Request: need administrator rights in the channel chat"), ReasonNoRights, true, false, "administrator"},
		{"no rights in the group", refusal(400, "Bad Request: not enough rights to send text messages to the chat"), ReasonNoRights, true, false, "not allowed to write"},
		{"topic", refusal(400, "Bad Request: message thread not found"), ReasonTopicNotFound, true, false, "topic ID"},
		{"upgraded group", fakeAnswer{status: 400, body: `{"ok":false,"error_code":400,"description":"Bad Request: group chat was upgraded to a supergroup chat","parameters":{"migrate_to_chat_id":-1009876543210}}`}, ReasonGroupUpgraded, true, false, "-1009876543210"},
		{"entities", refusal(400, "Bad Request: can't parse entities: unsupported start tag"), ReasonBadMessage, false, false, "form of the message"},
		{"too long", refusal(400, "Bad Request: message is too long"), ReasonBadMessage, false, false, "form of the message"},
		{"flood", fakeAnswer{status: 429, body: `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 7","parameters":{"retry_after":7}}`}, ReasonRateLimited, false, true, "7 seconds"},
		{"flood, header only", fakeAnswer{status: 429, body: `nonsense`, header: map[string]string{"Retry-After": "9"}}, ReasonRateLimited, false, true, "9 seconds"},
		{"Telegram down", refusal(502, "Bad Gateway"), ReasonTelegramError, false, true, "tried again"},
		{"not JSON, server error", fakeAnswer{status: 503, body: "<html>busy</html>"}, ReasonTelegramError, false, true, "status 503"},
		{"something new", refusal(400, "Bad Request: SOMETHING_NEW"), ReasonRefused, false, false, "SOMETHING_NEW"},
		{"forbidden, new wording", refusal(403, "Forbidden: a reason nobody has seen"), ReasonRefused, true, false, "status 403"},
		{"accepted status, refused body", fakeAnswer{status: 200, body: `{"ok":false,"description":"odd"}`}, ReasonRefused, false, false, "odd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeTelegram(t)
			fake.always(tc.answer)
			failure := fake.client().send(context.Background(), testSettings(), "text", nil, false)
			if failure == nil {
				t.Fatal("accepted")
			}
			if failure.Reason != tc.reason || failure.Settings != tc.settings || failure.Retryable != tc.retry {
				t.Errorf("reason %q settings %v retry %v, want %q %v %v (%s)", failure.Reason, failure.Settings, failure.Retryable, tc.reason, tc.settings, tc.retry, failure.Message)
			}
			if !strings.Contains(failure.Message, tc.says) {
				t.Errorf("message %q does not say %q", failure.Message, tc.says)
			}
			mustNotContain(t, "the message", failure.Message, testToken)
			mustNotContain(t, "the message", failure.Message, "/bot")
		})
	}
}

func TestRetryAfterIsReadAndBounded(t *testing.T) {
	fake := newFakeTelegram(t)
	fake.always(fakeAnswer{status: 429, body: `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`})
	failure := fake.client().send(context.Background(), testSettings(), "text", nil, false)
	if failure == nil || failure.RetryAfter != 7*time.Second {
		t.Fatalf("retry after %v, want 7s", failure)
	}

	fake.always(fakeAnswer{status: 429, body: `{"ok":false,"error_code":429,"parameters":{"retry_after":86400}}`})
	failure = fake.client().send(context.Background(), testSettings(), "text", nil, false)
	if failure == nil || failure.RetryAfter != maxRetryAfter {
		t.Fatalf("a day of waiting was accepted: %v", failure)
	}
}

func TestATimeoutIsATimeoutAndCanBeRetried(t *testing.T) {
	fake := newFakeTelegram(t)
	fake.always(fakeAnswer{status: 200, body: answerOK.body, delay: 2 * time.Second})
	client := fake.client()
	client.http.Timeout = 150 * time.Millisecond

	started := time.Now()
	failure := client.send(context.Background(), testSettings(), "text", nil, false)
	if failure == nil || failure.Reason != ReasonTimeout || !failure.Retryable {
		t.Fatalf("got %+v, want a timeout that can be retried", failure)
	}
	if took := time.Since(started); took > time.Second {
		t.Errorf("the call took %v: the timeout did not bound it", took)
	}
	mustNotContain(t, "the message", failure.Message, testToken)
}

func TestARedirectIsNotFollowed(t *testing.T) {
	var elsewhereHits atomic.Int64
	elsewhere := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhereHits.Add(1)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer elsewhere.Close()

	fake := newFakeTelegram(t)
	for _, status := range []int{301, 302, 307, 308} {
		fake.always(fakeAnswer{status: status, body: `{"ok":true}`, header: map[string]string{"Location": elsewhere.URL + "/bot" + testToken + "/sendMessage"}})
		failure := fake.client().send(context.Background(), testSettings(), "text", nil, false)
		if failure == nil {
			t.Fatalf("status %d: a redirect counted as accepted", status)
		}
		mustNotContain(t, "the message", failure.Message, testToken)
	}
	// also when the redirect stays on the same host: the path is fixed too
	fake.always(fakeAnswer{status: 302, body: `{}`, header: map[string]string{"Location": "/elsewhere"}})
	before := len(fake.received())
	fake.client().send(context.Background(), testSettings(), "text", nil, false)
	if after := len(fake.received()); after != before+1 {
		t.Errorf("%d requests for one send: the redirect was followed", after-before)
	}
	if elsewhereHits.Load() != 0 {
		t.Errorf("the other server was asked %d times: the token went there", elsewhereHits.Load())
	}
}

func TestACertificateThatCannotBeVerifiedSendsNothing(t *testing.T) {
	fake := newFakeTelegram(t)
	// a client that trusts the system's authorities only: not the fake's
	client := &apiClient{base: fake.server.URL, http: newHTTPClient(nil)}
	failure := client.send(context.Background(), testSettings(), "text", nil, false)
	if failure == nil || failure.Reason != ReasonCertificate {
		t.Fatalf("got %+v, want a certificate failure", failure)
	}
	if got := len(fake.received()); got != 0 {
		t.Errorf("%d requests reached a server whose certificate was not verified", got)
	}
	mustNotContain(t, "the message", failure.Message, testToken)
}

func TestOnlyHTTPSIsSpoken(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a request was sent over plain HTTP: %s", r.URL.Path)
	}))
	defer plain.Close()
	client := &apiClient{base: plain.URL, http: newHTTPClient(nil)}
	if failure := client.send(context.Background(), testSettings(), "text", nil, false); failure == nil {
		t.Fatal("sent over plain HTTP")
	}
	if newAPIClient().base != "https://api.telegram.org" {
		t.Errorf("the server's client talks to %q", newAPIClient().base)
	}
}

func TestTheClientUsesNoProxyAndVerifiesCertificates(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")

	transport, ok := newAPIClient().http.Transport.(*http.Transport)
	if !ok {
		t.Fatal("the transport is not the one made here")
	}
	if transport.Proxy != nil {
		t.Error("a proxy function is set: the environment's proxy would be used")
	}
	if transport.TLSClientConfig == nil || transport.TLSClientConfig.InsecureSkipVerify {
		t.Error("certificate verification is off")
	}
	if transport.TLSClientConfig.MinVersion < 0x0303 {
		t.Error("TLS below 1.2 is accepted")
	}
	if newAPIClient().http.Timeout <= 0 {
		t.Error("requests have no time limit")
	}

	// and with a proxy in the environment the request still goes straight to the fake
	fake := newFakeTelegram(t)
	if failure := fake.client().send(context.Background(), testSettings(), "text", nil, false); failure != nil {
		t.Fatalf("send with a proxy in the environment: %v", failure)
	}
}

func TestAnOversizedAnswerIsNotRead(t *testing.T) {
	fake := newFakeTelegram(t)
	// an answer that only says "accepted" after more than is ever read
	fake.always(fakeAnswer{status: 200, body: strings.Repeat(" ", maxAnswerBytes+16) + answerOK.body})
	if failure := fake.client().send(context.Background(), testSettings(), "text", nil, false); failure == nil {
		t.Fatal("an answer was read past the bound")
	}

	huge := `{"ok":false,"error_code":400,"description":"` + strings.Repeat("x", 2<<20) + `"}`
	fake.always(fakeAnswer{status: 400, body: huge})
	failure := fake.client().send(context.Background(), testSettings(), "text", nil, false)
	if failure == nil {
		t.Fatal("accepted")
	}
	if len(failure.Message) > 1000 {
		t.Errorf("the message is %d bytes long: the answer was not bounded", len(failure.Message))
	}
}

func TestNothingIsSentWithSettingsOfTheWrongShape(t *testing.T) {
	fake := newFakeTelegram(t)
	for _, token := range []string{"", "nonsense", testToken + "/../x", "123456789:short", testToken + "?a=b", "123456789:AAFakeFakeFakeFakeFake%2FFakeFakeFake0"} {
		st := testSettings()
		st.token = token
		failure := fake.client().send(context.Background(), st, "text", nil, false)
		if failure == nil || failure.Reason != ReasonSettings {
			t.Errorf("token %q: %+v", token, failure)
		}
	}
	for _, chat := range []string{"", "abc", "12 34", "@x", "-", "1e5", `1,"text":"x"`} {
		st := testSettings()
		st.chatID = chat
		failure := fake.client().send(context.Background(), st, "text", nil, false)
		if failure == nil || failure.Reason != ReasonSettings {
			t.Errorf("chat %q: %+v", chat, failure)
		}
	}
	if got := len(fake.received()); got != 0 {
		t.Errorf("%d requests were sent with unusable settings", got)
	}
}

func TestANetworkErrorNeverNamesTheTokenOrTheURL(t *testing.T) {
	fake := newFakeTelegram(t)
	client := fake.client()
	fake.server.Close() // nothing listens any more

	failure := client.send(context.Background(), testSettings(), "text", nil, false)
	if failure == nil || !failure.Retryable {
		t.Fatalf("got %+v, want a failure that can be retried", failure)
	}
	mustNotContain(t, "the message", failure.Message, testToken)
	mustNotContain(t, "the message", failure.Message, "sendMessage")
	mustNotContain(t, "the message", failure.Error(), "/bot")
}

func TestScrubRemovesTheTokenInEverySpelling(t *testing.T) {
	texts := []string{
		`Post "https://api.telegram.org/bot` + testToken + `/sendMessage": dial tcp: i/o timeout`,
		`token=` + testToken,
		`path /bot123456789%3AAAFakeFakeFakeFakeFakeFakeFakeFake0/sendMessage`,
		`another bot's 987654321:BBanotherTokenThatIsNotOursAtAll0000 here`,
		// spelled as a query value, outside any path: only knowing the token finds it
		`redirected to /login?next=` + url.QueryEscape(testToken),
		"line one\nline two " + testToken,
	}
	for _, text := range texts {
		cleaned := scrub(text, testToken)
		mustNotContain(t, "scrubbed text", cleaned, testToken)
		mustNotContain(t, "scrubbed text", cleaned, "AAFakeFake")
		mustNotContain(t, "scrubbed text", cleaned, "BBanotherToken")
		if strings.ContainsAny(cleaned, "\n\r") {
			t.Errorf("a line break survived: %q", cleaned)
		}
	}
	if long := scrub(strings.Repeat("a", 5000), testToken); len([]rune(long)) > maxErrorDetail {
		t.Errorf("a detail of %d characters", len([]rune(long)))
	}
}
