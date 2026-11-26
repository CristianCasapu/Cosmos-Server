package metrics

// Fork addition (plan 51, review round 3): a flush whose commit's answer was
// lost is neither counted twice nor lost.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The store's watcher is held for the test process: the tests change the
// configuration folder under it.
func init() { utils.PauseMonitoringStore() }

func dayTotal(t *testing.T, key string) int64 {
	t.Helper()
	db, _ := utils.MonitoringReadDB()
	var v int64
	db.QueryRow("SELECT COALESCE(SUM(value), 0) FROM metric_values WHERE key = ? AND granularity = 'day'", "cosmos."+key).Scan(&v)
	return v
}

// loseAnswers makes the next flush's commit "fail" after (committed) or
// without (not committed) writing it.
func loseAnswers(t *testing.T, committed bool) {
	t.Helper()
	prev := flushWrite
	t.Cleanup(func() { flushWrite = prev })
	flushWrite = func(list []DataPush, kv map[string]string) (*sql.DB, bool, error) {
		flushWrite = prev
		if committed {
			db, _, err := prev(list, kv)
			if err != nil {
				t.Fatal(err)
			}
			return db, true, errors.New("unexpected EOF")
		}
		db, _ := utils.MonitoringWriteDB()
		return db, true, errors.New("unexpected EOF")
	}
}

func TestAFlushWhoseAnswerWasLostIsNotCountedTwice(t *testing.T) {
	for _, committed := range []bool{true, false} {
		t.Run(map[bool]string{true: "committed", false: "not committed"}[committed], func(t *testing.T) {
			setupMetricsStore(t)
			uncertain = nil
			def := DataDef{Max: 100, Period: time.Hour, Label: "t", AggloType: "sum", SetOperation: "sum"}
			pushSetMetric("lost", 10, def)
			loseAnswers(t, committed)
			SaveMetrics() // its answer is lost
			pushSetMetric("lost", 5, def)
			SaveMetrics()
			if got := dayTotal(t, "lost"); got != 15 {
				t.Errorf("day total %d, pushed 15", got)
			}
			if len(uncertain) != 0 {
				t.Errorf("still uncertain: %d", len(uncertain))
			}
		})
	}
}

// Into another store (the fallback), the uncertain flush is carried along
// with the next flush, for the catch-up.
func TestAnUncertainFlushIsCarriedIntoTheOtherStore(t *testing.T) {
	setupMetricsStore(t)
	uncertain = nil
	def := DataDef{Max: 100, Period: time.Hour, Label: "t", AggloType: "sum", SetOperation: "sum"}
	pushSetMetric("carried", 10, def)
	loseAnswers(t, true)
	SaveMetrics()
	first, _ := utils.MonitoringWriteDB()
	firstID := utils.MonitoringLedgerIDOf(first)
	utils.CloseMetricsDatabase()
	utils.CONFIGFOLDER = t.TempDir() + "/"
	if err := utils.InitMetricsDatabase(); err != nil {
		t.Fatal(err)
	}
	pushSetMetric("carried", 5, def)
	SaveMetrics()
	db, _ := utils.MonitoringReadDB()
	var value string
	if err := db.QueryRow("SELECT value FROM monitoring_kv WHERE key LIKE ?", UncertainPrefix+"%").Scan(&value); err != nil {
		t.Fatalf("no note carried: %v", err)
	}
	var note UncertainFlush
	json.Unmarshal([]byte(value), &note)
	found := false
	for id, d := range note.Deltas {
		found = found || (d[0] == 10 && d[1] == 1 && len(id) > 0)
	}
	if note.Store != firstID || note.Token == "" || !found {
		t.Errorf("note %+v (first store %s)", note, firstID)
	}
	if got := dayTotal(t, "carried"); got != 15 {
		t.Errorf("the second store got %d (the change since the last flush known to be written: 15)", got)
	}
	if len(uncertain) != 0 {
		t.Errorf("not carried: %d", len(uncertain))
	}
}

// A flush that took the store's handle just before a switch closed it is
// written once more at once on the store in use, and reported as written.
func TestAFlushOnAClosedHandleIsWrittenAtOnce(t *testing.T) {
	setupMetricsStore(t)
	old, _ := utils.MonitoringWriteDB()
	utils.CloseMetricsDatabase()
	if err := utils.InitMetricsDatabase(); err != nil {
		t.Fatal(err)
	}
	prev := bucketsHandle
	t.Cleanup(func() { bucketsHandle = prev })
	first := true
	bucketsHandle = func() (*sql.DB, error) {
		if first {
			first = false
			return old, nil // closed by the switch
		}
		return prev()
	}
	def := DataDef{Max: 100, Period: time.Hour, Label: "t", AggloType: "sum", SetOperation: "sum"}
	pushSetMetric("closedhandle", 4, def)
	lock <- true
	dp := []DataPush{}
	for _, v := range dataBuffer {
		dp = append(dp, v)
	}
	<-lock
	if err := writeBuckets(dp); err != nil {
		t.Fatalf("reported as failed: %v", err)
	}
	if got := dayTotal(t, "closedhandle"); got != 4 {
		t.Errorf("day total %d", got)
	}
}

// A falling gauge (the last value, averaged) whose flush's answer was lost:
// counted once (round 4: the look-up kept the higher value written before).
func TestAFallingGaugeWhoseFlushAnswerWasLostIsCountedOnce(t *testing.T) {
	setupMetricsStore(t)
	uncertain = nil
	def := DataDef{Max: 1000, Period: time.Hour, Label: "t", AggloType: "avg", SetOperation: "min"}
	pushSetMetric("falling", 50, def)
	SaveMetrics() // 50 written
	pushSetMetric("falling", 30, def)
	loseAnswers(t, true)
	SaveMetrics() // 30 written, its answer lost
	pushSetMetric("falling", 20, def)
	SaveMetrics()
	if got := dayTotal(t, "falling"); got != 20 {
		t.Errorf("day total %d, the bucket's value 20", got)
	}
}
