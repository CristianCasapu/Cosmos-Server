package security

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Bounds of the queries.
const (
	DefaultPageSize = 50
	MaxPageSize     = 200
	MaxTopIPs       = 100
	MaxBuckets      = 1000
	maxFilterKinds  = 20
)

// ErrBadFilter is wrapped by the errors a filter that cannot be answered
// gives; the read API turns it into a 400.
var ErrBadFilter = errors.New("security: invalid filter")

func badFilter(reason string) error {
	return errors.Join(ErrBadFilter, errors.New(reason))
}

// Filter selects events. The zero value selects everything.
type Filter struct {
	// From and To bound the time of the events: From is included, To is not.
	From time.Time
	To   time.Time
	// Kinds are kind identifiers or prefixes ending in ".*" ("auth.*").
	// Entries that match no kind are ignored; if none of them matches, the
	// filter matches nothing.
	Kinds []string
	// Class is ClassIncident, ClassAudit or "" for both.
	Class string
	// MinSeverity keeps the events of at least this severity.
	MinSeverity Severity
	// IP, Actor and Target match exactly.
	IP     string
	Actor  string
	Target string
}

func (f Filter) where() (string, []interface{}, error) {
	conditions := []string{"1 = 1"}
	args := []interface{}{}

	if !f.From.IsZero() {
		conditions = append(conditions, "ts >= ?")
		args = append(args, utils.TimeToMillis(f.From))
	}
	if !f.To.IsZero() {
		conditions = append(conditions, "ts < ?")
		args = append(args, utils.TimeToMillis(f.To))
	}
	if len(f.Kinds) > 0 {
		if len(f.Kinds) > maxFilterKinds {
			return "", nil, badFilter("too many kinds")
		}
		seen := map[string]bool{}
		kinds := []string{}
		for _, pattern := range f.Kinds {
			for _, kind := range kindsMatching(pattern) {
				if !seen[kind] {
					seen[kind] = true
					kinds = append(kinds, kind)
				}
			}
		}
		if len(kinds) == 0 {
			conditions = append(conditions, "1 = 0")
		} else {
			conditions = append(conditions, "kind IN ("+strings.TrimSuffix(strings.Repeat("?, ", len(kinds)), ", ")+")")
			for _, kind := range kinds {
				args = append(args, kind)
			}
		}
	}
	switch f.Class {
	case "":
	case ClassIncident, ClassAudit:
		conditions = append(conditions, "class = ?")
		args = append(args, f.Class)
	default:
		return "", nil, badFilter("class must be incident or audit")
	}
	if f.MinSeverity != 0 {
		if _, known := severityNames[f.MinSeverity]; !known {
			return "", nil, badFilter("unknown severity")
		}
		conditions = append(conditions, "severity >= ?")
		args = append(args, int(f.MinSeverity))
	}
	if f.IP != "" {
		ip := cleanIP(f.IP)
		if ip == "" {
			return "", nil, badFilter("ip is not an address")
		}
		conditions = append(conditions, "ip = ?")
		args = append(args, ip)
	}
	if f.Actor != "" {
		if len(f.Actor) > maxActor*4 {
			return "", nil, badFilter("actor is too long")
		}
		conditions = append(conditions, "actor = ?")
		args = append(args, f.Actor)
	}
	if f.Target != "" {
		if len(f.Target) > maxTarget*4 {
			return "", nil, badFilter("target is too long")
		}
		conditions = append(conditions, "target = ?")
		args = append(args, f.Target)
	}
	return strings.Join(conditions, " AND "), args, nil
}

// Page is one page of events, newest first.
type Page struct {
	Events []Event `json:"events"`
	// NextCursor is the cursor of the next (older) page, 0 when this is the
	// last one.
	NextCursor int64 `json:"nextCursor"`
}

const eventColumns = "id, ts, kind, class, severity, actor, ip, target, host, path, method, outcome, n, details"

