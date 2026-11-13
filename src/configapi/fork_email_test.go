package configapi

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: tests of fork_email.go and of the bounds of the "send a
// test email" route, through the real handlers.

const storedMailPassword = "S3CRET-smtp-password"

func emailTestServer(t *testing.T, email utils.EmailConfig) chan struct{} {
	t.Helper()
	restarted := settingsTestServer(t)
	config := utils.DefaultConfig
	config.NewInstall = false
	config.HTTPConfig.Hostname = "cosmos.example.com"
	config.HTTPConfig.HTTPPort = "2052"
	config.HTTPConfig.HTTPSPort = "2053"
	config.EmailConfig = email
	utils.SaveConfigTofile(config)
	utils.LoadBaseMainConfig(config)
	return restarted
}

func savedMail() utils.EmailConfig {
	return utils.EmailConfig{
		Enabled: true, Host: "smtp.example.com", Port: "587", Username: "cosmos@example.com",
		Password: storedMailPassword, From: "cosmos@example.com", UseTLS: true,
	}
}

func readSettings(t *testing.T, as func(*http.Request) *http.Request) (utils.Config, string) {
	t.Helper()
	w := httptest.NewRecorder()
	ConfigApiGet(w, as(httptest.NewRequest("GET", "https://cosmos.example.com/cosmos/api/config", nil)))
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

// saveSettings sends the settings back the way the page does and, after a
// save that was accepted, waits for the restart that follows it.
func saveSettings(t *testing.T, restarted chan struct{}, config utils.Config) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	ConfigApiSet(w, asAdmin(httptest.NewRequest("PUT", "https://cosmos.example.com/cosmos/api/config", bytes.NewReader(body))))
	if w.Code == http.StatusOK {
		select {
		case <-restarted:
		case <-time.After(10 * time.Second):
			t.Fatal("the restart that follows a save did not finish")
		}
	}
	return w
}

func TestMailPasswordIsNeverSentToTheBrowser(t *testing.T) {
	emailTestServer(t, savedMail())

	// an administrator who confirmed the password may read credentials: still no SMTP password
	config, raw := readSettings(t, asAdmin)
	if strings.Contains(raw, storedMailPassword) {
		t.Fatal("the stored SMTP password was sent to an administrator's browser")
	}
	if config.EmailConfig.Password != "***" || config.EmailConfig.Host != "smtp.example.com" || config.EmailConfig.Username != "cosmos@example.com" {
		t.Errorf("administrator sees %+v", config.EmailConfig)
	}

	// without the password confirmed: host and user name masked too, as before
	config, raw = readSettings(t, asAdminWithoutPassword)
	if strings.Contains(raw, storedMailPassword) || config.EmailConfig.Password != "***" || config.EmailConfig.Host != "***" || config.EmailConfig.Username != "***" {
		t.Errorf("unconfirmed administrator sees %+v", config.EmailConfig)
	}

	// no password stored: the page must be able to tell
	mail := savedMail()
	mail.Password = ""
	emailTestServer(t, mail)
	if config, _ = readSettings(t, asAdmin); config.EmailConfig.Password != "" {
		t.Errorf("an empty password is shown as %q", config.EmailConfig.Password)
	}
}

func TestSaveKeepsTheStoredPasswordForTheSameConnection(t *testing.T) {
	restarted := emailTestServer(t, savedMail())

	config, _ := readSettings(t, asAdmin)
	config.EmailConfig.From = "Cosmos at home <cosmos@example.com>"
	config.EmailConfig.NotifyLogin = true
	if w := saveSettings(t, restarted, config); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	saved := utils.ReadConfigFromFile().EmailConfig
	if saved.Password != storedMailPassword || saved.From != "Cosmos at home <cosmos@example.com>" || !saved.NotifyLogin {
		t.Errorf("saved: %+v", saved)
	}

	// stricter certificate checking is not a weaker connection
	mail := savedMail()
	mail.AllowInsecureTLS = true
	restarted = emailTestServer(t, mail)
	config, _ = readSettings(t, asAdmin)
	config.EmailConfig.AllowInsecureTLS = false
	if w := saveSettings(t, restarted, config); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if saved := utils.ReadConfigFromFile().EmailConfig; saved.Password != storedMailPassword || saved.AllowInsecureTLS {
		t.Errorf("saved: %+v", saved)
	}
}

