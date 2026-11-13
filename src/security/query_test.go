package security

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

var day = time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)

func at(hour int, minute int) time.Time {
	return day.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

// a small history: a brute force, a scan, and what two users did
func seedHistory(t *testing.T) {
	t.Helper()
	openDatabase(t)
	seed(t,
		Event{Time: at(8, 0), Kind: utils.SecLoginSuccess, Actor: "alice", IP: "192.0.2.10"},
		Event{Time: at(8, 5), Kind: utils.SecSudoSuccess, Actor: "alice", IP: "192.0.2.10"},
		Event{Time: at(8, 10), Kind: utils.SecContainerAction, Actor: "alice", IP: "192.0.2.10", Target: "jellyfin", Details: map[string]string{"action": "restart"}},
		Event{Time: at(9, 0), Kind: utils.SecLoginFailed, IP: "203.0.113.7", Target: "admin", Details: map[string]string{"reason": "unknown_user"}},
		Event{Time: at(9, 1), Kind: utils.SecLoginFailed, IP: "203.0.113.7", Target: "alice", Details: map[string]string{"reason": "wrong_password"}},
		Event{Time: at(9, 2), Kind: utils.SecLoginFailed, IP: "203.0.113.7", Count: 40, Details: map[string]string{"summary": "1"}},
		Event{Time: at(9, 30), Kind: utils.SecScanProbe, IP: "198.51.100.20", Path: "/.env", Details: map[string]string{"probe": "dotfile"}},
		Event{Time: at(9, 31), Kind: utils.SecScanProbe, IP: "198.51.100.20", Path: "/wp-login.php", Details: map[string]string{"probe": "wordpress"}},
		Event{Time: at(10, 0), Kind: utils.SecShieldBanTemp, IP: "203.0.113.7", Details: map[string]string{"limit": "escalation"}},
		Event{Time: at(11, 0), Kind: utils.SecLoginSuccess, Actor: "bob", IP: "192.0.2.11"},
		Event{Time: at(11, 5), Kind: utils.SecAccessDenied, Actor: "bob", IP: "192.0.2.11", Path: "/cosmos/api/config", Details: map[string]string{"permission": "Configuration Read (view config)"}},
		Event{Time: at(26, 0), Kind: utils.SecUserRoleChanged, Actor: "alice", IP: "192.0.2.10", Target: "bob", Details: map[string]string{"role": "Admin"}},
	)
}

func kindsOf(events []Event) string {
	kinds := make([]string, len(events))
	for i, event := range events {
		kinds[i] = event.Kind
	}
	return strings.Join(kinds, " ")
}

func TestListFilters(t *testing.T) {
	seedHistory(t)

	for name, c := range map[string]struct {
		filter Filter
		want   int
	}{
		"everything":            {Filter{}, 12},
		"incidents":             {Filter{Class: ClassIncident}, 7},
		"audit":                 {Filter{Class: ClassAudit}, 5},
		"at least high":         {Filter{MinSeverity: SeverityHigh}, 2},
		"high incidents":        {Filter{MinSeverity: SeverityHigh, Class: ClassIncident}, 1},
		"one kind":              {Filter{Kinds: []string{utils.SecLoginFailed}}, 3},
		"two kinds":             {Filter{Kinds: []string{utils.SecLoginFailed, utils.SecScanProbe}}, 5},
		"a family":              {Filter{Kinds: []string{"auth.*"}}, 6},
		"a family and a kind":   {Filter{Kinds: []string{"auth.login.*", utils.SecShieldBanTemp, utils.SecShieldBanTemp}}, 6},
		"unknown kind":          {Filter{Kinds: []string{"no.such.kind"}}, 0},
		"a bare star":           {Filter{Kinds: []string{"*"}}, 0},
		"half a word":           {Filter{Kinds: []string{"auth.log*"}}, 0},
		"one address":           {Filter{IP: "203.0.113.7"}, 4},
		"address, another form": {Filter{IP: "::ffff:203.0.113.7"}, 4},
		"one user":              {Filter{Actor: "alice"}, 4},
		"about someone":         {Filter{Target: "bob"}, 1},
		"from":                  {Filter{From: at(11, 0)}, 3},
		"to is excluded":        {Filter{To: at(9, 0)}, 3},
		"a window":              {Filter{From: at(9, 0), To: at(10, 0)}, 5},
		"combined":              {Filter{From: at(9, 0), To: at(12, 0), Class: ClassIncident, IP: "203.0.113.7", MinSeverity: SeverityMedium}, 4},
	} {
		page, err := List(c.filter, 0, 0)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(page.Events) != c.want {
			t.Errorf("%s: %d events (%s), want %d", name, len(page.Events), kindsOf(page.Events), c.want)
		}
		for i := 1; i < len(page.Events); i++ {
			if page.Events[i].ID >= page.Events[i-1].ID {
				t.Errorf("%s: not newest first", name)
			}
		}
	}

	for name, filter := range map[string]Filter{
		"class":         {Class: "everything"},
		"severity":      {MinSeverity: 9},
		"address":       {IP: "not an address"},
		"address range": {IP: "203.0.113.0/24"},
		"too many":      {Kinds: strings.Split(strings.Repeat("auth.* ", maxFilterKinds+1), " ")},
		"long actor":    {Actor: strings.Repeat("a", 1000)},
	} {
		if _, err := List(filter, 0, 0); !errors.Is(err, ErrBadFilter) {
			t.Errorf("a filter with a bad %s was answered: %v", name, err)
		}
	}
}

func TestListPagesWithACursor(t *testing.T) {
	seedHistory(t)

	seen := []int64{}
	cursor := int64(0)
	pages := 0
	for {
		page, err := List(Filter{}, cursor, 5)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if len(page.Events) > 5 {
			t.Fatalf("a page of %d events, want at most 5", len(page.Events))
		}
		for _, event := range page.Events {
			seen = append(seen, event.ID)
		}
		if page.NextCursor == 0 {
			break
		}
		if page.NextCursor != page.Events[len(page.Events)-1].ID {
			t.Fatalf("next cursor %d is not the last id of the page", page.NextCursor)
		}
		cursor = page.NextCursor
	}
	if pages != 3 || len(seen) != 12 {
		t.Fatalf("%d pages with %d events, want 3 pages with 12", pages, len(seen))
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] >= seen[i-1] {
			t.Fatalf("event %d repeated or out of order across pages: %v", seen[i], seen)
		}
	}

	// a page that ends exactly at the last row says so
	if page, _ := List(Filter{}, 0, 12); page.NextCursor != 0 || len(page.Events) != 12 {
		t.Fatalf("a full last page: %d events, next cursor %d", len(page.Events), page.NextCursor)
	}
	// the bounds
	if page, _ := List(Filter{}, 0, 100000); len(page.Events) != 12 {
		t.Fatalf("limit over the maximum: %d events", len(page.Events))
	}
	if _, err := List(Filter{}, -1, 0); !errors.Is(err, ErrBadFilter) {
		t.Fatalf("a negative cursor was answered: %v", err)
	}
	// new events do not shift a page that was already asked for
	seed(t, Event{Kind: utils.SecLogout, Actor: "alice"})
	if page, _ := List(Filter{}, seen[4], 5); len(page.Events) != 5 || page.Events[0].ID != seen[5] {
		t.Fatalf("the second page changed after a new event arrived")
	}
}

