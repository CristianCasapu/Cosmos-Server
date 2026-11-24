package watch

// Plan 48, reviews 5 and 6: the watches say the level of their problems to
// the Telegram notifier from their own state (watch.state.json and
// memory), never from the notifications, which the owner may delete.
//
// The rule (review 6): "over" only on evidence; absence is never evidence.
// With the state file read, what it holds is the watch's own conclusion of
// earlier looks; without it (lost, unreadable), a look of this run counts
// only once it concludes by the watch's own rule:
//
//   - S.M.A.R.T. failing: over when the disk's memo is not open and either
//     the state file was read or the disk has read well for calmAfter in
//     this run (the rule that sends "healthy again"); a disk without a memo
//     is unknown.
//   - S.M.A.R.T. unreadable: over when not set and either the state file
//     was read or the last readWindow reads of this run were good.
//   - A disk filling: over when its ring is not open and either the state
//     file was read or the ring has enough samples for a trend; a mount
//     that a listing that worked no longer holds is over; any other mount
//     without a ring is unknown.
//   - Docker: over when not open, once the state file was read or a check
//     of this run concluded (Docker answered, or the problem opened); the
//     watch switched off by the owner is over (docker.unwatched says so).
//   - Snapshots: over when the folder is not late and a snapshot of it is
//     known within its limit, or when the folder is no longer backed up
//     (the owner's settings); unknown otherwise.
//
// Names are compared as the notifications store them (utils.ThreadName).