func TestSaveNeedsThePasswordAgainForAnotherConnection(t *testing.T) {
	changes := map[string]func(*utils.EmailConfig){
		"another server":            func(c *utils.EmailConfig) { c.Host = "smtp.attacker.example" },
		"another port":              func(c *utils.EmailConfig) { c.Port = "2525" },
		"another user":              func(c *utils.EmailConfig) { c.Username = "someone@example.com" },
		"encryption off":            func(c *utils.EmailConfig) { c.UseTLS = false },
		"certificate checking off":  func(c *utils.EmailConfig) { c.AllowInsecureTLS = true },
		"server and sender at once": func(c *utils.EmailConfig) { c.Host, c.From = "mail.example.net", "x@example.net" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			restarted := emailTestServer(t, savedMail())
			config, _ := readSettings(t, asAdmin)
			change(&config.EmailConfig)

			w := saveSettings(t, restarted, config)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "UC011") || !strings.Contains(w.Body.String(), "enter the mail password again") {
				t.Fatalf("save: %d %s", w.Code, w.Body.String())
			}
			if saved := utils.ReadConfigFromFile().EmailConfig; saved != savedMail() {
				t.Errorf("a refused save changed the settings: %+v", saved)
			}

			// with the password typed, the same change is saved
			config.EmailConfig.Password = "typed-again"
			if w := saveSettings(t, restarted, config); w.Code != http.StatusOK {
				t.Fatalf("save with the password: %d %s", w.Code, w.Body.String())
			}
			if saved := utils.ReadConfigFromFile().EmailConfig; saved.Password != "typed-again" {
				t.Errorf("saved: %+v", saved)
			}
		})
	}

	// nothing stored, nothing to protect: the mask means "none"
	mail := savedMail()
	mail.Password, mail.Username = "", ""
	restarted := emailTestServer(t, mail)
	config, _ := readSettings(t, asAdmin)
	config.EmailConfig.Host, config.EmailConfig.Password = "relay.example.net", "***"
	if w := saveSettings(t, restarted, config); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if saved := utils.ReadConfigFromFile().EmailConfig; saved.Password != "" || saved.Host != "relay.example.net" {
		t.Errorf("saved: %+v", saved)
	}
}

func TestSaveChecksEmailSettingsThatChanged(t *testing.T) {
	refused := map[string]func(*utils.EmailConfig){
		"port not a number":     func(c *utils.EmailConfig) { c.Port = "smtp" },
		"port out of range":     func(c *utils.EmailConfig) { c.Port = "70000" },
		"port zero":             func(c *utils.EmailConfig) { c.Port = "0" },
		"port with a sign":      func(c *utils.EmailConfig) { c.Port = "+587" },
		"host with a space":     func(c *utils.EmailConfig) { c.Host = "smtp example.com" },
		"host with a path":      func(c *utils.EmailConfig) { c.Host = "smtp.example.com/x" },
		"host empty":            func(c *utils.EmailConfig) { c.Host = "  " },
		"sender not an address": func(c *utils.EmailConfig) { c.From = "Cosmos" },
		"line break in sender":  func(c *utils.EmailConfig) { c.From = "a@example.com\r\nBcc: b@example.com" },
		"line break in user":    func(c *utils.EmailConfig) { c.Username = "me\r\nMAIL FROM:<x@example.com>" },
		"line break in host":    func(c *utils.EmailConfig) { c.Host = "smtp.example.com\nx" },
		"endless host":          func(c *utils.EmailConfig) { c.Host = strings.Repeat("a", 300) },
	}
	for name, change := range refused {
		t.Run(name, func(t *testing.T) {
			restarted := emailTestServer(t, savedMail())
			config, _ := readSettings(t, asAdmin)
			config.EmailConfig.Password = "typed" // so that only the value under test decides
			change(&config.EmailConfig)
			if w := saveSettings(t, restarted, config); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "UC011") {
				t.Fatalf("save: %d %s", w.Code, w.Body.String())
			}
			if saved := utils.ReadConfigFromFile().EmailConfig; saved != savedMail() {
				t.Errorf("a refused save changed the settings: %+v", saved)
			}
		})
	}

	// spaces around a value are removed, not refused
	restarted := emailTestServer(t, savedMail())
	config, _ := readSettings(t, asAdmin)
	config.EmailConfig = utils.EmailConfig{Enabled: true, Host: " mail.example.net ", Port: " 465 ", Username: " me@example.net ",
		Password: " kept as typed ", From: " Me <me@example.net> ", UseTLS: true}
	if w := saveSettings(t, restarted, config); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	want := utils.EmailConfig{Enabled: true, Host: "mail.example.net", Port: "465", Username: "me@example.net",
		Password: " kept as typed ", From: "Me <me@example.net>", UseTLS: true}
	if saved := utils.ReadConfigFromFile().EmailConfig; saved != want {
		t.Errorf("saved: %+v", saved)
	}

	// switched off: half-filled settings may wait
	restarted = emailTestServer(t, utils.EmailConfig{})
	config, _ = readSettings(t, asAdmin)
	config.EmailConfig = utils.EmailConfig{Host: "smtp.example.com", From: "later"}
	if w := saveSettings(t, restarted, config); w.Code != http.StatusOK {
		t.Fatalf("save of settings that are switched off: %d %s", w.Code, w.Body.String())
	}
}

