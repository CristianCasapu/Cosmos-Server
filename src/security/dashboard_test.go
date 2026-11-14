package security

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// ---- groups ----

func TestGroupsHoldEveryIncidentKindOnceAndNoAuditEntry(t *testing.T) {
	seen := map[string]string{}
	for _, group := range Groups() {
		if group.ID == "" || len(group.Kinds) == 0 || len(group.Kinds) > maxFilterKinds {
			t.Fatalf("group %+v", group)
		}
		for _, kind := range group.Kinds {
			known, ok := catalogue[kind]
			if !ok {
				t.Errorf("group %s: %s is not in the catalogue", group.ID, kind)
				continue
			}
			if known.Class != ClassIncident {
				t.Errorf("group %s: %s is an audit entry", group.ID, kind)
			}
			if other, twice := seen[kind]; twice {
				t.Errorf("%s is in %s and %s", kind, other, group.ID)
			}
			seen[kind] = group.ID
		}
	}
	for kind, known := range catalogue {
		if known.Class == ClassIncident && seen[kind] == "" {
			t.Errorf("incident %s is in no group", kind)
		}
	}
	if GroupOf(utils.SecScanProbe) != "scan" || GroupOf("no.such.kind") != GroupOther {
		t.Fatal("GroupOf")
	}
}

// ---- overview ----

// seedDashboard is seedHistory plus an earlier failed login (the day
// before), a scan from the local network and a stale session (harmless).
func seedDashboard(t *testing.T) *[]string {
	t.Helper()
	seedHistory(t)
	seed(t,
		Event{Time: at(-20, 0), Kind: utils.SecLoginFailed, IP: "198.51.100.99", Target: "root"},
		Event{Time: at(10, 30), Kind: utils.SecShieldHostname, IP: "192.168.1.20", Host: "192.168.1.5"},
		Event{Time: at(11, 30), Kind: utils.SecSessionRejected, IP: "192.0.2.50", Details: map[string]string{"reason": "expired"}},
	)
	previousNow, previousCountry := overviewNow, countryOf
	looked := []string{}
	overviewNow = func() time.Time { return at(12, 0) }
	countryOf = func(ip string) string { looked = append(looked, ip); return "ZZ" }
	t.Cleanup(func() { overviewNow, countryOf = previousNow, previousCountry })
	return &looked
}

func TestOverviewOfADay(t *testing.T) {
	looked := seedDashboard(t)

	var overview Overview
	decode(t, call(OverviewRoute, "GET", "/cosmos/api/security/overview", adminReader), &overview)

	if overview.Period != "24h" || !overview.Now.Equal(at(12, 0)) || overview.BucketSeconds != 3600 || len(overview.Groups) != len(groups) {
		t.Fatalf("overview = %+v", overview)
	}
	current := overview.Current
	if !current.From.Equal(at(-12, 0)) || !current.To.Equal(at(12, 0)) {
		t.Fatalf("period %v to %v", current.From, current.To)
	}
	want := map[string]GroupStat{
		"login": {42, 1}, "lockout": {0, 0}, "ban": {1, 1}, "scan": {3, 2}, "access": {1, 1}, GroupOther: {1, 1},
	}
	for id, stat := range want {
		if current.Groups[id] != stat {
			t.Errorf("group %s = %+v, want %+v", id, current.Groups[id], stat)
		}
	}
	// the stale session is not counted as hostile
	if current.Total != 47 || current.Addresses != 4 {
		t.Errorf("hostile = %d from %d, want 47 from 4", current.Total, current.Addresses)
	}
	if current.Kinds[utils.SecLoginFailed] != 42 || current.Kinds[utils.SecSessionRejected] != 1 || current.Kinds[utils.SecLoginSuccess] != 0 {
		t.Errorf("kinds = %+v", current.Kinds)
	}

	// the 24 hours before: the one failed login of the day before
	previous := overview.Previous
	if !previous.From.Equal(at(-36, 0)) || previous.Groups["login"].Total != 1 || previous.Total != 1 {
		t.Errorf("previous = %+v", previous)
	}
	// the last hour: the refused permission and the stale session
	if overview.Recent.Total != 1 || overview.Recent.Groups["access"].Total != 1 || overview.Recent.Groups[GroupOther].Total != 1 {
		t.Errorf("recent = %+v", overview.Recent)
	}
	if overview.Day.Total != current.Total {
		t.Errorf("day = %+v", overview.Day)
	}

	// one bar an hour, every group in every bar
	if len(overview.Buckets) != 24 || !overview.Buckets[0].Start.Equal(at(-12, 0)) {
		t.Fatalf("%d buckets from %v", len(overview.Buckets), overview.Buckets[0].Start)
	}
	nine := overview.Buckets[21]
	if !nine.Start.Equal(at(9, 0)) || nine.Groups["login"] != 42 || nine.Groups["scan"] != 2 || len(nine.Groups) != len(groups) {
		t.Errorf("bar of 9:00 = %+v", nine)
	}
	sum := int64(0)
	for _, bucket := range overview.Buckets {
		for _, n := range bucket.Groups {
			sum += n
		}
	}
	if sum != 48 {
		t.Errorf("the bars add up to %d, want 48", sum)
	}

	// the busiest address first, with what it did
	top := overview.Top
	if len(top) != 5 || top[0].IP != "203.0.113.7" || top[0].Total != 43 || top[0].Local || top[0].Country != "ZZ" {
		t.Fatalf("top = %+v", top)
	}
	if len(top[0].Kinds) != 2 || top[0].Kinds[0] != (KindTotal{utils.SecLoginFailed, 42}) || top[0].Kinds[1] != (KindTotal{utils.SecShieldBanTemp, 1}) {
		t.Errorf("what it did = %+v", top[0].Kinds)
	}
	for _, address := range top {
		if address.IP == "192.168.1.20" {
			if !address.Local || address.Country != "" {
				t.Errorf("local address = %+v", address)
			}
		}
	}
	for _, ip := range *looked {
		if ip == "192.168.1.20" {
			t.Error("the country of a local address was looked up")
		}
	}
}

