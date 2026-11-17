package main

// Fork addition (plan 48): an update that proved itself after its trial is
// told (kind update.proven, information). It is called after
// proveUpdateTrial returned true, in the goroutine that ran the proof, so it
// can neither fail nor delay the proof; it never panics out and does not
// wait for anything but the notifications' database.

import (
	"strconv"
	"time"

	"github.com/azukaar/cosmos-server/src/forkrelease"
	"github.com/azukaar/cosmos-server/src/utils"
)

// writeProvenNotice is what writes it. A variable for the tests.
var writeProvenNotice = utils.WriteNotification

func noteUpdateProven(run forkrelease.TrialRun, took time.Duration) {
	defer func() { recover() }()
	version := forkrelease.SafeText(run.Version)
	duration := utils.NotificationDuration(took)
	writeProvenNotice(utils.Notification{Recipient: "admin", Level: "info", Fork: &utils.NotificationInfo{
		Kind: "update.proven", Source: "updates",
		Title:   utils.NText("notif.update.proven.title", "version", version),
		What:    utils.NText("notif.update.proven.what", "version", version, "duration", duration, "run", strconv.Itoa(run.Run)),
		Subject: &utils.NotificationSubject{Type: "release", Name: version, Link: "/cosmos-ui/config-general/general"},
	}})
}
