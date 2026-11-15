package edgeblock

// What the security record and the notifications get.

import (
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The notification of a lasting trouble (bell for administrators, and
// Telegram through the existing forwarder).
const (
	TitleProblem      = "header.notification.title.edgeBlocksProblem"
	MessagePermission = "header.notification.message.edgeBlocksPermission"
	MessageToken      = "header.notification.message.edgeBlocksToken"
)

// replaced by the tests
var (
	recordEvent  = utils.RecordSecurityEvent
	notifyAdmins = func(title string, message string, vars string) {
		utils.WriteNotification(utils.Notification{Recipient: "admin", Title: title, Message: message, Vars: vars, Level: "error"})
	}
)

// recordBlocked writes the incident of a block Cosmos asked for. Its address
// came through Cloudflare's proxy: it is marked as reported, so that it never
// reaches the Fail2ban file.
func recordBlocked(key string, address string, reason string, mode string, until time.Time, zones []string) {
	recordEvent(utils.SecurityEvent{
		Kind:      utils.SecEdgeBlocked,
		IP:        address,
		Target:    key,
		Forwarded: true,
		Details: map[string]string{
			"reason": reason,
			"mode":   mode,
			"until":  until.UTC().Format(time.RFC3339),
			"zone":   strings.Join(zones, " "),
		},
	})
}

// recordLifted writes that Cosmos removed a rule by itself. What an
// administrator lifted is recorded by the route, with who did it.
func recordLifted(r *Rule, why string) {
	switch why {
	case "", "admin", "all", "off", "replaced":
		return
	}
	if r.Reason == "test" {
		return
	}
	recordEvent(utils.SecurityEvent{
		Kind:    utils.SecEdgeUnblocked,
		Target:  r.Key,
		Details: map[string]string{"reason": why, "zone": r.Zone},
	})
}

var notified = struct {
	sync.Mutex
	at map[string]time.Time
}{at: map[string]time.Time{}}

// notifyProblem tells the administrators once a day per zone and cause.
func notifyProblem(zone string, reason string, now time.Time) {
	notified.Lock()
	key := zone + "\x00" + reason
	if now.Sub(notified.at[key]) < 24*time.Hour {
		notified.Unlock()
		return
	}
	notified.at[key] = now
	notified.Unlock()
	message := MessagePermission
	if reason == "invalid_token" {
		message = MessageToken
	}
	go notifyAdmins(TitleProblem, message, zone)
}
