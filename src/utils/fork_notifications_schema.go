package utils

// Fork addition: the columns the fork's notifications need (see
// fork_notifications.go), added to upstream's notifications table.
//
// Every column has a constant default, so rows written before (and rows
// written by upstream's own insert, which names its columns) read as an old
// notification: no kind, one occurrence, never resolved. Adding a column
// with a constant default does not rewrite a SQLite table; the step runs in
// the transaction of upstream's schema, is repeated harmlessly at every
// opening, and marks what it knows about the rows that were there before.

import (
	"database/sql"
	"strings"
)

// forkNotificationColumns are added in this order, prefixed so that a later
// migration of upstream cannot take the same names.
var forkNotificationColumns = []struct{ name, kind string }{
	{"fork_kind", "TEXT NOT NULL DEFAULT ''"},
	{"fork_source", "TEXT NOT NULL DEFAULT ''"},
	{"fork_thread", "TEXT NOT NULL DEFAULT ''"},
	{"fork_ref", "TEXT NOT NULL DEFAULT ''"},
	{"fork_audience", "TEXT NOT NULL DEFAULT ''"},
	{"fork_count", "INTEGER NOT NULL DEFAULT 0"},
	{"fork_first", "BIGINT NOT NULL DEFAULT 0"},
	{"fork_resolved", "BIGINT NOT NULL DEFAULT 0"},
	{"fork_search", "TEXT NOT NULL DEFAULT ''"},
	{"fork_info", "TEXT NOT NULL DEFAULT ''"},
}

const forkNotificationsSchemaVersion = "1"

// forkNotificationsSchema is the hook of initMetricsSchema.
func forkNotificationsSchema(tx *sql.Tx, dialect string) error {
	have, err := notificationColumns(tx, dialect)
	if err != nil {
		return err
	}
	for _, column := range forkNotificationColumns {
		if have[column.name] {
			continue
		}
		if _, err := tx.Exec("ALTER TABLE notifications ADD COLUMN " + column.name + " " + column.kind); err != nil {
			return err
		}
	}
	for _, stmt := range []string{
		"CREATE INDEX IF NOT EXISTS fork_idx_notifications_date ON notifications(recipient, date)",
		"CREATE INDEX IF NOT EXISTS fork_idx_notifications_thread ON notifications(recipient, fork_thread)",
		"CREATE INDEX IF NOT EXISTS fork_idx_notifications_ref ON notifications(fork_ref)",
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}

	var done string
	err = tx.QueryRow(rebindFor(dialect, "SELECT value FROM monitoring_kv WHERE key = ?"), "fork_notifications_schema").Scan(&done)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if done == forkNotificationsSchemaVersion {
		return nil
	}

	// The rows from before: what their title says about them. All the
	// notifications Cosmos raised so far went to the administrators.
	for title, kind := range legacyNotificationKinds {
		k := notificationKinds[kind]
		if _, err := tx.Exec(rebindFor(dialect,
			"UPDATE notifications SET fork_kind = ?, fork_source = ?, fork_audience = 'admin' WHERE fork_kind = '' AND fork_info = '' AND title = ?"),
			kind, k.Source, title); err != nil {
			return err
		}
	}
	set := "INSERT INTO monitoring_kv(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value"
	_, err = tx.Exec(rebindFor(dialect, set), "fork_notifications_schema", forkNotificationsSchemaVersion)
	return err
}

// notificationColumns lists the columns the notifications table has.
func notificationColumns(tx *sql.Tx, dialect string) (map[string]bool, error) {
	query := "SELECT name FROM pragma_table_info('notifications')"
	if dialect == DialectPostgres {
		query = "SELECT column_name FROM information_schema.columns WHERE table_name = 'notifications' AND table_schema = current_schema()"
	}
	rows, err := tx.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		have[strings.ToLower(name)] = true
	}
	return have, rows.Err()
}
