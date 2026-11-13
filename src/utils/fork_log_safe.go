package utils

// Fork addition: one message, one line.
//
// Log messages carry text that arrived in a request (a path, a name, an
// error). A line break in such text started a new line in cosmos.log,
// cosmos.plain.log and the journal that looked like a line Cosmos wrote, and
// other control characters reached the terminal of whoever reads the log.
// Before a message is written, line breaks and control characters are
// replaced by their visible form (\n, \r, \x1b ...). Tabs stay, and so do the
// colour codes Cosmos itself puts into the request line.

import (
	"fmt"
	"strings"
)

// logSafe is the hook of RawLogMessage.
func logSafe(message string) string {
	clean := true
	for i := 0; i < len(message); i++ {
		if c := message[i]; (c < 0x20 && c != '\t') || c == 0x7f {
			clean = false
			break
		}
	}
	if clean {
		return message
	}

	var safe strings.Builder
	safe.Grow(len(message) + 8)
	for i := 0; i < len(message); i++ {
		c := message[i]
		switch {
		case c == '\n':
			safe.WriteString(`\n`)
		case c == '\r':
			safe.WriteString(`\r`)
		case c == 0x1b:
			if end := colourCodeEnd(message, i); end > 0 {
				safe.WriteString(message[i:end])
				i = end - 1
				continue
			}
			safe.WriteString(`\x1b`)
		case (c < 0x20 && c != '\t') || c == 0x7f:
			fmt.Fprintf(&safe, `\x%02x`, c)
		default:
			safe.WriteByte(c)
		}
	}
	return safe.String()
}

// colourCodeEnd returns the index after a colour code (ESC [ digits and
// semicolons m) starting at i, or 0 when what starts there is something else.
func colourCodeEnd(message string, i int) int {
	if i+2 >= len(message) || message[i+1] != '[' {
		return 0
	}
	for j := i + 2; j < len(message) && j < i+16; j++ {
		c := message[j]
		switch {
		case c == 'm':
			return j + 1
		case (c >= '0' && c <= '9') || c == ';':
		default:
			return 0
		}
	}
	return 0
}
