package utils

// Fork addition (plan 51): tests of fork_monitoring_store.go. A PostgreSQL
// that does not answer leaves monitoring on the local file, said; the
// watcher tries again with a back-off; a real PostgreSQL (COSMOS_FORK_PG_TEST,
// a host:port with user/password cosmos/cosmos-test-pw and a database
// cosmos) proves the switch and the healing.

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

type storeStub struct {
	problem *PostgresProblem
	probes  int
	down    []MonitoringStoreState
	back    []MonitoringStoreState
}

func withMonitoringStore(t *testing.T, db DatabaseConfig, problem *PostgresProblem) *storeStub {
	t.Helper()
	stub := &storeStub{problem: problem}
	prevFolder, prevConfig := CONFIGFOLDER, GetBaseMainConfig()
	prevProbe, prevDown, prevBack, prevResolve := probeMonitoringPostgres, monitoringTellDown, monitoringTellBack, monitoringResolveDown
	CONFIGFOLDER = t.TempDir() + "/"
	config := prevConfig
	config.Database = db
	LoadBaseMainConfig(config)
	probeMonitoringPostgres = func(ctx context.Context, d DatabaseConfig) *PostgresProblem {
		stub.probes++
		if stub.problem == nil {
			return nil
		}
		p := *stub.problem
		return &p
	}
	monitoringTellDown = func(s MonitoringStoreState) { stub.down = append(stub.down, s) }
	monitoringTellBack = func(s MonitoringStoreState, since time.Time) { stub.back = append(stub.back, s) }
	monitoringResolveDown = func() {}
	setStoreState(func(s *MonitoringStoreState) { *s = MonitoringStoreState{Configured: "sqlite"} })
	storeStateMu.Lock()
	storeNoticePending, storeForceLocal = false, false
	storeStateMu.Unlock()
	t.Cleanup(func() {
		CloseMetricsDatabase()
		CONFIGFOLDER = prevFolder
		LoadBaseMainConfig(prevConfig)
		probeMonitoringPostgres, monitoringTellDown, monitoringTellBack, monitoringResolveDown = prevProbe, prevDown, prevBack, prevResolve
		setStoreState(func(s *MonitoringStoreState) { *s = MonitoringStoreState{Configured: "sqlite"} })
		storeStateMu.Lock()
		storeNoticePending, storeForceLocal = false, false
		storeStateMu.Unlock()
	})
	return stub
}

var refusedProblem = &PostgresProblem{"closed", "Nothing answers at localhost:5432: check the address and the port, and that the database runs."}

// The owner's state: PostgreSQL configured where nothing answers. The
// store is the local file, it records, and the state says why.
func TestBrokenPostgresLeavesMonitoringOnTheLocalFile(t *testing.T) {
	stub := withMonitoringStore(t, DatabaseConfig{PostgresHost: "localhost:5432", PostgresDatabase: "cosmos", PostgresUsername: "cosmos", PostgresPassword: "x"}, refusedProblem)
	if err := InitMetricsDatabase(); err != nil {
		t.Fatalf("the opening failed: %v", err)
	}
	if MonitoringDialect() != DialectSQLite {
		t.Fatalf("dialect %s", MonitoringDialect())
	}
	db, err := MonitoringWriteDB()
	if err != nil {
		t.Fatalf("no store: %v", err)
	}
	if _, err := db.Exec("INSERT INTO monitoring_kv(key, value) VALUES('t', 'v')"); err != nil {
		t.Fatalf("the local file does not record: %v", err)
	}
	if _, err := os.Stat(CONFIGFOLDER + "database.db"); err != nil {
		t.Fatalf("no local file: %v", err)
	}
	s := GetMonitoringStoreState()
	if !s.Fallback || s.Configured != "postgres" || s.InUse != DialectSQLite || s.Host != "localhost:5432" || s.Reason != "closed" || s.Since.IsZero() {
		t.Fatalf("state %+v", s)
	}
	if !strings.Contains(MonitoringStoreSentence(s), "does not answer since") || !strings.Contains(MonitoringStoreSentence(s), "local file") {
		t.Errorf("sentence %q", MonitoringStoreSentence(s))
	}
	if GetMainConfig().Database.PostgresHost != "localhost:5432" {
		t.Error("the settings were changed: the fallback must not touch them")
	}

	// the notice is told once, by the watcher, after the store is open
	w := &monitoringWatch{backoff: monitoringRetryFirst}
	w.look(true)
	w.look(false)
	if len(stub.down) != 1 || stub.down[0].Host != "localhost:5432" {
		t.Errorf("told %+v", stub.down)
	}
	// a second opening with the same settings (a restart of the watcher's
	// try, an unrelated reopen) is not a new notice
	InitMetricsDatabase()
	w.look(true)
	if len(stub.down) != 1 {
		t.Errorf("told again: %+v", stub.down)
	}
}

