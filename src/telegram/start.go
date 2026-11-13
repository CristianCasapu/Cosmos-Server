package telegram

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/azukaar/cosmos-server/src/security"
	"github.com/azukaar/cosmos-server/src/utils"
)

var (
	startOnce sync.Once
	std       atomic.Pointer[notifier]
	stateMu   sync.Mutex
)

// currentSettings reads the settings in use. They are read for every
// message, so a save takes effect at once.
func currentSettings() settings {
	return settingsOf(utils.GetMainConfig())
}

// Start starts the notifier and connects it to its sources: the security
// record, the notifications for the administrators and the situations
// reported with utils.ForkNotice. version is the version that is running.
// It is called once, when the server starts; with the notifications
// switched off everything it connects does nothing.
func Start(version string) {
	startOnce.Do(func() {
		// the configuration was just loaded and nothing else runs yet
		RememberSecrets(utils.GetMainConfig())

		n := newNotifier(newAPIClient(), currentSettings)
		n.raise = raiseInPanel
		n.logf = utils.Warn
		n.start()
		std.Store(n)

		utils.SetNotificationForwarder(forwardNotification)
		utils.SetForkNoticeHandler(handleSituation)
		security.Subscribe("telegram", handleEvent)
		utils.OnExit(func() { markExit(exitSignal, "") })

		announceStart(version, time.Now())
	})
}

// raiseInPanel tells the administrators, in the panel, that messages are
// not delivered. reason is a sentence of this package: it holds no token.
func raiseInPanel(reason string) {
	utils.WriteNotification(utils.Notification{
		Recipient: "admin",
		Title:     TitleDeliveryFailed,
		Message:   MessageDeliveryFailed,
		Vars:      reason,
		Level:     "error",
		Link:      "/cosmos-ui/config-general/general",
	})
}

// handleEvent receives every event of the security record.
func handleEvent(event security.Event) {
	n := std.Load()
	if n == nil {
		return
	}
	st := n.settings()
	if !st.usable {
		return
	}
	if nt, tell := noticeFromEvent(event, st); tell {
		n.Notify(nt)
	}
}

// forwardNotification receives every notification Cosmos raises.
func forwardNotification(notification utils.Notification) {
	n := std.Load()
	if n == nil || !n.settings().usable {
		return
	}
	if nt, tell := noticeFromNotification(notification, utils.GetMainConfig()); tell {
		n.Notify(nt)
	}
}

// handleSituation receives what is reported with utils.ForkNotice.
func handleSituation(kind string, values map[string]string) {
	switch kind {
	case utils.NoticeExitRestart:
		markExit(exitRestart, "")
		return
	case utils.NoticeExitUpdate:
		markExit(exitUpdate, values["version"])
		return
	}

	n := std.Load()
	if n == nil {
		return
	}
	st := n.settings()
	if !st.usable {
		return
	}
	if reason, has := values["reason"]; has {
		values["reason"] = hideSecrets(reason, utils.GetMainConfig())
	}
	nt, tell := noticeFromSituation(kind, values)
	if !tell || !st.categories[nt.category] {
		return
	}
	if kind == utils.NoticeUpdateAvailable && !firstAnnouncement(statePath(), values["version"]) {
		return
	}
	n.Notify(nt)
}

// firstAnnouncement reports whether a version was not announced as
// available before, and remembers that it now is. The update check runs
// every few hours and after every start: one message per version.
func firstAnnouncement(path string, version string) bool {
	if version == "" {
		return false
	}
	stateMu.Lock()
	defer stateMu.Unlock()

	state := readState(path)
	if state == nil {
		state = &runState{}
	}
	if state.Announced == version {
		return false
	}
	state.Announced = version
	if err := writeState(path, *state); err != nil {
		utils.Warn("Telegram: the state file could not be written: " + err.Error())
	}
	return true
}

// markExit records how this run ends, right before it does.
func markExit(reason string, updateTo string) {
	markExitAt(statePath(), reason, updateTo)
}

func markExitAt(path string, reason string, updateTo string) {
	stateMu.Lock()
	defer stateMu.Unlock()

	state := readState(path)
	if state == nil {
		return // no start was recorded: nothing to end
	}
	state.Running = false
	state.Exit = reason
	state.UpdateTo = updateTo
	writeState(path, *state)
}

// announceStart tells that Cosmos started, and what that start follows.
func announceStart(version string, now time.Time) {
	nt := recordStart(statePath(), version, now)
	if n := std.Load(); nt != nil && n != nil {
		n.Notify(*nt)
	}
}

// recordStart writes the state of this run and returns what to say about
// its start, if anything.
func recordStart(path string, version string, now time.Time) *notice {
	stateMu.Lock()
	defer stateMu.Unlock()

	nt, next := startNotice(readState(path), version, now)
	if err := writeState(path, next); err != nil {
		utils.Warn("Telegram: the state file could not be written: " + err.Error())
	}
	return nt
}
