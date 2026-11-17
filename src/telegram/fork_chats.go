package telegram

// Fork addition (plan 47, I2d): the chats a bot can write to, so that the
// settings form offers them instead of asking for a chat ID.
//
//	POST /cosmos/api/telegram/chats  {"token": "<token>" | "" | "***"}
//
// The server asks Telegram, with the client of this package (the fixed host,
// no proxy, no redirect, bounded in time), who the bot is (getMe) and who
// wrote to it lately (getUpdates). getUpdates is asked without an offset,
// which confirms nothing: another program reading the bot's updates loses
// none of them; and without allowed_updates, which Telegram would keep for
// later calls. The token is never part of the answer nor of a log line; the
// names of chats are other people's text, cleaned and bounded, and the
// panel shows them as text.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	maxChatsBody     = 16 << 10
	maxUpdatesAnswer = 1 << 20 // 100 updates of long messages fit well below
	maxChats         = 20
	maxTopics        = 10
	maxChatName      = 64
	chatsGap         = 3 * time.Second
)

// The reasons of a look that found nothing to offer, besides the ones of
// client.go.
const (
	ReasonWebhook  = "webhook"
	ReasonTooLarge = "too_large"
)

var (
	chatsMu       sync.Mutex
	chatsBusy     bool
	lastChatsLook time.Time
)

// Topic is a topic of a group with topics, as the updates name it.
type Topic struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Chat is one chat the bot can write to.
type Chat struct {
	// ID as text: a group's ID does not fit every number type of a browser
	ID string `json:"id"`
	// Kind: private, group, supergroup or channel
	Kind     string  `json:"kind"`
	Name     string  `json:"name"`
	Username string  `json:"username,omitempty"`
	Topics   []Topic `json:"topics,omitempty"`
}

// ChatsResult is the answer of the route.
type ChatsResult struct {
	Success bool `json:"success"`
	// Bot is the bot's public name (without @), "" when not known
	Bot   string `json:"bot,omitempty"`
	Chats []Chat `json:"chats"`
	// Reason and Message when Success is false
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

type tgChat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
	IsForum   bool   `json:"is_forum"`
}

type tgTopicCreated struct {
	Name string `json:"name"`
}

type tgMessage struct {
	Chat           tgChat          `json:"chat"`
	ThreadID       int             `json:"message_thread_id"`
	IsTopicMessage bool            `json:"is_topic_message"`
	TopicCreated   *tgTopicCreated `json:"forum_topic_created"`
	ReplyTo        *struct {
		TopicCreated *tgTopicCreated `json:"forum_topic_created"`
	} `json:"reply_to_message"`
}

type tgMember struct {
	Chat      tgChat `json:"chat"`
	NewMember struct {
		Status string `json:"status"`
	} `json:"new_chat_member"`
}

type tgUpdate struct {
	Message           *tgMessage `json:"message"`
	EditedMessage     *tgMessage `json:"edited_message"`
	ChannelPost       *tgMessage `json:"channel_post"`
	EditedChannelPost *tgMessage `json:"edited_channel_post"`
	MyChatMember      *tgMember  `json:"my_chat_member"`
}

// call posts one Bot API method and reads its answer, at most limit bytes.
// The token is part of the path: as in send, nothing that fails here
// carries the URL.
func (c *apiClient) call(ctx context.Context, token string, method string, body interface{}, limit int64) ([]byte, *sendError) {
	if !ValidToken(token) {
		return nil, &sendError{Reason: ReasonSettings, Settings: true, Message: "The bot token is missing or is not a bot token."}
	}
	if !strings.HasPrefix(c.base, "https://") {
		return nil, &sendError{Reason: ReasonSettings, Message: "Telegram is only reached over HTTPS."}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, &sendError{Reason: ReasonSettings, Message: "The request to Telegram could not be prepared."}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+token+"/"+method, bytes.NewReader(payload))
	if err != nil {
		return nil, &sendError{Reason: ReasonSettings, Message: "The request to Telegram could not be prepared."}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "cosmos-server")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, networkError(err, token)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, networkError(err, token)
	}
	if int64(len(raw)) > limit {
		return nil, &sendError{Reason: ReasonTooLarge, Message: "Telegram's answer was larger than expected, so it was not read. Try again later, or type the chat ID yourself."}
	}

	var answer apiAnswer
	parsed := json.Unmarshal(raw, &answer) == nil
	if resp.StatusCode == http.StatusOK && parsed && answer.OK {
		return raw, nil
	}
	if !parsed {
		answer = apiAnswer{}
	}
	if resp.StatusCode == http.StatusConflict || answer.ErrorCode == http.StatusConflict {
		return nil, &sendError{Reason: ReasonWebhook,
			Message: "Another program receives this bot's messages through a webhook, so Telegram does not tell who wrote to the bot. Type the chat ID yourself, or use a bot of its own for Cosmos."}
	}
	return nil, answerError(resp.StatusCode, resp.Header.Get("Retry-After"), answer, token)
}

// chats asks Telegram who the bot is and which chats it can write to.
func (c *apiClient) chats(ctx context.Context, token string) ChatsResult {
	result := ChatsResult{Chats: []Chat{}}

	raw, failure := c.call(ctx, token, "getMe", struct{}{}, maxAnswerBytes)
	if failure != nil {
		result.Reason, result.Message = failure.Reason, failure.Message
		return result
	}
	var me struct {
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &me) == nil && chatNamePattern.MatchString("@"+me.Result.Username) {
		result.Bot = me.Result.Username
	}

	raw, failure = c.call(ctx, token, "getUpdates", map[string]int{"limit": 100, "timeout": 0}, maxUpdatesAnswer)
	if failure != nil {
		result.Reason, result.Message = failure.Reason, failure.Message
		return result
	}
	var updates struct {
		Result []tgUpdate `json:"result"`
	}
	if err := json.Unmarshal(raw, &updates); err != nil {
		result.Reason, result.Message = ReasonTelegramError, "Telegram's answer could not be read."
		return result
	}
	result.Chats = chatsOf(updates.Result)
	result.Success = true
	return result
}

