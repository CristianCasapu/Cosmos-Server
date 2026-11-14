package snapshots

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxShownName is how many bytes of a path from a backed-up folder are shown
// in a message.
const maxShownName = 300

// shownName is a path from a backed-up folder as it may appear in the log,
// the admins' notice, the API's answer and a snapshot's record. The names in
// such a folder are chosen by whoever writes there and may hold any byte but
// "/": control characters (line breaks, terminal escapes) and bytes that are
// not text are shown as "?", and a long path is cut, keeping its end, which
// names the entry itself.
func shownName(name string) string {
	var plain strings.Builder
	plain.Grow(len(name))
	for len(name) > 0 {
		r, size := utf8.DecodeRuneInString(name)
		name = name[size:]
		if (r == utf8.RuneError && size <= 1) || unicode.IsControl(r) || r == ' ' || r == ' ' {
			plain.WriteByte('?')
			continue
		}
		plain.WriteRune(r)
	}
	shown := plain.String()
	if len(shown) <= maxShownName {
		return shown
	}
	cut := len(shown) - maxShownName
	for cut < len(shown) && !utf8.RuneStart(shown[cut]) {
		cut++
	}
	return "…" + shown[cut:]
}
