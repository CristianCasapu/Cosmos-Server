package telegram

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// apiHost is the only place a request of this package goes to. It is a
// constant on purpose: no setting, no redirect and no proxy can change it.
const apiHost = "https://api.telegram.org"

const (
	requestTimeout  = 20 * time.Second
	maxAnswerBytes  = 64 << 10
	maxRetryAfter   = 15 * time.Minute
	maxErrorDetail  = 200
	hiddenToken     = "[token]"
	maxMessageUnits = 4096 // Telegram's limit, in UTF-16 code units
)

// The reasons a message was not accepted, as identifiers for the UI.
const (
	ReasonWrongToken    = "wrong_token"
	ReasonChatNotFound  = "chat_not_found"
	ReasonNotStarted    = "not_started"
	ReasonBlocked       = "blocked"
	ReasonNotMember     = "not_member"
	ReasonNoRights      = "no_rights"
	ReasonTopicNotFound = "topic_not_found"
	ReasonGroupUpgraded = "group_upgraded"
	ReasonDeactivated   = "user_deactivated"
	ReasonChatIsBot     = "chat_is_bot"
	ReasonBadMessage    = "bad_message"
	ReasonRateLimited   = "rate_limited"
	ReasonTelegramError = "telegram_error"
	ReasonRefused       = "refused"
	ReasonTimeout       = "timeout"
	ReasonDNS           = "dns"
	ReasonCertificate   = "certificate"
	ReasonNetwork       = "network"
	ReasonSettings      = "settings"
)

// sendError says why a message was not accepted. Message is a sentence for
// the administrator; it never contains the token or the request's URL.
type sendError struct {
	Reason  string
	Message string
	// Retryable: trying again later can succeed (network, Telegram's own
	// errors, flood control). RetryAfter is what Telegram asked for.
	Retryable  bool
	RetryAfter time.Duration
	// Settings: the token or the chat is refused. Sending again with the
	// same settings is pointless.
	Settings bool
}

func (e *sendError) Error() string { return e.Message }

// entity marks a part of a message's text, in UTF-16 code units.
type entity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

type linkPreview struct {
	Disabled bool `json:"is_disabled"`
}

// sendRequest is the body of sendMessage. There is no parse_mode: the text
// is never parsed for formatting.
type sendRequest struct {
	ChatID      json.RawMessage `json:"chat_id"`
	ThreadID    int             `json:"message_thread_id,omitempty"`
	Text        string          `json:"text"`
	Entities    []entity        `json:"entities,omitempty"`
	LinkPreview linkPreview     `json:"link_preview_options"`
	Silent      bool            `json:"disable_notification,omitempty"`
}

type apiAnswer struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	ErrorCode   int    `json:"error_code"`
	Parameters  struct {
		RetryAfter      int   `json:"retry_after"`
		MigrateToChatID int64 `json:"migrate_to_chat_id"`
	} `json:"parameters"`
}

// apiClient talks to the Bot API.
type apiClient struct {
	base string // apiHost; the tests point it at a fake of their own
	http *http.Client
}

// newAPIClient makes the client the server uses.
func newAPIClient() *apiClient {
	return &apiClient{base: apiHost, http: newHTTPClient(nil)}
}

// newHTTPClient makes an HTTP client for the Bot API: no proxy (Cosmos has
// no proxy setting, and the environment's is not used), no redirect
// followed, certificates verified against roots (nil: the system's), every
// step bounded in time.
func newHTTPClient(roots *x509.CertPool) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            dialer.DialContext,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  15 * time.Second,
		ExpectContinueTimeout:  time.Second,
		MaxIdleConns:           2,
		MaxIdleConnsPerHost:    2,
		IdleConnTimeout:        60 * time.Second,
		MaxResponseHeaderBytes: 16 << 10,
		ForceAttemptHTTP2:      true,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// send posts one message. A nil result means Telegram accepted it.
