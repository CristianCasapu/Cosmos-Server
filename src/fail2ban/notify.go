package fail2ban

// The administrators are told when Fail2ban stops answering while it is
// set up for Cosmos (two looks in a row, a minute apart: a reload is not
// a failure), and again when it answers again.

import (
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const downThread = "fail2ban.down"

var (
	downMu    sync.Mutex
	downLooks int
	downSince time.Time
	downTold  bool
)

// notifyDown and notifyBack are what tells. Variables for the tests.
var (
	notifyDown = func(since time.Time) {
		utils.WriteNotification(utils.Notification{Recipient: "admin", Level: "error", Fork: &utils.NotificationInfo{
			Kind: "fail2ban.down", Thread: downThread,
			Subject: &utils.NotificationSubject{Type: "service", Name: "Fail2ban", Link: "/cosmos-ui/security"},
			What:    utils.NText("notif.fail2ban.down.what"),
			Means:   utils.NText("notif.fail2ban.down.means"),
			Doing:   utils.NText("notif.fail2ban.down.doing"),
			Steps: []utils.NotificationStep{{NotificationText: *utils.NText("notif.step.security.open"), Link: "/cosmos-ui/security"},
				{NotificationText: *utils.NText("notif.step.fail2ban.host")}},
			Security: &utils.NotificationSecurity{Kinds: []string{"firewall.*"}},
		}})
	}
	notifyBack = func(since time.Time, now time.Time) {
		if _, open := utils.ResolveThread(downThread); !open {
			return
		}
		down := utils.NotificationDuration(now.Sub(since))
		utils.WriteNotification(utils.Notification{Recipient: "admin", Level: "success", Fork: &utils.NotificationInfo{
			Kind: "fail2ban.restored", Thread: downThread, Resolves: downThread,
			Subject: &utils.NotificationSubject{Type: "service", Name: "Fail2ban", Link: "/cosmos-ui/security"},
			What:    utils.NText("notif.fail2ban.restored.what", "at", now.Local().Format("15:04")),
			Means:   utils.NText("notif.fail2ban.restored.means", "duration", down),
		}})
	}
)

// noteAnswer is told after each look whether Fail2ban answered.
func noteAnswer(answered bool, setUp bool, now time.Time) {
	downMu.Lock()
	defer downMu.Unlock()
	if answered || !setUp {
		if downTold && answered {
			notifyBack(downSince, now)
		}
		downLooks, downTold = 0, false
		return
	}
	if downLooks == 0 {
		downSince = now
	}
	downLooks++
	if downLooks == 2 && !downTold {
		downTold = true
		notifyDown(downSince)
	}
}
