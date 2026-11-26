package utils

// Fork addition: notifications that explain themselves, are grouped when
// they repeat, and are marked resolved when the problem goes away.
//
// A notification may carry a NotificationInfo (Notification.Fork): a stable
// kind, the object it is about, sentences (translation keys with their
// values) for what happened, what it means, what Cosmos does and what the
// administrator can do, the complete technical text, labelled facts and
// the security record entries that belong to it. A notification without it
// (paid code, an upstream addition) is given a kind from its title.
//
// Grouping: per recipient, the newest row of the same kind and thread (what
// it is about) that is not resolved and was seen within groupWindow is
// updated (count, last seen, the latest texts) instead of adding a row. It
// alerts again (unread, forwarded to Telegram) when it is new, when it got
// worse, or when it last alerted realertAfter ago.
//
// Resolving: ResolveThread marks the open rows of a thread resolved; the
// caller writes the follow-up ("the router forwards the port again") with
// Resolves set, which is shown on the same timeline.

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	groupWindow  = 24 * time.Hour
	realertAfter = 4 * time.Hour
	maxSeen      = 20
	maxRaw       = 4000
	maxVarValue  = 300
	maxFacts     = 12
	maxSteps     = 6
	maxInfoBytes = 16 << 10
)

// NotificationText is a sentence: a translation key and its values.
type NotificationText struct {
	Key  string            `json:"key"`
	Vars map[string]string `json:"vars,omitempty"`
}

// NText builds a sentence from a key and key, value pairs.
func NText(key string, pairs ...string) *NotificationText {
	t := &NotificationText{Key: key}
	for i := 0; i+1 < len(pairs); i += 2 {
		if t.Vars == nil {
			t.Vars = map[string]string{}
		}
		t.Vars[pairs[i]] = pairs[i+1]
	}
	return t
}

// NotificationSubject is what a notification is about.
type NotificationSubject struct {
	Type string `json:"type"` // port, router, domain, container, disk, job, alert, release, chat, folder, setting, service
	Name string `json:"name"`
	Link string `json:"link,omitempty"` // a path of the panel, /cosmos-ui/…
}

// NotificationStep is one thing the administrator can do.
type NotificationStep struct {
	NotificationText
	Link   string `json:"link,omitempty"`
	Action string `json:"action,omitempty"` // a command the panel knows: "upnp.check"
}

// NotificationFact is a labelled value.
type NotificationFact struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// NotificationSecurity says which entries of the security record belong to
// a notification.
type NotificationSecurity struct {
	Kinds  []string `json:"kinds,omitempty"` // kinds or "prefix.*"
	Target string   `json:"target,omitempty"`
	IP     string   `json:"ip,omitempty"`
}

// NotificationInfo is what the fork stores with a notification.
type NotificationInfo struct {
	Kind     string                `json:"kind"`
	Source   string                `json:"source,omitempty"`
	Thread   string                `json:"thread,omitempty"`
	Subject  *NotificationSubject  `json:"subject,omitempty"`
	Title    *NotificationText     `json:"title,omitempty"`
	What     *NotificationText     `json:"what,omitempty"`
	Means    *NotificationText     `json:"means,omitempty"`
	Doing    *NotificationText     `json:"doing,omitempty"`
	Steps    []NotificationStep    `json:"steps,omitempty"`
	Raw      string                `json:"raw,omitempty"`
	Facts    []NotificationFact    `json:"facts,omitempty"`
	Security *NotificationSecurity `json:"security,omitempty"`
	Resolves string                `json:"resolves,omitempty"`
	// kept by the writer
	Seen    []int64 `json:"seen,omitempty"`
	Alerted int64   `json:"alerted,omitempty"`

	// told to the forwarder, not stored
	Ref    string `json:"-"`
	Count  int    `json:"-"`
	Repeat bool   `json:"-"`
}

// notificationRecipients fans a group out to nicknames. A variable for
// the tests (the users are in auth.db).
var notificationRecipients = func(group string) []string {
	names := []string{}
	for _, user := range ListAllUsers(group) {
		names = append(names, user.Nickname)
	}
	return names
}

