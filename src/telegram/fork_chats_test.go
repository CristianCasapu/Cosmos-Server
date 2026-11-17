package telegram

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Fork addition (plan 47): tests of fork_chats.go against the fake Telegram.

const getMeOK = `{"ok":true,"result":{"id":123456789,"is_bot":true,"first_name":"Cosmos","username":"cosmos_test_bot"}}`

// answerChats answers getMe and getUpdates; updates is the result array.
func answerChats(fake *fakeTelegram, updates string) {
	fake.answerWith(func(_ int, req fakeRequest) fakeAnswer {
		if strings.HasSuffix(req.path, "/getMe") {
			return fakeAnswer{status: 200, body: getMeOK}
		}
		return fakeAnswer{status: 200, body: `{"ok":true,"result":` + updates + `}`}
	})
}

func chatsOfRoute(t *testing.T, body string) (ChatsResult, string) {
	t.Helper()
	rec := call(ChatsRoute, apiRequest("POST", "/api/telegram/chats", "sudo", body))
	var answer struct {
		Status string      `json:"status"`
		Data   ChatsResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil || answer.Status != "OK" {
		t.Fatalf("answer %d %s", rec.Code, rec.Body.String())
	}
	return answer.Data, rec.Body.String()
}

func resetChatsGate() {
	chatsMu.Lock()
	lastChatsLook = time.Time{}
	chatsBusy = false
	chatsMu.Unlock()
}

const privateUpdate = `{"update_id":1,"message":{"message_id":1,"from":{"id":123456789,"first_name":"Ann"},"chat":{"id":123456789,"type":"private","first_name":"Ann","last_name":"Example","username":"ann_example"},"date":1,"text":"hello"}}`
const groupUpdate = `{"update_id":2,"message":{"message_id":2,"chat":{"id":-1001234567890,"type":"supergroup","title":"Home server","is_forum":true},"message_thread_id":7,"is_topic_message":true,"reply_to_message":{"message_id":7,"forum_topic_created":{"name":"Alerts"}},"date":2,"text":"hi"}}`
const channelAdded = `{"update_id":3,"my_chat_member":{"chat":{"id":-1009876543210,"type":"channel","title":"Server news","username":"server_news"},"date":3,"old_chat_member":{"status":"left"},"new_chat_member":{"status":"administrator"}}}`

func TestChatsFoundAreOfferedNewestFirst(t *testing.T) {
	fake := newFakeTelegram(t)
	useFake(t, fake)
	useConfig(t, testConfig())
	resetChatsGate()
	answerChats(fake, "["+privateUpdate+","+groupUpdate+","+channelAdded+","+privateUpdate+"]")

	result, raw := chatsOfRoute(t, `{"token":""}`)
	if !result.Success || result.Bot != "cosmos_test_bot" {
		t.Fatalf("result %+v", result)
	}
	if len(result.Chats) != 3 {
		t.Fatalf("chats %+v", result.Chats)
	}
	ann, group, channel := result.Chats[0], result.Chats[2], result.Chats[1]
	if ann.ID != "123456789" || ann.Kind != "private" || ann.Name != "Ann Example" || ann.Username != "ann_example" {
		t.Errorf("private chat %+v", ann)
	}
	if channel.ID != "-1009876543210" || channel.Kind != "channel" || channel.Name != "Server news" {
		t.Errorf("channel %+v", channel)
	}
	if group.ID != "-1001234567890" || group.Kind != "supergroup" || group.Name != "Home server" ||
		len(group.Topics) != 1 || group.Topics[0].ID != 7 || group.Topics[0].Name != "Alerts" {
		t.Errorf("group %+v", group)
	}
	mustNotContain(t, "the answer", raw, testToken)

	// getUpdates without offset (nothing confirmed) nor allowed_updates (kept by Telegram)
	for _, req := range fake.received() {
		if strings.HasSuffix(req.path, "/getUpdates") {
			if _, has := req.body["offset"]; has {
				t.Errorf("an offset was sent: %s", req.raw)
			}
			if _, has := req.body["allowed_updates"]; has {
				t.Errorf("allowed_updates was sent: %s", req.raw)
			}
			if req.body["limit"] != float64(100) || req.body["timeout"] != float64(0) {
				t.Errorf("getUpdates %s", req.raw)
			}
		}
		if req.path != "/bot"+testToken+"/getMe" && req.path != "/bot"+testToken+"/getUpdates" {
			t.Errorf("unexpected request %s", req.path)
		}
	}
}

func TestNobodyWroteYet(t *testing.T) {
	fake := newFakeTelegram(t)
	useFake(t, fake)
	useConfig(t, testConfig())
	resetChatsGate()
	answerChats(fake, "[]")
	result, _ := chatsOfRoute(t, `{"token":"***"}`)
	if !result.Success || len(result.Chats) != 0 || result.Chats == nil {
		t.Fatalf("result %+v", result)
	}
}

func TestABotRemovedFromAGroupIsNotOffered(t *testing.T) {
	removed := `{"update_id":9,"my_chat_member":{"chat":{"id":-1001234567890,"type":"supergroup","title":"Home server"},"date":9,"new_chat_member":{"status":"kicked"}}}`
	chats := chatsOf(updatesOf(t, "["+groupUpdate+","+removed+"]"))
	if len(chats) != 0 {
		t.Fatalf("chats %+v", chats)
	}
	// added again afterwards: offered
	chats = chatsOf(updatesOf(t, "["+groupUpdate+","+removed+","+channelAdded+"]"))
	if len(chats) != 1 || chats[0].Kind != "channel" {
		t.Fatalf("chats %+v", chats)
	}
}

func updatesOf(t *testing.T, raw string) []tgUpdate {
	t.Helper()
	var updates []tgUpdate
	if err := json.Unmarshal([]byte(raw), &updates); err != nil {
		t.Fatal(err)
	}
	return updates
}

func TestHostileNamesAreCleanedAndBounded(t *testing.T) {
	long := strings.Repeat("A", 300)
	update := `{"update_id":1,"message":{"chat":{"id":42,"type":"private","first_name":"Ann‮\n<b>x</b>\u0000","last_name":"` + long + `","username":"no/t valid"}}}`
	odd := `{"update_id":2,"message":{"chat":{"id":43,"type":"bot","first_name":"x"}}}`
	chats := chatsOf(updatesOf(t, "["+update+","+odd+"]"))
	if len(chats) != 1 {
		t.Fatalf("chats %+v", chats)
	}
	name := chats[0].Name
	if strings.ContainsAny(name, "‮\n\x00") || len([]rune(name)) > maxChatName {
		t.Errorf("name not cleaned: %q", name)
	}
	if !strings.HasPrefix(name, "Ann <b>x</b>") {
		t.Errorf("name %q", name)
	}
	if chats[0].Username != "" {
		t.Errorf("a username that is not one was kept: %q", chats[0].Username)
	}

	// many chats: at most maxChats
	parts := []string{}
	for i := 0; i < 50; i++ {
		parts = append(parts, `{"update_id":1,"message":{"chat":{"id":-`+strconv.Itoa(1000+i)+`,"type":"group","title":"g"}}}`)
	}
	if got := chatsOf(updatesOf(t, "["+strings.Join(parts, ",")+"]")); len(got) != maxChats {
		t.Errorf("%d chats, want %d", len(got), maxChats)
	}
}

func TestChatsFailuresInWordsWithoutTheToken(t *testing.T) {
	cases := []struct {
		name   string
		answer func(req fakeRequest) fakeAnswer
		reason string
		words  string
	}{
		{"wrong token", func(fakeRequest) fakeAnswer { return refusal(401, "Unauthorized") }, ReasonWrongToken, "does not know this bot token"},
		{"webhook", func(req fakeRequest) fakeAnswer {
			if strings.HasSuffix(req.path, "/getMe") {
				return fakeAnswer{status: 200, body: getMeOK}
			}
			return refusal(409, "Conflict: can't use getUpdates method while webhook is active; use deleteWebhook to delete the webhook first")
		}, ReasonWebhook, "webhook"},
		{"oversized", func(req fakeRequest) fakeAnswer {
			if strings.HasSuffix(req.path, "/getMe") {
				return fakeAnswer{status: 200, body: getMeOK}
			}
			return fakeAnswer{status: 200, body: `{"ok":true,"result":[` + strings.Repeat(" ", maxUpdatesAnswer) + `]}`}
		}, ReasonTooLarge, "larger than expected"},
		{"echoes the token", func(fakeRequest) fakeAnswer {
			return refusal(400, "Bad Request: something about /bot"+testToken+"/getMe and "+testToken)
		}, ReasonRefused, "refused"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeTelegram(t)
			useFake(t, fake)
			useConfig(t, testConfig())
			resetChatsGate()
			fake.answerWith(func(_ int, req fakeRequest) fakeAnswer { return tc.answer(req) })

			var logs bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(previous)
			result, raw := chatsOfRoute(t, `{"token":"`+testToken+`"}`)
			if result.Success || result.Reason != tc.reason || !strings.Contains(result.Message, tc.words) {
				t.Errorf("result %+v", result)
			}
			mustNotContain(t, "the answer", raw, testToken)
			mustNotContain(t, "the answer", raw, "AAFakeFake")
			mustNotContain(t, "the log", logs.String(), "AAFakeFake")
		})
	}
}

