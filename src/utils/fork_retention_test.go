package utils

// Fork addition (plan 51, review round 2, M1): tests of fork_retention.go.

import (
	"testing"
)

// The retention deletes in chunks until nothing is left, and only what it
// should.
func TestRetentionDeletesInChunks(t *testing.T) {
	withMonitoringStore(t, DatabaseConfig{}, nil)
	InitMetricsDatabase()
	db, _ := MonitoringWriteDB()
	tx, _ := db.Begin()
	for i := 0; i < retentionChunk*2+7; i++ {
		tx.Exec("INSERT INTO metric_values(node, key, granularity, date, value, expire) VALUES('n', 'k', 'h', ?, 1, 5)", i)
	}
	for i := 0; i < 11; i++ {
		tx.Exec("INSERT INTO metric_values(node, key, granularity, date, value, expire) VALUES('n', 'kept', 'h', ?, 1, 999999)", i)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	n, err := retentionDelete(db, "DELETE FROM metric_values WHERE node = ? AND expire > 0 AND expire < ?", "n", 100)
	if err != nil || n != retentionChunk*2+7 {
		t.Fatalf("deleted %d, %v", n, err)
	}
	var left int
	db.QueryRow("SELECT COUNT(*) FROM metric_values").Scan(&left)
	if left != 11 {
		t.Errorf("left %d", left)
	}
}