// notificationClock is the time. A variable for the tests.
var notificationClock = time.Now

func levelRank(level string) int {
	switch normalLevel(level) {
	case "error":
		return 2
	case "warning":
		return 1
	}
	return 0
}

// normalLevel is the level as the panel shows it.
func normalLevel(level string) string {
	switch strings.ToLower(level) {
	case "error", "critical", "fatal":
		return "error"
	case "warn", "warning":
		return "warning"
	case "success":
		return "success"
	}
	return "info"
}

// cleanVar makes a value of a sentence safe to store: no control
// characters, bounded.
func cleanVar(value string, max int) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == ' ' || r == ' ' ||
			(r >= '‪' && r <= '‮') || (r >= '⁦' && r <= '⁩') || r == '‎' || r == '‏' {
			return ' '
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > max {
		runes := []rune(value)
		value = string(runes[:max-1]) + "…"
	}
	return value
}

// cleanRaw keeps line breaks and tabs of a technical text, nothing else.
func cleanRaw(value string) string {
	value = strings.ToValidUTF8(value, "?")
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= '‪' && r <= '‮') || (r >= '⁦' && r <= '⁩') {
			return ' '
		}
		return r
	}, value)
	if len(value) > maxRaw {
		value = strings.ToValidUTF8(value[:maxRaw], "") + "…"
	}
	return value
}

func cleanText(t *NotificationText) *NotificationText {
	if t == nil || t.Key == "" {
		return nil
	}
	out := &NotificationText{Key: cleanVar(t.Key, 120)}
	for name, value := range t.Vars {
		if out.Vars == nil {
			out.Vars = map[string]string{}
		}
		if len(out.Vars) >= 16 {
			break
		}
		out.Vars[cleanVar(name, 32)] = cleanVar(value, maxVarValue)
	}
	return out
}

// safeLink keeps a link only when it is a path of the panel.
func safeLink(link string) string {
	if !strings.HasPrefix(link, "/cosmos-ui/") || strings.ContainsAny(link, "\\ \t\r\n\"'<>") || strings.Contains(link, "//") {
		return ""
	}
	if len(link) > 300 {
		return ""
	}
	return link
}

// cleanInfo bounds and cleans what a caller gave.
func cleanInfo(in NotificationInfo) NotificationInfo {
	out := NotificationInfo{
		Kind: cleanVar(in.Kind, 64), Source: cleanVar(in.Source, 32), Thread: cleanVar(in.Thread, 200),
		Title: cleanText(in.Title), What: cleanText(in.What), Means: cleanText(in.Means), Doing: cleanText(in.Doing),
		Raw: cleanRaw(in.Raw), Resolves: cleanVar(in.Resolves, 200),
		Ref: in.Ref, Count: in.Count, Repeat: in.Repeat, Seen: in.Seen, Alerted: in.Alerted,
	}
	if in.Subject != nil && in.Subject.Name != "" {
		out.Subject = &NotificationSubject{Type: cleanVar(in.Subject.Type, 32), Name: cleanVar(in.Subject.Name, 200), Link: safeLink(in.Subject.Link)}
	}
	for _, step := range in.Steps {
		if len(out.Steps) == maxSteps {
			break
		}
		text := cleanText(&step.NotificationText)
		if text == nil {
			continue
		}
		out.Steps = append(out.Steps, NotificationStep{NotificationText: *text, Link: safeLink(step.Link), Action: cleanVar(step.Action, 32)})
	}
	for _, fact := range in.Facts {
		if len(out.Facts) == maxFacts {
			break
		}
		if fact.Value == "" {
			continue
		}
		out.Facts = append(out.Facts, NotificationFact{Key: cleanVar(fact.Key, 120), Value: cleanVar(fact.Value, 1000)})
	}
	if in.Security != nil {
		sec := &NotificationSecurity{Target: cleanVar(in.Security.Target, 128), IP: cleanVar(in.Security.IP, 45)}
		for _, kind := range in.Security.Kinds {
			if len(sec.Kinds) < 8 {
				sec.Kinds = append(sec.Kinds, cleanVar(kind, 64))
			}
		}
		out.Security = sec
	}
	if out.Source == "" {
		out.Source = notificationKinds[out.Kind].Source
	}
	if out.Source == "" {
		out.Source = "other"
	}
	if out.Thread == "" {
		out.Thread = out.Kind
		if out.Subject != nil {
			out.Thread += ":" + out.Subject.Type + ":" + out.Subject.Name
		}
	}
	if out.Title == nil && (out.What != nil || out.Raw != "") {
		if title := notificationKinds[out.Kind].Title; title != "" {
			out.Title = &NotificationText{Key: title}
		}
	}
	return out
}

