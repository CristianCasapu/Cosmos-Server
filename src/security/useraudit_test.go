package security

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// what two users did, what was done to their accounts, and what strangers
// did, around "now" = day 13, 12:00
var auditNow = at(12, 0)

func seedAudit(t *testing.T) {
	t.Helper()
	openDatabase(t)
	seed(t,
		// before maria's account existed: an older account of that name
		Event{Time: at(-200, 0), Kind: utils.SecLoginFailed, IP: "203.0.113.9", Target: "maria", Details: map[string]string{"reason": "wrong_password"}},
		Event{Time: at(-48, 0), Kind: utils.SecLoginSuccess, Actor: "maria", IP: "198.51.100.99"},
		Event{Time: at(-30, 0), Kind: utils.SecUserDeleted, Actor: "admin", IP: "192.0.2.1", Target: "maria", Details: map[string]string{"self": "no"}},
		Event{Time: at(-20, 0), Kind: utils.SecUserCreated, Actor: "admin", IP: "192.0.2.1", Target: "maria", Details: map[string]string{"role": "User"}},
		Event{Time: at(-19, 0), Kind: utils.SecUserInviteUsed, Actor: "maria", IP: "192.168.1.20"},
		Event{Time: at(8, 0), Kind: utils.SecLoginSuccess, Actor: "admin", IP: "192.0.2.1", Details: map[string]string{"mfa": "none"}},
		Event{Time: at(8, 1), Kind: utils.SecConfigSaved, Actor: "admin", IP: "192.0.2.1", Details: map[string]string{"sections": "HTTPConfig"}},
		Event{Time: at(8, 2), Kind: utils.SecContainerAction, Actor: "admin", IP: "192.0.2.1", Target: "maria", Details: map[string]string{"action": "stop"}}, // a container named maria
		Event{Time: at(8, 3), Kind: utils.SecTokenCreated, Actor: "admin", IP: "192.0.2.1", Target: "maria"},                                                  // and a token
		Event{Time: at(9, 0), Kind: utils.SecLoginFailed, IP: "203.0.113.9", Target: "maria", Details: map[string]string{"reason": "wrong_password"}},
		Event{Time: at(9, 1), Kind: utils.SecLoginFailed, IP: "203.0.113.9", Target: "nosuchuser", Details: map[string]string{"reason": "unknown_user"}},
		Event{Time: at(9, 2), Kind: utils.SecLoginFailed, IP: "203.0.113.9", Count: 33, Details: map[string]string{"summary": "1"}},
		Event{Time: at(9, 3), Kind: utils.SecScanProbe, IP: "203.0.113.9", Path: "/.env", Details: map[string]string{"probe": "dotfile"}},
		Event{Time: at(9, 4), Kind: utils.SecShieldBanTemp, IP: "203.0.113.9"},
		Event{Time: at(9, 5), Kind: utils.SecTokenInvalid, IP: "203.0.113.9"},
		Event{Time: at(10, 0), Kind: utils.SecLoginSuccess, Actor: "maria", IP: "192.168.1.20", Details: map[string]string{"mfa": "pending"}},
		Event{Time: at(10, 1), Kind: utils.SecMFAFailed, Actor: "maria", IP: "192.168.1.20", Details: map[string]string{"reason": "wrong_code"}},
		Event{Time: at(10, 2), Kind: utils.SecMFASuccess, Actor: "maria", IP: "192.168.1.20"},
		Event{Time: at(10, 3), Kind: utils.SecAccessDenied, Actor: "maria", IP: "192.168.1.20", Path: "/cosmos/api/config", Details: map[string]string{"permission": "Configuration Read"}},
		Event{Time: at(10, 4), Kind: utils.SecLoginSuccess, Actor: "maria", IP: "10.0.0.5", Details: map[string]string{"mfa": "none"}},
		Event{Time: at(11, 0), Kind: utils.SecUserMFAReset, Actor: "admin", IP: "192.0.2.1", Target: "maria"},
		Event{Time: at(11, 1), Kind: utils.SecLoginSuccess, Actor: "paul", IP: "192.168.1.30"},
		Event{Time: at(11, 2), Kind: utils.SecSudoFailed, Actor: "paul", IP: "192.168.1.30"},
		Event{Time: at(11, 3), Kind: utils.SecRouteCreated, Actor: "paul", IP: "192.168.1.30", Target: "<img src=x onerror=alert(1)>"},
		Event{Time: at(11, 4), Kind: utils.SecUserEdited, Actor: "admin", IP: "192.0.2.1", Target: "paul", Details: map[string]string{"changed": "email"}},
	)
}

