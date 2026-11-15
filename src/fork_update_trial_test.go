package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/forkrelease"
	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: the new server's proof of a good start, and the report of
// a return to the version before.

const trialVersion = "0.24.0-fork.10"

// trialFolderWith is an install with trialVersion on trial (start 1).
func trialFolderWith(t *testing.T) string {
	t.Helper()
	folder := t.TempDir()
	os.WriteFile(filepath.Join(folder, "meta.json"), []byte(`{"version": "`+trialVersion+`"}`), 0644)
	raw, _ := json.Marshal(map[string]interface{}{"state": "trial", "newVersion": trialVersion, "previousVersion": "0.24.0-fork.9", "run": 1})
	if err := os.WriteFile(filepath.Join(folder, "cosmos-update.trial"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return folder
}

func shortTrial(t *testing.T) *int {
	t.Helper()
	settle, poll, giveUp := trialSettle, trialPoll, trialGiveUp
	trialSettle, trialPoll, trialGiveUp = 50*time.Millisecond, 10*time.Millisecond, 2*time.Second
	placed := 0
	previous, previousMark := placeUpdatedLauncher, markAnswered
	placeUpdatedLauncher = func() { placed++ }
	t.Cleanup(func() {
		trialSettle, trialPoll, trialGiveUp = settle, poll, giveUp
		placeUpdatedLauncher, markAnswered = previous, previousMark
	})
	return &placed
}

func listenerPort(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	return port
}

func TestTheHTTPCheckOfATrial(t *testing.T) {
	asked := ""
	cases := map[int]bool{200: true, 401: true, 404: true, 302: true, 499: true, 500: false, 503: false}
	for status, passes := range cases {
		port := listenerPort(t, func(w http.ResponseWriter, r *http.Request) {
			asked = r.Host + r.URL.Path
			if status == 302 {
				http.Redirect(w, r, "http://203.0.113.9/elsewhere", http.StatusFound)
				return
			}
			w.WriteHeader(status)
		})
		err := checkHTTPAnswers(port)
		if (err == nil) != passes {
			t.Errorf("status %d: %v", status, err)
		}
		if asked != "127.0.0.1:"+port+"/cosmos/api/status" {
			t.Errorf("asked %s", asked)
		}
	}
	// nothing listens
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	_, closed, _ := net.SplitHostPort(listener.Addr().String())
	listener.Close()
	if err := checkHTTPAnswers(closed); err == nil {
		t.Error("a closed port passed")
	}
	for _, bad := range []string{"", "0", "65536", "80; rm", "-1"} {
		if err := checkHTTPAnswers(bad); err == nil {
			t.Errorf("port %q passed", bad)
		}
	}
}

func TestANewVersionProvesItself(t *testing.T) {
	placed := shortTrial(t)
	folder := trialFolderWith(t)
	up := false
	port := listenerPort(t, func(w http.ResponseWriter, r *http.Request) {
		if !up {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	answered := 0
	markAnswered = func(f string, v string, run int) {
		if f != folder || v != trialVersion || run != 1 {
			t.Errorf("answered for %s %s %d", f, v, run)
		}
		answered++
	}
	dbCalls := 0
	checks := trialChecks{
		config:   func() error { return nil },
		database: func() error { dbCalls++; return nil },
		http:     func() error { return checkHTTPAnswers(port) },
	}
	slept := []time.Duration{}
	sleep := func(d time.Duration) {
		slept = append(slept, d)
		if len(slept) == 3 {
			up = true // the listener answers from the fourth try on
		}
		if pending, _ := forkrelease.PendingTrial(folder); pending.Run != 1 {
			t.Error("the trial ended before the checks passed twice")
		}
		if *placed != 0 {
			t.Error("the launcher was put in place during the trial")
		}
	}
	if !proveUpdateTrial(folder, trialVersion, 1, checks, sleep) {
		t.Fatal("not proven")
	}
	if _, err := os.Lstat(filepath.Join(folder, "cosmos-update.trial")); err == nil {
		t.Error("the trial file is still there")
	}
	if *placed != 1 || answered != 1 {
		t.Errorf("the launcher was put in place %d times, the answer noted %d times", *placed, answered)
	}
	if slept[len(slept)-1] != trialSettle || dbCalls < 2 {
		t.Errorf("not checked again after the settling time: %v, %d database checks", slept, dbCalls)
	}
}

func TestATrialIsNotProvenByHalfAnswers(t *testing.T) {
	placed := shortTrial(t)
	trialGiveUp = 100 * time.Millisecond
	cases := map[string]trialChecks{
		"the database does not answer":  {config: func() error { return nil }, database: func() error { return errors.New("down") }, http: func() error { return nil }},
		"the HTTP port does not answer": {config: func() error { return nil }, database: func() error { return nil }, http: func() error { return errors.New("down") }},
		"no configuration":              {config: func() error { return errors.New("none") }, database: func() error { return nil }, http: func() error { return nil }},
	}
	for name, checks := range cases {
		folder := trialFolderWith(t)
		if proveUpdateTrial(folder, trialVersion, 1, checks, time.Sleep) {
			t.Errorf("%s: proven", name)
		}
		if _, err := os.Lstat(filepath.Join(folder, "cosmos-update.trial")); err != nil {
			t.Errorf("%s: the trial ended", name)
		}
	}

	// answers, then stops answering within the settling time: not proven
	folder := trialFolderWith(t)
	calls := 0
	flaky := trialChecks{config: func() error { return nil }, database: func() error { return nil }, http: func() error {
		calls++
		if calls%2 == 0 {
			return errors.New("gone")
		}
		return nil
	}}
	if proveUpdateTrial(folder, trialVersion, 1, flaky, time.Sleep) {
		t.Error("a server that stops answering a minute later was proven")
	}
	// another version on trial: not this server's to prove
	folder = trialFolderWith(t)
	ok := trialChecks{config: func() error { return nil }, database: func() error { return nil }, http: func() error { return nil }}
	if proveUpdateTrial(folder, "0.24.0-fork.11", 1, ok, time.Sleep) {
		t.Error("another version proved the trial")
	}
	if *placed != 0 {
		t.Error("the launcher was put in place without a proof")
	}
}

func TestAReturnToTheVersionBeforeIsReported(t *testing.T) {
	var told []utils.Notification
	previous := notifyRollback
	notifyRollback = func(n utils.Notification) { told = append(told, n) }
	defer func() { notifyRollback = previous }()

	english := func(text *utils.NotificationText) string {
		sentence := utils.NotificationEnglish(text.Key)
		for k, v := range text.Vars {
			sentence = strings.ReplaceAll(sentence, "{{"+k+"}}", v)
		}
		return sentence
	}
	cases := []struct {
		note forkrelease.RollbackNote
		want string
	}{
		{forkrelease.RollbackNote{Failed: "0.24.0-fork.10", Back: "0.24.0-fork.9", Reason: forkrelease.RollbackStopped, Starts: 3, Minutes: 5, Restored: true},
			"Cosmos went back to version 0.24.0-fork.9 by itself: version 0.24.0-fork.10 did not start properly (it stopped 3 times before it had proved itself). 0.24.0-fork.10 is not installed again; a later release will be. To allow it again, remove the line 0.24.0-fork.10 from "},
		{forkrelease.RollbackNote{Failed: "0.24.0-fork.10", Back: "0.24.0-fork.9", Reason: forkrelease.RollbackTimeout, Starts: 3, Minutes: 15, Restored: true},
			"Cosmos went back to version 0.24.0-fork.9 by itself: version 0.24.0-fork.10 did not start properly (it did not answer within 15 minutes). 0.24.0-fork.10 is not installed again; a later release will be. To allow it again, remove the line 0.24.0-fork.10 from "},
		{forkrelease.RollbackNote{Failed: "0.24.0-fork.10", Back: "0.24.0-fork.9", Reason: forkrelease.RollbackDamaged, Starts: 3, Minutes: 5, Restored: false},
			"could not go back to version 0.24.0-fork.9 by itself"},
	}
	for _, c := range cases {
		told = nil
		folder := t.TempDir()
		raw, _ := json.Marshal(c.note)
		os.WriteFile(filepath.Join(folder, "cosmos-update.rolledback"), raw, 0600)
		reportRollback(folder, "0.24.0-fork.9")
		reportRollback(folder, "0.24.0-fork.9") // the note is gone: once
		if len(told) != 1 {
			t.Fatalf("%s: told %d times", c.note.Reason, len(told))
		}
		n := told[0]
		if n.Recipient != "admin" || n.Level != "error" || n.Fork == nil || n.Fork.Kind != "update.rolledback" || n.Fork.Subject.Name != "0.24.0-fork.10" {
			t.Errorf("%s: %+v", c.note.Reason, n)
			continue
		}
		if got := english(n.Fork.What); !strings.Contains(got, c.want) || strings.Contains(got, "{{") || (c.note.Restored && !strings.Contains(got, filepath.Join(folder, "cosmos-update.failed"))) {
			t.Errorf("%s: says %q, want %q", c.note.Reason, got, c.want)
		}
		if got := english(n.Fork.Means); got == "" || strings.Contains(got, "{{") {
			t.Errorf("%s: means %q", c.note.Reason, got)
		}
		if english(n.Fork.Title) != "A server update was undone" {
			t.Errorf("title %q", english(n.Fork.Title))
		}
	}

	// no note: nothing
	told = nil
	reportRollback(t.TempDir(), "0.24.0-fork.9")
	if len(told) != 0 {
		t.Error("told without a note")
	}
}

// The hooks in upstream's files and the launcher's order of steps.
func TestTheTrialIsWiredIn(t *testing.T) {
	launcher := readSource(t, filepath.Join("launcher", "launcher.go"))
	main := launcher[strings.Index(launcher, "func main()"):]
	at := func(s string) int {
		i := strings.Index(main, s)
		if i < 0 {
			t.Fatalf("the launcher has no %q", s)
		}
		return i
	}
	watchdog, kill := at("forkrelease.RunWatchdog(os.Args)"), at(`exec.Command("killall", "cosmos")`)
	trial, noUpdate := at("forkrelease.LauncherTrial(currentFolder)"), at(`fmt.Println("No updates to install, starting Cosmos...")`)
	accepts, md5, keep := at("forkrelease.LauncherAccepts("), at("if hash != hex.EncodeToString"), at("if !forkrelease.KeepReplaced(currentFolder) { return }")
	unzip, undo, begin, remove := at("err = unzip(dlPath, currentFolder)"), at("forkrelease.UndoInstall(currentFolder, err)"), at("forkrelease.BeginTrial(currentFolder)"), at("os.Remove(dlPath)\n\n\t\tfmt.Println(\"Update complete")
	if !(watchdog < kill && trial < noUpdate && accepts < md5 && md5 < keep && keep < unzip && unzip < undo && undo < begin && begin < remove) {
		t.Error("the launcher's steps are not in the order: watchdog, killall, trial, no update, signature, MD5, keep, unzip, undo on error, trial begins")
	}
	if !strings.Contains(main[unzip:begin], "if err != nil {\n\t\t\tfmt.Println(err)\n\t\t\tforkrelease.UndoInstall(currentFolder, err)") {
		t.Error("a failed unzip is not undone")
	}

	update := readSource(t, "update.go")
	cleanUp := update[strings.Index(update, "func cleanUpUpdateFiles()"):]
	if pending, rename := strings.Index(cleanUp, "if forkrelease.TrialPending(currentFolder) { return }"), strings.Index(cleanUp, "os.Rename(updatedPath"); pending < 0 || pending > rename {
		t.Error("the launcher of an update on trial is put in place")
	}
	latest := update[strings.Index(update, "func GetLatestVersion("):]
	if skip, pick := strings.Index(latest, "if forkrelease.FailedHere(release.TagName) { continue }"), strings.Index(latest, "latestRelease = release"); skip < 0 || skip > pick {
		t.Error("a release that went back is still offered")
	}

	index := readSource(t, "index.go")
	if hook, serve := strings.Index(index, "startUpdateTrial()"), strings.LastIndex(index, "StartServer()"); hook < 0 || hook > serve {
		t.Error("the trial is not proven at start")
	}
	if !strings.Contains(readSource(t, "fork_update_trial.go"), "flushLauncherRefusal()") {
		t.Error("a refusal by the launcher is never told")
	}
	if strings.Count(readSource(t, "fork_update_trial.go"), "telegram.QuietStart()") != 1 {
		t.Error("the Telegram start message is not held back after a return")
	}
}

// Finding 4: the proof depends on nothing outside this machine.
func TestTheProofChecksOnlyLocalDatabases(t *testing.T) {
	asked := 0
	monitoring := func() error { asked++; return errors.New("no route to host") }
	ok := func() error { return nil }
	for _, c := range []struct {
		dialect    string
		newInstall bool
		users      func() error
		pass       bool
		asked      int
	}{
		{dialect: "postgres", users: ok, pass: true, asked: 0}, // on another machine: not part of the proof
		{dialect: utils.DialectSQLite, users: ok, pass: false, asked: 1},
		{dialect: utils.DialectSQLite, newInstall: true, users: ok, pass: true, asked: 0},
		{dialect: "postgres", users: func() error { return errors.New("locked") }, pass: false, asked: 0},
	} {
		asked = 0
		err := localDatabasesAnswer(trialDatabases{users: c.users, dialect: func() string { return c.dialect },
			newInstall: func() bool { return c.newInstall }, monitoring: monitoring})
		if (err == nil) != c.pass || asked != c.asked {
			t.Errorf("%+v: %v, monitoring asked %d times", c, err, asked)
		}
	}
	if err := localDatabasesAnswer(trialDatabases{users: ok, dialect: func() string { return utils.DialectSQLite },
		newInstall: func() bool { return false }, monitoring: ok}); err != nil {
		t.Error(err)
	}
}

// Finding 3: a planned restart is heard by the trial (a clean stop).
func TestAPlannedExitIsHeard(t *testing.T) {
	heard := 0
	utils.OnPlannedExit(func() { heard++ })
	utils.ForkNotice(utils.NoticeExitRestart)
	utils.ForkNotice(utils.NoticeExitUpdate, "version", "x")
	utils.ForkNotice(utils.NoticeContainerDying, "name", "x")
	if heard != 2 {
		t.Errorf("heard %d planned exits, want 2", heard)
	}
	source := readSource(t, "fork_update_trial.go")
	start := source[strings.Index(source, "func startUpdateTrial()"):]
	start = start[:strings.Index(start, "\n}\n")]
	if !strings.Contains(start, "utils.OnExit(cleanStop)") || !strings.Contains(start, "utils.OnPlannedExit(cleanStop)") {
		t.Error("a stop that was asked for is not noted as clean")
	}
}

// The server's proof fits the launcher's watchdog: the settle and a check
// after it end well inside the grace after the first answer, and the server
// keeps trying at least as long as the watchdog waits for the first answer.
func TestTheProofFitsTheWatchdog(t *testing.T) {
	if trialSettle+2*trialPoll+2*trialHTTPTimeout >= forkrelease.ProofGrace {
		t.Errorf("settle %v and checks do not fit the grace %v", trialSettle, forkrelease.ProofGrace)
	}
	if trialGiveUp < forkrelease.WatchdogWindow {
		t.Errorf("the server gives up (%v) before the watchdog (%v)", trialGiveUp, forkrelease.WatchdogWindow)
	}
	if forkrelease.WatchdogWindow < 15*time.Minute {
		t.Errorf("a start gets %v to answer: a migration after a reboot can take longer", forkrelease.WatchdogWindow)
	}
}
