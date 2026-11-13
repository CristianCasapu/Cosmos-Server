package utils

// Fork addition: how the rest of the server reaches the Telegram notifier
// (package src/telegram), which imports utils and so cannot be imported
// here. Two function variables, filled in by telegram.Start; until then,
// and in the tests of other packages, both do nothing.
//
//   - WriteNotification hands every notification to the forwarder before it
//     is written for each recipient: what Cosmos raises for its
//     administrators is sent on once.
//   - ForkNotice reports a situation that raises no notification:
//
//	utils.ForkNotice(utils.NoticeUpdateAvailable, "version", latest, "current", running)
//
// Neither ever waits for the network, fails or panics. Never pass a secret.

import (
	"sync/atomic"
)

var (
	notificationForwarder atomic.Value // of func(Notification)
	forkNoticeHandler     atomic.Value // of func(string, map[string]string)
)

// The situations reported with ForkNotice, and the details each carries.
const (
	NoticeUpdateAvailable = "update.available" // version, current, at ("HH:MM"; empty when updates are not automatic)
	NoticeUpdateFailed    = "update.failed"    // version, reason
	NoticeContainerDying  = "container.dying"  // name, exits, code
	// The two below are not told to anyone: they say why the process is
	// about to end, so that the next start can tell. They are handled
	// before ForkNotice returns.
	NoticeExitRestart = "exit.restart"
	NoticeExitUpdate  = "exit.update" // version
)

// maxNoticePairs bounds what one ForkNotice call can carry.
const maxNoticePairs = 8

// SetNotificationForwarder sets what receives a copy of every notification.
// nil removes it.
func SetNotificationForwarder(forward func(Notification)) {
	if forward == nil {
		forward = func(Notification) {}
	}
	notificationForwarder.Store(forward)
}

// SetForkNoticeHandler sets what receives the situations reported with
// ForkNotice. nil removes it.
func SetForkNoticeHandler(handle func(kind string, values map[string]string)) {
	if handle == nil {
		handle = func(string, map[string]string) {}
	}
	forkNoticeHandler.Store(handle)
}

// forwardNotification gives a notification to the forwarder, if there is one.
func forwardNotification(notification Notification) {
	defer func() { recover() }() // a notification is written whatever happens here
	if forward, ok := notificationForwarder.Load().(func(Notification)); ok && forward != nil {
		forward(notification)
	}
}

// ForkNotice reports a situation by its kind, with details as key, value
// pairs. The kinds are the Notice constants above.
func ForkNotice(kind string, pairs ...string) {
	defer func() { recover() }()
	handle, ok := forkNoticeHandler.Load().(func(string, map[string]string))
	if !ok || handle == nil {
		return
	}
	values := map[string]string{}
	for i := 0; i+1 < len(pairs) && i/2 < maxNoticePairs; i += 2 {
		values[pairs[i]] = pairs[i+1]
	}
	handle(kind, values)
}
