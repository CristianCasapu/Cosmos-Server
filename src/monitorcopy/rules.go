// Package monitorcopy copies the history of the monitoring store (metrics,
// events, notifications, the security record) between the local SQLite file
// and PostgreSQL, on an administrator's request only.
//
// Fork addition (plan 51, item E2). The engine is dialect-generic: a Store is
// a handle and its dialect, and the same code copies either way. The target's
// tables are laid out by Cosmos' own schema code (utils.PrepareMonitoringSchema),
// never by the copy. Every table of the store has a rule here; a test fails
// when a table or a column appears in the schema without one.
package monitorcopy

import (
	"strings"
	"time"
)

// the kind of a column, for scanning and for the dialects' differences
type kind int

const (
	text      kind = iota // TEXT in both
	integer               // BIGINT in both (SQLite: INTEGER, 64 bits)
	integer32             // INTEGER: 64 bits in SQLite, 32 in PostgreSQL
	real                  // REAL in SQLite, DOUBLE PRECISION in PostgreSQL
)

type column struct {
	name string
	kind kind
	// def is the column's default as an SQL literal: what a source of an
	// older layout without this column gives
	def string
	// nanAs is what a REAL column stores in the local file for a value that is
	// not a finite number (SQLite would make NaN a NULL, which NOT NULL refuses)
	nanAs float64
}

func col(name string, k kind, def string) column { return column{name: name, kind: k, def: def} }

// scale is a REAL column the panel divides by: a value that is not a finite
// number is stored as 1, the neutral scale, never 0.
func scale(name string) column { return column{name: name, kind: real, def: "0", nanAs: 1} }

// rule says how one table is copied. Two shapes:
//
//   - a natural key (key set): the source's rows are read in key order and
//     written with INSERT ... ON CONFLICT(key), whose clause is the rule for
//     rows that exist in both stores;
//   - a surrogate id (key empty): the id is never copied (both stores count
//     from 1, so the source's ids collide with the target's); the target gives
//     the row a new id from its own sequence. A row is the same record when
//     its identity is the same: the rows are read in the order of their time
//     (window), all rows of one time value in one batch, and matched against
//     the target's rows of the same time values, as a multiset, so two equal
//     rows stay two rows and a row already copied is never copied again.
type rule struct {
	table   string
	columns []column // copied columns, in order (a surrogate table's id is not one)
	key     []string // natural key, in order
	// window is the SQL expression of the time a row belongs to (unix ms), for
	// the surrogate tables' order and for the verification's ranges; "" when
	// the table has none (metrics, monitoring_kv)
	window string
	// retention: rows the target's daily retention would delete at once are
	// not copied (column and age); zero when the table keeps everything
	retentionCol string
	retention    time.Duration
	// expireCol: rows whose expiry passed are not copied (metric_values)
	expireCol string
	// touched is the column that says when a row was last written, for the
	// catch-up of a window (Options.From); touchedSlack widens the window back
	// (a bucket written in the window carries the time its period began).
	// "" leaves the table out of a catch-up.
	touched      string
	touchedSlack time.Duration
	// touchedFilter, when set, is the catch-up's time condition instead of
	// touched and touchedSlack (from and end in unix ms)
	touchedFilter func(from, end int64) (string, []interface{})
	// where is a fixed condition on the rows copied (monitoring_kv's
	// bookkeeping keys are left out by it)
	where string
	// identity of a surrogate row: indexes into columns
	identity func(r row) []interface{}
	// refresh: for a matched surrogate row, whether the source's copy is the
	// newer one and replaces refreshCols of the target's
	refresh     func(src, dst row) bool
	refreshCols []string
	// onConflict is the clause after ON CONFLICT(key) for a natural table;
	// targetInUse says whether the target is the store Cosmos is writing
	onConflict func(targetInUse bool) string
	// verifySums: integer columns summed (with the window) when the two
	// stores are compared over the range only the copy wrote
	verifySums []string
}

// row is one row's values in the order of the rule's columns, with the id
// and window value of a surrogate table.
type row struct {
	id     int64
	w      int64
	values []interface{}
}

func (r rule) surrogate() bool { return len(r.key) == 0 }

