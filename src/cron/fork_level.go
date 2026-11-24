package cron

// Fork addition (plan 48, reviews 5 and 6): the scheduled jobs say the
// level of a job's failure to the Telegram notifier from their own state.
// The rule (review 6): "over" only on evidence seen in this run of the
// server; absence is never evidence.
//
//   - A job's run failure (utils.JobThread): open while its last run in
//     this run of the server failed; over once it ran well; unknown before
//     it ran in this run, while it runs, and while the job is not in the
//     scheduler's list (a scheduler empties its list while it resets and
//     waits for running jobs, for hours during a sync). One exception, a
//     decision of the owner: a job of the owner's own list ("Custom") that
//     the scheduler took in this run and that is now gone from the list
//     and from the saved configuration was removed: its failure is over
//     (the chat says "No longer followed", whose text says that what it
//     was about is gone).
//   - A job the scheduler could not take (utils.JobScheduleThread): open
//     while its last scheduling in this run failed; over once a later
//     pass took it (and "works again" is written); unknown before.
//   - The one thread all scheduling failures shared before review 6
//     (utils.JobSchedulingLegacy): over once a pass in this run took every
//     job of the list; unknown before.
//
// Unknown also while the list is being changed (asked again later).

import (
	"github.com/azukaar/cosmos-server/src/utils"
)

type schedKey struct{ scheduler, name string }

var (
	// scheduled: per job, whether its last scheduling in this run failed;
	// takenCustom: the threads of the owner's jobs the scheduler took in
	// this run (both under CRONLock)
	scheduled   = map[schedKey]bool{}
	takenCustom = map[string]bool{}
	// savedJobNames: the jobs of the saved configuration
	savedJobNames = func() []string {
		names := []string{}
		for _, job := range utils.GetMainConfig().CRON {
			names = append(names, job.Name)
		}
		return names
	}
)

// forkScheduled records the scheduler's answer for a job, and tells it.
// The caller holds CRONLock.
func forkScheduled(job ConfigJob, err error) {
	key := schedKey{job.Scheduler, job.Name}
	if len(scheduled) >= 4096 {
		scheduled = map[schedKey]bool{}
	}
	failedBefore := scheduled[key]
	scheduled[key] = err != nil
	if job.Scheduler == "Custom" && len(takenCustom) < 4096 {
		takenCustom[utils.JobThread("Custom", job.Name)] = true
	}
	if err != nil {
		go utils.ForkJobScheduleFailed(job.Scheduler, job.Name, err)
	} else if failedBefore {
		go utils.ForkJobScheduled(job.Scheduler, job.Name)
	}
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

// jobOf finds the job a run-failure thread is about: its own thread, or
// the thread of its name alone (before review 6).
func jobOf(thread string) (ConfigJob, bool) {
	var legacy *ConfigJob
	for scheduler, list := range jobsList {
		for _, job := range list {
			if utils.JobThread(scheduler, job.Name) == thread {
				return job, true
			}
			if legacy == nil && utils.JobThreadLegacy(job.Name) == thread {
				j := job
				legacy = &j
			}
		}
	}
	if legacy != nil {
		return *legacy, true
	}
	return ConfigJob{}, false
}

func jobFailureLevel(thread string) (open bool, ok bool) {
	if thread == utils.JobSchedulingLegacy {
		if len(scheduled) == 0 {
			return false, false
		}
		for scheduler, list := range jobsList {
			for _, job := range list {
				if job.Disabled {
					continue
				}
				if failed, seen := scheduled[schedKey{scheduler, job.Name}]; !seen || failed {
					return false, false
				}
			}
		}
		return false, true
	}
	for key, failed := range scheduled {
		if utils.JobScheduleThread(key.scheduler, key.name) == thread {
			return failed, true
		}
	}
	job, found := jobOf(thread)
	if !found {
		if !takenCustom[thread] {
			return false, false // not in the list now: unknown
		}
		for _, name := range savedJobNames() {
			if utils.JobThread("Custom", name) == thread {
				return false, false // still saved: the list is being changed
			}
		}
		return false, true // removed by the owner
	}
	if job.Running || job.LastRun.IsZero() {
		return false, false // its result is set aside while it runs; not run yet in this run
	}
	return !job.LastRunSuccess, true
}

func init() {
	utils.RegisterThreadLevel("error.job", func(thread string) (open bool, ok bool) {
		known := jobLevel(func() { open, ok = jobFailureLevel(thread) })
		return open, known && ok
	})
	utils.RegisterOpenThreads("error.job", func() (list []string, ok bool) {
		ok = jobLevel(func() {
			for scheduler, jobs := range jobsList {
				for _, job := range jobs {
					if !job.Running && !job.LastRun.IsZero() && !job.LastRunSuccess && job.Name != "" {
						list = append(list, utils.JobThread(scheduler, job.Name))
					}
				}
			}
			for key, failed := range scheduled {
				if failed {
					list = append(list, utils.JobScheduleThread(key.scheduler, key.name))
				}
			}
		})
		return list, ok
	})
}
