package fail2ban

import (
	"net/netip"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Bans feed back into the security record by asking, not by being told:
// once a minute Cosmos runs "fail2ban-client banned" (one process, the same
// fixed command the panel uses) and records the addresses that are banned
// now and were not a minute ago. The other way round, a Fail2ban action
// that writes each ban somewhere Cosmos reads, would be a new way into
// Cosmos to install and to trust; this needs neither.
//
// What it does not see: a ban that came and went within a minute, and the
// bans made while Cosmos was not running (the first look only notes what
// is there).

const (
	watchEvery = time.Minute
	// at most this many bans are recorded from one look; a flood of bans
	// is bounded again by the record's own limits
	maxRecordedPerLook = 100
)

type banKey struct {
	jail    string
	address netip.Addr
}

// newBans compares what is banned now with what was banned at the last
// look and returns what is new, in the order of the fork's jails. A nil
// previous means there was no last look: nothing is new then.
func newBans(previous map[banKey]bool, now map[string][]netip.Addr) (added []banKey, current map[banKey]bool) {
	current = map[banKey]bool{}
	added = []banKey{}
	for _, known := range jails {
		for _, address := range now[known.name] {
			key := banKey{jail: known.name, address: address}
			if current[key] {
				continue
			}
			current[key] = true
			if previous != nil && !previous[key] {
				added = append(added, key)
			}
		}
	}
	return added, current
}

// look is one round of the watcher.
func look(previous map[banKey]bool) map[banKey]bool {
	if !clientInstalled() {
		return nil // nothing to compare with once Fail2ban appears
	}
	banned, err := bannedNow()
	if err != nil {
		// not running, or no answer: keep what was known, so that bans are
		// not recorded a second time when it answers again
		return previous
	}
	added, current := newBans(previous, banned)
	for position, ban := range added {
		if position == maxRecordedPerLook {
			break
		}
		utils.RecordSecurityEvent(utils.SecurityEvent{
			Kind:    utils.SecFail2banBan,
			IP:      ban.address.String(),
			Details: map[string]string{"jail": ban.jail},
		})
	}
	if len(added) > 0 {
		forgetStatus()
	}
	return current
}

var watchOnce sync.Once

// Start begins watching Fail2ban for new bans. It returns at once; on a
// server without Fail2ban the watcher only checks, once a minute, whether
// the client has appeared.
func Start() {
	watchOnce.Do(func() {
		go func() {
			defer func() {
				if failure := recover(); failure != nil {
					utils.Error("Fail2ban: the watcher stopped", nil)
				}
			}()
			var known map[banKey]bool
			for {
				known = look(known)
				time.Sleep(watchEvery)
			}
		}()
	})
}
