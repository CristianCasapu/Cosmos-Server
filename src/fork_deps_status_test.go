package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/forkrelease"
	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: tests of the notices about the automatic dependency
// updates (fork_deps_status.go). Nothing here talks to GitHub.

const depsRepo = "example-owner/Cosmos-Server"

func depsStatus(t *testing.T, body string) forkrelease.DepsStatus {
	t.Helper()
	st, err := forkrelease.ParseDepsStatus([]byte(body), depsRepo)
	if err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return st
}

func depsJSON(run int, at time.Time, outcome string, extra string) string {
	body := `{"schema":1,"run":` + strconv.Itoa(run) + `,"at":"` + at.UTC().Format(time.RFC3339) + `","outcome":"` + outcome + `"`
	if extra != "" {
		body += "," + extra
	}
	return body + "}"
}

const depsChanges = `"release":"0.24.0-fork.31","changes":[
	{"eco":"npm","name":"react","from":"18.3.1","to":"19.0.0","kind":"major","ageDays":12},
	{"eco":"go","name":"golang.org/x/net","from":"v0.30.0","to":"v0.31.0","kind":"minor"},
	{"eco":"npm","name":"vite","from":"5.0.0","to":"5.0.1","kind":"patch"}]`

func say(t *utils.NotificationText) string {
	if t == nil {
		return ""
	}
	return utils.RenderNotificationText(*t)
}

func depsCtx(now time.Time) depsContext {
	return depsContext{now: now, repo: depsRepo, current: "0.24.0-fork.30", autoUpdate: true, hour: 2, minute: 0}
}

func TestDepsUpdatedSaysWhenItIsInstalled(t *testing.T) {
	now := time.Date(2026, 11, 21, 18, 0, 0, 0, time.UTC)
	st := depsStatus(t, depsJSON(9, now.Add(-time.Hour), "updated", depsChanges))
	cases := []struct {
		name   string
		change func(*depsContext)
		want   string
	}{
		{"automatic, at night", func(c *depsContext) {},
			"Dependencies were updated automatically: 2 minor, 1 major (react 19.0.0), in release 0.24.0-fork.31, which this server installs tonight at 02:00."},
		{"automatic, in the day", func(c *depsContext) { c.hour, c.minute = 13, 30 },
			"Dependencies were updated automatically: 2 minor, 1 major (react 19.0.0), in release 0.24.0-fork.31, which this server installs at 13:30."},
		{"by hand", func(c *depsContext) { c.autoUpdate = false },
			"Dependencies were updated automatically: 2 minor, 1 major (react 19.0.0), in release 0.24.0-fork.31: install it from Configuration > General."},
		{"already installed", func(c *depsContext) { c.current = "0.24.0-fork.31" },
			"Dependencies were updated automatically: 2 minor, 1 major (react 19.0.0), in release 0.24.0-fork.31, which is already installed."},
		{"a newer one installed", func(c *depsContext) { c.current = "0.24.1-fork.33"; c.autoUpdate = false },
			"Dependencies were updated automatically: 2 minor, 1 major (react 19.0.0), in release 0.24.0-fork.31, which is already installed."},
	}
	for _, tc := range cases {
		c := depsCtx(now)
		tc.change(&c)
		d := decideDeps(st, depsTold{}, c)
		if len(d.notes) != 1 || d.resolve {
			t.Fatalf("%s: %d notes, resolve %v", tc.name, len(d.notes), d.resolve)
		}
		n := d.notes[0]
		info := n.Fork
		if got := say(info.What); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
		if n.Level != "info" || n.Recipient != "admin" || info.Kind != "deps.updated" || info.Thread != depsNewsThread || info.Source != "updates" || info.Resolves != "" {
			t.Errorf("%s: %+v %+v", tc.name, n, info)
		}
		if info.Subject.Type != "release" || info.Subject.Name != "0.24.0-fork.31" || info.Subject.Link != depsGeneralLink {
			t.Errorf("%s: subject %+v", tc.name, info.Subject)
		}
		if !strings.Contains(info.Raw, "npm react 18.3.1 → 19.0.0 (major)") || !strings.HasPrefix(info.Raw, "Updated (3):") {
			t.Errorf("%s: raw %q", tc.name, info.Raw)
		}
		if info.Facts[0].Value != "0.24.0-fork.31" || info.Facts[1].Value != "2" || info.Facts[2].Value != "1" {
			t.Errorf("%s: facts %+v", tc.name, info.Facts)
		}
	}
	// every key used has its English
	for _, k := range []string{"notif.deps.updated.means", "notif.deps.updated.doing.auto", "notif.deps.updated.doing.manual", "notif.deps.updated.title"} {
		if utils.NotificationEnglish(k) == "" {
			t.Errorf("%s has no English", k)
		}
	}
}

