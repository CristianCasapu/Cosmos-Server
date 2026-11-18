package utils

import (
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
	if isOpen, closing, err := ForkThreadState("upnp:cosmos:HTTPS", []string{"upnp.forward.restored"}, now.Add(-time.Hour)); err != nil || !isOpen || closing != nil {
		t.Fatalf("open: %v %v %v", isOpen, closing, err)
	}
	// ended quietly: closed, no follow-up
	ResolveThread("upnp:cosmos:HTTPS")
	if isOpen, closing, err := ForkThreadState("upnp:cosmos:HTTPS", []string{"upnp.forward.restored"}, now.Add(-time.Hour)); err != nil || isOpen || closing != nil {
		t.Fatalf("quiet end: %v %v %v", isOpen, closing, err)
	}
	// ended with a follow-up
	*now = now.Add(time.Minute)
	WriteNotification(Notification{Recipient: "admin", Level: "success", Fork: &NotificationInfo{
		Kind: "upnp.forward.restored", Thread: "upnp:cosmos:HTTPS", Resolves: "upnp:cosmos:HTTPS",
		What: NText("notif.upnp.forward.restored.https", "port", "2053", "at", "09:14"),
	}})
	isOpen, closing, err := ForkThreadState("upnp:cosmos:HTTPS", []string{"upnp.forward.restored"}, now.Add(-time.Hour))
	if err != nil || isOpen || closing == nil || closing.Fork.Kind != "upnp.forward.restored" || closing.Fork.Resolves != "upnp:cosmos:HTTPS" {
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
