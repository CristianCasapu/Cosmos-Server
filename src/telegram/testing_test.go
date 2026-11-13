package telegram

import (
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/azukaar/cosmos-server/src/utils"
)

// A token with the shape of a real one. It belongs to no bot.
const testToken = "123456789:AAFakeFakeFakeFakeFakeFakeFakeFake0"
const testChat = "-1001234567890"

// fakeAnswer is what the fake Telegram answers to one request.
type fakeAnswer struct {
	status int
	body   string
	header map[string]string
	delay  time.Duration
}

var answerOK = fakeAnswer{status: 200, body: `{"ok":true,"result":{"message_id":1}}`}

func refusal(status int, description string) fakeAnswer {
	body, _ := json.Marshal(map[string]interface{}{"ok": false, "error_code": status, "description": description})
	return fakeAnswer{status: status, body: string(body)}
}

type fakeRequest struct {
	method string
	path   string
	body   map[string]interface{}
	raw    string
	at     time.Time
}

// fakeTelegram is a Bot API that lives in the test: an HTTPS server on the
// loopback address whose certificate the client under test is told to
// trust. Nothing ever leaves the machine.
type fakeTelegram struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []fakeRequest
	// answer decides the answer to the n-th request (from 0); nil: accepted
	answer func(n int, req fakeRequest) fakeAnswer
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()
	f := &fakeTelegram{}
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		req := fakeRequest{method: r.Method, path: r.URL.Path, raw: string(raw), at: time.Now()}
		json.Unmarshal(raw, &req.body)

		f.mu.Lock()
		n := len(f.requests)
		f.requests = append(f.requests, req)
		answer := f.answer
		f.mu.Unlock()

		reply := answerOK
		if answer != nil {
			reply = answer(n, req)
		}
		if reply.delay > 0 {
			select {
			case <-time.After(reply.delay):
			case <-r.Context().Done():
				return
			}
		}
		for name, value := range reply.header {
			w.Header().Set(name, value)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(reply.status)
		io.WriteString(w, reply.body)
	}))
	t.Cleanup(f.server.Close)
	return f
}

// client is an API client like the server's, pointed at the fake.
func (f *fakeTelegram) client() *apiClient {
	roots := x509.NewCertPool()
	roots.AddCert(f.server.Certificate())
	return &apiClient{base: f.server.URL, http: newHTTPClient(roots)}
}

func (f *fakeTelegram) answerWith(answer func(n int, req fakeRequest) fakeAnswer) {
	f.mu.Lock()
	f.answer = answer
	f.mu.Unlock()
}

func (f *fakeTelegram) always(reply fakeAnswer) {
	f.answerWith(func(int, fakeRequest) fakeAnswer { return reply })
}

func (f *fakeTelegram) received() []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeRequest(nil), f.requests...)
}

func (f *fakeTelegram) texts() []string {
	texts := []string{}
	for _, req := range f.received() {
		text, _ := req.body["text"].(string)
		texts = append(texts, text)
	}
	return texts
}

// waitFor polls until the fake received n requests.
func (f *fakeTelegram) waitFor(t *testing.T, n int) []fakeRequest {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := f.received(); len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the fake received %d requests, want %d: %q", len(f.received()), n, f.texts())
	return nil
}

func testConfig() utils.Config {
	config := utils.Config{}
	config.HTTPConfig.Hostname = "home.example"
	config.Telegram = utils.TelegramConfig{Enabled: true, BotToken: testToken, ChatID: testChat}
	return config
}

func testSettings() settings {
	return settingsOf(testConfig())
}

// covered returns the parts of a message that lie inside a code entity.
func covered(t *testing.T, text string, entities []entity) []string {
	t.Helper()
	encoded := utf16.Encode([]rune(text))
	parts := []string{}
	for _, e := range entities {
		if e.Type != "code" {
			t.Errorf("entity of type %q: only code entities are ever sent", e.Type)
		}
		if e.Offset < 0 || e.Length <= 0 || e.Offset+e.Length > len(encoded) {
			t.Fatalf("entity %+v outside the text (%d units)", e, len(encoded))
		}
		parts = append(parts, string(utf16.Decode(encoded[e.Offset:e.Offset+e.Length])))
	}
	return parts
}

// uncovered returns the text with every code entity cut out: what is left
// is what Telegram may still turn into links and mentions.
func uncovered(t *testing.T, text string, entities []entity) string {
	t.Helper()
	encoded := utf16.Encode([]rune(text))
	keep := make([]bool, len(encoded))
	for i := range keep {
		keep[i] = true
	}
	for _, e := range entities {
		for i := e.Offset; i < e.Offset+e.Length && i < len(encoded); i++ {
			keep[i] = false
		}
	}
	rest := []uint16{}
	for i, unit := range encoded {
		if keep[i] {
			rest = append(rest, unit)
		}
	}
	return string(utf16.Decode(rest))
}

// entitiesOf reads the entities of a request the fake received.
func entitiesOf(t *testing.T, req fakeRequest) []entity {
	t.Helper()
	raw, _ := json.Marshal(req.body["entities"])
	entities := []entity{}
	if string(raw) != "null" {
		if err := json.Unmarshal(raw, &entities); err != nil {
			t.Fatalf("entities of the request: %v", err)
		}
	}
	return entities
}

func mustNotContain(t *testing.T, where string, text string, secret string) {
	t.Helper()
	if strings.Contains(text, secret) {
		t.Errorf("%s contains the secret: %q", where, text)
	}
}