func TestOverviewPeriodsAndZones(t *testing.T) {
	seedDashboard(t)

	var week Overview
	decode(t, call(OverviewRoute, "GET", "/cosmos/api/security/overview?period=7d&tz=120", adminReader), &week)
	if week.BucketSeconds != 6*3600 || len(week.Buckets) < 28 || len(week.Buckets) > 29 {
		t.Fatalf("week: %d bars of %d s", len(week.Buckets), week.BucketSeconds)
	}
	for _, bucket := range week.Buckets {
		if local := bucket.Start.Add(2 * time.Hour); local.Hour()%6 != 0 || local.Minute() != 0 {
			t.Fatalf("a bar starts at %v local", local)
		}
	}
	if week.Previous.Total != 0 || week.Current.Groups["login"].Total != 43 {
		t.Errorf("week = %+v / %+v", week.Current.Groups, week.Previous)
	}

	var month Overview
	decode(t, call(OverviewRoute, "GET", "/cosmos/api/security/overview?period=30d&tz=-300", adminReader), &month)
	if month.BucketSeconds != 86400 || len(month.Buckets) != 31 {
		t.Fatalf("month: %d bars", len(month.Buckets))
	}
	if local := month.Buckets[0].Start.Add(-5 * time.Hour); local.Hour() != 0 {
		t.Fatalf("days start at %v local", local)
	}

	for name, query := range map[string]string{
		"unknown period":    "period=1y",
		"long period":       "period=" + strings.Repeat("7", 600),
		"zone too far":      "tz=5000",
		"zone not a number": "tz=east",
	} {
		if w := call(OverviewRoute, "GET", "/cosmos/api/security/overview?"+query, adminReader); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, w.Code)
		}
	}
}

func TestOverviewOfAnEmptyRecord(t *testing.T) {
	openDatabase(t)
	var overview Overview
	w := call(OverviewRoute, "GET", "/cosmos/api/security/overview", adminReader)
	decode(t, w, &overview)
	if overview.Current.Total != 0 || len(overview.Top) != 0 || len(overview.Buckets) != 25 {
		t.Fatalf("empty overview = %+v", overview)
	}
	// lists are lists, not null
	if body := w.Body.String(); !strings.Contains(body, `"top":[]`) || !strings.Contains(body, `"kinds":{}`) {
		t.Fatalf("empty answer = %s", body)
	}
}

// ---- incidents ----

