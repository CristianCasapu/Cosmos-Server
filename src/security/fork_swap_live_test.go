package security

// Fork addition (plan 51, review round 2, S2): the real security-record
// writer through 200 switches of the monitoring store between PostgreSQL and
// the local file while PostgreSQL answers: no error, no entry lost. Needs
// COSMOS_FORK_PG_TEST, see src/utils/fork_monitoring_store_test.go.

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestSwapsLoseNoSecurityEntryLive(t *testing.T) {
	host := os.Getenv("COSMOS_FORK_PG_TEST")
	if host == "" {
		t.Skip("COSMOS_FORK_PG_TEST not set")
	}
	or := func(name, fallback string) string {
		if v := os.Getenv(name); v != "" {
			return v
		}
		return fallback
	}
	pg := utils.DatabaseConfig{PostgresHost: host, PostgresDatabase: or("COSMOS_FORK_PG_TEST_DB", "cosmos"),
		PostgresUsername: or("COSMOS_FORK_PG_TEST_USER", "cosmos"), PostgresPassword: or("COSMOS_FORK_PG_TEST_PASSWORD", "cosmos-test-pw")}
	useFolder(t)
	prevConfig := utils.GetBaseMainConfig()
	t.Cleanup(func() { utils.CloseMetricsDatabase(); utils.LoadBaseMainConfig(prevConfig) })
	set := func(db utils.DatabaseConfig) {
		config := utils.GetBaseMainConfig()
		config.Database = db
		utils.LoadBaseMainConfig(config)
		if err := utils.InitMetricsDatabase(); err != nil {
			t.Errorf("switch: %v", err)
		}
	}
	set(pg)
	run := fmt.Sprintf("swap%d", time.Now().UnixNano())
	var written atomic.Int64
	var failures sync.Map
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; ; j++ {
				select {
				case <-stop:
					return
				default:
				}
				err := insert([]Event{{Time: time.Now(), Kind: utils.SecLoginFailed, Class: string(ClassIncident), Severity: "low", Target: run, IP: fmt.Sprintf("192.0.2.%d", j%250), Count: 1}})
				if err != nil {
					msg := err.Error()
					if len(msg) > 100 {
						msg = msg[:100]
					}
					n, _ := failures.LoadOrStore(msg, new(atomic.Int64))
					n.(*atomic.Int64).Add(1)
					continue
				}
				written.Add(1)
			}
		}(i)
	}
	for swap := 0; swap < 200; swap++ {
		if swap%2 == 0 {
			set(utils.DatabaseConfig{})
		} else {
			set(pg)
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
	time.Sleep(3 * time.Second)
	report := []string{}
	failures.Range(func(k, v interface{}) bool {
		report = append(report, fmt.Sprintf("%s ×%d", k, v.(*atomic.Int64).Load()))
		return true
	})
	found := 0
	for _, dsn := range []struct{ driver, dsn, q string }{
		{utils.DialectSQLite, "file:" + utils.CONFIGFOLDER + "database.db?mode=ro", "SELECT COUNT(*) FROM " + tableName + " WHERE target = ?"},
		{"pgx", (&url.URL{Scheme: "postgres", User: url.UserPassword(pg.PostgresUsername, pg.PostgresPassword), Host: pg.PostgresHost, Path: "/" + pg.PostgresDatabase, RawQuery: "sslmode=disable"}).String(),
			"SELECT COUNT(*) FROM " + tableName + " WHERE target = $1"},
	} {
		db, err := sql.Open(dsn.driver, dsn.dsn)
		if err != nil {
			t.Fatal(err)
		}
		var n int
		if err := db.QueryRow(dsn.q, run).Scan(&n); err != nil {
			t.Fatal(err)
		}
		db.Close()
		found += n
	}
	t.Logf("entries written %d, found %d; errors %v", written.Load(), found, report)
	if len(report) > 0 || int64(found) != written.Load() {
		t.Errorf("errors %s; %d of %d", strings.Join(report, "; "), found, written.Load())
	}
}