func TestDepsFailedSaysWhatAndWhere(t *testing.T) {
	now := time.Date(2026, 11, 21, 18, 0, 0, 0, time.UTC)
	st := depsStatus(t, depsJSON(10, now, "failed",
		`"failedStep":"checks","issue":42,"failure":"go test: FAIL src/utils\nsee javascript:alert(1)","tried":[{"eco":"npm","name":"vite","from":"5.0.0","to":"6.0.0","reason":"quick checks: build"}]`))
	d := decideDeps(st, depsTold{}, depsCtx(now))
	if len(d.notes) != 1 || !d.next.FailedOpen {
		t.Fatalf("%+v", d)
	}
	n := d.notes[0]
	info := n.Fork
	if n.Level != "error" || info.Kind != "deps.failed" || say(info.What) != "The automatic dependency update failed its checks: nothing was published; it needs a session." {
		t.Errorf("%s %s %q", n.Level, info.Kind, say(info.What))
	}
	if !strings.HasPrefix(info.Raw, "go test: FAIL src/utils\nsee javascript:alert(1)\n\nTried and failed (1):\nnpm vite 5.0.0 → 6.0.0: quick checks: build") {
		t.Errorf("raw %q", info.Raw)
	}
	if got := say(&info.Steps[0].NotificationText); got != "Open the issue on GitHub and start a session: https://github.com/"+depsRepo+"/issues/42" {
		t.Errorf("step %q", got)
	}
	for _, step := range info.Steps {
		if step.Link != "" && step.Link != depsGeneralLink {
			t.Errorf("a step links to %s", step.Link)
		}
	}
	if info.Facts[0].Value != "the build, the tests and the browser check" || info.Facts[2].Value != "https://github.com/"+depsRepo+"/actions/runs/10" {
		t.Errorf("facts %+v", info.Facts)
	}

	release := depsStatus(t, depsJSON(11, now, "failed", `"failedStep":"release"`))
	if got := say(decideDeps(release, depsTold{}, depsCtx(now)).notes[0].Fork.What); !strings.Contains(got, "passed its checks, but its release could not be published") {
		t.Errorf("release: %q", got)
	}
	moved := decideDeps(depsStatus(t, depsJSON(12, now, "failed", `"failedStep":"custom-moved"`)), depsTold{}, depsCtx(now)).notes[0]
	if moved.Level != "warning" || !strings.Contains(say(moved.Fork.What), "the custom branch changed") || len(moved.Fork.Steps) != 1 {
		t.Errorf("custom moved: %s %q %+v", moved.Level, say(moved.Fork.What), moved.Fork.Steps)
	}
}

