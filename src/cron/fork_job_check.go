package cron

// Fork addition: the server's check of scheduled jobs before they are saved.
// Upstream saved any job the panel or the API sent (PUT /api/config, POST and
// PUT /api/cron): a schedule gocron cannot read was saved and then silently
// never run, a job without a command ran "sh -c ''", a container that does not
// exist failed at every run. The form checks the same (pages/cron/newJob.jsx,
// utils/jobForm.js, utils/cronExpression.js); this is the guard.
//
// Only a job that is new or differs from the stored one is checked: a job
// saved before this check (an odd schedule, an empty command, a container
// that is gone) still loads, runs and saves as it did, as long as it is not
// changed. Switching a stored job on or off changes nothing it is checked for
// and is never refused: a failing job can always be switched off. Docker is
// asked only about a container the save names anew (a new job, or a changed
// container), so a job whose container did not change saves while Docker is
// down or after the container is gone. The routes compare with the jobs read
// under the configuration's lock (see recheckJobs).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	robfig "github.com/robfig/cron/v3"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	maxJobName     = 64
	maxJobCrontab  = 128
	maxJobCommand  = 4096
	maxJobs        = 200
	containerCheck = 5 * time.Second
)

var jobContainerName = regexp.MustCompile(`^/?[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

// errDockerDown says that Docker did not answer the question.
var errDockerDown = errors.New("docker does not answer")

// containerExists asks Docker whether a container of this name exists;
// replaced by the tests. A nil error with false: Docker answered "no such
// container".
var containerExists = func(name string) (bool, error) {
	if err := docker.Connect(); err != nil || docker.DockerClient == nil {
		return false, errDockerDown
	}
	ctx, cancel := context.WithTimeout(context.Background(), containerCheck)
	defer cancel()
	_, err := docker.DockerClient.ContainerInspect(ctx, strings.TrimPrefix(name, "/"))
	if err == nil {
		return true, nil
	}
	if ctx.Err() != nil {
		return false, errDockerDown
	}
	if strings.Contains(strings.ToLower(err.Error()), "no such container") {
		return false, nil
	}
	return false, errDockerDown
}

// cronParser is the parser gocron v2 uses for CronJob(crontab, true).
var cronParser = robfig.NewParser(robfig.SecondOptional | robfig.Minute | robfig.Hour | robfig.Dom | robfig.Month | robfig.Dow | robfig.Descriptor)

// ScheduleProblem tells why gocron would not schedule this crontab, "" when
// it would. It reads the expression exactly as gocron v2's IsValid does: the
// scheduler's location (time.Local) unless the expression names a zone, and
// a schedule that never comes refused.
func ScheduleProblem(crontab string) string {
	if strings.TrimSpace(crontab) == "" {
		return "Enter when the job runs (a schedule such as 0 0 3 * * *, every night at 03:00)"
	}
	if len(crontab) > maxJobCrontab {
		return fmt.Sprintf("The schedule is at most %d characters", maxJobCrontab)
	}
	withLocation := crontab
	if !strings.HasPrefix(crontab, "TZ=") && !strings.HasPrefix(crontab, "CRON_TZ=") {
		withLocation = "CRON_TZ=" + time.Local.String() + " " + crontab
	}
	schedule, err := cronParser.Parse(withLocation)
	if err != nil {
		return "The schedule " + plain(crontab) + " cannot be read: six parts (second minute hour day month weekday) or five (without the seconds), for example 0 0 3 * * * for every night at 03:00"
	}
	if schedule.Next(time.Now()).IsZero() {
		return "The schedule " + plain(crontab) + " never comes: no date matches it"
	}
	return ""
}

// plain quotes a value from a request for a message: printable, bounded.
func plain(value string) string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	if utf8.RuneCountInString(clean) > 80 {
		clean = string([]rune(clean)[:80]) + "…"
	}
	return "\"" + clean + "\""
}

// JobProblem checks one job as it will be saved under key, as a new job. The
// message is a sentence for the administrator.
func JobProblem(key string, job utils.CRONConfig) string {
	return jobProblem(key, job, nil)
}

// sameButSwitch: the two jobs differ at most in Enabled.
func sameButSwitch(a, b utils.CRONConfig) bool {
	a.Enabled, b.Enabled = false, false
	return reflect.DeepEqual(a, b)
}

// jobProblem checks a job against the one stored under the same key (nil: a
// new job). A stored job switched on or off is not checked; the container is
// looked up only when it is named anew.
func jobProblem(key string, job utils.CRONConfig, old *utils.CRONConfig) string {
	if old != nil && sameButSwitch(*old, job) {
		return ""
	}
	name := job.Name
	if strings.TrimSpace(name) == "" {
		return "Give the job a name, for example \"Back up the photos\""
	}
	if name != strings.TrimSpace(name) {
		return "The job's name must not start or end with a space"
	}
	if utf8.RuneCountInString(name) > maxJobName {
		return fmt.Sprintf("A job's name is at most %d characters", maxJobName)
	}
	for _, r := range name {
		if unicode.IsControl(r) || r == '<' || r == '>' {
			return "A job's name is plain text on one line, without < or >"
		}
	}
	if key != name {
		return "The job " + plain(name) + " is saved under another name (" + plain(key) + "): the two must be the same"
	}
	if problem := ScheduleProblem(job.Crontab); problem != "" {
		return "Job " + plain(name) + ": " + problem
	}
	if strings.TrimSpace(job.Command) == "" {
		return "Job " + plain(name) + ": enter the command to run"
	}
	if len(job.Command) > maxJobCommand {
		return fmt.Sprintf("Job %s: the command is at most %d bytes", plain(name), maxJobCommand)
	}
	if strings.ContainsRune(job.Command, 0) {
		return "Job " + plain(name) + ": the command contains a character that cannot be run (NUL)"
	}
	if job.Container != "" && (old == nil || old.Container != job.Container) {
		if !jobContainerName.MatchString(job.Container) {
			return "Job " + plain(name) + ": " + plain(job.Container) + " is not a container name"
		}
		exists, err := containerExists(job.Container)
		if err != nil {
			return "Job " + plain(name) + ": Cosmos cannot ask Docker whether the container " + plain(job.Container) + " exists (Docker does not answer). Try again when Docker runs, or run the job on the server itself"
		}
		if !exists {
			return "Job " + plain(name) + ": there is no container " + plain(job.Container) + " on this server. Pick one from the list"
		}
	}
	return ""
}

// storedJob is the job stored under key, nil when there is none.
func storedJob(stored map[string]utils.CRONConfig, key string) *utils.CRONConfig {
	if old, ok := stored[key]; ok {
		return &old
	}
	return nil
}

// JobsProblem checks the jobs of a save against the stored ones: every job
// that is new or changed, and the number of jobs when it grows. "" when the
// save may go on.
func JobsProblem(request map[string]utils.CRONConfig, stored map[string]utils.CRONConfig) string {
	if len(request) > maxJobs && len(request) > len(stored) {
		return fmt.Sprintf("At most %d scheduled jobs", maxJobs)
	}
	keys := make([]string, 0, len(request))
	for key := range request {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if problem := jobProblem(key, request[key], storedJob(stored, key)); problem != "" {
			return problem
		}
	}
	return ""
}

// RecheckJobs is the second half of the check of a whole-settings save,
// called under the configuration's lock with the jobs stored now: when a
// job the request names was changed by another save since `checked` was
// read, the request's jobs are checked again against what is stored now.
func RecheckJobs(request map[string]utils.CRONConfig, checked map[string]utils.CRONConfig, stored map[string]utils.CRONConfig) string {
	for key := range request {
		if !sameStored(checked, stored, key) {
			return JobsProblem(request, stored)
		}
	}
	return ""
}

// sameStored: the job stored under key is the same in both (or in neither).
func sameStored(a map[string]utils.CRONConfig, b map[string]utils.CRONConfig, key string) bool {
	x, y := storedJob(a, key), storedJob(b, key)
	if x == nil || y == nil {
		return x == nil && y == nil
	}
	return reflect.DeepEqual(*x, *y)
}

// jobSaveProblem is the hook of the job routes of api_crud.go: the job as it
// will be stored under key, compared with what is stored there now.
func jobSaveProblem(key string, job utils.CRONConfig, stored map[string]utils.CRONConfig) string {
	old := storedJob(stored, key)
	if old != nil && reflect.DeepEqual(*old, job) {
		return ""
	}
	if len(stored) >= maxJobs && old == nil {
		return fmt.Sprintf("At most %d scheduled jobs", maxJobs)
	}
	return jobProblem(key, job, old)
}

// jobCheck is what refuseJob checked a job against, for refuseStaleJob.
type jobCheck struct {
	key     string
	job     utils.CRONConfig
	against map[string]utils.CRONConfig
}

// refuseJob answers 400 in words when the job the route would store under key
// may not be saved; the hook of cronConfigCreate and cronConfigUpdate, before
// the configuration's lock (Docker may be asked).
func refuseJob(w http.ResponseWriter, key string, job utils.CRONConfig, code string) (*jobCheck, bool) {
	if job.Name == "" {
		job.Name = key
	}
	check := &jobCheck{key: key, job: job, against: utils.GetMainConfig().CRON}
	problem := jobSaveProblem(key, job, check.against)
	if problem == "" {
		return check, false
	}
	utils.HTTPError(w, problem, http.StatusBadRequest, code)
	return check, true
}

// refuseStaleJob is the second half, under the lock with the jobs read there:
// when the job stored under the key changed since refuseJob looked, the job is
// checked again against what is stored now.
func refuseStaleJob(w http.ResponseWriter, check *jobCheck, stored map[string]utils.CRONConfig, code string) bool {
	if check == nil || (sameStored(check.against, stored, check.key) && len(check.against) == len(stored)) {
		return false
	}
	problem := jobSaveProblem(check.key, check.job, stored)
	if problem == "" {
		return false
	}
	utils.HTTPError(w, problem, http.StatusBadRequest, code)
	return true
}

// jobKey is the name cronConfigUpdate stores the job under.
func jobKey(name string, job utils.CRONConfig) string {
	if job.Name != "" {
		return job.Name
	}
	return name
}
