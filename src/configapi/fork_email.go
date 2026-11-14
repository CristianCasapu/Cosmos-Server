package configapi

// Fork addition: the email (SMTP) settings as the server guards them.
//
//   - The stored SMTP password is never sent to the browser, also not to an
//     account that may read credentials: the settings page gets "***" and
//     sends it back to mean "keep it" (hook of ConfigApiGet).
//   - "***" keeps the stored password only for the connection it was saved
//     for. Changing the server, the port, the user name or the encryption
//     needs the password typed again; otherwise a session could have the
//     stored password sent to a server of its choosing by saving another
//     host name. The same rule holds for "send a test email".
//   - When the email settings change, they are trimmed and checked, so that
//     a value that cannot work is refused at the save and not at the first
//     mail. Unchanged settings are left alone: an older configuration still
//     saves.

import (
	"errors"
	"net/mail"
	"strconv"
	"strings"
	"unicode"

	"github.com/azukaar/cosmos-server/src/utils"
)

const emailSecretMask = "***"

// maskEmailPassword is the hook of ConfigApiGet.
func maskEmailPassword(config *utils.Config) {
	if config.EmailConfig.Password != "" {
		config.EmailConfig.Password = emailSecretMask
	}
}

// sameEmailConnection reports whether cfg reaches the same server, as the
// same user, at least as safely as saved: the connection the stored
// password may be used for.
func sameEmailConnection(cfg, saved utils.EmailConfig) bool {
	return strings.TrimSpace(cfg.Host) == strings.TrimSpace(saved.Host) &&
		strings.TrimSpace(cfg.Port) == strings.TrimSpace(saved.Port) &&
		cfg.Username == saved.Username && cfg.UseTLS == saved.UseTLS &&
		(!cfg.AllowInsecureTLS || saved.AllowInsecureTLS)
}

func emailHasControl(text string) bool {
	return strings.IndexFunc(text, unicode.IsControl) >= 0
}

// validateEmailConfig checks settings that are about to be used or saved.
func validateEmailConfig(cfg utils.EmailConfig) error {
	for _, value := range []string{cfg.Host, cfg.Port, cfg.Username, cfg.Password, cfg.From} {
		if emailHasControl(value) {
			return errors.New("The email settings must not contain line breaks or control characters")
		}
	}
	if len(cfg.Host) > 253 || len(cfg.Username) > 320 || len(cfg.Password) > 1024 || len(cfg.From) > 400 {
		return errors.New("A value of the email settings is too long")
	}
	if !cfg.Enabled {
		// not in use: half-filled settings may be kept for later
		return nil
	}
	if cfg.Host == "" || strings.IndexFunc(cfg.Host, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && !strings.ContainsRune(".-_:", r)
	}) >= 0 {
		return errors.New("Enter the name of the mail server, such as smtp.example.com, without spaces")
	}
	if port, err := strconv.Atoi(cfg.Port); err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != cfg.Port {
		return errors.New("The mail server's port must be a number between 1 and 65535")
	}
	if _, err := mail.ParseAddress(cfg.From); err != nil {
		return errors.New("The sender must be an email address, such as cosmos@example.com")
	}
	return nil
}

// checkEmailSettings is the hook of ConfigApiSet, called before upstream
// puts the stored values back for the fields that arrive as "***".
func checkEmailSettings(request *utils.Config, saved utils.Config) error {
	old := saved.EmailConfig
	next := request.EmailConfig

	// what the request means, with the masks resolved
	meant := next
	if meant.Host == emailSecretMask {
		meant.Host = old.Host
	}
	if meant.Username == emailSecretMask {
		meant.Username = old.Username
	}
	keepsPassword := next.Password == emailSecretMask
	if keepsPassword {
		meant.Password = old.Password
	}
	if meant == old {
		return nil
	}

	if keepsPassword && old.Password != "" && !sameEmailConnection(meant, old) {
		return errors.New("The mail server, port, user name or encryption changed: enter the mail password again")
	}

	// trimmed as they will be used; a mask stays a mask for upstream's restore
	trim := func(field *string, meantField *string) {
		if *field != emailSecretMask {
			*field = strings.TrimSpace(*field)
			*meantField = *field
		}
	}
	trim(&request.EmailConfig.Host, &meant.Host)
	trim(&request.EmailConfig.Port, &meant.Port)
	trim(&request.EmailConfig.Username, &meant.Username)
	trim(&request.EmailConfig.From, &meant.From)

	return validateEmailConfig(meant)
}
