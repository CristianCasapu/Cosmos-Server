package cron

import (
	"errors"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func jobLevelEnv(t *testing.T, saved ...string) {
	t.Helper()
	savedList, savedInit, savedScheduled, savedTaken, savedNames := jobsList, wasInit, scheduled, takenCustom, savedJobNames
	savedConfig, savedOff := savedSettings, backupsOff
	t.Cleanup(func() {
		jobsList, wasInit, scheduled, takenCustom, savedJobNames = savedList, savedInit, savedScheduled, savedTaken, savedNames
		savedSettings, backupsOff = savedConfig, savedOff
	})
	savedSettings = func() utils.Config { return utils.Config{} }
	backupsOff = func() bool { return false }
	wasInit = true
	scheduled, takenCustom = map[schedKey]bool{}, map[string]bool{}
	savedJobNames = func() []string { return saved }
}

func level(thread string) (bool, bool) { return utils.SourceThreadLevel("error.job", thread) }

// reviews 5 and 6: a job's failure is open while its last run failed,
// unknown while it runs or before it ran in this run, over once it ran
// well; a job missing from the list is unknown (review 6, C1).
func TestTheLevelOfAJob(t *testing.T) {
	jobLevelEnv(t, "backup", "cleanup", "fresh", "busy")
	jobsList = map[string]map[string]ConfigJob{"Custom": {
		"backup":  {Name: "backup", Scheduler: "Custom", LastRun: time.Now(), LastRunSuccess: false},
		"cleanup": {Name: "cleanup", Scheduler: "Custom", LastRun: time.Now(), LastRunSuccess: true},
		"fresh":   {Name: "fresh", Scheduler: "Custom"},
		"busy":    {Name: "busy", Scheduler: "Custom", Running: true, LastRun: time.Now()},
	}}
	for _, c := range []struct {
		name        string
		open, known bool
	}{{"backup", true, true}, {"cleanup", false, true}, {"fresh", false, false}, {"busy", false, false}, {"missing", false, false}} {
		open, known := level(utils.JobThread("Custom", c.name))
		if open != c.open || known != c.known {
			t.Errorf("%s: open %v known %v", c.name, open, known)
		}
	}
	if list := utils.SourceOpenThreads()["error.job"]; len(list) != 1 || list[0] != utils.JobThread("Custom", "backup") {
		t.Errorf("open list %v", list)
	}
	wasInit = false
	if _, known := level(utils.JobThread("Custom", "backup")); known {
		t.Error("known before the jobs were loaded")
	}
}

// review 6, C1: a scheduler that resets empties its list while it waits for
// running jobs (hours during a sync): its failing job stays unknown however
// long it is missing; it is over only when it ran well.
func TestAJobMissingDuringAResetIsNotOver(t *testing.T) {
	jobLevelEnv(t)
	thread := utils.JobThread("SnapRAID", "sync")
	jobsList = map[string]map[string]ConfigJob{"SnapRAID": {
		"sync": {Name: "sync", Scheduler: "SnapRAID", LastRun: time.Now(), LastRunSuccess: false},
	}}
	if open, known := level(thread); !open || !known {
		t.Fatalf("failing: open %v known %v", open, known)
	}
	jobsList["SnapRAID"] = map[string]ConfigJob{} // resetScheduler
	for i := 0; i < 3; i++ {
		if _, known := level(thread); known {
			t.Fatal("missing during a reset is known")
		}
	}
	jobsList["SnapRAID"]["sync"] = ConfigJob{Name: "sync", Scheduler: "SnapRAID", LastRun: time.Now(), LastRunSuccess: true}
	if open, known := level(thread); open || !known {
		t.Errorf("ran well: open %v known %v", open, known)
	}
}

// review 6: two jobs of one name in two schedulers are two threads; a
// failure written before review 6 (the name alone) still finds its job.
func TestTwoJobsOfOneNameAreTwoThreads(t *testing.T) {
	jobLevelEnv(t, "nightly")
	jobsList = map[string]map[string]ConfigJob{
		"Custom":   {"nightly": {Name: "nightly", Scheduler: "Custom", LastRun: time.Now(), LastRunSuccess: true}},
		"SnapRAID": {"nightly": {Name: "nightly", Scheduler: "SnapRAID", LastRun: time.Now(), LastRunSuccess: false}},
	}
	if utils.JobThread("Custom", "nightly") == utils.JobThread("SnapRAID", "nightly") {
		t.Fatal("one thread for two jobs")
	}
	if open, known := level(utils.JobThread("SnapRAID", "nightly")); !open || !known {
		t.Errorf("SnapRAID's: open %v known %v", open, known)
	}
	if open, known := level(utils.JobThread("Custom", "nightly")); open || !known {
		t.Errorf("Custom's: open %v known %v", open, known)
	}
	delete(jobsList, "Custom")
	if open, known := level(utils.JobThreadLegacy("nightly")); !open || !known {
		t.Errorf("the thread of the name alone: open %v known %v", open, known)
	}
}

// review 6, C1: a job the scheduler could not take has its own thread: open
// while its scheduling failed, over once a later pass took it; the thread
// all scheduling failures shared before is over only once every job was
// taken in this run.
func TestASchedulingFailureHasItsOwnTruth(t *testing.T) {
	jobLevelEnv(t, "bad", "good")
	job := ConfigJob{Name: "bad", Scheduler: "Custom"}
	jobsList = map[string]map[string]ConfigJob{"Custom": {"bad": job, "good": {Name: "good", Scheduler: "Custom"}}}
	thread := utils.JobScheduleThread("Custom", "bad")
	if _, known := level(thread); known {
		t.Fatal("known before any pass")
	}
	if _, known := level(utils.JobSchedulingLegacy); known {
		t.Fatal("the old shared thread is known before any pass")
	}
	forkScheduled(job, errors.New("bad crontab"))
	forkScheduled(ConfigJob{Name: "good", Scheduler: "Custom"}, nil)
	if open, known := level(thread); !open || !known {
		t.Errorf("refused: open %v known %v", open, known)
	}
	if _, known := level(utils.JobSchedulingLegacy); known {
		t.Error("the old shared thread is over while a job is refused")
	}
	if list := utils.SourceOpenThreads()["error.job"]; len(list) != 1 || list[0] != thread {
		t.Errorf("open list %v", list)
	}
	forkScheduled(job, nil)
	if open, known := level(thread); open || !known {
		t.Errorf("taken: open %v known %v", open, known)
	}
	if open, known := level(utils.JobSchedulingLegacy); open || !known {
		t.Errorf("the old shared thread after a clean pass: open %v known %v", open, known)
	}
}

// review 6: a job of the owner's list that the scheduler took in this run
// and that is gone from the list and the saved configuration was removed by
// the owner: over. Gone from the list only: unknown.
func TestAJobTheOwnerRemovedIsOver(t *testing.T) {
	jobLevelEnv(t, "old")
	job := ConfigJob{Name: "old", Scheduler: "Custom", LastRun: time.Now(), LastRunSuccess: false}
	jobsList = map[string]map[string]ConfigJob{"Custom": {"old": job}}
	forkScheduled(job, nil)
	thread := utils.JobThread("Custom", "old")
	jobsList["Custom"] = map[string]ConfigJob{}
	if _, known := level(thread); known {
		t.Error("gone from the list but still saved: known")
	}
	savedJobNames = func() []string { return nil }
	if open, known := level(thread); open || !known {
		t.Errorf("removed: open %v known %v", open, known)
	}
	// a job never taken in this run is unknown, saved or not
	if _, known := level(utils.JobThread("Custom", "never")); known {
		t.Error("a job never seen in this run is known")
	}
}

// review 7, N1: a pass forgets the answer of a job its list no longer
// holds (unknown); a job the owner removed, or disabled, is over.
func TestARefusedJobGoneFromTheListIsForgotten(t *testing.T) {
	jobLevelEnv(t, "bad")
	job := ConfigJob{Name: "bad", Scheduler: "Custom"}
	jobsList = map[string]map[string]ConfigJob{"Custom": {"bad": job}}
	forkScheduled(job, errors.New("bad crontab"))
	thread := utils.JobScheduleThread("Custom", "bad")
	if open, known := level(thread); !open || !known {
		t.Fatalf("refused: open %v known %v", open, known)
	}
	jobsList["Custom"] = map[string]ConfigJob{}
	forkPruneScheduled()
	if len(scheduled) != 0 {
		t.Errorf("not forgotten: %v", scheduled)
	}
	if _, known := level(thread); known {
		t.Error("gone from the list but still saved: known")
	}
	savedJobNames = func() []string { return nil }
	if open, known := level(thread); open || !known {
		t.Errorf("removed: open %v known %v", open, known)
	}
	// disabled by the owner: over
	job.Disabled = true
	jobsList["Custom"] = map[string]ConfigJob{"bad": job}
	forkScheduled(ConfigJob{Name: "bad", Scheduler: "Custom"}, errors.New("bad crontab"))
	forkPruneScheduled()
	if open, known := level(thread); open || !known {
		t.Errorf("disabled: open %v known %v", open, known)
	}
}

// review 7, N3: a Restic or SnapRAID job the saved settings no longer make
// is over, in both thread forms; with backups off every Restic job is.
func TestAJobTheSettingsNoLongerMakeIsOver(t *testing.T) {
	jobLevelEnv(t)
	jobsList = map[string]map[string]ConfigJob{"Restic": {}, "SnapRAID": {}}
	config := utils.Config{}
	config.Backup.Backups = map[string]utils.SingleBackupConfig{"kept": {Name: "kept"}}
	config.Storage.SnapRAIDs = []utils.SnapRAIDConfig{{Name: "pool"}}
	savedSettings = func() utils.Config { return config }
	for _, c := range []struct {
		thread string
		known  bool
	}{
		{utils.JobThread("Restic", "Restic backup kept"), false},
		{utils.JobThread("Restic", "Restic backup gone"), true},
		{utils.JobThreadLegacy("Restic forget gone"), true},
		{utils.JobScheduleThread("Restic", "Restic backup gone"), true},
		{utils.JobThread("SnapRAID", "SnapRAID sync pool"), false},
		{utils.JobThread("SnapRAID", "SnapRAID scrub old"), true},
		{utils.JobThread("SnapRAID", "SnapRAID fix pool"), false},
		{utils.JobThread("Custom", "Restic-like"), false},
	} {
		open, known := level(c.thread)
		if open || known != c.known {
			t.Errorf("%s: open %v known %v, want known %v", c.thread, open, known, c.known)
		}
	}
	backupsOff = func() bool { return true }
	if open, known := level(utils.JobThread("Restic", "Restic backup kept")); open || !known {
		t.Errorf("backups off: open %v known %v", open, known)
	}
}