import (
	"strings"
	"sync"
	"time"

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

// What this run saw itself (under the store's lock).
type smartRunMemo struct {
	first time.Time // the first good read of this run
	reads int       // good reads in this run
}

var (
	smartRun = map[string]smartRunMemo{}
	fillGone = map[string]bool{}
	levelNow = time.Now
	// snapshotLimits: the folders backed up now and how late is late
	snapshotLimits = func() (map[string]bool, time.Duration) {
		cfg := utils.GetMainConfig().Snapshots
		folders := map[string]bool{}
		if cfg.Enabled && strings.TrimSpace(cfg.Destination) != "" {
			for _, source := range cfg.Sources {
				folders[source] = true
			}
		}
		return folders, lateAfter(cfg.Frequency, settingsNow().SnapshotLate)
	}
)

// noteSMARTRead records a good read of a disk. The caller holds the
// store's lock.
func noteSMARTRead(key string, now time.Time) {
	if len(smartRun) >= 4*maxDisks {
		smartRun = map[string]smartRunMemo{}
	}
	r := smartRun[key]
	if r.first.IsZero() {
		r.first = now
	}
	r.reads++
	smartRun[key] = r
}

// noteFillGone: a listing that worked no longer holds the mount. The caller
// holds the store's lock.
func noteFillGone(mount string) {
	if len(fillGone) >= 4*maxMounts {
		fillGone = map[string]bool{}
	}
	fillGone[mount] = true
}

// known: the watch's state is evidence: read from the file, or a look of
// this run concluded.
func (s *store) known(what string) bool {
	if hasLooked(what) {
		return true
	}
	s.with(func(*stateData) bool { return false })
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loaded
}

// matching finds the key whose thread is this one.
func matching[V any](items map[string]V, thread func(string) string, name string) (string, V, bool) {
	for key, value := range items {
		if utils.ThreadName(thread(key)) == name {
			return key, value, true
		}
	}
	var zero V
	return "", zero, false
}

func registerLevels(st *store) {
	// level reads the state (inside with: st.loaded is read under its lock)
	level := func(read func(d *stateData, loaded bool, thread string) (bool, bool)) utils.ThreadLevel {
		return func(thread string) (bool, bool) {
			result, ok := false, false
			st.with(func(d *stateData) bool {
				result, ok = read(d, st.loaded, thread)
				return false
			})
			return result, ok
		}
	}
	list := func(open func(d *stateData) []string) func() ([]string, bool) {
		return func() ([]string, bool) {
			out := []string{}
			st.with(func(d *stateData) bool {
				for _, thread := range open(d) {
					out = append(out, utils.ThreadName(thread))
				}
				return false
			})
			return out, true
		}
	}

	// S.M.A.R.T.: "smart:<key>" failing, "smart:<key>:unreadable"
	utils.RegisterThreadLevel("smart.failing", level(func(d *stateData, loaded bool, thread string) (bool, bool) {
		key, memo, has := matching(d.SMART, smartThread, thread)
		switch {
		case !has:
			return false, false
		case memo.Open:
			return true, true
		case loaded:
			return false, true
		}
		r := smartRun[key]
		calm := r.first
		if memo.LastBad.After(calm) {
			calm = memo.LastBad
		}
		return false, !r.first.IsZero() && levelNow().Sub(calm) >= calmAfter
	}))
	utils.RegisterOpenThreads("smart.failing", list(func(d *stateData) []string {
		out := []string{}
		for key, memo := range d.SMART {
			if memo.Open {
				out = append(out, smartThread(key))
			}
		}
		return out
	}))
	unreadableThread := func(key string) string { return smartThread(key) + ":unreadable" }
	utils.RegisterThreadLevel("smart.unreadable", level(func(d *stateData, loaded bool, thread string) (bool, bool) {
		key, memo, has := matching(d.SMART, unreadableThread, thread)
		switch {
		case !has:
			return false, false
		case memo.Unreadable:
			return true, true
		case loaded:
			return false, true
		}
		return false, smartRun[key].reads >= readWindow && failures(memo.History) == 0
	}))
	utils.RegisterOpenThreads("smart.unreadable", list(func(d *stateData) []string {
		out := []string{}
		for key, memo := range d.SMART {
			if memo.Unreadable {
				out = append(out, unreadableThread(key))
			}
		}
		return out
	}))
	// disks filling: "fill:<mount>"
	utils.RegisterThreadLevel("disk.filling", level(func(d *stateData, loaded bool, thread string) (bool, bool) {
		_, ring, has := matching(d.Fill, fillThread, thread)
		if !has {
			_, gone, _ := matching(fillGone, fillThread, thread)
			return false, gone
		}
		if ring.Open {
			return true, true
		}
		_, _, trendKnown := trend(ring.Samples)
		return false, loaded || trendKnown
	}))
	utils.RegisterOpenThreads("disk.filling", list(func(d *stateData) []string {
		out := []string{}
		for mount, ring := range d.Fill {
			if ring.Open {
				out = append(out, fillThread(mount))
			}
		}
		return out
	}))
	// Docker: "docker.down"
	utils.RegisterThreadLevel("docker.down", func(thread string) (bool, bool) {
		if thread != dockerThread {
			return false, false
		}
		open := false
		st.with(func(d *stateData) bool { open = d.Docker.Open; return false })
		if open {
			return true, true
		}
		return false, st.known("docker")
	})
	utils.RegisterOpenThreads("docker.down", list(func(d *stateData) []string {
		if d.Docker.Open {
			return []string{dockerThread}
		}
		return []string{}
	}))
	// snapshots: "snapshots.late:<folder>"
	snapThread := func(source string) string { return "snapshots.late:" + source }
	utils.RegisterThreadLevel("snapshots.late", func(thread string) (bool, bool) {
		folders, limit := snapshotLimits()
		open, known := false, false
		st.with(func(d *stateData) bool {
			if _, late, has := matching(d.SnapLate, snapThread, thread); has && late {
				open, known = true, true
				return false
			}
			source, _, backedUp := matching(folders, snapThread, thread)
			if !backedUp {
				known = true // no longer backed up: the owner's settings
				return false
			}
			last := d.SnapLast[source]
			known = !last.IsZero() && levelNow().Sub(last) <= limit
			return false
		})
		return open, known
	})
	utils.RegisterOpenThreads("snapshots.late", list(func(d *stateData) []string {
		out := []string{}
		for source, late := range d.SnapLate {
			if late {
				out = append(out, snapThread(source))
			}
		}
		return out
	}))
}

// registerStuck: whether a watch is stuck is known in memory only, once
// the loop runs: a watch of this run is stuck (told), or over when its
// slot is free (its look of this run finished); a look still in progress
// is unknown (review 6); a name no slot has is over (it was a hang of an
// earlier run of the server: the process that hung is gone).
func registerStuck(slots map[string]*slot) {
	utils.RegisterThreadLevel("watch.stuck", func(thread string) (bool, bool) {
		for _, s := range slots {
			if thread == utils.ThreadName("watch.stuck:"+s.what) {
				s.mu.Lock()
				defer s.mu.Unlock()
				if s.busy && !s.told {
					return false, false // a look in progress: nothing concluded
				}
				return s.busy, true
			}
		}
		return false, true
	})
	utils.RegisterOpenThreads("watch.stuck", func() ([]string, bool) {
		out := []string{}
		for _, s := range slots {
			s.mu.Lock()
			if s.busy && s.told {
				out = append(out, utils.ThreadName("watch.stuck:"+s.what))
			}
			s.mu.Unlock()
		}
		return out, true
	})
}
