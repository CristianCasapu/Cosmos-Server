package turnstile

// What is wrong right now, and telling the administrators. Two problems
// exist: Cloudflare could not be asked (ResultUnreachable) and Cloudflare
// refuses the secret key (ResultKeyRefused). Either is noticed by a
// visitor's check or by the test of the secret key that runs at start and
// every six hours; either shows on the settings card and on the home page
// for administrators, and raises one notification (the bell, and Telegram
// through the existing forwarder) per problem and six hours.

import (
	"context"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The notification this package raises (translation keys).
const (
	TitleProblem       = "header.notification.title.turnstileProblem"
	MessageUnreachable = "header.notification.message.turnstileUnreachable"
	MessageKeyRefused  = "header.notification.message.turnstileKeyRefused"
)

const (
	recheckEvery = 6 * time.Hour
	notifyEvery  = 6 * time.Hour
	// a problem nobody saw again for this long is over
	problemForget = time.Hour
)

// Problem is one thing that is wrong, for the card.
type Problem struct {
	Reason string    `json:"reason"`
	Since  time.Time `json:"since"`
	Last   time.Time `json:"last"`
	Count  int       `json:"count"`
}

var (
	problemsMu sync.Mutex
	problems   = map[string]*Problem{}
	notified   = map[string]time.Time{}
)

// replaced by the tests
var notifyAdmins = func(title string, message string, level string) {
	utils.WriteNotification(utils.Notification{Recipient: "admin", Title: title, Message: message, Level: level})
}

func noteProblem(reason string) {
	t := now()
	problemsMu.Lock()
	p := problems[reason]
	if p == nil {
		p = &Problem{Reason: reason, Since: t}
		problems[reason] = p
	}
	p.Last = t
	p.Count++
	tell := t.Sub(notified[reason]) >= notifyEvery
	if tell {
		notified[reason] = t
	}
	problemsMu.Unlock()
	if !tell {
		return
	}
	message := MessageUnreachable
	if reason == ResultKeyRefused {
		message = MessageKeyRefused
	}
	go notifyAdmins(TitleProblem, message, "error")
}

func clearProblem(reason string) {
	problemsMu.Lock()
	delete(problems, reason)
	problemsMu.Unlock()
}

func clearProblems() {
	problemsMu.Lock()
	problems = map[string]*Problem{}
	notified = map[string]time.Time{}
	problemsMu.Unlock()
}

// Problems lists what is wrong now, the oldest first.
func Problems() []Problem {
	t := now()
	problemsMu.Lock()
	defer problemsMu.Unlock()
	out := []Problem{}
	for _, reason := range []string{ResultKeyRefused, ResultUnreachable} {
		if p := problems[reason]; p != nil {
			if t.Sub(p.Last) > problemForget && reason == ResultUnreachable {
				delete(problems, reason)
				continue
			}
			out = append(out, *p)
		}
	}
	return out
}

// checkStored tests the saved secret key, as the watch does.
func checkStored(ctx context.Context) {
	config := settings()
	if config.SecretKey == "" || ConsoleOff() {
		return
	}
	switch Secret(ctx, config.SecretKey) {
	case "ok", "test":
		clearProblem(ResultKeyRefused)
		clearProblem(ResultUnreachable)
	case ResultKeyRefused:
		utils.RecordSecurity(nil, utils.SecTurnstileUnavailable, "", "reason", ResultKeyRefused) // security record
		noteProblem(ResultKeyRefused)
	default:
		utils.RecordSecurity(nil, utils.SecTurnstileUnavailable, "", "reason", ResultUnreachable) // security record
		noteProblem(ResultUnreachable)
	}
}

var startOnce sync.Once

// Start runs the test of the secret key a minute after start and every six
// hours. Nothing is sent while no key is saved.
func Start() {
	startOnce.Do(func() {
		go func() {
			time.Sleep(time.Minute)
			for {
				func() {
					defer func() { recover() }()
					checkStored(context.Background())
				}()
				time.Sleep(recheckEvery)
			}
		}()
	})
}
