package monitorcopy

// Metric buckets both stores hold (plan 51, review rounds 3 and 4).
//
// A metric's hour and day rows are built on write: each flush of a raw bucket
// adds its change since the last flush (sum and avg: value and the count of
// raw buckets; max and min: the larger or smaller value). When the store
// changes, the first flush of a raw bucket into the other store meets no row
// of its hour or day there: the row gets the whole raw bucket (so that it
// reads right on its own, averages included) and that store notes the part
// that went elsewhere (prior:<bucket>, src/metrics/fork_flush.go).
//
// A store's own part of its row is the row less that note. Each store keeps
// a ledger in its monitoring_kv (keys of the copy's bookkeeping prefix, never
// copied) of how much of the other store's row it has absorbed
// (absorbed:<other>:<bucket>, value and count), written in the transaction of
// the addition:
//   - a manual copy from X into Y adds X.row − prior − (what X absorbed from Y)
//     − (what Y absorbed from X): everything of X's own not yet in Y, once;
//   - the catch-up of a fallback of server Y adds Y's part of the local file's
//     totals (own:<Y>:<bucket>, the change of the local file's own totals
//     during Y's fallbacks, kept by src/utils/fork_monitoring_fallback.go) less
//     what Y absorbed of it before (absorbedown:<local>:<bucket>): the local
//     file's rows are shared by the outages of every server and by its own
//     periods, and a server gets its own part only (review round 4, S1). A
//     manual copy into Y counts Y's part as absorbed too.
// A negative change of value (a falling gauge) is merged like a positive one;
// only a negative count is refused (review round 4, S2). sum and avg add;
// max and min take the larger or smaller value (and add the count); the last
// value is taken from the store with the later flush in a catch-up, and is
// not merged by a manual copy (said as not merged exactly).
//
// Raw rows (the last hour's fine points) are replaced on each flush: in both
// stores, the catch-up takes the local file's when the raw bucket ended
// before PostgreSQL became the store again (its later flushes went there),
// PostgreSQL's otherwise; a manual copy keeps the rule it had (the store in
// use wins towards PostgreSQL, the source towards the local file).

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// metricInfo is what a merge needs to know of a metric.
type metricInfo struct {
	agg    string
	period int64 // ms, from time_scale (the period in 30 s)
}

