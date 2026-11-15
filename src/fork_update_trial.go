package main

// Fork addition: a new version proves that it started properly, and an
// update that went back by itself is reported.
//
// The launcher installs an update on trial (package forkrelease, trial.go):
// it keeps the files it replaces, counts the failed starts of the new
// version and goes back to the version before after three, a failed start
// being one that ended before the proof without a clean stop, or that did
// not answer within the watchdog's window. Here the new server proves
// itself with local checks only (nothing outside the machine, no Docker):
// the configuration is loaded, the SQLite databases on this machine answer
// a query, its HTTP port answers on 127.0.0.1 (any status below 500), and
// all of it still holds a minute later. Then the trial ends and only then is
// the launcher that came with the update put in place, so during a trial
// the launcher that runs is the old, proven one. A stop that was asked for
// (SIGTERM, SIGINT, a planned restart) is noted as clean, so it does not
// count against the new version.
//
// After going back, the old server tells the administrators (notification
// update.rolledback, which Telegram receives like every notification), and
// its Telegram start message is not sent: it would claim an update. Only the
// program files go back: data a newer version migrated stays as it is.

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/azukaar/cosmos-server/src/forkrelease"
	"github.com/azukaar/cosmos-server/src/telegram"
	"github.com/azukaar/cosmos-server/src/utils"
)

// Variables so tests can shorten them.
var (
	trialSettle      = 60 * time.Second // still answering this long after the first answer
	trialPoll        = 3 * time.Second
	trialGiveUp      = 20 * time.Minute // the launcher's watchdog ends the start before
	trialPruneEvery  = time.Hour
	trialHTTPTimeout = 5 * time.Second
)

// trialChecks are what the new server must pass. Fields so tests can fake
// them.
type trialChecks struct {
	config   func() error
	database func() error
	http     func() error
}

func (c trialChecks) run() error {
	for _, check := range []func() error{c.config, c.database, c.http} {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

// serverTrialChecks are the checks against this server.
func serverTrialChecks() trialChecks {
	return trialChecks{
		config: func() error {
			if _, err := trialHTTPPort(utils.GetMainConfig().HTTPConfig.HTTPPort); err != nil {
				return errors.New("the configuration holds no usable HTTP port")
			}
			return nil
		},
		database: func() error {
			return localDatabasesAnswer(trialDatabases{
				users:      func() error { _, err := utils.ListUsers("admin"); return err },
				dialect:    utils.MonitoringDialect,
				newInstall: func() bool { return utils.GetMainConfig().NewInstall },
				monitoring: func() error {
					db, err := utils.MonitoringReadDB()
					if err != nil {
						return err
					}
					var one int
					if err := db.QueryRow("SELECT 1").Scan(&one); err != nil || one != 1 {
						return errors.New("no answer")
					}
					return nil
				},
			})
		},
		http: func() error {
			// GetMainConfig has COSMOS_HTTP_PORT applied, like the listener
			return checkHTTPAnswers(utils.GetMainConfig().HTTPConfig.HTTPPort)
		},
	}
}

// trialDatabases are the databases a start is checked against.
type trialDatabases struct {
	users      func() error  // auth.db, always on this machine
	dialect    func() string // of the monitoring database
	newInstall func() bool
	monitoring func() error
}

// localDatabasesAnswer: the SQLite databases on this machine answer. A
// monitoring database on another machine (Postgres) is not part of the
// proof: a good release must not be undone because another machine is down.
func localDatabasesAnswer(d trialDatabases) error {
	if err := d.users(); err != nil {
		return errors.New("the user database does not answer")
	}
	if d.newInstall() || d.dialect() != utils.DialectSQLite {
		return nil // not opened before the setup, or not on this machine
	}
	if err := d.monitoring(); err != nil {
		return errors.New("the monitoring database does not answer")
	}
	return nil
}

func trialHTTPPort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, errors.New("not a port")
	}
	return port, nil
}

// checkHTTPAnswers: the server answers on its HTTP port on 127.0.0.1 with a
// status below 500. No proxy, no redirect followed.
func checkHTTPAnswers(portValue string) error {
	port, err := trialHTTPPort(portValue)
	if err != nil {
		return errors.New("the configuration holds no usable HTTP port")
	}
	client := &http.Client{
		Timeout:       trialHTTPTimeout,
		Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/cosmos/api/status")
	if err != nil {
		return errors.New("the HTTP port " + strconv.Itoa(port) + " does not answer")
	}
	io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	response.Body.Close()
	if response.StatusCode >= 500 {
		return errors.New("the HTTP port " + strconv.Itoa(port) + " answers with status " + strconv.Itoa(response.StatusCode))
	}
	return nil
}

// placeUpdatedLauncher puts the launcher that came with the update in place
// (upstream's cleanUpUpdateFiles). A variable for the tests.
var placeUpdatedLauncher = cleanUpUpdateFiles

// markAnswered tells the watchdog the start answered. A variable for the tests.
var markAnswered = forkrelease.MarkAnswered

// proveUpdateTrial waits until the checks pass, and pass again trialSettle
// later; then the trial of version ends. False when it gave up.
func proveUpdateTrial(folder string, version string, run int, checks trialChecks, sleep func(time.Duration)) bool {
	tries := int(trialGiveUp / trialPoll)
	lastReason := ""
	for try := 0; try < tries; try++ {
		if try > 0 {
			sleep(trialPoll)
		}
		if err := checks.run(); err != nil {
			if err.Error() != lastReason {
				utils.Log("Update: version " + version + " is on trial and has not proved itself yet: " + err.Error())
				lastReason = err.Error()
			}
			continue
		}
		markAnswered(folder, version, run) // the watchdog now waits for the proof
		sleep(trialSettle)
		if err := checks.run(); err != nil {
			utils.Warn("Update: version " + version + " answered, then not any more: " + err.Error())
			lastReason = err.Error()
			continue
		}
		if err := forkrelease.ProveTrial(folder, version, run); err != nil {
			utils.Error("Update: the trial of version "+version+" could not be ended", err)
			return false
		}
		utils.Log("Update: version " + version + " started properly; its trial is over")
		placeUpdatedLauncher()
		return true
	}
	utils.Error("Update: version "+version+" did not prove itself; the launcher goes back to the version before at a later start", nil)
	return false
}

func trialFolder() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(self), nil
}