func (c *apiClient) send(ctx context.Context, st settings, text string, entities []entity, silent bool) *sendError {
	if !ValidToken(st.token) {
		return &sendError{Reason: ReasonSettings, Settings: true, Message: "The bot token is missing or is not a bot token."}
	}
	if !ValidChatID(st.chatID) {
		return &sendError{Reason: ReasonSettings, Settings: true, Message: "The chat ID is missing or is not a chat ID."}
	}
	if !strings.HasPrefix(c.base, "https://") {
		return &sendError{Reason: ReasonSettings, Message: "Telegram is only reached over HTTPS."}
	}

	text, entities = clampMessage(text, entities)
	if strings.TrimSpace(text) == "" {
		return &sendError{Reason: ReasonBadMessage, Message: "The message is empty."}
	}

	request := sendRequest{
		ChatID:      chatIDValue(st.chatID),
		ThreadID:    st.topicID,
		Text:        text,
		Entities:    entities,
		LinkPreview: linkPreview{Disabled: true},
		Silent:      silent,
	}
	body, err := json.Marshal(request)
	if err != nil {
		return &sendError{Reason: ReasonBadMessage, Message: "The message could not be prepared."}
	}

	// The token is part of the path: this URL must never reach a log line
	// or an error. Everything below that fails goes through networkError.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+st.token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return &sendError{Reason: ReasonSettings, Message: "The request to Telegram could not be prepared."}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "cosmos-server")

	resp, err := c.http.Do(req)
	if err != nil {
		return networkError(err, st.token)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes))
	if err != nil {
		return networkError(err, st.token)
	}

	var answer apiAnswer
	parsed := json.Unmarshal(raw, &answer) == nil
	if resp.StatusCode == http.StatusOK && parsed && answer.OK {
		return nil
	}
	if !parsed {
		answer = apiAnswer{}
	}
	return answerError(resp.StatusCode, resp.Header.Get("Retry-After"), answer, st.token)
}

// chatIDValue is the chat as JSON: a number when it is one, else the name.
func chatIDValue(chatID string) json.RawMessage {
	if chatNumberPattern.MatchString(chatID) {
		return json.RawMessage(chatID)
	}
	encoded, _ := json.Marshal(chatID)
	return encoded
}

// answerError turns an answer of Telegram that is not a success into a
// reason in plain words. Telegram may change its codes and texts, so the
// texts are matched loosely and there is always a fallback.
func answerError(status int, retryAfterHeader string, answer apiAnswer, token string) *sendError {
	description := strings.ToLower(answer.Description)
	has := func(parts ...string) bool {
		for _, part := range parts {
			if strings.Contains(description, part) {
				return true
			}
		}
		return false
	}

	switch {
	case status == http.StatusTooManyRequests || answer.ErrorCode == http.StatusTooManyRequests:
		wait := time.Duration(answer.Parameters.RetryAfter) * time.Second
		if wait <= 0 {
			if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfterHeader)); err == nil && seconds > 0 {
				wait = time.Duration(seconds) * time.Second
			}
		}
		if wait <= 0 {
			wait = 5 * time.Second
		}
		if wait > maxRetryAfter {
			wait = maxRetryAfter
		}
		return &sendError{Reason: ReasonRateLimited, Retryable: true, RetryAfter: wait,
			Message: "Telegram is receiving too many messages from this bot and asks to wait " + strconv.Itoa(int(wait/time.Second)) + " seconds."}

	case status == http.StatusUnauthorized && !has("bot can't", "kicked", "initiate"), status == http.StatusNotFound:
		return &sendError{Reason: ReasonWrongToken, Settings: true,
			Message: "Telegram does not know this bot token. Copy the token again from BotFather; if you made a new token there, the old one no longer works."}

	case has("bot was blocked by the user"):
		return &sendError{Reason: ReasonBlocked, Settings: true,
			Message: "The bot is blocked in this chat. Open the bot in Telegram, unblock it (\"Restart bot\") and try again."}

	case has("can't initiate conversation", "cant initiate conversation"):
		return &sendError{Reason: ReasonNotStarted, Settings: true,
			Message: "The bot may not write to this person yet. Open the bot in Telegram, press Start, and try again."}

	case has("bot was kicked", "bot is not a member", "not a member of"):
		return &sendError{Reason: ReasonNotMember, Settings: true,
			Message: "The bot is not a member of this group or channel, or was removed from it. Add the bot to it and try again."}

	case has("user is deactivated"):
		return &sendError{Reason: ReasonDeactivated, Settings: true,
			Message: "The Telegram account behind this chat ID no longer exists."}

	case has("send messages to bots"):
		return &sendError{Reason: ReasonChatIsBot, Settings: true,
			Message: "This chat ID belongs to a bot. Use your own chat ID, or that of a group or channel."}

	case has("upgraded to a supergroup"):
		message := "The group was turned into a supergroup and has a new chat ID."
		if answer.Parameters.MigrateToChatID != 0 {
			message = "The group was turned into a supergroup. Its new chat ID is " + strconv.FormatInt(answer.Parameters.MigrateToChatID, 10) + ": enter that one."
		}
		return &sendError{Reason: ReasonGroupUpgraded, Settings: true, Message: message}

	case has("message thread not found", "topic_closed", "topic_deleted", "topic closed", "topic deleted", "thread not found"):
		return &sendError{Reason: ReasonTopicNotFound, Settings: true,
			Message: "The group has no open topic with this topic ID. Check the topic ID, or leave it empty."}

	case has("not enough rights", "have no rights", "need administrator rights", "chat_write_forbidden", "chat_send_plain_forbidden", "chat_restricted"):
		return &sendError{Reason: ReasonNoRights, Settings: true,
			Message: "The bot is not allowed to write in this chat. In a channel it must be an administrator that may post messages; in a group, check that members may send messages."}

	case has("chat not found", "peer_id_invalid", "chat_id is empty", "chat_id_invalid"):
		return &sendError{Reason: ReasonChatNotFound, Settings: true,
			Message: "Telegram cannot find this chat. Check the chat ID. For a private chat, open the bot in Telegram and press Start first; for a group or channel, add the bot to it (group IDs are negative)."}

	case has("can't parse entities", "cant parse entities", "message is too long", "message text is empty", "entity", "entities"):
		return &sendError{Reason: ReasonBadMessage,
			Message: "Telegram refused the form of the message."}

	case status >= 500:
		return &sendError{Reason: ReasonTelegramError, Retryable: true,
			Message: "Telegram has a problem at the moment (status " + strconv.Itoa(status) + "). That usually passes by itself."}
	}

	message := "Telegram refused the message (status " + strconv.Itoa(status) + ")"
	if detail := scrub(answer.Description, token); detail != "" {
		message += ": " + detail
	}
	// a refusal that is about the bot or the chat as a whole, whatever its words
	settings := status == http.StatusForbidden || status == http.StatusUnauthorized
	return &sendError{Reason: ReasonRefused, Settings: settings, Message: message + "."}
}

