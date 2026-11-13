package security

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The recorder. Record is what a request pays: a catalogue lookup, cleaning,
// the limiter and a send into a queue that never blocks. One goroutine takes
// what is queued, writes the Fail2ban lines, stores the rows in one
// transaction and hands them to the subscribers.
const (
	// queueSize bounds the events waiting to be written. When the queue is
	// full (the database does not answer) events are dropped and counted.
	queueSize = 1024
	// maxBatch is how many events one transaction stores.
	maxBatch = 200
	// maxUnsaved bounds the events kept for another try while the database
	// cannot be written (not opened yet, busy, broken).
	maxUnsaved = 1024
	// how often summaries are looked for, and old rows deleted
	summaryInterval = 15 * time.Second
	pruneInterval   = 10 * time.Minute
)

type queued struct {
	event Event
	// barrier is closed once everything queued before it has been written
	barrier chan struct{}
}

// Recorder is one instance of the record. The server has one, started by
// Start; tests make their own.
type Recorder struct {
	queue   chan queued
	limiter *limiter
	log     *logFile
	subs    subscribers
	now     func() time.Time

	dropped atomic.Int64 // events lost: the queue or the retry buffer was full
	refused atomic.Int64 // events of a kind that is not in the catalogue

	// of the writer goroutine only
	unsaved   []Event
	lastPrune time.Time

	stop    chan struct{}
	stopped chan struct{}
	once    sync.Once
}

func newRecorder(logPath func() string, now func() time.Time) *Recorder {
	return newRecorderSized(logPath, now, queueSize)
}

