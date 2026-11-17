package telegram

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/security"

	"github.com/azukaar/cosmos-server/src/utils"
)

// directClient is what sends the messages that do not go through the
// queue: the test message, and the last message to a chat that is losing
// the notifications. A variable so the tests can point it at a fake.
var directClient = newAPIClient()

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
		if more := watchesSilenced(before.Watch, after.Watch); more != "" {
			if what != "" {
				what += "; "
			}
			what += more
		}
		if what == "" {
			return
		}
	}

	queueChange(pendingChange{was: was, what: what, actor: actor, ip: ip})
}

// pendingChange is one announcement waiting to be sent.
type pendingChange struct {
	was             settings
	what, actor, ip string
}

var (
	changeMu      sync.Mutex
	changePending []pendingChange
	changeRunning bool
)

const maxPendingChanges = 32

// queueChange adds an announcement; one sender at a time sends what
// waits, several saves to the same chat in one message (review R6: a third
// save while two were under way was dropped).
func queueChange(change pendingChange) {
	changeMu.Lock()
	defer changeMu.Unlock()
	if len(changePending) >= maxPendingChanges {
		changePending = changePending[1:]
	}
	changePending = append(changePending, change)
	if !changeRunning {
		changeRunning = true
		go sendChanges()
	}
}

func sendChanges() {
	defer func() { recover() }()
	for {
		changeMu.Lock()
		if len(changePending) == 0 {
			changeRunning = false
			changeMu.Unlock()
			return
		}
		batch := changePending
		changePending = nil
		changeMu.Unlock()

		// per chat (the settings as they were), in order
		order := []string{}
		groups := map[string][]pendingChange{}
		for _, change := range batch {
			if _, seen := groups[change.was.fingerprint]; !seen {
				order = append(order, change.was.fingerprint)
			}
			groups[change.was.fingerprint] = append(groups[change.was.fingerprint], change)
		}
		for _, key := range order {
			sendChange(groups[key])
		}
	}
}

// sendChange tells one chat what one or more saves took away from it.
func sendChange(changes []pendingChange) {
	defer func() { recover() }()
	whats := []string{}
	seen := map[string]bool{}
	for _, change := range changes {
		if !seen[change.what] {
			seen[change.what] = true
			whats = append(whats, change.what)
		}
	}
	last := changes[len(changes)-1]
	nt := newNotice("telegram.moved", map[string]string{"_what": strings.Join(whats, "; ")})
	nt.at = time.Now()
	nt.key = "telegram.moved"
	if last.actor != "" || last.ip != "" {
		nt.extra = movedBy(last.actor, last.ip)
	}
	m := render(last.was, nt)
	for attempt := 0; ; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout+5*time.Second)
		_, failure := directClient.sendMessage(ctx, last.was, outgoing{message: m})
		cancel()
		if failure == nil || !failure.Retryable || attempt >= len(changeRetries) {
			return
		}
		time.Sleep(changeRetries[attempt])
	}
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
	// every critical, error or security kind that was being sent and is not
	// any more: its switch, its group, or the minimum severity (review R6)
	for _, name := range Categories() {
		if was.categories[name] && !is.categories[name] && name != CategorySecurity {
			for _, kind := range KindNames() {
				if kt := texts.Kinds[kind]; kt.Category == name && kt.severity >= sevError && kindOn(was, kind) {
					parts = append(parts, "no longer include the group \""+name+"\"")
					break
				}
			}
		}
	}
	labels := []string{}
	for _, name := range KindNames() {
		kt := texts.Kinds[name]
		if ownKinds[name] || (kt.severity < sevError && kt.Category != CategorySecurity) {
			continue
		}
		if _, set := is.kinds[name]; set && kindOn(was, name) && !kindOn(is, name) {
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

// watchesSilenced says which of the server's own watches a save switches off.
func watchesSilenced(before utils.WatchConfig, after utils.WatchConfig) string {
	names := []string{}
	for _, w := range []struct {
		was, is bool
		name    string
	}{
		{before.SMARTOff, after.SMARTOff, "failing disks"},
		{before.FillOff, after.FillOff, "disks filling fast"},
		{before.DockerOff, after.DockerOff, "Docker not answering"},
		{before.SnapshotsOff, after.SnapshotsOff, "late snapshots"},
		{before.CountryOff, after.CountryOff, "sign-ins from a new country"},
	} {
		if !w.was && w.is {
			names = append(names, w.name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "no longer watch for " + strings.Join(names, ", ")
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
