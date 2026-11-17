package telegram

// Fork addition (plan 48): every message is put into words here, from one
// table of texts per kind (messages.en.json), so that the phone's preview
// says what happened and to what:
//
//	🔴 Critical · Disk /mnt/data nearly full
//	🟡 Warning · Failed sign-in as admin from 203.0.113.7
//	🟢 OK · Fail2ban runs again
//
// then what happened, what it means, what to do, the error text when there
// is one, the server and the time, and a button to the place in the panel.
//
// The table is the only place with words; a translation is another file of
// the same shape (messages.<lang>.json). Values from outside go through
// composer.value only: guarded against secrets, cleaned, bounded, covered by
// a code entity. There is no parse mode: Telegram never reads markup in a
// message of this package.

import (
	_ "embed"
	"encoding/json"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/azukaar/cosmos-server/src/upnp"
	"github.com/azukaar/cosmos-server/src/utils"
)

//go:embed messages.en.json
var messagesEnglish []byte

// severity is how serious a message is. It decides its mark, its word, its
// queue and how it is delivered.
type severity int

const (
	sevInfo severity = iota
	sevWarn
	sevError
	sevCritical
)

// SeverityNames are the severities from the least to the most serious, as
// the settings name them.
var SeverityNames = []string{"info", "warn", "error", "critical"}

var severityMarks = []string{"🔵", "🟡", "🟠", "🔴"}

// closingMark marks a message that says something is over.
const closingMark = "🟢"

func (s severity) String() string { return SeverityNames[s] }

func parseSeverity(name string) (severity, bool) {
	for i, known := range SeverityNames {
		if known == name {
			return severity(i), true
		}
	}
	return sevInfo, false
}

// kindTexts are the words of one kind of message.
type kindTexts struct {
	Severity string `json:"severity"`
	Category string `json:"category"`
	// Label names the kind in a few words: the preview when details are
	// hidden, the panel's list. Many is its plural, for summaries.
	Label string `json:"label"`
	Many  string `json:"many"`
	// Preview is the first line after the mark: the event and its subject.
	Preview string `json:"preview"`
	// What says what happened, with the facts. WhatKey is the notification
	// sentence (utils.NotificationEnglish) that says it, for the kinds that
	// come from notifications. WhatAuto is a second form of What.
	What     string `json:"what,omitempty"`
	WhatKey  string `json:"whatKey,omitempty"`
	WhatAuto string `json:"whatAuto,omitempty"`
	Means    string `json:"means,omitempty"`
	Todo     string `json:"todo,omitempty"`
	// Page is a fixed path of the panel for the button when there is no
	// notification to open.
	Page string `json:"page,omitempty"`
	// Level: the notification's level may lower the severity (a warning
	// stays a warning). Closing: the kind says that something is over. Pin:
	// the message is pinned while what it tells lasts.
	Level bool `json:"level,omitempty"`
	// Off: not sent unless switched on by itself (like the security
	// events under the minimum severity).
	Off     bool              `json:"off,omitempty"`
	Closing bool              `json:"closing,omitempty"`
	Pin     bool              `json:"pin,omitempty"`
	Sample  map[string]string `json:"sample"`
	// Raw names the values that can carry the text of an error: they go
	// through the guard of error texts, and are left out with "Include error
	// details" switched off.
	Raw []string `json:"raw,omitempty"`

	severity severity
}

type messageTable struct {
	Words map[string]string    `json:"words"`
	Kinds map[string]kindTexts `json:"kinds"`
}

// texts is the table in use.
var texts = mustLoadTable(messagesEnglish)

func mustLoadTable(raw []byte) messageTable {
	var table messageTable
	if err := json.Unmarshal(raw, &table); err != nil {
		panic("telegram: the table of messages cannot be read: " + err.Error())
	}
	for name, kind := range table.Kinds {
		sev, ok := parseSeverity(kind.Severity)
		if !ok {
			panic("telegram: kind " + name + " has no known severity")
		}
		kind.severity = sev
		table.Kinds[name] = kind
	}
	return table
}

