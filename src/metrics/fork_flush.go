package metrics

// Fork addition (plan 51, review round 3, S2): a flush whose commit's answer
// was lost.
//
// A flush adds each open raw bucket's change since its last successful flush
// to the hour and day totals. When the store does not answer the commit (the
// database stopped under it), the commit may have happened or not: the flush
// counts as failed and the next one adds the same change again. Into the same
// store that counts it twice; into the other store (the local file during a
// fallback) the catch-up then adds it again to PostgreSQL's total.
//
// Every flush writes a token in its own transaction (a bookkeeping key of the
// copy, never copied). A flush whose commit failed is kept, with the change
// it added to each hour and day total, as uncertain:
//   - the next flush into the same store looks the token up first: there, the
//     flush happened (its buckets are marked written); not there, it did not;
//   - a flush into another store carries the uncertain flush along, in its own
//     transaction (the catch-up from that store reads it: when the token is in
//     PostgreSQL, PostgreSQL holds that change already, and it counts it as
//     absorbed from the local file, so it is not added twice, see
//     src/monitorcopy/buckets.go).
// The tokens are removed after two days, the carried notes after forty.

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	flushTokenPrefix = "fork_monitoring_copy:flush:"
	// UncertainPrefix starts the notes of uncertain flushes carried into
	// another store (read by src/monitorcopy)
	UncertainPrefix = "fork_monitoring_copy:uncertain:"
	maxUncertain    = 50
)

// UncertainFlush is the note of an uncertain flush, as carried.
type UncertainFlush struct {
	Store  string              `json:"store"`  // the store the commit went to (utils.LedgerID)
	Token  string              `json:"token"`  // its token there
	Deltas map[string][2]int64 `json:"deltas"` // bucket "node|key|granularity|date" -> value, avg_index added
}

type uncertainFlush struct {
	UncertainFlush
	keys   []string // the buckets flushed
	values []int    // their values then
}

var (
	uncertain      []uncertainFlush // under lock
	lastTokenSweep time.Time
	// flushWrite is writeBucketsTx; the tests make a commit's answer get lost
	flushWrite = writeBucketsTx
)

func newFlushToken() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// padded is a time as text that sorts as the time does.
func padded(t time.Time) string { return fmt.Sprintf("%013d", t.UnixMilli()) }

// deltasOf is what a flush adds to the hour and day totals.
func deltasOf(buckets []DataPush) map[string][2]int64 {
	node := utils.MonitoringNode()
	out := map[string][2]int64{}
	for _, dp := range buckets {
		value := int64(0)
		if dp.AggloType == "sum" || dp.AggloType == "avg" {
			value = int64(dp.Value - dp.Written)
		}
		count := int64(0)
		if !dp.Contributed {
			count = 1
		}
		for _, pool := range []struct {
			granularity string
			period      time.Duration
		}{{granularityHour, time.Hour}, {granularityDay, 24 * time.Hour}} {
			id := node + "|" + dp.Key + "|" + pool.granularity + "|" + strconv.FormatInt(utils.TimeToMillis(ModuloTime(dp.Date, pool.period)), 10)
			d := out[id]
			out[id] = [2]int64{d[0] + value, d[1] + count}
		}
	}
	return out
}

