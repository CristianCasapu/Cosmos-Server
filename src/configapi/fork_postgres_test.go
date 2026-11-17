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
	settingsTestServer(t) // a configuration folder of the test's own
	old := utils.GetBaseMainConfig()
	config := old
	config.Database = db
	utils.LoadBaseMainConfig(config) // in memory only: nothing is written
	tried := &[]utils.DatabaseConfig{}
	oldCheck := checkPostgres
	checkPostgres = func(ctx context.Context, d utils.DatabaseConfig) *utils.PostgresProblem {
		*tried = append(*tried, d)
		return nil
	}
	t.Cleanup(func() { checkPostgres = oldCheck; utils.LoadBaseMainConfig(old) })
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
	stubPostgresStore(t, nil)
	stored := utils.Config{Database: utils.DatabaseConfig{PostgresHost: "an odd value://", PostgresPassword: postgresTestSecret}}
	request := stored
	request.Database.PostgresPassword = "***"
	if p := checkPostgresSettings(nil, &request, stored); p != nil {
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
		if p := checkPostgresSettings(nil, &request, stored); p != nil {
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
		if p := checkPostgresSettings(nil, &request, stored); p == nil || p.Code != "UC045" {
			t.Errorf("%+v accepted", bad)
		}
	}
}

// A caller who may change the settings but not read the password cannot send
// the saved password to another database server (review, finding 2).
func TestTheMaskedPostgresPasswordStaysWithItsServer(t *testing.T) {
	restarted := settingsTestServer(t)
	saved := utils.DatabaseConfig{PostgresHost: "127.0.0.1:5432", PostgresDatabase: "cosmos", PostgresUsername: "cosmos", PostgresPassword: postgresTestSecret}
	save := func(as func(*http.Request) *http.Request, config utils.Config) *httptest.ResponseRecorder {
		body, _ := json.Marshal(config)
		w := httptest.NewRecorder()
		ConfigApiSet(w, as(httptest.NewRequest("PUT", "https://cosmos.example.com/cosmos/api/config", strings.NewReader(string(body)))))
		if w.Code == http.StatusOK {
			<-restarted
		}
		return w
	}
	reset := func() {
		config := hostPowersConfig()
		config.Database = saved
		utils.SaveConfigTofile(config)
		utils.LoadBaseMainConfig(config)
	}

	for _, change := range []func(*utils.DatabaseConfig){
		func(d *utils.DatabaseConfig) { d.PostgresHost = "198.51.100.7:5432" },
		func(d *utils.DatabaseConfig) { d.PostgresHost = "127.0.0.1:6432" },
		func(d *utils.DatabaseConfig) { d.PostgresDatabase = "other" },
		func(d *utils.DatabaseConfig) { d.PostgresUsername = "other" },
	} {
		reset()
		settings := readSettingsAs(t, asConfigurationToken)
		if settings.Database.PostgresPassword != "***" {
			t.Fatalf("the token reads the password: %q", settings.Database.PostgresPassword)
		}
		change(&settings.Database)
		w := save(asConfigurationToken, settings)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "UC046") || !strings.Contains(w.Body.String(), "type the password again") {
			t.Errorf("%+v with the masked password: %d %s", settings.Database, w.Code, w.Body.String())
		}
		if stored := utils.ReadConfigFromFile().Database; stored != saved {
			t.Errorf("stored %+v", stored)
		}
	}

	// the same server with the mask: the saved password stays
	reset()
	settings := readSettingsAs(t, asConfigurationToken)
	settings.ServerCountry = "RO"
	if w := save(asConfigurationToken, settings); w.Code != http.StatusOK {
		t.Fatalf("an unchanged database with the mask: %d %s", w.Code, w.Body.String())
	}
	if stored := utils.ReadConfigFromFile().Database; stored.PostgresPassword != postgresTestSecret {
		t.Error("the saved password was lost")
	}

	// another server with a password typed for it: not refused for the
	// password; (plan 51) a closed local port is refused by the connection
	// check, and saved with "Save anyway" by an administrator only
	reset()
	settings = readSettingsAs(t, asConfigurationToken)
	settings.Database.PostgresHost = "127.0.0.1:1"
	settings.Database.PostgresPassword = "typed-for-the-new-server"
	if w := save(asConfigurationToken, settings); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "UC047") ||
		!strings.Contains(w.Body.String(), "Nothing answers at 127.0.0.1:1") || strings.Contains(w.Body.String(), "typed-for-the-new-server") {
		t.Fatalf("a closed port: %d %s", w.Code, w.Body.String())
	}
	if stored := utils.ReadConfigFromFile().Database; stored != saved {
		t.Errorf("refused, but stored %+v", stored)
	}
	unchecked := func(as func(*http.Request) *http.Request) func(*http.Request) *http.Request {
		return func(req *http.Request) *http.Request {
			req.URL.RawQuery = "database=unchecked"
			return as(req)
		}
	}
	if w := save(unchecked(asConfigurationToken), settings); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "UC048") {
		t.Fatalf("Save anyway by a token without the admin permission: %d %s", w.Code, w.Body.String())
	}
	settings.Database.PostgresPassword = "typed-for-the-new-server"
	if w := save(unchecked(asAdmin), settings); w.Code != http.StatusOK {
		t.Fatalf("Save anyway by an administrator: %d %s", w.Code, w.Body.String())
	}
	if stored := utils.ReadConfigFromFile().Database; stored.PostgresPassword != "typed-for-the-new-server" || stored.PostgresHost != "127.0.0.1:1" {
		t.Errorf("stored %+v", stored)
	}
}