// word is a fixed text of the table.
func word(name string) string {
	return texts.Words[name]
}

// KindNames lists the kinds of the table, sorted.
func KindNames() []string {
	names := make([]string, 0, len(texts.Kinds))
	for name := range texts.Kinds {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func knownKind(kind string) bool {
	_, known := texts.Kinds[kind]
	return known
}

// The kinds that are not about something that happened on the server, and
// cannot be switched off.
var ownKinds = map[string]bool{"telegram.test": true, "telegram.moved": true}

// notice is one thing to tell the administrator, before it is put into
// words for the settings in use.
type notice struct {
	kind     string
	category string
	// key is what the budget is kept per. It comes from a bounded set,
	// never from input. Empty: the kind.
	key      string
	severity severity
	closing  bool
	at       time.Time
	// vars are the values of the texts, as they came: render guards them.
	vars map[string]string
	// what and means replace the table's sentences (a notification brings
	// its own); English with {{vars}}.
	what  string
	means string
	// extra writes a line of facts after what happened. May be nil.
	extra func(c *composer)
	// raw is an error text, shown shortened at the end; rawIsRequest: it
	// is the request an incident made (method, host, path).
	raw          string
	rawIsRequest bool
	// ref is the notification's reference, for the button.
	ref string
	// thread: the opening of something that lasts; resolves: its end.
	thread   string
	resolves string
	repeat   bool
	count    int
	first    time.Time
	// counted: the security record's summary row stands for this many.
	counted int
	sample  bool
	// mergedLabel: the opening of this closing was never sent (it is told
	// here, with how long it lasted since mergedSince)
	mergedLabel    string
	mergedSince    time.Time
	mergedSeverity severity
}

func (nt notice) texts() kindTexts {
	return texts.Kinds[nt.kind]
}

func (nt notice) budgetKey() string {
	if nt.key != "" {
		return nt.key
	}
	return nt.kind
}

// priority is how the queue treats it.
func (nt notice) priority() priority {
	switch {
	case nt.severity == sevCritical && !nt.closing:
		return priorityCritical
	case nt.severity == sevInfo || nt.closing:
		return priorityLow
	}
	return priorityNormal
}

// label is the kind in a few words.
func (nt notice) label() string {
	if label := nt.texts().Label; label != "" {
		return label
	}
	return "Notification"
}

// newNotice starts a notice of a kind of the table, with its severity and
// category.
func newNotice(kind string, vars map[string]string) notice {
	kt, known := texts.Kinds[kind]
	if !known {
		kind, kt = "notification.other", texts.Kinds["notification.other"]
	}
	if vars == nil {
		vars = map[string]string{}
	}
	return notice{kind: kind, category: kt.Category, severity: kt.severity, closing: kt.Closing, vars: vars}
}

// message is what is sent: the text, its entities and the button.
type message struct {
	text     string
	entities []entity
	button   *button
}

// button is one URL button under the message.
type button struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

var (
	sentenceVar  = regexp.MustCompile(`\{\{(\w+)\}\}`)
	pageOK       = regexp.MustCompile(`^/cosmos-ui(/[a-z0-9-]+)*$`)
	hostnameOK   = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	refOK        = regexp.MustCompile(`^[0-9a-f]{16}$`)
	maxPreview   = 28 // a value in the first line
	maxRaw       = 300
	previewLimit = 64 // what the first line must say within, in characters
)

// writeTemplate writes an English text: its words as they are, its values
// through composer.value.
func writeTemplate(c *composer, text string, vars map[string]string, max int) {
	// an optional value in brackets is left out with its brackets
	for _, m := range sentenceVar.FindAllStringSubmatch(text, -1) {
		if strings.TrimSpace(vars[m[1]]) == "" {
			text = strings.ReplaceAll(text, " ("+m[0]+")", "")
		}
	}
	last := 0
	for _, m := range sentenceVar.FindAllStringSubmatchIndex(text, -1) {
		c.fixed(text[last:m[0]])
		name := text[m[2]:m[3]]
		if strings.HasPrefix(name, "_") {
			// a text of this server's own catalogues (a label of the
			// security record), never one from outside: plain words, with
			// the configuration's secrets cut out all the same
			c.fixed(cleanValue(hideKnown(vars[name], c.secrets), maxFreeText))
		} else {
			c.value(vars[name], max)
		}
		last = m[1]
	}
	c.fixed(text[last:])
}

// hasVars reports whether every value a text needs is there.
func hasVars(text string, vars map[string]string) bool {
	for _, m := range sentenceVar.FindAllStringSubmatch(text, -1) {
		if strings.TrimSpace(vars[m[1]]) == "" {
			return false
		}
	}
	return true
}

func usesVars(text string) bool {
	return sentenceVar.MatchString(text)
}

// headline writes the mark and the word of a severity.
func headline(c *composer, sev severity, closing bool) {
	if closing {
		c.fixed(closingMark + " " + word("closing") + " · ")
		return
	}
	c.fixed(severityMarks[sev] + " " + word(sev.String()) + " · ")
}

// render puts a notice into words. details: the values may be shown.
func render(st settings, nt notice) message {
	m := renderWith(st, nt, !st.hideDetails)
	if leaks(m.text, m.entities, st.secrets) || (m.button != nil && leaks(m.button.URL, nil, st.secrets)) {
		// a secret got through the values' guard: send nothing of them
		m = renderWith(st, nt, false)
		m.button = nil
	}
	return m
}

func renderWith(st settings, nt notice, details bool) message {
	kt := nt.texts()
	c := &composer{secrets: st.secrets}
	vars := nt.vars
	if len(kt.Raw) > 0 {
		// review T3: error texts in values
		vars = map[string]string{}
		for k, v := range nt.vars {
			vars[k] = v
		}
		for _, k := range kt.Raw {
			if _, has := vars[k]; !has {
				continue
			}
			if st.hideErrors {
				vars[k] = word("rawHidden")
			} else {
				vars[k] = rawPass(vars[k], st.secrets)
			}
		}
	}

	headline(c, nt.severity, nt.closing)
	if details && kt.Preview != "" && hasVars(kt.Preview, vars) {
		// review N1: in the first line a value holds letters, digits and a
		// little punctuation only, so that it cannot look like a mark
		safe := map[string]string{}
		for key, value := range vars {
			if strings.HasPrefix(key, "_") {
				safe[key] = value
			} else {
				safe[key] = previewSafe(value)
			}
		}
		writeTemplate(c, kt.Preview, safe, maxPreview)
		if nt.mergedLabel != "" {
			c.fixed(strings.NewReplacer("{{label}}", nt.mergedLabel, "{{duration}}", mergedDuration(nt)).Replace(word("mergedWas")))
		}
	} else {
		c.fixed(nt.label())
	}
	c.line().line()

	what := nt.what
	if what == "" {
		what = kt.What
		if kt.WhatAuto != "" && strings.TrimSpace(vars["at"]) != "" {
			what = kt.WhatAuto
		}
	}
	if nt.mergedLabel != "" {
		c.fixed(word("mergedWhy")).line()
	}
	if what != "" && (details || !usesVars(what)) {
		writeTemplate(c, what, vars, maxFreeText)
		c.line()
	}
	if details && nt.extra != nil {
		before := c.length
		nt.extra(c)
		if c.length > before {
			c.line()
		}
	}
	means := kt.Means
	if means == "" {
		means = nt.means
	}
	if means != "" && (details || !usesVars(means)) {
		writeTemplate(c, means, vars, maxValue)
		c.line()
	}
	if kt.Todo != "" && (details || !usesVars(kt.Todo)) {
		c.fixed(word("todo"))
		writeTemplate(c, kt.Todo, vars, maxValue)
		c.line()
	}
	if !details {
		c.fixed(word("hidden")).line()
	}
	if details && !st.hideErrors && strings.TrimSpace(nt.raw) != "" {
		if nt.rawIsRequest {
			c.fixed(word("request")).line()
		} else {
			c.fixed(word("raw")).line()
		}
		c.pre(nt.raw, maxRaw).line()
	}
	if nt.counted > 1 {
		writeTemplate(c, word("counted"), map[string]string{"count": strconv.Itoa(nt.counted)}, 12)
		c.line()
	}
	if nt.count > 1 {
		first := "-"
		if !nt.first.IsZero() {
			first = nt.first.Local().Format("2 Jan 15:04")
		}
		writeTemplate(c, word("seen"), map[string]string{"count": strconv.Itoa(nt.count), "first": first}, 20)
		c.line()
	}
	if nt.repeat && nt.resolves == "" {
		c.fixed(word("again")).line()
	}

	c.value(st.serverName, maxServerName).fixed(" · ")
	if nt.sample {
		c.fixed(word("sample"))
	} else {
		at := nt.at
		if at.IsZero() {
			at = time.Now()
		}
		c.fixed(at.Local().Format("2 Jan 15:04"))
	}

	text, entities := c.result()
	m := message{text: text, entities: entities}
	if url := st.panelURL(nt, kt); url != "" {
		m.button = &button{Text: word("button"), URL: url}
	}
	return m
}

// panelURL is the address of the place in the panel where the notice is
// handled: the notification's details when there is one, else the kind's
// page. It is made of the configured hostname and port and fixed paths:
// nothing of an event goes into it. "" when the panel has no address.
func (st settings) panelURL(nt notice, kt kindTexts) string {
	if st.panel == "" {
		return ""
	}
	switch {
	case refOK.MatchString(nt.ref):
		return st.panel + "/cosmos-ui/notifications/r/" + nt.ref
	case pageOK.MatchString(kt.Page):
		return st.panel + kt.Page
	}
	// a notification without its reference (a sample): the list of them
	return st.panel + "/cosmos-ui/notifications"
}

// panelBase is "https://host[:port]", or "" when the server has no name a
// phone could open.
func panelBase(config utils.Config) string {
	host := strings.TrimSpace(config.HTTPConfig.Hostname)
	if !hostnameOK.MatchString(host) || host == "localhost" || !strings.Contains(host, ".") && net.ParseIP(host) == nil {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return ""
	}
	scheme, port := "http", config.HTTPConfig.HTTPPort
	if utils.IsHTTPS {
		scheme = "https"
		if p := upnp.ExternalHTTPSPort(); p > 0 {
			port = strconv.Itoa(p)
		} else {
			port = config.HTTPConfig.HTTPSPort
		}
	}
	address := scheme + "://" + host
	if port != "" && !(scheme == "https" && port == "443") && !(scheme == "http" && port == "80") {
		if n, err := strconv.Atoi(port); err == nil && n > 0 && n < 65536 {
			address += ":" + strconv.Itoa(n)
		}
	}
	return address
}

// previewSafe keeps of a value what may stand in the first line: letters,
// digits, spaces and the punctuation of names, addresses and paths. The
// blank-looking letters (Hangul fillers, braille blank) are not letters here.
func previewSafe(text string) string {
	var b strings.Builder
	for _, r := range text {
		switch {
		case r == 0x115F || r == 0x1160 || r == 0x3164 || r == 0xFFA0 || (r >= 0x2800 && r <= 0x28FF):
			b.WriteRune(' ')
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || strings.ContainsRune(".,:;-_/@()#+&'%*=~[]!?<>", r):
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	return b.String()
}

// mergedDuration says how long what was never told lasted.
func mergedDuration(nt notice) string {
	if nt.mergedSince.IsZero() || nt.at.IsZero() || nt.at.Before(nt.mergedSince) {
		return "a while"
	}
	return utils.NotificationDuration(nt.at.Sub(nt.mergedSince))
}

// PreviewLine is what a phone shows of a message before it is opened: its
// first line, without formatting (there is none in the text itself: the
// entities are apart).
func PreviewLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}

// composeNotice is the text of a notice, for the tests and the summary.
func composeNotice(st settings, nt notice, details bool) (string, []entity) {
	m := renderWith(st, nt, details)
	return m.text, m.entities
}

// sampleNotice is a notice of a kind with the table's sample values.
func sampleNotice(kind string) notice {
	kt := texts.Kinds[kind]
	vars := map[string]string{}
	for k, v := range kt.Sample {
		vars[k] = v
	}
	nt := newNotice(kind, vars)
	nt.sample = true
	if kt.WhatKey != "" {
		nt.what = utils.NotificationEnglish(kt.WhatKey)
	}
	switch kind {
	case "telegram.moved":
		nt.extra = movedBy(vars["actor"], vars["ip"])
	case "address.changed":
		nt = addressNotice(vars)
		nt.sample = true
	}
	return nt
}

// The summary of what was not sent one by one.

// composeDigest writes the summary: per kind when one kind makes it up,
// else the kinds with the most first. Fixed words and numbers only.
func composeDigest(st settings, counts map[string]int, since time.Time, now time.Time, top severity) message {
	kinds := make([]string, 0, len(counts))
	total := 0
	for kind, count := range counts {
		kinds = append(kinds, kind)
		total += count
	}
	sort.Slice(kinds, func(i, j int) bool {
		if (kinds[i] == otherKinds) != (kinds[j] == otherKinds) {
			return kinds[j] == otherKinds
		}
		if counts[kinds[i]] != counts[kinds[j]] {
			return counts[kinds[i]] > counts[kinds[j]]
		}
		return kinds[i] < kinds[j]
	})
	many := func(kind string) string {
		if kt, known := texts.Kinds[kind]; known && kt.Many != "" {
			return kt.Many
		}
		return word("otherEvents")
	}

	c := &composer{secrets: st.secrets}
	headline(c, top, false)
	span := spanWords(now.Sub(since))
	if len(kinds) == 1 {
		writeTemplate(c, word("summaryOne"), map[string]string{"count": strconv.Itoa(total), "many": many(kinds[0]), "span": span}, 80)
	} else {
		list := []string{}
		for i, kind := range kinds {
			if i == 3 {
				break
			}
			list = append(list, strconv.Itoa(counts[kind])+" "+many(kind))
		}
		// numbers and the table's words only: written as they are
		text := strings.NewReplacer("{{count}}", strconv.Itoa(total), "{{span}}", span, "{{list}}", strings.Join(list, ", ")).Replace(word("summaryMany"))
		c.fixed(text)
	}
	c.line().line()
	c.fixed(word("summaryWhat")).line()
	for _, kind := range kinds {
		c.fixed(strconv.Itoa(counts[kind]) + " × " + many(kind)).line()
	}
	c.fixed(word("summaryMeans")).line()
	c.fixed(word("todo") + word("summaryTodo")).line()
	c.value(st.serverName, maxServerName).fixed(" · " + now.Local().Format("2 Jan 15:04"))
	text, entities := c.result()
	m := message{text: text, entities: entities}
	if st.panel != "" {
		m.button = &button{Text: word("button"), URL: st.panel + "/cosmos-ui/notifications"}
	}
	return m
}

// otherKinds stands for the kinds a summary has no room to name.
const otherKinds = "\x00other"

// spanWords says how long ago the first event of a summary was.
func spanWords(span time.Duration) string {
	minutes := int((span + 30*time.Second) / time.Minute)
	switch {
	case minutes <= 1:
		return word("spanMinute")
	case minutes < 120:
		return strings.ReplaceAll(word("spanMinutes"), "{{n}}", strconv.Itoa(minutes))
	default:
		return strings.ReplaceAll(word("spanHours"), "{{n}}", strconv.Itoa((minutes+30)/60))
	}
}
