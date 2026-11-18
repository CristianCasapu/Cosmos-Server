package monitorcopy

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Options of one copy.
type Options struct {
	// SourceID names the source in the target's markers: a resumed copy
	// continues where the copy from the same source stopped.
	SourceID string
	// TargetInUse: the target is the store Cosmos writes (to PostgreSQL);
	// false when the source is (to the local file).
	TargetInUse bool
	// Batch is the number of rows read and written per transaction.
	Batch int
	// Pause is how long to wait after a batch that took took (rate limit).
	Pause func(took time.Duration) time.Duration
	// Guard is asked before each batch: an error stops the copy (the store
	// in use changed).
	Guard func() error
	Now   func() time.Time
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// TableProgress is one table's state during and after a copy.
type TableProgress struct {
	Table   string `json:"table"`
	Total   int64  `json:"total"`   // rows of the source to go through
	Done    int64  `json:"done"`    // rows of the source gone through
	Written int64  `json:"written"` // rows written to the target (new or newer)
	// values that are not finite numbers (NaN, infinities), stored as 0 in a
	// SQLite target, which cannot hold them in a NOT NULL column
	Replaced int64 `json:"replaced"`
	// State: waiting, copying, copied, verifying, verified, mismatch, failed
	State string `json:"state"`
	// the verification: rows of the copied range in each store
	SourceRows int64 `json:"sourceRows"`
	TargetRows int64 `json:"targetRows"`
	Missing    int64 `json:"missing"` // source rows without their counterpart in the target, or with one that differs
}

// Tracker holds the progress of a copy; the engine updates it, readers take
// a snapshot.
type Tracker struct {
	mu     sync.Mutex
	phase  string
	tables []TableProgress
	since  time.Time
	read   int64
}

func (t *Tracker) set(i int, f func(p *TableProgress)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	f(&t.tables[i])
}

func (t *Tracker) setPhase(phase string) {
	t.mu.Lock()
	t.phase = phase
	if phase == "copying" {
		t.since = time.Now()
	}
	t.mu.Unlock()
}

// reset puts every table back to waiting.
func (t *Tracker) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.phase = "preparing"
	t.tables = make([]TableProgress, len(rules))
	for i, r := range rules {
		t.tables[i] = TableProgress{Table: r.table, State: "waiting"}
	}
}

func (t *Tracker) add(read int64) {
	t.mu.Lock()
	t.read += read
	t.mu.Unlock()
}

// Rate is the rows read per second by this run since the copying began
// (a resumed copy counts only what it read itself).
func (t *Tracker) Rate() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	elapsed := time.Since(t.since).Seconds()
	if t.since.IsZero() || elapsed < 1 || t.read == 0 {
		return 0
	}
	return float64(t.read) / elapsed
}

// Snapshot returns the phase and a copy of the tables' progress.
func (t *Tracker) Snapshot() (string, []TableProgress) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.phase, append([]TableProgress(nil), t.tables...)
}

// Report is the outcome of a copy that ran to its end.
type Report struct {
	End      int64           `json:"end"` // the copy covers what was recorded before this time (unix ms)
	Written  int64           `json:"written"`
	Mismatch bool            `json:"mismatch"`
	Tables   []TableProgress `json:"tables"`
}

// CopyError says which table failed and why, in a fixed sentence.
type CopyError struct {
	Table    string
	Sentence string
	Err      error
}

func (e *CopyError) Error() string { return e.Sentence }
func (e *CopyError) Unwrap() error { return e.Err }

// errStopped: the copy was cancelled (after a complete batch).
var errStopped = errors.New("monitorcopy: stopped")

type runMarker struct {
	Run      string `json:"run"`
	Started  int64  `json:"started"`
	End      int64  `json:"end"`
	Finished bool   `json:"finished"`
}

type tableMarker struct {
	Run     string   `json:"run"`
	Done    bool     `json:"done"`
	W       int64    `json:"w"`             // surrogate: the last time value copied entirely
	Key     []string `json:"key,omitempty"` // natural: the last key copied
	Written int64    `json:"written"`
	// values that are not finite numbers, stored as 0 in a SQLite target
	Replaced int64 `json:"replaced,omitempty"`
}