// legacyInfo is the info of a notification written without one.
func legacyInfo(n Notification) NotificationInfo {
	kind, known := legacyNotificationKinds[n.Title]
	if !known {
		kind = "other"
	}
	if n.Title == "header.notification.title.serverError" {
		kind = majorErrorKind(n.Message)
	}
	thread := kind + ":" + cleanVar(n.Vars, 200)
	if n.Title == "header.notification.title.serverError" {
		thread = kind + ":" + cleanVar(n.Message, 200)
	} else if !known {
		thread = "other:" + cleanVar(n.Title, 100) + ":" + cleanVar(n.Vars, 100)
	}
	return NotificationInfo{Kind: kind, Thread: thread}
}

// newRef is the address of a notification that is the same for every
// recipient's copy.
func newRef() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}

// writeOutcome is what forkWriteNotification did.
type writeOutcome struct {
	alert bool
	ref   string
	count int
	first int64
}

// forkWriteNotification is the hook of WriteNotification. It writes every
// notification; false only when the database is not open, so that
// upstream's path reports the error as before.
func forkWriteNotification(n Notification) bool {
	db, err := MonitoringWriteDB()
	if err != nil {
		return false
	}
	explicit := n.Fork != nil
	var info NotificationInfo
	if explicit {
		info = cleanInfo(*n.Fork)
	} else {
		info = cleanInfo(legacyInfo(n))
	}
	if explicit {
		if info.Title != nil {
			n.Title = info.Title.Key
		}
		if info.What != nil {
			n.Message = info.What.Key
		}
		if info.Subject != nil && n.Vars == "" {
			n.Vars = info.Subject.Name
		}
		if n.Link == "" && info.Subject != nil {
			n.Link = info.Subject.Link
		}
	}
	n.Link = safeLink(n.Link)
	n.Level = normalLevel(n.Level)

	recipients := []string{n.Recipient}
	audience := ""
	if n.Recipient == "all" || n.Recipient == "admin" || n.Recipient == "user" {
		recipients = notificationRecipients(n.Recipient)
		if n.Recipient == "admin" {
			audience = "admin"
		}
	}

	outcome, err := writeRows(db, n, info, explicit, recipients, audience)
	if err != nil {
		Error("Notifications: Error while writing notification", err)
		return true
	}
	if outcome.alert {
		forwarded := n
		if explicit {
			copyInfo := info
			copyInfo.Ref, copyInfo.Count, copyInfo.Repeat = outcome.ref, outcome.count, outcome.count > 1
			copyInfo.Seen = []int64{outcome.first}
			forwarded.Fork = &copyInfo
		} else {
			forwarded.Fork = nil
		}
		forwardNotification(forwarded)
	}
	return true
}

// searchText is what the search of the list matches, in English.
func searchText(n Notification, info NotificationInfo, explicit bool) string {
	parts := []string{}
	if explicit {
		for _, t := range []*NotificationText{info.Title, info.What, info.Means} {
			if t != nil {
				parts = append(parts, RenderNotificationText(*t))
			}
		}
		if info.Subject != nil {
			parts = append(parts, info.Subject.Name)
		}
		parts = append(parts, info.Raw)
	} else {
		parts = append(parts, LegacyNotificationEnglish(n.Title, ""), LegacyNotificationEnglish(n.Message, n.Vars), n.Message, n.Vars)
	}
	parts = append(parts, info.Kind)
	text := strings.Join(parts, " \n")
	if len(text) > 8000 {
		text = strings.ToValidUTF8(text[:8000], "")
	}
	return text
}