func TestListLimitIsBounded(t *testing.T) {
	openDatabase(t)
	many := make([]Event, 0, MaxPageSize+50)
	for i := 0; i < MaxPageSize+50; i++ {
		many = append(many, Event{Kind: utils.SecLoginSuccess, Actor: "alice"})
	}
	seed(t, many...)
	page, err := List(Filter{}, 0, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != MaxPageSize || page.NextCursor == 0 {
		t.Fatalf("%d events for a limit of 5000, want %d and a next page", len(page.Events), MaxPageSize)
	}
	if page, _ := List(Filter{}, 0, 0); len(page.Events) != DefaultPageSize {
		t.Fatalf("%d events without a limit, want %d", len(page.Events), DefaultPageSize)
	}
}

func TestActorHistory(t *testing.T) {
	seedHistory(t)
	page, err := ActorHistory("alice", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := kindsOf(page.Events); got != "user.role.changed container.action auth.sudo.success auth.login.success" {
		t.Fatalf("history of alice = %s", got)
	}
	if _, err := ActorHistory("", 0, 0); !errors.Is(err, ErrBadFilter) {
		t.Fatalf("the history of nobody was answered: %v", err)
	}
}

func TestCountByKindCountsWhatSummariesStandFor(t *testing.T) {
	seedHistory(t)
	counts, err := CountByKind(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 8 {
		t.Fatalf("%d kinds counted, want 8", len(counts))
	}
	first := counts[0]
	if first.Kind != utils.SecLoginFailed || first.Rows != 3 || first.Total != 42 || first.Class != ClassIncident ||
		first.Severity != "medium" || !first.Last.Equal(at(9, 2)) {
		t.Fatalf("most frequent kind = %+v, want 42 failed logins in 3 rows", first)
	}

	incidents, _ := CountByKind(Filter{Class: ClassIncident, From: at(9, 30)})
	if len(incidents) != 3 {
		t.Fatalf("%d incident kinds since 9:30, want 3: %+v", len(incidents), incidents)
	}
}

func TestBucketsAreDenseAndSplitByClass(t *testing.T) {
	seedHistory(t)

	hours, err := Buckets(Filter{From: at(8, 0), To: at(12, 0)}, time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []Bucket{
		{Start: at(8, 0), Audit: 3},
		{Start: at(9, 0), Incidents: 44},
		{Start: at(10, 0), Incidents: 1},
		{Start: at(11, 0), Incidents: 1, Audit: 1},
	}
	if len(hours) != len(want) {
		t.Fatalf("%d buckets, want %d: %+v", len(hours), len(want), hours)
	}
	for i := range want {
		if !hours[i].Start.Equal(want[i].Start) || hours[i].Incidents != want[i].Incidents || hours[i].Audit != want[i].Audit {
			t.Errorf("bucket %d = %+v, want %+v", i, hours[i], want[i])
		}
	}

	// days in UTC, then days that start at local midnight two hours east
	days, err := Buckets(Filter{From: at(0, 0), To: at(48, 0)}, 24*time.Hour, 0)
	if err != nil || len(days) != 2 || days[0].Incidents+days[0].Audit != 50 || days[1].Audit != 1 {
		t.Fatalf("days = %+v (%v)", days, err)
	}
	local, err := Buckets(Filter{From: at(-2, 0), To: at(46, 0)}, 24*time.Hour, 2*time.Hour)
	if err != nil || len(local) != 2 || !local[0].Start.Equal(at(-2, 0)) || local[1].Audit != 1 {
		t.Fatalf("local days = %+v (%v)", local, err)
	}

	// a filter narrows the buckets too
	one, _ := Buckets(Filter{From: at(8, 0), To: at(12, 0), IP: "203.0.113.7"}, time.Hour, 0)
	if one[1].Incidents != 42 || one[3].Incidents != 0 {
		t.Fatalf("buckets of one address = %+v", one)
	}

	for name, call := range map[string]func() ([]Bucket, error){
		"no range":    func() ([]Bucket, error) { return Buckets(Filter{}, time.Hour, 0) },
		"backwards":   func() ([]Bucket, error) { return Buckets(Filter{From: at(9, 0), To: at(8, 0)}, time.Hour, 0) },
		"too many":    func() ([]Bucket, error) { return Buckets(Filter{From: at(0, 0), To: at(24*100, 0)}, time.Hour, 0) },
		"tiny bucket": func() ([]Bucket, error) { return Buckets(Filter{From: at(0, 0), To: at(1, 0)}, time.Second, 0) },
		"offset": func() ([]Bucket, error) {
			return Buckets(Filter{From: at(0, 0), To: at(1, 0)}, time.Hour, 30*time.Hour)
		},
	} {
		if _, err := call(); !errors.Is(err, ErrBadFilter) {
			t.Errorf("buckets with %s were answered: %v", name, err)
		}
	}
}

func TestTopIPs(t *testing.T) {
	seedHistory(t)
	top, err := TopIPs(Filter{Class: ClassIncident}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 3 {
		t.Fatalf("%d addresses, want 3: %+v", len(top), top)
	}
	first := top[0]
	if first.IP != "203.0.113.7" || first.Total != 43 || first.Rows != 4 || first.Kinds != 2 ||
		!first.First.Equal(at(9, 0)) || !first.Last.Equal(at(10, 0)) {
		t.Fatalf("busiest address = %+v", first)
	}
	if top[1].IP != "198.51.100.20" || top[1].Total != 2 {
		t.Fatalf("second address = %+v", top[1])
	}
	if one, _ := TopIPs(Filter{}, 1); len(one) != 1 {
		t.Fatalf("limit 1 gave %d addresses", len(one))
	}
	if all, _ := TopIPs(Filter{}, 100000); len(all) != 4 {
		t.Fatalf("a limit over the maximum gave %d addresses, want the 4 there are", len(all))
	}
}

// ---- the read API ----

func apiRequest(method string, target string, auth *utils.AuthContext) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	if auth != nil {
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
	}
	return req
}

var (
	adminReader = &utils.AuthContext{Nickname: "alice", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN_READ}}
	plainUser   = &utils.AuthContext{Nickname: "bob", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_RESOURCES_READ, utils.PERM_CONFIGURATION_READ, utils.PERM_USERS_READ}}
	halfLogged  = &utils.AuthContext{Nickname: "alice", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN_READ}, MFAState: 1}
	readToken   = &utils.AuthContext{Nickname: "alice", APIToken: &utils.APITokenContext{Name: "monitor", Owner: "alice", Permissions: []utils.Permission{utils.PERM_ADMIN_READ}}}
	otherToken  = &utils.AuthContext{Nickname: "alice", APIToken: &utils.APITokenContext{Name: "deploy", Owner: "alice", Permissions: []utils.Permission{utils.PERM_RESOURCES, utils.PERM_RESOURCES_READ}}}
)

var routes = map[string]http.HandlerFunc{
	"/cosmos/api/security/events":  EventsRoute,
	"/cosmos/api/security/counts":  CountsRoute,
	"/cosmos/api/security/top-ips": TopIPsRoute,
	"/cosmos/api/security/kinds":   KindsRoute,
}

func call(handler http.HandlerFunc, method string, target string, auth *utils.AuthContext) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	handler(w, apiRequest(method, target, auth))
	return w
}

