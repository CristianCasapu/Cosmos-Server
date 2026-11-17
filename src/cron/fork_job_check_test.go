package cron

// Fork addition: tests of fork_job_check.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/utils"
)

func stubContainers(t *testing.T, known map[string]bool, down bool) {
	t.Helper()
	was := containerExists
	containerExists = func(name string) (bool, error) {
		if down {
			return false, errDockerDown
		}
		return known[strings.TrimPrefix(name, "/")], nil
	}
	t.Cleanup(func() { containerExists = was })
}

func TestSchedulesAreReadLikeGocron(t *testing.T) {
	for _, good := range []string{
		"0 0 3 * * *", "0 */5 * * * *", "0 30 2 * * 1", "0 0 4 1 * *", "* * * * * *",
		"0 0 3 * * mon-fri", "0 0 0 1 jan *", "@daily", "@hourly", "@every 1h30m",
		"TZ=Europe/Berlin 0 0 3 * * *", "CRON_TZ=UTC 0 0 * * * *",
		"0 3 * * *", // gocron v2.23 takes the seconds as optional
	} {
		if p := ScheduleProblem(good); p != "" {
			t.Errorf("%q refused: %s", good, p)
		}
	}
	for _, bad := range []string{
		"", " ", "every night", "0 0 25 * * *", "0 61 * * * *", "0 0 3 * * * *", "0 0 3 31 2 *",
		"@sometimes", "TZ=Nowhere/City 0 0 3 * * *", "0 0 3 * * 9", strings.Repeat("1", 129),
	} {
		if p := ScheduleProblem(bad); p == "" {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestAJobIsCheckedBeforeItIsSaved(t *testing.T) {
	stubContainers(t, map[string]bool{"db": true}, false)
	good := utils.CRONConfig{Name: "dump", Enabled: true, Crontab: "0 0 3 * * *", Command: "pg_dumpall -f /x", Container: "db"}
	if p := JobProblem("dump", good); p != "" {
		t.Fatal(p)
	}
	onServer := good
	onServer.Container = ""
	if p := JobProblem("dump", onServer); p != "" {
		t.Fatal(p)
	}
	cases := map[string]func(j *utils.CRONConfig) (key string){
		"no name":           func(j *utils.CRONConfig) string { j.Name = " "; return " " },
		"long name":         func(j *utils.CRONConfig) string { j.Name = strings.Repeat("a", 65); return j.Name },
		"name with a break": func(j *utils.CRONConfig) string { j.Name = "a\nb"; return j.Name },
		"name with markup":  func(j *utils.CRONConfig) string { j.Name = "<b>x"; return j.Name },
		"key not the name":  func(j *utils.CRONConfig) string { return "other" },
		"bad schedule":      func(j *utils.CRONConfig) string { j.Crontab = "nightly"; return j.Name },
		"empty command":     func(j *utils.CRONConfig) string { j.Command = "  \n"; return j.Name },
		"huge command":      func(j *utils.CRONConfig) string { j.Command = strings.Repeat("x", 4097); return j.Name },
		"NUL in command":    func(j *utils.CRONConfig) string { j.Command = "echo \x00"; return j.Name },
		"unknown container": func(j *utils.CRONConfig) string { j.Container = "gone"; return j.Name },
		"not a name":        func(j *utils.CRONConfig) string { j.Container = "db; rm -rf /"; return j.Name },
	}
	for what, change := range cases {
		job := good
		key := change(&job)
		if p := JobProblem(key, job); p == "" {
			t.Errorf("%s accepted", what)
		} else if strings.ContainsAny(p, "\n\x00") {
			t.Errorf("%s: the message carries control characters: %q", what, p)
		}
	}
}

func TestDockerNotAnsweringIsSaid(t *testing.T) {
	stubContainers(t, nil, true)
	p := JobProblem("j", utils.CRONConfig{Name: "j", Crontab: "0 0 3 * * *", Command: "true", Container: "db"})
	if !strings.Contains(p, "Docker does not answer") {
		t.Errorf("got %q", p)
	}
}

func TestOldJobsThatDidNotChangeAreNotChecked(t *testing.T) {
	stubContainers(t, map[string]bool{}, true) // Docker down: no container can be asked about
	stored := map[string]utils.CRONConfig{
		"old":     {Name: "old", Enabled: true, Crontab: "0 3 * * *", Command: "echo old"},
		"empty":   {Name: "empty", Crontab: "0 0 3 * * *", Command: ""},
		"gone":    {Name: "gone", Crontab: "0 0 3 * * *", Command: "true", Container: "removed-long-ago"},
		"badtime": {Name: "badtime", Crontab: "nightly", Command: "true"},
	}
	request := map[string]utils.CRONConfig{}
	for k, v := range stored {
		request[k] = v
	}
	if p := JobsProblem(request, stored); p != "" {
		t.Fatalf("unchanged jobs refused: %s", p)
	}
	request["new"] = utils.CRONConfig{Name: "new", Crontab: "0 0 1 * * *", Command: "echo new"}
	if p := JobsProblem(request, stored); p != "" {
		t.Fatalf("a good new job beside old odd ones refused: %s", p)
	}
	changed := stored["empty"]
	changed.Crontab = "0 0 4 * * *"
	request["empty"] = changed
	if p := JobsProblem(request, stored); p == "" {
		t.Error("a changed job without a command accepted")
	}
	delete(request, "empty")
	request["new"] = utils.CRONConfig{Name: "new", Crontab: "0 0 1 * * *", Command: "true", Container: "db"}
	if p := JobsProblem(request, stored); !strings.Contains(p, "Docker does not answer") {
		t.Errorf("a new job in a container with Docker down: %q", p)
	}
}

func TestTheNumberOfJobsIsBoundedWhenItGrows(t *testing.T) {
	stored := map[string]utils.CRONConfig{}
	for i := 0; i < 205; i++ {
		name := "job" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + itoaTest(i)
		stored[name] = utils.CRONConfig{Name: name, Crontab: "0 0 3 * * *", Command: "true"}
	}
	same := map[string]utils.CRONConfig{}
	for k, v := range stored {
		same[k] = v
	}
	if p := JobsProblem(same, stored); p != "" {
		t.Fatalf("an old long list refused: %s", p)
	}
	same["one more"] = utils.CRONConfig{Name: "one more", Crontab: "0 0 3 * * *", Command: "true"}
	if p := JobsProblem(same, stored); p == "" {
		t.Error("the list grew past the bound")
	}
	if p := jobSaveProblem("one more", same["one more"], stored); p == "" {
		t.Error("the job route let the list grow past the bound")
	}
}

func itoaTest(i int) string {
	s := ""
	for {
		s = string(rune('0'+i%10)) + s
		i /= 10
		if i == 0 {
			return s
		}
	}
}

// A failing job can always be switched off, and an old one on again: the
// switch alone is not a change the check looks at (review, finding 1).
func TestSwitchingAStoredJobIsNeverRefused(t *testing.T) {
	for _, docker := range []struct {
		name string
		down bool
	}{{"container gone", false}, {"Docker unreachable", true}} {
		stubContainers(t, map[string]bool{}, docker.down)
		stored := map[string]utils.CRONConfig{
			"gone":    {Name: "gone", Enabled: true, Crontab: "0 0 3 * * *", Command: "pg_dumpall", Container: "removed"},
			"badtime": {Name: "badtime", Enabled: true, Crontab: "nightly", Command: "true"},
			"empty":   {Name: "empty", Enabled: false, Crontab: "0 0 3 * * *", Command: ""},
		}
		request := map[string]utils.CRONConfig{}
		for k, v := range stored {
			request[k] = v
		}
		off := request["gone"]
		off.Enabled = false
		request["gone"] = off
		if p := JobsProblem(request, stored); p != "" {
			t.Errorf("%s: switching off a job whose container is gone refused: %s", docker.name, p)
		}
		odd := request["badtime"]
		odd.Enabled = false
		request["badtime"] = odd
		on := request["empty"]
		on.Enabled = true
		request["empty"] = on
		if p := JobsProblem(request, stored); p != "" {
			t.Errorf("%s: switching an old odd job off, another on, refused: %s", docker.name, p)
		}
		// the same through the job routes' check
		if p := jobSaveProblem("gone", off, stored); p != "" {
			t.Errorf("%s: the route refuses the switch: %s", docker.name, p)
		}
		// a change of the command of a job whose container did not change: Docker is not asked
		edited := stored["gone"]
		edited.Command = "pg_dumpall -c"
		if p := jobSaveProblem("gone", edited, stored); p != "" {
			t.Errorf("%s: an edit that keeps the container refused: %s", docker.name, p)
		}
		// naming another container asks Docker
		moved := stored["gone"]
		moved.Container = "other"
		if p := jobSaveProblem("gone", moved, stored); p == "" {
			t.Errorf("%s: a container that is not there accepted", docker.name)
		}
	}
}

// Under the lock the jobs are compared again: a job another save changed
// meanwhile is checked against what is stored now (review, finding 6).
func TestRecheckJobsWhenTheStoredJobsChanged(t *testing.T) {
	stubContainers(t, map[string]bool{}, false)
	checked := map[string]utils.CRONConfig{"a": {Name: "a", Crontab: "0 0 3 * * *", Command: "true", Container: "db"}}
	request := map[string]utils.CRONConfig{"a": checked["a"]}
	if p := RecheckJobs(request, checked, checked); p != "" {
		t.Fatalf("nothing changed meanwhile, refused: %s", p)
	}
	now := map[string]utils.CRONConfig{"a": {Name: "a", Crontab: "0 0 3 * * *", Command: "true"}}
	asked := false
	containerExists = func(string) (bool, error) { asked = true; return false, nil }
	if p := RecheckJobs(request, checked, now); p != jobsChangedMeanwhile {
		t.Errorf("a job changed meanwhile: %q", p)
	}
	if p := RecheckJobs(request, checked, map[string]utils.CRONConfig{}); p != jobsChangedMeanwhile {
		t.Errorf("a job deleted meanwhile: %q", p)
	}
	if asked {
		t.Error("Docker was asked under the configuration's lock")
	}
}

// The job routes compare again under the lock with the jobs read there: a job
// another save changed between the route's check and the lock is refused
// ("try again"), and Docker is not asked under the lock (second review, 1 and 6).
func TestTheJobRoutesRefuseAJobThatChangedMeanwhile(t *testing.T) {
	prevFolder, prevConfig := utils.CONFIGFOLDER, utils.GetBaseMainConfig()
	utils.CONFIGFOLDER = t.TempDir() + "/"
	t.Setenv("CONFIG_FILE", "")
	t.Cleanup(func() { utils.LoadBaseMainConfig(prevConfig); utils.CONFIGFOLDER = prevFolder })

	job := utils.CRONConfig{Name: "dump", Enabled: true, Crontab: "0 0 3 * * *", Command: "true", Container: "db"}
	inMemory := prevConfig
	inMemory.CRON = map[string]utils.CRONConfig{"dump": job}
	onDisk := inMemory
	changed := job
	changed.Container = "other" // what another save stored meanwhile
	onDisk.CRON = map[string]utils.CRONConfig{"dump": changed}
	utils.SaveConfigTofile(onDisk)
	utils.LoadBaseMainConfig(inMemory) // what the route's check reads, before the lock

	asked := 0
	stubContainers(t, map[string]bool{"db": true}, false)
	was := containerExists
	containerExists = func(name string) (bool, error) { asked++; return was(name) }

	admin := func(req *http.Request) *http.Request {
		return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{
			Nickname: "admin", Role: utils.ADMIN, UserRole: utils.ADMIN, Permissions: utils.GetRolePermissions(utils.ADMIN), IsSudoed: true,
		}))
	}
	// the same job sent back (only switched off): unchanged against the memory, stale against the file
	body, _ := json.Marshal(utils.CRONConfig{Name: "dump", Enabled: false, Crontab: job.Crontab, Command: job.Command, Container: job.Container})
	req := mux.SetURLVars(admin(httptest.NewRequest("PUT", "/cosmos/api/cron/dump", bytes.NewReader(body))), map[string]string{"name": "dump"})
	w := httptest.NewRecorder()
	cronConfigUpdate(w, req)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "changed meanwhile") || !strings.Contains(w.Body.String(), "CR022") {
		t.Errorf("update: %d %s", w.Code, w.Body.String())
	}
	if stored := utils.ReadConfigFromFile().CRON["dump"]; stored != changed {
		t.Errorf("the other save's job was replaced: %+v", stored)
	}

	// a new job while the number of jobs changed meanwhile
	onDisk.CRON["another"] = utils.CRONConfig{Name: "another", Crontab: "0 0 4 * * *", Command: "true"}
	utils.SaveConfigTofile(onDisk)
	body, _ = json.Marshal(utils.CRONConfig{Name: "fresh", Enabled: true, Crontab: "0 0 5 * * *", Command: "true"})
	w = httptest.NewRecorder()
	cronConfigCreate(w, admin(httptest.NewRequest("POST", "/cosmos/api/cron", bytes.NewReader(body))))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "CR013") {
		t.Errorf("create: %d %s", w.Code, w.Body.String())
	}
	if asked != 0 {
		t.Errorf("Docker was asked %d times", asked)
	}
}
