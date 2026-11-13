package telegram

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func TestCleanValue(t *testing.T) {
	cases := map[string]string{
		"plain":                          "plain",
		"  spaces   around  ":            "spaces around",
		"two\nlines":                     "two lines",
		"carriage\r\nreturn":             "carriage return",
		"tab\tseparated":                 "tab separated",
		"nul\x00byte":                    "nul byte",
		"escape\x1b[31mred":              "escape [31mred",
		"line\u2028separator":            "line separator",
		"paragraph\u2029separator":       "paragraph separator",
		"right\u202eto left":             "right to left",
		"isolate\u2066d\u2069":           "isolate d",
		"zero\u200bwidth":                "zero width",
		"byte\ufefforder":                "byte order",
		"no\u00a0break":                  "no break",
		"bad\xffutf8":                    "bad utf8",
		"\n\n\n":                         "",
		"":                               "",
		"unicode: ăîșț 日本語 🙂":            "unicode: ăîșț 日本語 🙂",
		"<b>bold</b> *md* [a](http://x)": "<b>bold</b> *md* [a](http://x)", // not formatting: no parse mode is sent
	}
	for in, want := range cases {
		if got := cleanValue(in, maxValue); got != want {
			t.Errorf("cleanValue(%q) = %q, want %q", in, got, want)
		}
	}

	long := cleanValue(strings.Repeat("ș", 500), 200)
	if got := len([]rune(long)); got != 200 || !strings.HasSuffix(long, "…") {
		t.Errorf("a long value is %d characters and ends in %q", got, long[len(long)-3:])
	}
	if got := cleanValue("exactly", 7); got != "exactly" {
		t.Errorf("a value of exactly the bound was cut: %q", got)
	}
}

func TestEveryValueIsCoveredAndCountedInUTF16(t *testing.T) {
	c := &composer{}
	c.fixed("Șef 🙂: ").value("a😀b", maxValue).fixed(" · ").value("日本語", maxValue).line().fixed("End ").value("", maxValue)
	text, entities := c.result()

	if want := "Șef 🙂: a😀b · 日本語\nEnd -"; text != want {
		t.Fatalf("text %q, want %q", text, want)
	}
	parts := covered(t, text, entities)
	if len(parts) != 2 || parts[0] != "a😀b" || parts[1] != "日本語" {
		t.Fatalf("the entities cover %q", parts)
	}
	// "Șef 🙂: " is 8 UTF-16 units (the face is two), not 7 characters or 12 bytes
	if entities[0].Offset != 8 || entities[0].Length != 4 {
		t.Errorf("first entity %+v, want offset 8 length 4", entities[0])
	}
	// an empty value is a dash of ours, with no entity
	if got := uncovered(t, text, entities); got != "Șef 🙂:  · \nEnd -" {
		t.Errorf("outside the entities: %q", got)
	}
}

// What an attacker can put into a name, a path or an address must end up
// inside a code entity, on one line, whatever it is.
func TestHostileValuesStayInsideTheirEntity(t *testing.T) {
	hostile := []string{
		"https://evil.example/login",
		"t.me/evil_channel",
		"@admin @everyone",
		"#urgent /start /delete_account",
		"+1 555 0100 owner@example.com",
		"tg://user?id=1",
		"x\n\nCosmos · bank · Security alert\nYour server was hacked, pay at https://evil.example",
		"<a href=\"https://evil.example\">click</a>",
		"[click](https://evil.example) *bold* _it_ `code` ```pre```",
		"\u202egnp.exe",
		strings.Repeat("https://evil.example/ ", 400),
		"😀😀😀😀😀😀😀😀😀😀 https://evil.example",
	}
	for _, value := range hostile {
		c := &composer{}
		header(c, testSettings(), "Security")
		c.fixed("Failed login").line()
		c.fixed("By ").value(value, maxValue).fixed(" from ").value(value, maxValue).line()
		c.fixed("2026-11-13 10:00:00 UTC")
		text, entities := c.result()

		rest := uncovered(t, text, entities)
		if want := "Cosmos ·  · Security\nFailed login\nBy  from \n2026-11-13 10:00:00 UTC"; rest != want {
			t.Errorf("value %q leaked out of its entity:\n%q\nwant\n%q", value, rest, want)
		}
		if lines := strings.Count(text, "\n"); lines != 3 {
			t.Errorf("value %q changed the number of lines to %d", value, lines+1)
		}
		for _, part := range covered(t, text, entities) {
			if strings.ContainsAny(part, "\n\r\u202e\u2028") {
				t.Errorf("a covered value holds a line break or an override: %q", part)
			}
			if len([]rune(part)) > maxValue {
				t.Errorf("a covered value is %d characters long", len([]rune(part)))
			}
		}
	}
}

func TestClampMessage(t *testing.T) {
	// a message that fits is left alone
	text, entities := clampMessage("short", []entity{{Type: "code", Offset: 0, Length: 5}})
	if text != "short" || len(entities) != 1 {
		t.Errorf("a short message changed: %q %v", text, entities)
	}

	// too long: cut to the limit, in UTF-16 units, never inside a character
	for _, filler := range []string{"a", "ș", "😀", "a😀"} {
		c := &composer{}
		c.fixed("Head ").value("first", maxValue).fixed(" ")
		for c.length < maxMessageUnits+500 {
			c.entities = append(c.entities, entity{Type: "code", Offset: c.length, Length: units(filler)})
			c.fixed(filler)
		}
		text, entities := c.result()

		if got := len(utf16.Encode([]rune(text))); got > maxMessageUnits {
			t.Errorf("filler %q: %d units after the cut", filler, got)
		}
		if strings.ContainsRune(text, '\ufffd') {
			t.Errorf("filler %q: the cut split a character", filler)
		}
		if !strings.HasSuffix(text, "…") {
			t.Errorf("filler %q: the cut is not marked", filler)
		}
		covered(t, text, entities) // fails if an entity reaches past the end
	}

	// entities that make no sense are dropped, not sent
	_, entities = clampMessage("abc", []entity{{Type: "code", Offset: -1, Length: 2}, {Type: "code", Offset: 1, Length: 0}, {Type: "code", Offset: 3, Length: 1}, {Type: "code", Offset: 1, Length: 9}})
	if len(entities) != 1 || entities[0].Offset != 1 || entities[0].Length != 2 {
		t.Errorf("entities %+v", entities)
	}
}
