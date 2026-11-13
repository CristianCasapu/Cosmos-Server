package security

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The dashboard's incident list: the record's list, incidents only, with a
// group and a text to look for.

// MaxSearchText bounds the text the list looks for.
const MaxSearchText = 100

// likePattern turns a text into a LIKE pattern that matches it anywhere,
// with the wildcards of the text taken literally (escape character \).
func likePattern(text string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(text))
	return "%" + escaped + "%"
}

// searchedColumns are the columns the text is looked for in.
var searchedColumns = []string{"kind", "actor", "ip", "target", "host", "path", "method", "details"}

// SearchIncidents lists the incidents a filter, a group ("" for all) and a
// text ("" for any) select, newest first, like List.
func SearchIncidents(filter Filter, group string, text string, cursor int64, limit int) (Page, error) {
	page := Page{Events: []Event{}}
	filter.Class = ClassIncident
	if group != "" {
		found, ok := groupByID(group)
		if !ok {
			return page, badFilter("unknown group")
		}
		if len(filter.Kinds) > 0 {
			return page, badFilter("give a group or kinds, not both")
		}
		filter.Kinds = found.Kinds
	}
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) > MaxSearchText || !utf8.ValidString(text) {
		return page, badFilter("the text to look for is too long")
	}
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
	if text != "" {
		pattern := likePattern(text)
		conditions := make([]string, len(searchedColumns))
		for i, column := range searchedColumns {
			conditions[i] = "LOWER(" + column + ") LIKE ? ESCAPE '\\'"
			args = append(args, pattern)
		}
		where += " AND (" + strings.Join(conditions, " OR ") + ")"
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

// IncidentsRoute godoc
// @Summary Security dashboard: incidents
// @Description Lists the incidents (never audit entries), newest first, a page at a time, by group, severity, address and text. Admin read permission.
// @Tags security
// @Produce json
// @Security BearerAuth
// @Param group query string false "login, lockout, ban, scan, access or other"
// @Param q query string false "Text to look for (at most 100 characters)"
// @Param from query string false "As for the list"
// @Param to query string false "As for the list"
// @Param kind query string false "As for the list"
// @Param severity query string false "As for the list"
// @Param ip query string false "As for the list"
// @Param cursor query int false "nextCursor of the page before"
// @Param limit query int false "Page size, 50 by default, at most 200"
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/security/incidents [get]
func IncidentsRoute(w http.ResponseWriter, req *http.Request) {
	if !allowed(w, req) {
		return
	}
	query := req.URL.Query()
	filter, err := parseFilter(query)
	if err != nil {
		refuse(w, err)
		return
	}
	if filter.Class == ClassAudit {
		refuse(w, badFilter("this list has incidents only"))
		return
	}
	cursor, err := parseInt(query, "cursor", 1<<62)
	if err != nil {
		refuse(w, err)
		return
	}
	limit, err := parseInt(query, "limit", MaxPageSize)
	if err != nil {
		refuse(w, err)
		return
	}
	page, err := SearchIncidents(filter, query.Get("group"), query.Get("q"), cursor, int(limit))
	if err != nil {
		refuse(w, err)
		return
	}
	answer(w, page)
}
