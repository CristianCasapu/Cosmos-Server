package certs

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The notices about certificates. All go through the notifications of the
// administrators (which the Telegram notifier forwards), and each is held
// back so that a problem that lasts does not write a notice per attempt.

// The notifications this package raises (translation keys of the panel).
const (
	TitleExpiring    = "header.notification.title.certificateExpiring"
	MessageExpiring  = "header.notification.message.certificateExpiring"
	TitleUntrusted   = "header.notification.title.certificateUntrusted"
	MessageUntrusted = "header.notification.message.certificateUntrusted"
)

const (
	failureNoticeEvery   = 24 * time.Hour
	expiryNoticeEvery    = 24 * time.Hour
	untrustedNoticeEvery = 7 * 24 * time.Hour
	// an address is not reported before its first order had the time to end
	untrustedGrace = time.Hour
)

// notify writes a notification. A variable for the tests.
var notify = utils.WriteNotification

// majorError is upstream's notice of a failed order. A variable for the tests.
var majorError = utils.MajorError

// NoticeFailure is called where upstream raised "couldn't get the TLS
// certificate" after every failed order. The notice is raised when the
// reason is new for this certificate or a day has passed since it was last
// raised; otherwise the failure goes to the log only.
func NoticeFailure(key string, message string, reason error) {
	now := clock()
	known := store.get(key)
	id := "fail:" + known.LastCode
	if last := known.Noticed[id]; last != 0 && now.Sub(time.Unix(last, 0)) < failureNoticeEvery {
		utils.Error(message, reason)
		return
	}
	store.update(key, func(cert *certState) {
		if cert.Noticed == nil {
			cert.Noticed = map[string]int64{}
		}
		cert.Noticed[id] = now.Unix()
	})
	if reason == nil {
		reason = errors.New(FailureSentence(known.LastCode))
	}
	majorError(message, reason)
}

// daysLeft says how long a certificate still lasts, in words a notice can
// carry: "12 days", "1 day", "20 hours", "expired".
func timeLeft(until time.Time, now time.Time) string {
	left := until.Sub(now)
	switch {
	case left <= 0:
		return "expired"
	case left < 48*time.Hour:
		return strconv.Itoa(int(math.Ceil(left.Hours()))) + " hours left"
	}
	return strconv.Itoa(int(left.Hours()/24)) + " days left"
}

// raiseNotices looks at the status and raises what is due: a certificate
// this server orders that is inside its warning window (half of the
// renewal margin gone, or expired), and addresses served directly with a
// certificate browsers do not trust.
func raiseNotices(status Status, now time.Time) {
	for _, cert := range status.Certificates {
		if cert.Kind != "acme" || !cert.Present || !cert.Warn || cert.Running {
			continue
		}
		key := certKey(cert)
		known := store.get(key)
		if last := known.Noticed["expiry"]; last != 0 && now.Sub(time.Unix(last, 0)) < expiryNoticeEvery {
			continue
		}
		store.update(key, func(state *certState) {
			if state.Noticed == nil {
				state.Noticed = map[string]int64{}
			}
			state.Noticed["expiry"] = now.Unix()
		})
		why := "it was not renewed yet"
		if cert.Blocked != "" {
			why = "it cannot be renewed the way the domain is set up"
		} else if cert.Last != nil && !cert.Last.OK {
			why = FailureSentence(cert.Last.Code)
		}
		level := "warning"
		if cert.Expired {
			level = "error"
		}
		notify(utils.Notification{
			Recipient: "admin",
			Title:     TitleExpiring,
			Message:   MessageExpiring,
			Vars:      cert.Domain + " (" + timeLeft(cert.NotAfter, now) + "): " + why,
			Level:     level,
		})
	}

	// addresses served directly whose certificate a browser refuses, where
	// this server is the one that should have got a trusted one
	ordered := map[string]bool{}
	for _, cert := range status.Certificates {
		if cert.Kind == "acme" {
			ordered[cert.Domain] = true
		}
	}
	current := []string{}
	for _, host := range status.Hosts {
		if !host.Shielded && !host.Trusted && ordered[host.Domain] && host.Problem != "plainHTTP" {
			current = append(current, host.Host)
		}
	}
	sort.Strings(current)

	due := []string{}
	raise := false
	store.updateAll(func(data *stateData) {
		if data.FirstSeen == nil {
			data.FirstSeen = map[string]int64{}
		}
		listed := map[string]bool{}
		for _, host := range current {
			listed[host] = true
			if data.FirstSeen[host] == 0 {
				data.FirstSeen[host] = now.Unix()
			}
			if now.Sub(time.Unix(data.FirstSeen[host], 0)) >= untrustedGrace {
				due = append(due, host)
			}
		}
		for host := range data.FirstSeen {
			if !listed[host] {
				delete(data.FirstSeen, host)
			}
		}
		if len(due) == 0 {
			data.Untrusted = nil
			data.UntrustedAt = 0
			return
		}
		changed := strings.Join(due, ",") != strings.Join(data.Untrusted, ",")
		if changed || now.Sub(time.Unix(data.UntrustedAt, 0)) >= untrustedNoticeEvery {
			raise = true
			data.Untrusted = due
			data.UntrustedAt = now.Unix()
		}
	})
	if raise {
		names := due
		more := ""
		if len(names) > 10 {
			more = " and " + strconv.Itoa(len(names)-10) + " more"
			names = names[:10]
		}
		notify(utils.Notification{
			Recipient: "admin",
			Title:     TitleUntrusted,
			Message:   MessageUntrusted,
			Vars:      strings.Join(names, ", ") + more,
			Level:     "warning",
		})
	}
}

// certKey is the key what is remembered about a certificate is kept under.
func certKey(cert Certificate) string {
	if cert.Method == "http" {
		return utils.ForkLocalCertKey(utils.LocalCertZone(utils.GetMainConfig(), cert.Domain))
	}
	return utils.ForkZoneCertKey(cert.Domain)
}