// List returns the events a filter selects, newest first, limit at a time
// (DefaultPageSize when 0, at most MaxPageSize). cursor is 0 for the first
// page and the NextCursor of the page before for the following ones.
func List(filter Filter, cursor int64, limit int) (Page, error) {
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
	// one more than asked for tells whether there is a next page
	rows, err := db.Query(utils.MonitoringRebind(
		"SELECT "+eventColumns+" FROM "+tableName+" WHERE "+where+" ORDER BY id DESC LIMIT "+strconv.Itoa(limit+1)), args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()

	for rows.Next() {
		var event Event
		var at int64
		var severity int
		var details string
		if err := rows.Scan(&event.ID, &at, &event.Kind, &event.Class, &severity, &event.Actor, &event.IP,
			&event.Target, &event.Host, &event.Path, &event.Method, &event.Outcome, &event.Count, &details); err != nil {
			return page, err
		}
		event.Time = utils.MillisToTime(at)
		event.Severity = Severity(severity).String()
		if details != "" {
			json.Unmarshal([]byte(details), &event.Details)
		}
		page.Events = append(page.Events, event)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Events) > limit {
		page.Events = page.Events[:limit]
		page.NextCursor = page.Events[limit-1].ID
	}
	return page, nil
}

// ActorHistory returns what one user did and what was recorded about
// requests made as that user, newest first.
func ActorHistory(actor string, cursor int64, limit int) (Page, error) {
	if actor == "" {
		return Page{Events: []Event{}}, badFilter("actor is required")
	}
	return List(Filter{Actor: actor}, cursor, limit)
}

// KindCount is how often one kind occurred.
type KindCount struct {
	Kind     string `json:"kind"`
	Class    string `json:"class"`
	Severity string `json:"severity"`
	// Rows is the number of rows, Total the number of events they stand for
	// (summary rows count for as many events as they summarise).
	Rows  int64     `json:"rows"`
	Total int64     `json:"total"`
	Last  time.Time `json:"last"`
}

// CountByKind counts the events a filter selects, per kind, most frequent
// first.
func CountByKind(filter Filter) ([]KindCount, error) {
	counts := []KindCount{}
	where, args, err := filter.where()
	if err != nil {
		return counts, err
	}
	db, err := readDB()
	if err != nil {
		return counts, err
	}
	rows, err := db.Query(utils.MonitoringRebind(
		"SELECT kind, COUNT(*), SUM(n), MAX(ts) FROM "+tableName+" WHERE "+where+
			" GROUP BY kind ORDER BY SUM(n) DESC, kind"), args...)
	if err != nil {
		return counts, err
	}
	defer rows.Close()

	for rows.Next() {
		var count KindCount
		var last int64
		if err := rows.Scan(&count.Kind, &count.Rows, &count.Total, &last); err != nil {
			return counts, err
		}
		count.Last = utils.MillisToTime(last)
		if known, ok := catalogue[count.Kind]; ok {
			count.Class, count.Severity = known.Class, known.Severity
		}
		counts = append(counts, count)
	}
	return counts, rows.Err()
}

// Bucket is the number of events in one span of time.
type Bucket struct {
	Start     time.Time `json:"start"`
	Incidents int64     `json:"incidents"`
	Audit     int64     `json:"audit"`
}

// Buckets counts the events a filter selects per span of time, incidents
// and audit entries side by side, oldest first, with the empty spans
// included. size is the length of a span (an hour, a day); offset shifts
// where spans start (the offset of a time zone makes days start at local
// midnight). The filter must have a From and a To, at most MaxBuckets spans
// apart.
func Buckets(filter Filter, size time.Duration, offset time.Duration) ([]Bucket, error) {
	buckets := []Bucket{}
	if size < time.Minute {
		return buckets, badFilter("a bucket is at least a minute")
	}
	if filter.From.IsZero() || filter.To.IsZero() || !filter.To.After(filter.From) {
		return buckets, badFilter("buckets need a from and a later to")
	}
	if offset <= -24*time.Hour || offset >= 24*time.Hour {
		return buckets, badFilter("offset out of range")
	}
	where, args, err := filter.where()
	if err != nil {
		return buckets, err
	}

	sizeMs, offsetMs := size.Milliseconds(), offset.Milliseconds()
	index := func(at time.Time) int64 { return (utils.TimeToMillis(at) + offsetMs) / sizeMs }
	first, last := index(filter.From), index(filter.To.Add(-time.Millisecond))
	if last-first+1 > MaxBuckets {
		return buckets, badFilter("too many buckets: narrow the time range or use larger buckets")
	}

	db, err := readDB()
	if err != nil {
		return buckets, err
	}
	// the arguments of the bucket expression come before those of the filter
	queryArgs := append([]interface{}{offsetMs, sizeMs}, args...)
	rows, err := db.Query(utils.MonitoringRebind(
		"SELECT (ts + ?) / ? AS bucket, class, SUM(n) FROM "+tableName+" WHERE "+where+
			" GROUP BY bucket, class ORDER BY bucket"), queryArgs...)
	if err != nil {
		return buckets, err
	}
	defer rows.Close()

	for i := first; i <= last; i++ {
		buckets = append(buckets, Bucket{Start: utils.MillisToTime(i*sizeMs - offsetMs)})
	}
	for rows.Next() {
		var bucket, total int64
		var class string
		if err := rows.Scan(&bucket, &class, &total); err != nil {
			return buckets, err
		}
		if bucket < first || bucket > last {
			continue
		}
		if class == ClassAudit {
			buckets[bucket-first].Audit += total
		} else {
			buckets[bucket-first].Incidents += total
		}
	}
	return buckets, rows.Err()
}

// IPCount is what one address did.
type IPCount struct {
	IP string `json:"ip"`
	// Total is the number of events, Rows the number of rows, Kinds the
	// number of different kinds.
	Total int64     `json:"total"`
	Rows  int64     `json:"rows"`
	Kinds int64     `json:"kinds"`
	First time.Time `json:"first"`
	Last  time.Time `json:"last"`
}

// TopIPs returns the addresses with the most events among those a filter
// selects, limit of them (10 when 0, at most MaxTopIPs).
func TopIPs(filter Filter, limit int) ([]IPCount, error) {
	top := []IPCount{}
	where, args, err := filter.where()
	if err != nil {
		return top, err
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > MaxTopIPs {
		limit = MaxTopIPs
	}
	db, err := readDB()
	if err != nil {
		return top, err
	}
	rows, err := db.Query(utils.MonitoringRebind(
		"SELECT ip, SUM(n), COUNT(*), COUNT(DISTINCT kind), MIN(ts), MAX(ts) FROM "+tableName+
			" WHERE "+where+" AND ip != '' GROUP BY ip ORDER BY SUM(n) DESC, MAX(ts) DESC, ip LIMIT "+strconv.Itoa(limit)), args...)
	if err != nil {
		return top, err
	}
	defer rows.Close()

	for rows.Next() {
		var count IPCount
		var first, last int64
		if err := rows.Scan(&count.IP, &count.Total, &count.Rows, &count.Kinds, &first, &last); err != nil {
			return top, err
		}
		count.First, count.Last = utils.MillisToTime(first), utils.MillisToTime(last)
		top = append(top, count)
	}
	return top, rows.Err()
}
