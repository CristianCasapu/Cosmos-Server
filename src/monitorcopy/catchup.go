package monitorcopy

// The catch-up after a fallback (plan 51, S4b, the owner's decision of
// 2026-11-18). While PostgreSQL did not answer, the store's writers wrote
// into the local file; when PostgreSQL is the store again, the watcher
// (utils/fork_monitoring_store.go) calls MonitoringCatchUp with the window of
// the fallback, and the rows the local file got in it are copied into
// PostgreSQL by the copy's own machinery: by time (each table's touched
// column, Options.From), paced, idempotent, verified, in the one slot a copy
// has. It is the one copy that runs without an administrator's click.
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

// Window is a period whose rows of the local file are caught up (unix ms).
type Window struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
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
func CatchUpAfterFallback(from, to time.Time, done func(utils.CatchUpResult)) {
	done = once(done)
	w := Window{From: from.UnixMilli(), To: to.UnixMilli()}
	if from.IsZero() || !to.After(from) {
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

// merge sorts windows and joins those that overlap or touch.
func merge(windows []Window) []Window {
	sort.Slice(windows, func(i, j int) bool { return windows[i].From < windows[j].From })
	out := []Window{}
	for _, w := range windows {
		if w.To <= w.From {
			continue
		}
		if n := len(out); n > 0 && w.From <= out[n-1].To {
			if w.To > out[n-1].To {
				out[n-1].To = w.To
			}
			continue
		}
		out = append(out, w)
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
			if q.window.From < w.To && q.window.To > w.From || q.window.From == w.From {
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
		caught := utils.CatchUpResult{Ran: true, Copied: result.Written, Missing: result.Missing}
		if result.Outcome != "done" {
			caught.Problem = result.Message
			if caught.Missing == 0 {
				caught.Missing = 1 // at least: what was not gone through is not known
			}
		}
		removed := false
		if result.Outcome == "done" {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			left, err := pendingWindows(ctx, write)
			if err == nil {
				kept := []Window{}
				for _, x := range left {
					if x != w {
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
		// no local file: nothing was written aside
		r := Result{Direction: CatchUp, Outcome: "done", FinishedAt: time.Now(), From: w.From, To: w.To, UpTo: w.To}
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
	}
	utils.Log("Monitoring history catch-up: started")
	var final Result
	window := w
	done := launchLocked(CatchUp, local, write, local, opt, &window, func(r Result) {
		for _, p := range r.Tables {
			r.Missing += p.Missing
		}
		if r.Outcome != "done" && r.Outcome != "mismatch" {
			for _, p := range r.Tables {
				if p.Total > p.Done {
					r.Missing += p.Total - p.Done
				}
			}
		}
		if r.Outcome == "mismatch" {
			r.Message = "Some rows written while the database was away are not in PostgreSQL after the catch-up; the copy on the External page brings over what is missing."
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
