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

// settingsBeforeRecheck runs just before recheckBeforeWriting; the tests make
// another save happen there.
var settingsBeforeRecheck = func() {}

// recheckBeforeWriting compares under a short lock, before the replicated parts
// of the save are written (publishReplicatedDomains writes them on a single
// node too, outside the lock), so that a refusal leaves nothing half saved.
// The same comparison runs again where the save takes the lock to write; a
// save that slips in between those two is refused there, after the replicated
// parts (they passed their own checks) — a window of microseconds that only
// moving the paid replication code could close.
func recheckBeforeWriting(request *utils.Config, checked utils.Config) *httpSettingsProblem {
	settingsBeforeRecheck()
	utils.ConfigLock.Lock()
	stored := utils.ReadConfigFromFile()
	utils.ConfigLock.Unlock()
	return recheckJobsAndAlerts(request, checked, stored)
}

// recheckJobsAndAlerts is the second hook of PUT /api/config, under the
// configuration's lock: the save is refused when a job the request names
// changed since `checked` was read (no Docker under the lock); the alerts (no
// outside lookup) are checked again.
func recheckJobsAndAlerts(request *utils.Config, checked utils.Config, stored utils.Config) *httpSettingsProblem {
	if problem := cron.RecheckJobs(request.CRON, checked.CRON, stored.CRON); problem != "" {
		return &httpSettingsProblem{"UC043", problem}
	}
	return checkAlerts(request, stored)
}
