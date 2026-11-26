package main

// Fork addition (plan 48): the routes of the Telegram messages' previews
// (telegram/samples.go), registered beside the other Telegram routes.

import (
	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/telegram"
)

// registerTelegramMessageRoutes adds the previews of the kinds of messages
// and "send me this sample" to the admin router; each checks its
// permission itself.
func registerTelegramMessageRoutes(admin *mux.Router) {
	admin.HandleFunc("/api/telegram/kinds", telegram.KindsRoute)
	admin.HandleFunc("/api/telegram/sample", telegram.SampleRoute)
}