// networkError turns a failure to reach Telegram into plain words. The
// error of the HTTP client holds the request's URL, and with it the token:
// only the innermost cause is kept, and that is scrubbed as well.
func networkError(err error, token string) *sendError {
	cause := err
	var urlError *url.Error
	if errors.As(err, &urlError) && urlError.Err != nil {
		cause = urlError.Err
	}
	detail := scrub(cause.Error(), token)

	var dnsError *net.DNSError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	var invalidCertificate x509.CertificateInvalidError
	var verification *tls.CertificateVerificationError
	var netError net.Error

	switch {
	case errors.Is(err, context.Canceled):
		return &sendError{Reason: ReasonNetwork, Retryable: true, Message: "Sending was interrupted."}
	case errors.As(err, &dnsError):
		return &sendError{Reason: ReasonDNS, Retryable: true,
			Message: "The address of Telegram (api.telegram.org) could not be looked up. Check the server's internet connection and DNS."}
	case errors.As(err, &unknownAuthority), errors.As(err, &hostnameError), errors.As(err, &invalidCertificate), errors.As(err, &verification):
		return &sendError{Reason: ReasonCertificate, Retryable: true,
			Message: "The certificate of api.telegram.org could not be verified, so nothing was sent. Something between this server and Telegram answers in its place, or the server's clock is wrong."}
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netError) && netError.Timeout():
		return &sendError{Reason: ReasonTimeout, Retryable: true,
			Message: "Telegram did not answer in time. Check that the server can reach api.telegram.org (port 443)."}
	}

	message := "Telegram could not be reached. Check that the server can reach api.telegram.org (port 443)"
	if detail != "" {
		message += ": " + detail
	}
	return &sendError{Reason: ReasonNetwork, Retryable: true, Message: message + "."}
}

var (
	// the path of a Bot API request, wherever it shows up in a text
	botPathPattern = regexp.MustCompile(`(?i)/bot[^/\s"']*`)
	// anything with the shape of a bot token, also one that is not ours
	tokenShapePattern = regexp.MustCompile(`[0-9]{5,15}:[A-Za-z0-9_-]{20,80}`)
)

// scrub makes a text from outside (an error, an answer of Telegram) safe to
// show and to log: no token in any spelling, no URL of the API, no control
// characters, bounded.
func scrub(text string, token string) string {
	if token != "" {
		for _, spelled := range []string{token, url.PathEscape(token), url.QueryEscape(token)} {
			text = strings.ReplaceAll(text, spelled, hiddenToken)
		}
	}
	text = botPathPattern.ReplaceAllString(text, "/bot"+hiddenToken)
	text = tokenShapePattern.ReplaceAllString(text, hiddenToken)
	return cleanValue(text, maxErrorDetail)
}
