package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/forkrelease"
	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: the new server's proof of a good start, and the report of
// a return to the version before.

const (
	trialVersion = "0.24.0-fork.10"
	trialID      = "0123456789abcdef"
)

var trialStart = forkrelease.TrialRun{Version: trialVersion, ID: trialID, Run: 1}

// trialFolderWith is an install with trialVersion on trial (start 1).
func trialFolderWith(t *testing.T) string {
	t.Helper()
	folder := t.TempDir()
	os.WriteFile(filepath.Join(folder, "meta.json"), []byte(`{"version": "`+trialVersion+`"}`), 0644)
	raw, _ := json.Marshal(map[string]interface{}{"format": 1, "state": "trial", "newVersion": trialVersion, "previousVersion": "0.24.0-fork.9", "id": trialID, "run": 1})
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

// panelLike answers as a healthy Cosmos does (measured on a real start):
// the status for the configured hostname, the panel's page.
func panelLike(status int, page int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cosmos/api/status":
			w.WriteHeader(status)
		case "/cosmos-ui/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(page)
			w.Write([]byte("<!doctype html><html><body>panel</body></html>"))
		default:
			http.NotFound(w, r)
		}
	}
}

func portOf(t *testing.T, url string) int {
	t.Helper()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(url, "http://"), "https://"))
	n, _ := strconv.Atoi(port)
	return n
}

func TestTheServerCheckOfATrial(t *testing.T) {
	asked := []string{}
	for _, c := range []struct {
		status, page int
		passes       bool
	}{
		{200, 200, true}, {401, 200, true}, {403, 200, true}, {204, 200, true},
		{400, 200, false}, {404, 200, false}, {302, 200, false}, {500, 200, false}, {503, 200, false},
		{200, 404, false}, {200, 302, false}, {200, 500, false},
	} {
		handler := panelLike(c.status, c.page)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			asked = append(asked, r.Host+r.URL.Path)
			handler(w, r)
		}))
		err := checkServerAnswers(listener{port: portOf(t, server.URL), host: "cosmos.server.local", apiOnName: true})
		server.Close()
		if (err == nil) != c.passes {
			t.Errorf("status %d, page %d: %v", c.status, c.page, err)
		}
	}
	if len(asked) == 0 || !strings.HasPrefix(asked[0], "cosmos.server.local:") {
		t.Errorf("asked %v: not for the configured hostname", asked)
	}
	// a page that is not HTML
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{}"))
	}))
	if err := checkServerAnswers(listener{port: portOf(t, server.URL), host: "cosmos.server.local", apiOnName: true}); err == nil {
		t.Error("a JSON answer was taken for the panel")
	}
	server.Close()
	// nothing listens
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	port := closed.Addr().(*net.TCPAddr).Port
	closed.Close()
	if err := checkServerAnswers(listener{port: port, host: "x", apiOnName: true}); err == nil {
		t.Error("a closed port passed")
	}
}

