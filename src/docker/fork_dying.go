package docker

// Fork addition: notices a container that keeps stopping with an error (a
// crash loop under a restart policy) and reports it once, through
// utils.ForkNotice. Docker's events are all there is to it: no container is
// inspected, nothing is polled.
//
// An exit that follows a stop or kill asked for through Docker (by an
// administrator, by Cosmos updating or recreating the container, by the
// lazy-start reaper) is not counted: Docker sends a "kill" event first.

import (
	"strconv"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	dyingWindow    = 10 * time.Minute
	dyingExits     = 3                // this many failed exits within the window is "keeps stopping"
	dyingQuiet     = 6 * time.Hour    // then nothing more about that container for this long
	dyingKillGrace = 60 * time.Second // an exit this soon after a kill was asked for
	dyingTracked   = 256              // containers remembered at most
)

type dyingContainer struct {
	exits    []time.Time
	killedAt time.Time
	toldAt   time.Time
}

var (
	dyingMu         sync.Mutex
	dyingContainers = map[string]*dyingContainer{}
)

// noteContainerEvent looks at one Docker event of a container.
func noteContainerEvent(action string, attributes map[string]string) {
	if action != "die" && action != "kill" {
		return
	}
	name := attributes["name"]
	if name == "" {
		return
	}
	if report, code := containerDied(name, action, attributes["exitCode"], time.Now()); report {
		utils.ForkNotice(utils.NoticeContainerDying, "name", name, "exits", strconv.Itoa(dyingExits), "code", code)
	}
}

// containerDied records a kill or an exit and reports whether this exit is
// the one that makes the container worth a message.
func containerDied(name string, action string, exitCode string, now time.Time) (bool, string) {
	dyingMu.Lock()
	defer dyingMu.Unlock()

	c := dyingContainers[name]
	if c == nil {
		if len(dyingContainers) >= dyingTracked {
			for other, state := range dyingContainers {
				if now.Sub(state.killedAt) > dyingWindow && now.Sub(state.toldAt) > dyingQuiet &&
					(len(state.exits) == 0 || now.Sub(state.exits[len(state.exits)-1]) > dyingWindow) {
					delete(dyingContainers, other)
				}
			}
		}
		if len(dyingContainers) >= dyingTracked {
			return false, ""
		}
		c = &dyingContainer{}
		dyingContainers[name] = c
	}

	if action == "kill" {
		c.killedAt = now
		return false, ""
	}

	// an exit without an error, or one that was asked for
	if exitCode == "" || exitCode == "0" || now.Sub(c.killedAt) < dyingKillGrace {
		return false, ""
	}

	recent := c.exits[:0]
	for _, at := range c.exits {
		if now.Sub(at) < dyingWindow {
			recent = append(recent, at)
		}
	}
	c.exits = append(recent, now)

	if len(c.exits) < dyingExits || now.Sub(c.toldAt) < dyingQuiet {
		return false, ""
	}
	c.toldAt = now
	c.exits = nil
	return true, exitCode
}
