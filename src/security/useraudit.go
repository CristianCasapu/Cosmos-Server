package security

// Fork addition: the queries of the user audit panel. What users did and
// what happened to their accounts: audit rows, incidents of a logged-in
// session, and the incidents that name an existing account (a wrong
// password for it, a reset asked for it). Incidents of strangers (scans,
// bans, unknown hostnames, bad tokens) are left out: that is the security
// dashboard's subject.

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Bounds of the audit queries.
const (
	maxAuditText      = 64 // characters of the free text
	maxAuditOutcomes  = 4
	summaryAddresses  = 10
	summaryAddressAge = 30 * 24 * time.Hour
)

// accountIncidentKinds are the incidents whose target is the account they
// were aimed at. A failed login with reason unknown_user names no account
// and is left out (see unknownUserLike).
var accountIncidentKinds = []string{
	utils.SecLoginFailed,
	utils.SecMFALocked,
	utils.SecResetRequested,
	utils.SecRegisterFailed,
}

// accountTargetKinds are the kinds whose target is an account: matching a
// user by target is limited to these, so a container or a token that has
// the same name as a user is not mixed in.
var accountTargetKinds = append(append([]string{}, accountIncidentKinds...),
	utils.SecUserCreated,
	utils.SecUserDeleted,
	utils.SecUserEdited,
	utils.SecUserRoleChanged,
	utils.SecUserInviteCreated,
	utils.SecUserResetCreated,
	utils.SecUserMFAReset,
)

// unknownUserLike matches the stored details of a failed login whose name
// is no account. Details are written by json.Marshal, so this text cannot
// come from inside a value (a quote in a value is stored as \").
const unknownUserLike = `%"reason":"unknown\_user"%`

