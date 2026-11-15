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
// a query, its listener on 127.0.0.1 (HTTPS with a real handshake when the
// server serves HTTPS) answers /cosmos/api/status as a healthy server does
// and serves the panel's page, and all of it still holds a minute later.
// Then the trial ends and only then is the launcher that came with the
// update put in place, so during a trial the launcher that runs is the old,
// proven one.
//
// A stop that was asked for is noted as clean from the very start of the
// process (armUpdateTrialStop, the first line of main), before the slow exit
// hooks run, so it does not count against the new version. An exit because
// Docker could not be reached is noted too: the launcher does not count it
// when it cannot reach Docker either.
//
// After going back, the old server tells the administrators (notification
// update.rolledback, which Telegram receives like every notification), and
// its Telegram start message is not sent: it would claim an update. Only the
// program files go back: data a newer version migrated stays as it is.

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
			_, err := listenerOf(utils.GetMainConfig())
			return err
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
			// GetMainConfig has COSMOS_HTTP_PORT / COSMOS_HTTPS_PORT applied, like the listener
			target, err := listenerOf(utils.GetMainConfig())
			if err != nil {
				return err
			}
			return checkServerAnswers(target)
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

// listener is where this server is asked on 127.0.0.1, and how.
type listener struct {
	https bool
	port  int
	host  string // the configured hostname without port or brackets: Host header and TLS server name
	// apiOnName: the server's host check of the API (EnsureHostnameCosmosAPI)
	// can let this hostname through, so /cosmos/api/status must answer as a
	// healthy server does. False for a hostname with a port or an IPv6
	// literal: that check compares the whole configured hostname with the
	// request's host before its first ":", which never matches them, so a
	// healthy server answers 400 there to everyone.
	apiOnName bool
	// setup: the setup is not finished. Its status route asks Docker without
	// a time limit, so it is not asked: the local checks and the panel's page
	// prove a server that serves nobody yet.
	setup bool
}

func trialPort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, errors.New("not a port")
	}
	return port, nil
}

// listenerOf decides, as StartServer does, whether the server serves HTTPS,
// and takes the hostname apart as the server's host checks do.
func listenerOf(main utils.Config) (listener, error) {
	config := main.HTTPConfig
	mode := config.HTTPSCertificateMode
	cert, key := config.TLSCert, config.TLSKey
	if mode == utils.HTTPSCertModeList["SELFSIGNED"] {
		cert, key = config.SelfTLSCert, config.SelfTLSKey
	}
	https := (mode == utils.HTTPSCertModeList["SELFSIGNED"] || mode == utils.HTTPSCertModeList["PROVIDED"] || mode == utils.HTTPSCertModeList["LETSENCRYPT"]) &&
		cert != "" && key != ""
	value := config.HTTPPort
	if https {
		value = config.HTTPSPort
	}
	port, err := trialPort(value)
	if err != nil {
		return listener{}, errors.New("the configuration holds no usable port to listen on")
	}
	configured := config.Hostname
	host := configured
	if h, _, err := net.SplitHostPort(configured); err == nil {
		host = h // name:port, IPv4:port, [IPv6]:port
	} else if strings.HasPrefix(configured, "[") && strings.HasSuffix(configured, "]") {
		host = configured[1 : len(configured)-1] // [IPv6]
	}
	anyHost := main.NewInstall || configured == "0.0.0.0" || config.AcceptAllInsecureHostname
	if host == "" || host == "0.0.0.0" {
		host = "localhost" // any name is let through then
	}
	return listener{https: https, port: port, host: host, setup: main.NewInstall,
		apiOnName: anyHost || !strings.Contains(configured, ":")}, nil
}

