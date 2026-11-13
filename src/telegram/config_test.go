package telegram

import (
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestValidateConfig(t *testing.T) {
	good := func() utils.TelegramConfig {
		return utils.TelegramConfig{Enabled: true, BotToken: testToken, ChatID: testChat}
	}
	accepted := []func(*utils.TelegramConfig){
		func(c *utils.TelegramConfig) {},
		func(c *utils.TelegramConfig) { *c = utils.TelegramConfig{} }, // nothing set, switched off
		func(c *utils.TelegramConfig) { c.ChatID = "123456789" },
		func(c *utils.TelegramConfig) { c.ChatID = "@my_channel" },
		func(c *utils.TelegramConfig) { c.BotToken = "  " + testToken + "\n" }, // pasted with spaces
		func(c *utils.TelegramConfig) { c.TopicID = 7 },
		func(c *utils.TelegramConfig) { c.ServerName = "Home server (cellar)" },
		func(c *utils.TelegramConfig) { c.Categories = map[string]bool{"security": false, "logins": true} },
		func(c *utils.TelegramConfig) { c.MinSeverity = "medium" },
		func(c *utils.TelegramConfig) { c.QuietFrom, c.QuietTo = "23:00", "07:30" },
		func(c *utils.TelegramConfig) { c.MaxPerMinute = 15 },
		func(c *utils.TelegramConfig) { c.Enabled = false; c.BotToken = ""; c.ChatID = "" },
	}
	for i, change := range accepted {
		cfg := good()
		change(&cfg)
		if err := ValidateConfig(&cfg); err != nil {
			t.Errorf("accepted case %d refused: %v", i, err)
		}
	}

	refused := []func(*utils.TelegramConfig){
		func(c *utils.TelegramConfig) { c.BotToken = "" },
		func(c *utils.TelegramConfig) { c.ChatID = "" },
		func(c *utils.TelegramConfig) { c.BotToken = "not a token" },
		func(c *utils.TelegramConfig) { c.BotToken = testToken + "/getMe" },
		func(c *utils.TelegramConfig) { c.BotToken = "123456789:AAFake/../../FakeFakeFakeFakeFakeFake0" },
		func(c *utils.TelegramConfig) { c.BotToken = "123456789:AAFakeFakeFakeFakeFake?FakeFakeFake0" },
		func(c *utils.TelegramConfig) { c.BotToken = "***" },
		func(c *utils.TelegramConfig) { c.ChatID = "my chat" },
		func(c *utils.TelegramConfig) { c.ChatID = "https://t.me/someone" },
		func(c *utils.TelegramConfig) { c.ChatID = "***" },
		func(c *utils.TelegramConfig) { c.TopicID = -1 },
		func(c *utils.TelegramConfig) { c.TopicID = 1 << 40 },
		func(c *utils.TelegramConfig) { c.ServerName = strings.Repeat("n", 65) },
		func(c *utils.TelegramConfig) { c.ServerName = "two\nlines" },
		func(c *utils.TelegramConfig) { c.ServerName = "hidden\u202eoverride" },
		func(c *utils.TelegramConfig) { c.Categories = map[string]bool{"everything": true} },
		func(c *utils.TelegramConfig) { c.MinSeverity = "info" },
		func(c *utils.TelegramConfig) { c.QuietFrom = "23:00" },
		func(c *utils.TelegramConfig) { c.QuietFrom, c.QuietTo = "25:00", "07:00" },
		func(c *utils.TelegramConfig) { c.QuietFrom, c.QuietTo = "11pm", "7am" },
		func(c *utils.TelegramConfig) { c.MaxPerMinute = 16 },
		func(c *utils.TelegramConfig) { c.MaxPerMinute = -1 },
	}
	for i, change := range refused {
		cfg := good()
		change(&cfg)
		err := ValidateConfig(&cfg)
		if err == nil {
			t.Errorf("refused case %d accepted: %+v", i, cfg)
			continue
		}
		// the reason is shown to the administrator and logged: never with the token
		mustNotContain(t, "the reason", err.Error(), "AAFake")
	}

	// the plain form is what is stored
	cfg := good()
	cfg.BotToken, cfg.ChatID = " "+testToken+" ", " "+testChat+" "
	if err := ValidateConfig(&cfg); err != nil || cfg.BotToken != testToken || cfg.ChatID != testChat {
		t.Errorf("not trimmed: %q %q %v", cfg.BotToken, cfg.ChatID, err)
	}
}

func TestSettingsDefaults(t *testing.T) {
	st := testSettings()
	if !st.usable {
		t.Fatal("a token and a chat of the right shape are not usable")
	}
	want := map[string]bool{"security": true, "logins": false, "system": true, "storage": true, "apps": true, "other": true}
	for name, on := range want {
		if st.categories[name] != on {
			t.Errorf("category %s is %v by default, want %v", name, st.categories[name], on)
		}
	}
	if len(Categories()) != len(want) {
		t.Errorf("categories %v", Categories())
	}
	if st.minSeverity != "high" || st.maxPerMinute != defaultMaxPerMinute || st.hideDetails || st.quiet || st.silenceCrit {
		t.Errorf("defaults: %+v", st)
	}
	if st.serverName != "home.example" {
		t.Errorf("the server name is %q, want the Hostname setting", st.serverName)
	}

	config := testConfig()
	config.Telegram.Categories = map[string]bool{"security": false, "logins": true}
	config.Telegram.ServerName = "Cellar"
	config.Telegram.MaxPerMinute = 99 // edited by hand
	config.Telegram.MinSeverity = "nonsense"
	st = settingsOf(config)
	if st.categories["security"] || !st.categories["logins"] || !st.categories["system"] {
		t.Errorf("categories not taken from the settings: %v", st.categories)
	}
	if st.serverName != "Cellar" || st.maxPerMinute != defaultMaxPerMinute || st.minSeverity != "high" {
		t.Errorf("settings: %+v", st)
	}

	// what would not pass the checks is not used
	for _, change := range []func(*utils.Config){
		func(c *utils.Config) { c.Telegram.Enabled = false },
		func(c *utils.Config) { c.Telegram.BotToken = "" },
		func(c *utils.Config) { c.Telegram.BotToken = testToken + "/x" },
		func(c *utils.Config) { c.Telegram.ChatID = "somewhere" },
	} {
		config := testConfig()
		change(&config)
		if settingsOf(config).usable {
			t.Errorf("usable: %+v", config.Telegram)
		}
	}

	// the fingerprint follows the token, the chat and the topic, nothing else
	base := testSettings().fingerprint
	config = testConfig()
	config.Telegram.ServerName = "Other"
	if settingsOf(config).fingerprint != base {
		t.Error("the fingerprint changed with the server name")
	}
	config.Telegram.ChatID = "42"
	if settingsOf(config).fingerprint == base {
		t.Error("the fingerprint did not change with the chat")
	}
}

func TestQuietHours(t *testing.T) {
	config := testConfig()
	config.Telegram.QuietFrom, config.Telegram.QuietTo = "23:00", "07:00"
	st := settingsOf(config)
	for minute, quiet := range map[int]bool{23 * 60: true, 23*60 + 59: true, 0: true, 6*60 + 59: true, 7 * 60: false, 12 * 60: false, 22*60 + 59: false} {
		if st.inQuietHours(minute) != quiet {
			t.Errorf("over midnight, minute %d: quiet %v, want %v", minute, !quiet, quiet)
		}
	}

	config.Telegram.QuietFrom, config.Telegram.QuietTo = "13:00", "14:30"
	st = settingsOf(config)
	for minute, quiet := range map[int]bool{13 * 60: true, 14*60 + 29: true, 14*60 + 30: false, 12*60 + 59: false, 0: false} {
		if st.inQuietHours(minute) != quiet {
			t.Errorf("within a day, minute %d: quiet %v, want %v", minute, !quiet, quiet)
		}
	}

	// none set, half set, or the same time twice: no quiet hours
	for _, hours := range [][2]string{{"", ""}, {"23:00", ""}, {"08:00", "08:00"}, {"x", "y"}} {
		config.Telegram.QuietFrom, config.Telegram.QuietTo = hours[0], hours[1]
		if st := settingsOf(config); st.quiet || st.inQuietHours(0) {
			t.Errorf("quiet hours from %q", hours)
		}
	}
}
