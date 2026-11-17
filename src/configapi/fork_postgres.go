package configapi

// Fork addition (plan 47, I2e): the monitoring database (Configuration >
// External).
//
//	POST /cosmos/api/config/postgres-test  {host, database, username, password}
//
// connects with the settings of the form, saved or not, and says in words
// whether the database answers. A password left empty or masked ("***") is
// the saved one, and only for the saved server, database and user: the saved
// password is never sent to another server. Admin, one test at a time and a
// few seconds apart, the dial bounded (utils.CheckPostgres); the password is
// in no answer and no log line.
//
// checkPostgresSettings is the hook of PUT /api/config: changed fields must
// have the shapes of utils.PostgresFieldsProblem; unchanged ones are left as
// they are, so an old odd value never blocks saving something else.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	postgresTestMaxBody = 8 << 10
	postgresTestGap     = 3 * time.Second
)

var (
	postgresTestMu   sync.Mutex
	postgresTestLast time.Time
	postgresTestBusy bool
	// checkPostgres is what the test asks: utils.CheckPostgresStore (plan
	// 51: also that the account may create the store's tables); replaced by
	// the tests.
	checkPostgres = utils.CheckPostgresStore
)

type postgresTestRequest struct {
	Host     string `json:"host"`
	Database string `json:"database"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// PostgresTestRoute godoc
// @Summary Monitoring database: test a connection before saving
// @Description Connects to the PostgreSQL server the form names and asks it one question; answers success or the reason in words. "***" as the password means the saved one, only for the saved server, database and user. Nothing is saved; the password is never in the answer. Administrators only, one test at a time, 3 seconds apart.
// @Tags config
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Failure 429 {object} utils.HTTPErrorResult
// @Router /api/config/postgres-test [post]
func PostgresTestRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	var body postgresTestRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, postgresTestMaxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		utils.HTTPError(w, "Invalid request", http.StatusBadRequest, "PG001")
		return
	}
	db := utils.DatabaseConfig{
		PostgresHost:     strings.TrimSpace(body.Host),
		PostgresDatabase: strings.TrimSpace(body.Database),
		PostgresUsername: strings.TrimSpace(body.Username),
		PostgresPassword: body.Password,
	}
	if db.PostgresPassword == "" || db.PostgresPassword == "***" {
		saved := utils.GetMainConfig().Database
		same := saved.PostgresHost == db.PostgresHost && saved.PostgresDatabase == db.PostgresDatabase && saved.PostgresUsername == db.PostgresUsername
		switch {
		case same:
			db.PostgresPassword = saved.PostgresPassword
		case db.PostgresPassword == "***":
			utils.HTTPError(w, "The saved password is only used for the saved server, database and user: enter the password for these settings", http.StatusBadRequest, "PG002")
			return
		}
	}
	if problem := utils.PostgresFieldsProblem(db); problem != "" {
		utils.HTTPError(w, problem, http.StatusBadRequest, "PG003")
		return
	}
	if db.PostgresHost == "" {
		utils.HTTPError(w, "Enter the database server first", http.StatusBadRequest, "PG003")
		return
	}

	postgresTestMu.Lock()
	if postgresTestBusy || time.Since(postgresTestLast) < postgresTestGap {
		postgresTestMu.Unlock()
		utils.HTTPError(w, "Wait a few seconds before testing again", http.StatusTooManyRequests, "PG429")
		return
	}
	postgresTestBusy = true
	postgresTestMu.Unlock()
	defer func() {
		postgresTestMu.Lock()
		postgresTestBusy, postgresTestLast = false, time.Now()
		postgresTestMu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()
	result := map[string]interface{}{"success": true, "message": "The database answered: these settings work."}
	if problem := checkPostgres(ctx, db); problem != nil {
		message := problem.Message
		if db.PostgresPassword != "" {
			message = strings.ReplaceAll(message, db.PostgresPassword, "[password]")
		}
		// Fork addition (plan 51): the likely right server, from the containers of this server
		advice := postgresAdvice(ctx, db.PostgresHost, problem.Reason)
		result = map[string]interface{}{"success": false, "reason": problem.Reason, "message": message, "advice": advice}
		utils.Log("Monitoring database: a connection test failed (" + problem.Reason + ")")
	}
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": result})
}

// keepPostgresPassword is the hook of PUT /api/config that runs before the
// masked password ("***", what a caller without the credentials permission
// reads) is put back: it is put back only for the server, database and user
// it was saved for. Otherwise a caller who may change the settings but not
// read the password could point the monitoring database at a server of their
// own and receive it at the next start. Like checkEmailSettings.
func keepPostgresPassword(request *utils.Config, stored utils.Config) *httpSettingsProblem {
	r, s := request.Database, stored.Database
	if r.PostgresPassword != "***" || s.PostgresPassword == "" {
		return nil
	}
	if strings.TrimSpace(r.PostgresHost) != s.PostgresHost || strings.TrimSpace(r.PostgresDatabase) != s.PostgresDatabase ||
		strings.TrimSpace(r.PostgresUsername) != s.PostgresUsername {
		return &httpSettingsProblem{"UC046", "Monitoring database: the server, port, database or user changed: type the password again"}
	}
	return nil
}

// checkPostgresSettings is the hook of PUT /api/config, after the masked
// password was put back: changed settings must have their shapes and
// (plan 51) must work: the server connects with them, signs in and makes a
// table in a transaction it rolls back, before anything is stored. A save
// that does not change them never connects. "Save anyway"
// (?database=unchecked) skips the connection, for administrators only, and
// is recorded.
func checkPostgresSettings(req *http.Request, request *utils.Config, stored utils.Config) *httpSettingsProblem {
	r, s := &request.Database, stored.Database
	r.PostgresHost = strings.TrimSpace(r.PostgresHost)
	r.PostgresDatabase = strings.TrimSpace(r.PostgresDatabase)
	r.PostgresUsername = strings.TrimSpace(r.PostgresUsername)
	if r.PostgresHost == s.PostgresHost && r.PostgresDatabase == s.PostgresDatabase && r.PostgresUsername == s.PostgresUsername &&
		(r.PostgresPassword == s.PostgresPassword || r.PostgresPassword == "***") {
		return nil
	}
	check := *r
	if check.PostgresPassword == "***" {
		check.PostgresPassword = ""
	}
	if problem := utils.PostgresFieldsProblem(check); problem != "" {
		return &httpSettingsProblem{"UC045", "Monitoring database: " + problem}
	}
	if r.PostgresHost == "" {
		return nil // back to the local file: nothing to connect to
	}
	if req != nil && req.URL.Query().Get("database") == "unchecked" {
		if !utils.HasPermission(req, utils.PERM_ADMIN) {
			utils.RecordSecurity(req, utils.SecAccessGrantDenied, "", "what", "database.unchecked")
			return &httpSettingsProblem{"UC048", "Monitoring database: only an administrator may save settings the database has not confirmed"}
		}
		utils.RecordSecurity(req, utils.SecDatabaseUnchecked, r.PostgresHost)
		return nil
	}
	return checkPostgresConnection(req, check)
}

// postgresSaveCheckBusy: one save check at a time (each may take seconds).
var (
	postgresSaveMu   sync.Mutex
	postgresSaveBusy bool
	// checkPostgresStore is utils.CheckPostgresStore; replaced by the tests.
	checkPostgresStore = utils.CheckPostgresStore
	// postgresAdvice is utils.PostgresHostAdvice; replaced by the tests.
	postgresAdvice = func(ctx context.Context, typed string, reason string) string {
		return utils.PostgresHostAdvice(ctx, typed, reason)
	}
)

// checkPostgresConnection is the save's connection check: UC047 with what
// failed and what to try.
func checkPostgresConnection(req *http.Request, db utils.DatabaseConfig) *httpSettingsProblem {
	postgresSaveMu.Lock()
	if postgresSaveBusy {
		postgresSaveMu.Unlock()
		return &httpSettingsProblem{"UC049", "Monitoring database: another save is checking the database: try again in a few seconds"}
	}
	postgresSaveBusy = true
	postgresSaveMu.Unlock()
	defer func() {
		postgresSaveMu.Lock()
		postgresSaveBusy = false
		postgresSaveMu.Unlock()
	}()

	parent := context.Background()
	if req != nil {
		parent = req.Context()
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	problem := checkPostgresStore(ctx, db)
	if problem == nil {
		return nil
	}
	message := problem.Message
	if db.PostgresPassword != "" {
		message = strings.ReplaceAll(message, db.PostgresPassword, "[password]")
	}
	actx, acancel := context.WithTimeout(parent, 5*time.Second)
	advice := postgresAdvice(actx, db.PostgresHost, problem.Reason)
	acancel()
	if advice != "" {
		message += " " + advice
	}
	utils.Log("Monitoring database: a save was refused, the database did not pass the check (" + problem.Reason + ")")
	return &httpSettingsProblem{"UC047", "Monitoring database: " + message + " Nothing was saved."}
}
