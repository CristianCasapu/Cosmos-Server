package main

// Fork addition: the places where the server itself meets the Telegram
// notifications (package telegram): their start, their two routes, and the
// announcement of a newer release.

import (
	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/telegram"
	"github.com/azukaar/cosmos-server/src/utils"
)

// startTelegram starts the notifier. From then on the security record, the
// notifications for the administrators and utils.ForkNotice reach it; with
// the notifications switched off none of that sends anything.
func startTelegram() {
	telegram.Start(GetCosmosVersion())
}

// registerTelegramRoutes adds the two routes to the admin router: behind
// the token check, the SmartShield and the admin address restriction, like
// their neighbours. Each checks its permission itself.
func registerTelegramRoutes(admin *mux.Router) {
	admin.HandleFunc("/api/telegram", telegram.StatusRoute)
	admin.HandleFunc("/api/telegram/test", telegram.TestRoute)
}

// announceServerUpdate reports that a newer release exists. The check runs
// after every start and every six hours; the notifier tells each version
// once.
func announceServerUpdate(status ServerUpdateStatus) {
	if !status.UpdateAvailable || status.LatestVersion == "" {
		return
	}
	at := ""
	if utils.GetMainConfig().AutoUpdate {
		hour, minute := maintenanceTime()
		at = twoDigits(hour) + ":" + twoDigits(minute)
	}
	utils.ForkNotice(utils.NoticeUpdateAvailable, "version", status.LatestVersion, "current", status.CurrentVersion, "at", at)
}
