package utils

// Fork addition: the kind and the English of the notification about an
// update that went back by itself (src/fork_update_trial.go). Kept in a file
// of their own and added to the catalogue when the package loads; the same
// keys are in translation.json (TestNotificationTextsAreInThePanel covers
// them).

var rollbackNotificationKinds = map[string]notificationKind{
	"update.rolledback": {"updates", "notif.update.rolledback.title"},
}

var rollbackNotificationTexts = map[string]string{
	"notif.update.rolledback.title":         "A server update was undone",
	"notif.update.rolledback.what.stopped":  "Cosmos went back to version {{back}} by itself: version {{failed}} did not start properly (it stopped {{starts}} times). {{failed}} is not installed again; a later release will be.",
	"notif.update.rolledback.what.timeout":  "Cosmos went back to version {{back}} by itself: version {{failed}} did not start properly (it did not answer within {{minutes}} minutes). {{failed}} is not installed again; a later release will be.",
	"notif.update.rolledback.what.damaged":  "Version {{failed}} did not start properly, and Cosmos could not go back to version {{back}} by itself: the copies it kept of the files of {{back}} did not match their checksums. {{failed}} stays installed.",
	"notif.update.rolledback.means":         "The server runs the files of version {{back}} again, the ones that were installed before the update: nothing was downloaded to go back. The files of {{failed}} were removed.",
	"notif.update.rolledback.means.damaged": "The server may not work properly until another release is installed.",
	"notif.update.rolledback.doing":         "Cosmos installs the next release as usual once it is published.",
	"notif.step.update.general":             "Open Configuration > General (server updates)",
}

func init() {
	for kind, k := range rollbackNotificationKinds {
		notificationKinds[kind] = k
	}
	for key, text := range rollbackNotificationTexts {
		notificationTexts[key] = text
	}
}
