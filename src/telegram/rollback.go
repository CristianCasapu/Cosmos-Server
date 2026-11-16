package telegram

import "sync/atomic"

// quietStart: this start is told by another message, so the start message
// is not sent. Set when the launcher went back to the version before a
// failed update: the notification about that (kind update.rolledback) says
// what happened, and the start message would claim an update ("from version
// Y to X") that did not take place.
var quietStart atomic.Bool

// QuietStart is called before Start when another message tells this start.
func QuietStart() { quietStart.Store(true) }
