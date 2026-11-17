package monitorcopy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func request(t *testing.T, handler http.HandlerFunc, method, body string, auth *utils.AuthContext) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://cosmos.example/cosmos/api/monitoring-copy", strings.NewReader(body))
	if auth != nil {
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
	}
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

var (
	reader = &utils.AuthContext{Nickname: "reader", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN_READ}}
	admin  = &utils.AuthContext{Nickname: "admin", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN_READ, utils.PERM_ADMIN}, IsSudoed: true}
)

// captureSecurity collects what the routes record.
func captureSecurity(t *testing.T) func() []utils.SecurityEvent {
	var mu sync.Mutex
	events := []utils.SecurityEvent{}
	utils.SetSecurityRecorder(func(e utils.SecurityEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}, func() {})
	t.Cleanup(func() { utils.SetSecurityRecorder(func(utils.SecurityEvent) {}, func() {}) })
	return func() []utils.SecurityEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]utils.SecurityEvent(nil), events...)
	}
}

func useLocalStore(t *testing.T) string {
	t.Helper()
	folder := t.TempDir() + string(os.PathSeparator)
	previous := utils.CONFIGFOLDER
	utils.CONFIGFOLDER = folder
	previousConfig := utils.GetMainConfig()
	config := previousConfig
	config.Database = utils.DatabaseConfig{}
	utils.LoadBaseMainConfig(config)
	if err := utils.InitMetricsDatabase(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		wait()
		utils.CloseMetricsDatabase()
		utils.LoadBaseMainConfig(previousConfig)
		utils.CONFIGFOLDER = previous
		state.mu.Lock()
		state.last = nil
		state.mu.Unlock()
		forgetSummaries()
	})
	return folder
}

func TestRoutesPermissionsAndMethods(t *testing.T) {
	useLocalStore(t)
	for _, route := range []struct {
		handler http.HandlerFunc
		method  string
	}{{StatusRoute, "GET"}, {StartRoute, "POST"}, {CancelRoute, "POST"}} {
		if w := request(t, route.handler, route.method, `{"direction":"to-postgres"}`, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("%s without a session: %d", route.method, w.Code)
		}
	}
	// a reader of the admin pages sees the status, starts nothing
	if w := request(t, StatusRoute, "GET", "", reader); w.Code != http.StatusOK {
		t.Fatalf("GET as a reader: %d %s", w.Code, w.Body.String())
	}
	for _, handler := range []http.HandlerFunc{StartRoute, CancelRoute} {
		if w := request(t, handler, "POST", `{"direction":"to-postgres"}`, reader); w.Code != http.StatusUnauthorized {
			t.Errorf("POST as a reader: %d", w.Code)
		}
	}
	// an admin who did not confirm the password starts nothing
	unsudoed := *admin
	unsudoed.IsSudoed = false
	if w := request(t, StartRoute, "POST", `{"direction":"to-postgres"}`, &unsudoed); w.Code != http.StatusUnauthorized {
		t.Errorf("POST without sudo: %d", w.Code)
	}
	// the methods
	if w := request(t, StatusRoute, "POST", "", admin); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST the status: %d", w.Code)
	}
	for _, handler := range []http.HandlerFunc{StartRoute, CancelRoute} {
		if w := request(t, handler, "GET", "", admin); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET a command: %d", w.Code)
		}
	}
	// the input
	for _, body := range []string{"", "{}", `{"direction":"sideways"}`, `{"direction":"to-local","more":1}`, `{"direction":"` + strings.Repeat("x", 2000) + `"}`} {
		if w := request(t, StartRoute, "POST", body, admin); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "MC001") {
			t.Errorf("start with %.40q: %d %s", body, w.Code, w.Body.String())
		}
	}
	// the local file in use: nothing to copy between the stores
	w := request(t, StartRoute, "POST", `{"direction":"to-postgres"}`, admin)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "MC002") {
		t.Errorf("start with the local file in use: %d %s", w.Code, w.Body.String())
	}
	if w := request(t, CancelRoute, "POST", "", admin); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "MC004") {
		t.Errorf("cancel with nothing running: %d %s", w.Code, w.Body.String())
	}
	var answer struct{ Data View }
	json.Unmarshal(request(t, StatusRoute, "GET", "", admin).Body.Bytes(), &answer)
	if answer.Data.InUse != "local" || len(answer.Data.Offers) != 2 || answer.Data.Offers[0].Available || answer.Data.Offers[0].Problem != "not-postgres" {
		t.Errorf("status with the local file in use: %+v", answer.Data)
	}
}

