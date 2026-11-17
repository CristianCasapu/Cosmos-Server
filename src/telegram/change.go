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

// changeRetries are the waits between the attempts of a farewell message;
// the last one repeats until changeRetryFor has passed.
var changeRetries = []time.Duration{5 * time.Second, 15 * time.Second, time.Minute, 5 * time.Minute}

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

	queueChange(was, what, actor, ip)
}

// The announcements waiting, one entry per chat (the settings as they
// were): what was switched off, and every account and address that did
// it, each bounded (review T4). An entry is never pushed out by another
// chat's: a chat beyond maxChangeChats is logged and recorded instead.
type chatChange struct {
	was       settings
	whats     []string
	moreWhats int
	actors    []string // "account from address"
	moreBy    int
}

const (
	maxChangeChats  = 16
	maxChangeWhats  = 12
	maxChangeActors = 6
)

var (
	changeMu      sync.Mutex
	changeChats   = map[string]*chatChange{}
	changeOrder   []string
	changeRunning bool
	// changeRetryFor is how long a warning is tried; changeRetries the
	// waits between attempts (the last one repeats). Variables for the tests.
	changeRetryFor = 30 * time.Minute
)

func queueChange(was settings, what string, actor string, ip string) {
	by := strings.TrimSpace(actor)
	if ip != "" {
		by = strings.TrimSpace(by + " from " + ip)
	}
	changeMu.Lock()
	defer changeMu.Unlock()
	entry := changeChats[was.fingerprint]
	if entry == nil {
		if len(changeChats) >= maxChangeChats {
			lost := lostWarning
			go lost(what, by)
			return
		}
		entry = &chatChange{was: was}
		changeChats[was.fingerprint] = entry
		changeOrder = append(changeOrder, was.fingerprint)
	}
	if !contains(entry.whats, what) {
		if len(entry.whats) < maxChangeWhats {
			entry.whats = append(entry.whats, what)
		} else {
			entry.moreWhats++
		}
	}
	if by != "" && !contains(entry.actors, by) {
		if len(entry.actors) < maxChangeActors {
			entry.actors = append(entry.actors, by)
		} else {
			entry.moreBy++
		}
	}
	if !changeRunning {
		changeRunning = true
		go sendChanges()
	}
}

func contains(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

func sendChanges() {
	defer func() { recover() }()
	for {
		changeMu.Lock()
		if len(changeOrder) == 0 {
			changeRunning = false
			changeMu.Unlock()
			return
		}
		key := changeOrder[0]
		changeOrder = changeOrder[1:]
		entry := changeChats[key]
		delete(changeChats, key) // what comes meanwhile for this chat is told after
		changeMu.Unlock()
		if entry != nil {
			sendChange(entry)
		}
	}
}

// sendChange tells one chat what saves took away from it, with who did,
// trying for changeRetryFor and honouring Telegram's waits; when it cannot,
// the server's log, the security record and the panel say so.
func sendChange(entry *chatChange) {
	defer func() { recover() }()
	what := strings.Join(entry.whats, "; ")
	if entry.moreWhats > 0 {
		what += " and " + strconv.Itoa(entry.moreWhats) + " more changes"
	}
	by := strings.Join(entry.actors, ", ")
	if entry.moreBy > 0 {
		by += " and " + strconv.Itoa(entry.moreBy) + " more"
	}
	nt := newNotice("telegram.moved", map[string]string{"_what": what})
	nt.at = time.Now()
	nt.key = "telegram.moved"
	if by != "" {
		nt.extra = func(c *composer) { c.fixed("Changed by ").value(by, maxFreeText).fixed(".") }
	}
	m := render(entry.was, nt)
	changeMu.Lock()
	retries, retryFor, lost := changeRetries, changeRetryFor, lostWarning
	changeMu.Unlock()
	deadline := time.Now().Add(retryFor)
	for attempt := 0; ; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout+5*time.Second)
		_, failure := directClient.sendMessage(ctx, entry.was, outgoing{message: m})
		cancel()
		if failure == nil {
			return
		}
		wait := failure.RetryAfter
		if wait <= 0 {
			step := attempt
			if step >= len(retries) {
				step = len(retries) - 1
			}
			wait = retries[step]
		}
		if !failure.Retryable || time.Now().Add(wait).After(deadline) {
			lost(what, by)
			return
		}
		time.Sleep(wait)
	}
}

// lostWarning is what happens when a chat could not be told: a line in the
// log, an entry of the security record, a notification in the panel. A
// variable for the tests.
var lostWarning = func(what string, by string) {
	defer func() { recover() }()
	utils.Warn("Telegram: the chat could not be told that its notifications " + what + " (changed by " + by + ")")
	utils.RecordSecurityEvent(utils.SecurityEvent{Kind: utils.SecTelegramWarningLost, Details: map[string]string{"what": cleanValue(what, 300), "by": cleanValue(by, 200)}})
	raiseInPanel("The Telegram chat could not be told that its notifications " + what + ".")
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