// startUpdateTrial is the hook at start, just before the server listens: it
// reports a return to this version and, when this version is on trial,
// starts proving it.
func startUpdateTrial() {
	folder, err := trialFolder()
	if err != nil {
		return
	}
	reportRollback(folder, GetCosmosVersion())
	flushLauncherRefusal() // an update the launcher refused at this start, now that the databases are open
	go pruneKept(folder)
	trial, pending := forkrelease.PendingTrial(folder)
	version := GetCosmosVersion()
	if !pending || trial.NewVersion != version {
		return
	}
	run := trial.Run
	// a stop that was asked for is not a failed start
	cleanStop := func() { forkrelease.MarkCleanStop(folder, version, run) }
	utils.OnExit(cleanStop)
	utils.OnPlannedExit(cleanStop)
	utils.Log("Update: version " + version + " is on trial (start " + strconv.Itoa(run) + "; it goes back after " + strconv.Itoa(forkrelease.TrialStarts) + " failed starts)")
	go proveUpdateTrial(folder, version, run, serverTrialChecks(), time.Sleep)
}

// pruneKept removes the copy kept by the last update once its version has
// proved itself forkrelease.KeptFor ago (checked every hour).
func pruneKept(folder string) {
	for {
		if forkrelease.PruneKept(folder, time.Now()) {
			utils.Log("Update: the copy of the version before the last update was removed; it proved itself long enough ago")
		}
		time.Sleep(trialPruneEvery)
	}
}

// The start message of Telegram is decided when it starts, before
// startUpdateTrial runs: the note of the launcher is looked at here.
func init() {
	if folder, err := trialFolder(); err == nil && forkrelease.RollbackNoted(folder) {
		telegram.QuietStart()
	}
}

// notifyRollback tells the administrators. A variable so tests can replace it.
var notifyRollback = func(n utils.Notification) {
	utils.WriteNotification(n)
}

// reportRollback reports what the launcher noted when it went back.
func reportRollback(folder string, running string) {
	note, found := forkrelease.TakeRollbackNote(folder)
	if !found {
		return
	}
	notifyRollback(rollbackNotification(note))
	if note.Restored {
		utils.Error("Update: version "+note.Failed+" did not start properly; Cosmos went back to version "+note.Back+" by itself (running "+forkrelease.SafeText(running)+")", nil)
	} else {
		utils.Error("Update: version "+note.Failed+" did not start properly and Cosmos could not go back to version "+note.Back+": the kept files did not match their checksums", nil)
	}
}

func rollbackNotification(note forkrelease.RollbackNote) utils.Notification {
	const link = "/cosmos-ui/config-general"
	starts, minutes := strconv.Itoa(note.Starts), strconv.Itoa(note.Minutes)
	what := "notif.update.rolledback.what.stopped"
	means := utils.NText("notif.update.rolledback.means", "back", note.Back, "failed", note.Failed)
	switch {
	case !note.Restored:
		what = "notif.update.rolledback.what.damaged"
		means = utils.NText("notif.update.rolledback.means.damaged")
	case note.Reason == forkrelease.RollbackTimeout:
		what = "notif.update.rolledback.what.timeout"
	}
	return utils.Notification{
		Recipient: "admin",
		Level:     "error",
		Fork: &utils.NotificationInfo{
			Kind:    "update.rolledback",
			Thread:  "update.rolledback:" + note.Failed,
			Title:   utils.NText("notif.update.rolledback.title"),
			Subject: &utils.NotificationSubject{Type: "release", Name: note.Failed, Link: link},
			What:    utils.NText(what, "back", note.Back, "failed", note.Failed, "starts", starts, "minutes", minutes, "file", note.File),
			Means:   means,
			Doing:   utils.NText("notif.update.rolledback.doing"),
			Steps:   []utils.NotificationStep{{NotificationText: *utils.NText("notif.step.update.general"), Link: link}},
			Raw:     "failed " + note.Failed + ", back to " + note.Back + ", reason " + note.Reason + ", starts " + starts,
		},
	}
}
