package security

// Fork addition: the routes of the user audit panel, all GET, all bounded.
//
//   /api/security/audit         the trail of user actions, for log readers (PERM_ADMIN_READ)
//   /api/security/user-summary  what the record says about one account, for log readers
//   /api/security/my-activity   the own account's trail, for any logged-in session
//
// None of them writes.

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// MaxSelfPageSize bounds a page of the own activity.
const MaxSelfPageSize = 50

// parseAuditFilter reads the parameters of the trail: those of parseFilter
// that apply (from, to, kind, ip) and user, outcome, q.
func parseAuditFilter(query url.Values) (AuditFilter, error) {
	var filter AuditFilter
	base, err := parseFilter(query)
	if err != nil {
		return filter, err
	}
	for _, name := range []string{"class", "severity", "actor", "target"} {
		if query.Has(name) {
			return filter, badFilter(name + " is not a parameter of this route (use user)")
		}
	}
	filter.Filter = base
	filter.User = query.Get("user")
	for _, value := range query["outcome"] {
		for _, outcome := range strings.Split(value, ",") {
			if outcome = strings.TrimSpace(outcome); outcome != "" {
				filter.Outcomes = append(filter.Outcomes, outcome)
			}
		}
	}
	filter.Text = query.Get("q")
	return filter, nil
}

// AuditRoute godoc
// @Summary Security record: the user audit trail
// @Description What users did and what happened to their accounts, newest first, a page at a time: audit rows, incidents of logged-in sessions and incidents that name an account. Admin read permission.
// @Tags security
// @Produce json
// @Security BearerAuth
// @Param from query string false "Start (RFC 3339), included"
// @Param to query string false "End (RFC 3339), excluded"
// @Param kind query string false "Kinds, comma separated or repeated; auth.* selects a family"
// @Param ip query string false "Client address"
// @Param user query string false "Nickname: what the user did and what was done to the account"
// @Param outcome query string false "ok, failed, denied, blocked; comma separated or repeated"
// @Param q query string false "Text contained in any field, at most 64 characters"
// @Param cursor query int false "nextCursor of the page before"
// @Param limit query int false "Page size, 50 by default, at most 200"
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/security/audit [get]
func AuditRoute(w http.ResponseWriter, req *http.Request) {
	if !allowed(w, req) {
		return
	}
	query := req.URL.Query()
	filter, err := parseAuditFilter(query)
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
	page, err := AuditList(filter, cursor, int(limit))
	if err != nil {
		refuse(w, err)
		return
	}
	answer(w, page)
}

// AccountFacts is what the account database says about a user: it holds
// what happened before the record existed.
type AccountFacts struct {
	Exists     bool       `json:"exists"`
	TwoFactor  bool       `json:"twoFactor"`
	Registered bool       `json:"registered"`
	CreatedAt  *time.Time `json:"createdAt"`
	LastLogin  *time.Time `json:"lastLogin"`
	// PasswordChangedAt is when a password was last set (registration or
	// reset link).
	PasswordChangedAt *time.Time `json:"passwordChangedAt"`
}

type userSummaryAnswer struct {
	Summary
	// Account is null when the caller may not read users (PERM_USERS_READ).
	Account *AccountFacts `json:"account"`
}

func timeOrNil(at time.Time) *time.Time {
	if at.IsZero() || at.Unix() <= 0 {
		return nil
	}
	return &at
}

func accountFacts(nickname string) (*AccountFacts, error) {
	user, err := utils.GetUser(nickname)
	if errors.Is(err, utils.ErrNotFound) {
		return &AccountFacts{}, nil
	}
	if err != nil {
		return nil, err
	}
	return &AccountFacts{
		Exists:            true,
		TwoFactor:         user.MFAKey != "" && user.Was2FAVerified,
		Registered:        timeOrNil(user.RegisteredAt) != nil,
		CreatedAt:         timeOrNil(user.CreatedAt),
		LastLogin:         timeOrNil(user.LastLogin),
		PasswordChangedAt: timeOrNil(user.LastPasswordChangedAt),
	}, nil
}

// UserSummaryRoute godoc
// @Summary Security record: what is known about one account
// @Description Last login, failed attempts in the last 24 hours and 7 days, last password change, addresses used in the last 30 days, and the start of the record; the account's two-factor state and dates only for callers who may read users. Admin read permission.
// @Tags security
// @Produce json
// @Security BearerAuth
// @Param user query string true "Nickname"
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/security/user-summary [get]
func UserSummaryRoute(w http.ResponseWriter, req *http.Request) {
	if !allowed(w, req) {
		return
	}
	query := req.URL.Query()
	user := query.Get("user")
	if len(user) > maxQueryValue {
		refuse(w, badFilter("user is too long"))
		return
	}
	summary, err := UserSummary(user, time.Now())
	if err != nil {
		refuse(w, err)
		return
	}
	result := userSummaryAnswer{Summary: summary}
	if utils.HasPermission(req, utils.PERM_USERS_READ) {
		if result.Account, err = accountFacts(user); err != nil {
			refuse(w, err)
			return
		}
	}
	answer(w, result)
}

// MyActivityRoute godoc
// @Summary Security record: recent activity on the own account
// @Description The caller's own trail, newest first: what the user did, and what happened to the account (shown without who did it, from where or details). Sessions only, API tokens are refused.
// @Tags security
// @Produce json
// @Security BearerAuth
// @Param cursor query int false "nextCursor of the page before"
// @Param limit query int false "Page size, 20 by default, at most 50"
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/security/my-activity [get]
func MyActivityRoute(w http.ResponseWriter, req *http.Request) {
	// a token is not a person looking at the own account: refused, so a
	// leaked token does not give away its owner's history
	if utils.GetAuthContext(req).APIToken != nil {
		utils.HTTPError(w, "Only for a logged-in session", http.StatusForbidden, "SEC003")
		return
	}
	// logged in, with the two-factor code given
	if utils.CheckPermissions(w, req, utils.PERM_LOGIN) != nil {
		return
	}
	if req.Method != "GET" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	self := utils.GetAuthContext(req).Nickname

	query := req.URL.Query()
	for name := range query {
		if name != "cursor" && name != "limit" {
			refuse(w, badFilter(cleanText(name, 32)+" is not a parameter of this route"))
			return
		}
	}
	cursor, err := parseInt(query, "cursor", 1<<62)
	if err != nil {
		refuse(w, err)
		return
	}
	limit, err := parseInt(query, "limit", MaxSelfPageSize)
	if err != nil {
		refuse(w, err)
		return
	}
	if limit == 0 {
		limit = 20
	}

	filter := AuditFilter{User: self}
	// a nickname used again after its account was deleted does not inherit
	// the old account's history
	account, err := utils.GetUser(self)
	if errors.Is(err, utils.ErrNotFound) {
		answer(w, Page{Events: []Event{}})
		return
	}
	if err != nil {
		refuse(w, err)
		return
	}
	filter.From = account.CreatedAt
	if account.CreatedAt.Unix() <= 0 {
		filter.From = time.Time{}
	}

	page, err := AuditList(filter, cursor, int(limit))
	if err != nil {
		refuse(w, err)
		return
	}
	for i := range page.Events {
		page.Events[i] = redactForSelf(page.Events[i], self)
	}
	answer(w, page)
}
