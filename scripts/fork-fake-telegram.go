//go:build ignore

// Fork addition: a stand-in for Telegram's Bot API, for trying the Telegram
// notifications (src/telegram) without sending anything to Telegram. Used
// with scripts/fork-ui-check-telegram.mjs.
//
// It answers HTTPS on :443 as api.telegram.org, with a certificate from an
// authority it makes at start and writes to /shared/ca.pem, and has a plain
// control port (:8080): GET /mode?set=<ok|wrong_token|chat_not_found|
// not_started|blocked|not_member|topic|flood|down> chooses how it answers
// sendMessage (and, for wrong_token and down, every method), GET
// /updates?set=<none|private|group|both|webhook> chooses what getUpdates
// answers (none: nobody wrote yet; private: "Ann Example" @ann_example,
// 123456789; group: "Home server", -1001234567890; both; webhook: refused as
// while a webhook is set), GET /received lists what it got (?clear=1
// empties the list). getMe answers the bot cosmos_test_bot.
//
// How it is used: on a Docker network made with --internal (no way out),
// run it in a container with the network alias api.telegram.org and a
// folder mounted at /shared; start the throwaway Cosmos on the same network
// with that folder mounted and SSL_CERT_FILE=/shared/ca.pem. The server's
// own code then talks to this program, unchanged: fixed host, certificate
// verified. It is not part of any build (go run scripts/fork-fake-telegram.go).
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type received struct {
	At     time.Time              `json:"at"`
	Method string                 `json:"method"`
	Path   string                 `json:"path"`
	Body   map[string]interface{} `json:"body"`
}

var (
	mu      sync.Mutex
	mode    = "ok"
	updates = "none"
	got     []received
)

const (
	privateUpdate = `{"update_id":101,"message":{"message_id":1,"from":{"id":123456789,"is_bot":false,"first_name":"Ann"},"chat":{"id":123456789,"type":"private","first_name":"Ann","last_name":"Example","username":"ann_example"},"date":1700000000,"text":"/start"}}`
	groupUpdate   = `{"update_id":102,"my_chat_member":{"chat":{"id":-1001234567890,"type":"supergroup","title":"Home server"},"from":{"id":123456789,"is_bot":false,"first_name":"Ann"},"date":1700000001,"old_chat_member":{"status":"left"},"new_chat_member":{"status":"member"}}}`
)

var updateAnswers = map[string]struct {
	status int
	body   string
}{
	"none":    {200, `{"ok":true,"result":[]}`},
	"private": {200, `{"ok":true,"result":[` + privateUpdate + `]}`},
	"group":   {200, `{"ok":true,"result":[` + groupUpdate + `]}`},
	"both":    {200, `{"ok":true,"result":[` + privateUpdate + `,` + groupUpdate + `]}`},
	"webhook": {409, `{"ok":false,"error_code":409,"description":"Conflict: can't use getUpdates method while webhook is active; use deleteWebhook to delete the webhook first"}`},
}

var answers = map[string]struct {
	status int
	body   string
}{
	"ok":             {200, `{"ok":true,"result":{"message_id":1}}`},
	"wrong_token":    {401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`},
	"chat_not_found": {400, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`},
	"not_started":    {403, `{"ok":false,"error_code":403,"description":"Forbidden: bot can't initiate conversation with a user"}`},
	"blocked":        {403, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`},
	"not_member":     {403, `{"ok":false,"error_code":403,"description":"Forbidden: bot is not a member of the supergroup chat"}`},
	"topic":          {400, `{"ok":false,"error_code":400,"description":"Bad Request: message thread not found"}`},
	"flood":          {429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 3","parameters":{"retry_after":3}}`},
	"down":           {502, `<html>Bad Gateway</html>`},
}

func main() {
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "cosmos-telegram test authority"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		IsCA: true, KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "api.telegram.org"},
		DNSNames:  []string{"api.telegram.org"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, _ := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)

	if err := os.WriteFile("/shared/ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0644); err != nil {
		log.Fatal(err)
	}

	api := http.NewServeMux()
	api.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		entry := received{At: time.Now(), Method: r.Method, Path: r.URL.Path}
		json.Unmarshal(raw, &entry.Body)
		mu.Lock()
		got = append(got, entry)
		answer := answers[mode]
		if mode != "wrong_token" && mode != "down" {
			switch {
			case strings.HasSuffix(r.URL.Path, "/getMe"):
				answer = answers["ok"]
				answer.body = `{"ok":true,"result":{"id":123456789,"is_bot":true,"first_name":"Cosmos test","username":"cosmos_test_bot"}}`
			case strings.HasSuffix(r.URL.Path, "/getUpdates"):
				answer = updateAnswers[updates]
			}
		}
		mu.Unlock()
		log.Printf("%s %s -> %d", r.Method, strings.Split(r.URL.Path, ":")[0]+":…", answer.status)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(answer.status)
		io.WriteString(w, answer.body)
	})
	go func() {
		server := &http.Server{Addr: ":443", Handler: withMessages(api), TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		}}
		log.Fatal(server.ListenAndServeTLS("", ""))
	}()

	control := http.NewServeMux()
	control.HandleFunc("/mode", func(w http.ResponseWriter, r *http.Request) {
		wanted := r.URL.Query().Get("set")
		mu.Lock()
		defer mu.Unlock()
		if _, known := answers[wanted]; known {
			mode = wanted
		}
		io.WriteString(w, mode+"\n")
	})
	control.HandleFunc("/updates", func(w http.ResponseWriter, r *http.Request) {
		wanted := r.URL.Query().Get("set")
		mu.Lock()
		defer mu.Unlock()
		if _, known := updateAnswers[wanted]; known {
			updates = wanted
		}
		io.WriteString(w, updates+"\n")
	})
	control.HandleFunc("/received", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Query().Get("clear") != "" {
			got = nil
		}
		json.NewEncoder(w).Encode(got)
	})
	registerMessageRoutes(control) // plan 48: the messages as a chat shows them
	log.Println("fake Telegram ready")
	log.Fatal(http.ListenAndServe(":8080", control))
}

