package configapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: tests of fork_telegram.go. The settings are read and saved
// through the real handlers, the way the settings page does it.

const telegramTestToken = "123456789:AAFakeFakeFakeFakeFakeFakeFakeFake0"
const telegramOtherToken = "987654321:BBotherOtherOtherOtherOtherOtherOther"

// No test of this package sends anything: what would be told to a chat is
// kept here instead.
var (
	telegramChangesMu sync.Mutex
	telegramChanges   []string
)

func init() {
	announceTelegramChange = func(req *http.Request, before utils.Config, after utils.Config) {
		telegramChangesMu.Lock()
		defer telegramChangesMu.Unlock()
		telegramChanges = append(telegramChanges, before.Telegram.ChatID+" -> "+after.Telegram.ChatID)
	}
}

func telegramTestConfig(t *testing.T) chan struct{} {
	t.Helper()
	restarted := settingsTestServer(t)
	config := utils.DefaultConfig
	config.NewInstall = false
	config.HTTPConfig.Hostname = "172.16.0.14"
	config.HTTPConfig.HTTPPort = "2052"
	config.HTTPConfig.HTTPSPort = "2053"
	config.HTTPConfig.HTTPSCertificateMode = utils.HTTPSCertModeList["DISABLED"]
	config.EmailConfig.Port = "587"
	config.Telegram = utils.TelegramConfig{Enabled: true, BotToken: telegramTestToken, ChatID: "-1001234567890", ServerName: "Cellar"}
	utils.SetBaseMainConfig(config)
	return restarted
}

// as builds a request of a logged-in user with the given permissions.
func as(req *http.Request, permissions ...utils.Permission) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{
		Nickname: "someone", Permissions: permissions, IsSudoed: true,
	}))
}