func runKey(sourceID string) string          { return markerPrefix + sourceID }
func tableKey(sourceID, table string) string { return markerPrefix + sourceID + ":" + table }

// Copy copies src into dst by the rules, resumes an unfinished copy from the
// same source, and verifies the result. It returns errStopped when ctx is
// cancelled, a *CopyError on a failure.
func Copy(ctx context.Context, src, dst Store, opt Options, track *Tracker) (Report, error) {
	if opt.Batch <= 0 {
		opt.Batch = 1000
	}
	c := &copier{src: src, dst: dst, opt: opt, track: track}
	report, err := c.run(ctx)
	// a handle closed under the copy by a switch of the store fails the
	// statement: that is the switch, not a failure of the table
	if err != nil && !errors.Is(err, errStopped) && c.guard() != nil {
		return report, ErrStoreChanged
	}
	return report, err
}

type copier struct {
	src, dst Store
	opt      Options
	track    *Tracker
	marker   runMarker
	layouts  map[string]layout
}

// background is the context of one batch's statements: a cancel waits for
// the batch to end, a database that does not answer does not hold it for ever.
func batchContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 2*time.Minute)
}

func (c *copier) run(ctx context.Context) (Report, error) {
	if err := c.prepare(); err != nil {
		return Report{}, err
	}
	c.track.setPhase("copying")
	for i, r := range rules {
		if !c.layouts[r.table].exists {
			continue
		}
		if err := c.copyTable(ctx, i, r); err != nil {
			return Report{}, err
		}
	}

	bctx, cancel := batchContext()
	defer cancel()
	c.marker.Finished = true
	if err := c.saveRun(bctx); err != nil {
		return Report{}, c.fail(rule{}, "marker", err)
	}
	if err := c.checkSequences(bctx); err != nil {
		return Report{}, err
	}
	return c.verifyAll(ctx)
}

// verifyOnly compares the stores as the end of a copy does, without
// copying (for the tests).
func verifyOnly(ctx context.Context, src, dst Store, opt Options, track *Tracker) (Report, error) {
	c := &copier{src: src, dst: dst, opt: opt, track: track}
	if c.opt.Batch <= 0 {
		c.opt.Batch = 1000
	}
	if err := c.prepare(); err != nil {
		return Report{}, err
	}
	return c.verifyAll(ctx)
}

// prepare lays out the target, reads the source's layout, loads or starts
// the run and counts what there is to go through.
func (c *copier) prepare() error {
	track := c.track
	track.reset()

	bctx, cancel := batchContext()
	defer cancel()
	if err := c.guard(); err != nil {
		return err
	}
	if err := utils.PrepareMonitoringSchema(c.dst.DB, c.dst.Dialect); err != nil {
		return &CopyError{Sentence: "The target's tables could not be prepared.", Err: err}
	}
	tables, err := c.src.tables(bctx)
	if err != nil {
		return &CopyError{Sentence: "The source's tables could not be read.", Err: err}
	}
	c.layouts = map[string]layout{}
	for _, r := range rules {
		l, err := c.src.layoutOf(bctx, r, tables)
		if err != nil {
			if errors.Is(err, errOldLayout) {
				return &CopyError{Table: r.table, Sentence: "The source's table " + r.table + " is of an older layout: open it once with Cosmos, then copy.", Err: err}
			}
			return &CopyError{Table: r.table, Sentence: "The source's table " + r.table + " could not be read.", Err: err}
		}
		c.layouts[r.table] = l
	}
	if err := c.loadRun(bctx); err != nil {
		return err
	}
	for i, r := range rules {
		if !c.layouts[r.table].exists {
			track.set(i, func(p *TableProgress) { p.State = "copied" })
			continue
		}
		where, args := c.copyFilter(r, c.marker.End)
		var total int64
		if err := c.src.DB.QueryRowContext(bctx, c.src.bind("SELECT COUNT(*) FROM "+r.table+" WHERE 1 = 1"+where), args...).Scan(&total); err != nil {
			return c.fail(r, "counting", err)
		}
		track.set(i, func(p *TableProgress) { p.Total = total })
	}
	return nil
}

