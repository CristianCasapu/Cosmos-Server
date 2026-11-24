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
	threadOpens    = map[string]func() []string{}
)

// RegisterOpenThreads says which threads of a kind a source holds open now
// (review 5): an open problem is found even when its notification was
// deleted. ok is false while the source does not know yet.
func RegisterOpenThreads(kind string, open func() (threads []string, ok bool)) {
	threadLevelsMu.Lock()
	defer threadLevelsMu.Unlock()
	threadOpens[kind] = func() []string {
		threads, ok := open()
		if !ok {
			return nil
		}
		return threads
	}
}

// SourceOpenThreads lists, per kind, the threads the sources hold open.
func SourceOpenThreads() map[string][]string {
	threadLevelsMu.Lock()
	opens := map[string]func() []string{}
	for kind, f := range threadOpens {
		opens[kind] = f
	}
	threadLevelsMu.Unlock()
	out := map[string][]string{}
	for kind, f := range opens {
		func() {
			defer func() { recover() }()
			if list := f(); len(list) > 0 {
				out[kind] = list
			}
		}()
	}
	return out
}

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

// ForkThreadState says what the notifications know of a thread: open (a
// row of it is not resolved), over (none is open, and since `since` one
// was resolved or a closing row came: POSITIVE evidence, review 5 L1), or
// neither: no row at all (deleted by the owner, pruned after 30 days) is
// unknown, never "over". closing is the newest closing row since `since`.
// Any error of the store is an error: the caller treats it as unknown.
func ForkThreadState(thread string, closings []string, since time.Time) (open bool, over bool, closing *Notification, err error) {
	thread = ThreadName(thread) // as it was stored (review 6)
	if thread == "" {
		return false, false, nil, errors.New("no thread")
	}
	db, err := MonitoringReadDB()
	if err != nil {
		return false, false, nil, err
	}
	var count int
	if err := db.QueryRow(MonitoringRebind("SELECT COUNT(*) FROM notifications WHERE fork_thread = ? AND fork_resolved = 0 AND fork_info <> ''"),
		thread).Scan(&count); err != nil {
		return false, false, nil, err
	}
	if count > 0 {
		return true, false, nil, nil
	}
	sinceMs := TimeToMillis(since)
	var resolved int
	if err := db.QueryRow(MonitoringRebind("SELECT COUNT(*) FROM notifications WHERE fork_thread = ? AND fork_resolved >= ? AND fork_resolved > 0 AND fork_info <> ''"),
		thread, sinceMs).Scan(&resolved); err != nil {
		return false, false, nil, err
	}
	if len(closings) > 0 {
		in, args := kindList(closings)
		args = append([]interface{}{thread}, args...)
		args = append(args, sinceMs)
		rows, err := db.Query(MonitoringRebind("SELECT "+notificationColumnsSQL+" FROM notifications"+
			" WHERE fork_thread = ? AND fork_info <> '' AND fork_kind IN "+in+" AND date >= ?"+
			" ORDER BY date DESC, id DESC LIMIT 1"), args...)
		if err != nil {
			return false, false, nil, err
		}
		defer rows.Close()
		if rows.Next() {
			v, err := scanNotification(rows)
			if err != nil {
				return false, false, nil, err
			}
			if v.Info != nil {
				n := viewNotification(v)
				closing = &n
			}
		}
		if err := rows.Err(); err != nil {
			return false, false, nil, err
		}
	}
	return false, resolved > 0 || closing != nil, closing, nil
}

// ThreadName is a thread's name as the notifications store it (review 6):
// a source compares its own keys through it, so that a name the store
// cleaned (trimmed, control characters replaced, cut) is still found.
func ThreadName(name string) string { return cleanVar(name, 200) }

// JobThread is the thread of a scheduled job's failure. It names the
// scheduler (review 6: two jobs of one name in two schedulers are two
// things); the jobs of the owner's own list ("Custom") keep the name
// alone, as before.
func JobThread(scheduler string, name string) string {
	if scheduler == "" || scheduler == "Custom" {
		return "error.job:" + cleanVar(name, 160)
	}
	return "error.job:" + cleanVar(scheduler+"/"+name, 160)
}

// JobThreadLegacy is the thread a job's failure had before review 6 (the
// name alone, whatever the scheduler).
func JobThreadLegacy(name string) string { return "error.job:" + cleanVar(name, 160) }

// JobScheduleThread is the thread of a job that could not be scheduled
// (review 6: its own truth, not a job named "scheduling").
func JobScheduleThread(scheduler string, name string) string {
	if scheduler == "" {
		scheduler = "Custom"
	}
	return "error.job.schedule:" + cleanVar(scheduler+"/"+name, 160)
}

// JobSchedulingLegacy is the one thread every scheduling failure shared
// before review 6.
const JobSchedulingLegacy = "error.job:scheduling"

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