// chatsOf gathers the chats of the updates, oldest first as Telegram sends
// them: a chat the bot was removed from later is left out; the chat seen
// last comes first.
func chatsOf(updates []tgUpdate) []Chat {
	type entry struct {
		chat   Chat
		topics map[int]string
		last   int
		out    bool
	}
	found := map[int64]*entry{}
	note := func(c tgChat, at int) *entry {
		kind := c.Type
		switch kind {
		case "private", "group", "supergroup", "channel":
		default:
			return nil
		}
		e := found[c.ID]
		if e == nil {
			e = &entry{topics: map[int]string{}}
			found[c.ID] = e
		}
		name := strings.TrimSpace(cleanValue(strings.TrimSpace(c.FirstName+" "+c.LastName), maxChatName))
		if kind != "private" {
			name = cleanValue(c.Title, maxChatName)
		}
		username := ""
		if chatNamePattern.MatchString("@" + c.Username) {
			username = c.Username
		}
		e.chat = Chat{ID: strconv.FormatInt(c.ID, 10), Kind: kind, Name: name, Username: username}
		e.last = at
		return e
	}
	for i, u := range updates {
		for _, m := range []*tgMessage{u.Message, u.EditedMessage, u.ChannelPost, u.EditedChannelPost} {
			if m == nil {
				continue
			}
			e := note(m.Chat, i)
			if e == nil {
				continue
			}
			e.out = false
			if m.Chat.IsForum && m.ThreadID > 0 && m.ThreadID <= maxTopicID && (m.IsTopicMessage || m.TopicCreated != nil) {
				name := ""
				if m.TopicCreated != nil {
					name = m.TopicCreated.Name
				} else if m.ReplyTo != nil && m.ReplyTo.TopicCreated != nil {
					name = m.ReplyTo.TopicCreated.Name
				}
				name = cleanValue(name, maxChatName)
				if _, known := e.topics[m.ThreadID]; known {
					if name != "" {
						e.topics[m.ThreadID] = name
					}
				} else if len(e.topics) < maxTopics {
					e.topics[m.ThreadID] = name
				}
			}
		}
		if u.MyChatMember != nil {
			if e := note(u.MyChatMember.Chat, i); e != nil {
				switch u.MyChatMember.NewMember.Status {
				case "left", "kicked":
					e.out = true
				default:
					e.out = false
				}
			}
		}
	}

	entries := make([]*entry, 0, len(found))
	for _, e := range found {
		if !e.out {
			entries = append(entries, e)
		}
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].last > entries[b].last })
	if len(entries) > maxChats {
		entries = entries[:maxChats]
	}
	chats := make([]Chat, 0, len(entries))
	for _, e := range entries {
		ids := make([]int, 0, len(e.topics))
		for id := range e.topics {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		for _, id := range ids {
			e.chat.Topics = append(e.chat.Topics, Topic{ID: id, Name: e.topics[id]})
		}
		chats = append(chats, e.chat)
	}
	return chats
}

type chatsRequest struct {
	Token string `json:"token"`
}

// ChatsRoute godoc
// @Summary Telegram notifications: the chats the bot can write to
// @Description Asks Telegram (getMe, getUpdates without confirming anything) who the bot is and who wrote to it lately, for the settings form to offer. An empty or masked token means the saved one. The token is never part of the answer.
// @Tags telegram
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Failure 429 {object} utils.HTTPErrorResult
// @Router /api/telegram/chats [post]
func ChatsRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}

	var request chatsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxChatsBody)).Decode(&request); err != nil {
		utils.HTTPError(w, "Invalid request", http.StatusBadRequest, "TG004")
		return
	}
	token := strings.TrimSpace(request.Token)
	if token == "" || token == MaskedValue {
		token = strings.TrimSpace(utils.GetMainConfig().Telegram.BotToken)
		if token == "" {
			utils.HTTPError(w, "Enter the bot token first", http.StatusBadRequest, "TG005")
			return
		}
	}
	if !ValidToken(token) {
		utils.HTTPError(w, "This is not a bot token. Copy the whole token BotFather gave you: digits, a colon, then letters and digits", http.StatusBadRequest, "TG005")
		return
	}

	// one look at a time, and not in a row: each is two requests to Telegram
	chatsMu.Lock()
	if chatsBusy {
		chatsMu.Unlock()
		utils.HTTPError(w, "Cosmos is already asking Telegram: wait a moment", http.StatusTooManyRequests, "TG006")
		return
	}
	if wait := chatsGap - time.Since(lastChatsLook); wait > 0 {
		chatsMu.Unlock()
		utils.HTTPError(w, "Wait a few seconds before looking again", http.StatusTooManyRequests, "TG006")
		return
	}
	chatsBusy = true
	chatsMu.Unlock()
	defer func() {
		chatsMu.Lock()
		chatsBusy = false
		lastChatsLook = time.Now()
		chatsMu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(req.Context(), requestTimeout+5*time.Second)
	defer cancel()
	result := directClient.chats(ctx, token)
	if result.Success {
		utils.Log("Telegram: looked for the bot's chats, found " + strconv.Itoa(len(result.Chats)))
	} else {
		utils.Warn("Telegram: the bot's chats could not be read: " + result.Message)
	}
	json.NewEncoder(w).Encode(utils.APIResponse{Status: "OK", Data: result})
}
