package main

import (
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestBackupScheduled(t *testing.T) {
	// 2026-11-15 is a Sunday
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 11, day, hour, minute, 0, 0, time.Local) }
	runs := func(config utils.Config, day int) (hours []int) {
		for hour := 0; hour < 24; hour++ {
			for minute := 0; minute < 60; minute++ {
				if backupScheduled(config, at(day, hour, minute)) {
					hours = append(hours, hour*100+minute)
				}
			}
		}
		return hours
	}
	equal := func(a, b []int) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	cases := []struct {
		name   string
		config utils.Config
		day    int
		want   []int
	}{
		{"default: every two hours", utils.Config{}, 12, []int{0, 200, 400, 600, 800, 1000, 1200, 1400, 1600, 1800, 2000, 2200}},
		{"every two hours from 01:30", utils.Config{BackupFrequency: "2h", BackupTime: "01:30"}, 12, []int{130, 330, 530, 730, 930, 1130, 1330, 1530, 1730, 1930, 2130, 2330}},
		{"every six hours from 03:15", utils.Config{BackupFrequency: "6h", BackupTime: "03:15"}, 12, []int{315, 915, 1515, 2115}},
		{"every twelve hours from 22:00", utils.Config{BackupFrequency: "12h", BackupTime: "22:00"}, 12, []int{1000, 2200}},
		{"daily at 04:05", utils.Config{BackupFrequency: "daily", BackupTime: "04:05"}, 12, []int{405}},
		{"weekly, Sunday 03:00, on a Sunday", utils.Config{BackupFrequency: "weekly", BackupTime: "03:00", BackupWeekday: 0}, 15, []int{300}},
		{"weekly, Sunday 03:00, on a Thursday", utils.Config{BackupFrequency: "weekly", BackupTime: "03:00", BackupWeekday: 0}, 12, nil},
		{"weekly, Thursday", utils.Config{BackupFrequency: "weekly", BackupTime: "03:00", BackupWeekday: 4}, 12, []int{300}},
		{"broken time falls back to midnight", utils.Config{BackupFrequency: "daily", BackupTime: "25:99"}, 12, []int{0}},
		{"unknown frequency behaves like the default", utils.Config{BackupFrequency: "sometimes"}, 12, []int{0, 200, 400, 600, 800, 1000, 1200, 1400, 1600, 1800, 2000, 2200}},
	}
	for _, c := range cases {
		if got := runs(c.config, c.day); !equal(got, c.want) {
			t.Errorf("%s: runs at %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBackupDueOncePerMinute(t *testing.T) {
	utils.LoadBaseMainConfig(utils.Config{BackupFrequency: "daily", BackupTime: "04:05"})
	defer utils.LoadBaseMainConfig(utils.Config{})
	backupRunMu.Lock()
	lastBackupRun = ""
	backupRunMu.Unlock()

	now := time.Date(2026, 11, 12, 4, 5, 10, 0, time.Local)
	if !backupDue(now) {
		t.Fatal("not due at the scheduled time")
	}
	if backupDue(now.Add(30 * time.Second)) {
		t.Error("due twice in the same minute")
	}
	if !backupDue(now.Add(24 * time.Hour)) {
		t.Error("not due the next day")
	}
}
