package telegram

import (
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// How a message is put together.
//
// The text is plain: no parse_mode is ever sent, so nothing in it is read as
// formatting. Telegram would still turn URLs, @names, #tags, /commands,
// phone numbers and e-mail addresses in plain text into things to tap. To
// keep that from happening to a value that came from a request, every value
// is covered by a "code" entity (an error text by a "pre" entity), sent in
// the entities list: inside one, Telegram renders no other entity. A message
// is therefore fixed sentences (the table of messages.go) plus values
// (guarded against secrets, cleaned, bounded, covered).

const (
	maxValue    = 200 // a name, an address, a path
	maxFreeText = 500 // the text of an error
	cutMark     = "…"
)

// cleanValue makes a text from outside fit to stand in a message: line
// breaks, control characters and invisible or direction-changing characters
// become a space, runs of spaces become one, and what is longer than max
// characters is cut and ends in "…".
func cleanValue(text string, max int) string {
	var b strings.Builder
	b.Grow(len(text))
	space := true // drops leading spaces
	for _, r := range text {
		if r == utf8.RuneError || !unicode.IsPrint(r) || unicode.IsSpace(r) || isSeverityMark(r) {
			// IsPrint is false for control characters, line and paragraph
			// separators, and the format characters (zero width, bidi
			// overrides and isolates, byte order mark). The marks of the
			// severities are this package's: a value cannot fake one.
			r = ' '
		}
		if r == ' ' {
			if space {
				continue
			}
			space = true
		} else {
			space = false
		}
		b.WriteRune(r)
	}
	cleaned := strings.TrimRight(b.String(), " ")

	if max > 0 && utf8.RuneCountInString(cleaned) > max {
		runes := []rune(cleaned)
		cleaned = strings.TrimRight(string(runes[:max-1]), " ") + cutMark
	}
	return cleaned
}

// units is the length of a text as Telegram counts it: UTF-16 code units.
func units(text string) int {
	n := 0
	for _, r := range text {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// isSeverityMark recognises the marks of messages.go and the other
// coloured circles and squares, which a value could use to look like the
// start of a message.
func isSeverityMark(r rune) bool {
	return (r >= 0x1F534 && r <= 0x1F539) || (r >= 0x1F7E0 && r <= 0x1F7EB) || r == 0x26AA || r == 0x26AB || r == 0x2B55
}

// composer builds the text of a message and the entities over its values.
type composer struct {
	text     strings.Builder
	length   int // in UTF-16 code units
	entities []entity
	// secrets are cut out of every value (guard.go)
	secrets []string
}

// fixed adds text written in this package. It must never be given anything
// that came from outside.
func (c *composer) fixed(text string) *composer {
	c.text.WriteString(text)
	c.length += units(text)
	return c
}

// value adds a text from outside: cleaned, cut to max characters and
// covered by a code entity. An empty value is shown as a dash.
func (c *composer) value(text string, max int) *composer {
	return c.covered("code", text, max)
}

// pre adds an error text from outside the same way, as a block.
func (c *composer) pre(text string, max int) *composer {
	return c.covered("pre", text, max)
}

func (c *composer) covered(kind string, text string, max int) *composer {
	cleaned := cleanValue(guardValue(text, c.secrets), max)
	if cleaned == "" {
		return c.fixed("-")
	}
	size := units(cleaned)
	c.entities = append(c.entities, entity{Type: kind, Offset: c.length, Length: size})
	c.text.WriteString(cleaned)
	c.length += size
	return c
}

func (c *composer) line() *composer {
	return c.fixed("\n")
}

func (c *composer) result() (string, []entity) {
	return clampMessage(c.text.String(), c.entities)
}

// clampMessage cuts a message to what Telegram takes (4096 UTF-16 code
// units). It cuts at the end of a line, so never inside a value (values
// hold no line break) and ends with "…". Only a single line longer than
// the limit is cut inside, never inside a character; an entity that would
// reach past the end is then shortened, one that starts past it dropped.
func clampMessage(text string, entities []entity) (string, []entity) {
	if units(text) > maxMessageUnits {
		encoded := utf16.Encode([]rune(text))
		for end := maxMessageUnits - 2; end > 0; end-- {
			if encoded[end] == '\n' {
				text = string(utf16.Decode(encoded[:end+1])) + cutMark
				break
			}
		}
	}
	if units(text) > maxMessageUnits {
		encoded := utf16.Encode([]rune(text))
		end := maxMessageUnits - 1 // room for the mark
		if encoded[end-1] >= 0xD800 && encoded[end-1] < 0xDC00 {
			end-- // do not keep the first half of a surrogate pair
		}
		text = string(utf16.Decode(encoded[:end])) + cutMark
	}

	total := units(text)
	kept := make([]entity, 0, len(entities))
	for _, e := range entities {
		if e.Offset < 0 || e.Length <= 0 || e.Offset >= total {
			continue
		}
		if e.Offset+e.Length > total {
			e.Length = total - e.Offset
		}
		kept = append(kept, e)
	}
	if len(kept) == 0 {
		kept = nil
	}
	return text, kept
}