func writeRows(db *sql.DB, n Notification, info NotificationInfo, explicit bool, recipients []string, audience string) (writeOutcome, error) {
	now := notificationClock()
	nowMs := TimeToMillis(now)
	outcome := writeOutcome{}
	ref := newRef()
	search := searchText(n, info, explicit)

	tx, err := db.Begin()
	if err != nil {
		return outcome, err
	}
	defer tx.Rollback()

	actions, _ := json.Marshal(n.Actions)
	for _, recipient := range recipients {
		var (
			id                   int64
			level, rowRef, saved string
			count                int
			first                int64
		)
		err := tx.QueryRow(MonitoringRebind(
			"SELECT id, level, fork_ref, fork_count, fork_first, fork_info FROM notifications"+
				" WHERE recipient = ? AND fork_thread = ? AND fork_kind = ? AND fork_resolved = 0 AND date >= ?"+
				" ORDER BY date DESC, id DESC LIMIT 1"),
			recipient, info.Thread, info.Kind, nowMs-groupWindow.Milliseconds()).Scan(&id, &level, &rowRef, &count, &first, &saved)
		if err != nil && err != sql.ErrNoRows {
			return outcome, err
		}

		row := info
		if err == sql.ErrNoRows {
			row.Seen = []int64{nowMs}
			row.Alerted = nowMs
			stored, _ := encodeInfo(row)
			// a follow-up is about something that is over: it is resolved
			// from the start, and never grouped
			resolved := int64(0)
			if info.Resolves != "" {
				resolved = nowMs
			}
			if _, err := tx.Exec(MonitoringRebind(
				"INSERT INTO notifications(node, recipient, title, message, vars, icon, link, date, level, read, actions,"+
					" fork_kind, fork_source, fork_thread, fork_ref, fork_audience, fork_count, fork_first, fork_resolved, fork_search, fork_info)"+
					" VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?)"),
				MonitoringNode(), recipient, n.Title, n.Message, n.Vars, n.Icon, n.Link, nowMs, n.Level, string(actions),
				info.Kind, info.Source, info.Thread, ref, audience, nowMs, resolved, search, stored); err != nil {
				return outcome, err
			}
			outcome.alert = true
			if outcome.ref == "" {
				outcome.ref = ref
			}
			if outcome.count < 1 {
				outcome.count = 1
			}
			if outcome.first == 0 {
				outcome.first = nowMs
			}
			continue
		}

		var before NotificationInfo
		json.Unmarshal([]byte(saved), &before)
		row.Seen = append(before.Seen, nowMs)
		if len(row.Seen) > maxSeen {
			row.Seen = row.Seen[len(row.Seen)-maxSeen:]
		}
		alert := levelRank(n.Level) > levelRank(level) || nowMs-before.Alerted >= realertAfter.Milliseconds()
		row.Alerted = before.Alerted
		if alert {
			row.Alerted = nowMs
		}
		if count < 1 {
			count = 1
		}
		count++
		stored, _ := encodeInfo(row)
		readClause := ""
		if alert {
			readClause = ", read = 0"
		}
		newLevel := level
		if levelRank(n.Level) > levelRank(level) {
			newLevel = n.Level
		}
		if _, err := tx.Exec(MonitoringRebind(
			"UPDATE notifications SET title = ?, message = ?, vars = ?, link = ?, date = ?, level = ?, fork_count = ?, fork_search = ?, fork_info = ?"+readClause+
				" WHERE id = ?"),
			n.Title, n.Message, n.Vars, n.Link, nowMs, newLevel, count, search, stored, id); err != nil {
			return outcome, err
		}
		outcome.alert = outcome.alert || alert
		if outcome.ref == "" {
			outcome.ref = rowRef
		}
		if count > outcome.count {
			outcome.count = count
		}
		if first > 0 && (outcome.first == 0 || first < outcome.first) {
			outcome.first = first
		}
	}
	return outcome, tx.Commit()
}

