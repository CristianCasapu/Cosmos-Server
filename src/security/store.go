package security

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The table of the record. It lives in the monitoring database
// (database.db, or Postgres when that is configured) next to upstream's
// tables, but nothing of upstream's schema code knows it: this package
// creates it, under a name with the fork's prefix, each time it meets a
// database handle it has not prepared yet (a first start, a database opened
// again, a new folder). Its version is kept under a key of its own in
// monitoring_kv, so upstream's schema version and migrations are left alone
// and an upstream merge cannot drop or alter it.
const (
	tableName        = "fork_security_events"
	schemaVersionKey = "fork_security_schema"
	schemaVersion    = 1
)

const schemaV1 = `
CREATE TABLE IF NOT EXISTS fork_security_events (
	id {{AUTOPK}},
	node TEXT NOT NULL DEFAULT '',
	ts BIGINT NOT NULL DEFAULT 0,
	kind TEXT NOT NULL DEFAULT '',
	class TEXT NOT NULL DEFAULT '',
	severity INTEGER NOT NULL DEFAULT 1,
	actor TEXT NOT NULL DEFAULT '',
	ip TEXT NOT NULL DEFAULT '',
	target TEXT NOT NULL DEFAULT '',
	host TEXT NOT NULL DEFAULT '',
	path TEXT NOT NULL DEFAULT '',
	method TEXT NOT NULL DEFAULT '',
	outcome TEXT NOT NULL DEFAULT '',
	n INTEGER NOT NULL DEFAULT 1,
	details TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_fork_security_ts ON fork_security_events(ts);
CREATE INDEX IF NOT EXISTS idx_fork_security_kind ON fork_security_events(kind, id);
CREATE INDEX IF NOT EXISTS idx_fork_security_ip ON fork_security_events(ip, id);
CREATE INDEX IF NOT EXISTS idx_fork_security_actor ON fork_security_events(actor, id);
CREATE INDEX IF NOT EXISTS idx_fork_security_class ON fork_security_events(class, severity, id)
`

// Retention: rows older than retention are deleted, and each kind keeps at
// most its cap, oldest first. The cap is per kind so that a flood of one
// kind can only push out older rows of that same kind.
const (
	retention     = 180 * 24 * time.Hour
	capFloodable  = 5000  // incident kinds below "high": the ones a stranger can produce
	capOtherKinds = 20000 // audit kinds and incidents of severity "high"
)

func capOf(kind string) int {
	known, ok := catalogue[kind]
	if !ok || (known.Class == ClassIncident && known.severity < SeverityHigh) {
		return capFloodable
	}
	return capOtherKinds
}

var (
	schemaMu    sync.Mutex
	schemaReady *sql.DB
)

// writeDB returns the monitoring database with the table in place.
func writeDB() (*sql.DB, error) {
	db, err := utils.MonitoringWriteDB()
	if err != nil {
		return nil, err
	}

	schemaMu.Lock()
	defer schemaMu.Unlock()
	if schemaReady == db {
		return db, nil
	}
	if err := createSchema(db, utils.MonitoringDialect()); err != nil {
		return nil, err
	}
	schemaReady = db
	return db, nil
}

// readDB returns the reading handle, once the table exists.
func readDB() (*sql.DB, error) {
	if _, err := writeDB(); err != nil {
		return nil, err
	}
	return utils.MonitoringReadDB()
}

func createSchema(db *sql.DB, dialect string) error {
	autoPK := "INTEGER PRIMARY KEY AUTOINCREMENT"
	if dialect == utils.DialectPostgres {
		autoPK = "BIGSERIAL PRIMARY KEY"
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// one statement at a time: the SQLite driver takes no more
	for _, statement := range strings.Split(strings.ReplaceAll(schemaV1, "{{AUTOPK}}", autoPK), ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}

	// later versions of the table are brought up to date here, by what the
	// key says: "current < 2" and so on
	set := "INSERT INTO monitoring_kv(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value"
	if _, err := tx.Exec(utils.MonitoringRebind(set), schemaVersionKey, strconv.Itoa(schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

const insertSQL = "INSERT INTO " + tableName +
	"(node, ts, kind, class, severity, actor, ip, target, host, path, method, outcome, n, details)" +
	" VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id"

// insert stores the events in one transaction, in their order, and sets
// their IDs. Either all of them are stored or none.
func insert(events []Event) error {
	if len(events) == 0 {
		return nil
	}
	db, err := writeDB()
	if err != nil {
		return err
	}
	node := utils.MonitoringNode()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	statement, err := tx.Prepare(utils.MonitoringRebind(insertSQL))
	if err != nil {
		return err
	}
	defer statement.Close()

	ids := make([]int64, len(events))
	for i, event := range events {
		details := ""
		if len(event.Details) > 0 {
			encoded, err := json.Marshal(event.Details)
			if err != nil {
				return err
			}
			details = string(encoded)
		}
		severity, _ := ParseSeverity(event.Severity)
		if severity == 0 {
			severity = SeverityInfo
		}
		err := statement.QueryRow(node, utils.TimeToMillis(event.Time), event.Kind, event.Class, int(severity),
			event.Actor, event.IP, event.Target, event.Host, event.Path, event.Method, event.Outcome,
			event.Count, details).Scan(&ids[i])
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for i := range events {
		events[i].ID = ids[i]
	}
	return nil
}

// prune deletes what is older than the retention and what is over a kind's
// cap, for the rows this server wrote. It returns how many rows went.
func prune(now time.Time) (int64, error) {
	db, err := writeDB()
	if err != nil {
		return 0, err
	}
	node := utils.MonitoringNode()

	result, err := db.Exec(utils.MonitoringRebind("DELETE FROM "+tableName+" WHERE node = ? AND ts < ?"),
		node, utils.TimeToMillis(now.Add(-retention)))
	if err != nil {
		return 0, err
	}
	deleted, _ := result.RowsAffected()

	rows, err := db.Query(utils.MonitoringRebind("SELECT kind, COUNT(*) FROM "+tableName+" WHERE node = ? GROUP BY kind"), node)
	if err != nil {
		return deleted, err
	}
	over := []string{}
	for rows.Next() {
		var kind string
		var count int
		if err := rows.Scan(&kind, &count); err != nil {
			rows.Close()
			return deleted, err
		}
		if count > capOf(kind) {
			over = append(over, kind)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return deleted, err
	}

	for _, kind := range over {
		// the row just past the cap, counted from the newest, and all before it
		result, err := db.Exec(utils.MonitoringRebind(
			"DELETE FROM "+tableName+" WHERE node = ? AND kind = ? AND id <= ("+
				"SELECT id FROM "+tableName+" WHERE node = ? AND kind = ? ORDER BY id DESC LIMIT 1 OFFSET ?)"),
			node, kind, node, kind, capOf(kind))
		if err != nil {
			return deleted, err
		}
		n, _ := result.RowsAffected()
		deleted += n
	}
	return deleted, nil
}