// Working local-file settings: nothing is probed, nothing is told.
func TestLocalFileStoreProbesNothing(t *testing.T) {
	stub := withMonitoringStore(t, DatabaseConfig{}, refusedProblem)
	if err := InitMetricsDatabase(); err != nil {
		t.Fatal(err)
	}
	w := &monitoringWatch{backoff: monitoringRetryFirst}
	if wait := w.look(true); wait != monitoringIdleLook {
		t.Errorf("wait %v", wait)
	}
	if stub.probes != 0 || len(stub.down) != 0 || GetMonitoringStoreState().Fallback {
		t.Errorf("probes %d, told %v, %+v", stub.probes, stub.down, GetMonitoringStoreState())
	}
}

// The back-off doubles up to its cap and the state says when the next try is.
func TestFallbackRetriesWithABackOff(t *testing.T) {
	withMonitoringStore(t, DatabaseConfig{PostgresHost: "192.0.2.10:5432", PostgresUsername: "cosmos"}, refusedProblem)
	InitMetricsDatabase()
	w := &monitoringWatch{backoff: monitoringRetryFirst}
	if wait := w.look(true); wait != monitoringRetryFirst {
		t.Errorf("first wait %v", wait)
	}
	want := []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 160 * time.Second, 5 * time.Minute, 5 * time.Minute}
	for i, expected := range want {
		if wait := w.look(false); wait != expected {
			t.Errorf("try %d: wait %v, want %v", i, wait, expected)
		}
	}
	s := GetMonitoringStoreState()
	if s.NextTry.Before(time.Now().Add(4*time.Minute)) || s.Attempts < len(want) {
		t.Errorf("state %+v", s)
	}
	// a settings change starts the back-off again
	if wait := w.look(true); wait != monitoringRetryFirst {
		t.Errorf("after a change: %v", wait)
	}
}

// PostgreSQL answers the probe but its tables cannot be made: the store is
// not left closed, the local file is opened again, the reason says it.
func TestSchemaFailureAfterTheProbeFallsBack(t *testing.T) {
	stub := withMonitoringStore(t, DatabaseConfig{PostgresHost: "127.0.0.1:1", PostgresUsername: "cosmos", PostgresPassword: "x"}, nil)
	if err := InitMetricsDatabase(); err == nil {
		t.Fatal("opened a database that does not exist")
	}
	if _, err := MonitoringWriteDB(); err == nil {
		t.Fatal("expected the store closed after upstream's failure")
	}
	w := &monitoringWatch{backoff: monitoringRetryFirst}
	w.look(false)
	s := GetMonitoringStoreState()
	if s.InUse != DialectSQLite || !s.Fallback || s.Reason != "schema" {
		t.Fatalf("state %+v", s)
	}
	since := s.Since
	// it answers the probe again but still cannot be opened: the local file
	// at once again, the notice told (again: it was closed before the try),
	// the same outage
	w.look(false)
	w.look(false)
	after := GetMonitoringStoreState()
	if after.InUse != DialectSQLite || !after.Fallback || !after.Since.Equal(since) {
		t.Fatalf("after a failed switch: %+v (since %v)", after, since)
	}
	if len(stub.down) < 1 || len(stub.back) != 0 {
		t.Errorf("told %+v, back %+v", stub.down, stub.back)
	}
}

