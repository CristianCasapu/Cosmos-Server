package monitorcopy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/azukaar/cosmos-server/src/utils"

	// the security record's table is part of the store: its schema code
	// registers itself (utils.RegisterMonitoringSchema)
	_ "github.com/azukaar/cosmos-server/src/security"
)

// The directions.
const (
	ToPostgres = "to-postgres" // the local file's history into PostgreSQL, the store in use
	ToLocal    = "to-local"    // PostgreSQL's records into the local file, for a later switch back
)

// ErrStoreChanged: the store Cosmos writes changed during the copy (the
// fallback to the local file, or the settings).
var ErrStoreChanged = errors.New("the monitoring store in use changed during the copy")

// Errors of Start, in words.
var (
	ErrRunning      = errors.New("A copy is already running.")
	ErrNotPostgres  = errors.New("The monitoring store in use is not PostgreSQL: there is nothing to copy between the two stores now.")
	ErrNoLocalFile  = errors.New("There is no local file of monitoring records to copy.")
	ErrStoreClosed  = errors.New("The monitoring store is not open.")
	ErrBadDirection = errors.New("Unknown direction.")
)

// Result is how the last copy ended.
type Result struct {
	Direction  string          `json:"direction"`
	Outcome    string          `json:"outcome"` // done, mismatch, cancelled, failed, stopped
	FinishedAt time.Time       `json:"finishedAt"`
	Written    int64           `json:"written"`
	Replaced   int64           `json:"replaced"` // values that are not finite numbers, stored as 0 (to the local file)
	UpTo       int64           `json:"upTo"`     // unix ms: both stores hold the history recorded before it
	Message    string          `json:"message"`
	Table      string          `json:"table"`
	Tables     []TableProgress `json:"tables"`
}

type runState struct {
	mu         sync.Mutex
	running    bool
	direction  string
	started    time.Time
	cancel     context.CancelFunc
	cancelling bool
	track      *Tracker
	last       *Result
	done       chan struct{}
}

var state = &runState{}

// settings the tests change
var (
	batchSize = 1000
	pauseFor  = func(took time.Duration) time.Duration {
		wait := took / 4
		if wait < 2*time.Millisecond {
			wait = 2 * time.Millisecond
		}
		if wait > 250*time.Millisecond {
			wait = 250 * time.Millisecond
		}
		return wait
	}
)

func localDSN(path string, readOnly bool) string {
	if readOnly {
		// its records are never written: the file is opened read-only, and
		// the connection refuses writes even if it were not. SQLite may
		// still create the companion files database.db-wal and -shm next to
		// it (a reader of a WAL database needs the shared index) and leave
		// them: the main file's content and time are unchanged. immutable=1
		// would avoid them, but is unsafe while the fallback may write the
		// file; deleting them afterwards is unsafe for the same reason.
		return "file:" + path + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)"
	}
	return "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_txlock=immediate"
}

func openLocal(readOnly bool) (Store, error) {
	return openLocalFn(utils.LocalMonitoringFile(), readOnly)
}

// openLocalFn opens the local file; the tests look at what Start opened.
var openLocalFn = openLocalAt

func openLocalAt(path string, readOnly bool) (Store, error) {
	db, err := sql.Open(utils.DialectSQLite, localDSN(path, readOnly))
	if err != nil {
		return Store{}, err
	}
	db.SetMaxOpenConns(1)
	return Store{DB: db, Dialect: utils.DialectSQLite}, nil
}

func sourceID(direction string) string {
	identity := "sqlite:" + utils.LocalMonitoringFile()
	if direction == ToLocal {
		config := utils.GetMainConfig()
		identity = "pgx:" + config.Database.PostgresHost + "/" + config.Database.PostgresDatabase
	}
	sum := sha256.Sum256([]byte(identity))
	return direction + ":" + hex.EncodeToString(sum[:8])
}

// inUse is the store Cosmos writes, when it is PostgreSQL.
func inUse() (Store, Store, error) {
	if utils.MonitoringDialect() != utils.DialectPostgres {
		return Store{}, Store{}, ErrNotPostgres
	}
	w, err := utils.MonitoringWriteDB()
	if err != nil {
		return Store{}, Store{}, ErrStoreClosed
	}
	r, err := utils.MonitoringReadDB()
	if err != nil {
		return Store{}, Store{}, ErrStoreClosed
	}
	return Store{DB: w, Dialect: utils.DialectPostgres}, Store{DB: r, Dialect: utils.DialectPostgres}, nil
}