func TestAPIIsForAdminReadersOnlyAndReadOnly(t *testing.T) {
	seedHistory(t)
	startServerRecorder(t) // what the permission check reports is stored

	for path, handler := range routes {
		for name, c := range map[string]struct {
			auth *utils.AuthContext
			want int
		}{
			"nobody":                        {nil, http.StatusUnauthorized},
			"a user without the permission": {plainUser, http.StatusUnauthorized},
			"a session without its code":    {halfLogged, http.StatusUnauthorized},
			"a token without it":            {otherToken, http.StatusForbidden},
			"an admin reader":               {adminReader, http.StatusOK},
			"a token with it":               {readToken, http.StatusOK},
		} {
			w := call(handler, "GET", path, c.auth)
			if w.Code != c.want {
				t.Errorf("GET %s as %s: %d, want %d", path, name, w.Code, c.want)
			}
			if c.want != http.StatusOK && (strings.Contains(w.Body.String(), "203.0.113.7") || strings.Contains(w.Body.String(), "auth.login")) {
				t.Errorf("GET %s as %s: the refusal carries data: %s", path, name, w.Body.String())
			}
		}
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			if w := call(handler, method, path, adminReader); w.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: %d, want 405", method, path, w.Code)
			}
		}
	}

	// The calls changed nothing that was there; the only new rows are the
	// refusals themselves, which the permission check records.
	Flush()
	seeded, refused := 0, 0
	for _, event := range all(t) {
		if event.Kind == utils.SecAccessDenied && strings.HasPrefix(event.Path, "/cosmos/api/security/") {
			refused++
			if event.Actor == "" || event.IP == "" || event.Details["permission"] == "" {
				t.Errorf("refusal recorded without who, from where or what: %+v", event)
			}
			continue
		}
		seeded++
	}
	if seeded != 12 {
		t.Fatalf("%d events besides the refusals, want the 12 that were there: the API must not write", seeded)
	}
	// per route: the user without the permission and the token without it
	if refused != 2*len(routes) {
		t.Fatalf("%d refusals recorded, want %d", refused, 2*len(routes))
	}
}

