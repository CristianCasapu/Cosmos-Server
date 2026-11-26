package utils

// Fork addition (plan 51, review round 2, M1): the daily retention deletes
// in chunks, each in a transaction of its own with a statement bound of
// 10 minutes. The store's PostgreSQL sessions stop a statement after 30 s,
// which a single DELETE of a day's expired buckets (millions of rows) can
// exceed: it would then fail every day and the table would only grow. Small
// chunks also never hold the writers back for long.

import (
	"database/sql"
	"regexp"
	"strconv"
)

const (
	retentionChunk          = 20000
	retentionStatementBound = "600000" // ms
)

// retentionStatement is "DELETE FROM <table> WHERE <condition>".
var retentionStatement = regexp.MustCompile(`^DELETE FROM ([a-z_]+) WHERE (.+)$`)

// retentionDelete runs a retention DELETE in chunks until nothing is left;
// it returns the rows deleted.
func retentionDelete(db *sql.DB, query string, args ...interface{}) (int64, error) {
	m := retentionStatement.FindStringSubmatch(query)
	dialect := MonitoringDialectOf(db)
	if m == nil {
		res, err := db.Exec(rebindFor(dialect, query), args...)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	}
	table, condition := m[1], m[2]
	row := "rowid"
	if dialect == DialectPostgres {
		row = "ctid"
	}
	chunk := rebindFor(dialect, "DELETE FROM "+table+" WHERE "+row+" IN (SELECT "+row+" FROM "+table+" WHERE "+condition+" LIMIT "+strconv.Itoa(retentionChunk)+")")
	var deleted int64
	for {
		n, err := retentionChunkOnce(db, dialect, chunk, args)
		deleted += n
		if err != nil || n < retentionChunk {
			return deleted, err
		}
	}
}

func retentionChunkOnce(db *sql.DB, dialect string, statement string, args []interface{}) (int64, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if dialect == DialectPostgres {
		if _, err := tx.Exec("SET LOCAL statement_timeout = " + retentionStatementBound); err != nil {
			return 0, err
		}
	}
	res, err := tx.Exec(statement, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, tx.Commit()
}
