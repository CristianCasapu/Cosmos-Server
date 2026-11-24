package cron

// Fork addition (plan 48, review 5): the scheduled jobs say the level of a
// job's failure to the Telegram notifier from their own state: open while
// its last run failed, over once it ran well or when the job has been gone
// for goneAfter; unknown before it ran in this run of the server, while it
// runs, while the list is being changed (asked again later), and while a
// job is missing for less than goneAfter: the schedulers register their
// jobs after the start and again after each reset (SnapRAID, Restic), so a
// job missing for a moment is no evidence that it was removed.

import (
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const goneAfter = 5 * time.Minute

var (
	// goneSince: when a job asked about was first missing (under CRONLock)
	goneSince = map[string]time.Time{}
	levelNow  = time.Now
)

// gone reports whether a missing job has been missing long enough to be
// over. The caller holds CRONLock.
func gone(thread string) bool {
	now := levelNow()
	first, seen := goneSince[thread]
	if !seen {
		if len(goneSince) >= 512 {
			goneSince = map[string]time.Time{}
		}
		goneSince[thread] = now
		return false
	}
	return now.Sub(first) >= goneAfter
}

func jobLevel(read func()) bool {
	select {
	case CRONLock <- true:
		defer func() { <-CRONLock }()
	default:
		return false
	}
	if !wasInit {
		return false
	}
	read()
	return true
}

func init() {
	utils.RegisterThreadLevel("error.job", func(thread string) (open bool, ok bool) {
		known := jobLevel(func() {
			found, ran, failing := false, false, false
			for _, scheduler := range jobsList {
				for _, job := range scheduler {
					if utils.JobThread(job.Name) != thread {
						continue
					}
					found = true
					if job.Running {
						continue // its result is set aside while it runs
					}
					if !job.LastRun.IsZero() {
						ran = true
						failing = failing || !job.LastRunSuccess
					}
				}
			}
			if found {
				delete(goneSince, thread)
				open, ok = failing, ran
			} else {
				open, ok = false, gone(thread)
			}
		})
		return open, known && ok
	})
	utils.RegisterOpenThreads("error.job", func() (list []string, ok bool) {
		ok = jobLevel(func() {
			for _, scheduler := range jobsList {
				for _, job := range scheduler {
					if !job.Running && !job.LastRun.IsZero() && !job.LastRunSuccess && strings.TrimSpace(job.Name) != "" {
						list = append(list, utils.JobThread(job.Name))
					}
				}
			}
		})
		return list, ok
	})
}
