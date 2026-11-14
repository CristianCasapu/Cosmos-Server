package telegram

import (
	"context"
	"time"

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
		return
	}

	c := &composer{}
	header(c, was, "Security alert")
	c.fixed("The Telegram notifications of this server " + what + ": this chat no longer receives them as before. If that was not you, look at the server.").line()
	if !was.hideDetails && (actor != "" || ip != "") {
		c.fixed("Changed by ").value(actor, maxValue).fixed(" from ").value(ip, maxValue).line()
	}
	c.fixed(time.Now().Format(stampLayout))
	text, entities := c.result()

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
			failure := directClient.send(ctx, was, text, entities, false)
			cancel()
			if failure == nil || !failure.Retryable || attempt >= len(changeRetries) {
				return
			}
			time.Sleep(changeRetries[attempt])
		}
	}()
}
