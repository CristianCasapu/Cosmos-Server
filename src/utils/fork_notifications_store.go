package utils

// Fork addition: reading the notifications for the page and the details
// view, marking them and deleting them. Every query is limited to the
// recipient's own rows; rows written for the administrators are left out
// for a reader who is not one (a user who was an admin keeps their rows,
// but does not see them while they are not).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	NotificationPageDefault = 30
	NotificationPageMax     = 100
	NotificationSearchMax   = 100
	NotificationIDsMax      = 200
	NotificationThreadMax   = 50
)

// NotificationQuery selects notifications of one recipient.
type NotificationQuery struct {
	Recipient string
	Admin     bool     // the reader may see rows written for the administrators
	Levels    []string // error, warning, info, success
	Source    string
	Read      string // "read", "unread" or ""
	Search    string
	Cursor    string // "<ms>:<id>"
	Limit     int
}

// NotificationLine is a sentence as the panel gets it: key, English, values.
type NotificationLine struct {
	Key  string            `json:"key"`
	Text string            `json:"text"`
	Vars map[string]string `json:"vars,omitempty"`
}

// NotificationStepView is a step as the panel gets it.
type NotificationStepView struct {
	NotificationLine
	Link   string `json:"link,omitempty"`
	Action string `json:"action,omitempty"`
}

// NotificationFactView is a labelled value as the panel gets it.
type NotificationFactView struct {
	Key   string `json:"key"`
	Text  string `json:"text"`
	Value string `json:"value"`
}

// NotificationView is a notification as the page and the details get it.
type NotificationView struct {
	ID        int64                `json:"id"`
	Ref       string               `json:"ref"`
	Kind      string               `json:"kind"`
	Source    string               `json:"source"`
	Level     string               `json:"level"`
	Read      bool                 `json:"read"`
	Date      time.Time            `json:"date"`
	FirstSeen *time.Time           `json:"firstSeen"`
	Count     int                  `json:"count"`
	Resolved  *time.Time           `json:"resolved"`
	Title     NotificationLine     `json:"title"`
	Message   NotificationLine     `json:"message"`
	Subject   *NotificationSubject `json:"subject"`
	Link      string               `json:"link"`
	Legacy    bool                 `json:"legacy"`
	Thread    string               `json:"-"`
	Audience  string               `json:"-"`
	Info      *NotificationInfo    `json:"-"`
}

// NotificationDetails is the details view of one notification.
type NotificationDetails struct {
	NotificationView
	What     *NotificationLine      `json:"what"`
	Means    *NotificationLine      `json:"means"`
	Doing    *NotificationLine      `json:"doing"`
	Steps    []NotificationStepView `json:"steps"`
	Raw      string                 `json:"raw"`
	Facts    []NotificationFactView `json:"facts"`
	Seen     []time.Time            `json:"seen"`
	Thread   []NotificationView     `json:"thread"`
	Security *NotificationSecurity  `json:"security"`
	Resolves string                 `json:"resolves,omitempty"`
}

// NotificationSecretMasker cuts the configuration's secrets out of a text.
// Set by the main package (the Telegram notifier knows them); the generic
// patterns of MaskNotificationText apply in any case.
var NotificationSecretMasker = func(text string) string { return text }

var (
	secretPairPattern = regexp.MustCompile(`(?i)\b(token|api[_-]?key|apikey|key|secret|client[_-]?secret|password|passwd|pwd|pass|cookie|session|authorization|auth|bearer|jwt|signature|sig|otp|code)(\s*[=:]\s*|\s+)("[^"]*"|'[^']*'|[^\s&,;"']+)`)
	bearerPattern     = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	longRandomPattern = regexp.MustCompile(`[A-Za-z0-9+/_=-]{32,}`)
	botTokenPattern   = regexp.MustCompile(`\b[0-9]{5,15}:[A-Za-z0-9_-]{20,80}\b`)
	hasLetterAndDigit = regexp.MustCompile(`[A-Za-z].*[0-9]|[0-9].*[A-Za-z]`)
	// Fork addition: a link to a run, an issue or the actions of a GitHub
	// repository (the dependency update notices build them from numbers,
	// see src/fork_deps_status.go) is not taken for a random string: an
	// owner and a repository name are not secrets.
	githubLinkPattern = regexp.MustCompile(`https://github\.com/[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}/(?:actions/runs/[0-9]{1,16}|issues/[0-9]{1,16}|actions)\b`)
)

