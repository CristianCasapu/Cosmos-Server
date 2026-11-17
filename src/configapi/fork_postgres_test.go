package configapi

// Fork addition: tests of fork_postgres.go (and utils/fork_postgres.go).

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const postgresTestSecret = "Pg-" + "test" + "-Secret-77"

func postgresTestCall(t *testing.T, body string) (int, map[string]interface{}, string) {
	t.Helper()
	postgresTestMu.Lock()
	postgresTestLast, postgresTestBusy = time.Time{}, false
	postgresTestMu.Unlock()
	req := httptest.NewRequest("POST", "http://cosmos.example.org/cosmos/api/config/postgres-test", strings.NewReader(body))
	req = asAdmin(req)
	w := httptest.NewRecorder()
	PostgresTestRoute(w, req)
	answer := map[string]interface{}{}
	json.Unmarshal(w.Body.Bytes(), &answer)
	return w.Code, answer, w.Body.String()
}

func withSavedDatabase(t *testing.T, db utils.DatabaseConfig) *[]utils.DatabaseConfig {
	t.Helper()
	old := utils.GetMainConfig()
	config := old
	config.Database = db
	utils.SetBaseMainConfig(config)
	tried := &[]utils.DatabaseConfig{}
	oldCheck := checkPostgres
	checkPostgres = func(ctx context.Context, d utils.DatabaseConfig) *utils.PostgresProblem {
		*tried = append(*tried, d)
		return nil
	}
	t.Cleanup(func() { checkPostgres = oldCheck; utils.SetBaseMainConfig(old) })
	return tried
}

