package main

// Fork addition: what the administrators are told about the automatic
// dependency updates (the workflow fork-deps-update.yml).
//
// At every update check (after a start and every six hours) the server
// reads status.json from the branch fork-deps-status of the repository it
// updates from (forkrelease.FetchDepsStatus: the updater's HTTP rules, 64
// KiB, 20 s) and, when the run or its outcome changed since the last one
// told, writes a notification for the admins (Telegram gets it through the
// notification path):
//
//   - updated: the summary and the release, and when this server installs
//     it (tonight, by hand, already installed). Resolves an open failure or
//     warning that the update has not run.
//   - failed: the step, the failure text, the issue to start a session from.
//   - paused: told once.
//   - nothing: nothing, except one digest every 28 days once nothing needed
//     updating for 28 days, and a follow-up when it resolves a failure.
//   - a status older than 15 days, not paused: a warning, once per run.
//
// What was told is kept in the configuration folder (fork-deps-told.json,
// 0600). An invalid status is ignored (logged once). The setting
// DependencyNoticesOff switches all of it off, the reading included.

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/azukaar/cosmos-server/src/forkrelease"
	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	depsThread      = "deps:updates" // failures, warnings, and what resolves them
	depsNewsThread  = "deps:news"    // the rest: resolving a failure must not mark news resolved
	depsToldFile    = "fork-deps-told.json"
	depsStaleAfter  = 15 * 24 * time.Hour
	depsDigestEvery = 28 * 24 * time.Hour
	depsGeneralLink = "/cosmos-ui/config-general"
	depsJobName     = "Automatic dependency update"
	maxDepsTold     = 16 << 10
	maxDepsFacts    = 12   // utils' bound
	maxDepsRaw      = 3800 // below utils' bound, so the list ends with what is left out
)

// The ways out, variables for the tests.
var (
	depsFetch     = forkrelease.FetchDepsStatus
	depsWrite     = utils.WriteNotification
	depsResolve   = func(thread string) { utils.ResolveThread(thread) }
	depsClock     = time.Now
	depsStatePath = func() string { return filepath.Join(utils.CONFIGFOLDER, depsToldFile) }
	depsVersion   = GetCosmosVersion
)

var (
	depsRunning     sync.Mutex
	depsProblemMu   sync.Mutex
	depsLastProblem string
)

// depsTold is what was told, kept between runs of the server.
type depsTold struct {
	Run        int64     `json:"run,omitempty"`
	Outcome    string    `json:"outcome,omitempty"`
	ToldAt     time.Time `json:"toldAt,omitzero"`
	Digest     time.Time `json:"digest,omitzero"`    // the last monthly digest
	StaleRun   int64     `json:"staleRun,omitempty"` // the run the last "has not run" warning was about
	StaleAt    time.Time `json:"staleAt,omitzero"`
	FailedOpen bool      `json:"failedOpen,omitempty"`
	StaleOpen  bool      `json:"staleOpen,omitempty"`
}

// depsContext is what the decision needs besides the status.
type depsContext struct {
	now        time.Time
	repo       string
	current    string // the running version
	autoUpdate bool
	hour       int // the nightly maintenance
	minute     int
}

type depsDecision struct {
	resolve bool // the open notices of the thread are resolved first
	notes   []utils.Notification
	next    depsTold
}

// checkDependencyStatus is the hook of refreshServerUpdateStatus. It never
// fails and never panics; a second call while one runs returns at once.
func checkDependencyStatus() {
	if !depsRunning.TryLock() {
		return
	}
	defer depsRunning.Unlock()
	defer func() {
		if recover() != nil {
			depsProblem("the check stopped on an unexpected error")
		}
	}()

	config := utils.GetMainConfig()
	if config.DependencyNoticesOff {
		return
	}
	body, err := depsFetch(updateRepo)
	if err != nil {
		depsProblem("the status could not be read: " + err.Error())
		return
	}
	st, err := forkrelease.ParseDepsStatus(body, updateRepo)
	if err != nil {
		depsProblem(err.Error() + "; it is ignored")
		return
	}
	depsProblem("")

	hour, minute := maintenanceTime()
	path := depsStatePath()
	told := readDepsTold(path)
	d := decideDeps(st, told, depsContext{now: depsClock(), repo: updateRepo, current: depsVersion(),
		autoUpdate: config.AutoUpdate, hour: hour, minute: minute})
	if d.next == told && len(d.notes) == 0 && !d.resolve {
		return
	}
	// the state first: a state that cannot be kept would tell the same
	// thing every six hours
	if err := writeDepsTold(path, d.next); err != nil {
		depsProblem("what was told could not be kept: " + err.Error())
		return
	}
	if d.resolve {
		depsResolve(depsThread)
	}
	for _, n := range d.notes {
		depsWrite(n)
	}
}