// MaskNotificationText hides what looks like a secret in a text shown in
// a notification or a log line.
func MaskNotificationText(text string) string {
	if text == "" {
		return text
	}
	text = NotificationSecretMasker(text)
	text = botTokenPattern.ReplaceAllString(text, "[hidden]")
	text = bearerPattern.ReplaceAllString(text, "$1 [hidden]")
	text = secretPairPattern.ReplaceAllStringFunc(text, func(whole string) string {
		parts := secretPairPattern.FindStringSubmatch(whole)
		value := parts[3]
		// "key" and "code" followed by a word are ordinary words: only a value
		// that looks made up is hidden after them
		if parts[2] != "=" && parts[2] != ":" && !strings.Contains(parts[2], "=") && !strings.Contains(parts[2], ":") {
			if len(value) < 16 || !hasLetterAndDigit.MatchString(value) {
				return whole
			}
		}
		if value == "***" || value == "[hidden]" || strings.EqualFold(value, "bearer") || strings.EqualFold(value, "basic") {
			return whole
		}
		return parts[1] + parts[2] + "[hidden]"
	})
	// the links of githubLinkPattern are kept as they are, the rest is looked at
	kept := githubLinkPattern.FindAllStringIndex(text, -1)
	masked, last := "", 0
	for _, at := range kept {
		masked += maskLongRandom(text[last:at[0]]) + text[at[0]:at[1]]
		last = at[1]
	}
	return masked + maskLongRandom(text[last:])
}

// maskLongRandom hides long strings that look made up.
func maskLongRandom(text string) string {
	return longRandomPattern.ReplaceAllStringFunc(text, func(whole string) string {
		if !hasLetterAndDigit.MatchString(whole) {
			return whole
		}
		// words joined by dashes (a name, a UUID) are not one random string
		short := true
		for _, part := range strings.FieldsFunc(whole, func(r rune) bool { return r == '-' || r == '_' }) {
			short = short && len(part) < 24
		}
		if short && strings.ContainsAny(whole, "-_") {
			return whole
		}
		return "[hidden]"
	})
}

const notificationColumnsSQL = "id, title, message, vars, icon, link, date, level, read, recipient, actions," +
	" fork_kind, fork_source, fork_thread, fork_ref, fork_audience, fork_count, fork_first, fork_resolved, fork_info"

func scanNotification(rows interface{ Scan(...interface{}) error }) (NotificationView, error) {
	var (
		v                                   NotificationView
		title, message, vars, icon, actions string
		recipient, info                     string
		date, first, resolved               int64
		read                                int
	)
	if err := rows.Scan(&v.ID, &title, &message, &vars, &icon, &v.Link, &date, &v.Level, &read, &recipient, &actions,
		&v.Kind, &v.Source, &v.Thread, &v.Ref, &v.Audience, &v.Count, &first, &resolved, &info); err != nil {
		return v, err
	}
	v.Date = MillisToTime(date)
	v.Read = read == 1
	v.Level = normalLevel(v.Level)
	if v.Count < 1 {
		v.Count = 1
	}
	if first > 0 && first != date {
		t := MillisToTime(first)
		v.FirstSeen = &t
	}
	if resolved > 0 {
		t := MillisToTime(resolved)
		v.Resolved = &t
	}
	if v.Source == "" {
		v.Source = "other"
	}
	if v.Kind == "" {
		v.Kind = "other"
	}
	v.Link = safeLink(v.Link)
	if info != "" {
		var parsed NotificationInfo
		if json.Unmarshal([]byte(info), &parsed) == nil {
			v.Info = &parsed
		}
	}
	if v.Info == nil || (v.Info.Title == nil && v.Info.What == nil) {
		v.Legacy = true
		v.Title = NotificationLine{Key: title, Text: legacyNotificationTexts[title]}
		if title == "header.notification.title.serverError" || NotificationEnglish(message) == "" {
			// upstream's server errors carry free text in the message
			v.Message = NotificationLine{Text: MaskNotificationText(message)}
			if NotificationEnglish(message) == "" && vars != "" {
				v.Message.Text = strings.TrimSpace(v.Message.Text + " " + MaskNotificationText(vars))
			}
		} else {
			v.Message = NotificationLine{Key: message, Text: legacyNotificationTexts[message], Vars: map[string]string{"Vars": MaskNotificationText(vars)}}
		}
		return v, nil
	}
	v.Subject = v.Info.Subject
	if v.Subject != nil {
		copySubject := *v.Subject
		copySubject.Name = MaskNotificationText(copySubject.Name)
		copySubject.Link = safeLink(copySubject.Link)
		v.Subject = &copySubject
	}
	if v.Info.Title != nil {
		v.Title = lineOf(*v.Info.Title)
	} else {
		v.Title = NotificationLine{Key: title, Text: NotificationEnglish(title)}
	}
	if v.Info.What != nil {
		v.Message = lineOf(*v.Info.What)
	} else {
		v.Message = NotificationLine{Text: MaskNotificationText(v.Info.Raw)}
	}
	return v, nil
}