func readSettings(t *testing.T, req *http.Request) (utils.Config, string) {
	t.Helper()
	w := httptest.NewRecorder()
	ConfigApiGet(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET config: %d %s", w.Code, w.Body.String())
	}
	var got struct {
		Data utils.Config `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got.Data, w.Body.String()
}

func saveSettings(t *testing.T, config utils.Config, restarted chan struct{}) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	ConfigApiSet(w, asAdmin(httptest.NewRequest("PUT", "http://172.16.0.14:2053/cosmos/api/config", bytes.NewReader(body))))
	if w.Code == http.StatusOK {
		<-restarted // the restart that follows a save is over
	}
	return w
}

func TestTelegramTokenIsMaskedForEveryReader(t *testing.T) {
	telegramTestConfig(t)
	get := func() *http.Request {
		return httptest.NewRequest("GET", "http://172.16.0.14:2053/cosmos/api/config", nil)
	}

	readers := map[string]*http.Request{
		"an administrator who confirmed the password": asAdmin(get()),
		"someone who may read credentials":            as(get(), utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ, utils.PERM_CREDENTIALS_READ),
		"someone who may read the configuration":      as(get(), utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ),
		"any logged-in user":                          as(get(), utils.PERM_LOGIN),
	}
	for who, req := range readers {
		config, raw := readSettings(t, req)
		if config.Telegram.BotToken != "***" {
			t.Errorf("%s gets the token %q, want ***", who, config.Telegram.BotToken)
		}
		if strings.Contains(raw, telegramTestToken) || strings.Contains(raw, "AAFakeFake") {
			t.Errorf("the answer to %s contains the token", who)
		}
		// the rest of the settings is there: the form needs it
		if !config.Telegram.Enabled || config.Telegram.ServerName != "Cellar" {
			t.Errorf("%s: settings %+v", who, config.Telegram)
		}
	}

	// the chat is shown to those who may read credentials, like the SMTP host
	if config, _ := readSettings(t, readers["an administrator who confirmed the password"]); config.Telegram.ChatID != "-1001234567890" {
		t.Errorf("an administrator gets the chat %q", config.Telegram.ChatID)
	}
	for _, who := range []string{"someone who may read the configuration", "any logged-in user"} {
		if config, raw := readSettings(t, readers[who]); config.Telegram.ChatID != "***" || strings.Contains(raw, "1001234567890") {
			t.Errorf("%s gets the chat %q", who, config.Telegram.ChatID)
		}
	}

	// without a token there is nothing to mask: the form can tell that none is stored
	config := utils.GetBaseMainConfig()
	config.Telegram = utils.TelegramConfig{}
	utils.SetBaseMainConfig(config)
	if config, _ := readSettings(t, asAdmin(get())); config.Telegram.BotToken != "" || config.Telegram.ChatID != "" {
		t.Errorf("nothing stored reads as %+v", config.Telegram)
	}
}

func TestTelegramTokenSurvivesASaveOfTheForm(t *testing.T) {
	restarted := telegramTestConfig(t)
	get := func() *http.Request {
		return asAdmin(httptest.NewRequest("GET", "http://172.16.0.14:2053/cosmos/api/config", nil))
	}

	// the settings page reads the settings and saves them whole, the token as the mask it got
	read, _ := readSettings(t, get())
	read.Telegram.ServerName = "Attic"
	if w := saveSettings(t, read, restarted); w.Code != http.StatusOK {
		t.Fatalf("PUT config: %d %s", w.Code, w.Body.String())
	}
	stored := utils.ReadConfigFromFile().Telegram
	if stored.BotToken != telegramTestToken || stored.ChatID != "-1001234567890" || stored.ServerName != "Attic" {
		t.Fatalf("after a save with the mask: %+v", stored)
	}
	if utils.GetMainConfig().Telegram.BotToken != telegramTestToken {
		t.Error("the configuration in use lost the token")
	}

	// someone who got the chat masked sends that mask back: the chat stays too
	read, _ = readSettings(t, get())
	read.Telegram.ChatID = "***"
	if w := saveSettings(t, read, restarted); w.Code != http.StatusOK {
		t.Fatalf("PUT config: %d %s", w.Code, w.Body.String())
	}
	if stored := utils.ReadConfigFromFile().Telegram; stored.ChatID != "-1001234567890" || stored.BotToken != telegramTestToken {
		t.Fatalf("after a save with a masked chat: %+v", stored)
	}

	// a token that was typed replaces the stored one
	read, _ = readSettings(t, get())
	read.Telegram.BotToken = "  " + telegramOtherToken + " "
	if w := saveSettings(t, read, restarted); w.Code != http.StatusOK {
		t.Fatalf("PUT config: %d %s", w.Code, w.Body.String())
	}
	if stored := utils.ReadConfigFromFile().Telegram; stored.BotToken != telegramOtherToken {
		t.Fatalf("after a save with a new token: %q", stored.BotToken)
	}

	// an empty token removes it, which only goes with the notifications off
	read, _ = readSettings(t, get())
	read.Telegram.BotToken = ""
	if w := saveSettings(t, read, restarted); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "bot token") {
		t.Fatalf("switched on without a token: %d %s", w.Code, w.Body.String())
	}
	read.Telegram.Enabled = false
	if w := saveSettings(t, read, restarted); w.Code != http.StatusOK {
		t.Fatalf("PUT config: %d %s", w.Code, w.Body.String())
	}
	if stored := utils.ReadConfigFromFile().Telegram; stored.BotToken != "" || stored.Enabled {
		t.Fatalf("after removing the token: %+v", stored)
	}
}

func TestTelegramSettingsAreCheckedOnSave(t *testing.T) {
	restarted := telegramTestConfig(t)
	read, _ := readSettings(t, asAdmin(httptest.NewRequest("GET", "http://172.16.0.14:2053/cosmos/api/config", nil)))

	refused := map[string]func(*utils.TelegramConfig){
		"a token that would change the request": func(c *utils.TelegramConfig) { c.BotToken = telegramOtherToken + "/../getUpdates" },
		"a chat that is an address":             func(c *utils.TelegramConfig) { c.ChatID = "https://evil.example/" },
		"an unknown category":                   func(c *utils.TelegramConfig) { c.Categories = map[string]bool{"all": true} },
		"more than fifteen a minute":            func(c *utils.TelegramConfig) { c.MaxPerMinute = 1000 },
		"half of the quiet hours":               func(c *utils.TelegramConfig) { c.QuietFrom = "23:00" },
	}
	for name, change := range refused {
		request := read
		change(&request.Telegram)
		w := saveSettings(t, request, restarted)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Telegram") {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "BBother") || strings.Contains(w.Body.String(), "AAFake") {
			t.Errorf("%s: the answer names a token: %s", name, w.Body.String())
		}
	}
	// nothing of a refused save was written
	if stored := utils.ReadConfigFromFile().Telegram; stored.BotToken != telegramTestToken || stored.ChatID != "-1001234567890" || stored.MaxPerMinute != 0 {
		t.Fatalf("a refused save changed the settings: %+v", stored)
	}

	// settings that were edited by hand into something unusable do not keep the rest from being saved
	config := utils.GetBaseMainConfig()
	config.Telegram.MinSeverity = "whatever"
	utils.SetBaseMainConfig(config)
	read, _ = readSettings(t, asAdmin(httptest.NewRequest("GET", "http://172.16.0.14:2053/cosmos/api/config", nil)))
	read.BackupFrequency = "daily"
	if w := saveSettings(t, read, restarted); w.Code != http.StatusOK {
		t.Fatalf("an unchanged section kept a save from going through: %d %s", w.Code, w.Body.String())
	}
}

func TestTelegramChatIsToldBeforeItLosesTheNotifications(t *testing.T) {
	restarted := telegramTestConfig(t)
	telegramChangesMu.Lock()
	telegramChanges = nil
	telegramChangesMu.Unlock()

	read, _ := readSettings(t, asAdmin(httptest.NewRequest("GET", "http://172.16.0.14:2053/cosmos/api/config", nil)))
	read.Telegram.ChatID = "666"
	if w := saveSettings(t, read, restarted); w.Code != http.StatusOK {
		t.Fatalf("PUT config: %d %s", w.Code, w.Body.String())
	}
	telegramChangesMu.Lock()
	defer telegramChangesMu.Unlock()
	// handed over with the settings as they were (token restored) and as they are
	if len(telegramChanges) != 1 || telegramChanges[0] != "-1001234567890 -> 666" {
		t.Errorf("what the save handed over: %q", telegramChanges)
	}
}

func TestTelegramSectionIsNamedInTheSecurityRecordWithoutValues(t *testing.T) {
	before := securityTestConfig()
	after := securityTestConfig()
	after.Telegram = utils.TelegramConfig{Enabled: true, BotToken: telegramTestToken, ChatID: "42"}
	sections := strings.Join(changedSections(before, after), ", ")
	if !strings.Contains(sections, "Telegram") || strings.Contains(sections, "AAFake") || strings.Contains(sections, "42") {
		t.Errorf("sections %q", sections)
	}
}