func TestPostgresTestUsesTheSavedPasswordOnlyForTheSavedConnection(t *testing.T) {
	tried := withSavedDatabase(t, utils.DatabaseConfig{PostgresHost: "127.0.0.1:5432", PostgresDatabase: "cosmos", PostgresUsername: "cosmos", PostgresPassword: postgresTestSecret})
	code, answer, body := postgresTestCall(t, `{"host":"127.0.0.1:5432","database":"cosmos","username":"cosmos","password":"***"}`)
	if code != 200 || len(*tried) != 1 || (*tried)[0].PostgresPassword != postgresTestSecret {
		t.Fatalf("%d %s %+v", code, body, *tried)
	}
	if data := answer["data"].(map[string]interface{}); data["success"] != true {
		t.Errorf("%s", body)
	}
	if strings.Contains(body, postgresTestSecret) {
		t.Fatal("the password is in the answer")
	}
	// another server: the saved password is not sent there
	code, _, body = postgresTestCall(t, `{"host":"198.51.100.7:5432","database":"cosmos","username":"cosmos","password":"***"}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "only used for the saved server") || len(*tried) != 1 {
		t.Errorf("another server: %d %s", code, body)
	}
	code, _, body = postgresTestCall(t, `{"host":"127.0.0.1:5432","database":"cosmos","username":"other","password":"***"}`)
	if code != http.StatusBadRequest || len(*tried) != 1 {
		t.Errorf("another user: %d %s", code, body)
	}
	// an empty password for another connection is tried as empty
	postgresTestCall(t, `{"host":"198.51.100.7","database":"x","username":"y","password":""}`)
	if len(*tried) != 2 || (*tried)[1].PostgresPassword != "" {
		t.Errorf("empty: %+v", *tried)
	}
}

func TestPostgresTestRefusesBadShapesAndMethods(t *testing.T) {
	tried := withSavedDatabase(t, utils.DatabaseConfig{})
	for _, body := range []string{
		`{"host":"http://db:5432"}`,
		`{"host":"db:99999"}`,
		`{"host":"db/x"}`,
		`{"host":""}`,
		`{"host":"db","database":"a b"}`,
		`{"host":"db","unknown":1}`,
		`not json`,
	} {
		if code, _, answer := postgresTestCall(t, body); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", body, code, answer)
		}
	}
	if len(*tried) != 0 {
		t.Errorf("tried: %+v", *tried)
	}
	req := asAdmin(httptest.NewRequest("GET", "http://cosmos.example.org/x", nil))
	w := httptest.NewRecorder()
	PostgresTestRoute(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", w.Code)
	}
	big := `{"host":"db","password":"` + strings.Repeat("a", 9<<10) + `"}`
	if code, _, _ := postgresTestCall(t, big); code != http.StatusBadRequest {
		t.Errorf("a big body: %d", code)
	}
}

func TestPostgresTestIsSpacedOut(t *testing.T) {
	withSavedDatabase(t, utils.DatabaseConfig{})
	postgresTestCall(t, `{"host":"db"}`)
	req := asAdmin(httptest.NewRequest("POST", "http://cosmos.example.org/x", strings.NewReader(`{"host":"db"}`)))
	w := httptest.NewRecorder()
	PostgresTestRoute(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("a second test at once: %d", w.Code)
	}
}

func TestCheckPostgresSaysAClosedPortInWords(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	problem := utils.CheckPostgres(ctx, utils.DatabaseConfig{PostgresHost: address, PostgresUsername: "cosmos", PostgresPassword: postgresTestSecret})
	if problem == nil || problem.Reason != "closed" || !strings.Contains(problem.Message, "Nothing answers at "+address) || strings.Contains(problem.Message, postgresTestSecret) {
		t.Fatalf("%+v", problem)
	}
	problem = utils.CheckPostgres(ctx, utils.DatabaseConfig{PostgresHost: "no-such-host.invalid:5432", PostgresUsername: "cosmos"})
	if problem == nil || (problem.Reason != "dns" && problem.Reason != "network" && problem.Reason != "timeout") {
		t.Fatalf("%+v", problem)
	}
}

// A real PostgreSQL, only in a throwaway network: COSMOS_FORK_LIVE_POSTGRES=<host:port>,
// user postgres with the password in COSMOS_FORK_LIVE_POSTGRES_PASSWORD.
func TestCheckPostgresLive(t *testing.T) {
	host := os.Getenv("COSMOS_FORK_LIVE_POSTGRES")
	if host == "" {
		t.Skip("COSMOS_FORK_LIVE_POSTGRES not set")
	}
	password := os.Getenv("COSMOS_FORK_LIVE_POSTGRES_PASSWORD")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ok := utils.DatabaseConfig{PostgresHost: host, PostgresDatabase: "postgres", PostgresUsername: "postgres", PostgresPassword: password}
	if problem := utils.CheckPostgres(ctx, ok); problem != nil {
		t.Fatalf("right settings: %+v", problem)
	}
	for reason, db := range map[string]utils.DatabaseConfig{
		"password": {PostgresHost: host, PostgresDatabase: "postgres", PostgresUsername: "postgres", PostgresPassword: "wrong-" + password},
		"database": {PostgresHost: host, PostgresDatabase: "nosuchdb", PostgresUsername: "postgres", PostgresPassword: password},
	} {
		problem := utils.CheckPostgres(ctx, db)
		if problem == nil || problem.Reason != reason || strings.Contains(problem.Message, password) {
			t.Errorf("%s: %+v", reason, problem)
		}
	}
}

func TestPostgresSettingsCheckedOnSave(t *testing.T) {
	stored := utils.Config{Database: utils.DatabaseConfig{PostgresHost: "an odd value://", PostgresPassword: postgresTestSecret}}
	request := stored
	request.Database.PostgresPassword = "***"
	if p := checkPostgresSettings(&request, stored); p != nil {
		t.Fatalf("an unchanged odd value blocks the save: %v", p)
	}
	for _, good := range []utils.DatabaseConfig{
		{},
		{PostgresHost: "127.0.0.1:15432", PostgresDatabase: "cosmos", PostgresUsername: "cosmos"},
		{PostgresHost: "db.example.org"},
		{PostgresHost: "[2001:db8::1]:5432"},
		{PostgresHost: " notes-db:5432 ", PostgresUsername: "owner_name"},
	} {
		request := utils.Config{Database: good}
		if p := checkPostgresSettings(&request, stored); p != nil {
			t.Errorf("%+v refused: %v", good, p)
		}
	}
	for _, bad := range []utils.DatabaseConfig{
		{PostgresHost: "http://db:5432"},
		{PostgresHost: "db:0"},
		{PostgresHost: "db:5432/cosmos"},
		{PostgresHost: "db", PostgresDatabase: strings.Repeat("d", 64)},
		{PostgresHost: "db", PostgresUsername: "a;drop"},
	} {
		request := utils.Config{Database: bad}
		if p := checkPostgresSettings(&request, stored); p == nil || p.Code != "UC045" {
			t.Errorf("%+v accepted", bad)
		}
	}
}
