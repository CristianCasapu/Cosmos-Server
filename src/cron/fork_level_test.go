package cron

import (
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// review 5: a job's failure is open while its last run failed, unknown
// while it runs or before it ran, over once it ran well or is gone.
func TestTheLevelOfAJob(t *testing.T) {
	savedList, savedInit := jobsList, wasInit
	defer func() { jobsList, wasInit = savedList, savedInit }()
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
	}{{"backup", true, true}, {"cleanup", false, true}, {"fresh", false, false}, {"busy", false, false}, {"removed", false, true}} {
		open, known := utils.SourceThreadLevel("error.job", utils.JobThread(c.name))
		if open != c.open || known != c.known {
			t.Errorf("%s: open %v known %v", c.name, open, known)
		}
	}
	if list := utils.SourceOpenThreads()["error.job"]; len(list) != 1 || list[0] != utils.JobThread("backup") {
		t.Errorf("open list %v", list)
	}
	wasInit = false
	if _, known := utils.SourceThreadLevel("error.job", utils.JobThread("backup")); known {
		t.Error("known before the jobs were loaded")
	}
}