func localExists() bool {
	info, err := os.Stat(utils.LocalMonitoringFile())
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

// Start begins a copy in the background. It never runs by itself: only this
// call, from the route of an administrator, starts one.
func Start(direction string) error {
	if direction != ToPostgres && direction != ToLocal {
		return ErrBadDirection
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.running {
		return ErrRunning
	}
	write, read, err := inUse()
	if err != nil {
		return err
	}

	var src, dst Store
	var own Store // the handle this copy opened and closes
	switch direction {
	case ToPostgres:
		if !localExists() {
			return ErrNoLocalFile
		}
		local, err := openLocal(true)
		if err != nil {
			return ErrNoLocalFile
		}
		src, dst, own = local, write, local
	case ToLocal:
		local, err := openLocal(false)
		if err != nil {
			return errors.New("The local file could not be opened.")
		}
		src, dst, own = read, local, local
	}

	guard := func() error {
		w, errW := utils.MonitoringWriteDB()
		r, errR := utils.MonitoringReadDB()
		if errW != nil || errR != nil || w != write.DB || r != read.DB || utils.MonitoringDialect() != utils.DialectPostgres {
			return ErrStoreChanged
		}
		return nil
	}
	opt := Options{
		SourceID:    sourceID(direction),
		TargetInUse: direction == ToPostgres,
		Batch:       batchSize,
		Pause:       pauseFor,
		Guard:       guard,
	}

	ctx, cancel := context.WithCancel(context.Background())
	track := &Tracker{}
	track.reset() // shown at once, before the copy's goroutine runs
	done := make(chan struct{})
	state.running, state.direction, state.started = true, direction, time.Now()
	state.cancel, state.cancelling, state.track, state.done = cancel, false, track, done

	utils.Log("Monitoring history copy: started (" + direction + ")")
	go func() {
		defer close(done)
		defer cancel()
		report, err := Copy(ctx, src, dst, opt, track)
		own.DB.Close()
		result := resultOf(direction, report, err, track)
		state.mu.Lock()
		state.running, state.cancel, state.last = false, nil, &result
		state.mu.Unlock()
		forgetSummaries()
	}()
	return nil
}

func resultOf(direction string, report Report, err error, track *Tracker) Result {
	_, tables := track.Snapshot()
	result := Result{Direction: direction, FinishedAt: time.Now(), Tables: tables}
	for _, p := range tables {
		result.Written += p.Written
		result.Replaced += p.Replaced
	}
	var copyErr *CopyError
	switch {
	case err == nil:
		result.Outcome, result.UpTo, result.Written = "done", report.End, report.Written
		if report.Mismatch {
			result.Outcome = "mismatch"
			names := []string{}
			for _, p := range report.Tables {
				if p.State == "mismatch" {
					names = append(names, p.Table)
				}
			}
			result.Message = "The two stores do not agree on: " + strings.Join(names, ", ") + "."
			utils.Warn("Monitoring history copy: finished, the stores do not agree on " + strings.Join(names, ", "))
		} else {
			utils.Log("Monitoring history copy: finished, " + itoa(result.Written) + " rows written")
		}
	case errors.Is(err, errStopped):
		result.Outcome, result.Message = "cancelled", "The copy was cancelled after a complete batch. Start it again to resume where it stopped."
		utils.Log("Monitoring history copy: cancelled")
	case errors.Is(err, ErrStoreChanged):
		result.Outcome = "stopped"
		result.Message = "The monitoring store in use changed during the copy (PostgreSQL stopped answering, or the settings were changed): the copy stopped after its last complete batch. It can be resumed once PostgreSQL is in use again."
		utils.Warn("Monitoring history copy: stopped, the store in use changed")
	case errors.As(err, &copyErr):
		result.Outcome, result.Message, result.Table = "failed", copyErr.Sentence, copyErr.Table
		utils.Error("Monitoring history copy: "+copyErr.Sentence, errors.New(logSafe(copyErr.Err)))
	default:
		result.Outcome, result.Message = "failed", "The copy failed."
		utils.Error("Monitoring history copy: failed", errors.New(logSafe(err)))
	}
	return result
}

// logSafe is an error's text for the log: the password of the monitoring
// database masked, no control characters, bounded.
func logSafe(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if password := utils.GetMainConfig().Database.PostgresPassword; password != "" {
		text = strings.ReplaceAll(text, password, "***")
	}
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	if len(text) > 500 {
		text = text[:500]
	}
	return text
}

// Cancel asks the running copy to stop after its current batch.
func Cancel() bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.running || state.cancel == nil {
		return false
	}
	state.cancelling = true
	state.cancel()
	return true
}

// wait blocks until the running copy (if any) ended; for the tests.
func wait() {
	state.mu.Lock()
	done := state.done
	state.mu.Unlock()
	if done != nil {
		<-done
	}
}

// the summaries of the stores --------------------------------------------

// TableCount is one table of a store's summary.
type TableCount struct {
	Table string `json:"table"`
	Rows  int64  `json:"rows"`
}

// Summary is what a store holds that a copy would take.
type Summary struct {
	Bytes   int64        `json:"bytes"` // the local file (with its journal), 0 for PostgreSQL
	Records int64        `json:"records"`
	First   int64        `json:"first"` // unix ms, 0 when nothing has a time
	Last    int64        `json:"last"`
	Tables  []TableCount `json:"tables"`
	// LastCopy: when the last complete copy from this store into the other
	// one began (unix ms, 0 when never)
	LastCopy int64 `json:"lastCopy"`
}

type cachedSummary struct {
	at      time.Time
	summary *Summary
	problem string
	valid   bool // counted at least once
}

var (
	summaryMu    sync.Mutex
	summaries    = map[string]cachedSummary{}
	counting     = map[string]bool{} // a count of the direction runs in the background
	summaryGen   int                 // raised by forgetSummaries: a count begun before is dropped
	summaryCache = 30 * time.Second
	summaryWG    sync.WaitGroup // the counts in the background, for the tests

	computeSummaryFn = computeSummary
)

// forgetSummaries makes the next view count again; until that count is
// done, the numbers counted before are shown.
func forgetSummaries() {
	summaryMu.Lock()
	for direction, cached := range summaries {
		cached.at = time.Time{}
		summaries[direction] = cached
	}
	summaryGen++
	summaryMu.Unlock()
}

func summarize(ctx context.Context, s Store, end int64) (*Summary, error) {
	tables, err := s.tables(ctx)
	if err != nil {
		return nil, err
	}
	out := &Summary{Tables: []TableCount{}}
	for _, r := range rules {
		l, err := s.layoutOf(ctx, r, tables)
		if err != nil || !l.exists {
			continue
		}
		where, args := copyFilter(r, end)
		query := "SELECT COUNT(*) FROM " + r.table + " WHERE 1 = 1" + where
		if r.window != "" {
			query = "SELECT COUNT(*), MIN(" + r.window + "), MAX(" + r.window + ") FROM " + r.table + " WHERE 1 = 1" + where
		}
		var n int64
		var first, last sql.NullInt64
		dest := []interface{}{&n}
		if strings.Contains(query, "MIN(") {
			dest = append(dest, &first, &last)
		}
		if err := s.DB.QueryRowContext(ctx, s.bind(query), args...).Scan(dest...); err != nil {
			return nil, err
		}
		out.Tables = append(out.Tables, TableCount{Table: r.table, Rows: n})
		if r.table != "monitoring_kv" {
			out.Records += n
		}
		if first.Valid && (out.First == 0 || first.Int64 < out.First) {
			out.First = first.Int64
		}
		if last.Valid && last.Int64 > out.Last {
			out.Last = last.Int64
		}
	}
	return out, nil
}

// lastCopy reads when the last complete copy from source id began, from the
// target's marker.
func lastCopy(ctx context.Context, target Store, id string) int64 {
	value, found, err := target.getKV(ctx, target.DB, runKey(id))
	if err != nil || !found {
		return 0
	}
	m := runMarker{}
	if json.Unmarshal([]byte(value), &m) != nil || !m.Finished {
		return 0
	}
	return m.End
}

func localBytes() int64 {
	total := int64(0)
	for _, suffix := range []string{"", "-wal"} {
		if info, err := os.Stat(utils.LocalMonitoringFile() + suffix); err == nil {
			total += info.Size()
		}
	}
	return total
}

// summaryOf answers at once with what was counted last (valid false when
// nothing was yet). When that is older than 30 s and refresh is set, one count
// starts in the background (one per direction at a time, bounded by 20 s);
// counting says one runs. A view never waits for a count.
func summaryOf(direction string, refresh bool) (summary *Summary, problem string, valid bool, isCounting bool) {
	summaryMu.Lock()
	defer summaryMu.Unlock()
	cached := summaries[direction]
	fresh := cached.valid && time.Since(cached.at) < summaryCache
	if !fresh && refresh && !counting[direction] {
		counting[direction] = true
		generation := summaryGen
		summaryWG.Add(1)
		go func() {
			defer summaryWG.Done()
			summary, problem := computeSummaryFn(direction)
			summaryMu.Lock()
			defer summaryMu.Unlock()
			counting[direction] = false
			if generation == summaryGen {
				summaries[direction] = cachedSummary{at: time.Now(), summary: summary, problem: problem, valid: true}
			}
		}()
	}
	return cached.summary, cached.problem, cached.valid, counting[direction]
}

func computeSummary(direction string) (*Summary, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	write, read, err := inUse()
	if err != nil {
		return nil, "not-postgres"
	}
	end := time.Now().UnixMilli()
	switch direction {
	case ToPostgres:
		if !localExists() {
			return nil, "no-local-file"
		}
		local, err := openLocal(true)
		if err != nil {
			return nil, "unreadable"
		}
		defer local.DB.Close()
		summary, err := summarize(ctx, local, end)
		if err != nil {
			return nil, "unreadable"
		}
		summary.Bytes = localBytes()
		summary.LastCopy = lastCopy(ctx, write, sourceID(ToPostgres))
		if summary.Records == 0 {
			return summary, "empty"
		}
		return summary, ""
	default:
		summary, err := summarize(ctx, read, end)
		if err != nil {
			return nil, "unreadable"
		}
		if localExists() {
			if local, err := openLocal(true); err == nil {
				summary.LastCopy = lastCopy(ctx, local, sourceID(ToLocal))
				local.DB.Close()
			}
		}
		if summary.Records == 0 {
			return summary, "empty"
		}
		return summary, ""
	}
}

// Offer is one direction a copy can take now.
type Offer struct {
	Direction string   `json:"direction"`
	Available bool     `json:"available"`
	Problem   string   `json:"problem,omitempty"` // not-postgres, no-local-file, empty, unreadable
	Source    *Summary `json:"source,omitempty"`
	// Counting: the source's records are being counted in the background;
	// Source is then what was counted before, or nil
	Counting bool `json:"counting,omitempty"`
}

// View is what GET /api/monitoring-copy answers.
type View struct {
	InUse       string          `json:"inUse"` // postgres, local or closed
	Running     bool            `json:"running"`
	Direction   string          `json:"direction,omitempty"`
	Phase       string          `json:"phase,omitempty"`
	StartedAt   *time.Time      `json:"startedAt,omitempty"`
	Cancelling  bool            `json:"cancelling,omitempty"`
	Tables      []TableProgress `json:"tables,omitempty"`
	SecondsLeft int64           `json:"secondsLeft"` // -1 when not known
	Last        *Result         `json:"last,omitempty"`
	Offers      []Offer         `json:"offers"`
}

// Status gathers the view at once. The summaries are counted in the
// background at most every 30 seconds, and never while a copy runs (the last
// ones are shown).
func Status() View {
	v := View{InUse: "closed", SecondsLeft: -1, Offers: []Offer{}}
	if _, err := utils.MonitoringWriteDB(); err == nil {
		v.InUse = "local"
		if utils.MonitoringDialect() == utils.DialectPostgres {
			v.InUse = "postgres"
		}
	}
	state.mu.Lock()
	running := state.running
	if running {
		started := state.started
		v.Running, v.Direction, v.StartedAt, v.Cancelling = true, state.direction, &started, state.cancelling
		v.Phase, v.Tables = state.track.Snapshot()
		if rate := state.track.Rate(); rate > 0 && v.Phase == "copying" {
			left := int64(0)
			for _, p := range v.Tables {
				if p.Total > p.Done {
					left += p.Total - p.Done
				}
			}
			v.SecondsLeft = int64(float64(left)/rate) + 1
		}
	}
	if state.last != nil {
		last := *state.last
		v.Last = &last
	}
	state.mu.Unlock()

	for _, direction := range []string{ToPostgres, ToLocal} {
		offer := Offer{Direction: direction}
		if v.InUse != "postgres" {
			offer.Problem = "not-postgres"
			v.Offers = append(v.Offers, offer)
			continue
		}
		var valid bool
		offer.Source, offer.Problem, valid, offer.Counting = summaryOf(direction, !running)
		offer.Available = valid && offer.Problem == "" && !running
		v.Offers = append(v.Offers, offer)
	}
	return v
}
