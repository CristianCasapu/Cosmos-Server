package monitorcopy

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// The verification goes through every source row the copy covered (before
// its end, within the retention) and looks for its counterpart in the target:
//
//   - a table with a natural key: the same key; for metrics a last_update not
//     older than the source's; for metric values copied to the local file
//     (the source wins) the same values, for buckets closed before the copy;
//   - a table with a surrogate id: a row of the same identity, each target
//     row standing for one source row at most (events and the security record
//     take every column as identity, so a copied row that changed is missing).
//
// Rows only the target has are not an error: the target was the store in use
// for periods of its own, which may be older or newer than the source's, as
// often as the two stores took turns (back to the local file, the fallback
// while PostgreSQL does not answer). An earlier check compared aggregates of
// the range below the target's oldest row at the first copy; that range holds
// the target's own rows as soon as the stores took turns, or when the first
// copy found the target empty, and the check then reported a false mismatch.
//
// Rows the daily retention may delete between the copy and the check (an
// hour of margin) are left out.

const verifyMargin = time.Hour

// bucketOpen: how long after its date a metric bucket may still change (the
// longest granularity is a month).
const bucketOpen = 32 * 24 * time.Hour

func (c *copier) verifyExtra(r rule, now int64) (string, []interface{}) {
	where := ""
	args := []interface{}{}
	if r.retentionCol != "" {
		where += " AND " + r.retentionCol + " >= ?"
		args = append(args, now-r.retention.Milliseconds()+verifyMargin.Milliseconds())
	}
	if r.expireCol != "" {
		where += " AND (" + r.expireCol + " = 0 OR " + r.expireCol + " > ?)"
		args = append(args, now+verifyMargin.Milliseconds())
	}
	return where, args
}

func (c *copier) verifyTable(ctx context.Context, i int, r rule) error {
	c.track.set(i, func(p *TableProgress) { p.State = "verifying"; p.SourceRows, p.TargetRows, p.Missing = 0, 0, 0 })
	now := c.opt.now().UnixMilli()
	extra, extraArgs := c.verifyExtra(r, now)
	mismatch := false
	var sourceRows, targetRows, missing int64

	// every source row of the copied range, one by one: the target's own
	// rows (of its turns as the store in use, at any time) are not compared
	var err error
	if r.surrogate() {
		sourceRows, targetRows, missing, err = c.verifySurrogate(ctx, r, math.MinInt64, extra, extraArgs)
	} else {
		sourceRows, targetRows, missing, err = c.verifyKeys(ctx, r, extra, extraArgs)
	}
	if err != nil {
		return err
	}
	mismatch = missing > 0

	c.track.set(i, func(p *TableProgress) {
		p.SourceRows, p.TargetRows, p.Missing = sourceRows, targetRows, missing
		p.State = "verified"
		if mismatch {
			p.State = "mismatch"
		}
	})
	return nil
}

// verifySurrogate matches the source rows after the time value after.
func (c *copier) verifySurrogate(ctx context.Context, r rule, after int64, extra string, extraArgs []interface{}) (int64, int64, int64, error) {
	var sourceRows, targetRows, missing int64
	for {
		select {
		case <-ctx.Done():
			return 0, 0, 0, errStopped
		default:
		}
		bctx, cancel := batchContext()
		batch, err := c.readSurrogate(bctx, r, after, extra, extraArgs)
		if err == nil && len(batch) > 0 {
			for _, one := range batch {
				if _, err = fitFor(c.dst, r, one.values); err != nil {
					break
				}
			}
		}
		var absent []row
		var pairs [][2]row
		if err == nil && len(batch) > 0 {
			absent, pairs, err = c.match(bctx, c.dst.DB, r, batch)
		}
		cancel()
		if err != nil {
			return 0, 0, 0, c.fail(r, "verifying", err)
		}
		if len(batch) == 0 {
			return sourceRows, targetRows, missing, nil
		}
		sourceRows += int64(len(batch))
		targetRows += int64(len(pairs))
		missing += int64(len(absent))
		after = batch[len(batch)-1].w
	}
}