func TestChatsRouteChecksAndBoundsItsInput(t *testing.T) {
	fake := newFakeTelegram(t)
	useFake(t, fake)
	useConfig(t, testConfig())
	resetChatsGate()
	answerChats(fake, "[]")

	// who may ask, and how
	for who, status := range map[string]int{"none": http.StatusUnauthorized, "reader": http.StatusUnauthorized, "admin": http.StatusUnauthorized, "token": http.StatusForbidden} {
		if rec := call(ChatsRoute, apiRequest("POST", "/api/telegram/chats", who, `{}`)); rec.Code != status {
			t.Errorf("as %s: %d, want %d", who, rec.Code, status)
		}
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		if rec := call(ChatsRoute, apiRequest(method, "/api/telegram/chats", "sudo", `{}`)); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d", method, rec.Code)
		}
	}
	for name, body := range map[string]string{
		"not json":  `token`,
		"too big":   `{"token":"` + strings.Repeat("1", maxChatsBody) + `"}`,
		"not token": `{"token":"123:abc/../x"}`,
	} {
		if rec := call(ChatsRoute, apiRequest("POST", "/api/telegram/chats", "sudo", body)); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	// no saved token and none typed
	config := testConfig()
	config.Telegram.BotToken = ""
	useConfig(t, config)
	if rec := call(ChatsRoute, apiRequest("POST", "/api/telegram/chats", "sudo", `{"token":"***"}`)); rec.Code != http.StatusBadRequest {
		t.Errorf("without a token: %d", rec.Code)
	}
	if got := len(fake.received()); got != 0 {
		t.Fatalf("%d requests reached Telegram for refused input", got)
	}

	// two looks in a row: the second waits
	useConfig(t, testConfig())
	chatsOfRoute(t, `{}`)
	if rec := call(ChatsRoute, apiRequest("POST", "/api/telegram/chats", "sudo", `{}`)); rec.Code != http.StatusTooManyRequests {
		t.Errorf("second look at once: %d", rec.Code)
	}
}