func decode(t *testing.T, w *httptest.ResponseRecorder, into interface{}) {
	t.Helper()
	var envelope struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Status != "OK" {
		t.Fatalf("answer %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(envelope.Data, into); err != nil {
		t.Fatalf("data: %v in %s", err, envelope.Data)
	}
}

func TestAPIEvents(t *testing.T) {
	seedHistory(t)

	var page Page
	query := url.Values{"class": {"incident"}, "kind": {"auth.*,scan.probe"}, "severity": {"medium"}, "from": {at(9, 0).Format(time.RFC3339)}, "limit": {"3"}}
	decode(t, call(EventsRoute, "GET", "/cosmos/api/security/events?"+query.Encode(), adminReader), &page)
	if len(page.Events) != 3 || page.NextCursor == 0 || kindsOf(page.Events) != "scan.probe scan.probe auth.login.failed" {
		t.Fatalf("page = %s, next %d", kindsOf(page.Events), page.NextCursor)
	}
	first := page.Events[0]
	if first.Class != ClassIncident || first.Severity != "medium" || first.Count != 1 || first.Time.IsZero() || first.ID == 0 {
		t.Fatalf("event = %+v", first)
	}

	var next Page
	query.Set("cursor", strconv.FormatInt(page.NextCursor, 10))
	decode(t, call(EventsRoute, "GET", "/cosmos/api/security/events?"+query.Encode(), adminReader), &next)
	if len(next.Events) != 2 || next.NextCursor != 0 || next.Events[0].ID >= page.Events[2].ID {
		t.Fatalf("second page = %s, next %d", kindsOf(next.Events), next.NextCursor)
	}

	// an empty answer is an empty list, not null
	if body := call(EventsRoute, "GET", "/cosmos/api/security/events?actor=nobody", adminReader).Body.String(); !strings.Contains(body, `"events":[]`) {
		t.Fatalf("empty answer = %s", body)
	}

	for name, query := range map[string]string{
		"limit over the maximum": "limit=201",
		"negative limit":         "limit=-1",
		"limit not a number":     "limit=many",
		"cursor not a number":    "cursor=x",
		"bad time":               "from=yesterday",
		"bad class":              "class=all",
		"bad severity":           "severity=critical",
		"bad address":            "ip=203.0.113.7%0Akind%3Dx",
		"too many kinds":         "kind=" + strings.Repeat("auth.*,", 30),
		"a huge parameter":       "actor=" + strings.Repeat("a", 5000),
	} {
		w := call(EventsRoute, "GET", "/cosmos/api/security/events?"+query, adminReader)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400: %s", name, w.Code, w.Body.String())
		}
	}
}

