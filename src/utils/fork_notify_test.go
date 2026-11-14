package utils

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
)

// Fork addition: tests of fork_notify.go and of its hook in
// WriteNotification.

func readSourceFile(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func captureForwarded(t *testing.T) func() []Notification {
	t.Helper()
	var mu sync.Mutex
	got := []Notification{}
	SetNotificationForwarder(func(n Notification) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, n)
	})
	t.Cleanup(func() { SetNotificationForwarder(nil) })
	return func() []Notification {
		mu.Lock()
		defer mu.Unlock()
		return append([]Notification(nil), got...)
	}
}

func TestNotifyEveryNotificationIsForwardedOnce(t *testing.T) {
	forwarded := captureForwarded(t)

	// one notification for the group, however many administrators it is then written for
	WriteNotification(Notification{Recipient: "admin", Title: "header.notification.title.upnpFailed", Vars: "no answer", Level: "error"})
	got := forwarded()
	if len(got) != 1 || got[0].Recipient != "admin" || got[0].Vars != "no answer" {
		t.Fatalf("forwarded %+v, want the notification once, for the group", got)
	}

	// what MajorError raises goes the same way
	MajorError("Backup: Error creating zip file", errors.New("disk full"))
	got = forwarded()
	if len(got) != 2 || got[1].Title != "header.notification.title.serverError" || !strings.Contains(got[1].Message, "Backup: Error creating zip file : disk full") {
		t.Fatalf("after MajorError: %+v", got)
	}
}

func TestNotifyWritingSurvivesAForwarderThatPanicsOrIsMissing(t *testing.T) {
	SetNotificationForwarder(nil)
	WriteNotification(Notification{Recipient: "admin", Title: "t"}) // must not panic

	SetNotificationForwarder(func(Notification) { panic("forwarder") })
	defer SetNotificationForwarder(nil)
	WriteNotification(Notification{Recipient: "admin", Title: "t"}) // must not panic either
}

func TestNotifyForkNotice(t *testing.T) {
	ForkNotice(NoticeUpdateFailed, "version", "9") // no handler: nothing happens

	type seen struct {
		kind   string
		values map[string]string
	}
	got := []seen{}
	SetForkNoticeHandler(func(kind string, values map[string]string) { got = append(got, seen{kind, values}) })
	defer SetForkNoticeHandler(nil)

	ForkNotice(NoticeUpdateAvailable, "version", "9", "current", "8", "dangling")
	ForkNotice(NoticeExitRestart)
	if len(got) != 2 || got[0].kind != NoticeUpdateAvailable || got[0].values["version"] != "9" || got[0].values["current"] != "8" || len(got[0].values) != 2 || len(got[1].values) != 0 {
		t.Fatalf("handled %+v", got)
	}

	// a call cannot carry more than a few pairs
	pairs := []string{}
	for i := 0; i < 100; i++ {
		pairs = append(pairs, string(rune('a'+i%26))+string(rune('a'+i/26)), "v")
	}
	ForkNotice("many", pairs...)
	if len(got[2].values) > maxNoticePairs {
		t.Errorf("%d pairs were passed on", len(got[2].values))
	}

	SetForkNoticeHandler(func(string, map[string]string) { panic("handler") })
	ForkNotice(NoticeExitRestart) // must not panic
}

// The process is about to end when RestartServer runs: the reason has to be
// handed over before, not in a goroutine.
func TestNotifyRestartServerSaysWhyItEnds(t *testing.T) {
	source := readSourceFile(t, "utils.go")
	restart := source[strings.Index(source, "func RestartServer("):]
	restart = restart[:strings.Index(restart, "\n}\n")]
	notice, exit := strings.Index(restart, "ForkNotice(NoticeExitRestart)"), strings.Index(restart, "os.Exit(")
	if notice < 0 || exit < 0 || notice > exit {
		t.Fatalf("RestartServer does not report the restart before it exits:\n%s", restart)
	}
	if !strings.Contains(readSourceFile(t, "notifications.go"), "\tforwardNotification(notification) // Fork addition") {
		t.Fatal("WriteNotification no longer hands its notifications on")
	}
}