// what is told, in order, over weeks of runs
func TestDepsToldOnceAndResolved(t *testing.T) {
	start := time.Date(2026, 11, 7, 16, 0, 0, 0, time.UTC)
	told := depsTold{}
	step := func(name string, st forkrelease.DepsStatus, now time.Time, wantKinds []string, wantResolve bool) depsDecision {
		t.Helper()
		d := decideDeps(st, told, depsCtx(now))
		kinds := []string{}
		for _, n := range d.notes {
			kinds = append(kinds, n.Fork.Kind)
		}
		if strings.Join(kinds, ",") != strings.Join(wantKinds, ",") || d.resolve != wantResolve {
			t.Errorf("%s: told %v resolve %v, want %v resolve %v", name, kinds, d.resolve, wantKinds, wantResolve)
		}
		told = d.next
		return d
	}

	nothing := depsStatus(t, depsJSON(1, start, "nothing", `"quietSince":"2026-11-07"`))
	step("a first quiet run", nothing, start.Add(time.Hour), nil, false)
	step("the same, six hours later", nothing, start.Add(7*time.Hour), nil, false)

	failed := depsStatus(t, depsJSON(2, start.Add(7*24*time.Hour), "failed", `"failedStep":"checks","issue":4`))
	step("a failure", failed, start.Add(7*24*time.Hour+time.Hour), []string{"deps.failed"}, false)
	step("the failure again", failed, start.Add(7*24*time.Hour+7*time.Hour), nil, false)

	recovered := depsStatus(t, depsJSON(3, start.Add(14*24*time.Hour), "nothing", `"quietSince":"2026-11-21"`))
	d := step("quiet after the failure", recovered, start.Add(14*24*time.Hour+time.Hour), []string{"deps.quiet"}, true)
	if n := d.notes[0]; n.Level != "success" || n.Fork.Resolves != depsThread || !strings.Contains(say(n.Fork.What), "ran again on 2026-11-21 without a problem") {
		t.Errorf("follow-up %s %q", n.Level, say(n.Fork.What))
	}

	failed2 := depsStatus(t, depsJSON(4, start.Add(21*24*time.Hour), "failed", `"failedStep":"advisories"`))
	step("another failure", failed2, start.Add(21*24*time.Hour+time.Hour), []string{"deps.failed"}, false)
	updated := depsStatus(t, depsJSON(5, start.Add(28*24*time.Hour), "updated", depsChanges))
	d = step("an update after it", updated, start.Add(28*24*time.Hour+time.Hour), []string{"deps.updated"}, true)
	if d.notes[0].Fork.Resolves != depsThread || d.notes[0].Fork.Thread != depsThread {
		t.Error("the update does not resolve the failure")
	}
	updated6 := depsStatus(t, depsJSON(6, start.Add(35*24*time.Hour), "updated", strings.Replace(depsChanges, "fork.31", "fork.32", 1)))
	d = step("an update with nothing open", updated6, start.Add(35*24*time.Hour+time.Hour), []string{"deps.updated"}, false)
	if d.notes[0].Fork.Resolves != "" || d.notes[0].Fork.Thread != depsNewsThread {
		t.Error("an update resolves nothing open: it is news, out of the thread of the failures")
	}

	paused := depsStatus(t, depsJSON(7, start.Add(42*24*time.Hour), "paused", ""))
	d = step("paused", paused, start.Add(42*24*time.Hour+time.Hour), []string{"deps.paused"}, false)
	if !strings.Contains(say(d.notes[0].Fork.What), "paused") {
		t.Errorf("paused %q", say(d.notes[0].Fork.What))
	}
	paused8 := depsStatus(t, depsJSON(8, start.Add(49*24*time.Hour), "paused", ""))
	step("paused, the next week", paused8, start.Add(49*24*time.Hour+time.Hour), nil, false)
	step("paused, two months on: no warning", paused8, start.Add(110*24*time.Hour), nil, false)
}

func TestDepsStaleWarningOncePerRunAndResolvedByARun(t *testing.T) {
	at := time.Date(2026, 11, 7, 16, 0, 0, 0, time.UTC)
	quiet := depsStatus(t, depsJSON(1, at, "nothing", `"quietSince":"2026-11-07"`))
	told := decideDeps(quiet, depsTold{}, depsCtx(at.Add(time.Hour))).next

	if d := decideDeps(quiet, told, depsCtx(at.Add(depsStaleAfter))); len(d.notes) != 0 {
		t.Errorf("at 15 days exactly: %d notes", len(d.notes))
	}
	d := decideDeps(quiet, told, depsCtx(at.Add(depsStaleAfter+time.Minute)))
	if len(d.notes) != 1 || d.notes[0].Fork.Kind != "deps.stale" || d.notes[0].Level != "warning" || !d.next.StaleOpen {
		t.Fatalf("after 15 days: %+v", d)
	}
	if got := say(d.notes[0].Fork.What); got != "The automatic dependency update has not run since 2026-11-07: the workflow may be disabled on GitHub." {
		t.Errorf("%q", got)
	}
	if got := say(&d.notes[0].Fork.Steps[0].NotificationText); !strings.HasSuffix(got, "https://github.com/"+depsRepo+"/actions") {
		t.Errorf("step %q", got)
	}
	told = d.next
	// not again for the same run, even weeks later, and no digest meanwhile
	for _, days := range []int{16, 20, 40, 60} {
		if d := decideDeps(quiet, told, depsCtx(at.Add(time.Duration(days)*24*time.Hour))); len(d.notes) != 0 {
			t.Errorf("day %d: %d notes (%s)", days, len(d.notes), d.notes[0].Fork.Kind)
		}
	}
	// a run comes: the warning is resolved
	next := depsStatus(t, depsJSON(2, at.Add(20*24*time.Hour), "nothing", `"quietSince":"2026-11-07"`))
	d = decideDeps(next, told, depsCtx(at.Add(20*24*time.Hour+time.Hour)))
	if !d.resolve || d.next.StaleOpen || len(d.notes) != 1 || d.notes[0].Fork.Resolves != depsThread {
		t.Errorf("a run after the warning: %+v", d)
	}
	// a pause ends it too, without a follow-up of its own besides "paused"
	paused := depsStatus(t, depsJSON(3, at.Add(20*24*time.Hour), "paused", ""))
	d = decideDeps(paused, told, depsCtx(at.Add(20*24*time.Hour+time.Hour)))
	if !d.resolve || d.next.StaleOpen || len(d.notes) != 1 || d.notes[0].Fork.Kind != "deps.paused" || d.notes[0].Fork.Resolves != depsThread {
		t.Errorf("a pause after the warning: %+v", d)
	}
}