// checkServerAnswers asks the server as a browser would, on 127.0.0.1:
// /cosmos/api/status must answer as a healthy server does (2xx, 401 or
// 403), and /cosmos-ui/ must serve the panel's page. In HTTPS mode with a
// real handshake (the certificate is not checked: this is the machine
// itself; the server name is the configured hostname, so the server picks
// its certificate as for a visitor). No proxy, no redirect followed.
func checkServerAnswers(target listener) error {
	address := "127.0.0.1:" + strconv.Itoa(target.port)
	transport := &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: trialHTTPTimeout}).DialContext(ctx, "tcp", address)
		},
		TLSClientConfig: &tls.Config{ServerName: target.host, InsecureSkipVerify: true}, // loopback only, see above
	}
	client := &http.Client{Timeout: trialHTTPTimeout, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	scheme := "http"
	if target.https {
		scheme = "https"
	}
	base := scheme + "://" + net.JoinHostPort(target.host, strconv.Itoa(target.port))
	where := scheme + " on port " + strconv.Itoa(target.port)

	if !target.setup {
		status, _, _, err := ask(client, base+"/cosmos/api/status")
		if err != nil {
			return errors.New("the server does not answer (" + where + ")")
		}
		healthy := (status >= 200 && status < 300) || status == http.StatusUnauthorized || status == http.StatusForbidden
		if !target.apiOnName {
			healthy = status < 500 // its own host check refuses this hostname form to everyone
		}
		if !healthy {
			return errors.New("the server answers /cosmos/api/status with status " + strconv.Itoa(status) + " (" + where + ")")
		}
	}
	status, kind, body, err := ask(client, base+"/cosmos-ui/")
	if err != nil {
		return errors.New("the server does not serve the panel (" + where + ")")
	}
	lower := strings.ToLower(strings.TrimSpace(body))
	if status != http.StatusOK || !(strings.Contains(kind, "text/html") || strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html")) {
		return errors.New("the server does not serve the panel's page: status " + strconv.Itoa(status) + " (" + where + ")")
	}
	return nil
}

