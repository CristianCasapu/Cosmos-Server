package security

import (
	"net/netip"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Bounds of the fields of an event, in characters.
const (
	maxActor       = 64
	maxTarget      = 128
	maxHost        = 128
	maxPath        = 200
	maxMethod      = 10
	maxDetails     = 8
	maxDetailKey   = 32
	maxDetailValue = 200
)

// cleanText makes a text from a request fit for the record: valid UTF-8, one
// line, nothing invisible, at most max characters. Control characters, line
// and paragraph separators and formatting characters (bidirectional
// overrides, zero-width characters, byte order marks) cannot be told apart
// from honest text by a reader, so they are turned into spaces; runs of
// white space become one space. A cut text ends in "…".
func cleanText(text string, max int) string {
	if max <= 0 || text == "" {
		return ""
	}
	// no need to look at megabytes to keep max characters
	if len(text) > max*utf8.UTFMax {
		text = text[:max*utf8.UTFMax]
	}
	text = strings.ToValidUTF8(text, "")

	var b strings.Builder
	b.Grow(len(text))
	space := true // leading white space is dropped
	count := 0
	cut := false
	for _, r := range text {
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.IsSpace(r) ||
			unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp, unicode.Co, unicode.Cs) {
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
		if count == max {
			cut = true
			break
		}
		b.WriteRune(r)
		count++
	}
	cleaned := strings.TrimRight(b.String(), " ")
	if cut {
		runes := []rune(cleaned)
		if len(runes) >= max {
			runes = runes[:max-1]
		}
		cleaned = strings.TrimRight(string(runes), " ") + "…"
	}
	return cleaned
}

// cleanIP returns the canonical form of an address, or "" when it is not
// one. IPv4-mapped IPv6 addresses become IPv4, a zone is dropped, brackets
// are accepted. Nothing but the output of the address parser is ever stored
// or written to the Fail2ban file.
func cleanIP(ip string) string {
	ip = strings.TrimSpace(ip)
	if len(ip) > 64 {
		return ""
	}
	ip = strings.TrimSuffix(strings.TrimPrefix(ip, "["), "]")
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	return addr.Unmap().WithZone("").String()
}

// cleanMethod keeps an HTTP method made of letters only.
func cleanMethod(method string) string {
	if method == "" || len(method) > maxMethod {
		return ""
	}
	for i := 0; i < len(method); i++ {
		c := method[i]
		if !(c >= 'A' && c <= 'Z') && !(c >= 'a' && c <= 'z') {
			return ""
		}
	}
	return strings.ToUpper(method)
}

// cleanDetailKey keeps a key made of lower-case letters, digits and "_".
func cleanDetailKey(key string) string {
	if key == "" || len(key) > maxDetailKey {
		return ""
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') && c != '_' {
			return ""
		}
	}
	return key
}

// cleanDetails bounds the details: at most maxDetails pairs (the first ones
// in the order of their keys), valid keys, cleaned values.
func cleanDetails(details map[string]string) map[string]string {
	if len(details) == 0 {
		return nil
	}
	keys := make([]string, 0, len(details))
	for key := range details {
		if cleanDetailKey(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > maxDetails {
		keys = keys[:maxDetails]
	}
	cleaned := make(map[string]string, len(keys))
	for _, key := range keys {
		cleaned[key] = cleanText(details[key], maxDetailValue)
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

// clean returns the event as it will be stored: class, severity and the
// default outcome from the catalogue, every field bounded and cleaned. The
// caller's Details map is not kept: the result has its own.
func clean(event Event, kind Kind, now time.Time) Event {
	event.ID = 0
	event.Class = kind.Class
	event.Severity = kind.Severity
	if event.Time.IsZero() {
		event.Time = now
	}
	event.Actor = cleanText(event.Actor, maxActor)
	event.IP = cleanIP(event.IP)
	event.Target = cleanText(event.Target, maxTarget)
	event.Host = cleanText(event.Host, maxHost)
	event.Path = cleanText(event.Path, maxPath)
	event.Method = cleanMethod(event.Method)
	if !outcomes[event.Outcome] {
		event.Outcome = kind.Outcome
	}
	if event.Count < 1 {
		event.Count = 1
	}
	event.Details = cleanDetails(event.Details)
	return event
}

// copyOf gives a subscriber an event of its own.
func copyOf(event Event) Event {
	if event.Details != nil {
		details := make(map[string]string, len(event.Details))
		for key, value := range event.Details {
			details[key] = value
		}
		event.Details = details
	}
	return event
}
