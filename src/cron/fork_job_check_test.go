package cron

// Fork addition: tests of fork_job_check.go.

import (
	"strings"
	"testing"

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
	changed.Enabled = true
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
