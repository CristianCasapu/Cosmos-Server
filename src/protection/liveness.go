package protection

import (
	"os"
	"time"
)

// The guard's two liveness marks (fork/nftables/cosmos-host-guard): the
// watcher touches alivePath at least every 10 s, every refresh (the timer
// runs one every minute) touches refreshPath. Only their modification times
// are read, never their content, and never through a link. Variables so
// that tests point them elsewhere.
var (
	alivePath   = "/run/cosmos-host-guard/alive"
	refreshPath = "/run/cosmos-host-guard/last-refresh"
)

const (
	// watcherStaleAfter: the watcher touches its mark every 10 s at most, so
	// a minute without means it does not run.
	watcherStaleAfter = 60 * time.Second
	// refreshStaleAfter: the timer runs a refresh every minute.
	refreshStaleAfter = 3 * time.Minute
)

// Liveness says whether the guard still follows the network.
type Liveness struct {
	// WatcherAt, RefreshAt are the marks' times, nil when a mark is not there
	// (or not a regular file): not known.
	WatcherAt *time.Time `json:"watcherAt,omitempty"`
	RefreshAt *time.Time `json:"refreshAt,omitempty"`
	// WatcherStale: the watcher has not run for more than a minute.
	WatcherStale bool `json:"watcherStale"`
	// RefreshStale: no refresh for more than three minutes.
	RefreshStale bool `json:"refreshStale"`
}

// markTime is the modification time of a mark, nil when it is not a regular
// file.
func markTime(path string) *time.Time {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	at := info.ModTime().UTC().Truncate(time.Second)
	return &at
}

// readLiveness reads the two marks at the time of a reading. A mark that is
// missing is not known and never stale; a mark in the future is fresh.
func readLiveness(now time.Time) Liveness {
	live := Liveness{WatcherAt: markTime(alivePath), RefreshAt: markTime(refreshPath)}
	live.WatcherStale = live.WatcherAt != nil && now.Sub(*live.WatcherAt) > watcherStaleAfter
	live.RefreshStale = live.RefreshAt != nil && now.Sub(*live.RefreshAt) > refreshStaleAfter
	return live
}
