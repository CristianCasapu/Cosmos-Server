package security

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// a clock the tests move by hand
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock {
	return &testClock{t: time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)}
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// useFolder points the configuration folder at an empty one for the test.
func useFolder(t *testing.T) string {
	t.Helper()
	previous := utils.CONFIGFOLDER
	folder := t.TempDir() + string(os.PathSeparator)
	utils.CONFIGFOLDER = folder
	t.Cleanup(func() { utils.CONFIGFOLDER = previous })
	return folder
}

// openDatabase opens a monitoring database in a folder of its own.
func openDatabase(t *testing.T) string {
	t.Helper()
	folder := useFolder(t)
	if err := utils.InitMetricsDatabase(); err != nil {
		t.Fatal("InitMetricsDatabase:", err)
	}
	t.Cleanup(utils.CloseMetricsDatabase)
	return folder
}

// startRecorder runs a recorder that writes its log file into folder.
func startRecorder(t *testing.T, folder string, now func() time.Time) *Recorder {
	t.Helper()
	recorder := newRecorder(func() string { return filepath.Join(folder, LogFileName) }, now)
	go recorder.run()
	t.Cleanup(recorder.Stop)
	return recorder
}

// all returns every stored event, oldest first.
func all(t *testing.T) []Event {
	t.Helper()
	events := []Event{}
	cursor := int64(0)
	for {
		page, err := List(Filter{}, cursor, MaxPageSize)
		if err != nil {
			t.Fatal("List:", err)
		}
		events = append(events, page.Events...)
		if page.NextCursor == 0 {
			break
		}
		cursor = page.NextCursor
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events
}

func readLog(t *testing.T, folder string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(folder, LogFileName))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// startServerRecorder starts the server's own recorder, the one Start makes
// and utils.RecordSecurity writes to, and takes it away again when the test
// ends: left running, its housekeeping and its limits would reach into the
// tests that follow.
func startServerRecorder(t *testing.T) {
	t.Helper()
	Start()
	t.Cleanup(func() {
		if recorder := std.Swap(nil); recorder != nil {
			recorder.Stop()
		}
		utils.SetSecurityRecorder(nil, nil)
	})
}
