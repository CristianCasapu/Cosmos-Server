package snapshots

import (
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	defaultHour   = 3
	defaultMinute = 0
)

var (
	dueMu   sync.Mutex
	lastDue string // the minute a run was last asked for, as 2006-01-02 15:04
)

// scheduled reports whether the settings ask for a run in the minute of now.
// Frequency is "6h", "12h", "daily" (also when empty or unknown) or
// "weekly"; Time ("HH:MM", default 03:00) is the time of day it is counted
// from; Weekday (0 = Sunday) is the day of a weekly run.
func scheduled(cfg utils.SnapshotsConfig, now time.Time) bool {
	if !configured(cfg) {
		return false
	}
	hour, minute, ok := parseTime(cfg.Time)
	if !ok {
		hour, minute = defaultHour, defaultMinute
	}
	if now.Minute() != minute {
		return false
	}

	switch cfg.Frequency {
	case "6h":
		return (now.Hour()-hour+24)%6 == 0
	case "12h":
		return (now.Hour()-hour+24)%12 == 0
	case "weekly":
		return now.Hour() == hour && int(now.Weekday()) == cfg.Weekday
	default:
		return now.Hour() == hour
	}
}

// Due is asked once a minute and answers true at most once per scheduled
// minute. Reading the settings each time means a changed schedule takes
// effect without a restart.
func Due(now time.Time) bool {
	if !scheduled(utils.GetMainConfig().Snapshots, now) {
		return false
	}
	stamp := now.Format("2006-01-02 15:04")

	dueMu.Lock()
	defer dueMu.Unlock()
	if lastDue == stamp {
		return false
	}
	lastDue = stamp
	return true
}
