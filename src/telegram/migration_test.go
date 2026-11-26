package telegram

import (
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// What each kind of notification was on custom before plan 48: its Telegram
// category and how it was queued, with the level its emitter writes. A
// changed category would stop messages for an owner who chose categories;
// a raised priority is allowed only where it is declared (FORK.md).
var beforePlan48 = []struct {
	kind, level, category string
	priority              priority
}{
	{"upnp.forward.failed", "error", CategorySystem, priorityNormal},
	{"upnp.router.unreachable", "error", CategorySystem, priorityNormal},
	{"upnp.config.invalid", "error", CategorySystem, priorityNormal},
	{"cert.order.failed", "error", CategorySystem, priorityNormal},
	{"cert.expiring", "warning", CategorySystem, priorityNormal},
	{"cert.untrusted", "warning", CategorySystem, priorityNormal},
	{"cert.renewed", "info", CategorySystem, priorityLow},
	{"container.updated", "info", CategoryApps, priorityLow},
	{"container.update.failed", "error", CategoryApps, priorityNormal},
	{"container.dying", "error", CategoryApps, priorityNormal},
	{"container.startFailed", "error", CategoryApps, priorityNormal},
	{"container.leftover", "warning", CategoryApps, priorityNormal},
	{"app.installed", "info", CategoryApps, priorityLow},
	{"app.installMail.failed", "warning", CategoryApps, priorityNormal},
	{"route.protected", "info", CategoryApps, priorityLow},
	{"snapshots.failed", "error", CategorySystem, priorityNormal},
	{"alert.disk", "warning", CategoryStorage, priorityNormal},
	{"alert.triggered", "warning", CategorySystem, priorityNormal},
	{"fail2ban.down", "error", CategorySystem, priorityNormal},
	{"update.rejected", "error", CategorySystem, priorityNormal},
	{"update.rolledback", "error", CategorySystem, priorityNormal},
	{"update.attention", "error", CategorySystem, priorityNormal},
	{"deps.updated", "info", CategorySystem, priorityLow},
	{"deps.failed", "error", CategorySystem, priorityNormal},
	{"deps.paused", "info", CategorySystem, priorityLow},
	{"deps.stale", "warning", CategorySystem, priorityNormal},
	{"reach.down", "error", CategorySystem, priorityNormal},
	{"reach.down", "warning", CategorySystem, priorityNormal},
	{"edgeblock.problem", "error", CategorySystem, priorityNormal},
	{"originpull.expiring", "warning", CategorySystem, priorityNormal},
	{"turnstile.problem", "error", CategorySystem, priorityNormal},
	{"error.backup", "error", CategorySystem, priorityNormal},
	{"error.job", "error", CategorySystem, priorityNormal},
	{"error.snapraid", "error", CategoryStorage, priorityNormal},
	{"error.internet", "error", CategorySystem, priorityNormal},
	{"error.database", "error", CategorySystem, priorityNormal},
	{"error.export", "error", CategoryApps, priorityNormal},
	{"error.other", "error", CategorySystem, priorityNormal},
}

// The kinds that became critical, declared in FORK.md and the changelog.
var raisedInPlan48 = map[string]bool{"update.attention": true, "reach.down": true}

func TestEveryKindKeepsItsCategoryAndOnlyDeclaredOnesRing(t *testing.T) {
	for _, before := range beforePlan48 {
		n := utils.Notification{Recipient: "admin", Level: before.level, Fork: &utils.NotificationInfo{
			Kind: before.kind, What: utils.NText("notif.error.other.what", "message", "x"),
		}}
		nt := noticeFromInfo(n, testConfig())
		if nt.category != before.category {
			t.Errorf("%s: category %s, was %s", before.kind, nt.category, before.category)
		}
		if got := nt.priority(); got != before.priority && !(got == priorityCritical && raisedInPlan48[before.kind]) {
			t.Errorf("%s (%s): priority %d, was %d", before.kind, before.level, got, before.priority)
		}
	}
	// the start messages: only the restart loop became critical
	if nt, _ := startNotice(&runState{Version: "v", Running: true, Starts: []time.Time{time.Now().Add(-48 * time.Hour)}}, "v", time.Now()); nt.category != CategorySystem || nt.priority() != priorityNormal {
		t.Errorf("crash: %+v", nt)
	}
}

// A fork.34 owner with only "system" on, or everything but "system" on,
// receives what they received.
func TestCustomisedCategoriesStillDecide(t *testing.T) {
	for _, systemOnly := range []bool{true, false} {
		config := testConfig()
		config.Telegram.Categories = map[string]bool{"security": !systemOnly, "logins": !systemOnly, "system": systemOnly, "storage": !systemOnly, "apps": !systemOnly, "other": !systemOnly}
		st := settingsOf(config)
		for _, before := range beforePlan48 {
			n := utils.Notification{Recipient: "admin", Level: before.level, Fork: &utils.NotificationInfo{Kind: before.kind, What: utils.NText("notif.error.other.what", "message", "x")}}
			nt := noticeFromInfo(n, config)
			want := (before.category == CategorySystem) == systemOnly
			if st.wants(nt) != want {
				t.Errorf("system only %v: %s sent %v, was %v", systemOnly, before.kind, st.wants(nt), want)
			}
		}
	}
}
