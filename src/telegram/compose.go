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
// is covered by a "code" entity, sent in the entities list: inside one,
// Telegram renders no other entity. A message is therefore fixed sentences
// (written here) plus values (cleaned, bounded, covered).

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
		if r == utf8.RuneError || !unicode.IsPrint(r) || unicode.IsSpace(r) {
			// IsPrint is false for control characters, line and paragraph
			// separators, and the format characters (zero width, bidi
			// overrides and isolates, byte order mark)
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

// composer builds the text of a message and the entities over its values.
type composer struct {
	text     strings.Builder
	length   int // in UTF-16 code units
	entities []entity
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
	cleaned := cleanValue(text, max)
	if cleaned == "" {
		return c.fixed("-")
	}
	size := units(cleaned)
	c.entities = append(c.entities, entity{Type: "code", Offset: c.length, Length: size})
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
// units), never inside a character, and keeps the entities within it: one
// that would reach past the end is shortened, one that starts past it is
// dropped. A cut value stays covered.
func clampMessage(text string, entities []entity) (string, []entity) {
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
