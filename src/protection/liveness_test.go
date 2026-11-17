package protection

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// marks points the liveness marks into a folder of the test's own.
func marks(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	previous := [2]string{alivePath, refreshPath}
	alivePath, refreshPath = filepath.Join(dir, "alive"), filepath.Join(dir, "last-refresh")
	t.Cleanup(func() { alivePath, refreshPath = previous[0], previous[1] })
	return dir
}

func touch(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestLivenessOfTheWatcherAndTheTimer(t *testing.T) {
	marks(t)
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

	// missing: not known, never stale
	if live := readLiveness(now); live.WatcherAt != nil || live.RefreshAt != nil || live.WatcherStale || live.RefreshStale {
		t.Errorf("no marks: %+v", live)
	}

	for name, test := range map[string]struct {
		alive, refresh       time.Duration
		watcherOld, timerOld bool
	}{
		"both fresh":                {5 * time.Second, 40 * time.Second, false, false},
		"watcher a minute":          {60 * time.Second, 40 * time.Second, false, false},
		"watcher stale":             {61 * time.Second, 40 * time.Second, true, false},
		"both stale":                {10 * time.Minute, 3*time.Minute + time.Second, true, true},
		"timer at three minutes":    {10 * time.Minute, 3 * time.Minute, true, false},
		"timer stale, watcher runs": {5 * time.Second, 5 * time.Minute, false, true},
		"in the future":             {-time.Hour, -time.Hour, false, false},
	} {
		touch(t, alivePath, now.Add(-test.alive))
		touch(t, refreshPath, now.Add(-test.refresh))
		live := readLiveness(now)
		if live.WatcherStale != test.watcherOld || live.RefreshStale != test.timerOld {
			t.Errorf("%s: %+v", name, live)
		}
		if live.WatcherAt == nil || !live.WatcherAt.Equal(now.Add(-test.alive)) || live.RefreshAt == nil || !live.RefreshAt.Equal(now.Add(-test.refresh)) {
			t.Errorf("%s: times %v %v", name, live.WatcherAt, live.RefreshAt)
		}
	}
}

func TestLivenessMarksAreOnlyRegularFiles(t *testing.T) {
	dir := marks(t)
	now := time.Now()
	old := filepath.Join(dir, "old")
	touch(t, old, now.Add(-time.Hour))
	// a link to an old file, a folder: neither counts
	if err := os.Symlink(old, alivePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(refreshPath, 0700); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(refreshPath, now.Add(-time.Hour), now.Add(-time.Hour))
	if live := readLiveness(now); live.WatcherAt != nil || live.RefreshAt != nil || live.WatcherStale || live.RefreshStale {
		t.Errorf("a link and a folder: %+v", live)
	}
}
