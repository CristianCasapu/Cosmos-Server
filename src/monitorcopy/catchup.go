package monitorcopy

// The catch-up after a fallback (plan 51, S4b, the owner's decision of
// 2026-11-18). While PostgreSQL did not answer, the store's writers wrote
// into the local file; when PostgreSQL is the store again (whatever made it
// so), utils/fork_monitoring_fallback.go calls MonitoringCatchUp with the
// fallback's window: the rows the writers inserted into the local file
// during the fallback (by id, whatever time they carry: review round 3, S1)
// and the rows touched in its time (metric rows, rows changed in place) are
// copied into PostgreSQL by the copy's own machinery, metric buckets both
// stores hold merged exactly once (buckets.go, S2): paced, idempotent,
// verified row by row and value by value, in the one slot a copy has. It is
// the one copy that runs without an administrator's click.
//
// The windows not yet caught up are kept in PostgreSQL's monitoring_kv
// (key fork_monitoring_copy:catchup), so that a restart, a new outage or a
// cancel does not lose them: each time PostgreSQL becomes the store
// (OnMonitoringStorePostgres, at start too) the pending ones are taken up
// again. A copy started by hand has the slot: the catch-up is then skipped,
// the window stays pending for the next time.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// CatchUp is the direction of the automatic catch-up (into PostgreSQL).
const CatchUp = "catch-up"

const catchUpKey = markerPrefix + "catchup"

// margin around a window: the switch's drain, the clocks of the writers
const catchUpMargin = 5 * time.Second

// Window is what a catch-up brings over (unix ms): the rows touched from
// From to To, the rows with ids in (After, Before] per table; Back is when
// PostgreSQL became the store again. A window of an older version has times
// only.
type Window struct {
	From   int64            `json:"from"`
	To     int64            `json:"to"`
	Back   int64            `json:"back,omitempty"`
	After  map[string]int64 `json:"after,omitempty"`
	Before map[string]int64 `json:"before,omitempty"`
}

func (w Window) same(o Window) bool {
	a, _ := json.Marshal(w)
	b, _ := json.Marshal(o)
	return string(a) == string(b)
}

// touches: o follows w without a row of the local file between them (the
// local file got no row between the two fallbacks): one run takes both.
func (w Window) touches(o Window) bool {
	if w.After == nil || o.After == nil || w.Before == nil {
		return o.From <= w.To // times only
	}
	for _, t := range utils.FallbackTables() {
		if o.After[t] > w.Before[t] || o.After[t] < w.After[t] {
			return false
		}
	}
	return true
}

// ids are the window's id ranges for the copy.
func (w Window) ids() map[string][2]int64 {
	if w.After == nil || w.Before == nil {
		return nil
	}
	out := map[string][2]int64{}
	for t, a := range w.After {
		out[t] = [2]int64{a, w.Before[t]}
	}
	return out
}

func init() {
	utils.MonitoringCatchUp = CatchUpAfterFallback
	utils.OnMonitoringStorePostgres = ResumeCatchUp
}

type waiting struct {
	window Window
	done   func(utils.CatchUpResult)
}

var (
	catchUpMu     sync.Mutex
	catchUpQueue  []waiting // windows given since the worker last saved them
	catchUpWorker bool
	catchUpWG     sync.WaitGroup // the worker, for the tests
)

// once wraps done so that it is called exactly once.
func once(done func(utils.CatchUpResult)) func(utils.CatchUpResult) {
	var o sync.Once
	return func(r utils.CatchUpResult) {
		if done != nil {
			o.Do(func() { done(r) })
		}
	}
}

// CatchUpAfterFallback is utils.MonitoringCatchUp. It returns at once; done
// is called exactly once, from the background.
func CatchUpAfterFallback(cw utils.CatchUpWindow, done func(utils.CatchUpResult)) {
	done = once(done)
	w := Window{From: cw.From.UnixMilli(), To: cw.To.UnixMilli(), After: cw.After, Before: cw.Before}
	if !cw.Back.IsZero() {
		w.Back = cw.Back.UnixMilli()
	}
	if cw.From.IsZero() || !cw.To.After(cw.From) {
		done(utils.CatchUpResult{Ran: true})
		return
	}
	state.mu.Lock()
	manual := state.running && state.direction != CatchUp
	if manual {
		r := Result{Direction: CatchUp, Outcome: "skipped", FinishedAt: time.Now(), From: w.From, To: w.To,
			Message: "A copy started by hand was running: the records written while the database was away were not caught up now; Cosmos takes them up the next time PostgreSQL becomes the store."}
		state.catchUp = &r
	}
	state.mu.Unlock()
	catchUpMu.Lock()
	catchUpQueue = append(catchUpQueue, waiting{window: w, done: done})
	catchUpMu.Unlock()
	if manual {
		utils.Log("Monitoring history catch-up: skipped, a copy started by hand is running")
		// kept for the next time; the one waiting is told now
		catchUpMu.Lock()
		catchUpQueue[len(catchUpQueue)-1].done = nil
		catchUpMu.Unlock()
		done(utils.CatchUpResult{Ran: false})
		go func() { saveQueued() }()
		return
	}
	startWorker()
}