func (c *copier) verifyAll(ctx context.Context) (Report, error) {
	c.track.setPhase("verifying")
	report := Report{End: c.marker.End}
	for i, r := range rules {
		if !c.layouts[r.table].exists {
			continue
		}
		if err := c.verifyTable(ctx, i, r); err != nil {
			return Report{}, err
		}
	}
	c.track.setPhase("done")
	_, report.Tables = c.track.Snapshot()
	for _, p := range report.Tables {
		report.Written += p.Written
		if p.State == "mismatch" {
			report.Mismatch = true
		}
	}
	return report, nil
}

func (c *copier) guard() error {
	if c.opt.Guard == nil {
		return nil
	}
	return c.opt.Guard()
}

func newRunID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// loadRun continues the unfinished copy from the same source, or starts a
// new one.
func (c *copier) loadRun(ctx context.Context) error {
	value, found, err := c.dst.getKV(ctx, c.dst.DB, runKey(c.opt.SourceID))
	if err != nil {
		return &CopyError{Sentence: "The copy's marker could not be read from the target.", Err: err}
	}
	previous := runMarker{}
	if found {
		if json.Unmarshal([]byte(value), &previous) != nil {
			previous = runMarker{}
		}
	}
	if found && previous.Run != "" && !previous.Finished && previous.End > 0 {
		c.marker = previous
	} else {
		c.marker = runMarker{Run: newRunID(), Started: utils.TimeToMillis(c.opt.now()), End: utils.TimeToMillis(c.opt.now())}
	}
	return c.saveRun(ctx)
}

func (c *copier) saveRun(ctx context.Context) error {
	encoded, _ := json.Marshal(c.marker)
	return c.dst.setKV(ctx, c.dst.DB, runKey(c.opt.SourceID), string(encoded))
}

func (c *copier) copyFilter(r rule, end int64) (string, []interface{}) { return copyFilter(r, end) }

// copyFilter is the condition on the source's rows that are copied: before
// the copy's end, not expired, not older than the target's retention.
func copyFilter(r rule, end int64) (string, []interface{}) {
	where := ""
	args := []interface{}{}
	if r.where != "" {
		where += " AND " + r.where
	}
	if r.window != "" {
		where += " AND (" + r.window + ") < ?"
		args = append(args, end)
	}
	if r.retentionCol != "" {
		where += " AND " + r.retentionCol + " >= ?"
		args = append(args, end-r.retention.Milliseconds())
	}
	if r.expireCol != "" {
		where += " AND (" + r.expireCol + " = 0 OR " + r.expireCol + " > ?)"
		args = append(args, end)
	}
	return where, args
}

func (c *copier) loadTable(ctx context.Context, r rule) (tableMarker, error) {
	value, found, err := c.dst.getKV(ctx, c.dst.DB, tableKey(c.opt.SourceID, r.table))
	m := tableMarker{}
	if err != nil {
		return m, err
	}
	if found && json.Unmarshal([]byte(value), &m) == nil && m.Run == c.marker.Run {
		return m, nil
	}
	return tableMarker{Run: c.marker.Run, W: math.MinInt64}, nil
}