func auditKinds(t *testing.T, filter AuditFilter) string {
	t.Helper()
	page, err := AuditList(filter, 0, MaxPageSize)
	if err != nil {
		t.Fatal("AuditList:", err)
	}
	return kindsOf(page.Events)
}

func TestAuditScopeLeavesStrangersOut(t *testing.T) {
	seedAudit(t)
	page, err := AuditList(AuditFilter{}, 0, MaxPageSize)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range page.Events {
		switch {
		case event.Kind == utils.SecScanProbe, event.Kind == utils.SecShieldBanTemp, event.Kind == utils.SecTokenInvalid:
			t.Errorf("a stranger's incident is in the trail: %+v", event)
		case event.Kind == utils.SecLoginFailed && (event.Target == "" || event.Target == "nosuchuser"):
			t.Errorf("a failed login that names no account is in the trail: %+v", event)
		}
	}
	if len(page.Events) != 20 {
		t.Fatalf("%d rows in the trail, want 20: %s", len(page.Events), kindsOf(page.Events))
	}
}

func TestAuditUserIsActorOrAccountTarget(t *testing.T) {
	seedAudit(t)
	got := auditKinds(t, AuditFilter{User: "maria"})
	want := "user.mfa.reset auth.login.success access.denied auth.mfa.success auth.mfa.failed auth.login.success auth.login.failed " +
		"user.invite.used user.created user.deleted auth.login.success auth.login.failed"
	if got != want {
		t.Fatalf("maria's trail:\n got %s\nwant %s", got, want)
	}
	// the container and the token called maria are not about her account
	if strings.Contains(got, "container") || strings.Contains(got, "token") {
		t.Fatal("rows about a container or token of the same name were mixed in")
	}
	if got := auditKinds(t, AuditFilter{User: "paul"}); got != "user.edited route.created auth.sudo.failed auth.login.success" {
		t.Fatalf("paul's trail: %s", got)
	}
	if got := auditKinds(t, AuditFilter{User: "nobody"}); got != "" {
		t.Fatalf("an unknown user's trail: %s", got)
	}
}