func TestDepsMonthlyDigest(t *testing.T) {
	quietSince := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	told := depsTold{}
	for week := 0; week < 12; week++ {
		ranAt := quietSince.Add(time.Duration(week*7)*24*time.Hour + 16*time.Hour)
		st := depsStatus(t, depsJSON(100+week, ranAt, "nothing", `"quietSince":"2026-10-03"`))
		d := decideDeps(st, told, depsCtx(ranAt.Add(time.Hour)))
		told = d.next
		wantDigest := week == 4 || week == 8 // 28 and 56 days after the series began
		if wantDigest != (len(d.notes) == 1) {
			t.Errorf("week %d: %d notes", week, len(d.notes))
		}
		if wantDigest {
			n := d.notes[0]
			if n.Level != "info" || n.Fork.Kind != "deps.quiet" || say(n.Fork.What) != "Dependencies are checked every week: nothing needed updating since 2026-10-03." {
				t.Errorf("week %d: %s %q", week, n.Level, say(n.Fork.What))
			}
		}
	}
}

func TestDepsFactsAndRawStayInBounds(t *testing.T) {
	parts := []string{}
	for i := 0; i < 300; i++ {
		kind := "minor"
		if i%50 == 0 {
			kind = "major"
		}
		parts = append(parts, `{"eco":"npm","name":"@scope/package-with-a-long-name-`+strconv.Itoa(i)+`","from":"1.0.0","to":"2.0.0","kind":"`+kind+`"}`)
	}
	now := time.Date(2026, 11, 21, 18, 0, 0, 0, time.UTC)
	st := depsStatus(t, depsJSON(9, now, "updated", `"release":"0.24.0-fork.31","changes":[`+strings.Join(parts, ",")+`]`))
	info := decideDeps(st, depsTold{}, depsCtx(now)).notes[0].Fork
	if len(info.Facts) != maxDepsFacts {
		t.Errorf("%d facts", len(info.Facts))
	}
	if last := info.Facts[len(info.Facts)-1]; last.Key != "notif.fact.deps.more" || last.Value != "294 more, in the list below" {
		t.Errorf("last fact %+v", last)
	}
	if info.Facts[5].Value != "npm @scope/package-with-a-long-name-0 1.0.0 → 2.0.0 (major)" {
		t.Errorf("the majors come first: %+v", info.Facts[5])
	}
	if len(info.Raw) > 4000 || !strings.HasSuffix(info.Raw, "more: see the run on GitHub.") {
		t.Errorf("raw: %d bytes, ends %q", len(info.Raw), info.Raw[len(info.Raw)-60:])
	}
	if got := say(info.What); !strings.Contains(got, "294 minor, 6 major (") || len(info.What.Vars["summary"]) > 300 {
		t.Errorf("%q", got)
	}
}

