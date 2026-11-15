package reachwatch

// What the administrators are told: once when the server cannot be reached
// any more (with the cause, since when and what Cosmos does, so that the
// Telegram message is enough on its own: the panel is what cannot be
// reached), again when the cause changes, and a follow-up with how long it
// lasted when it is back.

import (
	"strconv"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The panel's places.
const (
	settingsLink   = "/cosmos-ui/config-general/http-server"
	cloudflareLink = "/cosmos-ui/config-general/https"
)

// Replaced by the tests.
var (
	writeNotification = utils.WriteNotification
	resolveThread     = utils.ResolveThread
)

// when says a time the way the sentences do: "09:12", or with the date when
// it is not today.
func when(t time.Time, now time.Time) string {
	t, now = t.Local(), now.Local()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("2006-01-02 15:04")
}

func namesText(names []string) string {
	switch {
	case len(names) == 0:
		return ""
	case len(names) == 1:
		return names[0]
	case len(names) <= 3:
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:3], ", ") + " and " + strconv.Itoa(len(names)-3) + " more"
}

// causeText is the cause in a few words, in English (a fact's value).
func causeText(cause string) string {
	return utils.NotificationEnglish("notif.reach.cause." + cause)
}

// DownNotification is the notification of an outage (exported for the tests
// of what Telegram tells).
func DownNotification(o Outage, primary *NameState, p Plan, next time.Time, now time.Time) utils.Notification {
	port := strconv.Itoa(p.Port)
	name := primary.Host
	whatKey := "notif.reach.down." + o.Cause + ".what"
	if o.Asked && inList(o.Cause, utils.ReachWatchPathCauses) {
		whatKey = "notif.reach.down." + o.Cause + ".asked.what"
	}
	code := o.Code
	if code == "" {
		code = "?"
	}
	info := &utils.NotificationInfo{
		Kind:    "reach.down",
		Thread:  o.Thread,
		Title:   utils.NText("notif.reach.down.title"),
		Subject: &utils.NotificationSubject{Type: "domain", Name: namesText(o.Names), Link: settingsLink},
		What:    utils.NText(whatKey, "since", when(o.Since, now), "port", port, "name", name, "code", code),
		Means:   utils.NText("notif.reach.down." + o.Cause + ".means"),
		Doing:   utils.NText("notif.reach.doing.next", "at", when(next, now)),
		Steps: []utils.NotificationStep{
			{NotificationText: *utils.NText("notif.step.reach.check"), Action: "reach.check"},
			{NotificationText: *utils.NText("notif.step.reach."+o.Cause, "port", port, "name", name)},
			{NotificationText: *utils.NText("notif.step.reach.router"), Link: settingsLink},
			{NotificationText: *utils.NText("notif.step.reach.cloudflareCard"), Link: cloudflareLink},
		},
		Raw: primary.Raw,
		Facts: []utils.NotificationFact{
			{Key: "notif.fact.reach.names", Value: strings.Join(o.Names, ", ")},
			{Key: "notif.fact.reach.cause", Value: causeText(o.Cause)},
			{Key: "notif.fact.reach.answer", Value: code},
			{Key: "notif.fact.port", Value: port},
			{Key: "notif.fact.reach.since", Value: o.Since.Local().Format("2006-01-02 15:04")},
			{Key: "notif.fact.reach.tries", Value: strconv.Itoa(primary.Fails)},
		},
		Security: &utils.NotificationSecurity{Kinds: []string{"access.origin.denied"}},
	}
	if o.Router != "" {
		info.Facts = append(info.Facts, utils.NotificationFact{Key: "notif.fact.reach.router", Value: o.Router})
	}
	return utils.Notification{Recipient: "admin", Level: Level(o.Cause), Fork: info}
}

// RestoredNotification is the follow-up when the server is back.
func RestoredNotification(o Outage, p Plan, now time.Time) utils.Notification {
	end := now
	if o.End != nil {
		end = *o.End
	}
	down := utils.NotificationDuration(end.Sub(o.Since))
	minutes := strconv.Itoa(int(p.Interval / time.Minute))
	return utils.Notification{Recipient: "admin", Level: "success", Fork: &utils.NotificationInfo{
		Kind: "reach.restored", Thread: o.Thread, Resolves: o.Thread,
		Title:   utils.NText("notif.reach.restored.title"),
		Subject: &utils.NotificationSubject{Type: "domain", Name: namesText(o.Names), Link: settingsLink},
		What:    utils.NText("notif.reach.restored.what", "at", when(end, now), "duration", down, "since", when(o.Since, now)),
		Means:   utils.NText("notif.reach.restored.means"),
		Doing:   utils.NText("notif.reach.restored.doing", "minutes", minutes),
		Facts: []utils.NotificationFact{
			{Key: "notif.fact.downFor", Value: down},
			{Key: "notif.fact.reach.cause", Value: causeText(o.Cause)},
			{Key: "notif.fact.reach.names", Value: strings.Join(o.Names, ", ")},
		},
	}}
}

var notifyDown = func(o Outage, primary *NameState, p Plan, next time.Time) {
	go writeNotification(DownNotification(o, primary, p, next, clock()))
}

var notifyRestored = func(o Outage, p Plan) {
	n := RestoredNotification(o, p, clock())
	go func() {
		if _, open := resolveThread(o.Thread); open {
			writeNotification(n)
		}
	}()
}

// closeQuietly marks a thread resolved without a follow-up: the cause
// changed, or the watch stopped.
var closeQuietly = func(thread string) {
	if thread != "" {
		go resolveThread(thread)
	}
}
