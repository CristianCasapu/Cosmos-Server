package monitorcopy

// The size of a real local file: 2 million metric values and 100 000 events
// (the owner's file is about 105 MB), copied into PostgreSQL with the
// production batch and pause. Opt-in, it takes minutes:
//   MONITORCOPY_BIG=1 COSMOS_FORK_PG_TEST_DSN=... go test ./src/monitorcopy/ -run Big -v -timeout 60m

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func peakRSS() string {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return "?"
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "VmHWM:") {
			return strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "VmHWM:"))
		}
	}
	return "?"
}

func fileSum(t *testing.T, path string) [32]byte {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	io.Copy(h, f)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func TestBigCopy(t *testing.T) {
	if os.Getenv("MONITORCOPY_BIG") == "" {
		t.Skip("MONITORCOPY_BIG is not set")
	}
	path := filepath.Join(t.TempDir(), "database.db")
	local, err := openLocalAt(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := utils.PrepareMonitoringSchema(local.DB, local.Dialect); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	end := time.Now().UnixMilli() - 3600000
	// 400 metrics x 5000 hourly buckets = 2 000 000 values
	fill(t, local, contents{node: "server.local", metrics: 400, values: 5000, events: 100000, notifications: 5000, security: 20000, end: end})
	local.DB.Close()
	info, _ := os.Stat(path)
	t.Logf("generated the local file in %s: %d MB", time.Since(started).Round(time.Second), info.Size()>>20)
	before := fileSum(t, path)

	dst := openStore(t, utils.DialectPostgres)
	dst.DB.SetMaxOpenConns(8) // as the store in use

	run := func(label string) Report {
		src, err := openLocalAt(path, true)
		if err != nil {
			t.Fatal(err)
		}
		defer src.DB.Close()
		runtime.GC()
		var peakHeap, peakSys atomic.Uint64
		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			var m runtime.MemStats
			for {
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > peakHeap.Load() {
					peakHeap.Store(m.HeapAlloc)
				}
				if m.Sys > peakSys.Load() {
					peakSys.Store(m.Sys)
				}
				select {
				case <-stop:
					return
				case <-time.After(100 * time.Millisecond):
				}
			}
		}()
		track := &Tracker{}
		opt := Options{SourceID: "big", TargetInUse: true, Batch: batchSize, BatchTime: batchTime, Pause: pauseFor}
		begin := time.Now()
		var copyDone atomic.Int64
		done := make(chan struct{})
		go func() {
			for {
				select {
				case <-done:
					return
				case <-time.After(200 * time.Millisecond):
					if phase, _ := track.Snapshot(); phase == "verifying" && copyDone.Load() == 0 {
						copyDone.Store(int64(time.Since(begin)))
					}
				}
			}
		}()
		report, err := Copy(context.Background(), src, dst, opt, track)
		close(done)
		close(stop)
		wg.Wait()
		if err != nil {
			t.Fatalf("%s: %v (%v)", label, err, unwrap(err))
		}
		total := int64(0)
		for _, p := range report.Tables {
			total += p.Total
		}
		t.Logf("%s: %d rows gone through, %d written, mismatch %v; %s in all (copy %s, then the check), peak heap %d MB, Go runtime from the OS %d MB, peak RSS of the test process %s",
			label, total, report.Written, report.Mismatch, time.Since(begin).Round(time.Second), time.Duration(copyDone.Load()).Round(time.Second),
			peakHeap.Load()>>20, peakSys.Load()>>20, peakRSS())
		for _, p := range report.Tables {
			t.Logf("  %-22s total %8d written %8d state %-9s source %8d target %8d missing %d", p.Table, p.Total, p.Written, p.State, p.SourceRows, p.TargetRows, p.Missing)
		}
		if report.Mismatch {
			t.Fatalf("%s: mismatch", label)
		}
		return report
	}
	first := run("first copy")
	if first.Written < 2_100_000 {
		t.Errorf("written %d", first.Written)
	}
	again := run("second copy")
	if again.Written != 0 {
		t.Errorf("the second copy wrote %d rows", again.Written)
	}
	if fileSum(t, path) != before {
		t.Fatal("the local file changed")
	}
}

// TestFillForBrowser writes a local file with a history for a browser check
// of the card on a throwaway instance (the instance stopped meanwhile):
//
//	MONITORCOPY_FILL=/path/to/database.db go test ./src/monitorcopy/ -run FillForBrowser
func TestFillForBrowser(t *testing.T) {
	path := os.Getenv("MONITORCOPY_FILL")
	if path == "" {
		t.Skip("MONITORCOPY_FILL is not set")
	}
	local, err := openLocalAt(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer local.DB.Close()
	if err := utils.PrepareMonitoringSchema(local.DB, local.Dialect); err != nil {
		t.Fatal(err)
	}
	fill(t, local, contents{node: "server.local", metrics: 100, values: 3000, events: 20000, notifications: 500, security: 3000, end: time.Now().UnixMilli() - 3600000})
}

// TestWriterLatency measures how long a writer of the target waits while a
// copy writes into it, as the store's writers (metrics, events) would:
// p50/p99/max of one event insert and one bucket upsert, every 10 ms, before
// and during a copy of 200 000 metric values and 20 000 events, both ways.
//
//	MONITORCOPY_LATENCY=1 COSMOS_FORK_PG_TEST_DSN=... go test ./src/monitorcopy/ -run WriterLatency -v
func TestWriterLatency(t *testing.T) {
	if os.Getenv("MONITORCOPY_LATENCY") == "" {
		t.Skip("MONITORCOPY_LATENCY is not set")
	}
	pgBase(t)
	if os.Getenv("MONITORCOPY_OLD") != "" {
		// the settings before the review: 1000 rows, a quarter of the batch's time as pause
		previousBatch, previousTime, previousPause := batchSize, batchTime, pauseFor
		t.Cleanup(func() { batchSize, batchTime, pauseFor = previousBatch, previousTime, previousPause })
		batchSize, batchTime = 1000, 0
		pauseFor = func(took time.Duration) time.Duration {
			wait := took / 4
			if wait < 2*time.Millisecond {
				wait = 2 * time.Millisecond
			}
			if wait > 250*time.Millisecond {
				wait = 250 * time.Millisecond
			}
			return wait
		}
		t.Log("old settings")
	}
	end := time.Now().UnixMilli() - 3600000
	history := contents{node: "server.local", metrics: 100, values: 2000, events: 20000, notifications: 2000, security: 4000, end: end}

	var track *Tracker
	measure := func(label string, writer Store, during func()) {
		byPhase := map[string][]time.Duration{}
		samples := func(stop <-chan struct{}) []time.Duration {
			out := []time.Duration{}
			i := 0
			for {
				select {
				case <-stop:
					return out
				case <-time.After(10 * time.Millisecond):
				}
				i++
				begin := time.Now()
				if _, err := writer.DB.Exec(writer.bind("INSERT INTO events(node, event_id, label, application, level, date, data, object, search) VALUES('writer', 'w', 'w', 'cosmos', 'info', ?, '', '', '')"), time.Now().UnixMilli()); err != nil {
					t.Errorf("%s: writer: %v", label, err)
				}
				if _, err := writer.DB.Exec(writer.bind("INSERT INTO metric_values(node, key, granularity, date, value, avg_index, expire) VALUES('writer', 'w', 'hourly', ?, 1, 1, 0)"+
					" ON CONFLICT(node, key, granularity, date) DO UPDATE SET value = metric_values.value + 1"), (time.Now().UnixMilli()/3600000)*3600000); err != nil {
					t.Errorf("%s: writer: %v", label, err)
				}
				took := time.Since(begin)
				out = append(out, took)
				if track != nil {
					phase, _ := track.Snapshot()
					byPhase[phase] = append(byPhase[phase], took)
				}
			}
		}
		run := func(work func()) []time.Duration {
			stop := make(chan struct{})
			got := make(chan []time.Duration)
			go func() { got <- samples(stop) }()
			work()
			close(stop)
			return <-got
		}
		stats := func(d []time.Duration) string {
			sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
			if len(d) == 0 {
				return "no samples"
			}
			at := func(q float64) time.Duration { return d[int(q*float64(len(d)-1))] }
			return fmt.Sprintf("p50 %s p99 %s max %s (%d samples)", at(0.5).Round(10*time.Microsecond), at(0.99).Round(10*time.Microsecond), d[len(d)-1].Round(time.Millisecond), len(d))
		}
		track = nil
		before := run(func() { time.Sleep(5 * time.Second) })
		track = &Tracker{}
		begin := time.Now()
		while := run(during)
		t.Logf("%s: before %s; during the copy (%s) %s", label, stats(before), time.Since(begin).Round(time.Second), stats(while))
		for phase, d := range byPhase {
			t.Logf("  %s, %s: %s", label, phase, stats(d))
		}
	}

	t.Run("to-postgres", func(t *testing.T) {
		src := openStore(t, utils.DialectSQLite)
		fill(t, src, history)
		dst := openStore(t, utils.DialectPostgres)
		dst.DB.SetMaxOpenConns(8) // as the store in use, which its writers share
		measure("to PostgreSQL", dst, func() {
			report, err := Copy(context.Background(), src, dst, Options{SourceID: "lat", TargetInUse: true, Batch: batchSize, BatchTime: batchTime, Pause: pauseFor}, track)
			if err != nil || report.Mismatch {
				t.Errorf("%v %+v", err, report)
			}
		})
	})
	t.Run("to-local", func(t *testing.T) {
		src := openStore(t, utils.DialectPostgres)
		fill(t, src, history)
		path := filepath.Join(t.TempDir(), "database.db")
		dst, err := openLocalAt(path, false)
		if err != nil {
			t.Fatal(err)
		}
		defer dst.DB.Close()
		if err := utils.PrepareMonitoringSchema(dst.DB, dst.Dialect); err != nil {
			t.Fatal(err)
		}
		// a second writer on the file, as the fallback would be: Cosmos' own settings
		writer, err := sql.Open(utils.DialectSQLite, "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)")
		if err != nil {
			t.Fatal(err)
		}
		writer.SetMaxOpenConns(1)
		defer writer.Close()
		measure("to the local file", Store{DB: writer, Dialect: utils.DialectSQLite}, func() {
			report, err := Copy(context.Background(), src, dst, Options{SourceID: "lat", TargetInUse: false, Batch: batchSize, BatchTime: batchTime, Pause: pauseFor}, track)
			if err != nil || report.Mismatch {
				t.Errorf("%v %+v", err, report)
			}
		})
	})
}