func incidents(t *testing.T, query url.Values) Page {
	t.Helper()
	var page Page
	decode(t, call(IncidentsRoute, "GET", "/cosmos/api/security/incidents?"+query.Encode(), adminReader), &page)
	for _, event := range page.Events {
		if event.Class != ClassIncident {
			t.Fatalf("an audit entry in the incident list: %+v", event)
		}
	}
	return page
}

func TestIncidentsByGroupAndText(t *testing.T) {
	seedDashboard(t)

	if page := incidents(t, url.Values{}); len(page.Events) != 10 {
		t.Fatalf("all incidents: %s", kindsOf(page.Events))
	}
	if page := incidents(t, url.Values{"group": {"login"}}); len(page.Events) != 4 {
		t.Fatalf("failed sign-ins: %s", kindsOf(page.Events))
	}
	if page := incidents(t, url.Values{"group": {"scan"}, "ip": {"198.51.100.20"}}); len(page.Events) != 2 {
		t.Fatalf("scans of one address: %s", kindsOf(page.Events))
	}
	if page := incidents(t, url.Values{"q": {"WP-LOGIN"}}); len(page.Events) != 1 || page.Events[0].Path != "/wp-login.php" {
		t.Fatalf("text in the path, any case: %+v", page.Events)
	}
	// alice's own actions are audit entries; only the login tried as alice is an incident
	if page := incidents(t, url.Values{"q": {"alice"}}); len(page.Events) != 1 || page.Events[0].Target != "alice" {
		t.Fatalf("text in the target: %+v", page.Events)
	}
	if page := incidents(t, url.Values{"q": {"expired"}}); len(page.Events) != 1 || page.Events[0].Kind != utils.SecSessionRejected {
		t.Fatalf("text in the details: %+v", page.Events)
	}
	// LIKE's wildcards are taken literally
	if page := incidents(t, url.Values{"q": {"%"}}); len(page.Events) != 0 {
		t.Fatalf("%% matched %s", kindsOf(page.Events))
	}
	if page := incidents(t, url.Values{"q": {"_"}}); len(page.Events) != 2 {
		t.Fatalf("_ matched %s (want the two reasons with an underscore)", kindsOf(page.Events))
	}
	if page := incidents(t, url.Values{"severity": {"high"}}); len(page.Events) != 1 || page.Events[0].Kind != utils.SecShieldBanTemp {
		t.Fatalf("high: %s", kindsOf(page.Events))
	}

	// pages
	first := incidents(t, url.Values{"group": {"login"}, "limit": {"3"}})
	if len(first.Events) != 3 || first.NextCursor == 0 {
		t.Fatalf("first page = %s, next %d", kindsOf(first.Events), first.NextCursor)
	}
	second := incidents(t, url.Values{"group": {"login"}, "limit": {"3"}, "cursor": {strconv.FormatInt(first.NextCursor, 10)}})
	if len(second.Events) != 1 || second.NextCursor != 0 || second.Events[0].Target != "admin" {
		t.Fatalf("second page = %+v", second)
	}

	for name, query := range map[string]string{
		"audit entries":          "class=audit",
		"unknown group":          "group=everything",
		"group and kinds":        "group=login&kind=scan.probe",
		"text too long":          "q=" + strings.Repeat("a", MaxSearchText+1),
		"limit over the maximum": "limit=201",
		"bad cursor":             "cursor=-4",
		"bad address":            "ip=somewhere",
	} {
		if w := call(IncidentsRoute, "GET", "/cosmos/api/security/incidents?"+query, adminReader); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400: %s", name, w.Code, w.Body.String())
		}
	}
}

// ---- permissions ----

func TestDashboardRoutesAreForAdminReadersOnlyAndReadOnly(t *testing.T) {
	seedDashboard(t)
	for path, handler := range map[string]http.HandlerFunc{
		"/cosmos/api/security/overview":  OverviewRoute,
		"/cosmos/api/security/incidents": IncidentsRoute,
	} {
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
}

func TestLikePattern(t *testing.T) {
	for in, want := range map[string]string{
		"Admin":   "%admin%",
		"100%":    `%100\%%`,
		"a_b":     `%a\_b%`,
		`c:\temp`: `%c:\\temp%`,
	} {
		if got := likePattern(in); got != want {
			t.Errorf("likePattern(%q) = %q, want %q", in, got, want)
		}
	}
}