func (c *copier) pause(ctx context.Context, took time.Duration) error {
	wait := time.Duration(0)
	if c.opt.Pause != nil {
		wait = c.opt.Pause(took)
	}
	if wait <= 0 {
		select {
		case <-ctx.Done():
			return errStopped
		default:
			return nil
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return errStopped
	case <-timer.C:
		return nil
	}
}

func (c *copier) copyTable(ctx context.Context, i int, r rule) error {
	bctx, cancel := batchContext()
	m, err := c.loadTable(bctx, r)
	cancel()
	if err != nil {
		return c.fail(r, "marker", err)
	}
	if m.Done {
		c.track.set(i, func(p *TableProgress) {
			p.Done, p.Written, p.Replaced, p.State = p.Total, m.Written, m.Replaced, "copied"
		})
		return nil
	}
	// rows already gone through by the copy this one resumes
	if m.W != math.MinInt64 || len(m.Key) > 0 {
		bctx, cancel := batchContext()
		done, err := c.countUpTo(bctx, r, m)
		cancel()
		if err != nil {
			return c.fail(r, "counting", err)
		}
		c.track.set(i, func(p *TableProgress) { p.Done, p.Written, p.Replaced = done, m.Written, m.Replaced })
	}
	c.track.set(i, func(p *TableProgress) { p.State = "copying" })

	for {
		select {
		case <-ctx.Done():
			return errStopped
		default:
		}
		if err := c.guard(); err != nil {
			return err
		}
		started := time.Now()
		bctx, cancel := batchContext()
		read, written, err := c.batch(bctx, r, &m)
		cancel()
		if err != nil {
			return c.fail(r, "copying", err)
		}
		c.track.set(i, func(p *TableProgress) { p.Done += read; p.Written += written; p.Replaced = m.Replaced })
		c.track.add(read)
		if read == 0 {
			break
		}
		if err := c.pause(ctx, time.Since(started)); err != nil {
			return err
		}
	}

	m.Done = true
	bctx, cancel = batchContext()
	defer cancel()
	encoded, _ := json.Marshal(m)
	if err := c.dst.setKV(bctx, c.dst.DB, tableKey(c.opt.SourceID, r.table), string(encoded)); err != nil {
		return c.fail(r, "marker", err)
	}
	c.track.set(i, func(p *TableProgress) {
		if p.Done < p.Total {
			p.Done = p.Total
		}
		p.State = "copied"
	})
	return nil
}

// countUpTo counts the rows the marker says were gone through.
func (c *copier) countUpTo(ctx context.Context, r rule, m tableMarker) (int64, error) {
	where, args := c.copyFilter(r, c.marker.End)
	if r.surrogate() {
		where += " AND (" + r.window + ") <= ?"
		args = append(args, m.W)
	} else {
		keyArgs, err := keyValues(r, m.Key)
		if err != nil {
			return 0, err
		}
		where += " AND (" + strings.Join(r.key, ", ") + ") <= (" + placeholders(len(r.key)) + ")"
		args = append(args, keyArgs...)
	}
	var n int64
	err := c.src.DB.QueryRowContext(ctx, c.src.bind("SELECT COUNT(*) FROM "+r.table+" WHERE 1 = 1"+where), args...).Scan(&n)
	return n, err
}

// batch copies the next batch and moves the marker, in one transaction of
// the target. It returns the rows read (0 at the end) and written.
func (c *copier) batch(ctx context.Context, r rule, m *tableMarker) (int64, int64, error) {
	if r.surrogate() {
		return c.batchSurrogate(ctx, r, m)
	}
	return c.batchNatural(ctx, r, m)
}

// natural keys ------------------------------------------------------------

func keyValues(r rule, encoded []string) ([]interface{}, error) {
	if len(encoded) != len(r.key) {
		return nil, errors.New("monitorcopy: marker of another layout")
	}
	out := make([]interface{}, len(encoded))
	for i, name := range r.key {
		if r.columns[r.index(name)].kind == text {
			out[i] = encoded[i]
			continue
		}
		n, err := strconv.ParseInt(encoded[i], 10, 64)
		if err != nil {
			return nil, err
		}
		out[i] = n
	}
	return out, nil
}

func encodeKey(r rule, values []interface{}) []string {
	out := make([]string, len(r.key))
	for i, name := range r.key {
		switch v := values[r.index(name)].(type) {
		case string:
			out[i] = v
		case int64:
			out[i] = itoa(v)
		}
	}
	return out
}

// readNatural reads the next rows after the key `after` (nil: from the
// start) in key order; extra is a further condition.
func (c *copier) readNatural(ctx context.Context, r rule, after []string, extra string, extraArgs []interface{}) ([]row, error) {
	where, args := c.copyFilter(r, c.marker.End)
	where += extra
	args = append(args, extraArgs...)
	if len(after) > 0 {
		keyArgs, err := keyValues(r, after)
		if err != nil {
			return nil, err
		}
		where += " AND (" + strings.Join(r.key, ", ") + ") > (" + placeholders(len(r.key)) + ")"
		args = append(args, keyArgs...)
	}
	query := "SELECT " + c.layouts[r.table].list + " FROM " + r.table + " WHERE 1 = 1" + where +
		" ORDER BY " + strings.Join(r.key, ", ") + " LIMIT " + strconv.Itoa(c.opt.Batch)
	rows, err := c.src.DB.QueryContext(ctx, c.src.bind(query), args...)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, r, false, false)
}

