package cron

// Fork addition (plan 48, review 5): the scheduled jobs say the level of a
// job's failure to the Telegram notifier from their own state: open while
// its last run failed, over once it ran well or when the job is gone;
// unknown before it ran in this run of the server, while it runs, or
// while the list is being changed (asked again later).

import (
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

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
			open, ok = failing, !found || ran
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
