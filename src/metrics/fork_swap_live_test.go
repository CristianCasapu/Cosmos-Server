package metrics

// Fork addition (plan 51, review round 2, S2): the real metrics writer
// through 200 switches of the monitoring store between PostgreSQL and the
// local file while PostgreSQL answers: no error, no bucket lost. Needs
// COSMOS_FORK_PG_TEST (host:port) and _DB/_USER/_PASSWORD (default
// cosmos/cosmos/cosmos-test-pw), see src/utils/fork_monitoring_store_test.go.

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

func livePG(t *testing.T) utils.DatabaseConfig {
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
	return utils.DatabaseConfig{PostgresHost: host, PostgresDatabase: or("COSMOS_FORK_PG_TEST_DB", "cosmos"),
		PostgresUsername: or("COSMOS_FORK_PG_TEST_USER", "cosmos"), PostgresPassword: or("COSMOS_FORK_PG_TEST_PASSWORD", "cosmos-test-pw")}
}

func TestSwapsLoseNoBucketLive(t *testing.T) {
	pg := livePG(t)
	prevFolder, prevConfig := utils.CONFIGFOLDER, utils.GetBaseMainConfig()
	utils.CONFIGFOLDER = t.TempDir() + "/"
	t.Cleanup(func() {
		utils.CloseMetricsDatabase()
		utils.CONFIGFOLDER = prevFolder
		utils.LoadBaseMainConfig(prevConfig)
	})
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
				key := fmt.Sprintf("%s.%d.%d", run, i, j)
				err := writeBuckets([]DataPush{{Date: time.Now(), Key: key, Value: 1, AggloType: "sum", Period: time.Minute, Label: "x"}})
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
		{utils.DialectSQLite, "file:" + utils.CONFIGFOLDER + "database.db?mode=ro", "SELECT COUNT(DISTINCT key) FROM metrics WHERE key LIKE ?"},
		{"pgx", (&url.URL{Scheme: "postgres", User: url.UserPassword(pg.PostgresUsername, pg.PostgresPassword), Host: pg.PostgresHost, Path: "/" + pg.PostgresDatabase, RawQuery: "sslmode=disable"}).String(),
			"SELECT COUNT(DISTINCT key) FROM metrics WHERE key LIKE $1"},
	} {
		db, err := sql.Open(dsn.driver, dsn.dsn)
		if err != nil {
			t.Fatal(err)
		}
		var n int
		if err := db.QueryRow(dsn.q, run+".%").Scan(&n); err != nil {
			t.Fatal(err)
		}
		db.Close()
		found += n
	}
	t.Logf("buckets written %d, found %d; errors %v", written.Load(), found, report)
	if len(report) > 0 || int64(found) != written.Load() {
		t.Errorf("errors %s; %d of %d", strings.Join(report, "; "), found, written.Load())
	}
}