func ask(client *http.Client, url string) (status int, kind string, body string, err error) {
	response, err := client.Get(url)
	if err != nil {
		return 0, "", "", err
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	return response.StatusCode, response.Header.Get("Content-Type"), string(raw), nil
}

// placeUpdatedLauncher puts the launcher that came with the update in place
// (upstream's cleanUpUpdateFiles). A variable for the tests.
var placeUpdatedLauncher = cleanUpUpdateFiles

// markAnswered tells the watchdog the start answered. A variable for the tests.
var markAnswered = forkrelease.MarkAnswered

// proveUpdateTrial waits until the checks pass, and pass again trialSettle
// later; then the trial ends. False when it gave up.
func proveUpdateTrial(folder string, run forkrelease.TrialRun, checks trialChecks, sleep func(time.Duration)) bool {
	version := forkrelease.SafeText(run.Version)
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
		if err := markAnswered(folder, run); err != nil { // the watchdog now waits for the proof
			utils.Warn("Update: the first answer could not be noted for the watchdog: " + err.Error())
		}
		sleep(trialSettle)
		if err := checks.run(); err != nil {
			utils.Warn("Update: version " + version + " answered, then not any more: " + err.Error())
			lastReason = err.Error()
			continue
		}
		if err := forkrelease.ProveTrial(folder, run); err != nil {
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

// armedRun is the start on trial this process is, set by armUpdateTrialStop.
var armedRun *forkrelease.TrialRun

// armUpdateTrialStop is the hook at the very start of main: when this
// process is the server of a version on trial, a SIGTERM or SIGINT from now
// on, a planned restart, and a Docker that cannot be reached are noted for
// the launcher, the stop first, before the other exit hooks run.
func armUpdateTrialStop() {
	folder, err := trialFolder()
	if err != nil {
		return
	}
	armedRun = armTrialStop(os.Args, folder, trialHooks{onExit: utils.OnExit, onPlannedExit: utils.OnPlannedExit, onFatal: utils.OnFatal})
}

// trialHooks are where the stop marks are registered. Fields for the tests.
type trialHooks struct {
	onExit        func(func())
	onPlannedExit func(func())
	onFatal       func(func(string))
}

// armTrialStop registers the marks when folder holds a trial of the
// installed version and args are those of the server (no command). The
// start on trial, or nil.
func armTrialStop(args []string, folder string, hooks trialHooks) *forkrelease.TrialRun {
	if len(args) != 1 { // a command of the binary, not the server
		return nil
	}
	run, ok := forkrelease.TrialOf(folder)
	if !ok {
		return nil
	}
	cleanStop := func() {
		if err := forkrelease.MarkCleanStop(folder, run); err != nil {
			utils.Error("Update: the clean stop of the version on trial could not be noted; the launcher counts this start as failed", err)
		}
	}
	hooks.onExit(cleanStop) // the first exit hook: it runs before rclone, DNS and Telegram
	hooks.onPlannedExit(cleanStop)
	hooks.onFatal(func(message string) {
		if dockerFatal(message) {
			if err := forkrelease.MarkDockerUnreachable(folder, run); err != nil {
				utils.Error("Update: that Docker could not be reached could not be noted", err)
			}
		}
	})
	return &run
}

// dockerFatal: the fatal error is about Docker not being reachable.
func dockerFatal(message string) bool {
	return strings.HasPrefix(message, "Docker connection died")
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
	reportAttention(folder)
	go pruneKept(folder)
	if armedRun == nil || armedRun.Version != GetCosmosVersion() {
		return
	}
	run := *armedRun
	utils.Log("Update: version " + run.Version + " is on trial (start " + strconv.Itoa(run.Run) + "; it goes back after " + strconv.Itoa(forkrelease.TrialStarts) + " failed starts)")
	go proveUpdateTrial(folder, run, serverTrialChecks(), time.Sleep)
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
	switch {
	case !note.Restored:
		utils.Error("Update: version "+note.Failed+" did not start properly and Cosmos could not go back to version "+note.Back+": the kept files did not match their checksums", nil)
	case note.Reason == forkrelease.RollbackUnrecorded:
		utils.Error("Update: the trial of version "+note.Failed+" could not be recorded ("+note.Cause+"); Cosmos went back to version "+note.Back+" (running "+forkrelease.SafeText(running)+")", nil)
	default:
		utils.Error("Update: version "+note.Failed+" did not start properly; Cosmos went back to version "+note.Back+" by itself (running "+forkrelease.SafeText(running)+")", nil)
	}
}

// reportAttention tells what the launcher left for a person while the
// server runs (a finished restore whose bookkeeping could not be written).
func reportAttention(folder string) {
	text := forkrelease.Attention(folder)
	if text == "" {
		return
	}
	utils.Error("Update: "+strings.ReplaceAll(text, "\n", " "), nil)
	summary := text
	if lines := strings.SplitN(text, "\n\n", 2); len(lines) == 2 {
		summary = lines[1]
	}
	if len(summary) > 600 {
		summary = summary[:600] + "…"
	}
	file := forkrelease.AttentionFile(folder)
	notifyRollback(utils.Notification{
		Recipient: "admin",
		Level:     "error",
		Fork: &utils.NotificationInfo{
			Kind:    "update.attention",
			Thread:  "update.attention",
			Title:   utils.NText("notif.update.attention.title"),
			Subject: &utils.NotificationSubject{Type: "file", Name: file},
			What:    utils.NText("notif.update.attention.what", "summary", strings.ReplaceAll(summary, "\n", " ")),
			Means:   utils.NText("notif.update.attention.means", "file", file),
			Raw:     text,
		},
	})
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
	case note.Reason == forkrelease.RollbackUnrecorded:
		what = "notif.update.rolledback.what.unrecorded"
	}
	return utils.Notification{
		Recipient: "admin",
		Level:     "error",
		Fork: &utils.NotificationInfo{
			Kind:    "update.rolledback",
			Thread:  "update.rolledback:" + note.Failed,
			Title:   utils.NText("notif.update.rolledback.title"),
			Subject: &utils.NotificationSubject{Type: "release", Name: note.Failed, Link: link},
			What:    utils.NText(what, "back", note.Back, "failed", note.Failed, "starts", starts, "minutes", minutes, "file", note.File, "cause", note.Cause),
			Means:   means,
			Doing:   utils.NText("notif.update.rolledback.doing"),
			Steps:   []utils.NotificationStep{{NotificationText: *utils.NText("notif.step.update.general"), Link: link}},
			Raw:     "failed " + note.Failed + ", back to " + note.Back + ", reason " + note.Reason + ", starts " + starts,
		},
	}
}
