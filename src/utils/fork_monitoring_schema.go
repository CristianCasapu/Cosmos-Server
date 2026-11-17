package utils

// Fork addition: the whole schema of the monitoring store, for a store that
// Cosmos is not using right now (the copy of the history between the local
// file and PostgreSQL, src/monitorcopy).
//
// upstream's initMetricsSchema creates its tables (with the fork's columns of
// fork_notifications_schema.go); other packages keep tables of their own in
// the same store and create them themselves the first time they meet a handle
// (the security record, src/security/store.go). They register that code here,
// so that PrepareMonitoringSchema can lay out a complete store with Cosmos' own
// schema code, never with code of the copy.

import (
	"database/sql"
	"sort"
	"sync"
)

type monitoringSchemaHook struct {
	name   string
	create func(db *sql.DB, dialect string) error
}

var (
	monitoringSchemaMu    sync.Mutex
	monitoringSchemaHooks = map[string]monitoringSchemaHook{}
)

// RegisterMonitoringSchema records how a package creates its table(s) in the
// monitoring store. create must be idempotent (CREATE ... IF NOT EXISTS) and
// must use dialect, not the store in use, for its SQL. Called from init.
func RegisterMonitoringSchema(name string, create func(db *sql.DB, dialect string) error) {
	monitoringSchemaMu.Lock()
	defer monitoringSchemaMu.Unlock()
	monitoringSchemaHooks[name] = monitoringSchemaHook{name: name, create: create}
}

// RegisteredMonitoringSchemas lists the names given to RegisterMonitoringSchema, sorted.
func RegisteredMonitoringSchemas() []string {
	monitoringSchemaMu.Lock()
	defer monitoringSchemaMu.Unlock()
	names := make([]string, 0, len(monitoringSchemaHooks))
	for name := range monitoringSchemaHooks {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// PrepareMonitoringSchema lays out every table of the monitoring store in db:
// upstream's schema (initMetricsSchema, with the fork's hooks) and then each
// registered one, in the order of their names. It is what opening the store
// does, so running it on a store that is already prepared changes nothing.
func PrepareMonitoringSchema(db *sql.DB, dialect string) error {
	if err := initMetricsSchema(db, dialect); err != nil {
		return err
	}
	monitoringSchemaMu.Lock()
	hooks := make([]monitoringSchemaHook, 0, len(monitoringSchemaHooks))
	for _, hook := range monitoringSchemaHooks {
		hooks = append(hooks, hook)
	}
	monitoringSchemaMu.Unlock()
	sort.Slice(hooks, func(i, j int) bool { return hooks[i].name < hooks[j].name })
	for _, hook := range hooks {
		if err := hook.create(db, dialect); err != nil {
			return err
		}
	}
	return nil
}

// RebindFor turns `?` placeholders into `$n` when dialect is PostgreSQL, for
// SQL meant for a store other than the one in use.
func RebindFor(dialect string, query string) string {
	return rebindFor(dialect, query)
}

// LocalMonitoringFile is the local file of the monitoring store, the one
// InitMetricsDatabase opens when no PostgreSQL server is configured.
func LocalMonitoringFile() string {
	return CONFIGFOLDER + "database.db"
}
