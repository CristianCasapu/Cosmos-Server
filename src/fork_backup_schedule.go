package main

// Fork addition: the backup of the configuration runs on a schedule set in
// the settings instead of always every two hours.

import (
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

var (
	backupRunMu   sync.Mutex
	lastBackupRun string // the minute the backup was last started, as 2006-01-02 15:04
)

// backupScheduled reports whether the configuration asks for a backup in the
// minute of now. BackupFrequency is "" or "2h" (every two hours, as upstream
// did), "6h", "12h", "daily" or "weekly"; BackupTime ("HH:MM", default 00:00)
// is the time of day it is counted from; BackupWeekday (0 = Sunday) is the
// day of a weekly backup.
func backupScheduled(config utils.Config, now time.Time) bool {
	hour, minute, ok := parseMaintenanceTime(config.BackupTime)
	if !ok {
		hour, minute = 0, 0
	}
	if now.Minute() != minute {
		return false
	}

	every := 0
	switch config.BackupFrequency {
	case "", "2h":
		every = 2
	case "6h":
		every = 6
	case "12h":
		every = 12
	case "daily":
		return now.Hour() == hour
	case "weekly":
		return now.Hour() == hour && int(now.Weekday()) == config.BackupWeekday
	default:
		every = 2
	}
	return (now.Hour()-hour+24)%every == 0
}

// backupDue is asked once a minute and answers true at most once per
// scheduled minute.
func backupDue(now time.Time) bool {
	if !backupScheduled(utils.GetMainConfig(), now) {
		return false
	}
	stamp := now.Format("2006-01-02 15:04")

	backupRunMu.Lock()
	defer backupRunMu.Unlock()
	if lastBackupRun == stamp {
		return false
	}
	lastBackupRun = stamp
	return true
}
