package utils

import (
	"strings"
	"testing"
	"time"
)

// Review 4 (d): the notifications say whether a thread is open, the
// newest opening of each open thread, and the follow-up that ended one; a
// source with its state in memory says it itself.
func TestTheLevelOfAThread(t *testing.T) {
	now := notificationEnv(t)
	WriteNotification(upnpNotification("2053"))
	*now = now.Add(time.Minute)
	WriteNotification(upnpNotification("2053")) // grouped: one thread
	open, err := ForkOpenThreads([]string{"upnp.forward.failed"}, time.Hour, 10)
	if err != nil || len(open) != 1 || open[0].Fork.Thread != "upnp:cosmos:HTTPS" || open[0].Fork.Repeat {
		t.Fatalf("open threads: %v %+v", err, open)
	}
	if isOpen, over, closing, err := ForkThreadState("upnp:cosmos:HTTPS", []string{"upnp.forward.restored"}, now.Add(-time.Hour)); err != nil || !isOpen || over || closing != nil {
		t.Fatalf("open: %v %v %v %v", isOpen, over, closing, err)
	}
	// ended quietly: over (a resolved row), no follow-up
	ResolveThread("upnp:cosmos:HTTPS")
	if isOpen, over, closing, err := ForkThreadState("upnp:cosmos:HTTPS", []string{"upnp.forward.restored"}, now.Add(-time.Hour)); err != nil || isOpen || !over || closing != nil {
		t.Fatalf("quiet end: %v %v %v %v", isOpen, over, closing, err)
	}
	// ended with a follow-up
	*now = now.Add(time.Minute)
	WriteNotification(Notification{Recipient: "admin", Level: "success", Fork: &NotificationInfo{
		Kind: "upnp.forward.restored", Thread: "upnp:cosmos:HTTPS", Resolves: "upnp:cosmos:HTTPS",
		What: NText("notif.upnp.forward.restored.https", "port", "2053", "at", "09:14"),
	}})
	isOpen, over, closing, err := ForkThreadState("upnp:cosmos:HTTPS", []string{"upnp.forward.restored"}, now.Add(-time.Hour))
	if err != nil || isOpen || !over || closing == nil || closing.Fork.Kind != "upnp.forward.restored" || closing.Fork.Resolves != "upnp:cosmos:HTTPS" {
		t.Fatalf("follow-up: %v %+v %v", isOpen, closing, err)
	}
	if open, _ := ForkOpenThreads([]string{"upnp.forward.failed"}, time.Hour, 10); len(open) != 0 {
		t.Errorf("still open: %+v", open)
	}
	// a source that keeps its state in memory
	RegisterThreadLevel("test.kind", func(thread string) (bool, bool) { return thread == "a", true })
	if open, ok := SourceThreadLevel("test.kind", "a"); !open || !ok {
		t.Error("the source's level")
	}
	RegisterThreadLevel("test.panics", func(string) (bool, bool) { panic("no") })
	if _, ok := SourceThreadLevel("test.panics", "a"); ok {
		t.Error("a source that panics is believed")
	}
}

// review 5, L1: a row the owner deleted, or the retention pruned, is no
// evidence: the level is unknown, never "over".
func TestADeletedNotificationIsNotAnEnd(t *testing.T) {
	now := notificationEnv(t)
	WriteNotification(upnpNotification("2053"))
	for _, who := range []string{"alice", "bob"} {
		items := list(t, NotificationQuery{Recipient: who, Admin: true})
		ids := []int64{}
		for _, it := range items {
			ids = append(ids, it.ID)
		}
		if _, err := DeleteNotifications(who, true, ids); err != nil {
			t.Fatal(err)
		}
	}
	isOpen, over, closing, err := ForkThreadState("upnp:cosmos:HTTPS", []string{"upnp.forward.restored"}, now.Add(-time.Hour))
	if err != nil || isOpen || over || closing != nil {
		t.Fatalf("after the delete: open %v over %v closing %v err %v", isOpen, over, closing, err)
	}
	// an old resolution is no evidence for a later opening either
	*now = now.Add(time.Hour)
	WriteNotification(upnpNotification("2053"))
	ResolveThread("upnp:cosmos:HTTPS")
	*now = now.Add(time.Hour)
	if _, over, _, _ := ForkThreadState("upnp:cosmos:HTTPS", nil, now.Add(-time.Minute)); over {
		t.Error("a resolution before `since` read as the end")
	}
}

// review 6: a thread is stored under its cleaned name (trimmed, cut at 200
// characters); a source that gives the name as it has it still finds it.
func TestALongThreadNameIsFoundAsItIsGiven(t *testing.T) {
	now := notificationEnv(t)
	long := " fill:/mnt/" + strings.Repeat("данные", 40)
	WriteNotification(Notification{Recipient: "admin", Level: "warning", Fork: &NotificationInfo{
		Kind: "disk.filling", Thread: long, What: NText("notif.update.attention.what", "summary", "x")}})
	if !ThreadOpen(long) {
		t.Fatal("not open under the name given")
	}
	if open, _, _, err := ForkThreadState(long, nil, now.Add(-time.Hour)); err != nil || !open {
		t.Fatalf("state: open %v err %v", open, err)
	}
	*now = now.Add(time.Minute)
	if _, ok := ResolveThread(long); !ok {
		t.Fatal("not resolved under the name given")
	}
	if open, over, _, err := ForkThreadState(long, nil, now.Add(-time.Hour)); err != nil || open || !over {
		t.Errorf("after: open %v over %v err %v", open, over, err)
	}
}