// One watcher for the process however often the store is opened.
func TestNoGoroutinePerOpening(t *testing.T) {
	withMonitoringStore(t, DatabaseConfig{PostgresHost: "192.0.2.10:5432", PostgresUsername: "cosmos"}, refusedProblem)
	InitMetricsDatabase()
	time.Sleep(50 * time.Millisecond)
	before := runtime.NumGoroutine()
	for i := 0; i < 30; i++ {
		InitMetricsDatabase()
	}
	time.Sleep(100 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+3 {
		t.Errorf("goroutines %d → %d", before, after)
	}
}

// The probe makes a table in a transaction it rolls back, and says when the
// account may not create tables (a real PostgreSQL only).
func TestCheckPostgresStoreLive(t *testing.T) {
	host := os.Getenv("COSMOS_FORK_PG_TEST")
	if host == "" {
		t.Skip("COSMOS_FORK_PG_TEST not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ok := DatabaseConfig{PostgresHost: host, PostgresDatabase: "cosmos", PostgresUsername: "cosmos", PostgresPassword: "cosmos-test-pw"}
	if p := CheckPostgresStore(ctx, ok); p != nil {
		t.Fatalf("working settings: %+v", p)
	}
	// the role "reader" (made by the test setup) may sign in but not create
	reader := DatabaseConfig{PostgresHost: host, PostgresDatabase: "cosmos", PostgresUsername: "reader", PostgresPassword: "reader-test-pw"}
	if p := CheckPostgresStore(ctx, reader); p == nil || p.Reason != "create" || !strings.Contains(p.Message, "GRANT CREATE ON SCHEMA public TO reader") {
		t.Errorf("reader: %+v", p)
	}
	for _, d := range []DatabaseConfig{{PostgresHost: "127.0.0.1:1", PostgresUsername: "cosmos"}} {
		if p := CheckPostgresStore(ctx, d); p == nil || p.Reason != "closed" {
			t.Errorf("%+v: %+v", d, p)
		}
	}
}

// The healing against a real PostgreSQL: down at the start (the local file),
// up later (the switch, told), down again (the pings reopen the local file).
// COSMOS_FORK_PG_TEST_DOWN is a host:port where nothing answers.
func TestFallbackHealsLive(t *testing.T) {
	host := os.Getenv("COSMOS_FORK_PG_TEST")
	if host == "" {
		t.Skip("COSMOS_FORK_PG_TEST not set")
	}
	db := DatabaseConfig{PostgresHost: host, PostgresDatabase: "cosmos", PostgresUsername: "cosmos", PostgresPassword: "cosmos-test-pw"}
	stub := withMonitoringStore(t, db, refusedProblem)
	InitMetricsDatabase()
	if s := GetMonitoringStoreState(); !s.Fallback || s.InUse != DialectSQLite {
		t.Fatalf("down: %+v", s)
	}
	w := &monitoringWatch{backoff: monitoringRetryFirst}
	w.look(true)
	stub.problem = nil
	probeMonitoringPostgres = CheckPostgresStore
	w.look(false)
	s := GetMonitoringStoreState()
	if s.Fallback || s.InUse != DialectPostgres || len(stub.back) != 1 || stub.back[0].LocalFrom.IsZero() {
		t.Fatalf("healed: %+v, told %+v", s, stub.back)
	}
	store, _ := MonitoringWriteDB()
	if err := store.QueryRow("SELECT count(*) FROM metric_values").Scan(new(int)); err != nil {
		t.Fatalf("tables in PostgreSQL: %v", err)
	}
}