// AuditFilter selects rows of the user audit trail.
type AuditFilter struct {
	// From, To, Kinds and IP of Filter apply; Class, MinSeverity, Actor and
	// Target are ignored (the scope and User replace them).
	Filter
	// User keeps the rows done by this user and those about this account.
	User string
	// Outcomes keeps the rows with one of these outcomes (ok, failed,
	// denied, blocked); empty keeps all.
	Outcomes []string
	// Text keeps the rows where any text field contains it, ignoring case.
	Text string
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func stringArgs(values []string) []interface{} {
	args := make([]interface{}, len(values))
	for i, value := range values {
		args[i] = value
	}
	return args
}

// likeEscape makes a text a LIKE pattern that matches it literally inside
// a longer text.
func likeEscape(text string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + replacer.Replace(text) + "%"
}

// scopeCondition is what the audit trail is about: users.
func scopeCondition() (string, []interface{}) {
	args := []interface{}{ClassAudit}
	args = append(args, stringArgs(accountIncidentKinds)...)
	args = append(args, unknownUserLike)
	return "(class = ? OR actor != '' OR (kind IN (" + placeholders(len(accountIncidentKinds)) +
		") AND target != '' AND details NOT LIKE ? ESCAPE '\\'))", args
}

// userCondition keeps what a user did and what was done to the account.
func userCondition(user string) (string, []interface{}) {
	args := []interface{}{user, user}
	args = append(args, stringArgs(accountTargetKinds)...)
	return "(actor = ? OR (target = ? AND kind IN (" + placeholders(len(accountTargetKinds)) + ")))", args
}

func (f AuditFilter) where() (string, []interface{}, error) {
	base := f.Filter
	base.Class, base.MinSeverity, base.Actor, base.Target = "", 0, "", ""
	where, args, err := base.where()
	if err != nil {
		return "", nil, err
	}

	scope, scopeArgs := scopeCondition()
	where += " AND " + scope
	args = append(args, scopeArgs...)

	if f.User != "" {
		if len(f.User) > maxActor*4 {
			return "", nil, badFilter("user is too long")
		}
		condition, userArgs := userCondition(f.User)
		where += " AND " + condition
		args = append(args, userArgs...)
	}

	if len(f.Outcomes) > 0 {
		if len(f.Outcomes) > maxAuditOutcomes {
			return "", nil, badFilter("too many outcomes")
		}
		for _, outcome := range f.Outcomes {
			if !outcomes[outcome] {
				return "", nil, badFilter("outcome must be ok, failed, denied or blocked")
			}
		}
		where += " AND outcome IN (" + placeholders(len(f.Outcomes)) + ")"
		args = append(args, stringArgs(f.Outcomes)...)
	}

	if text := strings.TrimSpace(f.Text); text != "" {
		if len([]rune(text)) > maxAuditText {
			return "", nil, badFilter("the search text is too long (at most " + strconv.Itoa(maxAuditText) + " characters)")
		}
		pattern := likeEscape(strings.ToLower(text))
		columns := []string{"actor", "target", "ip", "path", "host", "kind", "details"}
		parts := make([]string, len(columns))
		for i, column := range columns {
			parts[i] = "LOWER(" + column + ") LIKE ? ESCAPE '\\'"
			args = append(args, pattern)
		}
		where += " AND (" + strings.Join(parts, " OR ") + ")"
	}
	return where, args, nil
}

// scanEvents reads rows of eventColumns.
func scanEvents(rows *sql.Rows) ([]Event, error) {
	events := []Event{}
	for rows.Next() {
		var event Event
		var at int64
		var severity int
		var details string
		if err := rows.Scan(&event.ID, &at, &event.Kind, &event.Class, &severity, &event.Actor, &event.IP,
			&event.Target, &event.Host, &event.Path, &event.Method, &event.Outcome, &event.Count, &details); err != nil {
			return events, err
		}
		event.Time = utils.MillisToTime(at)
		event.Severity = Severity(severity).String()
		if details != "" {
			json.Unmarshal([]byte(details), &event.Details)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// AuditList returns the rows of the user audit trail a filter selects,
// newest first, paged like List.
func AuditList(filter AuditFilter, cursor int64, limit int) (Page, error) {
	page := Page{Events: []Event{}}
	where, args, err := filter.where()
	if err != nil {
		return page, err
	}
	if cursor < 0 {
		return page, badFilter("cursor must not be negative")
	}
	if limit <= 0 {
		limit = DefaultPageSize
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}
	if cursor > 0 {
		where += " AND id < ?"
		args = append(args, cursor)
	}

	db, err := readDB()
	if err != nil {
		return page, err
	}
	rows, err := db.Query(utils.MonitoringRebind(
		"SELECT "+eventColumns+" FROM "+tableName+" WHERE "+where+" ORDER BY id DESC LIMIT "+strconv.Itoa(limit+1)), args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()

	if page.Events, err = scanEvents(rows); err != nil {
		return page, err
	}
	if len(page.Events) > limit {
		page.Events = page.Events[:limit]
		page.NextCursor = page.Events[limit-1].ID
	}
	return page, nil
}

// Sighting is a time and an address: a login, a password change.
type Sighting struct {
	Time time.Time `json:"time"`
	IP   string    `json:"ip"`
	Kind string    `json:"kind,omitempty"`
}

// AddressUse is an address an account was used from.
type AddressUse struct {
	IP    string    `json:"ip"`
	Count int64     `json:"count"`
	Last  time.Time `json:"last"`
}

// Failures counts the failed attempts against an account in a span of
// time, as events (summary rows count for what they stand for).
type Failures struct {
	// Password: logins refused for this account with a wrong password (or
	// before it was registered, or a malformed request naming it).
	Password int64 `json:"password"`
	// TwoFactor: wrong two-factor codes given by a session of the account.
	TwoFactor int64 `json:"twoFactor"`
	// Locked: times the account's two-factor check was closed.
	Locked int64 `json:"locked"`
	// Confirm: wrong passwords at the confirmation prompt (sudo).
	Confirm int64 `json:"confirm"`
}

// Summary is what the record says about one account.
type Summary struct {
	User      string    `json:"user"`
	LastLogin *Sighting `json:"lastLogin"`
	// LastPasswordChange is the last auth.password.changed or
	// user.invite.used of the account.
	LastPasswordChange *Sighting    `json:"lastPasswordChange"`
	Failures24h        Failures     `json:"failures24h"`
	Failures7d         Failures     `json:"failures7d"`
	Addresses          []AddressUse `json:"addresses"`
	// RecordSince is the time of the oldest row of the record: nothing
	// before it is known. Null when the record is empty.
	RecordSince *time.Time `json:"recordSince"`
}

func lastSighting(db *sql.DB, kinds []string, actor string) (*Sighting, error) {
	args := append(stringArgs(kinds), actor)
	var at int64
	var sighting Sighting
	err := db.QueryRow(utils.MonitoringRebind(
		"SELECT ts, ip, kind FROM "+tableName+" WHERE kind IN ("+placeholders(len(kinds))+") AND actor = ? ORDER BY id DESC LIMIT 1"),
		args...).Scan(&at, &sighting.IP, &sighting.Kind)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sighting.Time = utils.MillisToTime(at)
	return &sighting, nil
}

func sumOf(db *sql.DB, condition string, args ...interface{}) (int64, error) {
	var total sql.NullInt64
	err := db.QueryRow(utils.MonitoringRebind("SELECT SUM(n) FROM "+tableName+" WHERE "+condition), args...).Scan(&total)
	return total.Int64, err
}

func failuresSince(db *sql.DB, user string, since time.Time) (Failures, error) {
	var failures Failures
	var err error
	from := utils.TimeToMillis(since)
	if failures.Password, err = sumOf(db, "kind = ? AND target = ? AND ts >= ? AND details NOT LIKE ? ESCAPE '\\'",
		utils.SecLoginFailed, user, from, unknownUserLike); err != nil {
		return failures, err
	}
	if failures.TwoFactor, err = sumOf(db, "kind = ? AND actor = ? AND ts >= ?", utils.SecMFAFailed, user, from); err != nil {
		return failures, err
	}
	if failures.Locked, err = sumOf(db, "kind = ? AND target = ? AND ts >= ?", utils.SecMFALocked, user, from); err != nil {
		return failures, err
	}
	failures.Confirm, err = sumOf(db, "kind = ? AND actor = ? AND ts >= ?", utils.SecSudoFailed, user, from)
	return failures, err
}

// RecordSince returns the time of the oldest row, zero when there is none.
func RecordSince() (time.Time, error) {
	db, err := readDB()
	if err != nil {
		return time.Time{}, err
	}
	return recordSince(db)
}

func recordSince(db *sql.DB) (time.Time, error) {
	var oldest sql.NullInt64
	if err := db.QueryRow("SELECT MIN(ts) FROM " + tableName).Scan(&oldest); err != nil {
		return time.Time{}, err
	}
	if !oldest.Valid {
		return time.Time{}, nil
	}
	return utils.MillisToTime(oldest.Int64), nil
}

// UserSummary sums up what the record says about an account.
func UserSummary(user string, now time.Time) (Summary, error) {
	summary := Summary{User: user, Addresses: []AddressUse{}}
	if user == "" {
		return summary, badFilter("user is required")
	}
	if len(user) > maxActor*4 {
		return summary, badFilter("user is too long")
	}
	db, err := readDB()
	if err != nil {
		return summary, err
	}

	if summary.LastLogin, err = lastSighting(db, []string{utils.SecLoginSuccess}, user); err != nil {
		return summary, err
	}
	if summary.LastPasswordChange, err = lastSighting(db, []string{utils.SecPasswordChanged, utils.SecUserInviteUsed}, user); err != nil {
		return summary, err
	}
	if summary.Failures24h, err = failuresSince(db, user, now.Add(-24*time.Hour)); err != nil {
		return summary, err
	}
	if summary.Failures7d, err = failuresSince(db, user, now.Add(-7*24*time.Hour)); err != nil {
		return summary, err
	}

	rows, err := db.Query(utils.MonitoringRebind(
		"SELECT ip, SUM(n), MAX(ts) FROM "+tableName+" WHERE actor = ? AND ip != '' AND ts >= ?"+
			" GROUP BY ip ORDER BY MAX(ts) DESC, ip LIMIT "+strconv.Itoa(summaryAddresses)),
		user, utils.TimeToMillis(now.Add(-summaryAddressAge)))
	if err != nil {
		return summary, err
	}
	defer rows.Close()
	for rows.Next() {
		var use AddressUse
		var last int64
		if err := rows.Scan(&use.IP, &use.Count, &last); err != nil {
			return summary, err
		}
		use.Last = utils.MillisToTime(last)
		summary.Addresses = append(summary.Addresses, use)
	}
	if err := rows.Err(); err != nil {
		return summary, err
	}

	since, err := recordSince(db)
	if err != nil {
		return summary, err
	}
	if !since.IsZero() {
		summary.RecordSince = &since
	}
	return summary, nil
}

// redactForSelf is what a user may see of a row about the own account:
// everything of what the user did; of what someone else did (an
// administrator's change, a failed login against the account) only what
// happened and when, never who, from where or with what details.
func redactForSelf(event Event, self string) Event {
	if event.Actor == self {
		return event
	}
	event.Actor, event.IP, event.Host, event.Path, event.Method = "", "", "", "", ""
	event.Details = nil
	return event
}