func newRecorderSized(logPath func() string, now func() time.Time, queue int) *Recorder {
	return &Recorder{
		queue:   make(chan queued, queue),
		limiter: newLimiter(),
		log:     newLogFile(logPath, logMaxBytes),
		now:     now,
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
}

var (
	std     atomic.Pointer[Recorder]
	startMu sync.Mutex
)

// Start starts the server's recorder and connects utils.RecordSecurity to
// it. Calling it again does nothing.
func Start() {
	startMu.Lock()
	defer startMu.Unlock()
	if std.Load() != nil {
		return
	}
	recorder := newRecorder(func() string { return utils.CONFIGFOLDER + LogFileName }, time.Now)
	go recorder.run()
	std.Store(recorder)
	utils.SetSecurityRecorder(Record, Flush)
}

// Record adds an event to the record. It returns at once: the event is
// cleaned, counted against the limits and queued. Kinds that are not in the
// catalogue are refused.
func Record(event Event) {
	if recorder := std.Load(); recorder != nil {
		recorder.Record(event)
	}
}

// Flush waits until everything recorded so far has been written. For tests
// and for an orderly stop; a request never calls it.
func Flush() {
	if recorder := std.Load(); recorder != nil {
		recorder.Flush()
	}
}

// Subscribe registers a function that receives every event after it was
// written (stored and, for incidents, logged), in order, with ID set (0 when
// the database could not store it yet). The function runs in a goroutine of
// its own and gets its own copy of each event. It can neither delay nor
// crash the recorder: when it is slower than the events come, at most 256
// wait for it and the rest is skipped; when it panics, the panic is logged
// and the next event is delivered. The returned function ends the
// subscription. Subscribe may be called before Start.
func Subscribe(name string, handle func(Event)) (unsubscribe func()) {
	Start()
	return std.Load().subs.subscribe(name, handle)
}

// Record is Record for one recorder.
func (r *Recorder) Record(event Event) {
	defer func() {
		// whatever goes wrong here must not reach the request that reported
		if failure := recover(); failure != nil {
			r.dropped.Add(1)
		}
	}()

	kind, known := catalogue[event.Kind]
	if !known {
		r.refused.Add(1)
		return
	}
	now := r.now()
	event = clean(event, kind, now)
	if !r.limiter.allow(event, kind, now) {
		return
	}
	select {
	case r.queue <- queued{event: event}:
	default:
		r.dropped.Add(int64(event.Count))
	}
}

// Flush waits until everything recorded before the call has been written.
func (r *Recorder) Flush() {
	barrier := make(chan struct{})
	select {
	case r.queue <- queued{barrier: barrier}:
	case <-r.stopped:
		return
	}
	select {
	case <-barrier:
	case <-r.stopped:
	}
}

// Stop writes what is queued and counted, and ends the recorder.
func (r *Recorder) Stop() {
	r.once.Do(func() { close(r.stop) })
	<-r.stopped
}

func (r *Recorder) run() {
	defer close(r.stopped)
	ticker := time.NewTicker(summaryInterval)
	defer ticker.Stop()

	for {
		select {
		case first := <-r.queue:
			r.writeQueued(first)
		case <-ticker.C:
			r.housekeeping(false)
		case <-r.stop:
			for {
				select {
				case next := <-r.queue:
					r.writeQueued(next)
					continue
				default:
				}
				break
			}
			r.housekeeping(true)
			r.log.mu.Lock()
			r.log.close()
			r.log.mu.Unlock()
			return
		}
	}
}

// writeQueued writes first and whatever else is waiting, a batch at a time.
func (r *Recorder) writeQueued(first queued) {
	batch := make([]Event, 0, 16)
	barriers := []chan struct{}{}
	add := func(item queued) {
		if item.barrier != nil {
			barriers = append(barriers, item.barrier)
			return
		}
		batch = append(batch, item.event)
	}

	add(first)
	for len(batch) < maxBatch {
		select {
		case next := <-r.queue:
			add(next)
			continue
		default:
		}
		break
	}

	r.write(batch)
	for _, barrier := range barriers {
		close(barrier)
	}
}

// housekeeping writes the summaries that are due and the count of lost
// events, and prunes now and then.
func (r *Recorder) housekeeping(stopping bool) {
	now := r.now()
	rows := []Event{}
	for _, summary := range r.limiter.summaries(now, stopping) {
		if kind, known := catalogue[summary.Kind]; known {
			rows = append(rows, clean(summary, kind, now))
		}
	}
	if lost := r.dropped.Swap(0); lost > 0 {
		rows = append(rows, clean(Event{Kind: utils.SecRecorderDropped, Count: int(lost)}, catalogue[utils.SecRecorderDropped], now))
	}
	// with nothing new, events that could not be stored earlier get their
	// next try here
	if len(rows) > 0 || len(r.unsaved) > 0 {
		r.write(rows)
	}

	if now.Sub(r.lastPrune) >= pruneInterval && !stopping {
		r.lastPrune = now
		if _, err := prune(now); err != nil && !errors.Is(err, utils.ErrDatabaseClosed) {
			utils.Debug("Security events: pruning failed: " + err.Error())
		}
	}
}

// write is the one place events leave the recorder: to the Fail2ban file,
// to the table, to the subscribers. Each of the three is independent of the
// other two failing.
func (r *Recorder) write(batch []Event) {
	defer func() {
		if failure := recover(); failure != nil {
			utils.Error("Security events: writing failed unexpectedly", fmt.Errorf("%v", failure))
		}
	}()

	if err := r.log.writeEvents(batch); err != nil {
		utils.Debug("Security events: the log file could not be written: " + err.Error())
	}

	// events that could not be stored before go first: the table keeps the
	// order they happened in
	toStore := batch
	if len(r.unsaved) > 0 {
		toStore = append(r.unsaved, batch...)
	}
	if err := insert(toStore); err != nil {
		if !errors.Is(err, utils.ErrDatabaseClosed) {
			utils.Debug("Security events: the events could not be stored: " + err.Error())
		}
		if over := len(toStore) - maxUnsaved; over > 0 {
			for _, lost := range toStore[:over] {
				r.dropped.Add(int64(lost.Count))
			}
			toStore = toStore[over:]
		}
		r.unsaved = append([]Event(nil), toStore...)
	} else {
		// insert set the IDs in toStore; batch is its tail
		if len(r.unsaved) > 0 {
			copy(batch, toStore[len(r.unsaved):])
		}
		r.unsaved = nil
	}

	r.subs.publish(batch)
}
