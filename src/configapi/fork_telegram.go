package configapi

// Fork addition: what the settings code does for the Telegram notifications
// (utils.TelegramConfig, package telegram). The bot token is a secret: it
// never leaves the server through the configuration, whoever asks.

import (
	"net/http"

	"github.com/azukaar/cosmos-server/src/telegram"
	"github.com/azukaar/cosmos-server/src/utils"
)

// maskTelegram hides the Telegram secrets in a configuration that is about
// to be sent to a client. The token is masked for everyone: nothing in the
// UI needs it back, and "***" tells the form that one is stored. The chat
// is masked for those who may not read credentials, like the SMTP host.
func maskTelegram(config *utils.Config, canReadCredentials bool) {
	if config.Telegram.BotToken != "" {
		config.Telegram.BotToken = telegram.MaskedValue
	}
	if !canReadCredentials && config.Telegram.ChatID != "" {
		config.Telegram.ChatID = telegram.MaskedValue
	}
}

// keepTelegramSecrets puts back what a client could not have: a token or a
// chat that comes back masked is the stored one. Settings that changed are
// then checked; the error is a sentence for the administrator. Unchanged
// settings are left alone, so that a file edited by hand does not keep
// everything else from being saved.
func keepTelegramSecrets(request *utils.Config, stored utils.Config) error {
	if request.Telegram.BotToken == telegram.MaskedValue {
		request.Telegram.BotToken = stored.Telegram.BotToken
	}
	if request.Telegram.ChatID == telegram.MaskedValue {
		request.Telegram.ChatID = stored.Telegram.ChatID
	}
	if sameSetting(request.Telegram, stored.Telegram) {
		return nil
	}
	return telegram.ValidateConfig(&request.Telegram)
}

// announceTelegramChange tells the chat that received the notifications so
// far when a save takes them away from it.
func announceTelegramChange(req *http.Request, before utils.Config, after utils.Config) {
	telegram.AnnounceSettingsChange(before, after, utils.GetAuthContext(req).Nickname, utils.GetClientIP(req))
}
