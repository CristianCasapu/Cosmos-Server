package utils

// Fork addition (plan 48, review 4 (d)): the level of a thread (a problem
// that starts and ends: open or over), for the Telegram notifier to set
// the chat right after a lost message, a restart, a lost threads file or a
// setting switched off and on while the problem lasted.
//
// The notifications are where every source of a pair writes: an opening
// row stays open until the source resolves its thread (with a follow-up,
// or quietly when the cause changed, a mount or folder is gone, a watch was
// switched off). A source that keeps its state in memory only (a watch, the
// Fail2ban check) registers what it knows now (RegisterThreadLevel): that
// is the truth, and an open row it no longer has is resolved.

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ThreadLevel is what a source knows of a thread now. ok is false when it
// does not know (yet): the notifications are asked instead.
type ThreadLevel func(thread string) (open bool, ok bool)

var (
	threadLevelsMu sync.Mutex
	threadLevels   = map[string]ThreadLevel{}
)

// RegisterThreadLevel says what a source knows of its threads of a kind.
func RegisterThreadLevel(kind string, level ThreadLevel) {
	threadLevelsMu.Lock()
	defer threadLevelsMu.Unlock()
	threadLevels[kind] = level
}

// SourceThreadLevel asks the source of a kind, when it registered.
func SourceThreadLevel(kind string, thread string) (open bool, ok bool) {
	threadLevelsMu.Lock()
	level := threadLevels[kind]
	threadLevelsMu.Unlock()
	if level == nil {
		return false, false
	}
	defer func() {
		if recover() != nil {
			open, ok = false, false
		}
	}()
	return level(thread)
}

const maxThreadRows = 2000

func kindList(kinds []string) (string, []interface{}) {
	marks := make([]string, 0, len(kinds))
	args := make([]interface{}, 0, len(kinds))
	for _, kind := range kinds {
		marks = append(marks, "?")
		args = append(args, kind)
	}
	return "(" + strings.Join(marks, ", ") + ")", args
}

// ForkOpenThreads returns, for the kinds given, the newest open row of
// every thread seen within the last `within`, at most limit of them, as the
// notification that was raised (for the administrators).
func ForkOpenThreads(kinds []string, within time.Duration, limit int) ([]Notification, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	db, err := MonitoringReadDB()
	if err != nil {
		return nil, err
	}
	in, args := kindList(kinds)
	args = append(args, TimeToMillis(notificationClock().Add(-within)))
	rows, err := db.Query(MonitoringRebind("SELECT "+notificationColumnsSQL+" FROM notifications"+
		" WHERE fork_resolved = 0 AND fork_info <> '' AND fork_kind IN "+in+" AND date >= ?"+
		" ORDER BY date DESC, id DESC LIMIT "+strconv.Itoa(maxThreadRows)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	out := []Notification{}
	for rows.Next() && len(out) < limit {
		v, err := scanNotification(rows)
		if err != nil || v.Info == nil || v.Thread == "" || seen[v.Thread] {
			continue
		}
		seen[v.Thread] = true
		out = append(out, viewNotification(v))
	}
	return out, rows.Err()
}

// ForkThreadState reports whether a thread has an open row, and its newest
// row of one of the closing kinds since `since` (the follow-up that says
// it is over), when there is one.
func ForkThreadState(thread string, closings []string, since time.Time) (open bool, closing *Notification, err error) {
	if thread == "" {
		return false, nil, errors.New("no thread")
	}
	db, err := MonitoringReadDB()
	if err != nil {
		return false, nil, err
	}
	var count int
	if err := db.QueryRow(MonitoringRebind("SELECT COUNT(*) FROM notifications WHERE fork_thread = ? AND fork_resolved = 0 AND fork_info <> ''"),
		thread).Scan(&count); err != nil {
		return false, nil, err
	}
	if count > 0 || len(closings) == 0 {
		return count > 0, nil, nil
	}
	in, args := kindList(closings)
	args = append([]interface{}{thread}, args...)
	args = append(args, TimeToMillis(since))
	row := db.QueryRow(MonitoringRebind("SELECT "+notificationColumnsSQL+" FROM notifications"+
		" WHERE fork_thread = ? AND fork_info <> '' AND fork_kind IN "+in+" AND date >= ?"+
		" ORDER BY date DESC, id DESC LIMIT 1"), args...)
	v, scanErr := scanNotification(row)
	if scanErr == nil && v.Info != nil {
		n := viewNotification(v)
		return false, &n, nil
	}
	return false, nil, nil
}

// viewNotification is a stored row as the notification that raised it.
func viewNotification(v NotificationView) Notification {
	info := *v.Info
	info.Ref = v.Ref
	info.Count = v.Count
	info.Repeat = false
	if v.FirstSeen != nil {
		info.Seen = []int64{TimeToMillis(*v.FirstSeen)}
	} else {
		info.Seen = []int64{TimeToMillis(v.Date)}
	}
	return Notification{Recipient: "admin", Level: v.Level, Link: v.Link, Date: v.Date, Fork: &info}
}
