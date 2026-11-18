package watch

// Plan 48, review 5: the watches say the level of their problems to the
// Telegram notifier from their own state (watch.state.json and memory),
// never from the notifications, which the owner may delete. Three answers:
// open, over, or unknown (before the state was read from the file and
// before the watch looked once since the start).

import (
	"strings"
	"sync"

	"github.com/azukaar/cosmos-server/src/utils"
)

var (
	lookedMu sync.Mutex
	looked   = map[string]bool{}
)

// markLooked records that a watch concluded once since the start.
func markLooked(what string) {
	lookedMu.Lock()
	looked[what] = true
	lookedMu.Unlock()
}

func hasLooked(what string) bool {
	lookedMu.Lock()
	defer lookedMu.Unlock()
	return looked[what]
}

// known: the watch's state is evidence: read from the file, or looked at.
func (s *store) known(what string) bool {
	if hasLooked(what) {
		return true
	}
	s.with(func(*stateData) bool { return false })
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loaded
}

func registerLevels(st *store) {
	level := func(what string, open func(d *stateData, thread string) (bool, bool)) utils.ThreadLevel {
		return func(thread string) (bool, bool) {
			if !st.known(what) {
				return false, false
			}
			result, ok := false, false
			st.with(func(d *stateData) bool {
				result, ok = open(d, thread)
				return false
			})
			return result, ok
		}
	}
	list := func(what string, open func(d *stateData) []string) func() ([]string, bool) {
		return func() ([]string, bool) {
			if !st.known(what) {
				return nil, false
			}
			out := []string{}
			st.with(func(d *stateData) bool {
				out = open(d)
				return false
			})
			return out, true
		}
	}

	// S.M.A.R.T.: "smart:<key>" failing, "smart:<key>:unreadable"
	utils.RegisterThreadLevel("smart.failing", level("smart", func(d *stateData, thread string) (bool, bool) {
		key := strings.TrimPrefix(thread, "smart:")
		memo, has := d.SMART[key]
		return has && memo.Open, true // a disk the watch forgot (gone, replaced) is over
	}))
	utils.RegisterOpenThreads("smart.failing", list("smart", func(d *stateData) []string {
		out := []string{}
		for key, memo := range d.SMART {
			if memo.Open {
				out = append(out, smartThread(key))
			}
		}
		return out
	}))
	utils.RegisterThreadLevel("smart.unreadable", level("smart", func(d *stateData, thread string) (bool, bool) {
		key := strings.TrimSuffix(strings.TrimPrefix(thread, "smart:"), ":unreadable")
		memo, has := d.SMART[key]
		return has && memo.Unreadable, true
	}))
	utils.RegisterOpenThreads("smart.unreadable", list("smart", func(d *stateData) []string {
		out := []string{}
		for key, memo := range d.SMART {
			if memo.Unreadable {
				out = append(out, smartThread(key)+":unreadable")
			}
		}
		return out
	}))
	// disks filling: "fill:<mount>"
	utils.RegisterThreadLevel("disk.filling", level("fill", func(d *stateData, thread string) (bool, bool) {
		ring, has := d.Fill[strings.TrimPrefix(thread, "fill:")]
		return has && ring.Open, true
	}))
	utils.RegisterOpenThreads("disk.filling", list("fill", func(d *stateData) []string {
		out := []string{}
		for mount, ring := range d.Fill {
			if ring.Open {
				out = append(out, fillThread(mount))
			}
		}
		return out
	}))
	// Docker: "docker.down"
	utils.RegisterThreadLevel("docker.down", level("docker", func(d *stateData, thread string) (bool, bool) {
		return thread == dockerThread && d.Docker.Open, true
	}))
	utils.RegisterOpenThreads("docker.down", list("docker", func(d *stateData) []string {
		if d.Docker.Open {
			return []string{dockerThread}
		}
		return []string{}
	}))
	// snapshots: "snapshots.late:<source>"
	utils.RegisterThreadLevel("snapshots.late", level("snapshots", func(d *stateData, thread string) (bool, bool) {
		return d.SnapLate[strings.TrimPrefix(thread, "snapshots.late:")], true
	}))
	utils.RegisterOpenThreads("snapshots.late", list("snapshots", func(d *stateData) []string {
		out := []string{}
		for source, late := range d.SnapLate {
			if late {
				out = append(out, "snapshots.late:"+source)
			}
		}
		return out
	}))
}

// registerStuck: whether a watch is stuck is known in memory only, once
// the loop runs: a watch of this run is stuck or not; a name no slot has is
// over (it was a hang of an earlier run of the server).
func registerStuck(slots map[string]*slot) {
	utils.RegisterThreadLevel("watch.stuck", func(thread string) (bool, bool) {
		for _, s := range slots {
			if thread == "watch.stuck:"+s.what {
				s.mu.Lock()
				defer s.mu.Unlock()
				return s.busy && s.told, true
			}
		}
		return false, true
	})
	utils.RegisterOpenThreads("watch.stuck", func() ([]string, bool) {
		out := []string{}
		for _, s := range slots {
			s.mu.Lock()
			if s.busy && s.told {
				out = append(out, "watch.stuck:"+s.what)
			}
			s.mu.Unlock()
		}
		return out, true
	})
}