// depsProblem logs a problem when it differs from the last one; "" forgets
// the last one.
func depsProblem(message string) {
	depsProblemMu.Lock()
	defer depsProblemMu.Unlock()
	if message == depsLastProblem {
		return
	}
	depsLastProblem = message
	if message != "" {
		utils.Warn("Dependency updates: " + forkrelease.CleanStatusText(message, 300, false))
	}
}

func readDepsTold(path string) depsTold {
	var told depsTold
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return told
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxDepsTold+1))
	if err != nil || len(raw) > maxDepsTold || json.Unmarshal(raw, &told) != nil {
		return depsTold{}
	}
	return told
}

// writeDepsTold replaces the file: written beside it (0600), then renamed.
func writeDepsTold(path string, told depsTold) error {
	raw, err := json.MarshalIndent(told, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	os.Remove(tmp)
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	if _, err := file.Write(raw); err != nil {
		file.Close()
		os.Remove(tmp)
		return err
	}
	if err := file.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// decideDeps says what to tell about a status, from what was told before.
func decideDeps(st forkrelease.DepsStatus, told depsTold, c depsContext) depsDecision {
	d := depsDecision{next: told}
	open := told.FailedOpen || told.StaleOpen

	if st.Run != told.Run || st.Outcome != told.Outcome {
		d.next.Run, d.next.Outcome, d.next.ToldAt = st.Run, st.Outcome, c.now
		switch st.Outcome {
		case "updated":
			d.resolve = open
			d.notes = append(d.notes, depsUpdatedNotice(st, c, open))
			d.next.FailedOpen, d.next.StaleOpen = false, false
		case "failed":
			// a failure is a run: it ends the warning that none came
			d.resolve = told.StaleOpen
			d.notes = append(d.notes, depsFailedNotice(st, c))
			d.next.FailedOpen, d.next.StaleOpen = true, false
		case "paused":
			if told.StaleOpen {
				d.resolve = true
				d.next.FailedOpen, d.next.StaleOpen = false, false
			}
			if told.Outcome != "paused" {
				d.notes = append(d.notes, depsPausedNotice(st, told.StaleOpen))
			}
		case "nothing":
			if open {
				d.resolve = true
				d.notes = append(d.notes, depsRecoveredNotice(st))
				d.next.FailedOpen, d.next.StaleOpen = false, false
			}
		}
	}

	if st.Outcome != "paused" && c.now.Sub(st.At) > depsStaleAfter && d.next.StaleRun != st.Run && !d.next.StaleOpen {
		d.notes = append(d.notes, depsStaleNotice(st, c))
		d.next.StaleOpen, d.next.StaleRun, d.next.StaleAt = true, st.Run, c.now
	}

	if st.Outcome == "nothing" && !st.QuietSince.IsZero() && !d.next.StaleOpen && c.now.Sub(st.QuietSince) >= depsDigestEvery &&
		(d.next.Digest.IsZero() || c.now.Sub(d.next.Digest) >= depsDigestEvery) {
		d.notes = append(d.notes, depsDigestNotice(st))
		d.next.Digest = c.now
	}
	return d
}

func depsWhen(t time.Time) string { return t.Local().Format("2006-01-02 15:04") }

var depsStepNames = map[string]string{
	"update":       "choosing and applying the new versions",
	"advisories":   "the check for known vulnerabilities",
	"checks":       "the build, the tests and the browser check",
	"push":         "publishing the changes to the branches",
	"custom-moved": "the custom branch changed meanwhile",
	"release":      "the release",
}

func depsChangeText(c forkrelease.DepsChange) string {
	text := c.Eco + " " + c.Name + " " + c.From + " → " + c.To
	if c.Kind != "" {
		text += " (" + c.Kind + ")"
	}
	if c.Reason != "" {
		text += ": " + c.Reason
	}
	return text
}

// depsList is the complete list of a status, as the technical text.
func depsList(st forkrelease.DepsStatus, first string) string {
	var b strings.Builder
	b.WriteString(first)
	left := 0
	section := func(title string, list []forkrelease.DepsChange) {
		if len(list) == 0 {
			return
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(title + " (" + strconv.Itoa(len(list)) + "):")
		for i, c := range list {
			line := "\n" + depsChangeText(c)
			if b.Len()+len(line) > maxDepsRaw {
				left += len(list) - i
				return
			}
			b.WriteString(line)
		}
	}
	section("Updated", st.Changes)
	section("Dropped by the full checks", st.Dropped)
	section("Tried and failed", st.Tried)
	section("Left for a session", st.Left)
	if left > 0 {
		b.WriteString("\n… and " + strconv.Itoa(left) + " more: see the run on GitHub.")
	}
	return b.String()
}

func depsRunFacts(st forkrelease.DepsStatus) []utils.NotificationFact {
	facts := []utils.NotificationFact{
		{Key: "notif.fact.deps.ranAt", Value: depsWhen(st.At)},
		{Key: "notif.fact.deps.run", Value: st.RunLink},
	}
	if st.IssueLink != "" {
		facts = append(facts, utils.NotificationFact{Key: "notif.fact.deps.issue", Value: st.IssueLink})
	}
	return facts
}

func depsRunStep(st forkrelease.DepsStatus) utils.NotificationStep {
	return utils.NotificationStep{NotificationText: *utils.NText("notif.step.deps.run", "link", st.RunLink)}
}

func depsGeneralStep() utils.NotificationStep {
	return utils.NotificationStep{NotificationText: *utils.NText("notif.step.deps.general"), Link: depsGeneralLink}
}

func depsJob() *utils.NotificationSubject {
	return &utils.NotificationSubject{Type: "job", Name: depsJobName, Link: depsGeneralLink}
}

func depsUpdatedNotice(st forkrelease.DepsStatus, c depsContext, resolves bool) utils.Notification {
	minor, major, majors := st.Summary()
	summary := forkrelease.DependencySummary(minor, major, majors)
	at := twoDigits(c.hour) + ":" + twoDigits(c.minute)

	installed := false
	if c.current != "" {
		if cmp, err := CompareVersions(c.current, st.Release); err == nil && cmp >= 0 {
			installed = true
		}
	}
	variant, doing := "manual", utils.NText("notif.deps.updated.doing.manual")
	switch {
	case installed:
		variant, doing = "installed", utils.NText("notif.doing.nothing")
	case c.autoUpdate && (c.hour >= 18 || c.hour < 6):
		variant, doing = "tonight", utils.NText("notif.deps.updated.doing.auto")
	case c.autoUpdate:
		variant, doing = "at", utils.NText("notif.deps.updated.doing.auto")
	}

	facts := []utils.NotificationFact{
		{Key: "notif.fact.release", Value: st.Release},
		{Key: "notif.fact.deps.minor", Value: strconv.Itoa(minor)},
		{Key: "notif.fact.deps.major", Value: strconv.Itoa(major)},
	}
	facts = append(facts, depsRunFacts(st)...)
	// the majors first, then the others, as many as the bound leaves room
	ordered := []forkrelease.DepsChange{}
	for _, ch := range st.Changes {
		if ch.Kind == "major" {
			ordered = append(ordered, ch)
		}
	}
	for _, ch := range st.Changes {
		if ch.Kind != "major" {
			ordered = append(ordered, ch)
		}
	}
	room := maxDepsFacts - len(facts)
	if len(ordered) > room {
		room--
	}
	for i, ch := range ordered {
		if i == room {
			facts = append(facts, utils.NotificationFact{Key: "notif.fact.deps.more",
				Value: strconv.Itoa(len(ordered)-i) + " more, in the list below"})
			break
		}
		facts = append(facts, utils.NotificationFact{Key: "notif.fact.deps.change", Value: depsChangeText(ch)})
	}

	info := &utils.NotificationInfo{
		Kind: "deps.updated", Source: "updates", Thread: depsNewsThread,
		Subject: &utils.NotificationSubject{Type: "release", Name: st.Release, Link: depsGeneralLink},
		What:    utils.NText("notif.deps.updated.what."+variant, "summary", summary, "release", st.Release, "at", at),
		Means:   utils.NText("notif.deps.updated.means"),
		Doing:   doing,
		Steps:   []utils.NotificationStep{depsGeneralStep(), depsRunStep(st)},
		Facts:   facts,
		Raw:     depsList(st, ""),
	}
	if resolves {
		info.Thread, info.Resolves = depsThread, depsThread
	}
	return utils.Notification{Recipient: "admin", Level: "info", Fork: info}
}

func depsFailedNotice(st forkrelease.DepsStatus, c depsContext) utils.Notification {
	what, means, level := "notif.deps.failed.what", "notif.deps.failed.means", "error"
	switch st.FailedStep {
	case "release":
		what = "notif.deps.failed.what.release"
	case "custom-moved":
		what, means, level = "notif.deps.failed.what.customMoved", "notif.deps.failed.means.customMoved", "warning"
	}
	steps := []utils.NotificationStep{}
	if st.IssueLink != "" {
		steps = append(steps, utils.NotificationStep{NotificationText: *utils.NText("notif.step.deps.issue", "link", st.IssueLink)})
	}
	steps = append(steps, depsRunStep(st))
	facts := []utils.NotificationFact{}
	if name := depsStepNames[st.FailedStep]; name != "" {
		facts = append(facts, utils.NotificationFact{Key: "notif.fact.deps.step", Value: name})
	}
	facts = append(facts, depsRunFacts(st)...)
	return utils.Notification{Recipient: "admin", Level: level, Fork: &utils.NotificationInfo{
		Kind: "deps.failed", Source: "updates", Thread: depsThread, Subject: depsJob(),
		What: utils.NText(what), Means: utils.NText(means), Doing: utils.NText("notif.deps.failed.doing"),
		Steps: steps, Facts: facts, Raw: depsList(st, st.Failure),
	}}
}

func depsPausedNotice(st forkrelease.DepsStatus, resolves bool) utils.Notification {
	info := &utils.NotificationInfo{
		Kind: "deps.paused", Source: "updates", Thread: depsNewsThread, Subject: depsJob(),
		What: utils.NText("notif.deps.paused.what"), Means: utils.NText("notif.deps.paused.means"),
		Steps: []utils.NotificationStep{depsRunStep(st)}, Facts: depsRunFacts(st),
	}
	if resolves {
		info.Thread, info.Resolves = depsThread, depsThread
	}
	return utils.Notification{Recipient: "admin", Level: "info", Fork: info}
}

func depsRecoveredNotice(st forkrelease.DepsStatus) utils.Notification {
	return utils.Notification{Recipient: "admin", Level: "success", Fork: &utils.NotificationInfo{
		Kind: "deps.quiet", Source: "updates", Thread: depsThread, Resolves: depsThread, Subject: depsJob(),
		What:  utils.NText("notif.deps.quiet.recovered", "date", st.At.Local().Format("2006-01-02")),
		Facts: depsRunFacts(st), Raw: depsList(st, ""),
	}}
}

func depsDigestNotice(st forkrelease.DepsStatus) utils.Notification {
	return utils.Notification{Recipient: "admin", Level: "info", Fork: &utils.NotificationInfo{
		Kind: "deps.quiet", Source: "updates", Thread: depsNewsThread, Subject: depsJob(),
		What:  utils.NText("notif.deps.quiet.what", "date", st.QuietSince.Format("2006-01-02")),
		Facts: depsRunFacts(st), Raw: depsList(st, ""),
	}}
}

func depsStaleNotice(st forkrelease.DepsStatus, c depsContext) utils.Notification {
	actions := "https://github.com/" + c.repo + "/actions"
	return utils.Notification{Recipient: "admin", Level: "warning", Fork: &utils.NotificationInfo{
		Kind: "deps.stale", Source: "updates", Thread: depsThread, Subject: depsJob(),
		What:  utils.NText("notif.deps.stale.what", "date", st.At.Local().Format("2006-01-02")),
		Means: utils.NText("notif.deps.stale.means"),
		Steps: []utils.NotificationStep{{NotificationText: *utils.NText("notif.step.deps.actions", "link", actions)}, depsRunStep(st)},
		Facts: depsRunFacts(st),
	}}
}

// installedForkDependencies is the dependency summary of the running
// release, from the meta.json beside the binary (part of the signed
// release), or nil.
func installedForkDependencies() *forkrelease.ForkDependencies {
	ex, err := os.Executable()
	if err != nil {
		return nil
	}
	file, err := os.Open(filepath.Join(filepath.Dir(ex), "meta.json"))
	if err != nil {
		return nil
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, forkrelease.MaxMetaFile+1))
	if err != nil {
		return nil
	}
	d, err := forkrelease.ParseForkDependencies(raw)
	if err != nil {
		utils.Warn("Dependency updates: " + err.Error())
		return nil
	}
	return d
}