// ResumeCatchUp is utils.OnMonitoringStorePostgres: the windows still
// pending in PostgreSQL are taken up again.
func ResumeCatchUp() { startWorker() }

func startWorker() {
	catchUpMu.Lock()
	defer catchUpMu.Unlock()
	if catchUpWorker {
		return
	}
	catchUpWorker = true
	catchUpWG.Add(1)
	go func() {
		defer catchUpWG.Done()
		catchUpWork()
	}()
}

// pending reads the windows kept in the store.
func pendingWindows(ctx context.Context, s Store) ([]Window, error) {
	value, found, err := s.getKV(ctx, s.DB, catchUpKey)
	if err != nil || !found {
		return nil, err
	}
	out := []Window{}
	if json.Unmarshal([]byte(value), &out) != nil {
		return nil, nil
	}
	return out, nil
}

func savePending(ctx context.Context, s Store, windows []Window) error {
	encoded, _ := json.Marshal(windows)
	return s.setKV(ctx, s.DB, catchUpKey, string(encoded))
}

// merge sorts windows and joins those that overlap or touch (in time, or
// in the local file's ids: no row of the file between them), so that a
// backlog after flapping is one run (review round 3, M6).
func merge(windows []Window) []Window {
	sort.Slice(windows, func(i, j int) bool { return windows[i].From < windows[j].From })
	out := []Window{}
	for _, w := range windows {
		if w.To <= w.From {
			continue
		}
		if n := len(out); n > 0 && out[n-1].touches(w) {
			p := &out[n-1]
			p.To, p.Back = max(p.To, w.To), max(p.Back, w.Back)
			if p.After != nil && w.After != nil {
				for t := range w.After {
					p.After[t] = min(p.After[t], w.After[t])
					p.Before[t] = max(p.Before[t], w.Before[t])
				}
			} else {
				p.After, p.Before = nil, nil
			}
			continue
		}
		c := w
		if w.After != nil {
			c.After, c.Before = map[string]int64{}, map[string]int64{}
			for t, v := range w.After {
				c.After[t] = v
			}
			for t, v := range w.Before {
				c.Before[t] = v
			}
		}
		out = append(out, c)
	}
	return out
}