func TestOlderEmailSettingsStillSaveUnchanged(t *testing.T) {
	// what an older version accepted: saved long ago, must not block other settings now
	odd := utils.EmailConfig{Enabled: true, Host: "smtp.example.com ", Port: "", Username: "me", Password: "pw", From: "Cosmos"}
	restarted := emailTestServer(t, odd)
	config, _ := readSettings(t, asAdmin)
	config.LoggingLevel = "DEBUG"
	if w := saveSettings(t, restarted, config); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if saved := utils.ReadConfigFromFile(); saved.EmailConfig != odd || saved.LoggingLevel != "DEBUG" {
		t.Errorf("saved: %+v", saved.EmailConfig)
	}
}

// ---- "send a test email" ----------------------------------------------------

// mailbox is a mail server on loopback that accepts one account and notes
// the passwords it was given.
type mailbox struct {
	mu        sync.Mutex
	passwords []string
	sessions  int
}

func (m *mailbox) start(t *testing.T) (host, port string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			m.mu.Lock()
			m.sessions++
			m.mu.Unlock()
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				reader := bufio.NewReader(conn)
				say := func(line string) { conn.Write([]byte(line + "\r\n")) }
				say("220 test ESMTP")
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimSpace(line)
					upper := strings.ToUpper(line)
					switch {
					case strings.HasPrefix(upper, "EHLO"):
						say("250-test")
						say("250 AUTH PLAIN")
					case strings.HasPrefix(upper, "AUTH PLAIN "):
						raw, _ := base64.StdEncoding.DecodeString(line[len("AUTH PLAIN "):])
						parts := strings.Split(string(raw), "\x00")
						password := parts[len(parts)-1]
						m.mu.Lock()
						m.passwords = append(m.passwords, password)
						m.mu.Unlock()
						if password == storedMailPassword {
							say("235 ok")
						} else {
							say("535 5.7.8 Username and Password not accepted")
						}
					case strings.HasPrefix(upper, "MAIL FROM"), strings.HasPrefix(upper, "RCPT TO"):
						say("250 ok")
					case upper == "DATA":
						say("354 go")
						for {
							l, err := reader.ReadString('\n')
							if err != nil || l == ".\r\n" {
								break
							}
						}
						say("250 queued")
					case upper == "QUIT":
						say("221 bye")
						return
					default:
						say("502 no")
					}
				}
			}()
		}
	}()
	host, port, _ = net.SplitHostPort(ln.Addr().String())
	return host, port
}

func (m *mailbox) seen() (int, []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions, append([]string{}, m.passwords...)
}

func testEmail(t *testing.T, body string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	ConfigApiTestEmail(w, asAdmin(httptest.NewRequest("POST", "https://cosmos.example.com/cosmos/api/config/test-email", strings.NewReader(body))))
	return w.Code, w.Body.String()
}