// encodeInfo stores the info within its bound: the technical text is cut
// first, then the facts.
func encodeInfo(info NotificationInfo) (string, error) {
	for {
		b, err := json.Marshal(info)
		if err != nil {
			return "", err
		}
		if len(b) <= maxInfoBytes {
			return string(b), nil
		}
		switch {
		case len(info.Raw) > 200:
			info.Raw = strings.ToValidUTF8(info.Raw[:len(info.Raw)/2], "") + "…"
		case len(info.Facts) > 0:
			info.Facts = info.Facts[:len(info.Facts)-1]
		case len(info.Steps) > 0:
			info.Steps = info.Steps[:len(info.Steps)-1]
		default:
			info.Seen = nil
			b, err := json.Marshal(info)
			return string(b), err
		}
	}
}

// ResolveThread marks the open notifications of a thread resolved, for
// every recipient. It returns when the oldest of them was first seen, and
// whether any was open.
func ResolveThread(thread string) (time.Time, bool) {
	thread = ThreadName(thread) // as it was stored (review 6)
	db, err := MonitoringWriteDB()
	if err != nil || thread == "" {
		return time.Time{}, false
	}
	var first sql.NullInt64
	if err := db.QueryRow(MonitoringRebind(
		"SELECT MIN(CASE WHEN fork_first > 0 THEN fork_first ELSE date END) FROM notifications WHERE fork_thread = ? AND fork_resolved = 0 AND fork_info <> ''"),
		thread).Scan(&first); err != nil || !first.Valid {
		return time.Time{}, false
	}
	res, err := db.Exec(MonitoringRebind(
		"UPDATE notifications SET fork_resolved = ? WHERE fork_thread = ? AND fork_resolved = 0 AND fork_info <> ''"),
		TimeToMillis(notificationClock()), thread)
	if err != nil {
		Error("Notifications: could not mark a notification resolved", err)
		return time.Time{}, false
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return time.Time{}, false
	}
	return MillisToTime(first.Int64), true
}

// ThreadOpen reports whether a thread has an open notification.
func ThreadOpen(thread string) bool {
	thread = ThreadName(thread) // as it was stored (review 6)
	db, err := MonitoringReadDB()
	if err != nil {
		return false
	}
	var n int
	db.QueryRow(MonitoringRebind("SELECT COUNT(*) FROM notifications WHERE fork_thread = ? AND fork_resolved = 0 AND fork_info <> ''"), thread).Scan(&n)
	return n > 0
}

// SetNotificationRecipients replaces how a group is fanned out, for the
// tests of other packages; it returns what puts the real one back.
func SetNotificationRecipients(recipients func(group string) []string) func() {
	previous := notificationRecipients
	notificationRecipients = recipients
	return func() { notificationRecipients = previous }
}

// SetNotificationClock replaces the clock the notifications are dated by,
// for the tests of other packages (plan 48: the Telegram model test runs
// the real store on its frozen clock); it returns what puts the real one
// back.
func SetNotificationClock(clock func() time.Time) func() {
	previous := notificationClock
	notificationClock = clock
	return func() { notificationClock = previous }
}

// NotificationDuration says how long, in words: "14 minutes", "1 hour 5 minutes".
func NotificationDuration(d time.Duration) string {
	if d < time.Minute {
		return "less than a minute"
	}
	minutes := int(d.Round(time.Minute) / time.Minute)
	plural := func(n int, word string) string {
		if n == 1 {
			return "1 " + word
		}
		return strconv.Itoa(n) + " " + word + "s"
	}
	if minutes < 60 {
		return plural(minutes, "minute")
	}
	if minutes < 48*60 {
		text := plural(minutes/60, "hour")
		if minutes%60 > 0 {
			text += " " + plural(minutes%60, "minute")
		}
		return text
	}
	return plural(minutes/(24*60), "day")
}
