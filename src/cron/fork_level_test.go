package cron

import (
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// review 5: a job's failure is open while its last run failed, unknown
// while it runs or before it ran, over once it ran well or has been gone
// for goneAfter (missing for a moment, as while a scheduler registers its
// jobs again, is unknown).
func TestTheLevelOfAJob(t *testing.T) {
	savedList, savedInit := jobsList, wasInit
	now := time.Now()
	levelNow = func() time.Time { return now }
	defer func() { jobsList, wasInit, levelNow = savedList, savedInit, time.Now }()
	wasInit = true
	jobsList = map[string]map[string]ConfigJob{"Custom": {
		"backup":  {Name: "backup", LastRun: time.Now(), LastRunSuccess: false},
		"cleanup": {Name: "cleanup", LastRun: time.Now(), LastRunSuccess: true},
		"fresh":   {Name: "fresh"},
		"busy":    {Name: "busy", Running: true, LastRun: time.Now()},
	}}
	for _, c := range []struct {
		name        string
		open, known bool
	}{{"backup", true, true}, {"cleanup", false, true}, {"fresh", false, false}, {"busy", false, false}, {"removed", false, false}} {
		open, known := utils.SourceThreadLevel("error.job", utils.JobThread(c.name))
		if open != c.open || known != c.known {
			t.Errorf("%s: open %v known %v", c.name, open, known)
		}
	}
	// still missing a moment later: unknown; after goneAfter: over
	now = now.Add(goneAfter - time.Second)
	if _, known := utils.SourceThreadLevel("error.job", utils.JobThread("removed")); known {
		t.Error("a job missing for a moment is over")
	}
	now = now.Add(time.Second)
	if open, known := utils.SourceThreadLevel("error.job", utils.JobThread("removed")); open || !known {
		t.Errorf("a job gone for %s: open %v known %v", goneAfter, open, known)
	}
	// it comes back (a scheduler registered it again): its own state, and
	// a later absence starts again
	jobsList["Custom"]["removed"] = ConfigJob{Name: "removed", LastRun: now, LastRunSuccess: false}
	if open, known := utils.SourceThreadLevel("error.job", utils.JobThread("removed")); !open || !known {
		t.Errorf("back: open %v known %v", open, known)
	}
	delete(jobsList["Custom"], "removed")
	if _, known := utils.SourceThreadLevel("error.job", utils.JobThread("removed")); known {
		t.Error("missing again: known at once")
	}
	if list := utils.SourceOpenThreads()["error.job"]; len(list) != 1 || list[0] != utils.JobThread("backup") {
		t.Errorf("open list %v", list)
	}
	wasInit = false
	if _, known := utils.SourceThreadLevel("error.job", utils.JobThread("backup")); known {
		t.Error("known before the jobs were loaded")
	}
}