// In HTTPS mode: a real handshake on the HTTPS listener, with the
// configured hostname as server name.
func TestTheHTTPSCheckOfATrial(t *testing.T) {
	names := []string{}
	server := httptest.NewUnstartedServer(panelLike(200, 200))
	server.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		names = append(names, hello.ServerName)
		return nil, nil
	}}
	server.StartTLS()
	defer server.Close()
	target := listener{https: true, port: portOf(t, server.URL), host: "cosmos.server.local", apiOnName: true}
	if err := checkServerAnswers(target); err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 || names[0] != "cosmos.server.local" {
		t.Errorf("server names %v", names)
	}
	// plain HTTP on the HTTPS port: no handshake, no proof
	plain := httptest.NewServer(panelLike(200, 200))
	defer plain.Close()
	if err := checkServerAnswers(listener{https: true, port: portOf(t, plain.URL), host: "x", apiOnName: true}); err == nil {
		t.Error("a listener without TLS passed the HTTPS check")
	}

	// which listener: as StartServer decides
	modes := utils.HTTPSCertModeList
	for _, c := range []struct {
		config utils.HTTPConfig
		https  bool
		port   int
	}{
		{utils.HTTPConfig{HTTPSCertificateMode: modes["DISABLED"], HTTPPort: "8080", HTTPSPort: "8443", Hostname: "a.example.org"}, false, 8080},
		{utils.HTTPConfig{HTTPSCertificateMode: modes["SELFSIGNED"], SelfTLSCert: "c", SelfTLSKey: "k", HTTPPort: "8080", HTTPSPort: "8443"}, true, 8443},
		{utils.HTTPConfig{HTTPSCertificateMode: modes["SELFSIGNED"], HTTPPort: "8080", HTTPSPort: "8443"}, false, 8080},
		{utils.HTTPConfig{HTTPSCertificateMode: modes["LETSENCRYPT"], TLSCert: "c", TLSKey: "k", HTTPPort: "80", HTTPSPort: "443"}, true, 443},
	} {
		got, err := listenerOf(utils.Config{HTTPConfig: c.config})
		if err != nil || got.https != c.https || got.port != c.port {
			t.Errorf("%+v: %+v %v", c.config.HTTPSCertificateMode, got, err)
		}
	}
	if _, err := listenerOf(utils.Config{HTTPConfig: utils.HTTPConfig{HTTPPort: "80; rm"}}); err == nil {
		t.Error("a port that is no port")
	}
}