// usePostgres makes a PostgreSQL database of its own the store in use, with
// a local file that holds a history.
func usePostgres(t *testing.T) (utils.Config, utils.Config) {
	t.Helper()
	dsn, name := newPGDatabase(t)
	folder := useLocalStore(t)
	utils.CloseMetricsDatabase()
	local, err := openLocalAt(folder+"database.db", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := utils.PrepareMonitoringSchema(local.DB, local.Dialect); err != nil {
		t.Fatal(err)
	}
	fill(t, local, contents{node: "server.local", metrics: 4, values: 50, events: 300, notifications: 20, security: 40, end: time.Now().UnixMilli() - 7200000})
	local.DB.Close()

	u, _ := url.Parse(dsn)
	password, _ := u.User.Password()
	localConfig := utils.GetMainConfig()
	pgConfig := localConfig
	pgConfig.Database = utils.DatabaseConfig{PostgresHost: u.Host, PostgresDatabase: name, PostgresUsername: u.User.Username(), PostgresPassword: password}
	utils.LoadBaseMainConfig(pgConfig)
	if err := utils.InitMetricsDatabase(); err != nil {
		t.Fatal(err)
	}
	if utils.MonitoringDialect() != utils.DialectPostgres {
		t.Fatal("PostgreSQL is not in use")
	}
	return pgConfig, localConfig
}

func TestStartStatusCancelAndStoreChange(t *testing.T) {
	pgConfig, localConfig := usePostgres(t)
	recorded := captureSecurity(t)
	previousBatch, previousPause := batchSize, pauseFor
	t.Cleanup(func() { batchSize, pauseFor = previousBatch, previousPause })
	goroutines := runtime.NumGoroutine()

	var view struct{ Data View }
	json.Unmarshal(request(t, StatusRoute, "GET", "", admin).Body.Bytes(), &view)
	toPostgres, toLocal := view.Data.Offers[0], view.Data.Offers[1]
	if view.Data.InUse != "postgres" || !toPostgres.Available || toPostgres.Source == nil || toPostgres.Source.Records != 4+4*50+300+20+40 ||
		toPostgres.Source.Bytes <= 0 || toPostgres.Source.First <= 0 || toPostgres.Source.Last <= toPostgres.Source.First || toPostgres.Source.LastCopy != 0 {
		t.Fatalf("the offer: %+v %+v", view.Data, toPostgres.Source)
	}
	if toLocal.Available || toLocal.Problem != "empty" {
		t.Fatalf("the reverse offer with an empty PostgreSQL: %+v", toLocal)
	}

	// start, and a second start while it runs
	batchSize, pauseFor = 20, func(time.Duration) time.Duration { return 5 * time.Millisecond }
	if w := request(t, StartRoute, "POST", `{"direction":"to-postgres"}`, admin); w.Code != http.StatusOK {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	if w := request(t, StartRoute, "POST", `{"direction":"to-local"}`, admin); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "MC003") {
		t.Fatalf("a second start: %d %s", w.Code, w.Body.String())
	}
	json.Unmarshal(request(t, StatusRoute, "GET", "", admin).Body.Bytes(), &view)
	if !view.Data.Running || len(view.Data.Tables) != len(rules) || view.Data.Offers[0].Available {
		t.Fatalf("while running: %+v", view.Data)
	}
	wait()
	json.Unmarshal(request(t, StatusRoute, "GET", "", admin).Body.Bytes(), &view)
	if view.Data.Running || view.Data.Last == nil || view.Data.Last.Outcome != "done" || view.Data.Last.Written < 500 || view.Data.Last.UpTo == 0 {
		t.Fatalf("after the copy: %+v %+v", view.Data, view.Data.Last)
	}
	if view.Data.Offers[0].Source == nil || view.Data.Offers[0].Source.LastCopy == 0 {
		t.Errorf("the offer does not say the history was copied: %+v", view.Data.Offers[0])
	}
	if !view.Data.Offers[1].Available {
		t.Errorf("the reverse is not offered with records in PostgreSQL: %+v", view.Data.Offers[1])
	}
	found := false
	for _, e := range recorded() {
		if e.Kind == utils.SecMonitoringCopy && e.Actor == "admin" && e.Details["action"] == "start" && e.Details["direction"] == "to-postgres" {
			found = true
		}
	}
	if !found {
		t.Errorf("the start is not in the security record: %+v", recorded())
	}

	// the reverse: PostgreSQL's records are the local file's already
	if err := Start(ToLocal); err != nil {
		t.Fatal(err)
	}
	wait()
	if last := Status().Last; last.Outcome != "done" || last.Written != 0 {
		t.Fatalf("the reverse: %+v", last)
	}

	// cancelled after a batch, by the route
	pauseFor = func(time.Duration) time.Duration { return 30 * time.Millisecond }
	if err := Start(ToPostgres); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if w := request(t, CancelRoute, "POST", "", admin); w.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	wait()
	if last := Status().Last; last.Outcome != "cancelled" {
		t.Fatalf("after a cancel: %+v", last)
	}

	// the store in use changes during the copy (the fallback to the local file)
	if err := Start(ToPostgres); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	utils.LoadBaseMainConfig(localConfig)
	if err := utils.InitMetricsDatabase(); err != nil {
		t.Fatal(err)
	}
	wait()
	if last := Status().Last; last.Outcome != "stopped" || !strings.Contains(last.Message, "changed") {
		t.Fatalf("after the store changed: %+v", last)
	}
	if Status().Offers[0].Available {
		t.Error("a copy is offered with the local file in use")
	}

	// back on PostgreSQL: resumed to the end
	utils.LoadBaseMainConfig(pgConfig)
	if err := utils.InitMetricsDatabase(); err != nil {
		t.Fatal(err)
	}
	pauseFor = func(time.Duration) time.Duration { return 0 }
	if err := Start(ToPostgres); err != nil {
		t.Fatal(err)
	}
	wait()
	if last := Status().Last; last.Outcome != "done" || last.Written != 0 {
		t.Fatalf("resumed: %+v", last)
	}

	// no goroutine left behind (the store's handles were opened anew twice: as many as before)
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > goroutines && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > goroutines {
		buf := make([]byte, 1<<16)
		t.Errorf("%d goroutines, %d before:\n%s", n, goroutines, buf[:runtime.Stack(buf, true)])
	}
}

// The password of the monitoring database never reaches the log line of a failure.
func TestLogSafeMasksThePassword(t *testing.T) {
	previous := utils.GetMainConfig()
	config := previous
	config.Database.PostgresPassword = "s3cret-pass"
	utils.LoadBaseMainConfig(config)
	t.Cleanup(func() { utils.LoadBaseMainConfig(previous) })
	got := logSafe(errTest("connect postgres://cosmos:s3cret-pass@db.example/cosmos\nfailed"))
	if strings.Contains(got, "s3cret") || strings.Contains(got, "\n") {
		t.Fatalf("%q", got)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
