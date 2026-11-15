package upnp

// Fork addition: what the administrators are told about the router, in
// words: which port, what that means for reaching the server, what the
// router answered, when Cosmos tries again, what to check, and a follow-up
// when the forward is back.

import (
	"strconv"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The panel's place of the internet access settings.
const settingsLink = "/cosmos-ui/config-general/http-server"

// Notice kinds.
const (
	noticeForward = "forward" // a forward could not be made
	noticeRouter  = "router"  // the router could not be reached
	noticeConfig  = "config"  // the settings cannot be applied
)

// upnpNotice is one thing to tell.
type upnpNotice struct {
	kind     string
	mapping  Mapping // the forward (noticeForward)
	key      string  // its key
	err      string  // the router's answer or the settings' error, complete
	answered bool    // the router answered with a refusal (not a broken connection)
	listed   bool    // the router's table was read in this pass
	present  bool    // and it showed the forward to this server
	gateway  string  // the router's address
	name     string  // the router's name
	local    string  // this server's address on the router's network
	external string  // the router's internet address
	retry    retry
	pauses   []time.Duration
	ports    []string // noticeRouter: the ports that are kept forwarded
}

// notifyUPnP tells the administrators. A variable so tests can replace it.
var notifyUPnP = func(n upnpNotice) {
	utils.WriteNotification(n.notification())
}

// threadOf is what a forward's notifications are grouped and resolved by.
func threadOf(key string) string { return "upnp:" + key }

const routerThread = "upnp:router"

func at(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("15:04")
}

// duration says how long, in words.
func duration(d time.Duration) string { return utils.NotificationDuration(d) }

func portName(m Mapping) string {
	return m.Name + " " + strconv.Itoa(m.WantedPort) + "/" + m.Protocol
}

func (n upnpNotice) routerLink() string {
	if n.gateway == "" {
		return ""
	}
	return "http://" + n.gateway
}

func (n upnpNotice) facts() []utils.NotificationFact {
	router := n.gateway
	if n.name != "" && router != "" {
		router = n.name + " (" + n.gateway + ")"
	}
	facts := []utils.NotificationFact{}
	if n.kind == noticeForward {
		m := n.mapping
		facts = append(facts,
			utils.NotificationFact{Key: "notif.fact.port", Value: strconv.Itoa(m.WantedPort)},
			utils.NotificationFact{Key: "notif.fact.internal", Value: strings.TrimPrefix(n.local+":", ":") + strconv.Itoa(m.InternalPort)},
			utils.NotificationFact{Key: "notif.fact.protocol", Value: m.Protocol})
		if m.Container != "" {
			facts = append(facts, utils.NotificationFact{Key: "notif.fact.container", Value: m.Container})
		}
		if m.Holder != "" {
			facts = append(facts, utils.NotificationFact{Key: "notif.fact.holder", Value: m.Holder})
		}
	}
	facts = append(facts,
		utils.NotificationFact{Key: "notif.fact.router", Value: router},
		utils.NotificationFact{Key: "notif.fact.external", Value: n.external},
		utils.NotificationFact{Key: "notif.fact.answer", Value: n.err})
	if n.retry.attempts > 0 {
		facts = append(facts, utils.NotificationFact{Key: "notif.fact.attempts", Value: strconv.Itoa(n.retry.attempts)})
	}
	if !n.retry.next.IsZero() {
		facts = append(facts, utils.NotificationFact{Key: "notif.fact.nextTry", Value: n.retry.next.Local().Format("2006-01-02 15:04")})
	}
	return facts
}

func (n upnpNotice) doing() *utils.NotificationText {
	if n.retry.next.IsZero() {
		return utils.NText("notif.upnp.doing.none")
	}
	if n.answered && len(n.pauses) > 0 && n.pauses[0] == refusedPauses[0] {
		return utils.NText("notif.upnp.doing.retryRefused", "at", at(n.retry.next))
	}
	return utils.NText("notif.upnp.doing.retry", "at", at(n.retry.next), "attempts", strconv.Itoa(n.retry.attempts))
}

func (n upnpNotice) notification() utils.Notification {
	info := &utils.NotificationInfo{Raw: n.err, Facts: n.facts(), Doing: n.doing(),
		Security: &utils.NotificationSecurity{Kinds: []string{"upnp.*"}}}
	check := utils.NotificationStep{NotificationText: *utils.NText("notif.step.upnp.check"), Action: "upnp.check"}
	settings := utils.NotificationStep{NotificationText: *utils.NText("notif.step.upnp.settings"), Link: settingsLink}

	switch n.kind {
	case noticeRouter:
		info.Kind, info.Thread = "upnp.router.unreachable", routerThread
		info.Subject = &utils.NotificationSubject{Type: "router", Name: firstNonEmpty(n.gateway, "router"), Link: settingsLink}
		info.What = utils.NText("notif.upnp.router.unreachable.what")
		info.Means = utils.NText("notif.upnp.router.unreachable.means")
		info.Steps = []utils.NotificationStep{check, {NotificationText: *utils.NText("notif.step.upnp.routerUnknown")},
			{NotificationText: *utils.NText("notif.step.upnp.wait")}, settings}
		if len(n.ports) > 0 {
			info.Facts = append([]utils.NotificationFact{{Key: "notif.fact.port", Value: strings.Join(n.ports, ", ")}}, info.Facts...)
		}
	case noticeConfig:
		info.Kind, info.Thread = "upnp.config.invalid", "upnp:config"
		info.Subject = &utils.NotificationSubject{Type: "setting", Name: "Internet access", Link: settingsLink}
		info.What = utils.NText("notif.upnp.config.invalid.what", "answer", n.err)
		info.Means = utils.NText("notif.upnp.config.invalid.means")
		info.Doing = utils.NText("notif.doing.nothing")
		info.Steps = []utils.NotificationStep{settings}
		info.Facts = nil
	default:
		m := n.mapping
		port := strconv.Itoa(m.WantedPort)
		info.Kind, info.Thread = "upnp.forward.failed", threadOf(n.key)
		info.Title = utils.NText("notif.upnp.forward.failed.title", "port", port)
		info.Subject = &utils.NotificationSubject{Type: "port", Name: portName(m), Link: settingsLink}
		which := "https"
		switch {
		case m.Kind == "app":
			which = "app"
			info.Subject.Name = m.Container + " " + strconv.Itoa(m.WantedPort) + "/" + m.Protocol
		case m.Name == "HTTP":
			which = "http"
		}
		state := "unknown"
		if n.listed && !n.present {
			state = "absent"
		}
		info.What = utils.NText("notif.upnp.forward.failed."+which+"."+state, "port", port, "app", m.Container)
		switch {
		case m.Code == "conflict" && m.Holder != "":
			info.Means = utils.NText("notif.upnp.means.conflict.holder", "port", port, "holder", m.Holder)
		case m.Code == "conflict":
			info.Means = utils.NText("notif.upnp.means.conflict", "port", port)
		case m.Code == "reserved":
			info.Means = utils.NText("notif.upnp.means.reserved", "port", port)
		case n.answered:
			info.Means = utils.NText("notif.upnp.means.refused")
		default:
			info.Means = utils.NText("notif.upnp.means.router")
		}
		router := utils.NotificationStep{NotificationText: *utils.NText("notif.step.upnp.router", "router", firstNonEmpty(n.routerLink(), "the router"),
			"port", port, "local", firstNonEmpty(n.local, "this server"), "internal", strconv.Itoa(m.InternalPort))}
		info.Steps = []utils.NotificationStep{check, settings, router}
		switch {
		case m.Code == "conflict":
			info.Steps = append(info.Steps, utils.NotificationStep{NotificationText: *utils.NText("notif.step.upnp.conflict", "port", port)})
		case n.answered:
			info.Steps = append(info.Steps, utils.NotificationStep{NotificationText: *utils.NText("notif.step.upnp.refused", "port", port)})
		default:
			info.Steps = append(info.Steps, utils.NotificationStep{NotificationText: *utils.NText("notif.step.upnp.wait")})
		}
		if m.Container != "" {
			info.Security.Target = m.Container
		} else {
			info.Security.Target = "Cosmos " + m.Name
		}
	}
	return utils.Notification{Recipient: "admin", Level: "error", Fork: info}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// resolveUPnP marks a thread resolved and, when it was open, tells the
// administrators. A variable so tests can replace it.
var resolveUPnP = func(thread string, followUp func(since time.Time, now time.Time) utils.Notification) {
	if !utils.ThreadOpen(thread) {
		return
	}
	first, open := utils.ResolveThread(thread)
	if !open {
		return
	}
	utils.WriteNotification(followUp(first, clock()))
}

// forwardBack is the follow-up of a forward that works again.
func forwardBack(key string, m Mapping) func(time.Time, time.Time) utils.Notification {
	return func(since time.Time, now time.Time) utils.Notification {
		port := strconv.Itoa(m.ExternalPort)
		which := "https"
		name := portName(m)
		switch {
		case m.Kind == "app":
			which = "app"
			name = m.Container + " " + strconv.Itoa(m.ExternalPort) + "/" + m.Protocol
		case m.Name == "HTTP":
			which = "http"
		}
		down := duration(now.Sub(since))
		return utils.Notification{Recipient: "admin", Level: "success", Fork: &utils.NotificationInfo{
			Kind: "upnp.forward.restored", Thread: threadOf(key), Resolves: threadOf(key),
			Title:   utils.NText("notif.upnp.forward.restored.title", "port", port),
			Subject: &utils.NotificationSubject{Type: "port", Name: name, Link: settingsLink},
			What:    utils.NText("notif.upnp.forward.restored."+which, "port", port, "at", at(now), "app", m.Container),
			Means:   utils.NText("notif.upnp.restored.means", "duration", down),
			Doing:   utils.NText("notif.upnp.restored.doing"),
			Facts: []utils.NotificationFact{{Key: "notif.fact.port", Value: port}, {Key: "notif.fact.protocol", Value: m.Protocol},
				{Key: "notif.fact.downFor", Value: down}},
			Security: &utils.NotificationSecurity{Kinds: []string{"upnp.*"}},
		}}
	}
}

// routerBack is the follow-up of a router that answers again.
func routerBack(gateway string) func(time.Time, time.Time) utils.Notification {
	return func(since time.Time, now time.Time) utils.Notification {
		down := duration(now.Sub(since))
		return utils.Notification{Recipient: "admin", Level: "success", Fork: &utils.NotificationInfo{
			Kind: "upnp.router.restored", Thread: routerThread, Resolves: routerThread,
			Subject:  &utils.NotificationSubject{Type: "router", Name: firstNonEmpty(gateway, "router"), Link: settingsLink},
			What:     utils.NText("notif.upnp.router.restored.what", "at", at(now)),
			Means:    utils.NText("notif.upnp.router.restored.means", "duration", down),
			Facts:    []utils.NotificationFact{{Key: "notif.fact.downFor", Value: down}},
			Security: &utils.NotificationSecurity{Kinds: []string{"upnp.*"}},
		}}
	}
}

// MappingByKey finds a forward in a status by the key its notifications
// carry ("cosmos:HTTPS", "app:<container>:<port>/<protocol>").
func MappingByKey(st Status, key string) (Mapping, bool) {
	for _, m := range append(append([]Mapping{}, st.Mappings...), st.Apps...) {
		if keyOf(m) == key {
			return m, true
		}
	}
	return Mapping{}, false
}