func stubPostgresStore(t *testing.T, problem *utils.PostgresProblem) *[]utils.DatabaseConfig {
	t.Helper()
	tried := &[]utils.DatabaseConfig{}
	old, oldAdvice := checkPostgresStore, postgresAdvice
	checkPostgresStore = func(ctx context.Context, d utils.DatabaseConfig) *utils.PostgresProblem {
		*tried = append(*tried, d)
		return problem
	}
	postgresAdvice = func(ctx context.Context, typed string, reason string) string {
		return "On this server PostgreSQL's port is published on 192.168.1.10 only, not on localhost: use 192.168.1.10:5432."
	}
	t.Cleanup(func() { checkPostgresStore, postgresAdvice = old, oldAdvice })
	return tried
}

// Plan 51: a save that changes the settings connects first; one that does
// not never connects; the refusal says what failed and what to try.
func TestPostgresSaveConnectsOnlyWhenTheSettingsChange(t *testing.T) {
	refused := &utils.PostgresProblem{Reason: "closed", Message: "Nothing answers at localhost:5432: check the address and the port, and that the database runs."}
	tried := stubPostgresStore(t, refused)
	stored := utils.Config{Database: utils.DatabaseConfig{PostgresHost: "192.168.1.10:5432", PostgresDatabase: "cosmos", PostgresUsername: "cosmos", PostgresPassword: postgresTestSecret}}

	// unchanged (the mask, or the password put back): nothing is tried, even with the database down
	for _, password := range []string{"***", postgresTestSecret} {
		request := stored
		request.Database.PostgresPassword = password
		request.ServerCountry = "RO"
		if p := checkPostgresSettings(nil, &request, stored); p != nil {
			t.Errorf("unchanged settings refused: %v", p)
		}
	}
	// back to the local file: nothing to connect to
	if p := checkPostgresSettings(nil, &utils.Config{}, stored); p != nil {
		t.Errorf("back to the local file: %v", p)
	}
	if len(*tried) != 0 {
		t.Fatalf("connected for an unchanged save: %+v", *tried)
	}

	request := stored
	request.Database.PostgresHost = "localhost:5432"
	p := checkPostgresSettings(nil, &request, stored)
	if p == nil || p.Code != "UC047" || !strings.Contains(p.Message, "Nothing answers at localhost:5432") ||
		!strings.Contains(p.Message, "use 192.168.1.10:5432") || !strings.Contains(p.Message, "Nothing was saved") {
		t.Fatalf("refusal: %+v", p)
	}
	if len(*tried) != 1 || (*tried)[0].PostgresPassword != postgresTestSecret || (*tried)[0].PostgresHost != "localhost:5432" {
		t.Errorf("tried %+v", *tried)
	}
	// a password in a remote's sentence is never repeated
	*refused = utils.PostgresProblem{Reason: "refused", Message: "odd " + postgresTestSecret}
	if p := checkPostgresSettings(nil, &request, stored); p == nil || strings.Contains(p.Message, postgresTestSecret) {
		t.Errorf("the password came out: %+v", p)
	}
	// the database answers: saved
	*refused = utils.PostgresProblem{}
	checkPostgresStore = func(context.Context, utils.DatabaseConfig) *utils.PostgresProblem { return nil }
	if p := checkPostgresSettings(nil, &request, stored); p != nil {
		t.Errorf("working settings refused: %v", p)
	}
}

