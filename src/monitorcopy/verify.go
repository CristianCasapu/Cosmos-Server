package monitorcopy

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// The verification compares the two stores over what the copy covered:
//
//   - below T0 (the target's oldest row of the table before the first copy
//     from this source) the target holds nothing but copied rows, so both
//     sides must agree exactly: row count, smallest, largest and sum of the
//     time, and sums of the integer columns of the rule;
//   - from T0 on (rows that may exist in both, decided by the conflict rule)
//     each row of the source must have its counterpart in the target: the
//     same key, or a row of the same identity;
//   - a table without time (metrics, monitoring_kv): each key of the source
//     must be in the target, and for metrics with a last_update not older.
//
// Rows the daily retention may delete between the copy and the check (an
// hour of margin) are left out on both sides.

const verifyMargin = time.Hour

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

	if r.window != "" {
		t0 := c.marker.T0[r.table]
		upper := t0
		if c.marker.End < upper {
			upper = c.marker.End
		}
		// below T0: the same aggregates on both sides
		exprs := []string{"COUNT(*)", "COALESCE(MIN(" + r.window + "), 0)", "COALESCE(MAX(" + r.window + "), 0)"}
		exprs = append(exprs, sumExprs(r.window)...)
		for _, name := range r.verifySums {
			exprs = append(exprs, sumExprs(name)...)
		}
		where, args := c.copyFilter(r, c.marker.End)
		where += extra + " AND (" + r.window + ") < ?"
		args = append(append(args, extraArgs...), upper)
		query := "SELECT " + strings.Join(exprs, ", ") + " FROM " + r.table + " WHERE 1 = 1" + where
		bctx, cancel := batchContext()
		a, errA := aggregates(bctx, c.src, query, args, len(exprs))
		b, errB := aggregates(bctx, c.dst, query, args, len(exprs))
		cancel()
		if errA != nil {
			return c.fail(r, "verifying", errA)
		}
		if errB != nil {
			return c.fail(r, "verifying", errB)
		}
		sourceRows, targetRows = a[0], b[0]
		for k := range a {
			if a[k] != b[k] {
				mismatch = true
			}
		}
		if a[0] > b[0] {
			missing += a[0] - b[0]
		}

		// from T0 on: every source row has its counterpart
		if t0 < c.marker.End {
			var s, t, m int64
			var err error
			if r.surrogate() {
				s, t, m, err = c.verifySurrogateFrom(ctx, r, t0, extra, extraArgs)
			} else {
				s, t, m, err = c.verifyKeys(ctx, r, " AND ("+r.window+") >= ?"+extra, append([]interface{}{t0}, extraArgs...))
			}
			if err != nil {
				return err
			}
			sourceRows, targetRows, missing = sourceRows+s, targetRows+t, missing+m
			if m > 0 {
				mismatch = true
			}
		}
	} else {
		s, t, m, err := c.verifyKeys(ctx, r, extra, extraArgs)
		if err != nil {
			return err
		}
		sourceRows, targetRows, missing = s, t, m
		mismatch = m > 0
	}

	c.track.set(i, func(p *TableProgress) {
		p.SourceRows, p.TargetRows, p.Missing = sourceRows, targetRows, missing
		p.State = "verified"
		if mismatch {
			p.State = "mismatch"
		}
	})
	return nil
}

func aggregates(ctx context.Context, s Store, query string, args []interface{}, n int) ([]int64, error) {
	out := make([]int64, n)
	dest := make([]interface{}, n)
	for k := range out {
		dest[k] = &out[k]
	}
	err := s.DB.QueryRowContext(ctx, s.bind(query), args...).Scan(dest...)
	return out, err
}

func (c *copier) verifySurrogateFrom(ctx context.Context, r rule, t0 int64, extra string, extraArgs []interface{}) (int64, int64, int64, error) {
	var sourceRows, targetRows, missing int64
	after := t0 - 1
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
				if err = fitFor(c.dst, r, one.values); err != nil {
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
				if err = fitFor(c.dst, r, one.values); err != nil {
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