// tokenIn: the token is in that store (false when it cannot be read).
func tokenIn(db *sql.DB, token string) (bool, error) {
	var one string
	err := db.QueryRow(utils.MonitoringRebindFor(db, "SELECT value FROM monitoring_kv WHERE key = ?"), flushTokenPrefix+token).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// flushBuckets is SaveMetrics' write; lock held.
func flushBuckets(flushKeys []string, buckets []DataPush) error {
	db, err := utils.MonitoringWriteDB()
	if err != nil {
		return err
	}
	here := utils.MonitoringLedgerIDOf(db)
	// uncertain flushes of this store: looked up
	kept := uncertain[:0]
	for _, u := range uncertain {
		if u.Store != here || here == "" {
			kept = append(kept, u)
			continue
		}
		found, err := tokenIn(db, u.Token)
		if err != nil {
			kept = append(kept, u)
			continue
		}
		if found { // it was written: its buckets are as written then
			for i, key := range u.keys {
				if dp, ok := dataBuffer[key]; ok && !(dp.Contributed && dp.Written >= u.values[i]) {
					dp.Written, dp.Contributed = u.values[i], true
					dataBuffer[key] = dp
				}
			}
		}
	}
	uncertain = kept
	// recompute what is left to flush after the look-ups
	keys, list := []string{}, []DataPush{}
	for i, key := range flushKeys {
		dp := dataBuffer[key]
		if dp.Contributed && dp.Value == dp.Written {
			continue
		}
		keys, list = append(keys, key), append(list, dp)
		buckets[i] = dp
	}
	now := time.Now()
	token := newFlushToken()
	kv := map[string]string{flushTokenPrefix + token: padded(now)}
	carried := map[string]bool{}
	for _, u := range uncertain {
		if u.Store != here {
			note, _ := json.Marshal(u.UncertainFlush)
			kv[UncertainPrefix+padded(now)+":"+u.Token] = string(note)
			carried[u.Token] = true
		}
	}
	_, committing, err := flushWrite(list, kv)
	if err == nil {
		for i, key := range keys {
			dp := dataBuffer[key]
			dp.Written, dp.Contributed = list[i].Value, true
			dataBuffer[key] = dp
		}
		left := uncertain[:0]
		for _, u := range uncertain {
			if !carried[u.Token] {
				left = append(left, u)
			}
		}
		uncertain = left
		sweepTokens(db, now)
		return nil
	}
	if committing && here != "" {
		values := make([]int, len(list))
		for i, dp := range list {
			values[i] = dp.Value
		}
		u := uncertainFlush{UncertainFlush: UncertainFlush{Store: here, Token: token, Deltas: deltasOf(list)}, keys: keys, values: values}
		uncertain = append(uncertain, u)
		if len(uncertain) > maxUncertain {
			uncertain = uncertain[len(uncertain)-maxUncertain:]
		}
	}
	return err
}

// sweepTokens removes old tokens and notes from the store in use, hourly.
func sweepTokens(db *sql.DB, now time.Time) {
	if now.Sub(lastTokenSweep) < time.Hour {
		return
	}
	lastTokenSweep = now
	db.Exec(utils.MonitoringRebindFor(db, "DELETE FROM monitoring_kv WHERE key >= ? AND key < ? AND value < ?"),
		flushTokenPrefix, flushTokenPrefix+"~", padded(now.Add(-48*time.Hour)))
	db.Exec(utils.MonitoringRebindFor(db, "DELETE FROM monitoring_kv WHERE key >= ? AND key < ?"),
		UncertainPrefix, UncertainPrefix+padded(now.Add(-40*24*time.Hour)))
	utils.PruneBucketKeys(db, utils.MonitoringDialectOf(db), utils.PriorPrefix, now, 50000, 5000)
}

// notePrior: before a flush of dp into the hour or day row of poolDate, when
// the raw bucket was flushed before (into another store: this one has no such
// row) the row about to be made gets the whole bucket; the part flushed
// elsewhere (its value for sum and avg, its count) is noted in this store's
// monitoring_kv in the same transaction (utils.PriorPrefix), and the copy
// subtracts it when it merges the stores (review round 4, M5).
func notePrior(tx *sql.Tx, dialect, node, granularity string, poolDate time.Time, dp DataPush) error {
	if !dp.Contributed {
		return nil
	}
	date := utils.TimeToMillis(poolDate)
	var one int
	err := tx.QueryRow(utils.RebindFor(dialect, "SELECT 1 FROM metric_values WHERE node = ? AND key = ? AND granularity = ? AND date = ?"),
		node, dp.Key, granularity, date).Scan(&one)
	if err == nil {
		return nil // the row is there: the change is added to it
	}
	if err != sql.ErrNoRows {
		return err
	}
	value := int64(0)
	if dp.AggloType == "sum" || dp.AggloType == "avg" {
		value = int64(dp.Written)
	}
	key := utils.PriorPrefix + node + "|" + dp.Key + "|" + granularity + "|" + strconv.FormatInt(date, 10)
	var text string
	tx.QueryRow(utils.RebindFor(dialect, "SELECT value FROM monitoring_kv WHERE key = ?"), key).Scan(&text)
	had, _ := utils.ParsePair(text)
	_, err = tx.Exec(utils.RebindFor(dialect, "INSERT INTO monitoring_kv(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value"),
		key, utils.FormatPair([2]int64{had[0] + value, had[1] + 1}))
	return err
}