func (r rule) index(name string) int {
	for i, c := range r.columns {
		if c.name == name {
			return i
		}
	}
	panic("monitorcopy: no column " + name + " in " + r.table)
}

func (r rule) columnNames() []string {
	names := make([]string, len(r.columns))
	for i, c := range r.columns {
		names[i] = c.name
	}
	return names
}

// Bookkeeping keys of monitoring_kv: each store keeps its own, written by its
// own schema code; they are never copied.
func bookkeepingKey(key string) bool {
	return key == "schema_version" || strings.HasSuffix(key, "_schema") || strings.HasPrefix(key, markerPrefix)
}

// markerPrefix starts the keys of the copy's own markers in the target's
// monitoring_kv.
const markerPrefix = "fork_monitoring_copy:"

// The retention of upstream's RunDatabaseRetention (events, notifications)
// and of the security record (src/security/store.go).
const (
	monthRetention    = 30 * 24 * time.Hour
	securityRetention = 180 * 24 * time.Hour
)

// rules: every table of the monitoring store. The order is the copy's.
var rules = func() []rule {
	kv := rule{
		table:   "monitoring_kv",
		columns: []column{col("key", text, "''"), col("value", text, "''")},
		key:     []string{"key"},
		// Key: key. A key in both stores: the target's value stays (the
		// fork keeps no data of its own there; schema bookkeeping and the
		// copy's markers are left out by skip).
		where:      "key <> 'schema_version' AND key NOT LIKE '%\\_schema' ESCAPE '\\' AND key NOT LIKE 'fork\\_monitoring\\_copy:%' ESCAPE '\\'",
		onConflict: func(bool) string { return "DO NOTHING" },
	}

	metricCols := []column{
		col("node", text, "''"), col("key", text, "''"), col("label", text, "''"), col("unit", text, "''"),
		col("max", integer, "0"), col("agglo_type", text, "''"), col("scale", integer32, "1"), col("object", text, "''"),
		scale("time_scale"), col("last_update", integer, "0"),
	}
	metrics := rule{
		table:   "metrics",
		columns: metricCols,
		touched: "last_update",
		// last_update is the date of the bucket last flushed: a bucket of an
		// hour's period, flushed late (kept while the pings found PostgreSQL
		// gone), is dated up to an hour before
		touchedSlack: 75 * time.Minute,
		key:          []string{"node", "key"},
		// Key: (node, key), the table's primary key. A metric in both
		// stores: the row whose last_update is newer wins (the catalogue
		// describes the metric as last seen), whichever store it is in.
		onConflict: func(bool) string {
			return "DO UPDATE SET label = excluded.label, unit = excluded.unit, max = excluded.max, agglo_type = excluded.agglo_type," +
				" scale = excluded.scale, object = excluded.object, time_scale = excluded.time_scale, last_update = excluded.last_update" +
				" WHERE metrics.last_update < excluded.last_update"
		},
	}

	values := rule{
		table: "metric_values",
		columns: []column{
			col("node", text, "''"), col("key", text, "''"), col("granularity", text, "''"), col("date", integer, "0"),
			col("value", integer, "0"), col("avg_index", integer32, "0"), col("expire", integer, "0"),
		},
		key:       []string{"node", "key", "granularity", "date"},
		window:    "date",
		expireCol: "expire",
		// a bucket carries the time its period began: a day bucket written in
		// the window began at the midnight before it, an hour bucket at the
		// hour, a raw bucket at most an hour (its longest period) before
		touched:      "date",
		touchedSlack: 24 * time.Hour,
		touchedFilter: func(from, end int64) (string, []interface{}) {
			day, hour := int64(24*time.Hour/time.Millisecond), int64(time.Hour/time.Millisecond)
			return "((granularity = 'day' AND date >= ?) OR (granularity = 'hour' AND date >= ?) OR (granularity NOT IN ('day', 'hour') AND date >= ?)) AND date < ?",
				[]interface{}{from - from%day, from - from%hour, from - hour, end}
		},
		// Key: (node, key, granularity, date), the primary key: one bucket.
		// A bucket in both stores is merged by buckets.go (review round 3,
		// S2): hour and day rows add up the other store's contribution once
		// (sum, avg) or take the larger (max) or smaller (min) value; raw rows
		// go to the store with the later flushes. onConflict is used by
		// nothing for this table.
		onConflict: func(targetInUse bool) string {
			if targetInUse {
				return "DO NOTHING"
			}
			return "DO UPDATE SET value = excluded.value, avg_index = excluded.avg_index, expire = excluded.expire" +
				" WHERE metric_values.value <> excluded.value OR metric_values.avg_index <> excluded.avg_index OR metric_values.expire <> excluded.expire"
		},
		verifySums: []string{"value", "avg_index"},
	}

	eventCols := []column{
		col("node", text, "''"), col("event_id", text, "''"), col("label", text, "''"), col("application", text, "''"),
		col("level", text, "''"), col("date", integer, "0"), col("data", text, "''"), col("object", text, "''"), col("search", text, "''"),
	}
	events := rule{
		table:        "events",
		columns:      eventCols,
		window:       "date",
		retentionCol: "date",
		retention:    monthRetention,
		touched:      "date",
		// Identity: every column but the id. An event is never changed once
		// written, so two rows with the same values are the same record (or
		// two identical records, which stay two: the match is a multiset).
		identity: func(r row) []interface{} { return r.values },
	}

	notificationCols := []column{
		col("node", text, "''"), col("recipient", text, "''"), col("title", text, "''"), col("message", text, "''"),
		col("vars", text, "''"), col("icon", text, "''"), col("link", text, "''"), col("date", integer, "0"),
		col("level", text, "''"), col("read", integer32, "0"), col("actions", text, "''"),
		col("fork_kind", text, "''"), col("fork_source", text, "''"), col("fork_thread", text, "''"), col("fork_ref", text, "''"),
		col("fork_audience", text, "''"), col("fork_count", integer32, "0"), col("fork_first", integer, "0"),
		col("fork_resolved", integer, "0"), col("fork_search", text, "''"), col("fork_info", text, "''"),
	}
	notifications := rule{
		table:   "notifications",
		columns: notificationCols,
		// The time a notification was born: fork_first (set once, when the
		// fork writes it), else date (upstream's rows never change date).
		// date itself moves when a repeated notice is grouped.
		window:       "CASE WHEN fork_first > 0 THEN fork_first ELSE date END",
		retentionCol: "date",
		touched:      "date", // written or grouped again
		retention:    monthRetention,
		verifySums:   []string{},
	}
	nNode, nRecipient, nTitle, nMessage, nDate, nRef := 0, 1, 2, 3, 7, 14
	notifications.identity = func(r row) []interface{} {
		// the fork's rows: one reference per sending, one row per recipient
		if ref := r.values[nRef].(string); ref != "" {
			return []interface{}{r.values[nNode], r.values[nRecipient], ref}
		}
		// upstream's: only read changes after the insert
		return []interface{}{r.values[nNode], r.values[nRecipient], "", r.values[nTitle], r.values[nMessage], r.values[nDate]}
	}
	// Both stores have it: the copy that was updated last (date moves on
	// each repeat) wins; read state and counters go with it.
	notifications.refresh = func(src, dst row) bool { return src.values[nDate].(int64) > dst.values[nDate].(int64) }
	notifications.refreshCols = []string{"title", "message", "vars", "link", "date", "level", "read", "fork_count", "fork_resolved", "fork_search", "fork_info"}

	security := rule{
		table: "fork_security_events",
		columns: []column{
			col("node", text, "''"), col("ts", integer, "0"), col("kind", text, "''"), col("class", text, "''"),
			col("severity", integer32, "1"), col("actor", text, "''"), col("ip", text, "''"), col("target", text, "''"),
			col("host", text, "''"), col("path", text, "''"), col("method", text, "''"), col("outcome", text, "''"),
			col("n", integer32, "1"), col("details", text, "''"),
		},
		window:       "ts",
		retentionCol: "ts",
		touched:      "ts",
		retention:    securityRetention,
		// Identity: every column but the id (entries are never changed).
		identity:   func(r row) []interface{} { return r.values },
		verifySums: []string{"severity", "n"},
	}

	return []rule{kv, metrics, values, events, notifications, security}
}()

func ruleOf(table string) (rule, bool) {
	for _, r := range rules {
		if r.table == table {
			return r, true
		}
	}
	return rule{}, false
}
