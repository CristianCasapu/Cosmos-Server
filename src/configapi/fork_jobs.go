package configapi

// Fork addition: the scheduled jobs saved with the whole settings
// (PUT /api/config, how the panel saves a job) are checked like those of the
// job routes: every new or changed job, see cron/fork_job_check.go. Unchanged
// jobs pass as they are. The checks run before the configuration's lock
// (Docker may be asked); under the lock, recheckJobsAndAlerts compares again
// with what is stored then, so a save made meanwhile cannot make a changed job
// or alert pass as unchanged.

import (
	"github.com/azukaar/cosmos-server/src/cron"
	"github.com/azukaar/cosmos-server/src/utils"
)

// checkJobs is the hook of PUT /api/config.
func checkJobs(request *utils.Config, stored utils.Config) *httpSettingsProblem {
	if problem := cron.JobsProblem(request.CRON, stored.CRON); problem != "" {
		return &httpSettingsProblem{"UC043", problem}
	}
	return nil
}

// recheckJobsAndAlerts is the second hook of PUT /api/config, under the
// configuration's lock: the jobs are checked again when one the request names
// changed since `checked` was read; the alerts (no outside lookup) always.
func recheckJobsAndAlerts(request *utils.Config, checked utils.Config, stored utils.Config) *httpSettingsProblem {
	if problem := cron.RecheckJobs(request.CRON, checked.CRON, stored.CRON); problem != "" {
		return &httpSettingsProblem{"UC043", problem}
	}
	return checkAlerts(request, stored)
}
