package security

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The read API of the record. Four routes, all GET, all for whoever may
// read the server's logs (PERM_ADMIN_READ, like /cosmos/_logs next to which
// they are registered), all bounded. There is no route that writes.

// allowed checks the caller and the method. It has answered when it returns
// false.
func allowed(w http.ResponseWriter, req *http.Request) bool {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN_READ) != nil {
		return false
	}
	if req.Method != "GET" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return false
	}
	return true
}

func answer(w http.ResponseWriter, data interface{}) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
		"data":   data,
	})
}

// refuse answers a query that failed: 400 when the request was wrong, 500
// otherwise. The reason of a 500 goes to the log, not to the caller.
func refuse(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrBadFilter) {
		reason := err.Error()
		if _, after, found := strings.Cut(reason, "\n"); found {
			reason = after
		}
		utils.HTTPError(w, "Invalid request: "+reason, http.StatusBadRequest, "SEC001")
		return
	}
	utils.Error("Security events: query failed", err)
	utils.HTTPError(w, "The security events could not be read", http.StatusInternalServerError, "SEC002")
}

// maxQueryValue bounds every query parameter.
const maxQueryValue = 512

func parseTime(query url.Values, name string) (time.Time, error) {
	value := query.Get(name)
	if value == "" {
		return time.Time{}, nil
	}
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, badFilter(name + " must be a time like 2026-11-13T10:00:00Z")
	}
	return at, nil
}

func parseInt(query url.Values, name string, max int64) (int64, error) {
	value := query.Get(name)
	if value == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 || (max > 0 && n > max) {
		return 0, badFilter(name + " must be a number between 0 and " + strconv.FormatInt(max, 10))
	}
	return n, nil
}

// parseFilter reads the filter parameters shared by the routes: from, to,
// kind (repeated or comma separated, "auth.*" for a prefix), class,
// severity (the minimum), ip, actor, target.
func parseFilter(query url.Values) (Filter, error) {
	var filter Filter
	for name, values := range query {
		for _, value := range values {
			if len(value) > maxQueryValue {
				return filter, badFilter(cleanText(name, 32) + " is too long")
			}
		}
	}

	var err error
	if filter.From, err = parseTime(query, "from"); err != nil {
		return filter, err
	}
	if filter.To, err = parseTime(query, "to"); err != nil {
		return filter, err
	}
	for _, value := range query["kind"] {
		for _, kind := range strings.Split(value, ",") {
			if kind = strings.TrimSpace(kind); kind != "" {
				filter.Kinds = append(filter.Kinds, kind)
			}
		}
	}
	filter.Class = query.Get("class")
	if name := query.Get("severity"); name != "" {
		severity, known := ParseSeverity(name)
		if !known {
			return filter, badFilter("severity must be info, low, medium or high")
		}
		filter.MinSeverity = severity
	}
	filter.IP = query.Get("ip")
	filter.Actor = query.Get("actor")
	filter.Target = query.Get("target")
	return filter, nil
}

// EventsRoute godoc
// @Summary Security events: list
// @Description Lists the security record, newest first, a page at a time. Admin read permission.
// @Tags security
// @Produce json
// @Security BearerAuth
// @Param from query string false "Start (RFC 3339), included"
// @Param to query string false "End (RFC 3339), excluded"
// @Param kind query string false "Kinds, comma separated or repeated; auth.* selects a family"
// @Param class query string false "incident or audit"
// @Param severity query string false "Minimum severity: info, low, medium, high"
// @Param ip query string false "Client address"
// @Param actor query string false "Nickname of the user"
// @Param target query string false "What the event is about"
// @Param cursor query int false "nextCursor of the page before"
// @Param limit query int false "Page size, 50 by default, at most 200"
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/security/events [get]
func EventsRoute(w http.ResponseWriter, req *http.Request) {
	if !allowed(w, req) {
		return
	}
	query := req.URL.Query()
	filter, err := parseFilter(query)
	if err != nil {
		refuse(w, err)
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

	page, err := List(filter, cursor, int(limit))
	if err != nil {
		refuse(w, err)
		return
	}
	answer(w, page)
}

// countsAnswer is the answer of CountsRoute.
type countsAnswer struct {
	Kinds   []KindCount `json:"kinds"`
	Buckets []Bucket    `json:"buckets"`
}

// CountsRoute godoc
// @Summary Security events: counts per kind and over time
// @Description Counts the events a filter selects per kind and, when bucket is given, per hour or day (incidents and audit entries side by side). Admin read permission.
// @Tags security
// @Produce json
// @Security BearerAuth
// @Param from query string false "Start (RFC 3339); required with bucket"
// @Param to query string false "End (RFC 3339); required with bucket"
// @Param bucket query string false "hour or day"
// @Param tz query int false "Minutes east of UTC at which days start, -1439 to 1439"
// @Param kind query string false "As for the list"
// @Param class query string false "As for the list"
// @Param severity query string false "As for the list"
// @Param ip query string false "As for the list"
// @Param actor query string false "As for the list"
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/security/counts [get]
func CountsRoute(w http.ResponseWriter, req *http.Request) {
	if !allowed(w, req) {
		return
	}
	query := req.URL.Query()
	filter, err := parseFilter(query)
	if err != nil {
		refuse(w, err)
		return
	}

	result := countsAnswer{Buckets: []Bucket{}}
	if result.Kinds, err = CountByKind(filter); err != nil {
		refuse(w, err)
		return
	}

	if bucket := query.Get("bucket"); bucket != "" {
		size := time.Hour
		switch bucket {
		case "hour":
		case "day":
			size = 24 * time.Hour
		default:
			refuse(w, badFilter("bucket must be hour or day"))
			return
		}
		offset := 0
		if value := query.Get("tz"); value != "" {
			minutes, err := strconv.Atoi(value)
			if err != nil || minutes <= -1440 || minutes >= 1440 {
				refuse(w, badFilter("tz must be a number of minutes between -1439 and 1439"))
				return
			}
			offset = minutes
		}
		if result.Buckets, err = Buckets(filter, size, time.Duration(offset)*time.Minute); err != nil {
			refuse(w, err)
			return
		}
	}
	answer(w, result)
}

// TopIPsRoute godoc
// @Summary Security events: the busiest addresses
// @Description Lists the client addresses with the most events among those a filter selects. Admin read permission.
// @Tags security
// @Produce json
// @Security BearerAuth
// @Param from query string false "As for the list"
// @Param to query string false "As for the list"
// @Param kind query string false "As for the list"
// @Param class query string false "As for the list"
// @Param severity query string false "As for the list"
// @Param limit query int false "How many, 10 by default, at most 100"
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/security/top-ips [get]
func TopIPsRoute(w http.ResponseWriter, req *http.Request) {
	if !allowed(w, req) {
		return
	}
	query := req.URL.Query()
	filter, err := parseFilter(query)
	if err != nil {
		refuse(w, err)
		return
	}
	limit, err := parseInt(query, "limit", MaxTopIPs)
	if err != nil {
		refuse(w, err)
		return
	}
	top, err := TopIPs(filter, int(limit))
	if err != nil {
		refuse(w, err)
		return
	}
	answer(w, top)
}

// KindsRoute godoc
// @Summary Security events: the kinds
// @Description Lists every kind of security event with its class, severity, label and description. Admin read permission.
// @Tags security
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/security/kinds [get]
func KindsRoute(w http.ResponseWriter, req *http.Request) {
	if !allowed(w, req) {
		return
	}
	answer(w, Kinds())
}
