package telegram

import (
	"strings"
	"testing"
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
		"the key is Zx9Kq2Lm8Np4Rs7Tv1Wy3Ab6Cd":                              "Zx9Kq2Lm8Np4Rs7Tv1Wy3Ab6Cd",
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
