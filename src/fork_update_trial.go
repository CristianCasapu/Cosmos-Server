package main

// Fork addition: a new version proves that it started properly, and an
// update that went back by itself is reported.
//
// The launcher installs an update on trial (package forkrelease, trial.go):
// it keeps the files it replaces, counts the starts of the new version and
// goes back to the version before when the new one has not proved itself
// within three starts, or when one start did not prove itself within five
// minutes. Here the new server proves itself: the configuration is loaded,
// the database answers a query, its HTTP port answers on 127.0.0.1 (any
// status below 500), and all of it still holds a minute later. Then the
// trial ends and only then is the launcher that came with the update put in
// place, so during a trial the launcher that runs is the old, proven one.
//
// After going back, the old server tells the administrators (notification
// update.rolledback, which Telegram receives like every notification), and
// its Telegram start message is not sent: it would claim an update.

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
	trialGiveUp      = 10 * time.Minute // the launcher's watchdog ends the start long before
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
			if _, err := utils.ListUsers("admin"); err != nil {
				return errors.New("the user database does not answer")
			}
			if utils.GetMainConfig().NewInstall {
				return nil // the monitoring database opens after the setup
			}
			db, err := utils.MonitoringReadDB()
			if err != nil {
				return errors.New("the monitoring database is not open")
			}
			var one int
			if err := db.QueryRow("SELECT 1").Scan(&one); err != nil || one != 1 {
				return errors.New("the monitoring database does not answer")
			}
			return nil
		},
		http: func() error {
			// GetMainConfig has COSMOS_HTTP_PORT applied, like the listener
			return checkHTTPAnswers(utils.GetMainConfig().HTTPConfig.HTTPPort)
		},
	}
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

// proveUpdateTrial waits until the checks pass, and pass again trialSettle
// later; then the trial of version ends. False when it gave up.
func proveUpdateTrial(folder string, version string, checks trialChecks, sleep func(time.Duration)) bool {
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
		sleep(trialSettle)
		if err := checks.run(); err != nil {
			utils.Warn("Update: version " + version + " answered, then not any more: " + err.Error())
			lastReason = err.Error()
			continue
		}
		if err := forkrelease.ProveTrial(folder, version); err != nil {
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
	trial, pending := forkrelease.PendingTrial(folder)
	version := GetCosmosVersion()
	if !pending || trial.NewVersion != version {
		return
	}
	utils.Log("Update: version " + version + " is on trial (start " + strconv.Itoa(trial.Starts) + " of " + strconv.Itoa(forkrelease.TrialStarts) + ")")
	go proveUpdateTrial(folder, version, serverTrialChecks(), time.Sleep)
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
			What:    utils.NText(what, "back", note.Back, "failed", note.Failed, "starts", starts, "minutes", minutes),
			Means:   means,
			Doing:   utils.NText("notif.update.rolledback.doing"),
			Steps:   []utils.NotificationStep{{NotificationText: *utils.NText("notif.step.update.general"), Link: link}},
			Raw:     "failed " + note.Failed + ", back to " + note.Back + ", reason " + note.Reason + ", starts " + starts,
		},
	}
}