func TestAPICountsTopIPsAndKinds(t *testing.T) {
	seedHistory(t)

	var counts countsAnswer
	decode(t, call(CountsRoute, "GET", "/cosmos/api/security/counts", adminReader), &counts)
	if len(counts.Kinds) != 8 || counts.Kinds[0].Total != 42 || len(counts.Buckets) != 0 {
		t.Fatalf("counts = %+v", counts)
	}

	query := url.Values{"from": {at(8, 0).Format(time.RFC3339)}, "to": {at(12, 0).Format(time.RFC3339)}, "bucket": {"hour"}}
	decode(t, call(CountsRoute, "GET", "/cosmos/api/security/counts?"+query.Encode(), adminReader), &counts)
	if len(counts.Buckets) != 4 || counts.Buckets[1].Incidents != 44 || counts.Buckets[0].Audit != 3 {
		t.Fatalf("buckets = %+v", counts.Buckets)
	}
	query = url.Values{"from": {at(-2, 0).Format(time.RFC3339)}, "to": {at(46, 0).Format(time.RFC3339)}, "bucket": {"day"}, "tz": {"120"}}
	decode(t, call(CountsRoute, "GET", "/cosmos/api/security/counts?"+query.Encode(), adminReader), &counts)
	if len(counts.Buckets) != 2 || !counts.Buckets[0].Start.Equal(at(-2, 0)) {
		t.Fatalf("local days = %+v", counts.Buckets)
	}
	for name, query := range map[string]string{
		"buckets without a range": "bucket=hour",
		"unknown bucket":          "bucket=second&from=2026-11-13T00:00:00Z&to=2026-11-13T01:00:00Z",
		"a year of hours":         "bucket=hour&from=2025-11-13T00:00:00Z&to=2026-11-13T00:00:00Z",
		"bad zone":                "bucket=day&tz=5000&from=2026-11-13T00:00:00Z&to=2026-11-14T00:00:00Z",
	} {
		if w := call(CountsRoute, "GET", "/cosmos/api/security/counts?"+query, adminReader); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, w.Code)
		}
	}

	var top []IPCount
	decode(t, call(TopIPsRoute, "GET", "/cosmos/api/security/top-ips?class=incident&limit=2", adminReader), &top)
	if len(top) != 2 || top[0].IP != "203.0.113.7" || top[0].Total != 43 {
		t.Fatalf("top = %+v", top)
	}
	if w := call(TopIPsRoute, "GET", "/cosmos/api/security/top-ips?limit=101", adminReader); w.Code != http.StatusBadRequest {
		t.Errorf("limit over the maximum: %d, want 400", w.Code)
	}

	var kinds []Kind
	decode(t, call(KindsRoute, "GET", "/cosmos/api/security/kinds", adminReader), &kinds)
	if len(kinds) != len(catalogue) {
		t.Fatalf("%d kinds listed, the catalogue has %d", len(kinds), len(catalogue))
	}
	for i, kind := range kinds {
		if kind.Label == "" || kind.Class == "" || kind.Severity == "" || (i > 0 && kinds[i-1].Kind >= kind.Kind) {
			t.Fatalf("kind %d = %+v", i, kind)
		}
	}
}

// What is stored comes back as data: JSON escapes it, nothing is turned into
// markup on the way.
func TestAPIReturnsTextAsText(t *testing.T) {
	openDatabase(t)
	seed(t, Event{Kind: utils.SecLoginFailed, IP: "203.0.113.7", Target: `<img src=x onerror=alert(1)>`, Details: map[string]string{"reason": `"quoted" & <b>bold</b>`}})

	w := call(EventsRoute, "GET", "/cosmos/api/security/events", adminReader)
	if strings.Contains(w.Body.String(), "<img") || strings.Contains(w.Body.String(), "<b>") {
		t.Fatalf("markup reached the answer unescaped: %s", w.Body.String())
	}
	var page Page
	decode(t, w, &page)
	if page.Events[0].Target != `<img src=x onerror=alert(1)>` || page.Events[0].Details["reason"] != `"quoted" & <b>bold</b>` {
		t.Fatalf("the text did not come back as it was stored: %+v", page.Events[0])
	}
}
