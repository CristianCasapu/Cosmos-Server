package configapi

// Fork addition: the scheduled jobs saved with the whole settings
// (PUT /api/config, how the panel saves a job) are checked like those of the
// job routes: every new or changed job, see cron/fork_job_check.go. Unchanged
// jobs pass as they are.

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