// One save check at a time: a second one is refused, not queued.
func TestPostgresSaveCheckOneAtATime(t *testing.T) {
	stubPostgresStore(t, nil)
	release, entered := make(chan struct{}), make(chan struct{})
	checkPostgresStore = func(context.Context, utils.DatabaseConfig) *utils.PostgresProblem {
		close(entered)
		<-release
		return nil
	}
	stored := utils.Config{}
	done := make(chan *httpSettingsProblem)
	go func() {
		request := utils.Config{Database: utils.DatabaseConfig{PostgresHost: "192.168.1.10:5432", PostgresUsername: "cosmos"}}
		done <- checkPostgresSettings(nil, &request, stored)
	}()
	<-entered
	request := utils.Config{Database: utils.DatabaseConfig{PostgresHost: "192.168.1.11:5432", PostgresUsername: "cosmos"}}
	if p := checkPostgresSettings(nil, &request, stored); p == nil || p.Code != "UC049" {
		t.Errorf("a second check at once: %+v", p)
	}
	close(release)
	if p := <-done; p != nil {
		t.Errorf("first: %+v", p)
	}
}

// The test answers the advice next to the reason.
func TestPostgresTestAnswersTheAdvice(t *testing.T) {
	withSavedDatabase(t, utils.DatabaseConfig{})
	stubPostgresStore(t, nil)
	checkPostgres = func(context.Context, utils.DatabaseConfig) *utils.PostgresProblem {
		return &utils.PostgresProblem{Reason: "closed", Message: "Nothing answers at localhost:5432."}
	}
	_, answer, body := postgresTestCall(t, `{"host":"localhost:5432","database":"cosmos","username":"cosmos","password":"x-typed"}`)
	data, _ := answer["data"].(map[string]interface{})
	advice, _ := data["advice"].(string)
	if data["success"] != false || !strings.Contains(advice, "192.168.1.10:5432") {
		t.Errorf("%s", body)
	}
}

// The connection test is for administrators only (review, M1).
func TestPostgresTestIsForAdministratorsOnly(t *testing.T) {
	tried := withSavedDatabase(t, utils.DatabaseConfig{PostgresHost: "127.0.0.1:5432", PostgresUsername: "cosmos", PostgresPassword: postgresTestSecret})
	for name, as := range map[string]func(*http.Request) *http.Request{
		"a token with the configuration permission": asConfigurationToken,
		"a plain user": asPlainUser,
		"an administrator without the password confirmation": asAdminWithoutPassword,
	} {
		postgresTestMu.Lock()
		postgresTestLast, postgresTestBusy = time.Time{}, false
		postgresTestMu.Unlock()
		req := as(httptest.NewRequest("POST", "http://cosmos.example.org/cosmos/api/config/postgres-test", strings.NewReader(`{"host":"127.0.0.1:5432","username":"cosmos","password":"***"}`)))
		w := httptest.NewRecorder()
		PostgresTestRoute(w, req)
		if w.Code == http.StatusOK || strings.Contains(w.Body.String(), postgresTestSecret) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if len(*tried) != 0 {
		t.Errorf("a connection was tried: %+v", *tried)
	}
}