func (c *copier) batchNatural(ctx context.Context, r rule, m *tableMarker) (int64, int64, error) {
	batch, err := c.readNatural(ctx, r, m.Key, "", nil)
	if err != nil || len(batch) == 0 {
		return 0, 0, err
	}
	replaced := int64(0)
	for _, one := range batch {
		n, err := fitFor(c.dst, r, one.values)
		if err != nil {
			return 0, 0, err
		}
		replaced += int64(n)
	}
	tx, err := c.dst.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	written, err := c.insert(ctx, tx, r, batch, " ON CONFLICT("+strings.Join(r.key, ", ")+") "+r.onConflict(c.opt.TargetInUse))
	if err != nil {
		return 0, 0, err
	}
	next := *m
	next.Key = encodeKey(r, batch[len(batch)-1].values)
	next.Written += written
	next.Replaced += replaced
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

// rowsPerStatement keeps a multi-row INSERT under both dialects' limits on
// placeholders.
func rowsPerStatement(r rule) int {
	n := 8000 / len(r.columns)
	if n > 500 {
		n = 500
	}
	return n
}

// insert writes rows with multi-row INSERTs; suffix follows the VALUES.
func (c *copier) insert(ctx context.Context, tx execer, r rule, batch []row, suffix string) (int64, error) {
	names := strings.Join(r.columnNames(), ", ")
	one := "(" + placeholders(len(r.columns)) + ")"
	step := rowsPerStatement(r)
	written := int64(0)
	for start := 0; start < len(batch); start += step {
		end := start + step
		if end > len(batch) {
			end = len(batch)
		}
		args := make([]interface{}, 0, (end-start)*len(r.columns))
		holes := make([]string, 0, end-start)
		for _, one := range batch[start:end] {
			args = append(args, one.values...)
		}
		for k := start; k < end; k++ {
			holes = append(holes, one)
		}
		result, err := tx.ExecContext(ctx, c.dst.bind("INSERT INTO "+r.table+"("+names+") VALUES "+strings.Join(holes, ", ")+suffix), args...)
		if err != nil {
			return 0, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		written += n
	}
	return written, nil
}

// surrogate ids -----------------------------------------------------------

// rowsQueryer is a *sql.DB or a *sql.Tx: a SQLite target has one connection,
// which its transaction holds.
type rowsQueryer interface {
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
}

// readSurrogate reads the rows after the time value after, in time order,
// about batch rows and always every row of the last time value.
func (c *copier) readSurrogate(ctx context.Context, r rule, after int64, extra string, extraArgs []interface{}) ([]row, error) {
	where, args := c.copyFilter(r, c.marker.End)
	where += extra
	args = append(args, extraArgs...)
	list := c.layouts[r.table].list
	query := "SELECT id, (" + r.window + ") AS w, " + list + " FROM " + r.table + " WHERE (" + r.window + ") > ?" + where +
		" ORDER BY w, id LIMIT " + strconv.Itoa(c.opt.Batch)
	rows, err := c.src.DB.QueryContext(ctx, c.src.bind(query), append([]interface{}{after}, args...)...)
	if err != nil {
		return nil, err
	}
	batch, err := scanRows(rows, r, true, true)
	if err != nil || len(batch) < c.opt.Batch {
		return batch, err
	}
	// the rest of the last time value, so that a time value is never split
	last := batch[len(batch)-1]
	for {
		query := "SELECT id, (" + r.window + ") AS w, " + list + " FROM " + r.table + " WHERE (" + r.window + ") = ? AND id > ?" + where +
			" ORDER BY id LIMIT " + strconv.Itoa(c.opt.Batch)
		rows, err := c.src.DB.QueryContext(ctx, c.src.bind(query), append([]interface{}{last.w, last.id}, args...)...)
		if err != nil {
			return nil, err
		}
		more, err := scanRows(rows, r, true, true)
		if err != nil {
			return nil, err
		}
		batch = append(batch, more...)
		if len(more) < c.opt.Batch {
			return batch, nil
		}
		last = more[len(more)-1]
	}
}

// match pairs each row of the batch with a row of the target of the same
// identity (each target row at most once). It returns the rows to insert and
// the pairs found.
func (c *copier) match(ctx context.Context, q rowsQueryer, r rule, batch []row) ([]row, [][2]row, error) {
	seen := map[int64]bool{}
	times := []interface{}{}
	for _, one := range batch {
		if !seen[one.w] {
			seen[one.w] = true
			times = append(times, one.w)
		}
	}
	found := map[[32]byte][]row{}
	all := "id, (" + r.window + ") AS w, " + strings.Join(r.columnNames(), ", ")
	for start := 0; start < len(times); start += 500 {
		end := start + 500
		if end > len(times) {
			end = len(times)
		}
		rows, err := q.QueryContext(ctx, c.dst.bind("SELECT "+all+" FROM "+r.table+" WHERE ("+r.window+") IN ("+placeholders(end-start)+")"), times[start:end]...)
		if err != nil {
			return nil, nil, err
		}
		have, err := scanRows(rows, r, true, true)
		if err != nil {
			return nil, nil, err
		}
		for _, one := range have {
			k := identityKey(append([]interface{}{one.w}, r.identity(one)...))
			found[k] = append(found[k], one)
		}
	}
	insert := []row{}
	pairs := [][2]row{}
	for _, one := range batch {
		k := identityKey(append([]interface{}{one.w}, r.identity(one)...))
		if list := found[k]; len(list) > 0 {
			pairs = append(pairs, [2]row{one, list[0]})
			found[k] = list[1:]
			continue
		}
		insert = append(insert, one)
	}
	return insert, pairs, nil
}

func (c *copier) batchSurrogate(ctx context.Context, r rule, m *tableMarker) (int64, int64, error) {
	batch, err := c.readSurrogate(ctx, r, m.W, "", nil)
	if err != nil || len(batch) == 0 {
		return 0, 0, err
	}
	replaced := int64(0)
	for _, one := range batch {
		n, err := fitFor(c.dst, r, one.values)
		if err != nil {
			return 0, 0, err
		}
		replaced += int64(n)
	}
	tx, err := c.dst.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	// matched inside the transaction's time: the target's rows of these
	// time values are read again by the next copy, never by this one
	insert, pairs, err := c.match(ctx, tx, r, batch)
	if err != nil {
		return 0, 0, err
	}
	written, err := c.insert(ctx, tx, r, insert, "")
	if err != nil {
		return 0, 0, err
	}
	if r.refresh != nil {
		sets := make([]string, len(r.refreshCols))
		for k, name := range r.refreshCols {
			sets[k] = name + " = ?"
		}
		update := c.dst.bind("UPDATE " + r.table + " SET " + strings.Join(sets, ", ") + " WHERE id = ?")
		for _, pair := range pairs {
			if !r.refresh(pair[0], pair[1]) {
				continue
			}
			args := make([]interface{}, 0, len(r.refreshCols)+1)
			for _, name := range r.refreshCols {
				args = append(args, pair[0].values[r.index(name)])
			}
			args = append(args, pair[1].id)
			if _, err := tx.ExecContext(ctx, update, args...); err != nil {
				return 0, 0, err
			}
			written++
		}
	}
	next := *m
	next.W = batch[len(batch)-1].w
	next.Written += written
	next.Replaced += replaced
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

// checkSequences makes sure each id sequence of the target is past its
// highest id. The copy lets the target number its rows, so this only ever
// moves a sequence forward, never back (a writer may hold a number already).
func (c *copier) checkSequences(ctx context.Context) error {
	for _, r := range rules {
		if !r.surrogate() {
			continue
		}
		var err error
		if c.dst.postgres() {
			_, err = c.dst.DB.ExecContext(ctx, "SELECT setval(pg_get_serial_sequence('"+r.table+"', 'id'), m) FROM (SELECT MAX(id) AS m FROM "+r.table+") x"+
				" WHERE m IS NOT NULL AND m > COALESCE((SELECT last_value FROM pg_sequences WHERE format('%I.%I', schemaname, sequencename) = pg_get_serial_sequence('"+r.table+"', 'id')), 0)")
		} else {
			_, err = c.dst.DB.ExecContext(ctx, "UPDATE sqlite_sequence SET seq = (SELECT MAX(id) FROM "+r.table+") WHERE name = '"+r.table+"' AND seq < (SELECT MAX(id) FROM "+r.table+")")
		}
		if err != nil {
			return c.fail(r, "sequence", err)
		}
	}
	return nil
}