// saveQueued moves the queued windows into the store's list. It returns the
// list and the callbacks waiting, by window.
func saveQueued() ([]Window, []waiting, Store, Store, error) {
	write, read, err := storeInUse()
	if err != nil {
		return nil, nil, Store{}, Store{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	catchUpMu.Lock()
	queued := catchUpQueue
	catchUpQueue = nil
	catchUpMu.Unlock()
	windows, err := pendingWindows(ctx, write)
	if err == nil {
		for _, q := range queued {
			windows = append(windows, q.window)
		}
		windows = merge(windows)
		err = savePending(ctx, write, windows)
	}
	if err != nil {
		// back into the queue: nothing given is lost
		catchUpMu.Lock()
		catchUpQueue = append(queued, catchUpQueue...)
		catchUpMu.Unlock()
		return nil, nil, Store{}, Store{}, err
	}
	return windows, queued, write, read, nil
}

func tell(waiters []waiting, r utils.CatchUpResult) {
	for _, w := range waiters {
		if w.done != nil {
			w.done(r)
		}
	}
}

// catchUpWork takes the pending windows one after the other, oldest first,
// while PostgreSQL is the store and the slot is free.
func catchUpWork() {
	var waiters []waiting
	defer func() {
		catchUpMu.Lock()
		catchUpWorker = false
		late := len(catchUpQueue) > 0
		catchUpMu.Unlock()
		// whoever is still waiting hears that nothing ran
		tell(waiters, utils.CatchUpResult{Ran: false})
		if late {
			// given while the worker was finishing
			if _, _, err := storeInUse(); err == nil {
				startWorker()
			}
		}
	}()
	for {
		windows, queued, write, read, err := saveQueued()
		waiters = append(waiters, queued...)
		if err != nil || len(windows) == 0 {
			if err == nil {
				tell(waiters, utils.CatchUpResult{Ran: true})
				waiters = nil
			}
			return
		}
		w := windows[0]
		// the waiters of this window (and of windows merged into it)
		mine, rest := []waiting{}, []waiting{}
		for _, q := range waiters {
			if q.window.From < w.To && q.window.To > w.From || q.window.From == w.From || w.touches(q.window) {
				mine = append(mine, q)
			} else {
				rest = append(rest, q)
			}
		}
		waiters = rest
		result, ran := runWindow(w, write, read)
		if !ran {
			tell(mine, utils.CatchUpResult{Ran: false})
			return
		}
		caught := utils.CatchUpResult{Ran: true, Counted: result.Counted, Aside: result.Aside, Copied: result.Written, Present: result.Present,
			Missing: result.Missing, Unchecked: result.Unchecked, Inexact: result.Inexact}
		if result.Outcome != "done" {
			caught.Problem = result.Message
		}
		removed := false
		if result.Outcome == "done" {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			left, err := pendingWindows(ctx, write)
			if err == nil {
				kept := []Window{}
				for _, x := range left {
					if !x.same(w) {
						kept = append(kept, x)
					}
				}
				if len(kept) < len(left) {
					err = savePending(ctx, write, kept)
					removed = err == nil
				}
			}
			cancel()
		}
		tell(mine, caught)
		if !removed {
			// kept (not done, or its removal failed): taken up again the next
			// time PostgreSQL becomes the store, never in a loop now
			return
		}
	}
}

func catchUpSourceID(w Window) string {
	sum := sha256.Sum256([]byte("sqlite:" + utils.LocalMonitoringFile()))
	return CatchUp + ":" + hex.EncodeToString(sum[:8]) + ":" + strconv.FormatInt(w.From, 10) + "-" + strconv.FormatInt(w.To, 10)
}

// runWindow catches one window up, in the copy's slot. ran is false when the
// slot was taken by a copy started by hand.
func runWindow(w Window, write, read Store) (Result, bool) {
	state.mu.Lock()
	if state.running {
		state.mu.Unlock()
		utils.Log("Monitoring history catch-up: waits, a copy started by hand is running")
		return Result{}, false
	}
	if !localExists() {
		// no local file: what was written aside cannot be read (review round 3, M4)
		r := Result{Direction: CatchUp, Outcome: "failed", FinishedAt: time.Now(), From: w.From, To: w.To,
			Message: "The local file (database.db) is not there any more: the records written into it while the database was away cannot be read."}
		state.catchUp = &r
		state.mu.Unlock()
		return r, true
	}
	local, err := openLocal(true)
	if err != nil {
		r := Result{Direction: CatchUp, Outcome: "failed", FinishedAt: time.Now(), From: w.From, To: w.To, Message: "The local file could not be opened."}
		state.catchUp = &r
		state.mu.Unlock()
		return r, true
	}
	opt := Options{
		SourceID:    catchUpSourceID(w),
		TargetInUse: true,
		Batch:       batchSize,
		BatchTime:   batchTime,
		Pause:       pauseFor,
		Guard:       guardFor(write, read),
		From:        w.From - catchUpMargin.Milliseconds(),
		End:         w.To + catchUpMargin.Milliseconds(),
		IDs:         w.ids(),
		Back:        w.Back,
	}
	utils.Log("Monitoring history catch-up: started")
	var final Result
	window := w
	done := launchLocked(CatchUp, local, write, local, opt, &window, func(r Result) {
		// true numbers (review round 3, M4): every row of the window is
		// present, missing, not mergeable exactly, or not checked
		r.Aside, r.Present, r.Missing, r.Unchecked, r.Inexact = 0, 0, 0, 0, 0
		for _, p := range r.Tables {
			r.Aside += p.Total
			r.Present += p.TargetRows
			r.Missing += p.Missing
			r.Inexact += p.Inexact
			if left := p.Total - p.TargetRows - p.Missing - p.Inexact; left > 0 {
				r.Unchecked += left
			}
		}
		if r.Outcome == "mismatch" {
			r.Message = "Some records written while the database was away are not in PostgreSQL after the catch-up; the copy on the External page brings over what is missing."
		}
		state.catchUp = &r
		final = r
	})
	state.mu.Unlock()
	<-done
	state.mu.Lock()
	r := final
	state.mu.Unlock()
	recordCatchUp(r)
	return r, true
}

// recordCatchUp writes the security record's entry of a catch-up: no user
// asked for it, Cosmos did it by itself.
func recordCatchUp(r Result) {
	outcome := "ok"
	if r.Outcome != "done" {
		outcome = "failed"
	}
	utils.RecordSecurityEvent(utils.SecurityEvent{
		Kind:    utils.SecMonitoringCopy,
		Outcome: outcome,
		Details: map[string]string{
			"action":    "catchup",
			"direction": ToPostgres,
			"copied":    strconv.FormatInt(r.Written, 10),
			"missing":   strconv.FormatInt(r.Missing, 10),
			"from":      time.UnixMilli(r.From).UTC().Format(time.RFC3339),
			"to":        time.UnixMilli(r.To).UTC().Format(time.RFC3339),
		},
	})
	if r.Outcome == "done" {
		utils.Log("Monitoring history catch-up: finished, " + itoa(r.Written) + " rows written")
	}
}
