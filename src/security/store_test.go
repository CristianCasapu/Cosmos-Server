package security

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// seed stores events as they are, bypassing the limits.
func seed(t *testing.T, events ...Event) []Event {
	t.Helper()
	cleaned := make([]Event, 0, len(events))
	for _, event := range events {
		kind, known := catalogue[event.Kind]
		if !known {
			t.Fatalf("seed: unknown kind %q", event.Kind)
		}
		cleaned = append(cleaned, clean(event, kind, time.Now()))
	}
	if err := insert(cleaned); err != nil {
		t.Fatal("insert:", err)
	}
	return cleaned
}

func TestEveryKindIsInTheCatalogue(t *testing.T) {
	// the identifiers the rest of the server can record: the Sec… constants
	file, err := parser.ParseFile(token.NewFileSet(), "../utils/fork_security.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]string{}
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if !strings.HasPrefix(name.Name, "Sec") || i >= len(spec.Values) {
				continue
			}
			if literal, ok := spec.Values[i].(*ast.BasicLit); ok && literal.Kind == token.STRING {
				value, _ := strconv.Unquote(literal.Value)
				declared[value] = name.Name
			}
		}
		return true
	})
	if len(declared) < 40 {
		t.Fatalf("only %d kinds found in utils/fork_security.go: the test no longer reads it right", len(declared))
	}

	for kind, name := range declared {
		if _, known := catalogue[kind]; !known {
			t.Errorf("utils.%s = %q is not in the catalogue: recording it would be refused", name, kind)
		}
	}
	for kind, described := range catalogue {
		if _, ok := declared[kind]; !ok {
			t.Errorf("the catalogue has %q, which no constant of utils names", kind)
		}
		if described.Label == "" || described.Description == "" || !outcomes[described.Outcome] {
			t.Errorf("%q lacks a label, a description or a valid outcome: %+v", kind, described)
		}
		if described.Class != ClassIncident && described.Class != ClassAudit {
			t.Errorf("%q has the class %q", kind, described.Class)
		}
		if _, ok := ParseSeverity(described.Severity); !ok {
			t.Errorf("%q has the severity %q", kind, described.Severity)
		}
		for _, r := range kind {
			if !(r >= 'a' && r <= 'z') && r != '.' {
				t.Errorf("%q: a kind is lower-case letters and dots, the Fail2ban format relies on it", kind)
			}
		}
	}
}

// The incidents of severity "high" keep more rows and are what notifications
// will be sent for: none of them may be something a stranger can produce
// with a request. Adding one here is a decision, not a detail.
func TestOnlyTheseIncidentsAreHigh(t *testing.T) {
	want := map[string]bool{
		utils.SecMFALocked:         true,
		utils.SecAccessGrantDenied: true,
		utils.SecShieldBanTemp:     true,
		utils.SecShieldBanPerm:     true,
	}
	for kind, described := range catalogue {
		if described.Class == ClassIncident && described.severity == SeverityHigh && !want[kind] {
			t.Errorf("%q is an incident of severity high: can someone who is not logged in produce it at will?", kind)
		}
		if want[kind] && described.severity != SeverityHigh {
			t.Errorf("%q is no longer high", kind)
		}
	}
}

func TestPruneDropsOldRowsAndKeepsEachKindWithinItsCap(t *testing.T) {
	openDatabase(t)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

	// old and recent rows of two kinds
	seed(t,
		Event{Kind: utils.SecLoginSuccess, Actor: "alice", Time: now.Add(-retention - time.Hour)},
		Event{Kind: utils.SecLoginFailed, IP: "203.0.113.7", Time: now.Add(-retention - time.Minute)},
		Event{Kind: utils.SecLoginSuccess, Actor: "alice", Time: now.Add(-retention + time.Hour)},
		Event{Kind: utils.SecLoginFailed, IP: "203.0.113.7", Time: now.Add(-time.Hour)},
	)
	// a flood of one kind, over its cap
	flood := make([]Event, 0, capFloodable+25)
	for i := 0; i < capFloodable+25; i++ {
		flood = append(flood, Event{Kind: utils.SecScanProbe, IP: "198.51.100.1", Target: fmt.Sprintf("probe-%d", i), Time: now.Add(-time.Minute)})
	}
	seed(t, flood...)

	deleted, err := prune(now)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2+25 {
		t.Fatalf("%d rows deleted, want the 2 old ones and the 25 over the cap", deleted)
	}

	counts, err := CountByKind(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]int64{}
	for _, count := range counts {
		rows[count.Kind] = count.Rows
	}
	if rows[utils.SecScanProbe] != capFloodable || rows[utils.SecLoginSuccess] != 1 || rows[utils.SecLoginFailed] != 1 {
		t.Fatalf("rows left = %v: the flood must be cut to its cap and must not cost the other kinds a row", rows)
	}

	// the oldest of the flood went, the newest stayed
	page, err := List(Filter{Kinds: []string{utils.SecScanProbe}}, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Events[0].Target != fmt.Sprintf("probe-%d", capFloodable+24) {
		t.Fatalf("newest probe left = %q", page.Events[0].Target)
	}

	// pruning again changes nothing
	if again, err := prune(now); err != nil || again != 0 {
		t.Fatalf("a second pruning deleted %d rows (%v)", again, err)
	}
}

func TestCapsFavourWhatCannotBeFlooded(t *testing.T) {
	for kind, described := range catalogue {
		floodable := described.Class == ClassIncident && described.severity < SeverityHigh
		if floodable && capOf(kind) != capFloodable || !floodable && capOf(kind) != capOtherKinds {
			t.Errorf("%q (%s, %s) has the cap %d", kind, described.Class, described.Severity, capOf(kind))
		}
	}
	if capOf("gone.kind") != capFloodable {
		t.Error("a kind that left the catalogue keeps the larger cap")
	}
}

func TestTheTableIsTheForksOwn(t *testing.T) {
	openDatabase(t)
	seed(t, Event{Kind: utils.SecLoginSuccess, Actor: "alice"})

	db, err := utils.MonitoringReadDB()
	if err != nil {
		t.Fatal(err)
	}
	var version string
	if err := db.QueryRow("SELECT value FROM monitoring_kv WHERE key = ?", schemaVersionKey).Scan(&version); err != nil || version != strconv.Itoa(schemaVersion) {
		t.Fatalf("schema version = %q (%v)", version, err)
	}
	// upstream's own version key is left alone
	var upstream string
	if err := db.QueryRow("SELECT value FROM monitoring_kv WHERE key = 'schema_version'").Scan(&upstream); err != nil || upstream == "" {
		t.Fatalf("upstream's schema_version = %q (%v)", upstream, err)
	}
	// "reset metrics" deletes upstream's events, not the record
	if err := utils.DeleteLocalEvents(); err != nil {
		t.Fatal(err)
	}
	if events := all(t); len(events) != 1 {
		t.Fatalf("%d events after upstream's events were deleted, want 1", len(events))
	}
	// and the daily retention of upstream leaves it alone too
	utils.RunDatabaseRetention()
	if events := all(t); len(events) != 1 {
		t.Fatalf("%d events after upstream's retention ran, want 1", len(events))
	}
}

func TestQueriesAnswerWithoutADatabaseWithAnError(t *testing.T) {
	useFolder(t)
	utils.CloseMetricsDatabase()
	if _, err := List(Filter{}, 0, 0); !errors.Is(err, utils.ErrDatabaseClosed) {
		t.Fatalf("List without a database: %v", err)
	}
}
