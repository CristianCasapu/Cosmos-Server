package docker

// Fork addition: what the administrators are told about containers, in
// words: which app, what happened to it, what that means, what to look at,
// and the links to its page.

import (
	"context"
	"strconv"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func appLink(name string) string {
	return "/cosmos-ui/servapps/containers/" + name
}

func appStep(name string) utils.NotificationStep {
	return utils.NotificationStep{NotificationText: *utils.NText("notif.step.app.open", "name", name), Link: appLink(name)}
}

// containerUpdatedInfo is the info of upstream's "container updated".
func containerUpdatedInfo(name string, image string) *utils.NotificationInfo {
	// an update that works again ends an update that failed
	utils.ResolveThread("container.update.failed:container:" + name)
	return &utils.NotificationInfo{
		Kind:     "container.updated",
		Subject:  &utils.NotificationSubject{Type: "container", Name: name, Link: appLink(name)},
		Title:    utils.NText("notif.container.updated.title", "name", name),
		What:     utils.NText("notif.container.updated.what", "name", name, "image", image),
		Means:    utils.NText("notif.container.updated.means"),
		Steps:    []utils.NotificationStep{appStep(name)},
		Facts:    []utils.NotificationFact{{Key: "notif.fact.container", Value: name}, {Key: "notif.fact.image", Value: image}},
		Security: &utils.NotificationSecurity{Kinds: []string{"container.*"}, Target: name},
	}
}

// forkContainerUpdateFailed replaces upstream's "Container failed to
// update" (utils.MajorError, which did not say which container): the same
// log line and event, and a notification about that container.
func forkContainerUpdateFailed(name string, image string, err error) {
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	utils.Error("Container failed to update: "+logSafe(name), err)
	utils.TriggerEvent("cosmos.error", "Critical Error", "error", "container@"+name, map[string]interface{}{
		"message": "Container failed to update", "error": errText, "container": name,
	})
	utils.WriteNotification(utils.Notification{Recipient: "admin", Level: "error", Fork: &utils.NotificationInfo{
		Kind:     "container.update.failed",
		Subject:  &utils.NotificationSubject{Type: "container", Name: name, Link: appLink(name)},
		Title:    utils.NText("notif.container.update.failed.title", "name", name),
		What:     utils.NText("notif.container.update.failed.what", "name", name),
		Means:    utils.NText("notif.container.update.failed.means"),
		Doing:    utils.NText("notif.container.update.failed.doing"),
		Steps:    []utils.NotificationStep{appStep(name), {NotificationText: *utils.NText("notif.step.app.logs")}},
		Raw:      errText,
		Facts:    []utils.NotificationFact{{Key: "notif.fact.container", Value: name}, {Key: "notif.fact.image", Value: image}},
		Security: &utils.NotificationSecurity{Kinds: []string{"container.*"}, Target: name},
	}})
}

// notifyDying tells that a container keeps stopping. A variable so tests
// can replace it.
var notifyDying = func(name string, exits int, code string) {
	utils.WriteNotification(utils.Notification{Recipient: "admin", Level: "error", Fork: &utils.NotificationInfo{
		Kind:    "container.dying",
		Subject: &utils.NotificationSubject{Type: "container", Name: name, Link: appLink(name)},
		Title:   utils.NText("notif.container.dying.title", "name", name),
		What:    utils.NText("notif.container.dying.what", "name", name, "exits", strconv.Itoa(exits), "code", code),
		Means:   utils.NText("notif.container.dying.means"),
		Doing:   utils.NText("notif.container.dying.doing"),
		Steps:   []utils.NotificationStep{appStep(name), {NotificationText: *utils.NText("notif.step.app.logs")}},
		Facts: append([]utils.NotificationFact{{Key: "notif.fact.container", Value: name}, {Key: "notif.fact.exits", Value: strconv.Itoa(exits)},
			{Key: "notif.fact.exitCode", Value: code}}, dyingReason(name)...),
		Security: &utils.NotificationSecurity{Kinds: []string{"container.*"}, Target: name},
	}})
}

// leftoverThread is what a leftover's notification is resolved by.
func leftoverThread(name string) string { return "container.leftover:" + name }

// leftoverInfo is the info of the notification about a leftover.
func leftoverInfo(entry Leftover) *utils.NotificationInfo {
	app, name := logSafe(entry.App), logSafe(entry.Name)
	return &utils.NotificationInfo{
		Kind:    "container.leftover",
		Thread:  leftoverThread(name),
		Subject: &utils.NotificationSubject{Type: "container", Name: app, Link: "/cosmos-ui/servapps"},
		Title:   utils.NText("notif.container.leftover.title", "app", app),
		What:    utils.NText("notif.container.leftover.what", "app", app, "name", name),
		Means:   utils.NText("notif.container.leftover.means", "app", app),
		Doing:   utils.NText("notif.doing.nothing"),
		Steps: []utils.NotificationStep{{NotificationText: *utils.NText("notif.step.servapps.leftover"), Link: "/cosmos-ui/servapps"},
			appStep(app)},
		Facts:    []utils.NotificationFact{{Key: "notif.fact.container", Value: app}, {Key: "notif.fact.kept", Value: name}},
		Security: &utils.NotificationSecurity{Kinds: []string{"container.*"}, Target: app},
	}
}

// dyingReason is why a container keeps stopping, when its log says (plan 43):
// the reason in words, never the log itself (it may hold what the image printed).
func dyingReason(name string) []utils.NotificationFact {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report, err := StartReportOf(ctx, name, nil)
	if err != nil || report.Reason == "" {
		return nil
	}
	return []utils.NotificationFact{{Key: "notif.fact.reason", Value: reasonWords(report)}}
}

// writeStartFailedNotice tells the administrators that a container just
// created does not run (plan 43). The reason in words; the log is on the
// app's page, for those who may read it.
func writeStartFailedNotice(r StartReport) {
	reason := reasonWords(r)
	utils.WriteNotification(utils.Notification{Recipient: "admin", Level: "error", Fork: &utils.NotificationInfo{
		Kind:     "container.startFailed",
		Subject:  &utils.NotificationSubject{Type: "container", Name: r.Name, Link: appLink(r.Name)},
		Title:    utils.NText("notif.container.startFailed.title", "name", r.Name),
		What:     utils.NText("notif.container.startFailed.what", "name", r.Name, "reason", reason),
		Means:    utils.NText("notif.container.startFailed.means"),
		Doing:    utils.NText("notif.container.startFailed.doing"),
		Steps:    []utils.NotificationStep{appStep(r.Name)},
		Facts:    []utils.NotificationFact{{Key: "notif.fact.container", Value: r.Name}, {Key: "notif.fact.image", Value: r.Image}, {Key: "notif.fact.reason", Value: reason}},
		Security: &utils.NotificationSecurity{Kinds: []string{"container.*"}, Target: r.Name},
	}})
}