// verifyKeys looks each source row (of the condition) up in the target by key.
func (c *copier) verifyKeys(ctx context.Context, r rule, extra string, extraArgs []interface{}) (int64, int64, int64, error) {
	var sourceRows, targetRows, missing int64
	var after []string
	keyIdx := make([]int, len(r.key))
	tuple := make([]string, len(r.key))
	for k, name := range r.key {
		keyIdx[k] = r.index(name)
		tuple[k] = "?"
		if r.columns[keyIdx[k]].kind != text {
			tuple[k] = "CAST(? AS BIGINT)"
		}
	}
	hole := "(" + strings.Join(tuple, ", ") + ")"
	lastUpdate := -1
	if r.table == "metrics" {
		lastUpdate = r.index("last_update")
	}
	// where the source wins (to the local file), a closed bucket the copy
	// wrote must hold the source's values; a bucket may still change for a
	// month after its date, and the target's own value wins where it is the
	// store in use, so only those are compared
	closedBefore := int64(math.MinInt64)
	values := []int{}
	if r.table == "metric_values" && !c.opt.TargetInUse {
		closedBefore = c.marker.End - bucketOpen.Milliseconds()
		values = []int{r.index("value"), r.index("avg_index"), r.index("expire")}
	}
	date := -1
	if r.table == "metric_values" {
		date = r.index("date")
	}
	for {
		select {
		case <-ctx.Done():
			return 0, 0, 0, errStopped
		default:
		}
		bctx, cancel := batchContext()
		batch, err := c.readNatural(bctx, r, after, extra, extraArgs)
		found := map[string]row{}
		for start := 0; err == nil && start < len(batch); start += 500 {
			end := start + 500
			if end > len(batch) {
				end = len(batch)
			}
			args := []interface{}{}
			holes := []string{}
			for _, one := range batch[start:end] {
				if _, err = fitFor(c.dst, r, one.values); err != nil {
					break
				}
				for _, k := range keyIdx {
					args = append(args, one.values[k])
				}
				holes = append(holes, hole)
			}
			if err != nil {
				break
			}
			var rows *sql.Rows
			rows, err = c.dst.DB.QueryContext(bctx, c.dst.bind("SELECT "+strings.Join(r.columnNames(), ", ")+" FROM "+r.table+
				" WHERE ("+strings.Join(r.key, ", ")+") IN (VALUES "+strings.Join(holes, ", ")+")"), args...)
			if err != nil {
				break
			}
			var have []row
			have, err = scanRows(rows, r, false, false)
			for _, one := range have {
				found[strings.Join(encodeKey(r, one.values), "\x00")] = one
			}
		}
		cancel()
		if err != nil {
			return 0, 0, 0, c.fail(r, "verifying", err)
		}
		if len(batch) == 0 {
			return sourceRows, targetRows, missing, nil
		}
		for _, one := range batch {
			sourceRows++
			there, ok := found[strings.Join(encodeKey(r, one.values), "\x00")]
			if !ok {
				missing++
				continue
			}
			// a metric described as last seen: the target's description must
			// not be older than the source's (unless the source moved on
			// after the copy began)
			if lastUpdate >= 0 {
				s, t := one.values[lastUpdate].(int64), there.values[lastUpdate].(int64)
				if t < s && s < c.marker.End {
					missing++
					continue
				}
			}
			if date >= 0 && one.values[date].(int64) < closedBefore {
				differs := false
				for _, k := range values {
					if one.values[k] != there.values[k] {
						differs = true
					}
				}
				if differs {
					missing++
					continue
				}
			}
			targetRows++
		}
		after = encodeKey(r, batch[len(batch)-1].values)
	}
}

// fail turns an error of the database into a fixed sentence about the table.
func (c *copier) fail(r rule, stage string, err error) error {
	var copyErr *CopyError
	if errors.Is(err, errStopped) || errors.As(err, &copyErr) || errors.Is(err, ErrStoreChanged) {
		return err
	}
	table := r.table
	name := "the copy's marker"
	if table != "" {
		name = "table " + table
	}
	sentence := "Copying " + name + " failed: the database refused the batch. The rows copied so far are kept; the copy can be resumed."
	switch stage {
	case "verifying":
		sentence = "The comparison of " + name + " failed: the database did not answer the check."
	case "counting", "reading":
		sentence = "Reading " + name + " failed."
	case "sequence":
		sentence = "The id counter of " + name + " could not be checked."
	}
	var tooLarge errTooLarge
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &tooLarge):
		sentence = "A value of column " + tooLarge.column + " in " + name + " is too large for PostgreSQL: nothing of that batch was copied."
	case errors.Is(err, context.DeadlineExceeded):
		sentence = "The database did not answer in time while working on " + name + ". The rows copied so far are kept; the copy can be resumed."
	case errors.Is(err, sql.ErrConnDone) || strings.Contains(err.Error(), "database is closed"):
		sentence = "The connection to the database closed while working on " + name + ". The rows copied so far are kept; the copy can be resumed."
	case errors.As(err, &pgErr):
		switch {
		case pgErr.Code == "42501":
			sentence = "The PostgreSQL user may not write " + name + "."
		case pgErr.Code == "53100":
			sentence = "PostgreSQL's disk is full: " + name + " could not be copied entirely. The rows copied so far are kept."
		case strings.HasPrefix(pgErr.Code, "08"), strings.HasPrefix(pgErr.Code, "57"):
			sentence = "PostgreSQL stopped answering while working on " + name + ". The rows copied so far are kept; the copy can be resumed."
		}
	case strings.Contains(err.Error(), "SQLITE_FULL") || strings.Contains(err.Error(), "database or disk is full"):
		sentence = "The disk of the local file is full: " + name + " could not be copied entirely. The rows copied so far are kept."
	case strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "database is locked"):
		sentence = "The local file was busy for too long while working on " + name + ". The rows copied so far are kept; the copy can be resumed."
	}
	return &CopyError{Table: table, Sentence: sentence, Err: err}
}