func lineOf(t NotificationText) NotificationLine {
	line := NotificationLine{Key: t.Key, Text: NotificationEnglish(t.Key)}
	if len(t.Vars) > 0 {
		line.Vars = map[string]string{}
		for name, value := range t.Vars {
			line.Vars[name] = MaskNotificationText(value)
		}
	}
	return line
}

func linePtr(t *NotificationText) *NotificationLine {
	if t == nil {
		return nil
	}
	line := lineOf(*t)
	return &line
}

// ErrBadNotificationQuery is a query the list does not take.
var ErrBadNotificationQuery = errors.New("bad notification query")

var cursorPattern = regexp.MustCompile(`^([0-9]{1,15}):([0-9]{1,15})$`)

// ListNotificationViews returns one page, newest first by last seen, and
// the cursor of the next page ("" at the end).
func ListNotificationViews(q NotificationQuery) ([]NotificationView, string, error) {
	db, err := MonitoringReadDB()
	if err != nil {
		return nil, "", err
	}
	where, args, err := notificationWhere(q)
	if err != nil {
		return nil, "", err
	}
	if q.Cursor != "" {
		m := cursorPattern.FindStringSubmatch(q.Cursor)
		if m == nil {
			return nil, "", ErrBadNotificationQuery
		}
		ms, _ := strconv.ParseInt(m[1], 10, 64)
		id, _ := strconv.ParseInt(m[2], 10, 64)
		where += " AND (date < ? OR (date = ? AND id < ?))"
		args = append(args, ms, ms, id)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = NotificationPageDefault
	}
	if limit > NotificationPageMax {
		limit = NotificationPageMax
	}
	rows, err := db.Query(MonitoringRebind("SELECT "+notificationColumnsSQL+" FROM notifications WHERE "+where+
		" ORDER BY date DESC, id DESC LIMIT "+strconv.Itoa(limit+1)), args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []NotificationView{}
	for rows.Next() {
		v, err := scanNotification(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		next = strconv.FormatInt(TimeToMillis(last.Date), 10) + ":" + strconv.FormatInt(last.ID, 10)
	}
	return out, next, nil
}

func notificationWhere(q NotificationQuery) (string, []interface{}, error) {
	where := "recipient = ?"
	args := []interface{}{q.Recipient}
	if q.Recipient == "" {
		return "", nil, ErrBadNotificationQuery
	}
	if !q.Admin {
		where += " AND fork_audience <> 'admin'"
	}
	if len(q.Levels) > 0 {
		parts := []string{}
		for _, level := range q.Levels {
			switch level {
			case "error":
				parts = append(parts, "level IN ('error', 'critical', 'fatal')")
			case "warning":
				parts = append(parts, "level IN ('warning', 'warn')")
			case "success":
				parts = append(parts, "level = 'success'")
			case "info":
				parts = append(parts, "level NOT IN ('error', 'critical', 'fatal', 'warning', 'warn', 'success')")
			default:
				return "", nil, ErrBadNotificationQuery
			}
		}
		where += " AND (" + strings.Join(parts, " OR ") + ")"
	}
	if q.Source != "" {
		known := false
		for _, source := range NotificationSources {
			known = known || source == q.Source
		}
		if !known {
			return "", nil, ErrBadNotificationQuery
		}
		if q.Source == "other" {
			where += " AND fork_source IN ('other', '')"
		} else {
			where += " AND fork_source = ?"
			args = append(args, q.Source)
		}
	}
	switch q.Read {
	case "":
	case "read":
		where += " AND read = 1"
	case "unread":
		where += " AND read = 0"
	default:
		return "", nil, ErrBadNotificationQuery
	}
	if q.Search != "" {
		if len([]rune(q.Search)) > NotificationSearchMax {
			return "", nil, ErrBadNotificationQuery
		}
		like := "%" + escapeLike(strings.ToLower(q.Search)) + "%"
		cols := []string{"title", "message", "vars", "fork_kind", "fork_search"}
		parts := []string{}
		for _, col := range cols {
			parts = append(parts, "LOWER("+col+") LIKE ? ESCAPE '\\'")
			args = append(args, like)
		}
		where += " AND (" + strings.Join(parts, " OR ") + ")"
	}
	return where, args, nil
}

// CountUnreadNotifications counts what the bell shows.
func CountUnreadNotifications(recipient string, admin bool) (int, error) {
	db, err := MonitoringReadDB()
	if err != nil {
		return 0, err
	}
	where, args, err := notificationWhere(NotificationQuery{Recipient: recipient, Admin: admin, Read: "unread"})
	if err != nil {
		return 0, err
	}
	var n int
	err = db.QueryRow(MonitoringRebind("SELECT COUNT(*) FROM notifications WHERE "+where), args...).Scan(&n)
	return n, err
}

// NotificationSourcesOf lists the sources the recipient has rows of.
func NotificationSourcesOf(recipient string, admin bool) ([]string, error) {
	db, err := MonitoringReadDB()
	if err != nil {
		return nil, err
	}
	where, args, err := notificationWhere(NotificationQuery{Recipient: recipient, Admin: admin})
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(MonitoringRebind("SELECT DISTINCT fork_source FROM notifications WHERE "+where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var source string
		if err := rows.Scan(&source); err != nil {
			return nil, err
		}
		if source == "" {
			source = "other"
		}
		have[source] = true
	}
	out := []string{}
	for _, source := range NotificationSources {
		if have[source] {
			out = append(out, source)
		}
	}
	return out, rows.Err()
}

// GetNotificationDetails returns one of the recipient's notifications by id
// or by ref, with its thread. ok is false when there is none the reader may see.
func GetNotificationDetails(recipient string, admin bool, id int64, ref string) (NotificationDetails, bool, error) {
	db, err := MonitoringReadDB()
	if err != nil {
		return NotificationDetails{}, false, err
	}
	where, args, err := notificationWhere(NotificationQuery{Recipient: recipient, Admin: admin})
	if err != nil {
		return NotificationDetails{}, false, err
	}
	switch {
	case id > 0:
		where += " AND id = ?"
		args = append(args, id)
	case ref != "":
		where += " AND fork_ref = ?"
		args = append(args, ref)
	default:
		return NotificationDetails{}, false, nil
	}
	row := db.QueryRow(MonitoringRebind("SELECT "+notificationColumnsSQL+" FROM notifications WHERE "+where+" ORDER BY date DESC, id DESC LIMIT 1"), args...)
	v, err := scanNotification(row)
	if err == sql.ErrNoRows {
		return NotificationDetails{}, false, nil
	}
	if err != nil {
		return NotificationDetails{}, false, err
	}
	d := NotificationDetails{NotificationView: v, Steps: []NotificationStepView{}, Facts: []NotificationFactView{}, Seen: []time.Time{}, Thread: []NotificationView{}}
	if !v.Legacy {
		d.What = linePtr(v.Info.What)
		d.Means = linePtr(v.Info.Means)
		d.Doing = linePtr(v.Info.Doing)
		d.Raw = MaskNotificationText(v.Info.Raw)
		d.Security = v.Info.Security
		d.Resolves = v.Info.Resolves
		for _, step := range v.Info.Steps {
			d.Steps = append(d.Steps, NotificationStepView{NotificationLine: lineOf(step.NotificationText), Link: safeLink(step.Link), Action: step.Action})
		}
		for _, fact := range v.Info.Facts {
			d.Facts = append(d.Facts, NotificationFactView{Key: fact.Key, Text: NotificationEnglish(fact.Key), Value: MaskNotificationText(fact.Value)})
		}
		for _, ms := range v.Info.Seen {
			d.Seen = append(d.Seen, MillisToTime(ms))
		}
	} else if v.Info != nil && len(v.Info.Seen) > 0 {
		for _, ms := range v.Info.Seen {
			d.Seen = append(d.Seen, MillisToTime(ms))
		}
		if v.Message.Key == "" {
			d.Raw = v.Message.Text
		}
	} else {
		d.Seen = append(d.Seen, v.Date)
		if v.Message.Key == "" {
			d.Raw = v.Message.Text
		}
	}

	// the same thread: earlier and later notifications about the same thing
	threadKey := v.Thread
	if v.Info != nil && v.Info.Resolves != "" {
		threadKey = v.Info.Resolves
	}
	if threadKey != "" {
		tWhere, tArgs, _ := notificationWhere(NotificationQuery{Recipient: recipient, Admin: admin})
		tWhere += " AND fork_thread = ? AND id <> ?"
		tArgs = append(tArgs, threadKey, v.ID)
		rows, err := db.Query(MonitoringRebind("SELECT "+notificationColumnsSQL+" FROM notifications WHERE "+tWhere+
			" ORDER BY date DESC, id DESC LIMIT "+strconv.Itoa(NotificationThreadMax)), tArgs...)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				other, err := scanNotification(rows)
				if err != nil {
					break
				}
				d.Thread = append(d.Thread, other)
			}
		}
	}
	return d, true, nil
}

// MarkNotifications sets the read flag of the recipient's rows (all of
// them when ids is nil).
func MarkNotifications(recipient string, admin bool, ids []int64, read bool) (int64, error) {
	db, err := MonitoringWriteDB()
	if err != nil {
		return 0, err
	}
	where, args, err := notificationWhere(NotificationQuery{Recipient: recipient, Admin: admin})
	if err != nil {
		return 0, err
	}
	if ids != nil {
		if len(ids) == 0 || len(ids) > NotificationIDsMax {
			return 0, ErrBadNotificationQuery
		}
		where += " AND id IN (" + strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ") + ")"
		for _, id := range ids {
			args = append(args, id)
		}
	}
	flag := 0
	if read {
		flag = 1
	}
	res, err := db.Exec(MonitoringRebind("UPDATE notifications SET read = "+strconv.Itoa(flag)+" WHERE "+where), args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ResolveNotificationByHand marks the problem a notification of the
// recipient's is about resolved, for every recipient: the owner's word
// that it is over (plan 48, review 7, N3). A source that still holds it
// open keeps it open (the Telegram chat too: the source comes first);
// otherwise the chat ends it as "No longer followed". It reports whether
// a problem was open.
func ResolveNotificationByHand(recipient string, admin bool, id int64) (bool, error) {
	db, err := MonitoringReadDB()
	if err != nil {
		return false, err
	}
	where, args, err := notificationWhere(NotificationQuery{Recipient: recipient, Admin: admin})
	if err != nil || id <= 0 {
		return false, ErrBadNotificationQuery
	}
	var thread string
	err = db.QueryRow(MonitoringRebind("SELECT fork_thread FROM notifications WHERE "+where+
		" AND id = ? AND fork_resolved = 0 AND fork_info <> ''"), append(args, id)...).Scan(&thread)
	if err == sql.ErrNoRows || (err == nil && thread == "") {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, resolved := ResolveThread(thread)
	return resolved, nil
}

// DeleteNotifications deletes the recipient's rows by id, or every read
// one when ids is nil.
func DeleteNotifications(recipient string, admin bool, ids []int64) (int64, error) {
	db, err := MonitoringWriteDB()
	if err != nil {
		return 0, err
	}
	where, args, err := notificationWhere(NotificationQuery{Recipient: recipient, Admin: admin})
	if err != nil {
		return 0, err
	}
	if ids == nil {
		where += " AND read = 1"
	} else {
		if len(ids) == 0 || len(ids) > NotificationIDsMax {
			return 0, ErrBadNotificationQuery
		}
		where += " AND id IN (" + strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ") + ")"
		for _, id := range ids {
			args = append(args, id)
		}
	}
	res, err := db.Exec(MonitoringRebind("DELETE FROM notifications WHERE "+where), args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