// the whole check: fetch, parse, decide, keep, tell
func TestDepsCheck(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, depsToldFile)
	now := time.Date(2026, 11, 21, 18, 0, 0, 0, time.UTC)
	body := depsJSON(9, now.Add(-time.Hour), "updated", depsChanges)
	var fetched int
	var written []utils.Notification
	var resolved []string
	saved := []any{depsFetch, depsWrite, depsResolve, depsClock, depsStatePath, depsVersion}
	t.Cleanup(func() {
		depsFetch, depsWrite, depsResolve = saved[0].(func(string) ([]byte, error)), saved[1].(func(utils.Notification)), saved[2].(func(string))
		depsClock, depsStatePath, depsVersion = saved[3].(func() time.Time), saved[4].(func() string), saved[5].(func() string)
		utils.LoadBaseMainConfig(utils.Config{})
	})
	fetchErr := error(nil)
	depsFetch = func(repo string) ([]byte, error) {
		fetched++
		if repo != updateRepo {
			t.Errorf("asked for %s", repo)
		}
		return []byte(body), fetchErr
	}
	depsWrite = func(n utils.Notification) { written = append(written, n) }
	depsResolve = func(thread string) { resolved = append(resolved, thread) }
	depsClock = func() time.Time { return now }
	depsStatePath = func() string { return path }
	depsVersion = func() string { return "0.24.0-fork.30" }
	config := utils.Config{AutoUpdate: true}
	utils.LoadBaseMainConfig(config)

	checkDependencyStatus()
	if fetched != 1 || len(written) != 1 || written[0].Fork.Kind != "deps.updated" {
		t.Fatalf("fetched %d, written %+v", fetched, written)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("state file %v %v", info, err)
	}
	if told := readDepsTold(path); told.Run != 9 || told.Outcome != "updated" {
		t.Errorf("kept %+v", told)
	}
	checkDependencyStatus()
	if fetched != 2 || len(written) != 1 {
		t.Errorf("told again: %d", len(written))
	}

	// an invalid or unreadable status: ignored, logged once, nothing changes
	before, _ := os.ReadFile(path)
	for _, b := range []string{`{"schema":2}`, `{"schema":1,"run":10,"at":"2026-11-21T16:40:00Z","outcome":"updated","release":"1.0.0"}`, strings.Repeat("[", 5000)} {
		body = b
		checkDependencyStatus()
		if !strings.HasPrefix(depsLastProblem, forkrelease.ErrDepsStatus.Error()) {
			t.Errorf("not noted: %q", depsLastProblem)
		}
	}
	body, fetchErr = "", errors.New("answered 404")
	checkDependencyStatus()
	if depsLastProblem != "the status could not be read: answered 404" {
		t.Errorf("not noted: %q", depsLastProblem)
	}
	after, _ := os.ReadFile(path)
	if len(written) != 1 || string(before) != string(after) {
		t.Errorf("an invalid status changed something: %d notes", len(written))
	}

	// a failure, then the setting off: nothing is read or told
	fetchErr = nil
	body = depsJSON(10, now, "failed", `"failedStep":"checks"`)
	config.DependencyNoticesOff = true
	utils.LoadBaseMainConfig(config)
	checkDependencyStatus()
	if fetched != 6 || len(written) != 1 {
		t.Errorf("with the notices off: fetched %d, %d notes", fetched, len(written))
	}
	config.DependencyNoticesOff = false
	utils.LoadBaseMainConfig(config)
	checkDependencyStatus()
	if len(written) != 2 || written[1].Fork.Kind != "deps.failed" || len(resolved) != 0 || depsLastProblem != "" {
		t.Errorf("after switching on: %d notes, resolved %v, problem %q", len(written), resolved, depsLastProblem)
	}
	body = depsJSON(11, now.Add(time.Hour), "updated", depsChanges)
	checkDependencyStatus()
	if len(written) != 3 || len(resolved) != 1 || resolved[0] != depsThread || written[2].Fork.Resolves != depsThread {
		t.Errorf("the update after the failure: %d notes, resolved %v", len(written), resolved)
	}

	// a state file that cannot be kept: nothing is told (else it would be told every six hours)
	os.Remove(path)
	os.Mkdir(path+".tmp", 0700)
	os.WriteFile(filepath.Join(path+".tmp", "x"), nil, 0600)
	body = depsJSON(12, now, "failed", `"failedStep":"push"`)
	checkDependencyStatus()
	if len(written) != 3 || !strings.HasPrefix(depsLastProblem, "what was told could not be kept") {
		t.Errorf("without a state file: %d notes, %q", len(written), depsLastProblem)
	}
}

func TestDepsToldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, depsToldFile)
	if told := readDepsTold(path); told != (depsTold{}) {
		t.Errorf("missing: %+v", told)
	}
	want := depsTold{Run: 4, Outcome: "failed", FailedOpen: true, ToldAt: time.Date(2026, 11, 21, 18, 0, 0, 0, time.UTC)}
	if err := writeDepsTold(path, want); err != nil {
		t.Fatal(err)
	}
	if told := readDepsTold(path); told != want {
		t.Errorf("%+v", told)
	}
	for name, content := range map[string]string{"broken": "{", "huge": `{"outcome":"` + strings.Repeat("a", maxDepsTold) + `"}`, "wrong type": `{"run":"4"}`} {
		os.WriteFile(path, []byte(content), 0600)
		if told := readDepsTold(path); told != (depsTold{}) {
			t.Errorf("%s: %+v", name, told)
		}
	}
	// never through a link
	other := filepath.Join(dir, "other.json")
	writeDepsTold(other, want)
	os.Remove(path)
	os.Symlink(other, path)
	if told := readDepsTold(path); told != (depsTold{}) {
		t.Errorf("read through a link: %+v", told)
	}
}
