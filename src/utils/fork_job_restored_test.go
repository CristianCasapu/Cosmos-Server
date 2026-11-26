package utils

import (
	"errors"
	"testing"
	"time"
)

// plan 48: a job that failed and then finishes again closes its failure
// with a follow-up; a job that never failed writes nothing.
func TestAJobThatWorksAgainClosesItsFailure(t *testing.T) {
	now := notificationEnv(t)
	forwarded := []Notification{}
	SetNotificationForwarder(func(n Notification) { forwarded = append(forwarded, n) })

	ForkJobSucceeded("Custom", "nightly")
	if items := list(t, NotificationQuery{Recipient: "bob", Admin: true}); len(items) != 0 {
		t.Fatalf("a job that never failed wrote %d notifications", len(items))
	}

	MajorError("CRON job nightly failed", errors.New("exit status 1"))
	*now = now.Add(25 * time.Minute)
	ForkJobSucceeded("Custom", "nightly")
	items := list(t, NotificationQuery{Recipient: "bob", Admin: true})
	if len(items) != 2 || items[0].Kind != "job.restored" || items[1].Resolved == nil {
		t.Fatalf("%+v", items)
	}
	last := forwarded[len(forwarded)-1]
	if last.Fork == nil || last.Fork.Resolves != "error.job:nightly" || last.Fork.Thread != "error.job:nightly" {
		t.Fatalf("the follow-up does not close the failure's thread: %+v", last.Fork)
	}
	// once closed, another success writes nothing
	ForkJobSucceeded("Custom", "nightly")
	if items := list(t, NotificationQuery{Recipient: "bob", Admin: true}); len(items) != 2 {
		t.Fatalf("got %d", len(items))
	}
}