func TestEmailTestUsesTheStoredPasswordOnlyForTheSavedConnection(t *testing.T) {
	saved, other := &mailbox{}, &mailbox{}
	host, port := saved.start(t)
	_, otherPort := other.start(t)
	mail := utils.EmailConfig{Enabled: true, Host: host, Port: port, Username: "cosmos", Password: storedMailPassword, From: "cosmos@example.com"}
	emailTestServer(t, mail)

	request := func(change func(*utils.EmailConfig)) string {
		cfg := mail
		cfg.Password = "***"
		change(&cfg)
		body, _ := json.Marshal(map[string]interface{}{"EmailConfig": cfg, "Recipient": "admin@example.com"})
		return string(body)
	}

	// the saved connection: the stored password is used, and not sent back
	code, answer := testEmail(t, request(func(c *utils.EmailConfig) {}))
	if code != http.StatusOK || !strings.Contains(answer, `"success":true`) || strings.Contains(answer, storedMailPassword) {
		t.Fatalf("saved connection: %d %s", code, answer)
	}
	if _, passwords := saved.seen(); len(passwords) != 1 || passwords[0] != storedMailPassword {
		t.Fatalf("the saved server was given %v", passwords)
	}

	// another port (another server): refused before any connection
	code, answer = testEmail(t, request(func(c *utils.EmailConfig) { c.Port = otherPort }))
	if code != http.StatusBadRequest || !strings.Contains(answer, "TE003") {
		t.Errorf("another port: %d %s", code, answer)
	}
	code, answer = testEmail(t, request(func(c *utils.EmailConfig) { c.Username = "someone-else" }))
	if code != http.StatusBadRequest || !strings.Contains(answer, "TE003") {
		t.Errorf("another user: %d %s", code, answer)
	}
	code, answer = testEmail(t, request(func(c *utils.EmailConfig) { c.AllowInsecureTLS = true }))
	if code != http.StatusBadRequest || !strings.Contains(answer, "TE003") {
		t.Errorf("weaker certificate checking: %d %s", code, answer)
	}
	if sessions, passwords := other.seen(); sessions != 0 || len(passwords) != 0 {
		t.Errorf("the other server was contacted: %d sessions, %v", sessions, passwords)
	}

	// a typed password goes where the form says, with the reason of a failure as a code
	code, answer = testEmail(t, request(func(c *utils.EmailConfig) { c.Port, c.Password = otherPort, "typed-wrong" }))
	if code != http.StatusOK || !strings.Contains(answer, `"success":false`) || !strings.Contains(answer, `"code":"auth-rejected"`) {
		t.Errorf("typed password: %d %s", code, answer)
	}
	if _, passwords := other.seen(); len(passwords) != 1 || passwords[0] != "typed-wrong" {
		t.Errorf("the other server was given %v", passwords)
	}
}

func TestEmailTestBoundsItsInput(t *testing.T) {
	box := &mailbox{}
	host, port := box.start(t)
	emailTestServer(t, utils.EmailConfig{})
	good := `"EmailConfig":{"Host":"` + host + `","Port":"` + port + `","From":"cosmos@example.com"}`

	for name, body := range map[string]string{
		"too large":             `{` + good + `,"Recipient":"admin@example.com","Padding":"` + strings.Repeat("a", 20000) + `"}`,
		"two recipients":        `{` + good + `,"Recipient":"a@example.com, b@example.com"}`,
		"recipient with a line": `{` + good + `,"Recipient":"a@example.com\r\nRCPT TO:<b@example.com>"}`,
		"not an address":        `{` + good + `,"Recipient":"nobody"}`,
		"port not a number":     `{"EmailConfig":{"Host":"` + host + `","Port":"25; x","From":"cosmos@example.com"}}`,
		"host with a space":     `{"EmailConfig":{"Host":"` + host + ` x","Port":"` + port + `","From":"cosmos@example.com"}}`,
		"sender not an address": `{"EmailConfig":{"Host":"` + host + `","Port":"` + port + `","From":"Cosmos"}}`,
		"nothing":               `{}`,
	} {
		if code, answer := testEmail(t, body); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, code, answer)
		}
	}
	if sessions, _ := box.seen(); sessions != 0 {
		t.Errorf("a refused request reached the mail server (%d sessions)", sessions)
	}

	// and the plain case works, to the sender's own address when none is given
	code, answer := testEmail(t, `{"EmailConfig":{"Host":" `+host+` ","Port":"`+port+`","From":"Cosmos at home <cosmos@example.com>"}}`)
	if code != http.StatusOK || !strings.Contains(answer, `"success":true`) || !strings.Contains(answer, `"recipient":"cosmos@example.com"`) {
		t.Errorf("plain test: %d %s", code, answer)
	}
}