// Fork addition (plan 48): the stand-in numbers the messages it accepts as
// Telegram does (message_id 1, 2, 3, …), keeps pins, and lists the messages
// as a chat shows them: GET /messages (?clear=1) gives, per accepted
// sendMessage, its id, text, entities, parse mode (empty when none was
// sent), whether it was silent (disable_notification), the message it
// replies to (reply_parameters, or the older reply_to_message_id) and its
// buttons; GET /pins the ids pinned now. How the other answers are chosen
// (/mode, /updates) is unchanged.

type shown struct {
	ID        int                    `json:"id"`
	At        time.Time              `json:"at"`
	Text      string                 `json:"text"`
	Entities  []interface{}          `json:"entities"`
	ParseMode string                 `json:"parse_mode"`
	Silent    bool                   `json:"silent"`
	ReplyTo   int                    `json:"reply_to"`
	Buttons   []shownButton          `json:"buttons"`
	Preview   string                 `json:"preview"`
	Raw       map[string]interface{} `json:"-"`
}

type shownButton struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

var (
	nextID        int
	shownMessages []shown
	pinned        = map[int]bool{}
)

// withMessages answers like the API it wraps, then numbers an accepted
// message and records it.
func withMessages(api http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		r.Body = io.NopCloser(strings.NewReader(string(raw)))
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, r)
		body := rec.Body.String()
		if rec.Code == http.StatusOK {
			var request map[string]interface{}
			json.Unmarshal(raw, &request)
			mu.Lock()
			switch {
			case strings.HasSuffix(r.URL.Path, "/sendMessage"):
				nextID++
				m := shown{ID: nextID, At: time.Now(), Raw: request}
				m.Text, _ = request["text"].(string)
				m.Entities, _ = request["entities"].([]interface{})
				m.ParseMode, _ = request["parse_mode"].(string)
				m.Silent, _ = request["disable_notification"].(bool)
				if reply, ok := request["reply_parameters"].(map[string]interface{}); ok {
					if id, ok := reply["message_id"].(float64); ok {
						m.ReplyTo = int(id)
					}
				} else if id, ok := request["reply_to_message_id"].(float64); ok {
					m.ReplyTo = int(id)
				}
				if markup, ok := request["reply_markup"].(map[string]interface{}); ok {
					rows, _ := markup["inline_keyboard"].([]interface{})
					for _, row := range rows {
						buttons, _ := row.([]interface{})
						for _, b := range buttons {
							button, _ := b.(map[string]interface{})
							text, _ := button["text"].(string)
							url, _ := button["url"].(string)
							m.Buttons = append(m.Buttons, shownButton{text, url})
						}
					}
				}
				m.Preview = strings.SplitN(m.Text, "\n", 2)[0]
				shownMessages = append(shownMessages, m)
				body = `{"ok":true,"result":{"message_id":` + strconv.Itoa(m.ID) + `,"date":` + strconv.FormatInt(m.At.Unix(), 10) + `,"chat":{"id":1,"type":"private"}}}`
			case strings.HasSuffix(r.URL.Path, "/pinChatMessage"), strings.HasSuffix(r.URL.Path, "/unpinChatMessage"):
				if id, ok := request["message_id"].(float64); ok {
					if strings.HasSuffix(r.URL.Path, "/pinChatMessage") {
						pinned[int(id)] = true
					} else {
						delete(pinned, int(id))
					}
				}
				body = `{"ok":true,"result":true}`
			}
			mu.Unlock()
		}
		for name, values := range rec.Header() {
			w.Header()[name] = values
		}
		w.WriteHeader(rec.Code)
		io.WriteString(w, body)
	})
}

func registerMessageRoutes(control *http.ServeMux) {
	control.HandleFunc("/messages", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Query().Get("clear") != "" {
			shownMessages = nil
		}
		list := shownMessages
		if list == nil {
			list = []shown{}
		}
		json.NewEncoder(w).Encode(list)
	})
	control.HandleFunc("/pins", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		ids := []int{}
		for id := range pinned {
			ids = append(ids, id)
		}
		json.NewEncoder(w).Encode(ids)
	})
}
