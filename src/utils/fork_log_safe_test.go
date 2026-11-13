package utils

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// Fork addition: tests of fork_log_safe.go.

func TestLogSafe(t *testing.T) {
	cases := map[string]string{
		"plain text stays":               "plain text stays",
		"a tab\tstays":                   "a tab\tstays",
		"unicode stays: ăîșț":            "unicode stays: ăîșț",
		"/x\n2026/11/13 [INFO] forged":   `/x\n2026/11/13 [INFO] forged`,
		"cr\rlf\r\n":                     `cr\rlf\r\n`,
		"bell\x07 and delete\x7f":        `bell\x07 and delete\x7f`,
		"\x1b[31mred\x1b[0m":             "\x1b[31mred\x1b[0m",
		"\x1b[1;32mbold green\x1b[0m":    "\x1b[1;32mbold green\x1b[0m",
		"clear the screen \x1b[2J":       `clear the screen \x1b[2J`,
		"move the cursor \x1b[10;10H":    `move the cursor \x1b[10;10H`,
		"set the title \x1b]0;owned\x07": `set the title \x1b]0;owned\x07`,
		"an escape at the end \x1b":      `an escape at the end \x1b`,
		"an unfinished colour \x1b[31":   `an unfinished colour \x1b[31`,
	}
	for in, want := range cases {
		if got := logSafe(in); got != want {
			t.Errorf("logSafe(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestALogMessageIsOneLine(t *testing.T) {
	prev := GetBaseMainConfig()
	config := Config{LoggingLevel: "INFO"}
	LoadBaseMainConfig(config)
	var out bytes.Buffer
	prevWriter, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&out)
	t.Cleanup(func() {
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
		LoadBaseMainConfig(prev)
	})

	Warn("[NotFound] No route matched: Host=cosmos.example.com Path=/zz\n2026/11/13 10:00:00 [INFO]  UserLogin: admin logged in\x1b[2J Method=GET")
	Error("Invalid Hostname for API request to /cosmos/api/users/x\r\nforged", nil)

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("two messages made %d lines:\n%s", len(lines), out.String())
	}
	if strings.Contains(out.String(), "\x1b[2J") || strings.Contains(out.String(), "\r") {
		t.Errorf("control characters of a message reached the log: %q", out.String())
	}
	if !strings.Contains(lines[0], `Path=/zz\n2026/11/13`) {
		t.Errorf("the line break is not shown in its visible form: %q", lines[0])
	}
}