// loadMetricInfo reads each metric's kind of total and period from the
// source's catalogue, the target's for metrics the source does not describe.
func (c *copier) loadMetricInfo(ctx context.Context) (map[string]metricInfo, error) {
	out := map[string]metricInfo{}
	for _, s := range []Store{c.dst, c.src} { // the source's last: it wins
		exists, err := s.tables(ctx)
		if err != nil {
			return nil, err
		}
		if !exists["metrics"] {
			continue
		}
		rows, err := s.DB.QueryContext(ctx, "SELECT node, key, agglo_type, time_scale FROM metrics")
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var node, key, agg string
			var scale float64
			if err := rows.Scan(&node, &key, &agg, &scale); err != nil {
				rows.Close()
				return nil, err
			}
			period := int64(scale * 30000)
			if period <= 0 {
				period = 30000
			}
			out[node+"|"+key] = metricInfo{agg: agg, period: period}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// bucket is one row of metric_values.
type bucket struct {
	node, key, granularity string
	date, value, index     int64
	expire                 int64
}

func bucketOf(r rule, one row) bucket {
	v := one.values
	return bucket{node: v[r.index("node")].(string), key: v[r.index("key")].(string), granularity: v[r.index("granularity")].(string),
		date: v[r.index("date")].(int64), value: v[r.index("value")].(int64), index: v[r.index("avg_index")].(int64),
		expire: v[r.index("expire")].(int64)}
}

func (b bucket) id() string {
	return b.node + "|" + b.key + "|" + b.granularity + "|" + strconv.FormatInt(b.date, 10)
}

func (b bucket) pooled() bool { return b.granularity == "hour" || b.granularity == "day" }

// periodEnd: when the bucket stops changing (its period over).
func (c *copier) periodEnd(b bucket) int64 {
	switch b.granularity {
	case "hour":
		return b.date + int64(time.Hour/time.Millisecond)
	case "day":
		return b.date + int64(24*time.Hour/time.Millisecond)
	}
	period := int64(30000)
	if m, ok := c.metrics[b.node+"|"+b.key]; ok {
		period = m.period
	}
	return b.date + period
}

const (
	ledgerPrefix    = markerPrefix + "absorbed:"
	ownLedgerPrefix = markerPrefix + "absorbedown:"
)

func ledgerKey(other string, b bucket) string { return ledgerPrefix + other + ":" + b.id() }

func ownLedgerKey(other string, b bucket) string { return ownLedgerPrefix + other + ":" + b.id() }

type tally = [2]int64

func add(a, b tally) tally { return tally{a[0] + b[0], a[1] + b[1]} }
func sub(a, b tally) tally { return tally{a[0] - b[0], a[1] - b[1]} }

// pairsOf reads the pairs a store keeps under prefix+bucket id, for the
// buckets given (by bucket id).
func pairsOf(ctx context.Context, s Store, q rowsQueryer, prefix string, buckets []bucket) (map[string]tally, error) {
	out := map[string]tally{}
	for start := 0; start < len(buckets); start += 500 {
		end := min(start+500, len(buckets))
		keys := []interface{}{}
		for _, b := range buckets[start:end] {
			if b.pooled() {
				keys = append(keys, prefix+b.id())
			}
		}
		if len(keys) == 0 {
			continue
		}
		rows, err := q.QueryContext(ctx, s.bind("SELECT key, value FROM monitoring_kv WHERE key IN ("+placeholders(len(keys))+")"), keys...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key, value string
			if err := rows.Scan(&key, &value); err != nil {
				rows.Close()
				return nil, err
			}
			if p, ok := utils.ParsePair(value); ok {
				out[strings.TrimPrefix(key, prefix)] = p
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// bucketLedgers is what a merge and its check know of a batch.
type bucketLedgers struct {
	prior, gave, own tally // the source's: noted at a change, absorbed from the target, the target server's part
	got, gotOwn      tally // the target's: absorbed from the source, of its own part
}

// ledgersOf reads them; dst is the target's transaction (or handle).
func (c *copier) ledgersOf(ctx context.Context, q rowsQueryer, buckets []bucket) (map[string]bucketLedgers, error) {
	prior, err := pairsOf(ctx, c.src, c.src.DB, utils.PriorPrefix, buckets)
	if err != nil {
		return nil, err
	}
	gave, err := pairsOf(ctx, c.src, c.src.DB, ledgerPrefix+c.dst.id()+":", buckets)
	if err != nil {
		return nil, err
	}
	server := c.opt.Store
	if server == "" {
		server = c.dst.id()
	}
	own, err := pairsOf(ctx, c.src, c.src.DB, utils.OwnPrefix+server+":", buckets)
	if err != nil {
		return nil, err
	}
	got, err := pairsOf(ctx, c.dst, q, ledgerPrefix+c.src.id()+":", buckets)
	if err != nil {
		return nil, err
	}
	gotOwn, err := pairsOf(ctx, c.dst, q, ownLedgerPrefix+c.src.id()+":", buckets)
	if err != nil {
		return nil, err
	}
	out := map[string]bucketLedgers{}
	for _, b := range buckets {
		id := b.id()
		out[id] = bucketLedgers{prior: prior[id], gave: gave[id], own: own[id], got: got[id], gotOwn: gotOwn[id]}
	}
	return out, nil
}

// catchUp: this copy is the catch-up of a fallback (its server's part only).
func (c *copier) catchUp() bool { return c.opt.From > 0 && c.opt.Store != "" }

// change is what the merge adds of bucket b.
func (c *copier) change(b bucket, l bucketLedgers) tally {
	if c.catchUp() {
		return sub(l.own, l.gotOwn)
	}
	return sub(sub(sub(tally{b.value, b.index}, l.prior), l.gave), l.got)
}

// The notes of uncertain flushes the metrics writer carried into the source
// (src/metrics/fork_flush.go): a flush into the target whose commit's answer
// was lost, and whose change the source got again.
const (
	uncertainPrefix = markerPrefix + "uncertain:"
	uncertainDone   = markerPrefix + "uncertain-done:"
	flushToken      = markerPrefix + "flush:"
)

type uncertainNote struct {
	Store  string              `json:"store"`
	Token  string              `json:"token"`
	Deltas map[string][2]int64 `json:"deltas"`
}

// absorbUncertain: for each such flush whose token the target holds (the
// commit did happen), the target holds that change already: it is counted
// as absorbed from the source (and of its own part, which holds it too), so
// that the merge does not add it again. Once per flush (a done key, in the
// same transaction).
func (c *copier) absorbUncertain(ctx context.Context) error {
	rows, err := c.src.DB.QueryContext(ctx, c.src.bind("SELECT value FROM monitoring_kv WHERE key >= ? AND key < ?"), uncertainPrefix, uncertainPrefix+"~")
	if err != nil {
		return err
	}
	notes := []uncertainNote{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			rows.Close()
			return err
		}
		var n uncertainNote
		if json.Unmarshal([]byte(value), &n) == nil && n.Store == c.dst.id() && n.Token != "" {
			notes = append(notes, n)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, n := range notes {
		tx, err := c.dst.begin(ctx)
		if err != nil {
			return err
		}
		_, done, err := c.dst.getKV(ctx, tx, uncertainDone+n.Token)
		if err == nil && !done {
			var written bool
			if _, written, err = c.dst.getKV(ctx, tx, flushToken+n.Token); err == nil && written {
			deltas:
				for id, d := range n.Deltas {
					for _, key := range []string{ledgerPrefix + c.src.id() + ":" + id, ownLedgerPrefix + c.src.id() + ":" + id} {
						value, _, e := c.dst.getKV(ctx, tx, key)
						if e != nil {
							err = e
							break deltas
						}
						had, _ := utils.ParsePair(value)
						if err = c.dst.setKV(ctx, tx, key, utils.FormatPair(add(had, d))); err != nil {
							break deltas
						}
					}
				}
			}
			if err == nil {
				err = c.dst.setKV(ctx, tx, uncertainDone+n.Token, "1")
			}
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// targetBuckets reads the target's rows of the batch's keys.
func (c *copier) targetBuckets(ctx context.Context, q rowsQueryer, r rule, batch []bucket) (map[string]bucket, error) {
	out := map[string]bucket{}
	for start := 0; start < len(batch); start += 400 {
		end := min(start+400, len(batch))
		args := []interface{}{}
		holes := []string{}
		for _, b := range batch[start:end] {
			args = append(args, b.node, b.key, b.granularity, b.date)
			holes = append(holes, "(?, ?, ?, CAST(? AS BIGINT))")
		}
		rows, err := q.QueryContext(ctx, c.dst.bind("SELECT "+strings.Join(r.columnNames(), ", ")+" FROM metric_values WHERE (node, key, granularity, date) IN (VALUES "+strings.Join(holes, ", ")+")"), args...)
		if err != nil {
			return nil, err
		}
		have, err := scanRows(rows, r, false, false)
		if err != nil {
			return nil, err
		}
		for _, one := range have {
			b := bucketOf(r, one)
			out[b.id()] = b
		}
	}
	return out, nil
}

// rawSourceWins: which store's raw row is the later one.
func (c *copier) rawSourceWins(b bucket) bool {
	if c.opt.From > 0 && c.opt.Back > 0 {
		return c.periodEnd(b) <= c.opt.Back
	}
	return !c.opt.TargetInUse
}

// mergeKind: how a bucket both stores hold is merged.
func (c *copier) mergeKind(b bucket) string {
	switch c.metrics[b.node+"|"+b.key].agg {
	case "sum", "avg":
		return "add"
	case "max":
		return "max"
	case "min":
		return "min"
	}
	// the last value flushed: mergeable by when the bucket ended, in a
	// catch-up (it knows when PostgreSQL came back)
	if c.opt.From > 0 && c.opt.Back > 0 {
		return "last"
	}
	return "none"
}

// batchBuckets copies the next batch of metric_values (see the top of the
// file), the ledgers and the table's marker in one transaction of the target.
func (c *copier) batchBuckets(ctx context.Context, r rule, m *tableMarker) (int64, int64, error) {
	batch, err := c.readNatural(ctx, r, m.Key, "", nil)
	if err != nil || len(batch) == 0 {
		return 0, 0, err
	}
	buckets := make([]bucket, len(batch))
	for i, one := range batch {
		buckets[i] = bucketOf(r, one)
	}
	tx, err := c.dst.begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	ledgers, err := c.ledgersOf(ctx, tx, buckets)
	if err != nil {
		return 0, 0, err
	}
	have, err := c.targetBuckets(ctx, tx, r, buckets)
	if err != nil {
		return 0, 0, err
	}
	greatest, least := "MAX", "MIN"
	if c.dst.postgres() {
		greatest, least = "GREATEST", "LEAST"
	}
	insert := "INSERT INTO metric_values(node, key, granularity, date, value, avg_index, expire) VALUES(?, ?, ?, ?, ?, ?, ?) ON CONFLICT(node, key, granularity, date) "
	written := int64(0)
	exec := func(query string, args ...interface{}) error {
		res, err := tx.ExecContext(ctx, c.dst.bind(query), args...)
		if err == nil {
			if n, e := res.RowsAffected(); e == nil {
				written += n
			}
		}
		return err
	}
	for _, b := range buckets {
		there, exists := have[b.id()]
		if !b.pooled() {
			if c.catchUp() && c.periodEnd(b) <= c.opt.Since {
				continue // ended before this fallback: not its last flush
			}
			if c.rawSourceWins(b) {
				err = exec(insert+"DO UPDATE SET value = excluded.value, avg_index = excluded.avg_index, expire = excluded.expire"+
					" WHERE metric_values.value <> excluded.value OR metric_values.avg_index <> excluded.avg_index OR metric_values.expire <> excluded.expire",
					b.node, b.key, b.granularity, b.date, b.value, b.index, b.expire)
			} else {
				err = exec(insert+"DO NOTHING", b.node, b.key, b.granularity, b.date, b.value, b.index, b.expire)
			}
			if err != nil {
				return 0, 0, err
			}
			continue
		}
		l := ledgers[b.id()]
		d := c.change(b, l)
		kind := c.mergeKind(b)
		if !exists {
			kind = "add" // the target has nothing of this bucket: the change is all of it
		}
		if d[1] < 0 || d == (tally{}) && (kind == "add" || exists) {
			continue // nothing new (a negative count is refused: said by the check)
		}
		switch kind {
		case "add":
			err = exec(insert+"DO UPDATE SET value = metric_values.value + excluded.value, avg_index = metric_values.avg_index + excluded.avg_index, expire = "+greatest+"(metric_values.expire, excluded.expire)",
				b.node, b.key, b.granularity, b.date, d[0], d[1], b.expire)
		case "max", "min":
			fn := greatest
			if kind == "min" {
				fn = least
			}
			err = exec(insert+"DO UPDATE SET value = "+fn+"(metric_values.value, excluded.value), avg_index = metric_values.avg_index + excluded.avg_index, expire = "+greatest+"(metric_values.expire, excluded.expire)",
				b.node, b.key, b.granularity, b.date, b.value, d[1], b.expire)
		case "last":
			value := "metric_values.value"
			if c.periodEnd(b) <= c.opt.Back {
				value = "excluded.value"
			}
			err = exec(insert+"DO UPDATE SET value = "+value+", avg_index = metric_values.avg_index + excluded.avg_index, expire = "+greatest+"(metric_values.expire, excluded.expire)",
				b.node, b.key, b.granularity, b.date, b.value, d[1], b.expire)
		default:
			continue // not mergeable: the target's row stays (counted by the check)
		}
		if err != nil {
			return 0, 0, err
		}
		_ = there
		if err := c.dst.setKV(ctx, tx, ledgerKey(c.src.id(), b), utils.FormatPair(add(l.got, d))); err != nil {
			return 0, 0, err
		}
		gotOwn := add(l.gotOwn, d)
		if !c.catchUp() {
			gotOwn = l.own // a manual copy took everything: the target's own part too
		}
		if err := c.dst.setKV(ctx, tx, ownLedgerKey(c.src.id(), b), utils.FormatPair(gotOwn)); err != nil {
			return 0, 0, err
		}
	}
	next := *m
	next.Key = encodeKey(r, batch[len(batch)-1].values)
	next.Written += written
	encoded, _ := json.Marshal(next)
	if err := c.dst.setKV(ctx, tx, tableKey(c.opt.SourceID, r.table), string(encoded)); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	*m = next
	return int64(len(batch)), written, nil
}

// checkBuckets is the verification of a batch of metric_values: values, not
// keys. It returns the rows present (with their value), missing, and not
// mergeable exactly.
func (c *copier) checkBuckets(ctx context.Context, r rule, batch []row) (present, missing, inexact int64, err error) {
	buckets := make([]bucket, len(batch))
	for i, one := range batch {
		buckets[i] = bucketOf(r, one)
	}
	var ledgers map[string]bucketLedgers
	var have map[string]bucket
	err = c.dst.within(ctx, func(q dbtx) error {
		var err error
		if ledgers, err = c.ledgersOf(ctx, q, buckets); err != nil {
			return err
		}
		have, err = c.targetBuckets(ctx, q, r, buckets)
		return err
	})
	if err != nil {
		return 0, 0, 0, err
	}
	// a bucket of the source still open may grow after the copy began (the
	// source is the store in use, towards the local file): only its presence
	// is checked
	settled := func(b bucket) bool {
		return c.opt.TargetInUse || c.periodEnd(b) <= c.marker.End-int64(2*time.Minute/time.Millisecond)
	}
	for _, b := range buckets {
		l := ledgers[b.id()]
		there, ok := have[b.id()]
		if !ok {
			// nothing of it in the target: right only when nothing was owed
			if b.pooled() && c.change(b, l) == (tally{}) && (c.catchUp() || tally{b.value, b.index} == add(add(l.prior, l.gave), l.got)) {
				present++
			} else {
				missing++
			}
			continue
		}
		if !settled(b) {
			present++
			continue
		}
		if !b.pooled() {
			if c.rawSourceWins(b) && (there.value != b.value || there.index != b.index) {
				missing++
			} else {
				present++
			}
			continue
		}
		// the whole part owed is accounted for in the target's ledger
		var accounted bool
		if c.catchUp() {
			accounted = l.gotOwn == l.own
		} else {
			accounted = add(add(l.got, l.gave), l.prior) == tally{b.value, b.index}
		}
		switch c.mergeKind(b) {
		case "add":
			if accounted {
				present++
			} else {
				missing++
			}
		case "max":
			if there.value >= b.value || c.catchUp() {
				present++
			} else {
				missing++
			}
		case "min":
			if there.value <= b.value || c.catchUp() {
				present++
			} else {
				missing++
			}
		case "last":
			if c.periodEnd(b) > c.opt.Back || there.value == b.value {
				present++
			} else {
				missing++
			}
		default:
			if there.value == b.value && there.index == b.index {
				present++
			} else {
				inexact++
			}
		}
	}
	return present, missing, inexact, nil
}

// verifyBuckets goes through the source's metric_values of the copied range,
// batch by batch, as verifyKeys does for the other tables.
func (c *copier) verifyBuckets(ctx context.Context, r rule, extra string, extraArgs []interface{}) (source, present, missing, inexact int64, err error) {
	var after []string
	for {
		select {
		case <-ctx.Done():
			return 0, 0, 0, 0, errStopped
		default:
		}
		started := time.Now()
		bctx, cancel := batchContext()
		batch, err := c.readNatural(bctx, r, after, extra, extraArgs)
		var p, m, x int64
		if err == nil && len(batch) > 0 {
			p, m, x, err = c.checkBuckets(bctx, r, batch)
		}
		cancel()
		if err != nil {
			return 0, 0, 0, 0, c.fail(r, "verifying", err)
		}
		if len(batch) == 0 {
			return source, present, missing, inexact, nil
		}
		source += int64(len(batch))
		present, missing, inexact = present+p, missing+m, inexact+x
		after = encodeKey(r, batch[len(batch)-1].values)
		if err := c.pause(ctx, time.Since(started)); err != nil {
			return 0, 0, 0, 0, err
		}
	}
}