func TestAuditFilters(t *testing.T) {
	seedAudit(t)
	cases := []struct {
		name   string
		filter AuditFilter
		want   string
	}{
		{"outcome failed", AuditFilter{User: "maria", Outcomes: []string{"failed"}}, "auth.mfa.failed auth.login.failed auth.login.failed"},
		{"refused", AuditFilter{Outcomes: []string{"denied", "blocked"}}, "access.denied"},
		{"period", AuditFilter{User: "maria", Filter: Filter{From: at(10, 0), To: at(10, 4)}}, "access.denied auth.mfa.success auth.mfa.failed auth.login.success"},
		{"kind family", AuditFilter{User: "maria", Filter: Filter{Kinds: []string{"user.*"}}}, "user.mfa.reset user.invite.used user.created user.deleted"},
		{"address", AuditFilter{Filter: Filter{IP: "10.0.0.5"}}, "auth.login.success"},
		{"text in a target, any case", AuditFilter{Text: "ONERROR"}, "route.created"},
		{"text in details", AuditFilter{Text: "httpconfig"}, "config.saved"},
		{"text in an address", AuditFilter{Text: "203.0.113"}, "auth.login.failed auth.login.failed"},
		{"a wildcard is a character", AuditFilter{Text: "%"}, ""},
		{"an underscore is a character", AuditFilter{Text: "wrong_"}, "auth.mfa.failed auth.login.failed auth.login.failed"},
		{"a backslash is a character", AuditFilter{Text: `\`}, ""},
	}
	for _, c := range cases {
		if got := auditKinds(t, c.filter); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}

	for name, filter := range map[string]AuditFilter{
		"unknown outcome":   {Outcomes: []string{"maybe"}},
		"too many outcomes": {Outcomes: []string{"ok", "ok", "ok", "ok", "ok"}},
		"text too long":     {Text: strings.Repeat("a", maxAuditText+1)},
		"user too long":     {User: strings.Repeat("a", maxActor*4+1)},
		"bad address":       {Filter: Filter{IP: "not-an-address"}},
		"too many kinds":    {Filter: Filter{Kinds: strings.Split(strings.Repeat("auth.*,", 21), ",")}},
	} {
		if _, err := AuditList(filter, 0, 10); err == nil || !strings.Contains(err.Error(), "invalid filter") {
			t.Errorf("%s: err = %v, want a bad filter", name, err)
		}
	}
}

func TestAuditPages(t *testing.T) {
	seedAudit(t)
	seen := map[int64]bool{}
	cursor := int64(0)
	pages := 0
	for {
		page, err := AuditList(AuditFilter{}, cursor, 3)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, event := range page.Events {
			if seen[event.ID] {
				t.Fatalf("row %d on two pages", event.ID)
			}
			seen[event.ID] = true
		}
		if page.NextCursor == 0 {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 20 || pages != 7 {
		t.Fatalf("%d rows on %d pages, want 20 on 7", len(seen), pages)
	}
	if page, _ := AuditList(AuditFilter{}, 0, 100000); len(page.Events) != 20 {
		t.Fatalf("a limit over the maximum gave %d rows", len(page.Events))
	}
}

func TestUserSummary(t *testing.T) {
	seedAudit(t)
	summary, err := UserSummary("maria", auditNow)
	if err != nil {
		t.Fatal(err)
	}
	if summary.LastLogin == nil || summary.LastLogin.IP != "10.0.0.5" || !summary.LastLogin.Time.Equal(at(10, 4)) {
		t.Fatalf("last login = %+v", summary.LastLogin)
	}
	if summary.LastPasswordChange == nil || summary.LastPasswordChange.Kind != utils.SecUserInviteUsed || summary.LastPasswordChange.IP != "192.168.1.20" {
		t.Fatalf("last password change = %+v", summary.LastPasswordChange)
	}
	if summary.Failures24h != (Failures{Password: 1, TwoFactor: 1}) {
		t.Fatalf("failures in 24 h = %+v", summary.Failures24h)
	}
	// the failure of eight days before counts in neither; the summary row of
	// 33 names no account and the unknown name is not maria
	if summary.Failures7d != (Failures{Password: 1, TwoFactor: 1}) {
		t.Fatalf("failures in 7 days = %+v", summary.Failures7d)
	}
	addresses := []string{}
	for _, use := range summary.Addresses {
		addresses = append(addresses, use.IP+"x"+strconv.FormatInt(use.Count, 10))
	}
	// newest first, actors' rows only (the attacker's address is not one she used)
	if strings.Join(addresses, " ") != "10.0.0.5x1 192.168.1.20x5 198.51.100.99x1" {
		t.Fatalf("addresses = %v", addresses)
	}
	if summary.RecordSince == nil || !summary.RecordSince.Equal(at(-200, 0)) {
		t.Fatalf("record since = %v", summary.RecordSince)
	}

	paul, _ := UserSummary("paul", auditNow)
	if paul.Failures24h.Confirm != 1 || paul.LastPasswordChange != nil {
		t.Fatalf("paul = %+v", paul)
	}
	if _, err := UserSummary("", auditNow); err == nil {
		t.Fatal("a summary without a user")
	}
}

// ---- routes ----

var (
	userReader = &utils.AuthContext{Nickname: "alice", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN_READ, utils.PERM_USERS_READ}}
	maria      = &utils.AuthContext{Nickname: "maria", Permissions: []utils.Permission{utils.PERM_LOGIN}}
	paul       = &utils.AuthContext{Nickname: "paul", Permissions: []utils.Permission{utils.PERM_LOGIN}}
	mariaHalf  = &utils.AuthContext{Nickname: "maria", Permissions: []utils.Permission{utils.PERM_LOGIN}, MFAState: 1}
	mariaToken = &utils.AuthContext{Nickname: "maria", APIToken: &utils.APITokenContext{Name: "phone", Owner: "maria", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN_READ}}}
)

func TestAuditRoutesAreForAdminReadersOnly(t *testing.T) {
	seedAudit(t)
	for path, handler := range map[string]http.HandlerFunc{
		"/cosmos/api/security/audit":                   AuditRoute,
		"/cosmos/api/security/user-summary?user=maria": UserSummaryRoute,
	} {
		for name, c := range map[string]struct {
			auth *utils.AuthContext
			want int
		}{
			"nobody":                        {nil, http.StatusUnauthorized},
			"a user without the permission": {plainUser, http.StatusUnauthorized},
			"maria herself":                 {maria, http.StatusUnauthorized},
			"a session without its code":    {halfLogged, http.StatusUnauthorized},
			"a token without it":            {otherToken, http.StatusForbidden},
			"an admin reader":               {adminReader, http.StatusOK},
			"a token with it":               {readToken, http.StatusOK},
		} {
			w := call(handler, "GET", path, c.auth)
			if w.Code != c.want {
				t.Errorf("GET %s as %s: %d, want %d", path, name, w.Code, c.want)
			}
			if c.want != http.StatusOK && (strings.Contains(w.Body.String(), "192.168") || strings.Contains(w.Body.String(), "auth.login")) {
				t.Errorf("GET %s as %s: the refusal carries data: %s", path, name, w.Body.String())
			}
		}
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			if w := call(handler, method, path, adminReader); w.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: %d, want 405", method, path, w.Code)
			}
		}
	}
}

func TestAuditRoute(t *testing.T) {
	seedAudit(t)
	var page Page
	query := url.Values{"user": {"maria"}, "outcome": {"failed"}, "limit": {"2"}}
	decode(t, call(AuditRoute, "GET", "/cosmos/api/security/audit?"+query.Encode(), adminReader), &page)
	if kindsOf(page.Events) != "auth.mfa.failed auth.login.failed" || page.NextCursor == 0 {
		t.Fatalf("page = %s, next %d", kindsOf(page.Events), page.NextCursor)
	}
	query.Set("cursor", strconv.FormatInt(page.NextCursor, 10))
	var next Page
	decode(t, call(AuditRoute, "GET", "/cosmos/api/security/audit?"+query.Encode(), adminReader), &next)
	if kindsOf(next.Events) != "auth.login.failed" || next.NextCursor != 0 {
		t.Fatalf("next page = %s, next %d", kindsOf(next.Events), next.NextCursor)
	}

	// what a request carried comes back as text in JSON, not as markup
	body := call(AuditRoute, "GET", "/cosmos/api/security/audit?q=onerror", adminReader).Body.String()
	if !strings.Contains(body, `\u003cimg src=x onerror=alert(1)\u003e`) {
		t.Fatalf("answer = %s", body)
	}

	for name, query := range map[string]string{
		"bad outcome":        "outcome=maybe",
		"text too long":      "q=" + strings.Repeat("a", 65),
		"limit over the max": "limit=201",
		"bad cursor":         "cursor=-1",
		"class is not here":  "class=incident",
		"actor is not here":  "actor=maria",
		"a huge parameter":   "user=" + strings.Repeat("a", 5000),
		"bad time":           "from=yesterday",
	} {
		if w := call(AuditRoute, "GET", "/cosmos/api/security/audit?"+query, adminReader); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400: %s", name, w.Code, w.Body.String())
		}
	}
}

func useAccounts(t *testing.T) {
	t.Helper()
	if err := utils.InitStore(); err != nil {
		t.Fatal("InitStore:", err)
	}
	t.Cleanup(utils.CloseStore)
	for _, user := range []utils.User{
		{Nickname: "maria", Password: "x", Role: utils.USER, CreatedAt: at(-20, 0), RegisteredAt: at(-19, 0), LastPasswordChangedAt: at(-19, 0),
			LastLogin: at(10, 4), MFAKey: "secret", Was2FAVerified: true},
		{Nickname: "paul", Password: "x", Role: utils.USER, CreatedAt: at(-100, 0)},
	} {
		if err := utils.CreateUser(user); err != nil {
			t.Fatal("CreateUser:", err)
		}
	}
}

func TestUserSummaryRouteGivesAccountFactsOnlyToUserReaders(t *testing.T) {
	seedAudit(t)
	useAccounts(t)

	var withAccount struct {
		Summary
		Account *AccountFacts `json:"account"`
	}
	decode(t, call(UserSummaryRoute, "GET", "/cosmos/api/security/user-summary?user=maria", userReader), &withAccount)
	if withAccount.Account == nil || !withAccount.Account.Exists || !withAccount.Account.TwoFactor || !withAccount.Account.Registered ||
		withAccount.Account.PasswordChangedAt == nil || withAccount.LastLogin == nil {
		t.Fatalf("summary for a user reader = %+v, account %+v", withAccount, withAccount.Account)
	}
	if strings.Contains(call(UserSummaryRoute, "GET", "/cosmos/api/security/user-summary?user=maria", userReader).Body.String(), "secret") {
		t.Fatal("the two-factor secret is in the answer")
	}

	var withoutAccount struct {
		Account *AccountFacts `json:"account"`
		User    string        `json:"user"`
	}
	decode(t, call(UserSummaryRoute, "GET", "/cosmos/api/security/user-summary?user=maria", adminReader), &withoutAccount)
	if withoutAccount.Account != nil || withoutAccount.User != "maria" {
		t.Fatalf("a log reader who may not read users got the account: %+v", withoutAccount.Account)
	}

	var gone struct {
		Account *AccountFacts `json:"account"`
	}
	decode(t, call(UserSummaryRoute, "GET", "/cosmos/api/security/user-summary?user=deleted", userReader), &gone)
	if gone.Account == nil || gone.Account.Exists {
		t.Fatalf("a user that does not exist: %+v", gone.Account)
	}

	for name, query := range map[string]string{"no user": "", "a huge user": "user=" + strings.Repeat("a", 600)} {
		if w := call(UserSummaryRoute, "GET", "/cosmos/api/security/user-summary?"+query, userReader); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, w.Code)
		}
	}
}

func myActivity(t *testing.T, auth *utils.AuthContext, query string) (Page, string) {
	t.Helper()
	w := call(MyActivityRoute, "GET", "/cosmos/api/security/my-activity"+query, auth)
	var page Page
	decode(t, w, &page)
	return page, w.Body.String()
}

func TestMyActivityIsStrictlyTheOwnAccount(t *testing.T) {
	seedAudit(t)
	useAccounts(t)

	page, body := myActivity(t, maria, "?limit=50")
	// her account was created at -20 h: the older account of the same name
	// (its login at -48 h, its deletion at -30 h, a failure at -200 h) is not hers
	want := "user.mfa.reset auth.login.success access.denied auth.mfa.success auth.mfa.failed auth.login.success auth.login.failed user.invite.used user.created"
	if kindsOf(page.Events) != want {
		t.Fatalf("maria's own activity:\n got %s\nwant %s", kindsOf(page.Events), want)
	}
	for _, event := range page.Events {
		if event.Actor != "" && event.Actor != "maria" {
			t.Errorf("someone else's name on a row: %+v", event)
		}
		if event.Actor != "maria" && (event.IP != "" || event.Details != nil || event.Path != "" || event.Host != "") {
			t.Errorf("a row by someone else keeps who, where or details: %+v", event)
		}
		if event.Actor == "maria" && event.IP == "" {
			t.Errorf("her own row lost her address: %+v", event)
		}
	}
	for _, other := range []string{"paul", "admin", "203.0.113.9", "192.0.2.1", "198.51.100.99", "nosuchuser", "HTTPConfig", "onerror", "jellyfin"} {
		if strings.Contains(body, other) {
			t.Errorf("maria's answer mentions %q: %s", other, body)
		}
	}

	paulPage, paulBody := myActivity(t, paul, "")
	if kindsOf(paulPage.Events) != "user.edited route.created auth.sudo.failed auth.login.success" {
		t.Fatalf("paul's own activity: %s", kindsOf(paulPage.Events))
	}
	if strings.Contains(paulBody, "maria") || strings.Contains(paulBody, "10.0.0.5") {
		t.Fatalf("paul reads maria's rows: %s", paulBody)
	}

	// one page at a time, and nothing chooses whose rows they are
	first, _ := myActivity(t, maria, "?limit=4")
	if len(first.Events) != 4 || first.NextCursor == 0 {
		t.Fatalf("first page: %d rows, next %d", len(first.Events), first.NextCursor)
	}
	rest, _ := myActivity(t, maria, "?limit=50&cursor="+strconv.FormatInt(first.NextCursor, 10))
	if len(rest.Events) != 5 {
		t.Fatalf("second page: %s", kindsOf(rest.Events))
	}
	for name, query := range map[string]string{
		"another user":       "?user=paul",
		"an actor":           "?actor=paul",
		"a target":           "?target=paul",
		"a filter":           "?kind=auth.*",
		"limit over the max": "?limit=51",
		"bad cursor":         "?cursor=x",
	} {
		if w := call(MyActivityRoute, "GET", "/cosmos/api/security/my-activity"+query, maria); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400: %s", name, w.Code, w.Body.String())
		}
	}
}

func TestMyActivityNeedsAFullSession(t *testing.T) {
	seedAudit(t)
	useAccounts(t)
	for name, c := range map[string]struct {
		auth *utils.AuthContext
		want int
	}{
		"nobody":                               {nil, http.StatusUnauthorized},
		"a session without its code":           {mariaHalf, http.StatusUnauthorized},
		"an API token, even an admin reader's": {mariaToken, http.StatusForbidden},
		"a token without an owner":             {&utils.AuthContext{APIToken: &utils.APITokenContext{Name: "ci", Permissions: []utils.Permission{utils.PERM_ADMIN_READ}}}, http.StatusForbidden},
		"maria":                                {maria, http.StatusOK},
	} {
		w := call(MyActivityRoute, "GET", "/cosmos/api/security/my-activity", c.auth)
		if w.Code != c.want {
			t.Errorf("%s: %d, want %d", name, w.Code, c.want)
		}
		if c.want != http.StatusOK && strings.Contains(w.Body.String(), "auth.") {
			t.Errorf("%s: the refusal carries data: %s", name, w.Body.String())
		}
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		if w := call(MyActivityRoute, method, "/cosmos/api/security/my-activity", maria); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d, want 405", method, w.Code)
		}
	}
	// a session whose account was deleted meanwhile sees nothing
	ghost := &utils.AuthContext{Nickname: "nosuchuser", Permissions: []utils.Permission{utils.PERM_LOGIN}}
	if page, _ := myActivity(t, ghost, ""); len(page.Events) != 0 {
		t.Fatalf("a deleted account's session got %s", kindsOf(page.Events))
	}
}

func TestRedactForSelf(t *testing.T) {
	own := Event{Kind: utils.SecLoginSuccess, Actor: "maria", IP: "10.0.0.5", Details: map[string]string{"mfa": "none"}}
	if got := redactForSelf(own, "maria"); got.IP != "10.0.0.5" || got.Details["mfa"] != "none" {
		t.Fatalf("own row = %+v", got)
	}
	other := Event{Kind: utils.SecLoginFailed, IP: "203.0.113.9", Target: "maria", Host: "h", Path: "/p", Method: "POST", Details: map[string]string{"reason": "wrong_password"}}
	got := redactForSelf(other, "maria")
	if got.IP != "" || got.Host != "" || got.Path != "" || got.Method != "" || got.Details != nil || got.Target != "maria" || got.Kind != utils.SecLoginFailed {
		t.Fatalf("someone else's row = %+v", got)
	}
}
