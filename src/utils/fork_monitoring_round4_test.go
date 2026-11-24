package utils

// Fork addition (plan 51, review round 4): a record belongs to one server;
// the second mark is taken after the drain; the watcher reaches the store
// the settings name from a store of another server.

import (
	"database/sql"
	"strconv"
	"testing"
	"time"
)

// writeLocalEvent writes an event into the local file through a handle of
// its own (as a writer of the store does while the local file is the store,
// or a straggler of the drain); it returns its id.
func writeLocalEvent(t *testing.T, label string) int64 {
	t.Helper()
	var id int64
	if err := withLocalFile(false, func(local *sql.DB) error {
		res, err := local.Exec("INSERT INTO events(node, event_id, label, application, level, date, data, object, search) VALUES('n', ?, ?, '', 'info', ?, '', '', '')", label, label, time.Now().UnixMilli())
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// addLocalDay adds to the local file's day row of a summed metric (what the
// writer does during a fallback).
func addLocalDay(t *testing.T, day int64, v int64) {
	t.Helper()
	withLocalFile(false, func(local *sql.DB) error {
		_, err := local.Exec("INSERT INTO metric_values(node, key, granularity, date, value, avg_index, expire) VALUES('n', 'k', 'day', ?, ?, 1, ?) ON CONFLICT(node, key, granularity, date) DO UPDATE SET value = metric_values.value + excluded.value", day, v, day+40*86400000)
		if err != nil {
			t.Fatal(err)
		}
		return nil
	})
}

func ownPart(t *testing.T, store string, day int64) [2]int64 {
	t.Helper()
	var value string
	withLocalFile(false, func(local *sql.DB) error {
		local.QueryRow("SELECT value FROM monitoring_kv WHERE key = ?", OwnPrefix+store+":n|k|day|"+strconv.FormatInt(day, 10)).Scan(&value)
		return nil
	})
	p, _ := ParsePair(value)
	return p
}

// S1 of round 4, the reviewer's sequence: A does not answer; the owner saves
// B; B has an outage of its own and comes back; the owner saves A again.
// A's catch-up takes A's rows and A's part of the totals only, B's takes B's.
func TestAServerBackAfterAnotherTakesOnlyItsOwnRows(t *testing.T) {
	a := pgConfig
	b := DatabaseConfig{PostgresHost: "192.0.2.20:5432", PostgresUsername: "cosmos", PostgresPassword: "y"}
	stub := withMonitoringStore(t, a, nil)
	fakePostgres(t)
	windows := handedWindows(t)
	ping := pingSwitch()
	InitMetricsDatabase()
	aStore := MonitoringLedgerIDOf(monitoringStoreHandle())
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	day := stub.clock().UnixMilli() - stub.clock().UnixMilli()%86400000
	// A's outage: rows and 10 of a day total in the local file
	fallBack(t, stub, w, ping)
	aRows := []int64{writeLocalEvent(t, "A-out"), writeLocalEvent(t, "A-out")}
	addLocalDay(t, day, 10)
	// the owner saves B, which answers
	config := GetBaseMainConfig()
	config.Database = b
	LoadBaseMainConfig(config)
	stub.setProblem(nil)
	ping.Store(true)
	InitMetricsDatabase()
	waitCatchUps(t)
	bStore := MonitoringLedgerIDOf(monitoringStoreHandle())
	if aStore == "" || bStore == "" || aStore == bStore {
		t.Fatalf("the two servers' identities: %q %q", aStore, bStore)
	}
	// B's own outage, and its way back
	fallBack(t, stub, w, ping)
	bRows := []int64{writeLocalEvent(t, "B-out"), writeLocalEvent(t, "B-out"), writeLocalEvent(t, "B-out")}
	addLocalDay(t, day, 7)
	comeBack(t, stub, w, ping)
	// the owner saves A again
	config.Database = a
	LoadBaseMainConfig(config)
	InitMetricsDatabase()
	waitCatchUps(t)
	got := windows()
	byStore := map[string][]CatchUpWindow{}
	for _, win := range got {
		byStore[win.Store] = append(byStore[win.Store], win)
	}
	if len(byStore[aStore]) != 1 || len(byStore[bStore]) != 1 {
		t.Fatalf("windows: %d of A, %d of B (%+v)", len(byStore[aStore]), len(byStore[bStore]), got)
	}
	in := func(win CatchUpWindow, id int64) bool { return id > win.After["events"] && id <= win.Before["events"] }
	for _, id := range aRows {
		if !in(byStore[aStore][0], id) || in(byStore[bStore][0], id) {
			t.Errorf("A's event %d: in A's window %v, in B's %v", id, in(byStore[aStore][0], id), in(byStore[bStore][0], id))
		}
	}
	for _, id := range bRows {
		if in(byStore[aStore][0], id) || !in(byStore[bStore][0], id) {
			t.Errorf("B's event %d: in A's window %v, in B's %v", id, in(byStore[aStore][0], id), in(byStore[bStore][0], id))
		}
	}
	if !byStore[aStore][0].To.Before(byStore[bStore][0].From.Add(fallbackLead() + time.Second)) {
		t.Errorf("A's window reaches into B's time: A to %v, B from %v", byStore[aStore][0].To, byStore[bStore][0].From)
	}
	if pa, pb := ownPart(t, aStore, day), ownPart(t, bStore, day); pa[0] != 10 || pb[0] != 7 {
		t.Errorf("the parts of the day total: A %v, B %v (want 10 and 7)", pa, pb)
	}
}

// M2 of round 4: the window's end marks are taken after the switch's drain:
// a row a writer that took the local file's handle before the switch puts
// there during the drain is in the window by its id.
func TestTheEndMarksAreTakenAfterTheDrain(t *testing.T) {
	stub := withMonitoringStore(t, pgConfig, nil)
	fakePostgres(t)
	windows := handedWindows(t)
	ping := pingSwitch()
	InitMetricsDatabase()
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	fallBack(t, stub, w, ping)
	ping.Store(true)
	stub.setProblem(nil)
	for i := 0; i < 400 && MonitoringDialect() != DialectPostgres; i++ {
		stub.advance(w.look(false))
	}
	straggler := writeLocalEvent(t, "drain") // within the drain, before the marks
	waitCatchUps(t)
	got := windows()
	if len(got) != 1 || straggler > got[0].Before["events"] || straggler <= got[0].After["events"] {
		t.Fatalf("the row written during the drain (id %d) is not in the window: %+v", straggler, got)
	}
}

// M4 of round 4, the 19th state: PostgreSQL server A in use, no fallback,
// the settings name B (changed without an opening): the watcher opens B.
func TestTheWatcherLeavesAServerTheSettingsNoLongerName(t *testing.T) {
	b := DatabaseConfig{PostgresHost: "192.0.2.20:5432", PostgresUsername: "cosmos", PostgresPassword: "y"}
	stub := withMonitoringStore(t, pgConfig, nil)
	fakePostgres(t)
	handedWindows(t)
	pingSwitch()
	InitMetricsDatabase()
	config := GetBaseMainConfig()
	config.Database = b
	LoadBaseMainConfig(config) // no opening follows
	w := &monitoringWatch{backoff: storeTiming.retryFirst}
	for i := 0; i < 5 && !storeOpenAs(postgresStoreKey(b)); i++ {
		stub.advance(w.look(false))
	}
	if !storeOpenAs(postgresStoreKey(b)) {
		t.Fatalf("still on %+v", GetMonitoringStoreState())
	}
	waitCatchUps(t)
}

// M7 of round 4: a PostgreSQL store is named by the identity row in its own
// monitoring_kv: the same database reached at another address keeps its
// name (its ledgers), and a fallback of that address uses it.
func TestTheSameDatabaseAtAnotherAddressKeepsItsLedger(t *testing.T) {
	withMonitoringStore(t, pgConfig, nil)
	fakePostgres(t)
	InitMetricsDatabase()
	w := monitoringStoreHandle()
	first := MonitoringLedgerIDOf(w)
	moved := pgConfig
	moved.PostgresHost = "192.0.2.77:5432" // the container got another address
	if again := storeIdentity(w, moved); again != first || first == "" || first == LedgerID(DialectPostgres, addressOf(pgConfig)) {
		t.Fatalf("named %q at the first address, %q at the other", first, again)
	}
	identities.Range(func(k, _ interface{}) bool { identities.Delete(k); return true }) // a restart: read from the local file
	if got := postgresLedgerID(moved); got != first {
		t.Errorf("a fallback at the new address would use %q, not %q", got, first)
	}
}
