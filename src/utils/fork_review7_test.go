package utils

import (
	"testing"
	"time"
)

// review 7, N5: a SnapRAID job's failure is a job failure (error.job), so
// that its "works again" (forkJobWorks) pairs with it; MajorError's own
// SnapRAID messages keep their kind.
func TestASnapRAIDJobFailureIsAJobFailure(t *testing.T) {
	for _, info := range []*NotificationInfo{
		jobFailureInfo("SnapRAID", "SnapRAID sync pool", "exit 1"),
		jobScheduleInfo("SnapRAID", "SnapRAID sync pool", "bad crontab"),
	} {
		if info.Kind != "error.job" {
			t.Errorf("%s: kind %s", info.Thread, info.Kind)
		}
		if info.Subject == nil || info.Subject.Name != "SnapRAID sync pool" {
			t.Errorf("%s: subject %+v", info.Thread, info.Subject)
		}
	}
	if kind := majorErrorKind("Failed to status pool"); kind != "error.snapraid" {
		t.Errorf("SnapRAID's own message: %s", kind)
	}
}

// review 7, N3: a notification resolved by hand resolves its problem for
// every recipient, and the store then says it is over (the chat ends it
// as "No longer followed" when no source knows it); a resolved or plain
// notification changes nothing.
func TestAProblemResolvedByHandIsOver(t *testing.T) {
	now := notificationEnv(t)
	WriteNotification(upnpNotification("2053"))
	items := list(t, NotificationQuery{Recipient: "alice", Admin: true})
	if len(items) == 0 {
		t.Fatal("no row")
	}
	since := now.Add(-time.Minute)
	*now = now.Add(time.Minute)
	resolved, err := ResolveNotificationByHand("alice", true, items[0].ID)
	if err != nil || !resolved {
		t.Fatalf("resolved %v err %v", resolved, err)
	}
	open, over, _, err := ForkThreadState("upnp:cosmos:HTTPS", nil, since)
	if err != nil || open || !over {
		t.Errorf("after: open %v over %v err %v", open, over, err)
	}
	for _, who := range []string{"alice", "bob"} {
		for _, it := range list(t, NotificationQuery{Recipient: who, Admin: true}) {
			if it.Resolved == nil {
				t.Errorf("%s: row %d still open", who, it.ID)
			}
		}
	}
	if again, err := ResolveNotificationByHand("alice", true, items[0].ID); err != nil || again {
		t.Errorf("again: %v %v", again, err)
	}
	if _, err := ResolveNotificationByHand("alice", true, 0); err == nil {
		t.Error("id 0 taken")
	}
}
