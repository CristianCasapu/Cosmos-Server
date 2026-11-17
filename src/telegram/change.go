package telegram

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/security"

	"github.com/azukaar/cosmos-server/src/utils"
)

// directClient is what sends the messages that do not go through the
// queue: the test message, and the last message to a chat that is losing
// the notifications. A variable so the tests can point it at a fake.
var directClient = newAPIClient()

// changeSlots bounds how many farewell messages are under way at once.
var changeSlots = make(chan struct{}, 2)

// changeRetries are the waits between the attempts of a farewell message.
var changeRetries = []time.Duration{5 * time.Second, 15 * time.Second}

// AnnounceSettingsChange tells the chat that received the notifications so
// far when a save of the settings takes them away from it: they were
// switched off, they go to another chat or another bot, or the security
// events were switched off. Whoever takes over an administrator's session
// can change these settings; this way the owner still hears of it, in the
// one place the intruder cannot reach.
//
// The message is sent with the settings as they were, outside the queue,
// in the background: the save does not wait for it.
func AnnounceSettingsChange(before utils.Config, after utils.Config, actor string, ip string) {
	was, is := settingsOf(before), settingsOf(after)
	if !was.usable {
		return
	}

	what := ""
	switch {
	case !is.usable:
		what = "were switched off"
	case was.token != is.token:
		what = "now use another bot"
	case was.chatID != is.chatID || was.topicID != is.topicID:
		what = "now go to another chat"
	case was.categories[CategorySecurity] && !is.categories[CategorySecurity]:
		what = "no longer include security events"
	default:
		// plan 48: what the severities and the switches per kind silence
		what = silenced(was, is)
		if what == "" {
			return
		}
	}

	nt := newNotice("telegram.moved", map[string]string{"_what": what})
	nt.at = time.Now()
	nt.key = "telegram.moved"
	if actor != "" || ip != "" {
		nt.extra = movedBy(actor, ip)
	}
	m := render(was, nt)

	select {
	case changeSlots <- struct{}{}:
	default:
		return // two are already under way
	}
	go func() {
		defer func() { <-changeSlots }()
		defer func() { recover() }()
		for attempt := 0; ; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), requestTimeout+5*time.Second)
			_, failure := directClient.sendMessage(ctx, was, outgoing{message: m})
			cancel()
			if failure == nil || !failure.Retryable || attempt >= len(changeRetries) {
				return
			}
			time.Sleep(changeRetries[attempt])
		}
	}()
}

// silenced says, in fixed words, what a change of the delivery per
// severity, of the switches per kind or of the minimum severity stops
// sending that was sent before: errors or critical messages switched off,
// a critical or security kind switched off, the minimum severity raised.
// "" when it stops nothing of that.
func silenced(was settings, is settings) string {
	parts := []string{}
	for _, sev := range []severity{sevCritical, sevError} {
		if was.delivery[sev] != DeliverOff && is.delivery[sev] == DeliverOff {
			parts = append(parts, "no longer send "+strings.ToLower(word(sev.String()))+" messages")
		}
	}
	labels := []string{}
	for _, name := range KindNames() {
		kt := texts.Kinds[name]
		if ownKinds[name] || (kt.severity != sevCritical && kt.Category != CategorySecurity) {
			continue
		}
		if off, set := is.kinds[name]; set && !off && kindOn(was, name) {
			labels = append(labels, kt.Label)
		}
	}
	if len(labels) > 0 {
		more := ""
		if len(labels) > 3 {
			more = " and " + strconv.Itoa(len(labels)-3) + " more"
			labels = labels[:3]
		}
		parts = append(parts, "no longer send: "+strings.Join(labels, ", ")+more)
	}
	if rank := map[string]int{"low": 1, "medium": 2, "high": 3}; rank[is.minSeverity] > rank[was.minSeverity] {
		parts = append(parts, "now send fewer security events (minimum severity raised to "+is.minSeverity+")")
	}
	return strings.Join(parts, "; ")
}

// kindOn reports whether a kind is sent with these settings, as far as
// they decide it (its switch, its category, the minimum severity for an
// event of the security record).
func kindOn(st settings, name string) bool {
	if on, set := st.kinds[name]; set {
		return on
	}
	kt := texts.Kinds[name]
	if !st.categories[kt.Category] || kt.Off {
		return false
	}
	if known, ok := security.KindOf(name); ok {
		severity, _ := security.ParseSeverity(known.Severity)
		minimum, _ := security.ParseSeverity(st.minSeverity)
		return severity >= minimum
	}
	return true
}

// movedBy writes who changed the settings, from where.
func movedBy(actor string, ip string) func(c *composer) {
	return func(c *composer) {
		c.fixed("Changed by ").value(actor, maxValue).fixed(" from ").value(ip, maxValue).fixed(".")
	}
}
