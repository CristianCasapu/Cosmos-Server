package watch

import (
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// recorder catches what the watches write and resolve.
type recorder struct {
	written  []utils.Notification
	resolved []string
}

func record(t *testing.T) *recorder {
	t.Helper()
	r := &recorder{}
	prevWrite, prevResolve := write, resolveThread
	write = func(n utils.Notification) { r.written = append(r.written, n) }
	resolveThread = func(thread string) { r.resolved = append(r.resolved, thread) }
	t.Cleanup(func() { write, resolveThread = prevWrite, prevResolve })
	return r
}

func (r *recorder) kinds() string {
	kinds := []string{}
	for _, n := range r.written {
		kinds = append(kinds, n.Fork.Kind)
	}
	return strings.Join(kinds, ",")
}

func TestSettingsAndTheirChecks(t *testing.T) {
	s := SettingsOf(utils.WatchConfig{})
	if !s.SMART || !s.Fill || !s.Docker || !s.Snapshots || !s.Country || s.FillDays != 3 || s.SnapshotLate != 0 {
		t.Errorf("defaults: %+v", s)
	}
	s = SettingsOf(utils.WatchConfig{FillDays: 99, SnapshotLateHours: 48, CountryOff: true})
	if s.FillDays != 3 || s.SnapshotLate != 48*time.Hour || s.Country {
		t.Errorf("%+v", s)
	}
	for _, bad := range []utils.WatchConfig{{FillDays: -1}, {FillDays: 31}, {SnapshotLateHours: -2}, {SnapshotLateHours: 24*90 + 1}} {
		if Validate(&bad) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if Validate(&utils.WatchConfig{FillDays: 7, SnapshotLateHours: 30}) != nil {
		t.Error("refused good settings")
	}
}
