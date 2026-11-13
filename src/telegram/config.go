// Package telegram sends notifications to one Telegram chat through the Bot
// API: the high-severity events of the security record, what Cosmos raises
// as an admin notification, and a few situations of its own (server updates,
// Cosmos starting again, a container that keeps stopping).
//
// Fork addition: nothing here exists upstream. The rest of the server reaches
// this package through utils (fork_notify.go) and through security.Subscribe;
// it is started once, by telegram.Start.
//
// What matters here, in this order: nothing in Cosmos ever waits for
// Telegram; the bot token appears in no log line, event, notification, error
// or API answer; a value that came from a request cannot change what a
// message says or how it is rendered; a flood cannot hide an alert.
package telegram

import (
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The categories a message can belong to. Each can be switched on or off in
// the settings.
const (
	CategorySecurity = "security"
	CategoryLogins   = "logins"
	CategorySystem   = "system"
	CategoryStorage  = "storage"
	CategoryApps     = "apps"
	CategoryOther    = "other"
)

// categoryDefaults says whether a category is on when the settings do not
// name it.
var categoryDefaults = map[string]bool{
	CategorySecurity: true,
	CategoryLogins:   false,
	CategorySystem:   true,
	CategoryStorage:  true,
	CategoryApps:     true,
	CategoryOther:    true,
}

// Categories lists the category names, sorted.
func Categories() []string {
	names := make([]string, 0, len(categoryDefaults))
	for name := range categoryDefaults {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// MaskedValue is what a client gets instead of a stored secret, and what it
// sends back to keep it.
const MaskedValue = "***"

const (
	defaultMaxPerMinute = 6
	maxMaxPerMinute     = 15
	maxServerName       = 64
	maxTopicID          = 1<<31 - 1
)

var (
	// A bot token is "<bot id>:<secret>". The pattern is also what keeps a
	// token from changing the path of the request it is put in: no "/", "?",
	// "#", "%" or "." can pass.
	tokenPattern = regexp.MustCompile(`^[0-9]{5,15}:[A-Za-z0-9_-]{20,80}$`)
	// A chat is a number (negative for groups and channels) or the public
	// name of a channel or group.
	chatNumberPattern = regexp.MustCompile(`^-?[0-9]{1,20}$`)
	chatNamePattern   = regexp.MustCompile(`^@[A-Za-z][A-Za-z0-9_]{3,31}$`)
	timePattern       = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
)

// ValidToken reports whether text has the shape of a bot token.
func ValidToken(text string) bool {
	return tokenPattern.MatchString(text)
}

// ValidChatID reports whether text has the shape of a chat id.
func ValidChatID(text string) bool {
	return chatNumberPattern.MatchString(text) || chatNamePattern.MatchString(text)
}

// ValidateConfig checks the Telegram settings and puts them in their plain
// form (spaces around values removed). The error is a sentence for the
// administrator; it never contains the token.
func ValidateConfig(cfg *utils.TelegramConfig) error {
	cfg.BotToken = strings.TrimSpace(cfg.BotToken)
	cfg.ChatID = strings.TrimSpace(cfg.ChatID)
	cfg.ServerName = strings.TrimSpace(cfg.ServerName)
	cfg.MinSeverity = strings.TrimSpace(cfg.MinSeverity)
	cfg.QuietFrom = strings.TrimSpace(cfg.QuietFrom)
	cfg.QuietTo = strings.TrimSpace(cfg.QuietTo)

	if cfg.BotToken != "" && !ValidToken(cfg.BotToken) {
		return errors.New("Telegram: this is not a bot token. Copy the whole token BotFather gave you: digits, a colon, then letters and digits")
	}
	if cfg.ChatID != "" && !ValidChatID(cfg.ChatID) {
		return errors.New("Telegram: the chat ID is a number such as 123456789 or -1001234567890, or the public name of a channel such as @mychannel")
	}
	if cfg.TopicID < 0 || cfg.TopicID > maxTopicID {
		return errors.New("Telegram: the topic ID is a positive number, or empty")
	}
	if utf8.RuneCountInString(cfg.ServerName) > maxServerName {
		return errors.New("Telegram: the server name is at most " + strconv.Itoa(maxServerName) + " characters")
	}
	for _, r := range cfg.ServerName {
		if !unicode.IsPrint(r) {
			return errors.New("Telegram: the server name must not contain line breaks or invisible characters")
		}
	}
	if len(cfg.Categories) > len(categoryDefaults) {
		return errors.New("Telegram: unknown category")
	}
	for name := range cfg.Categories {
		if _, known := categoryDefaults[name]; !known {
			return errors.New("Telegram: unknown category")
		}
	}
	switch cfg.MinSeverity {
	case "", "high", "medium", "low":
	default:
		return errors.New("Telegram: the minimum severity is high, medium or low")
	}
	if (cfg.QuietFrom == "") != (cfg.QuietTo == "") {
		return errors.New("Telegram: quiet hours need both a start and an end, or neither")
	}
	if cfg.QuietFrom != "" && (!timePattern.MatchString(cfg.QuietFrom) || !timePattern.MatchString(cfg.QuietTo)) {
		return errors.New("Telegram: enter the quiet hours as HH:MM, for example 23:00")
	}
	if cfg.MaxPerMinute < 0 || cfg.MaxPerMinute > maxMaxPerMinute {
		return errors.New("Telegram: the number of messages per minute is between 1 and " + strconv.Itoa(maxMaxPerMinute))
	}
	if cfg.Enabled && cfg.BotToken == "" {
		return errors.New("Telegram: enter the bot token, or switch the notifications off")
	}
	if cfg.Enabled && cfg.ChatID == "" {
		return errors.New("Telegram: enter the chat ID, or switch the notifications off")
	}
	return nil
}

// settings is the configuration as the notifier uses it: defaults filled
// in, times parsed.
type settings struct {
	enabled      bool
	token        string
	chatID       string
	topicID      int
	serverName   string
	categories   map[string]bool
	minSeverity  string
	hideDetails  bool
	quiet        bool
	quietFrom    int // minutes after midnight
	quietTo      int
	silenceCrit  bool
	maxPerMinute int
	usable       bool   // enabled, and token and chat have the right shape
	fingerprint  string // changes when the token, the chat or the topic change
}

// settingsOf reads the configuration. Settings that would not pass
// ValidateConfig (a file edited by hand) are not used: usable is false.
func settingsOf(config utils.Config) settings {
	cfg := config.Telegram
	st := settings{
		enabled:      cfg.Enabled,
		token:        strings.TrimSpace(cfg.BotToken),
		chatID:       strings.TrimSpace(cfg.ChatID),
		topicID:      cfg.TopicID,
		serverName:   strings.TrimSpace(cfg.ServerName),
		categories:   map[string]bool{},
		minSeverity:  cfg.MinSeverity,
		hideDetails:  cfg.HideDetails,
		silenceCrit:  cfg.QuietSilenceCritical,
		maxPerMinute: cfg.MaxPerMinute,
	}
	for name, byDefault := range categoryDefaults {
		st.categories[name] = byDefault
		if on, set := cfg.Categories[name]; set {
			st.categories[name] = on
		}
	}
	if st.serverName == "" {
		st.serverName = strings.TrimSpace(config.HTTPConfig.Hostname)
	}
	if st.serverName == "" {
		st.serverName = "Cosmos"
	}
	if st.minSeverity != "medium" && st.minSeverity != "low" {
		st.minSeverity = "high"
	}
	if st.maxPerMinute < 1 || st.maxPerMinute > maxMaxPerMinute {
		st.maxPerMinute = defaultMaxPerMinute
	}
	if st.topicID < 0 || st.topicID > maxTopicID {
		st.topicID = 0
	}
	if from, okFrom := minutesOf(cfg.QuietFrom); okFrom {
		if to, okTo := minutesOf(cfg.QuietTo); okTo && from != to {
			st.quiet, st.quietFrom, st.quietTo = true, from, to
		}
	}
	st.usable = st.enabled && ValidToken(st.token) && ValidChatID(st.chatID)
	st.fingerprint = st.token + "\x00" + st.chatID + "\x00" + strconv.Itoa(st.topicID)
	return st
}

// minutesOf reads "HH:MM" as minutes after midnight.
func minutesOf(text string) (int, bool) {
	text = strings.TrimSpace(text)
	if !timePattern.MatchString(text) {
		return 0, false
	}
	hours, _ := strconv.Atoi(text[:2])
	minutes, _ := strconv.Atoi(text[3:])
	return hours*60 + minutes, true
}

// inQuietHours reports whether the minute of the day lies in the quiet
// hours, which may span midnight.
func (st settings) inQuietHours(minuteOfDay int) bool {
	if !st.quiet {
		return false
	}
	if st.quietFrom < st.quietTo {
		return minuteOfDay >= st.quietFrom && minuteOfDay < st.quietTo
	}
	return minuteOfDay >= st.quietFrom || minuteOfDay < st.quietTo
}
