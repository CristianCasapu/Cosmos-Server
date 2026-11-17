package monitorcopy

// The size of a real local file: 2 million metric values and 100 000 events
// (the owner's file is about 105 MB), copied into PostgreSQL with the
// production batch and pause. Opt-in, it takes minutes:
//   MONITORCOPY_BIG=1 COSMOS_FORK_PG_TEST_DSN=... go test ./src/monitorcopy/ -run Big -v -timeout 60m

import (
	"bufio"
	"context"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"runtime"
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
		opt := Options{SourceID: "big", TargetInUse: true, Batch: batchSize, Pause: pauseFor}
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