func TestANewVersionProvesItself(t *testing.T) {
	placed := shortTrial(t)
	folder := trialFolderWith(t)
	up := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		panelLike(200, 200)(w, r)
	}))
	defer server.Close()
	answered := 0
	markAnswered = func(f string, run forkrelease.TrialRun) error {
		if f != folder || run != trialStart {
			t.Errorf("answered for %s %+v", f, run)
		}
		answered++
		return nil
	}
	dbCalls := 0
	checks := trialChecks{
		config:   func() error { return nil },
		database: func() error { dbCalls++; return nil },
		http: func() error {
			return checkServerAnswers(listener{port: portOf(t, server.URL), host: "x", apiOnName: true})
		},
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
	if !proveUpdateTrial(folder, trialStart, checks, sleep) {
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
	markAnswered = func(string, forkrelease.TrialRun) error { return nil }
	cases := map[string]trialChecks{
		"the database does not answer":  {config: func() error { return nil }, database: func() error { return errors.New("down") }, http: func() error { return nil }},
		"the HTTP port does not answer": {config: func() error { return nil }, database: func() error { return nil }, http: func() error { return errors.New("down") }},
		"no configuration":              {config: func() error { return errors.New("none") }, database: func() error { return nil }, http: func() error { return nil }},
	}
	for name, checks := range cases {
		folder := trialFolderWith(t)
		if proveUpdateTrial(folder, trialStart, checks, time.Sleep) {
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
	if proveUpdateTrial(folder, trialStart, flaky, time.Sleep) {
		t.Error("a server that stops answering a minute later was proven")
	}
	// another version or another trial: not this server's to prove
	ok := trialChecks{config: func() error { return nil }, database: func() error { return nil }, http: func() error { return nil }}
	for _, other := range []forkrelease.TrialRun{{Version: "0.24.0-fork.11", ID: trialID, Run: 1}, {Version: trialVersion, ID: "fedcba9876543210", Run: 1}, {Version: trialVersion, ID: trialID, Run: 2}} {
		if proveUpdateTrial(trialFolderWith(t), other, ok, time.Sleep) {
			t.Errorf("%+v proved the trial", other)
		}
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
		{forkrelease.RollbackNote{Failed: "0.24.0-fork.10", Back: "0.24.0-fork.9", Reason: forkrelease.RollbackUnrecorded, Starts: 1, Minutes: 15, Restored: true, Cause: "no space left on device"},
			"Cosmos went back to version 0.24.0-fork.9 by itself: the trial of version 0.24.0-fork.10 could not be recorded (no space left on device). 0.24.0-fork.10 is not noted as failed and is installed again at the next update."},
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
		if got := english(n.Fork.What); !strings.Contains(got, c.want) || strings.Contains(got, "{{") || (c.note.Restored && c.note.Reason != forkrelease.RollbackUnrecorded && !strings.Contains(got, filepath.Join(folder, "cosmos-update.failed"))) {
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
	arm := source[strings.Index(source, "func armUpdateTrialStop()"):]
	arm = arm[:strings.Index(arm, "\n}\n")]
	if !strings.Contains(arm, "trialHooks{onExit: utils.OnExit, onPlannedExit: utils.OnPlannedExit, onFatal: utils.OnFatal}") {
		t.Error("the marks are not registered with the process's exit hooks")
	}
	// the very first thing main does, before any other exit hook exists
	index := readSource(t, "index.go")
	main := index[strings.Index(index, "func main() {"):]
	if first := strings.TrimSpace(strings.SplitN(main, "\n", 3)[1]); !strings.HasPrefix(first, "armUpdateTrialStop()") {
		t.Errorf("main begins with %q", first)
	}
	if strings.Contains(index[:strings.Index(index, "func main() {")], "OnExit(") {
		t.Error("an exit hook is registered before main")
	}
}

// N7: only a fatal about Docker is excused (and only when the launcher
// cannot reach Docker either, see forkrelease).
func TestOnlyADockerFatalIsNoted(t *testing.T) {
	if !dockerFatal("Docker connection died, couldn't recover... Restarting") {
		t.Error("the Docker fatal of docker/events.go is not recognised")
	}
	events := readSource(t, filepath.Join("docker", "events.go"))
	if !strings.Contains(events, `utils.Fatal("Docker connection died`) {
		t.Error("docker/events.go no longer ends with that message: update dockerFatal")
	}
	for _, other := range []string{"Listening to HTTPS", "Cannot open auth.db", ""} {
		if dockerFatal(other) {
			t.Errorf("%q taken for a Docker fatal", other)
		}
	}
}

// N3: what the launcher left for a person while the server runs is told.
func TestAnUpdateThatNeedsAttentionIsTold(t *testing.T) {
	var told []utils.Notification
	previous := notifyRollback
	notifyRollback = func(n utils.Notification) { told = append(told, n) }
	defer func() { notifyRollback = previous }()
	folder := t.TempDir()
	reportAttention(folder)
	if len(told) != 0 {
		t.Fatal("told without a README")
	}
	os.WriteFile(forkrelease.AttentionFile(folder), []byte("Cosmos update: needs attention (now).\n\nCosmos went back to version 0.24.0-fork.9 and runs it, but 0.24.0-fork.10 could not be noted as failed.\n"), 0600)
	reportAttention(folder)
	if len(told) != 1 || told[0].Fork.Kind != "update.attention" || told[0].Level != "error" ||
		!strings.Contains(told[0].Fork.What.Vars["summary"], "could not be noted as failed") || told[0].Fork.Means.Vars["file"] != forkrelease.AttentionFile(folder) {
		t.Errorf("told %+v", told)
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

// At run time: nothing is armed without a trial of the installed version
// or for a command of the binary; with one, the clean stop goes to the
// first exit hook and marks this start.
func TestArmingAtRunTime(t *testing.T) {
	count := 0
	var exit func()
	var fatal func(string)
	hooks := trialHooks{onExit: func(f func()) { count++; exit = f }, onPlannedExit: func(func()) { count++ }, onFatal: func(f func(string)) { count++; fatal = f }}
	if run := armTrialStop([]string{"cosmos"}, t.TempDir(), hooks); run != nil || count != 0 {
		t.Fatalf("armed without a trial: %+v, %d hooks", run, count)
	}
	folder := trialFolderWith(t)
	if run := armTrialStop([]string{"cosmos", "install"}, folder, hooks); run != nil || count != 0 {
		t.Fatal("armed for a command")
	}
	os.WriteFile(filepath.Join(folder, "meta.json"), []byte(`{"version": "0.24.0-fork.9"}`), 0644)
	if run := armTrialStop([]string{"cosmos"}, folder, hooks); run != nil || count != 0 {
		t.Fatal("armed for a version that is not the one on trial")
	}
	os.WriteFile(filepath.Join(folder, "meta.json"), []byte(`{"version": "`+trialVersion+`"}`), 0644)
	run := armTrialStop([]string{"cosmos"}, folder, hooks)
	if run == nil || *run != trialStart || count != 3 {
		t.Fatalf("not armed: %+v, %d hooks", run, count)
	}
	exit()
	fatal("Docker connection died, couldn't recover... Restarting")
	for _, kind := range []string{"clean", "docker"} {
		if _, err := os.Stat(filepath.Join(folder, "cosmos"+"-update.mark."+trialID+".1."+kind)); err != nil {
			t.Errorf("no %s mark: %v", kind, err)
		}
	}
}

// F4: the proof against the server's own host checks (the middlewares of
// the API and the panel, configured as InitServer does), for every form of
// hostname. The whole router is not built here: InitServer needs the
// static folder, Docker and the databases.
func TestTheProofPassesTheServersHostChecks(t *testing.T) {
	previous := utils.GetBaseMainConfig()
	defer utils.LoadBaseMainConfig(previous)
	for _, c := range []struct {
		hostname  string
		acceptAll bool
		setup     bool
	}{
		{hostname: "cosmos.server.local"}, {hostname: "cosmos.server.local:8443"}, {hostname: "192.0.2.10"}, {hostname: "192.0.2.10:8080"},
		{hostname: "[2001:db8::1]"}, {hostname: "[2001:db8::1]:8443"}, {hostname: "0.0.0.0"}, {hostname: "", acceptAll: true},
		{hostname: "cosmos.server.local", setup: true},
	} {
		config := utils.Config{}
		config.NewInstall = c.setup
		config.HTTPConfig.Hostname = c.hostname
		config.HTTPConfig.AcceptAllInsecureHostname = c.acceptAll
		config.HTTPConfig.HTTPSCertificateMode = utils.HTTPSCertModeList["DISABLED"]
		utils.LoadBaseMainConfig(config)

		var api, ui http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }), panelLike(200, 200)
		if !c.acceptAll { // as InitServer adds them
			api = utils.EnsureHostname(utils.EnsureHostnameCosmosAPI(api))
			ui = utils.EnsureHostname(ui)
		}
		mux := http.NewServeMux()
		mux.Handle("/cosmos/api/status", api)
		mux.Handle("/cosmos-ui/", ui)
		server := httptest.NewServer(mux)
		config.HTTPConfig.HTTPPort = strconv.Itoa(portOf(t, server.URL))
		target, err := listenerOf(config)
		if err == nil {
			err = checkServerAnswers(target)
		}
		server.Close()
		if err != nil {
			t.Errorf("%q (accept all %v, setup %v): %v", c.hostname, c.acceptAll, c.setup, err)
		}
	}
	// a server that answers the API with 400 on a plain name is not healthy
	config := utils.Config{}
	config.HTTPConfig.Hostname = "cosmos.server.local"
	broken := httptest.NewServer(panelLike(400, 200))
	defer broken.Close()
	config.HTTPConfig.HTTPPort = strconv.Itoa(portOf(t, broken.URL))
	target, _ := listenerOf(config)
	if checkServerAnswers(target) == nil {
		t.Error("a 400 of the API on a plain name passed")
	}
}

// A server in setup is proven by the local checks and its panel page: its
// status route asks Docker without a time limit.
func TestASetupIsProvenWithoutTheStatusRoute(t *testing.T) {
	previous := trialHTTPTimeout
	trialHTTPTimeout = time.Second
	defer func() { trialHTTPTimeout = previous }()
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cosmos/api/status" {
			time.Sleep(3 * time.Second) // a dockerd that hangs
		}
		panelLike(200, 200)(w, r)
	}))
	defer hanging.Close()
	if err := checkServerAnswers(listener{port: portOf(t, hanging.URL), host: "x", setup: true, apiOnName: true}); err != nil {
		t.Errorf("a server in setup failed: %v", err)
	}
	if err := checkServerAnswers(listener{port: portOf(t, hanging.URL), host: "x", apiOnName: true}); err == nil {
		t.Error("a hanging status passed after the setup")
	}
}
