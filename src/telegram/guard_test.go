package telegram

import (
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The shapes secrets take in error texts and command lines (plan 48, review S2).
func TestTheGuardCutsCommandLineAndServiceSecrets(t *testing.T) {
	cases := map[string]string{
		"restic --password hunter2xyz backup /data":                          "hunter2xyz",
		"passwd hunter2abc":                                                  "hunter2abc",
		"mysqldump -u root -pS3cretPassw0rd db":                              "S3cretPassw0rd",
		"mysql -h db -u app -pAnotherOne9 x":                                 "AnotherOne9",
		"curl -u admin:Secr3tPass https://example.org":                       "Secr3tPass",
		"curl --user admin:Secr3tPass2 https://example.org":                  "Secr3tPass2",
		"SECRET_KEY_BASE=abcdef0123456789 rails s":                           "abcdef0123456789",
		"ENCRYPTION_KEY=plainvalue42":                                        "plainvalue42",
		"DB_PASS='quoted value' x":                                           "quoted value",
		`password = "two words here"`:                                        "words here",
		"postgres://u:pa/ss@db.example.org/x":                                "pa/ss",
		"mongodb://u:p@ssw0rd@db.example.org/x":                              "ssw0rd",
		"token ghp_abcdefghijklmnopqrstuvwxyz0123":                           "ghp_abcdefghij",
		"github_pat_11ABCDEFG0abcdefghijklmnop":                              "github_pat_11ABCDEFG0",
		"key sk-abcdefghijklmnopqrstuv":                                      "sk-abcdefghijklmnop",
		"xoxb-123456789012-abcdefghijkl":                                     "xoxb-123456789012",
		"AKIAABCDEFGHIJKLMNOP is the key":                                    "AKIAABCDEFGHIJKLMNOP",
		"post to https://hooks.slack.com/services/T000/B000/XXXXXXXX failed": "T000/B000",
		"https://discord.com/api/webhooks/123/abcDEF failed":                 "abcDEF",
		"https://ntfy.example.org/hooks/abc123def failed":                    "abc123def",
		"Authorization: Bearer abcdefgh12345678":                             "abcdefgh12345678",
		"GET /api?x=1&signature=abc123sig&y=2":                               "abc123sig",
		"GET /api?access_token=tok987654":                                    "tok987654",
	}
	for text, secret := range cases {
		if got := guardValue(text, nil); strings.Contains(got, secret) {
			t.Errorf("%q kept %q: %q", text, secret, got)
		}
	}
	// ordinary words, hexadecimal identifiers and addresses stay
	for _, text := range []string{
		"the container 3f9a1c2b4d5e6f7081928374655647382910abcdef0123456789abcdef012345 stopped",
		"exit status 137",
		"Get \"https://www.example.org/status\": dial tcp 192.0.2.7:443: connection refused",
		"no space left on device",
	} {
		if got := guardValue(text, nil); got != text {
			t.Errorf("%q became %q", text, got)
		}
	}
}

// The second line: a secret the first pass misses still keeps every value
// out of the message (review S3).
func TestASecretTheFirstPassMissesSendsTheMessageWithoutValues(t *testing.T) {
	previous := firstPass
	firstPass = func(text string, secrets []string) string { return text } // the first pass misses everything
	defer func() { firstPass = previous }()

	st := testSettings()
	nt := sampleNotice("container.dying")
	nt.sample = false
	nt.vars["name"] = "app password=SuperSecret123"
	m := render(st, nt)
	if strings.Contains(m.text, "SuperSecret123") || !strings.Contains(m.text, word("hidden")) || m.button != nil {
		t.Fatalf("the fallback did not happen:\n%s", m.text)
	}
	// and an ordinary message is not taken for a leak
	firstPass = previous
	if m := render(st, sampleNotice("container.dying")); strings.Contains(m.text, word("hidden")) {
		t.Errorf("an ordinary message fell back:\n%s", m.text)
	}
}

// Review R2/R3 (second round): shapes that were missed, and real-life values that must survive.
func TestTheGuardSecondRound(t *testing.T) {
	cut := map[string]string{
		`{"db_password":"Pw0rd-db-9"}`:                         "Pw0rd-db-9",
		"secret_access_key = AbCdEf0123456789":                 "AbCdEf0123456789",
		"secret_key_base: 0123abcdef":                          "0123abcdef",
		"DB_PASSWORD: hunter22x":                               "hunter22x",
		"db_password: \"two words\"":                           "two words",
		"--db-password hunter33y":                              "hunter33y",
		"dial root:pw-in-dsn@tcp(db:3306)/app":                 "pw-in-dsn",
		"https://ghtokenabcdefghijklmnop12@github.com/x/y.git": "ghtokenabcdefghijklmnop12",
		"https://app.example.org/cb#access_token=frag-tok-77":  "frag-tok-77",
		"sshpass -p S3cr3tssh ssh host":                        "S3cr3tssh",
		"admin:$apr1$abcdefgh$0123456789abcdefghijkl":          "abcdefgh$0123",
		"docker run -e POSTGRES_PASSWORD pgpass-55 postgres":   "pgpass-55",
	}
	for text, secret := range cut {
		if got := guardValue(text, nil); strings.Contains(got, secret) {
			t.Errorf("%q kept %q: %q", text, secret, got)
		}
	}
	// what people and machines name things: never cut, in values or in error texts
	keep := []string{
		"/mnt/Data1/Backups/Nextcloud",
		"/srv/Photos2024/IMG_Family",
		"/dev/disk/by-id/ata-WDC_WD40EFRX-68N32N0_WD-WCC7K4ABCD12",
		"HomeAssistant2024ProductionServer",
		"restic: repository at /mnt/backup/Restic2024Repo is locked",
		"the password was changed",
		"Token expired",
		"ghcr.io/home-assistant/home-assistant:2024.11.2",
		"sha256:3f9a1c2b4d5e6f7081928374655647382910abcdef0123456789abcdef012345",
		"123e4567-e89b-12d3-a456-426614174000",
		"jellyfin-cosmos-old-1a2b3c",
		"WDC WD40EFRX-68N32N0",
		"nextcloud_db_1",
		"/var/lib/docker/volumes/nextcloud_data/_data",
		"linuxserver/qbittorrent:4.6.7-r0-ls355",
		"MediaServer_Jellyfin_Main_Library",
		"AAAAbbbb1111AAAAbbbb1111AAAAbbbb1111", // every class, but a repeated pattern: no key
	}
	for _, text := range keep {
		if got := guardValue(text, nil); got != text {
			t.Errorf("value %q became %q", text, got)
		}
		if got := guardRaw(text, nil); got != text {
			t.Errorf("error text %q became %q", text, got)
		}
	}
	// a machine-made key in an error text is cut
	if got := guardRaw("upload failed with key Zx9Kq2Lm8Np4Rs7Tv1Wy3Ab6CdEf5Gh0Jk", nil); strings.Contains(got, "Zx9Kq2Lm8Np4") {
		t.Errorf("%q", got)
	}
}

// Cutting twice gives what cutting once gave: the second line never empties a clean message.
func TestTheGuardIsIdempotent(t *testing.T) {
	for _, text := range []string{
		`{"password":"x"}`, `password=[hidden]`, "a (token=abc) b", "[key: value]", `{"db_password":"y","user":"z"}`,
		"curl -u a:b http://x", "https://u:p@host/x?token=1#sig=2", "--password p --token t", "DB_PASSWORD: q",
	} {
		once := guardRaw(text, nil)
		if twice := guardRaw(once, nil); twice != once {
			t.Errorf("%q: once %q, twice %q", text, once, twice)
		}
	}
	st := testSettings()
	nt := sampleNotice("error.backup")
	nt.sample = false
	nt.raw = `upload: {"password":"x"} refused`
	if m := render(st, nt); strings.Contains(m.text, word("hidden")) || !strings.Contains(m.text, "refused") {
		t.Errorf("a clean message fell back:\n%s", m.text)
	}
}

// A failed scheduled job sends no error text; the setting leaves out every error text;
// an error text is its first line only.
func TestErrorTextsAreBounded(t *testing.T) {
	config := testConfig()
	n := utils.Notification{Recipient: "admin", Level: "error", Fork: &utils.NotificationInfo{
		Kind: "error.job", What: utils.NText("notif.error.job.what", "name", "nightly"), Raw: "CRON job nightly failed: sh -c 'mysql -u root -pX'",
	}}
	nt := noticeFromInfo(n, config)
	if text, _ := textOf(t, settingsOf(config), nt); strings.Contains(text, "mysql") || strings.Contains(text, word("raw")) {
		t.Errorf("a job's error text was sent:\n%s", text)
	}
	backup := sampleNotice("error.backup")
	backup.sample, backup.raw = false, "first line\nsecond line with more"
	text, _ := textOf(t, settingsOf(config), backup)
	if !strings.Contains(text, "first line") || strings.Contains(text, "second line") {
		t.Errorf("not the first line only:\n%s", text)
	}
	config.Telegram.HideErrors = true
	if text, _ := textOf(t, settingsOf(config), backup); strings.Contains(text, "first line") || strings.Contains(text, word("raw")) {
		t.Errorf("an error text with the setting off:\n%s", text)
	}
}
